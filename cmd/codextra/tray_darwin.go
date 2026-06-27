//go:build darwin && !cgo

package main

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gabewillen/codextra/internal/accounts"
	systray "github.com/gabewillen/codextra/internal/tray"
)

const trayRefreshInterval = 5 * time.Second
const trayDebugEnv = "CODEXTRA_TRAY_DEBUG"
const codexLogoSVGPath = "codextra-logo.svg"

var trayRunnerMu sync.Mutex
var trayRunner func() error

func registerTrayRunner(run func() error) {
	trayRunnerMu.Lock()
	trayRunner = run
	trayRunnerMu.Unlock()
}

func takeTrayRunner() func() error {
	trayRunnerMu.Lock()
	defer trayRunnerMu.Unlock()
	runner := trayRunner
	trayRunner = nil
	return runner
}

func startTray(ctx context.Context, storePath string, onActivate func(string) error) func() {
	trayLogf("startTray requested, storePath=%s", storePath)

	if os.Getenv("CODEXTRA_NO_TRAY") == "1" {
		trayLogf("tray disabled by CODEXTRA_NO_TRAY=1")
		return func() {}
	}

	sysTray, err := systray.New()
	if err != nil {
		log.Printf("codextra tray initialization: %v", err)
		return func() {}
	}
	trayLogf("created tray instance id=%d", sysTray.ID())
	trayLogf("current pid=%d", os.Getpid())

	icon, err := trayIconPNG()
	if err != nil {
		log.Printf("codextra tray icon: %v", err)
	}
	if icon != nil {
		sysTray.SetTemplateIcon(icon)
	} else {
		log.Printf("codextra tray icon unavailable")
	}
	sysTray.SetTooltip("codextra")
	trayLogf("set tray tooltip")
	sysTray.Show()
	trayLogf("tray shown")

	trayCtx, cancel := context.WithCancel(ctx)
	runnerStopped := make(chan struct{})
	runnerExited := make(chan struct{})
	stopped := make(chan struct{})
	refreshNow := make(chan struct{}, 1)
	var stopOnce sync.Once
	var closeRunnerOnce sync.Once
	var runnerExitOnce sync.Once
	closeRunner := func() {
		closeRunnerOnce.Do(func() { close(runnerStopped) })
	}

	registerTrayRunner(func() error {
		err := sysTray.RunUntil(runnerStopped)
		if err != nil {
			log.Printf("codextra tray event loop: %v", err)
		}
		trayLogf("tray event loop exited")
		// Cocoa cleanup must happen on the same OS thread that drove the
		// event loop. Running it here keeps Remove off background goroutines
		// so stopTray never blocks waiting on the main thread.
		sysTray.Remove()
		runnerExitOnce.Do(func() { close(runnerExited) })
		return err
	})

	// On a forced exit (second signal or shutdown timeout) the deferred
	// stopTray never runs, so ask the event-loop thread to remove the status
	// item and wait briefly for it rather than stranding the menu bar icon.
	registerPreExitCleanup(func() {
		closeRunner()
		select {
		case <-runnerExited:
		case <-time.After(500 * time.Millisecond):
		}
	})

	requestRefresh := func() {
		trayLogf("menu refresh requested")
		select {
		case refreshNow <- struct{}{}:
		default:
		}
	}

	trigger := func() {
		requestRefresh()
	}

	refreshNow <- struct{}{}
	go func() {
		defer close(stopped)

		ticker := time.NewTicker(trayRefreshInterval)
		defer ticker.Stop()
		trayLogf("started tray update ticker interval=%s", trayRefreshInterval)

		updateTrayMenu(sysTray, storePath, trigger, onActivate)
		for {
			select {
			case <-trayCtx.Done():
				trayLogf("tray context canceled; stopping update loop")
				return
			case <-ticker.C:
				trayLogf("tray update ticker tick")
				updateTrayMenu(sysTray, storePath, trigger, onActivate)
			case <-refreshNow:
				trayLogf("tray update requested")
				updateTrayMenu(sysTray, storePath, trigger, onActivate)
			}
		}
	}()

	return func() {
		stopOnce.Do(func() {
			trayLogf("stopping tray and closing event loop")
			closeRunner()
			cancel()
			registerTrayRunner(nil)
			select {
			case <-stopped:
			case <-time.After(500 * time.Millisecond):
				trayLogf("tray update loop timed out during shutdown")
			}
			trayLogf("tray stopped")
		})
	}
}

func updateTrayMenu(tray *systray.SystemTray, storePath string, requestRefresh func(), onActivate func(string) error) {
	trayLogf("updating tray menu")
	now := time.Now()
	snapshot, err := snapshotFromStore(storePath, now)
	if err != nil {
		log.Printf("codextra tray menu: failed to load account snapshot: %v", err)
		menu := systray.NewMenu()
		menu.AddDisabled("No account store: " + err.Error())
		tray.SetMenu(menu)
		return
	}

	all := append([]accounts.Account(nil), snapshot.Accounts...)
	sort.Slice(all, func(i, j int) bool {
		return strings.ToLower(all[i].Alias) < strings.ToLower(all[j].Alias)
	})

	menu := systray.NewMenu()

	// Spotlight the current account at the top, with a fat usage bar.
	if current, ok := findAccount(all, snapshot.CurrentAlias); ok {
		menu.AddDisabled("codextra · " + displayAlias(current.Alias))
		menu.AddDisabled("    " + currentAccountStatLine(current, now))
	} else {
		menu.AddDisabled("codextra · no active account")
	}
	menu.AddSeparator()

	if len(all) == 0 {
		menu.AddDisabled("No accounts")
		tray.SetMenu(menu)
		return
	}

	ready, cooling, needsLogin := groupAccounts(all, now)
	addAccountSection(menu, "Ready", ready, snapshot.CurrentAlias, now, requestRefresh, onActivate)
	addAccountSection(menu, "Cooling down", cooling, snapshot.CurrentAlias, now, requestRefresh, onActivate)
	addAccountSection(menu, "Needs sign-in", needsLogin, snapshot.CurrentAlias, now, requestRefresh, onActivate)

	trayLogf("menu built current=%q active=%q accountCount=%d ready=%d cooling=%d login=%d",
		snapshot.CurrentAlias, snapshot.ActiveAlias, len(all), len(ready), len(cooling), len(needsLogin))
	tray.SetMenu(menu)
}

func addAccountSection(menu *systray.Menu, title string, group []accounts.Account, currentAlias string, now time.Time, requestRefresh func(), onActivate func(string) error) {
	if len(group) == 0 {
		return
	}
	menu.AddDisabled(title)
	for _, account := range group {
		alias := account.Alias
		label := formatAccountMenuLabel(account, currentAlias, now)
		trayLogf("adding account to menu: alias=%s label=%q", alias, label)
		menu.AddCheckbox(label, account.Alias == currentAlias, func() {
			trayLogf("menu activation selected alias=%q", alias)
			if err := onActivate(alias); err != nil {
				log.Printf("codextra tray: activate %q: %v", alias, err)
				return
			}
			requestRefresh()
		})
	}
}

func findAccount(list []accounts.Account, alias string) (accounts.Account, bool) {
	if alias == "" {
		return accounts.Account{}, false
	}
	for _, account := range list {
		if account.Alias == alias {
			return account, true
		}
	}
	return accounts.Account{}, false
}

func groupAccounts(list []accounts.Account, now time.Time) (ready, cooling, needsLogin []accounts.Account) {
	for _, account := range list {
		if strings.TrimSpace(account.AccessToken) == "" {
			needsLogin = append(needsLogin, account)
			continue
		}
		if disabledUntil, _ := soonestDisabledUntil(account.DisabledUntil, now); !disabledUntil.IsZero() {
			cooling = append(cooling, account)
			continue
		}
		ready = append(ready, account)
	}
	return
}

// currentAccountStatLine is the fat status line shown under the spotlighted
// current account: a 16-segment usage bar followed by percent and reset hint.
func currentAccountStatLine(account accounts.Account, now time.Time) string {
	if disabledUntil, reason := soonestDisabledUntil(account.DisabledUntil, now); !disabledUntil.IsZero() {
		return fmt.Sprintf("%s · cools down in %s", reason, humanizeDuration(disabledUntil.Sub(now)))
	}
	bar := usageBar(account.UsagePercent, 16)
	line := fmt.Sprintf("%s  %d%%", bar, account.UsagePercent)
	if account.UsageResetAt > 0 {
		reset := time.Unix(account.UsageResetAt, 0)
		if reset.After(now) {
			line += " · resets in " + humanizeDuration(reset.Sub(now))
		}
	}
	return line
}

func formatAccountMenuLabel(account accounts.Account, currentAlias string, now time.Time) string {
	alias := displayAlias(account.Alias)

	if strings.TrimSpace(account.AccessToken) == "" {
		return fmt.Sprintf("%s  %s", accountGlyph(account, now), alias)
	}
	if disabledUntil, reason := soonestDisabledUntil(account.DisabledUntil, now); !disabledUntil.IsZero() {
		return fmt.Sprintf("%s  %s  ·  %s %s",
			accountGlyph(account, now), alias, reason, humanizeDuration(disabledUntil.Sub(now)))
	}
	return fmt.Sprintf("%s  %s   %s  %d%%",
		accountGlyph(account, now), alias, usageBar(account.UsagePercent, 10), account.UsagePercent)
}

func accountGlyph(account accounts.Account, now time.Time) string {
	if strings.TrimSpace(account.AccessToken) == "" {
		return "○"
	}
	if disabledUntil, _ := soonestDisabledUntil(account.DisabledUntil, now); !disabledUntil.IsZero() {
		return "◐"
	}
	return "●"
}

// usagePartials are the left-aligned block fragments for sub-cell fill, indexed
// by eighths: index 1 is ▏ (1/8) … index 7 is ▉ (7/8). A full cell uses █.
var usagePartials = [8]string{"", "▏", "▎", "▍", "▌", "▋", "▊", "▉"}

// usageBar renders a fixed-width unicode meter. width is the number of cells.
// percent is clamped to [0,100] and resolved to eighth-of-a-cell precision, so
// the fill edge lands close to the true value instead of snapping to whole
// cells. Filled cells use a solid block (█), the leading edge a partial block,
// and the remaining track a light shade (░).
func usageBar(percent, width int) string {
	if width <= 0 {
		return ""
	}
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}

	// Work in eighths of a cell for smooth sub-cell resolution.
	eighths := percent * width * 8 / 100
	if eighths == 0 && percent > 0 {
		eighths = 1 // never show an empty meter for non-zero usage
	}
	full := eighths / 8
	rem := eighths % 8
	if full > width {
		full, rem = width, 0
	}

	var b strings.Builder
	b.Grow(width * 3)
	b.WriteString(strings.Repeat("█", full))
	empty := width - full
	if rem > 0 && empty > 0 {
		b.WriteString(usagePartials[rem])
		empty--
	}
	b.WriteString(strings.Repeat("░", empty))
	return b.String()
}

func displayAlias(alias string) string {
	if alias == "" {
		return "(unnamed)"
	}
	return alias
}

func humanizeDuration(d time.Duration) string {
	if d <= 0 {
		return "now"
	}
	days := int(d / (24 * time.Hour))
	d -= time.Duration(days) * 24 * time.Hour
	hours := int(d / time.Hour)
	d -= time.Duration(hours) * time.Hour
	minutes := int(d / time.Minute)

	switch {
	case days > 0 && hours > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case days > 0:
		return fmt.Sprintf("%dd", days)
	case hours > 0 && minutes > 0:
		return fmt.Sprintf("%dh %dm", hours, minutes)
	case hours > 0:
		return fmt.Sprintf("%dh", hours)
	case minutes > 0:
		return fmt.Sprintf("%dm", minutes)
	default:
		return "<1m"
	}
}

func soonestDisabledUntil(disabled map[string]int64, now time.Time) (time.Time, string) {
	var earliest time.Time
	var reason string
	for label, unix := range disabled {
		if unix <= now.Unix() {
			continue
		}
		limit := time.Unix(unix, 0)
		if earliest.IsZero() || limit.Before(earliest) {
			earliest = limit
			reason = label
		}
	}
	return earliest, reason
}

func snapshotFromStore(storePath string, now time.Time) (accounts.Snapshot, error) {
	store, err := accounts.LoadStore(storePath)
	if err != nil {
		return accounts.Snapshot{}, err
	}
	return store.Snapshot(now)
}

func trayIconPNG() ([]byte, error) {
	const size = 64
	const viewBox = 20.0
	const scale = 3.0
	const offsetX = (size - viewBox*scale) / 2
	const logoOffsetY = (size-viewBox*scale)/2 - 2

	logoPaths, err := loadSVGPaths(codexLogoSVGPath)
	if err != nil {
		return nil, err
	}

	img := image.NewRGBA(image.Rect(0, 0, size, size))
	fg := color.RGBA{R: 0, G: 0, B: 0, A: 255}

	// Draw the exact Codex logo shape from codextra-logo.svg with an even-odd fill rule.
	parsed := 0
	contours := []pathContour{}
	for idx, raw := range logoPaths {
		pathContours, err := parsePathContours(raw, func(x, y float64) point {
			return point{
				x: offsetX + x*scale,
				y: logoOffsetY + y*scale,
			}
		})
		if err != nil {
			return nil, fmt.Errorf("svg path %d parse failed: %w", idx, err)
		}
		parsed += len(pathContours)
		contours = append(contours, pathContours...)
	}
	if parsed == 0 {
		return nil, fmt.Errorf("svg parser produced no contours")
	}
	fillContours(img, contours, fg)
	nonTransparent := 0
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] > 0 {
			nonTransparent++
		}
	}
	if nonTransparent == 0 {
		return nil, fmt.Errorf("svg path rendering produced an empty bitmap")
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func loadSVGPaths(path string) ([]string, error) {
	candidates := []string{
		path,
		filepath.Join("cmd", "codextra", path),
		filepath.Join("..", path),
		filepath.Join("..", "cmd", "codextra", path),
	}
	var lastErr error
	for _, candidate := range candidates {
		blob, err := os.ReadFile(candidate)
		if err == nil {
			trayLogf("tray icon: trying svg candidate=%q", candidate)
			paths, err := loadSVGPathsFromBlob(candidate, blob)
			if err == nil {
				return paths, nil
			}
			lastErr = err
			log.Printf("codextra tray icon: candidate %q parse failed: %v", candidate, err)
			continue
		}
		lastErr = err
	}
	return nil, fmt.Errorf("failed to read/parse svg from candidates %v: %w", candidates, lastErr)
}

type svgPathIndex struct {
	paths       []string
	symbolPaths map[string][]string
	uses        []svgUseRef
}

type svgUseRef struct {
	ownerID  string
	targetID string
	fileRef  string
}

func loadSVGPathsFromBlob(source string, blob []byte) ([]string, error) {
	if len(blob) == 0 {
		return nil, fmt.Errorf("empty svg payload")
	}
	index, err := loadSVGPathIndex(source, blob)
	if err != nil {
		return nil, err
	}
	paths, err := resolveSVGPathsFromIndex(source, index, "", make(map[string]struct{}))
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no path elements found in svg")
	}
	return paths, nil
}

func loadSVGPathIndex(source string, blob []byte) (svgPathIndex, error) {
	index := svgPathIndex{
		symbolPaths: map[string][]string{},
	}
	dec := xml.NewDecoder(bytes.NewReader(blob))
	idStack := []string{}

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return index, err
		}

		switch token := tok.(type) {
		case xml.StartElement:
			ownerID := currentSymbolID(idStack)
			idStack = append(idStack, xmlAttrValue(token.Attr, "id"))

			switch token.Name.Local {
			case "path":
				d := strings.TrimSpace(xmlAttrValue(token.Attr, "d"))
				if d == "" {
					break
				}
				index.paths = append(index.paths, d)
				if ownerID != "" {
					index.symbolPaths[ownerID] = append(index.symbolPaths[ownerID], d)
				}
			case "use":
				ref := xmlAttrValue(token.Attr, "href")
				if ref == "" {
					ref = xmlAttrValueWithSpace(token.Attr, "http://www.w3.org/1999/xlink", "href")
				}
				if ref == "" {
					break
				}
				fileRef, targetID, err := parseSVGUseRef(ref)
				if err != nil {
					log.Printf("codextra tray icon: svg <use> parse failed in %q (%q): %v", source, ref, err)
					break
				}
				index.uses = append(index.uses, svgUseRef{
					ownerID:  ownerID,
					targetID: targetID,
					fileRef:  fileRef,
				})
			}
		case xml.EndElement:
			if len(idStack) == 0 {
				return index, fmt.Errorf("svg parse malformed in %q: unmatched end element %q", source, token.Name.Local)
			}
			idStack = idStack[:len(idStack)-1]
		}
	}

	return index, nil
}

func resolveSVGPathsFromIndex(source string, index svgPathIndex, symbolID string, visited map[string]struct{}) ([]string, error) {
	key := source
	if symbolID != "" {
		key += "#" + symbolID
	}
	if _, ok := visited[key]; ok {
		return nil, nil
	}
	visited[key] = struct{}{}

	if symbolID == "" {
		if len(index.paths) > 0 {
			return index.paths, nil
		}
	} else if paths, ok := index.symbolPaths[symbolID]; ok && len(paths) > 0 {
		return paths, nil
	}

	out := []string{}
	for _, use := range index.uses {
		if use.ownerID != symbolID {
			continue
		}
		paths, err := resolveSVGUse(source, index, use, visited)
		if err != nil {
			return nil, err
		}
		out = append(out, paths...)
	}
	if len(out) == 0 {
		if symbolID == "" {
			return nil, fmt.Errorf("no path elements found in svg")
		}
		return nil, fmt.Errorf("svg symbol %q has no path elements", symbolID)
	}
	return out, nil
}

func resolveSVGUse(source string, index svgPathIndex, use svgUseRef, visited map[string]struct{}) ([]string, error) {
	if use.targetID == "" {
		return nil, fmt.Errorf("svg <use> missing fragment target in %q", source)
	}
	if use.fileRef == "" {
		return resolveSVGPathsFromIndex(source, index, use.targetID, visited)
	}

	symbolPath, err := resolveSVGUseFile(source, use.fileRef)
	if err != nil {
		return nil, err
	}
	blob, err := os.ReadFile(symbolPath)
	if err != nil {
		return nil, err
	}
	symbolIndex, err := loadSVGPathIndex(symbolPath, blob)
	if err != nil {
		return nil, err
	}
	return resolveSVGPathsFromIndex(symbolPath, symbolIndex, use.targetID, visited)
}

func resolveSVGUseFile(source, fileRef string) (string, error) {
	fileRef = strings.TrimSpace(fileRef)
	baseDir := filepath.Dir(source)
	if baseDir == "." {
		baseDir = ""
	}
	candidate := filepath.Join(baseDir, fileRef)
	if strings.Contains(candidate, "://") {
		return "", fmt.Errorf("svg external reference not supported: %q", fileRef)
	}
	if _, err := os.Stat(candidate); err != nil {
		return "", err
	}
	return candidate, nil
}

func parseSVGUseRef(raw string) (fileRef string, targetID string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf("empty use reference")
	}
	hash := strings.IndexByte(raw, '#')
	if hash < 0 || hash == len(raw)-1 {
		return "", "", fmt.Errorf("svg <use> reference missing fragment: %q", raw)
	}
	fileRef = strings.TrimSpace(raw[:hash])
	targetID = strings.TrimSpace(raw[hash+1:])
	if targetID == "" {
		return "", "", fmt.Errorf("svg <use> reference missing symbol id: %q", raw)
	}
	return fileRef, targetID, nil
}

func currentSymbolID(stack []string) string {
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] != "" {
			return stack[i]
		}
	}
	return ""
}

func xmlAttrValue(attrs []xml.Attr, name string) string {
	for _, attr := range attrs {
		if strings.EqualFold(attr.Name.Local, name) {
			if value := strings.TrimSpace(attr.Value); value != "" {
				return value
			}
		}
	}
	return ""
}

func xmlAttrValueWithSpace(attrs []xml.Attr, ns, name string) string {
	for _, attr := range attrs {
		if attr.Name.Space == ns && strings.EqualFold(attr.Name.Local, name) {
			if value := strings.TrimSpace(attr.Value); value != "" {
				return value
			}
		}
	}
	return ""
}

type point struct {
	x, y float64
}

type pathContour struct {
	pts    []point
	closed bool
}

type svgPathParser struct {
	data string
	pos  int
}

func parsePathContours(raw string, transform func(float64, float64) point) ([]pathContour, error) {
	parser := &svgPathParser{data: raw}
	contours := []pathContour{}
	var current pathContour
	var cur point
	var contourStart point

	var lastCmd byte
	var lastCubicCtrl point
	var lastQuadCtrl point
	var hasLastCubic bool
	var hasLastQuad bool

	closeCurrent := func() {
		if len(current.pts) < 2 {
			current = pathContour{}
			return
		}
		current.closed = true
		contours = append(contours, current)
		current = pathContour{}
	}

	startContour := func(p point) {
		if len(current.pts) > 0 {
			closeCurrent()
		}
		cur = p
		contourStart = p
		current = pathContour{pts: []point{transform(p.x, p.y)}}
		hasLastCubic = false
		hasLastQuad = false
	}

	addPoint := func(p point) {
		current.pts = append(current.pts, transform(p.x, p.y))
		cur = p
	}

	reflectPoint := func(a, b point) point {
		return point{x: 2*a.x - b.x, y: 2*a.y - b.y}
	}

	toAbs := func(cmd byte, x, y float64) point {
		switch cmd {
		case 'm', 'l', 'h', 'v', 'c', 'q', 'a', 's', 't':
			return point{x: cur.x + x, y: cur.y + y}
		default:
			return point{x: x, y: y}
		}
	}

	for {
		parser.skipWhitespace()
		if parser.eof() {
			break
		}

		cmd, ok := parser.readCommand()
		if ok {
			lastCmd = cmd
		} else if lastCmd == 0 {
			return nil, fmt.Errorf("svg path missing initial command")
		} else {
			cmd = lastCmd
		}

		switch cmd {
		case 'M', 'm':
			first := true
			nextCmd := byte('L')
			if cmd == 'm' {
				nextCmd = 'l'
			}
			for {
				x, ok, err := parser.readNumber()
				if err != nil {
					return nil, err
				}
				if !ok {
					if first {
						return nil, fmt.Errorf("svg path command M requires coordinates")
					}
					break
				}
				y, ok, err := parser.readNumber()
				if err != nil {
					return nil, err
				}
				if !ok {
					return nil, fmt.Errorf("svg path command M incomplete")
				}
				p := toAbs(cmd, x, y)
				if first {
					startContour(p)
					first = false
				} else {
					addPoint(p)
					hasLastCubic = false
					hasLastQuad = false
				}
				parser.skipWhitespace()
				if !parser.hasMoreNumbers() || parser.nextIsCommand() {
					break
				}
			}
			lastCmd = nextCmd

		case 'L', 'l':
			if len(current.pts) == 0 {
				return nil, fmt.Errorf("svg path command L before move-to")
			}
			for {
				x, ok, err := parser.readNumber()
				if err != nil {
					return nil, err
				}
				if !ok {
					break
				}
				y, ok, err := parser.readNumber()
				if err != nil {
					return nil, err
				}
				if !ok {
					return nil, fmt.Errorf("svg path command L incomplete")
				}
				addPoint(toAbs(cmd, x, y))
				parser.skipWhitespace()
				if !parser.hasMoreNumbers() || parser.nextIsCommand() {
					break
				}
			}
			hasLastCubic = false
			hasLastQuad = false

		case 'H', 'h':
			if len(current.pts) == 0 {
				return nil, fmt.Errorf("svg path command H before move-to")
			}
			for {
				x, ok, err := parser.readNumber()
				if err != nil {
					return nil, err
				}
				if !ok {
					break
				}
				if cmd == 'h' {
					addPoint(point{x: cur.x + x, y: cur.y})
				} else {
					addPoint(point{x: x, y: cur.y})
				}
				parser.skipWhitespace()
				if !parser.hasMoreNumbers() || parser.nextIsCommand() {
					break
				}
			}
			hasLastCubic = false
			hasLastQuad = false

		case 'V', 'v':
			if len(current.pts) == 0 {
				return nil, fmt.Errorf("svg path command V before move-to")
			}
			for {
				y, ok, err := parser.readNumber()
				if err != nil {
					return nil, err
				}
				if !ok {
					break
				}
				if cmd == 'v' {
					addPoint(point{x: cur.x, y: cur.y + y})
				} else {
					addPoint(point{x: cur.x, y: y})
				}
				parser.skipWhitespace()
				if !parser.hasMoreNumbers() || parser.nextIsCommand() {
					break
				}
			}
			hasLastCubic = false
			hasLastQuad = false

		case 'C', 'c':
			if len(current.pts) == 0 {
				return nil, fmt.Errorf("svg path command C before move-to")
			}
			for {
				c1x, ok, err := parser.readNumber()
				if err != nil {
					return nil, err
				}
				if !ok {
					break
				}
				c1y, ok, err := parser.readNumber()
				if err != nil {
					return nil, err
				}
				if !ok {
					return nil, fmt.Errorf("svg path command C incomplete")
				}
				c2x, ok, err := parser.readNumber()
				if err != nil {
					return nil, err
				}
				if !ok {
					return nil, fmt.Errorf("svg path command C incomplete")
				}
				c2y, ok, err := parser.readNumber()
				if err != nil {
					return nil, err
				}
				if !ok {
					return nil, fmt.Errorf("svg path command C incomplete")
				}
				ex, ok, err := parser.readNumber()
				if err != nil {
					return nil, err
				}
				if !ok {
					return nil, fmt.Errorf("svg path command C incomplete")
				}
				ey, ok, err := parser.readNumber()
				if err != nil {
					return nil, err
				}
				if !ok {
					return nil, fmt.Errorf("svg path command C incomplete")
				}
				p1 := toAbs(cmd, c1x, c1y)
				p2 := toAbs(cmd, c2x, c2y)
				end := toAbs(cmd, ex, ey)
				for _, p := range flattenCubic(cur, p1, p2, end) {
					addPoint(p)
				}
				cur = end
				lastCubicCtrl = p2
				hasLastCubic = true
				hasLastQuad = false
				parser.skipWhitespace()
				if !parser.hasMoreNumbers() || parser.nextIsCommand() {
					break
				}
			}

		case 'S', 's':
			if len(current.pts) == 0 {
				return nil, fmt.Errorf("svg path command S before move-to")
			}
			for {
				c2x, ok, err := parser.readNumber()
				if err != nil {
					return nil, err
				}
				if !ok {
					break
				}
				c2y, ok, err := parser.readNumber()
				if err != nil {
					return nil, err
				}
				if !ok {
					return nil, fmt.Errorf("svg path command S incomplete")
				}
				ex, ok, err := parser.readNumber()
				if err != nil {
					return nil, err
				}
				if !ok {
					return nil, fmt.Errorf("svg path command S incomplete")
				}
				ey, ok, err := parser.readNumber()
				if err != nil {
					return nil, err
				}
				if !ok {
					return nil, fmt.Errorf("svg path command S incomplete")
				}
				cp1 := cur
				if hasLastCubic {
					cp1 = reflectPoint(cur, lastCubicCtrl)
				}
				p2 := toAbs(cmd, c2x, c2y)
				end := toAbs(cmd, ex, ey)
				for _, p := range flattenCubic(cur, cp1, p2, end) {
					addPoint(p)
				}
				cur = end
				lastCubicCtrl = p2
				hasLastCubic = true
				hasLastQuad = false
				parser.skipWhitespace()
				if !parser.hasMoreNumbers() || parser.nextIsCommand() {
					break
				}
			}

		case 'Q', 'q':
			if len(current.pts) == 0 {
				return nil, fmt.Errorf("svg path command Q before move-to")
			}
			for {
				cx1, ok, err := parser.readNumber()
				if err != nil {
					return nil, err
				}
				if !ok {
					break
				}
				cy1, ok, err := parser.readNumber()
				if err != nil {
					return nil, err
				}
				if !ok {
					return nil, fmt.Errorf("svg path command Q incomplete")
				}
				ex, ok, err := parser.readNumber()
				if err != nil {
					return nil, err
				}
				if !ok {
					return nil, fmt.Errorf("svg path command Q incomplete")
				}
				ey, ok, err := parser.readNumber()
				if err != nil {
					return nil, err
				}
				if !ok {
					return nil, fmt.Errorf("svg path command Q incomplete")
				}
				cp := toAbs(cmd, cx1, cy1)
				end := toAbs(cmd, ex, ey)
				for _, p := range flattenQuadratic(cur, cp, end) {
					addPoint(p)
				}
				cur = end
				lastQuadCtrl = cp
				hasLastQuad = true
				hasLastCubic = false
				parser.skipWhitespace()
				if !parser.hasMoreNumbers() || parser.nextIsCommand() {
					break
				}
			}

		case 'T', 't':
			if len(current.pts) == 0 {
				return nil, fmt.Errorf("svg path command T before move-to")
			}
			for {
				ex, ok, err := parser.readNumber()
				if err != nil {
					return nil, err
				}
				if !ok {
					break
				}
				ey, ok, err := parser.readNumber()
				if err != nil {
					return nil, err
				}
				if !ok {
					return nil, fmt.Errorf("svg path command T incomplete")
				}
				cp := cur
				if hasLastQuad {
					cp = reflectPoint(cur, lastQuadCtrl)
				}
				end := toAbs(cmd, ex, ey)
				for _, p := range flattenQuadratic(cur, cp, end) {
					addPoint(p)
				}
				cur = end
				lastQuadCtrl = cp
				hasLastQuad = true
				hasLastCubic = false
				parser.skipWhitespace()
				if !parser.hasMoreNumbers() || parser.nextIsCommand() {
					break
				}
			}

		case 'A', 'a':
			if len(current.pts) == 0 {
				return nil, fmt.Errorf("svg path command A before move-to")
			}
			for {
				nums := make([]float64, 0, 7)
				for len(nums) < 7 {
					val, ok, err := parser.readNumber()
					if err != nil {
						return nil, err
					}
					if !ok {
						break
					}
					nums = append(nums, val)
				}
				if len(nums) == 0 {
					break
				}
				if len(nums) < 7 {
					trayLogf("svg path command A malformed: got %d values, skipping", len(nums))
					break
				}
				rx, ry := nums[0], nums[1]
				angle := nums[2]
				largeArcRaw := nums[3]
				sweepRaw := nums[4]
				ex := nums[5]
				ey := nums[6]
				end := toAbs(cmd, ex, ey)
				largeArc := int(math.Round(largeArcRaw))
				sweep := int(math.Round(sweepRaw))
				points := flattenArc(cur, end, rx, ry, angle, largeArc, sweep)
				for _, p := range points[1:] {
					addPoint(p)
				}
				cur = end
				hasLastCubic = false
				hasLastQuad = false
				parser.skipWhitespace()
				if !parser.hasMoreNumbers() || parser.nextIsCommand() {
					break
				}
			}

		case 'Z', 'z':
			if len(current.pts) >= 2 {
				closeCurrent()
			}
			cur = contourStart
			hasLastCubic = false
			hasLastQuad = false
			lastCmd = 'z'

		default:
			return nil, fmt.Errorf("unsupported svg command %q", cmd)
		}
	}

	if len(current.pts) > 0 {
		closeCurrent()
	}
	return contours, nil
}

func (p *svgPathParser) eof() bool {
	return p.pos >= len(p.data)
}

func (p *svgPathParser) skipWhitespace() {
	for !p.eof() {
		c := p.data[p.pos]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == ',' {
			p.pos++
			continue
		}
		return
	}
}

func (p *svgPathParser) readCommand() (byte, bool) {
	p.skipWhitespace()
	if p.eof() {
		return 0, false
	}
	c := p.data[p.pos]
	if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
		p.pos++
		return c, true
	}
	return 0, false
}

func (p *svgPathParser) hasMoreNumbers() bool {
	p.skipWhitespace()
	if p.eof() {
		return false
	}
	c := p.data[p.pos]
	return c == '-' || c == '+' || c == '.' || (c >= '0' && c <= '9')
}

func (p *svgPathParser) nextIsCommand() bool {
	p.skipWhitespace()
	if p.eof() {
		return false
	}
	c := p.data[p.pos]
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

func (p *svgPathParser) readNumber() (float64, bool, error) {
	p.skipWhitespace()
	if p.eof() {
		return 0, false, nil
	}

	start := p.pos
	i := p.pos
	c := p.data[i]
	if !((c >= '0' && c <= '9') || c == '.' || c == '+' || c == '-') {
		return 0, false, nil
	}

	if c == '+' || c == '-' {
		i++
		if i >= len(p.data) {
			return 0, false, nil
		}
		c = p.data[i]
	}

	seenDigit := false
	seenDot := false

	if c == '.' {
		seenDot = true
		i++
		for i < len(p.data) {
			if p.data[i] < '0' || p.data[i] > '9' {
				break
			}
			seenDigit = true
			i++
		}
	} else if c >= '0' && c <= '9' {
		seenDigit = true
		for i < len(p.data) {
			if p.data[i] < '0' || p.data[i] > '9' {
				break
			}
			i++
		}
		if i < len(p.data) && p.data[i] == '.' {
			seenDot = true
			i++
			for i < len(p.data) {
				if p.data[i] < '0' || p.data[i] > '9' {
					break
				}
				i++
			}
		}
	}

	if !seenDigit {
		p.pos = start
		return 0, false, nil
	}

	if i < len(p.data) && (p.data[i] == 'e' || p.data[i] == 'E') {
		i++
		if i < len(p.data) && (p.data[i] == '+' || p.data[i] == '-') {
			i++
		}
		if i >= len(p.data) {
			p.pos = start
			return 0, false, nil
		}
		expHasDigit := false
		for i < len(p.data) {
			if p.data[i] < '0' || p.data[i] > '9' {
				break
			}
			expHasDigit = true
			i++
		}
		if !expHasDigit {
			p.pos = start
			return 0, false, nil
		}
	}

	p.pos = i
	token := p.data[start:i]
	if token == "-" || token == "+" || token == "." || token == "+." || token == "-." {
		return 0, false, nil
	}
	if !seenDot && !seenDigit {
		return 0, false, nil
	}
	value, err := strconv.ParseFloat(token, 64)
	if err != nil {
		p.pos = start
		return 0, false, nil
	}
	return value, true, nil
}

func fillContours(img *image.RGBA, contours []pathContour, fg color.RGBA) {
	segments := make([][4]float64, 0, 1024)
	for _, contour := range contours {
		if len(contour.pts) < 2 {
			continue
		}
		for i := 0; i < len(contour.pts)-1; i++ {
			a := contour.pts[i]
			b := contour.pts[i+1]
			segments = append(segments, [4]float64{a.x, a.y, b.x, b.y})
		}
		if contour.closed {
			a := contour.pts[len(contour.pts)-1]
			b := contour.pts[0]
			segments = append(segments, [4]float64{a.x, a.y, b.x, b.y})
		}
	}

	for y := 0; y < img.Bounds().Dy(); y++ {
		py := float64(y) + 0.5
		for x := 0; x < img.Bounds().Dx(); x++ {
			px := float64(x) + 0.5
			inside := false
			for _, s := range segments {
				y1 := s[1]
				y2 := s[3]
				if y1 == y2 {
					continue
				}
				if (y1 > py) == (y2 > py) {
					continue
				}
				xCross := s[0] + (py-y1)*(s[2]-s[0])/(y2-y1)
				if xCross > px {
					inside = !inside
				}
			}
			if inside {
				img.Set(x, y, fg)
			}
		}
	}
}

func flattenCubic(start, c1, c2, end point) []point {
	const steps = 16
	curve := make([]point, 0, steps)
	for i := 1; i <= steps; i++ {
		t := float64(i) / float64(steps)
		mt := 1 - t
		curve = append(curve, point{
			x: mt*mt*mt*start.x + 3*mt*mt*t*c1.x + 3*mt*t*t*c2.x + t*t*t*end.x,
			y: mt*mt*mt*start.y + 3*mt*mt*t*c1.y + 3*mt*t*t*c2.y + t*t*t*end.y,
		})
	}
	return curve
}

func flattenQuadratic(start, cp, end point) []point {
	const steps = 12
	curve := make([]point, 0, steps)
	for i := 1; i <= steps; i++ {
		t := float64(i) / float64(steps)
		mt := 1 - t
		curve = append(curve, point{
			x: mt*mt*start.x + 2*mt*t*cp.x + t*t*end.x,
			y: mt*mt*start.y + 2*mt*t*cp.y + t*t*end.y,
		})
	}
	return curve
}

func flattenArc(start, end point, rx, ry, phi float64, largeArcFlag, sweepFlag int) []point {
	if start.x == end.x && start.y == end.y {
		return []point{start}
	}
	if rx == 0 || ry == 0 {
		return []point{start, end}
	}

	rx = math.Abs(rx)
	ry = math.Abs(ry)
	if rx == 0 || ry == 0 {
		return []point{start, end}
	}

	angle := phi * math.Pi / 180
	dx := (start.x - end.x) / 2
	dy := (start.y - end.y) / 2
	xp := math.Cos(angle)*dx + math.Sin(angle)*dy
	yp := -math.Sin(angle)*dx + math.Cos(angle)*dy

	lambda := (xp*xp)/(rx*rx) + (yp*yp)/(ry*ry)
	if lambda > 1 {
		scale := math.Sqrt(lambda)
		rx *= scale
		ry *= scale
	}

	sign := 1.0
	if largeArcFlag == sweepFlag {
		sign = -1
	}
	numer := rx*rx*ry*ry - rx*rx*yp*yp - ry*ry*xp*xp
	denom := rx*rx*yp*yp + ry*ry*xp*xp
	coef := 0.0
	if denom > 0 {
		coef = sign * math.Sqrt(math.Max(0, numer/denom))
	}
	cxp := coef * (rx * yp / ry)
	cyp := coef * (-ry * xp / rx)
	cx := math.Cos(angle)*cxp - math.Sin(angle)*cyp + (start.x+end.x)/2
	cy := math.Sin(angle)*cxp + math.Cos(angle)*cyp + (start.y+end.y)/2

	ux := (xp - cxp) / rx
	uy := (yp - cyp) / ry
	vx := (-xp - cxp) / rx
	vy := (-yp - cyp) / ry
	startAngle := math.Atan2(uy, ux)
	delta := math.Atan2(ux*vy-uy*vx, ux*vx+uy*vy)

	if sweepFlag == 0 && delta > 0 {
		delta -= 2 * math.Pi
	}
	if sweepFlag == 1 && delta < 0 {
		delta += 2 * math.Pi
	}

	steps := int(math.Max(1, math.Ceil(math.Abs(delta)/(math.Pi/12))))
	points := make([]point, 0, steps+1)
	for i := 0; i <= steps; i++ {
		theta := startAngle + delta*float64(i)/float64(steps)
		xt := rx * math.Cos(theta)
		yt := ry * math.Sin(theta)
		points = append(points, point{
			x: math.Cos(angle)*xt - math.Sin(angle)*yt + cx,
			y: math.Sin(angle)*xt + math.Cos(angle)*yt + cy,
		})
	}
	return points
}

func trayDebugEnabled() bool {
	env := strings.ToLower(strings.TrimSpace(os.Getenv(trayDebugEnv)))
	switch env {
	case "1", "true", "yes", "on":
		return true
	}
	legacy := strings.ToLower(strings.TrimSpace(os.Getenv("CODEXTRA_DEBUG")))
	return legacy == "1" || legacy == "true" || legacy == "yes" || legacy == "on"
}

func trayLogf(format string, args ...any) {
	if !trayDebugEnabled() {
		return
	}
	log.Printf("codextra tray debug: %s", strings.TrimSpace(fmt.Sprintf(format, args...)))
}
