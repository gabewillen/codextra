//go:build !darwin

package internal

// NewPlatformTray returns a no-op tray for platforms without a native backend.
// codextra only drives the tray on macOS; this stub exists so the package
// compiles (and tests run) on Linux and Windows where the tray is disabled.
func NewPlatformTray(callbacks *Callbacks) PlatformTray {
	return &noopTray{}
}

type noopTray struct{}

func (n *noopTray) Create() error                          { return nil }
func (n *noopTray) SetIcon(png []byte) error               { return nil }
func (n *noopTray) SetTooltip(text string) error           { return nil }
func (n *noopTray) SetMenu(menu *Menu) error               { return nil }
func (n *noopTray) ShowNotification(title, m string) error { return nil }
func (n *noopTray) Show() error                            { return nil }
func (n *noopTray) Hide() error                            { return nil }
func (n *noopTray) Bounds() (x, y, w, h int)               { return 0, 0, 0, 0 }
func (n *noopTray) Run() error                             { return nil }
func (n *noopTray) RunUntil(stop <-chan struct{}) error {
	if stop != nil {
		<-stop
	}
	return nil
}
func (n *noopTray) RunOnMain(fn func()) {
	if fn != nil {
		fn()
	}
}
func (n *noopTray) Destroy() {}
