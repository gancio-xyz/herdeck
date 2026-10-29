// Package tray is herdeck's menu bar icon: live counts in the title, a
// workspace list (click to focus), connection status and a few controls.
package tray

import (
	"fmt"
	"log"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"fyne.io/systray"

	"herdeck/internal/config"
	"herdeck/internal/core"
	"herdeck/internal/model"
	"herdeck/internal/render"
	"herdeck/internal/service"
)

// maxItems is the size of the pre-built workspace item pool. systray menu
// items can't be safely removed while their click goroutines run, so items
// are reused and hidden instead of rebuilt.
const maxItems = 32

type Callbacks struct {
	Focus func(workspaceID string)
	Pause func(paused bool)
	Quit  func()
}

type Tray struct {
	cb Callbacks

	mu    sync.Mutex
	ready bool
	last  *core.Tray
	ids   [maxItems]string

	items  [maxItems]*systray.MenuItem
	more   *systray.MenuItem
	empty  *systray.MenuItem
	herdr  *systray.MenuItem
	deck   *systray.MenuItem
	pause  *systray.MenuItem
	login  *systray.MenuItem
	config *systray.MenuItem
}

// Run starts the menu bar loop and blocks; it must be called from main.
// onReady runs once the tray exists (start the app from there).
func Run(cb Callbacks, onReady func(t *Tray)) {
	t := &Tray{cb: cb}
	systray.Run(func() {
		t.build()
		onReady(t)
	}, func() {})
}

// Quit ends Run.
func Quit() { systray.Quit() }

func (t *Tray) build() {
	icon := render.TrayIcon()
	systray.SetTemplateIcon(icon, icon)
	systray.SetTooltip("herdeck")

	t.empty = systray.AddMenuItem("No agents", "")
	t.empty.Disable()
	for i := range t.items {
		it := systray.AddMenuItem("", "Focus this workspace in herdr")
		it.Hide()
		t.items[i] = it
		go func(i int) {
			for range it.ClickedCh {
				t.mu.Lock()
				id := t.ids[i]
				t.mu.Unlock()
				if id != "" && t.cb.Focus != nil {
					t.cb.Focus(id)
				}
			}
		}(i)
	}
	t.more = systray.AddMenuItem("", "")
	t.more.Disable()
	t.more.Hide()

	systray.AddSeparator()
	t.herdr = systray.AddMenuItem("herdr: …", "")
	t.herdr.Disable()
	t.deck = systray.AddMenuItem("Deck: …", "")
	t.deck.Disable()

	systray.AddSeparator()
	t.pause = systray.AddMenuItemCheckbox("Pause deck", "Blank the deck and ignore presses", false)
	t.login = systray.AddMenuItemCheckbox("Launch at login", "", service.Installed())
	t.config = systray.AddMenuItem("Open config…", config.Path())
	quit := systray.AddMenuItem("Quit herdeck", "")

	go func() {
		for {
			select {
			case <-t.pause.ClickedCh:
				// The checkbox follows the app's state (see Update), so a
				// lost request never leaves it showing the wrong thing.
				t.mu.Lock()
				paused := t.last != nil && t.last.Paused
				t.mu.Unlock()
				if t.cb.Pause != nil {
					t.cb.Pause(!paused)
				}
			case <-t.login.ClickedCh:
				t.toggleLogin()
			case <-t.config.ClickedCh:
				openConfig()
			case <-quit.ClickedCh:
				if t.cb.Quit != nil {
					t.cb.Quit()
				}
				return
			}
		}
	}()

	t.mu.Lock()
	t.ready = true
	last := t.last
	t.mu.Unlock()
	if last != nil {
		t.Update(*last)
	}
}

func (t *Tray) toggleLogin() {
	var err error
	if t.login.Checked() {
		err = service.Remove()
	} else {
		_, err = service.Write()
	}
	if err != nil {
		log.Printf("launch at login: %v", err)
	}
	if service.Installed() {
		t.login.Check()
	} else {
		t.login.Uncheck()
	}
}

func openConfig() {
	p := config.Path()
	if err := config.WriteDefault(p); err != nil {
		log.Printf("open config: %v", err)
		return
	}
	cmd := "xdg-open"
	args := []string{p}
	if runtime.GOOS == "darwin" {
		cmd, args = "open", []string{"-t", p}
	}
	if err := exec.Command(cmd, args...).Start(); err != nil {
		log.Printf("open config: %v", err)
	}
}

var dots = map[model.Status]string{
	model.StatusBlocked: "🟠",
	model.StatusWorking: "🔵",
	model.StatusDone:    "🟢",
	model.StatusIdle:    "⚪️",
	model.StatusUnknown: "⚫️",
}

// Title is the menu bar text for a tray state, e.g. "🟠1 🔵3".
func Title(v core.Tray) string {
	if v.Paused {
		return "⏸"
	}
	if !strings.HasPrefix(v.Herdr, "connected") {
		return "⚠︎"
	}
	var parts []string
	for _, st := range []model.Status{model.StatusBlocked, model.StatusWorking, model.StatusDone} {
		if n := v.Counts[st]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s%d", dots[st], n))
		}
	}
	return strings.Join(parts, " ")
}

// ItemTitle is the menu text for one workspace.
func ItemTitle(it core.TrayItem) string {
	s := dots[it.Status] + "  " + it.Label
	if it.Focused {
		s += "   ◂ focused"
	}
	return s
}

// Update renders a tray state. Safe from any goroutine.
func (t *Tray) Update(v core.Tray) {
	t.mu.Lock()
	t.last = &v
	if !t.ready {
		t.mu.Unlock()
		return
	}
	for i := range t.ids {
		t.ids[i] = ""
		if i < len(v.Items) {
			t.ids[i] = v.Items[i].WorkspaceID
		}
	}
	t.mu.Unlock()

	systray.SetTitle(Title(v))
	if len(v.Items) == 0 {
		t.empty.Show()
	} else {
		t.empty.Hide()
	}
	for i, it := range t.items {
		if i < len(v.Items) {
			it.SetTitle(ItemTitle(v.Items[i]))
			it.Show()
		} else {
			it.Hide()
		}
	}
	if n := len(v.Items) - maxItems; n > 0 {
		t.more.SetTitle(fmt.Sprintf("…and %d more", n))
		t.more.Show()
	} else {
		t.more.Hide()
	}
	if v.Paused {
		t.pause.Check()
	} else {
		t.pause.Uncheck()
	}
	t.herdr.SetTitle("herdr: " + v.Herdr)
	t.deck.SetTitle("Deck: " + v.Deck)
}
