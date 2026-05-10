//go:build darwin && !cgo

package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gabewillen/codextra/internal/accounts"
	"github.com/gogpu/systray"
)

const trayRefreshInterval = 5 * time.Second

func startTray(ctx context.Context, storePath string, onActivate func(string) error) func() {
	if os.Getenv("CODEXTRA_NO_TRAY") == "1" {
		return func() {}
	}

	tray := systray.New()
	icon, err := trayIconPNG()
	if err != nil {
		log.Printf("codextra tray icon: %v", err)
	} else {
		tray.SetTemplateIcon(icon)
	}
	tray.SetTooltip("codextra")
	tray.Show()

	trayCtx, cancel := context.WithCancel(ctx)
	stopped := make(chan struct{})
	refreshNow := make(chan struct{}, 1)
	var stopOnce sync.Once

	requestRefresh := func() {
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

		updateTrayMenu(tray, storePath, trigger, onActivate)
		for {
			select {
			case <-trayCtx.Done():
				return
			case <-ticker.C:
				updateTrayMenu(tray, storePath, trigger, onActivate)
			case <-refreshNow:
				updateTrayMenu(tray, storePath, trigger, onActivate)
			}
		}
	}()

	go func() {
		if err := tray.Run(); err != nil {
			log.Printf("codextra tray event loop: %v", err)
		}
		cancel()
	}()

	return func() {
		stopOnce.Do(func() {
			cancel()
			tray.Remove()
			<-stopped
		})
	}
}

func updateTrayMenu(tray *systray.SystemTray, storePath string, requestRefresh func(), onActivate func(string) error) {
	snapshot, err := snapshotFromStore(storePath, time.Now())
	if err != nil {
		menu := systray.NewMenu()
		menu.Add("No account store: "+err.Error(), func() {})
		tray.SetMenu(menu)
		return
	}

	menu := systray.NewMenu()
	if snapshot.CurrentAlias != "" {
		menu.Add("Current: "+snapshot.CurrentAlias, func() {})
	} else {
		menu.Add("Current: none", func() {})
	}
	if snapshot.ActiveAlias != "" && snapshot.ActiveAlias != snapshot.CurrentAlias {
		menu.Add("Selected: "+snapshot.ActiveAlias, func() {})
	}
	menu.AddSeparator()

	accounts := append([]accounts.Account(nil), snapshot.Accounts...)
	sort.Slice(accounts, func(i, j int) bool {
		return strings.ToLower(accounts[i].Alias) < strings.ToLower(accounts[j].Alias)
	})

	if len(accounts) == 0 {
		menu.Add("No accounts", func() {})
	} else {
		for _, account := range accounts {
			alias := account.Alias
			label := formatAccountMenuLabel(account, snapshot.CurrentAlias)
			menu.AddCheckbox(label, account.Alias == snapshot.CurrentAlias, func() {
				if err := onActivate(alias); err != nil {
					log.Printf("codextra tray: activate %q: %v", alias, err)
					return
				}
				requestRefresh()
			})
		}
	}

	tray.SetMenu(menu)
}

func formatAccountMenuLabel(account accounts.Account, currentAlias string) string {
	status := accountStatusText(account, time.Now())
	if account.Alias == currentAlias {
		status = status + " (current)"
	}
	if account.Alias == "" {
		return "(unnamed) - " + status
	}
	return account.Alias + " - " + status
}

func accountStatusText(account accounts.Account, now time.Time) string {
	if strings.TrimSpace(account.AccessToken) == "" {
		return "missing token"
	}

	disabledUntil, reason := soonestDisabledUntil(account.DisabledUntil, now)
	if !disabledUntil.IsZero() {
		return fmt.Sprintf("limited (%s) until %s", reason, disabledUntil.Format(time.RFC3339))
	}

	return "ready"
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
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	center := float64(size) / 2
	radius := float64(size) * 0.22

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx := float64(x) - center
			dy := float64(y) - center
			distance := math.Hypot(dx, dy)
			if distance <= radius {
				img.Set(x, y, color.RGBA{R: 22, G: 122, B: 248, A: 255})
			}
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
