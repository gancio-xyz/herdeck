package app

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"strings"
	"sync"
	"testing"
	"time"

	"herdeck/internal/config"
	"herdeck/internal/core"
	"herdeck/internal/deck"
	"herdeck/internal/herdr"
	"herdeck/internal/herdr/herdrtest"
	"herdeck/internal/render"
)

const workspacesJSON = `[
 {"workspace_id":"w1","number":1,"label":"myrepo alpha","agent_status":"working","focused":true,"active_tab_id":"w1:t1","pane_count":1,"tab_count":1},
 {"workspace_id":"w2","number":2,"label":"myrepo bravo","agent_status":"blocked","focused":false,"active_tab_id":"w2:t1","pane_count":1,"tab_count":1}
]`

const agentsJSON = `[
 {"agent":"claude","agent_status":"working","pane_id":"w1:p1","terminal_id":"t1","workspace_id":"w1","tab_id":"w1:t1","focused":true,"cwd":"/x","foreground_cwd":"/x","revision":0},
 {"agent":"claude","agent_status":"blocked","pane_id":"w2:p1","terminal_id":"t2","workspace_id":"w2","tab_id":"w2:t1","focused":false,"cwd":"/x","foreground_cwd":"/x","revision":0}
]`

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// dominant reports whether most pixels of img are close to c.
func dominant(img image.Image, c color.RGBA) bool {
	if img == nil {
		return false
	}
	b := img.Bounds()
	near := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			d := func(a uint32, b uint8) int { v := int(a>>8) - int(b); return v * v }
			if d(r, c.R)+d(g, c.G)+d(bl, c.B) < 30*30 {
				near++
			}
		}
	}
	return near*2 > b.Dx()*b.Dy()
}

type trayLog struct {
	mu   sync.Mutex
	last core.Tray
}

func (l *trayLog) set(t core.Tray) { l.mu.Lock(); l.last = t; l.mu.Unlock() }
func (l *trayLog) get() core.Tray  { l.mu.Lock(); defer l.mu.Unlock(); return l.last }

func TestAppEndToEnd(t *testing.T) {
	srv := herdrtest.New(t)
	srv.SetWorkspaces(workspacesJSON)
	srv.SetAgents(agentsJSON)
	var (
		mu      sync.Mutex
		focused []string
	)
	srv.Handle("agent.focus", func(p json.RawMessage) (any, *herdrtest.Error) {
		var params struct{ Target string }
		json.Unmarshal(p, &params)
		mu.Lock()
		focused = append(focused, params.Target)
		mu.Unlock()
		return map[string]any{"type": "ok"}, nil
	})

	fc := deck.NewFakeConnector()
	fd := fc.Connect(nil)
	tl := &trayLog{}
	activated := make(chan struct{}, 4)

	a, err := New(Options{
		Config:   config.Default(),
		Herdr:    herdr.New(srv.Path),
		Deck:     fc,
		OnTray:   tl.set,
		Activate: func(context.Context) error { activated <- struct{}{}; return nil },
		Now:      fc.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()

	theme := render.DefaultTheme()

	// Blocked workspace sorts first and is painted amber; working is blue.
	waitFor(t, "initial paint", func() bool {
		return dominant(fd.Key(0), theme.Blocked) && dominant(fd.Key(1), theme.Working)
	})
	if fd.Brightness() != 70 {
		t.Fatalf("brightness %d", fd.Brightness())
	}
	waitFor(t, "tray state", func() bool {
		tr := tl.get()
		return tr.Herdr == "connected" && len(tr.Items) == 2 && tr.Items[0].WorkspaceID == "w2"
	})

	// Tap the blocked key: focuses its agent and activates the terminal.
	fc.Press(0, 100*time.Millisecond)
	waitFor(t, "focus", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(focused) == 1 && focused[0] == "w2:p1"
	})
	select {
	case <-activated:
	case <-time.After(2 * time.Second):
		t.Fatal("terminal not activated")
	}

	// Tray click goes through the same path.
	a.Send(core.FocusRequestMsg{WorkspaceID: "w1"})
	waitFor(t, "tray focus", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(focused) == 2 && focused[1] == "w1:p1"
	})

	// A live status event repaints within a frame.
	srv.Push("pane.agent_status_changed", map[string]any{
		"pane_id": "w2:p1", "workspace_id": "w2", "agent": "claude", "agent_status": "done",
	})
	waitFor(t, "status event repaint", func() bool {
		return dominant(fd.Key(0), theme.Working) && dominant(fd.Key(1), theme.Done)
	})

	// Unplug and replug: the new deck gets a full repaint.
	fc.Disconnect(nil)
	waitFor(t, "deck status", func() bool { return strings.HasPrefix(tl.get().Deck, "not connected") })
	fd2 := fc.Connect(nil)
	waitFor(t, "repaint after replug", func() bool {
		return fd2.Calls("SetKeyImage") >= core.PageSize && fd2.Calls("SetInfoBar") >= 1 &&
			fd2.Calls("SetTouchColor") >= 2 && fd2.Brightness() == 70
	})

	// herdr goes away and comes back.
	srv.Stop()
	waitFor(t, "herdr offline", func() bool { return strings.HasPrefix(tl.get().Herdr, "not connected") })
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "herdr back", func() bool { return tl.get().Herdr == "connected" })

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}
	if !fd2.Closed() {
		t.Fatal("deck should be closed on shutdown")
	}
}

func TestTerminalFromPS(t *testing.T) {
	ps := `    1     0 /sbin/launchd
  986     1 /System/Applications/Utilities/Terminal.app/Contents/MacOS/Terminal
 1171   986 login
 1180  1171 -zsh
 7370  1180 herdr
 7541  7370 /opt/homebrew/bin/herdr`
	got, err := terminalFromPS(ps)
	if err != nil || got != "Terminal" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := terminalFromPS("    1     0 /sbin/launchd\n  50 1 herdr"); err == nil {
		t.Fatal("want error without an app ancestor")
	}
}

func TestThemeFromConfig(t *testing.T) {
	th, err := Theme(config.Colors{Blocked: "#FF0000"})
	if err != nil || th.Blocked != (color.RGBA{0xff, 0, 0, 0xff}) || th.Working != render.DefaultTheme().Working {
		t.Fatalf("%+v %v", th, err)
	}
	if _, err := Theme(config.Colors{Done: "green"}); err == nil {
		t.Fatal("want parse error")
	}
}
