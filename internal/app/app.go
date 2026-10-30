// Package app wires herdr, the core state machine, the renderer and the deck
// together in a single event loop.
package app

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"log"
	"reflect"
	"strings"
	"time"

	"github.com/gancio-xyz/herdeck/internal/config"
	"github.com/gancio-xyz/herdeck/internal/core"
	"github.com/gancio-xyz/herdeck/internal/deck"
	"github.com/gancio-xyz/herdeck/internal/herdr"
	"github.com/gancio-xyz/herdeck/internal/model"
	"github.com/gancio-xyz/herdeck/internal/render"
)

const (
	tickEvery  = 100 * time.Millisecond
	frameEvery = 33 * time.Millisecond // ~30 fps cap
	cmdTimeout = 5 * time.Second
)

type Options struct {
	Config config.Config
	Herdr  herdr.API
	Deck   deck.Connector
	// Activate brings the terminal to the front after a focus; nil disables.
	Activate func(ctx context.Context) error
	// OnTray receives tray state whenever it changes. Called from the loop
	// goroutine; must not block.
	OnTray func(core.Tray)
	// LogView logs a one-line summary whenever the deck content changes
	// (used by --dry-run).
	LogView bool
	Now     func() time.Time
}

type App struct {
	opts  Options
	theme render.Theme
	model *core.Model
	reqs  chan core.Msg

	dk        deck.Deck
	keys      [core.PageSize]render.Tile
	keysValid [core.PageSize]bool
	bar       *render.InfoBar
	touch     [2]*core.TouchLight
	bright    int
	tray      *core.Tray
	logged    string
	lastPaint time.Time
}

func New(opts Options) (*App, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	theme, err := Theme(opts.Config.Colors)
	if err != nil {
		return nil, err
	}
	return &App{
		opts:   opts,
		theme:  theme,
		model:  core.New(CoreOptions(opts.Config), opts.Now()),
		reqs:   make(chan core.Msg, 16),
		bright: -1,
	}, nil
}

// CoreOptions maps the config onto core options.
func CoreOptions(c config.Config) core.Options {
	o := core.Options{
		Sort:           c.Layout.Sort,
		LongPress:      c.Layout.LongPress(),
		ShowIdle:       c.Layout.ShowIdle,
		ShowTerminals:  c.Layout.ShowTerminals,
		AnimateBlocked: c.Layout.AnimateBlocked,
		Brightness:     c.Deck.Brightness,
		DimAfter:       c.Deck.DimAfter(),
		DimBrightness:  c.Deck.DimBrightness,
		ReplyEnabled:   c.Reply.Enabled,
		ReplyKeys:      map[string]core.ReplyKeys{},
	}
	for name, k := range c.Reply.Agents {
		o.ReplyKeys[name] = core.ReplyKeys{Approve: k.ApproveKeys, Deny: k.DenyKeys}
	}
	return o
}

// Theme builds the render theme from config colors.
func Theme(c config.Colors) (render.Theme, error) {
	th := render.DefaultTheme()
	for _, f := range []struct {
		name string
		hex  string
		dst  *color.RGBA
	}{
		{"working", c.Working, &th.Working},
		{"blocked", c.Blocked, &th.Blocked},
		{"done", c.Done, &th.Done},
		{"idle", c.Idle, &th.Idle},
	} {
		if f.hex == "" {
			continue
		}
		rgba, err := render.ParseHex(f.hex)
		if err != nil {
			return th, fmt.Errorf("config colors.%s: %w", f.name, err)
		}
		*f.dst = rgba
	}
	return th, nil
}

// Send injects a message (from the tray) into the loop, giving up after a
// second if the loop is wedged or gone.
func (a *App) Send(msg core.Msg) {
	select {
	case a.reqs <- msg:
	case <-time.After(time.Second):
		log.Printf("dropped request %T: app not responding", msg)
	}
}

// Run blocks until ctx is done.
func (a *App) Run(ctx context.Context) error {
	// Producers get their own context so the deck can be blanked before the
	// connector closes it.
	prod, stop := context.WithCancel(context.WithoutCancel(ctx))
	defer stop()

	updates := a.opts.Herdr.Watch(prod)
	events := a.opts.Deck.Run(prod)
	results := make(chan core.Msg, 16)
	tick := time.NewTicker(tickEvery)
	defer tick.Stop()

	a.step(core.HerdrMsg{Connected: false}, a.opts.Now())
	a.step(core.DeckMsg{Connected: false}, a.opts.Now())

	for {
		var (
			msg core.Msg
			now = a.opts.Now()
		)
		select {
		case <-ctx.Done():
			a.blank()
			stop()
			// Drain so the producers can shut down cleanly.
			for updates != nil || events != nil {
				select {
				case _, ok := <-updates:
					if !ok {
						updates = nil
					}
				case _, ok := <-events:
					if !ok {
						events = nil
					}
				case <-results:
				}
			}
			return nil
		case u, ok := <-updates:
			if !ok {
				updates = nil
				continue
			}
			msg = herdrMsg(u)
		case ev, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			msg = a.deckEvent(ev)
			if ev.Kind == deck.EventInput && !ev.Input.At.IsZero() {
				now = ev.Input.At
			}
		case <-tick.C:
			msg = core.TickMsg{}
		case msg = <-results:
		case msg = <-a.reqs:
		}
		if msg == nil {
			continue
		}
		for _, c := range a.step(msg, now) {
			go a.exec(prod, c, results)
		}
		// Input repaints immediately (press feedback); everything else is
		// capped at ~30 fps, with ticks catching up.
		_, isTick := msg.(core.TickMsg)
		_, isInput := msg.(core.InputMsg)
		if isTick || isInput || a.opts.Now().Sub(a.lastPaint) >= frameEvery {
			a.paint(a.opts.Now())
		}
	}
}

func (a *App) step(msg core.Msg, now time.Time) []core.Cmd {
	return a.model.Update(now, msg)
}

func herdrMsg(u herdr.Update) core.Msg {
	switch u.Kind {
	case herdr.UpdateSnapshot:
		return core.SnapshotMsg{Snapshot: u.Snapshot}
	case herdr.UpdateStatus:
		return core.StatusMsg{Event: u.Status}
	case herdr.UpdateConnected:
		log.Printf("herdr: connected (version %s, protocol %d)", u.Info.Version, u.Info.Protocol)
		return core.HerdrMsg{Connected: true, Info: u.Info}
	case herdr.UpdateDisconnected:
		log.Printf("herdr: disconnected: %v", u.Err)
		return core.HerdrMsg{Connected: false, Err: u.Err}
	}
	return nil
}

func (a *App) deckEvent(ev deck.Event) core.Msg {
	switch ev.Kind {
	case deck.EventConnected:
		log.Printf("deck: connected (serial %s)", ev.Deck.Serial())
		a.dk = ev.Deck
		a.invalidate()
		return core.DeckMsg{Connected: true}
	case deck.EventDisconnected:
		if a.dk != nil || !errors.Is(ev.Err, deck.ErrNotFound) {
			log.Printf("deck: %v", ev.Err)
		}
		a.dk = nil
		return core.DeckMsg{Connected: false, Err: ev.Err}
	case deck.EventInput:
		kind := map[deck.InputKind]core.InputKind{
			deck.KeyDown: core.KeyDown, deck.KeyUp: core.KeyUp,
			deck.TouchDown: core.TouchDown, deck.TouchUp: core.TouchUp,
		}[ev.Input.Kind]
		return core.InputMsg{Kind: kind, Index: ev.Input.Index}
	}
	return nil
}

func (a *App) exec(parent context.Context, c core.Cmd, results chan<- core.Msg) {
	ctx, cancel := context.WithTimeout(parent, cmdTimeout)
	defer cancel()
	reply := func(m core.Msg) {
		select {
		case results <- m:
		case <-parent.Done():
		}
	}
	var err error
	switch c := c.(type) {
	case core.FocusCmd:
		if c.PaneID != "" {
			err = a.opts.Herdr.FocusAgent(ctx, c.PaneID)
		}
		if c.PaneID == "" || err != nil {
			err = a.opts.Herdr.FocusWorkspace(ctx, c.WorkspaceID)
		}
		if err == nil && a.opts.Activate != nil {
			if aerr := a.opts.Activate(ctx); aerr != nil {
				log.Printf("activate terminal: %v", aerr)
			}
		}
	case core.ReadCmd:
		text, rerr := a.opts.Herdr.Read(ctx, c.PaneID, c.Lines)
		reply(core.ReadResultMsg{PaneID: c.PaneID, Purpose: c.Purpose, Text: text, Err: shortErr(rerr)})
		return
	case core.SendKeysCmd:
		log.Printf("quick reply: sending %v to %s", c.Keys, c.PaneID)
		err = a.opts.Herdr.SendKeys(ctx, c.PaneID, c.Keys)
	}
	if err != nil && parent.Err() == nil {
		reply(core.CmdFailedMsg{Cmd: c, Err: shortErr(err)})
	}
}

// shortErr keeps API error codes readable on the 248 px info bar.
func shortErr(err error) error {
	if err == nil {
		return nil
	}
	var apiErr *herdr.APIError
	if errors.As(err, &apiErr) {
		return errors.New(apiErr.Code)
	}
	return err
}

func (a *App) invalidate() {
	a.keysValid = [core.PageSize]bool{}
	a.bar = nil
	a.touch = [2]*core.TouchLight{}
	a.bright = -1
}

func (a *App) paint(now time.Time) {
	a.lastPaint = now
	v := a.model.View(now)

	if a.tray == nil || !reflect.DeepEqual(*a.tray, v.Tray) {
		t := v.Tray
		a.tray = &t
		if a.opts.OnTray != nil {
			a.opts.OnTray(t)
		}
	}
	if a.opts.LogView {
		a.logView(v)
	}
	if a.dk == nil {
		return
	}
	if a.bright != v.Brightness {
		if a.try(a.dk.SetBrightness(v.Brightness)) {
			a.bright = v.Brightness
		}
	}
	for i, t := range v.Keys {
		if a.keysValid[i] && a.keys[i] == t {
			continue
		}
		if a.try(a.dk.SetKeyImage(i, render.Key(t, a.theme))) {
			a.keys[i], a.keysValid[i] = t, true
		}
	}
	if a.bar == nil || *a.bar != v.Bar {
		if a.try(a.dk.SetInfoBar(render.Bar(v.Bar, a.theme))) {
			b := v.Bar
			a.bar = &b
		}
	}
	for i, l := range v.Touch {
		if a.touch[i] != nil && *a.touch[i] == l {
			continue
		}
		if a.try(a.dk.SetTouchColor(i, a.touchColor(l))) {
			l := l
			a.touch[i] = &l
		}
	}
}

// try logs deck write errors; the connector reports actual unplugs.
func (a *App) try(err error) bool {
	if err != nil {
		log.Printf("deck write: %v", err)
		return false
	}
	return true
}

func (a *App) touchColor(l core.TouchLight) color.Color {
	switch l {
	case core.TouchOn:
		return color.RGBA{0x30, 0x30, 0x30, 0xff}
	case core.TouchAlert:
		return a.theme.Blocked
	case core.TouchDeny:
		return a.theme.Armed
	}
	return color.Black
}

// blank clears the deck on shutdown so stale state isn't left lit.
func (a *App) blank() {
	if a.dk == nil {
		return
	}
	for i := 0; i < core.PageSize; i++ {
		a.dk.SetKeyImage(i, render.Key(render.Tile{Empty: true}, a.theme))
	}
	a.dk.SetInfoBar(render.Bar(render.InfoBar{Line1: "herdeck stopped"}, a.theme))
	for i := 0; i < deck.TouchCount; i++ {
		a.dk.SetTouchColor(i, color.Black)
	}
}

func (a *App) logView(v core.View) {
	var b strings.Builder
	for _, t := range v.Keys {
		if t.Empty {
			continue
		}
		fmt.Fprintf(&b, "[%s %s]", statusGlyph(t.Status), t.Title)
	}
	line := fmt.Sprintf("%s | %s | %s", b.String(), v.Bar.Line1, v.Bar.Line2)
	if line != a.logged {
		a.logged = line
		log.Print(line)
	}
}

func statusGlyph(s model.Status) string {
	switch s {
	case model.StatusBlocked:
		return "BLOCKED"
	case model.StatusWorking:
		return "working"
	case model.StatusDone:
		return "done"
	case model.StatusIdle:
		return "idle"
	}
	return "?"
}
