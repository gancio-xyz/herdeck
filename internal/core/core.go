// Package core is herdeck's state machine: it turns herdr state and deck
// input into a View (what the deck and tray should show) and Cmds (what the
// app should do). No I/O, no goroutines, no wall clock: every call gets `now`.
package core

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"herdeck/internal/model"
	"herdeck/internal/render"
)

// Keys per page on the Neo.
const PageSize = 8

// Options configure the model. Zero durations get defaults in New.
type Options struct {
	Sort           string // state | workspace | stable
	LongPress      time.Duration
	ShowIdle       bool
	AnimateBlocked bool
	Brightness     int
	DimAfter       time.Duration // 0 disables dimming
	DimBrightness  int

	ReplyEnabled bool
	ReplyKeys    map[string]ReplyKeys // by agent name, e.g. "claude"

	OverlayFor   time.Duration // inspect / armed duration (default 8 s)
	VerifyWithin time.Duration // reply must unblock the agent within (default 3 s)
	MessageFor   time.Duration // transient info-bar messages (default 5 s)
	InspectLines int           // lines requested from herdr for inspect (default 40)
}

type ReplyKeys struct {
	Approve []string
	Deny    []string
}

// ---- input messages ----

type Msg interface{ isMsg() }

type InputKind int

const (
	KeyDown InputKind = iota + 1
	KeyUp
	TouchDown
	TouchUp
)

type (
	SnapshotMsg struct{ Snapshot model.Snapshot }
	StatusMsg   struct{ Event model.AgentStatusEvent }
	HerdrMsg    struct {
		Connected bool
		Info      model.ServerInfo
		Err       error
	}
	DeckMsg struct {
		Connected bool
		Err       error
	}
	InputMsg struct {
		Kind  InputKind
		Index int
	}
	// TickMsg lets time-based behaviour (long press, timeouts, dimming,
	// animation) advance. Send it every ~100 ms.
	TickMsg       struct{}
	ReadResultMsg struct {
		PaneID  string
		Purpose ReadPurpose
		Text    string
		Err     error
	}
	CmdFailedMsg struct {
		Cmd Cmd
		Err error
	}
	// FocusRequestMsg comes from the tray menu.
	FocusRequestMsg struct{ WorkspaceID string }
	PauseMsg        struct{ Paused bool }
)

func (SnapshotMsg) isMsg()     {}
func (StatusMsg) isMsg()       {}
func (HerdrMsg) isMsg()        {}
func (DeckMsg) isMsg()         {}
func (InputMsg) isMsg()        {}
func (TickMsg) isMsg()         {}
func (ReadResultMsg) isMsg()   {}
func (CmdFailedMsg) isMsg()    {}
func (FocusRequestMsg) isMsg() {}
func (PauseMsg) isMsg()        {}

// ---- output commands ----

type Cmd interface{ isCmd() }

type ReadPurpose int

const (
	ReadInspect ReadPurpose = iota + 1
	ReadPrompt
)

type (
	// FocusCmd focuses PaneID (falling back to the workspace) and brings the
	// terminal app forward.
	FocusCmd struct{ WorkspaceID, PaneID string }
	ReadCmd  struct {
		PaneID  string
		Lines   int
		Purpose ReadPurpose
	}
	SendKeysCmd struct {
		PaneID  string
		Keys    []string
		Approve bool
	}
)

func (FocusCmd) isCmd()    {}
func (ReadCmd) isCmd()     {}
func (SendKeysCmd) isCmd() {}

// ---- view ----

type TouchLight int

const (
	TouchOff   TouchLight = iota
	TouchOn               // another page exists in this direction
	TouchAlert            // ...and it has a blocked workspace
	TouchDeny             // quick reply armed: touching sends deny keys
)

type View struct {
	Keys       [PageSize]render.Tile
	Bar        render.InfoBar
	Touch      [2]TouchLight // [prev, next]
	Brightness int
	Tray       Tray
}

type Tray struct {
	Counts map[model.Status]int
	Items  []TrayItem // every visible workspace, in display order
	Herdr  string     // human-readable connection status
	Deck   string
	Paused bool
}

type TrayItem struct {
	WorkspaceID string
	Label       string
	Status      model.Status
	Focused     bool
}

// ---- model ----

type press struct {
	down  bool
	wsID  string
	at    time.Time
	fired bool // long press already handled
}

type overlay struct {
	wsID, paneID string
	purpose      ReadPurpose
	line1, line2 string
	shown, until time.Time
}

type armed struct {
	wsID, paneID, terminalID, agent string
	until                           time.Time
}

type verify struct {
	wsID, paneID string
	deadline     time.Time
}

type message struct {
	text  string
	until time.Time
}

type Model struct {
	opts Options

	snap   model.Snapshot
	custom map[string]string // pane id → custom status (events only)
	seen   map[string]int    // workspace id → first-seen sequence
	seq    int

	page     int
	selected string
	presses  [PageSize]press

	overlay *overlay
	armed   *armed
	verify  *verify
	alerts  map[string]time.Time // workspace id → alert flash until
	msg     *message

	lastActivity time.Time
	lastSig      string

	herdrUp  bool
	herdrErr string
	deckUp   bool
	deckErr  string
	paused   bool
}

func New(opts Options, now time.Time) *Model {
	if opts.LongPress == 0 {
		opts.LongPress = 600 * time.Millisecond
	}
	if opts.OverlayFor == 0 {
		opts.OverlayFor = 8 * time.Second
	}
	if opts.VerifyWithin == 0 {
		opts.VerifyWithin = 3 * time.Second
	}
	if opts.MessageFor == 0 {
		opts.MessageFor = 5 * time.Second
	}
	if opts.InspectLines == 0 {
		opts.InspectLines = 40
	}
	if opts.Sort == "" {
		opts.Sort = "state"
	}
	return &Model{
		opts:         opts,
		custom:       map[string]string{},
		seen:         map[string]int{},
		alerts:       map[string]time.Time{},
		lastActivity: now,
	}
}

// Update applies one message and returns the commands to run.
func (m *Model) Update(now time.Time, msg Msg) []Cmd {
	var cmds []Cmd
	switch msg := msg.(type) {
	case SnapshotMsg:
		m.snap = cloneSnapshot(msg.Snapshot)
		m.reapplyCustom()
	case StatusMsg:
		m.applyStatus(msg.Event)
	case HerdrMsg:
		m.herdrUp = msg.Connected
		m.herdrErr = ""
		if msg.Err != nil {
			m.herdrErr = msg.Err.Error()
		}
		if !msg.Connected {
			m.disarm()
			m.overlay = nil
		}
	case DeckMsg:
		m.deckUp = msg.Connected
		m.deckErr = ""
		if msg.Err != nil {
			m.deckErr = msg.Err.Error()
		}
		m.presses = [PageSize]press{}
		m.lastActivity = now
	case InputMsg:
		cmds = m.input(now, msg)
	case TickMsg:
		cmds = m.tick(now)
	case ReadResultMsg:
		m.readResult(now, msg)
	case CmdFailedMsg:
		if sk, ok := msg.Cmd.(SendKeysCmd); ok && m.verify != nil && m.verify.paneID == sk.PaneID {
			m.alerts[m.verify.wsID] = now.Add(m.opts.VerifyWithin)
			m.verify = nil
		}
		m.flash(now, fmt.Sprintf("%s failed: %v", cmdName(msg.Cmd), msg.Err))
	case FocusRequestMsg:
		if ws, ok := m.workspace(msg.WorkspaceID); ok {
			cmds = m.focus(now, ws)
		}
	case PauseMsg:
		m.paused = msg.Paused
		m.disarm()
		m.overlay = nil
		m.lastActivity = now
	}
	m.checkArmed(now)
	m.trackActivity(now)
	return cmds
}

// checkArmed disarms as soon as the armed agent stops being blocked (or its
// pane disappears or is reused), so an approve can never land on a different
// prompt than the one that was armed.
func (m *Model) checkArmed(now time.Time) {
	if m.armed == nil {
		return
	}
	a, ok := m.agent(m.armed.paneID)
	if !ok || a.Status != model.StatusBlocked || a.TerminalID != m.armed.terminalID {
		m.disarm()
		if m.overlay != nil && m.overlay.purpose == ReadPrompt {
			m.overlay = nil
		}
		m.flash(now, "agent no longer blocked — disarmed")
	}
}

func cmdName(c Cmd) string {
	switch c := c.(type) {
	case FocusCmd:
		return "focus"
	case ReadCmd:
		return "read"
	case SendKeysCmd:
		if c.Approve {
			return "approve"
		}
		return "deny"
	}
	return "command"
}

// ---- herdr state ----

func (m *Model) applyStatus(ev model.AgentStatusEvent) {
	m.custom[ev.PaneID] = ev.CustomStatus
	for wi := range m.snap.Workspaces {
		ws := &m.snap.Workspaces[wi]
		for ai := range ws.Agents {
			if a := &ws.Agents[ai]; a.PaneID == ev.PaneID {
				a.Status = ev.Status
				a.CustomStatus = ev.CustomStatus
			}
		}
	}
}

// cloneSnapshot deep-copies a snapshot: the model edits agents in place on
// status events, and the sender may still hold the original.
func cloneSnapshot(s model.Snapshot) model.Snapshot {
	out := model.Snapshot{Workspaces: make([]model.Workspace, len(s.Workspaces))}
	for i, ws := range s.Workspaces {
		ws.Agents = append([]model.Agent(nil), ws.Agents...)
		out.Workspaces[i] = ws
	}
	return out
}

// reapplyCustom carries event-only custom statuses over to a new snapshot.
func (m *Model) reapplyCustom() {
	live := map[string]bool{}
	for wi := range m.snap.Workspaces {
		ws := &m.snap.Workspaces[wi]
		for ai := range ws.Agents {
			a := &ws.Agents[ai]
			live[a.PaneID] = true
			if a.CustomStatus == "" {
				a.CustomStatus = m.custom[a.PaneID]
			}
		}
	}
	for id := range m.custom {
		if !live[id] {
			delete(m.custom, id)
		}
	}
}

// wsStatus is the most urgent status among the workspace's agents.
func wsStatus(ws model.Workspace) model.Status {
	st := model.StatusUnknown
	for _, a := range ws.Agents {
		if a.Status.Rank() < st.Rank() {
			st = a.Status
		}
	}
	return st
}

// urgentAgent picks the agent a press should act on: most urgent, then focused.
func urgentAgent(ws model.Workspace) (model.Agent, bool) {
	if len(ws.Agents) == 0 {
		return model.Agent{}, false
	}
	best := ws.Agents[0]
	for _, a := range ws.Agents[1:] {
		if a.Status.Rank() < best.Status.Rank() || (a.Status == best.Status && a.Focused && !best.Focused) {
			best = a
		}
	}
	return best, true
}

func (m *Model) workspace(id string) (model.Workspace, bool) {
	for _, ws := range m.snap.Workspaces {
		if ws.ID == id && len(ws.Agents) > 0 {
			return ws, true
		}
	}
	return model.Workspace{}, false
}

func (m *Model) agent(paneID string) (model.Agent, bool) {
	for _, ws := range m.snap.Workspaces {
		for _, a := range ws.Agents {
			if a.PaneID == paneID {
				return a, true
			}
		}
	}
	return model.Agent{}, false
}

func (m *Model) agentStatus(paneID string) (model.Status, bool) {
	a, ok := m.agent(paneID)
	return a.Status, ok
}

// visible returns the workspaces shown on the deck, in display order.
func (m *Model) visible() []model.Workspace {
	var out []model.Workspace
	for _, ws := range m.snap.Workspaces {
		if len(ws.Agents) == 0 {
			continue
		}
		if _, ok := m.seen[ws.ID]; !ok {
			m.seq++
			m.seen[ws.ID] = m.seq
		}
		if st := wsStatus(ws); !m.opts.ShowIdle && (st == model.StatusIdle || st == model.StatusUnknown) {
			continue
		}
		out = append(out, ws)
	}
	switch m.opts.Sort {
	case "workspace":
		sort.SliceStable(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	case "stable":
		sort.SliceStable(out, func(i, j int) bool { return m.seen[out[i].ID] < m.seen[out[j].ID] })
	default: // state; ties keep herdr order so keys don't shuffle within a group
		sort.SliceStable(out, func(i, j int) bool {
			ri, rj := wsStatus(out[i]).Rank(), wsStatus(out[j]).Rank()
			if ri != rj {
				return ri < rj
			}
			return out[i].Number < out[j].Number
		})
	}
	return out
}

func pageCount(n int) int {
	if n == 0 {
		return 1
	}
	return (n + PageSize - 1) / PageSize
}

func (m *Model) clampPage(n int) {
	if p := pageCount(n); m.page >= p {
		m.page = p - 1
	}
	if m.page < 0 {
		m.page = 0
	}
}

// slot returns the workspace on key idx of the current page.
func (m *Model) slot(idx int) (model.Workspace, bool) {
	vis := m.visible()
	m.clampPage(len(vis))
	i := m.page*PageSize + idx
	if idx < 0 || idx >= PageSize || i >= len(vis) {
		return model.Workspace{}, false
	}
	return vis[i], true
}

// trackActivity resets the dim timer when any visible status changes.
func (m *Model) trackActivity(now time.Time) {
	var b strings.Builder
	for _, ws := range m.visible() {
		fmt.Fprintf(&b, "%s=%s;", ws.ID, wsStatus(ws))
	}
	if sig := b.String(); sig != m.lastSig {
		m.lastSig = sig
		m.lastActivity = now
	}
}

// ---- input ----

func (m *Model) input(now time.Time, in InputMsg) []Cmd {
	m.lastActivity = now
	if m.paused || !m.herdrUp {
		// Offline the snapshot is stale: acting on it could focus or reply
		// to the wrong thing.
		m.presses = [PageSize]press{}
		return nil
	}
	switch in.Kind {
	case KeyDown:
		if in.Index < 0 || in.Index >= PageSize {
			return nil
		}
		ws, ok := m.slot(in.Index)
		if !ok {
			if m.armed != nil {
				m.disarm()
				m.overlay = nil
				m.flash(now, "quick reply disarmed")
			}
			return nil
		}
		m.presses[in.Index] = press{down: true, wsID: ws.ID, at: now}
	case KeyUp:
		if in.Index < 0 || in.Index >= PageSize {
			return nil
		}
		p := m.presses[in.Index]
		m.presses[in.Index] = press{}
		if !p.down || p.fired {
			return nil
		}
		if now.Sub(p.at) >= m.opts.LongPress { // released before a tick caught it
			return m.longPress(now, p.wsID)
		}
		return m.shortPress(now, p.wsID)
	case TouchDown:
		if m.armed != nil {
			return m.reply(now, false)
		}
		vis := m.visible()
		if in.Index == 0 && m.page > 0 {
			m.page--
		} else if in.Index == 1 && m.page < pageCount(len(vis))-1 {
			m.page++
		}
	}
	return nil
}

func (m *Model) shortPress(now time.Time, wsID string) []Cmd {
	m.overlay = nil
	if m.armed != nil {
		if m.armed.wsID == wsID {
			return m.reply(now, true)
		}
		m.disarm()
		m.flash(now, "quick reply disarmed")
		return nil
	}
	ws, ok := m.workspace(wsID)
	if !ok {
		return nil
	}
	return m.focus(now, ws)
}

func (m *Model) focus(now time.Time, ws model.Workspace) []Cmd {
	m.selected = ws.ID
	a, _ := urgentAgent(ws)
	return []Cmd{FocusCmd{WorkspaceID: ws.ID, PaneID: a.PaneID}}
}

func (m *Model) longPress(now time.Time, wsID string) []Cmd {
	m.overlay = nil
	m.disarm() // re-arming re-reads the prompt; never keep a stale arm
	ws, ok := m.workspace(wsID)
	if !ok {
		return nil
	}
	m.selected = ws.ID
	a, _ := urgentAgent(ws)
	if m.opts.ReplyEnabled && a.Status == model.StatusBlocked {
		if keys, ok := m.opts.ReplyKeys[a.Name]; ok && len(keys.Approve) > 0 {
			m.armed = &armed{wsID: ws.ID, paneID: a.PaneID, terminalID: a.TerminalID, agent: a.Name, until: now.Add(m.opts.OverlayFor)}
			m.overlay = &overlay{wsID: ws.ID, paneID: a.PaneID, purpose: ReadPrompt,
				line1: "ARMED · press = approve", line2: "reading prompt…", until: m.armed.until}
			return []Cmd{ReadCmd{PaneID: a.PaneID, Lines: m.opts.InspectLines, Purpose: ReadPrompt}}
		}
		m.flash(now, fmt.Sprintf("no reply keys configured for %q", a.Name))
	}
	m.overlay = &overlay{wsID: ws.ID, paneID: a.PaneID, purpose: ReadInspect,
		line1: displayTitle(ws.Label, nil), line2: "reading…", until: now.Add(m.opts.OverlayFor)}
	return []Cmd{ReadCmd{PaneID: a.PaneID, Lines: m.opts.InspectLines, Purpose: ReadInspect}}
}

// reply sends approve or deny keys to the armed agent, refusing if it is no
// longer blocked.
func (m *Model) reply(now time.Time, approve bool) []Cmd {
	ar := m.armed
	m.disarm()
	m.overlay = nil
	if a, ok := m.agent(ar.paneID); !ok || a.Status != model.StatusBlocked || a.TerminalID != ar.terminalID {
		m.flash(now, "agent is no longer blocked — nothing sent")
		return nil
	}
	keys := m.opts.ReplyKeys[ar.agent]
	send := keys.Deny
	if approve {
		send = keys.Approve
	}
	if len(send) == 0 {
		m.flash(now, fmt.Sprintf("no deny keys configured for %q", ar.agent))
		return nil
	}
	m.verify = &verify{wsID: ar.wsID, paneID: ar.paneID, deadline: now.Add(m.opts.VerifyWithin)}
	if approve {
		m.flash(now, "approve sent")
	} else {
		m.flash(now, "deny sent")
	}
	return []Cmd{SendKeysCmd{PaneID: ar.paneID, Keys: send, Approve: approve}}
}

func (m *Model) disarm() { m.armed = nil }

func (m *Model) flash(now time.Time, text string) {
	m.msg = &message{text: text, until: now.Add(m.opts.MessageFor)}
}

func (m *Model) tick(now time.Time) []Cmd {
	var cmds []Cmd
	for i := range m.presses {
		p := &m.presses[i]
		if p.down && !p.fired && now.Sub(p.at) >= m.opts.LongPress {
			p.fired = true
			cmds = append(cmds, m.longPress(now, p.wsID)...)
		}
	}
	if m.armed != nil && !now.Before(m.armed.until) {
		m.disarm()
		m.flash(now, "quick reply timed out")
	}
	if m.overlay != nil && !now.Before(m.overlay.until) {
		m.overlay = nil
	}
	if v := m.verify; v != nil {
		st, ok := m.agentStatus(v.paneID)
		switch {
		case !ok || st != model.StatusBlocked:
			m.verify = nil
		case !now.Before(v.deadline):
			m.verify = nil
			m.alerts[v.wsID] = now.Add(m.opts.VerifyWithin)
			m.flash(now, "reply may not have landed")
		}
	}
	for id, until := range m.alerts {
		if !now.Before(until) {
			delete(m.alerts, id)
		}
	}
	if m.msg != nil && !now.Before(m.msg.until) {
		m.msg = nil
	}
	return cmds
}

func (m *Model) readResult(now time.Time, r ReadResultMsg) {
	o := m.overlay
	if o == nil || o.paneID != r.PaneID || o.purpose != r.Purpose {
		return
	}
	if r.Err != nil {
		o.line2 = "read failed: " + r.Err.Error()
		return
	}
	lines := MeaningfulLines(r.Text)
	if r.Purpose == ReadPrompt {
		o.line2 = PromptLine(lines)
	} else {
		o.line1, o.line2 = InspectLines(lines, o.line1)
	}
	// Long lines scroll across the bar; keep the overlay up long enough.
	o.shown = now
	d := m.opts.OverlayFor
	if n := len([]rune(o.line2)) - marqueeWidth; n > 0 {
		d = max(d, marqueeDelay+time.Duration(n)*marqueeStep+2*time.Second)
	}
	d = min(d, 30*time.Second)
	if r.Purpose == ReadPrompt && m.armed != nil {
		m.armed.until = now.Add(d)
	}
	o.until = now.Add(d)
}

// Marquee: lines longer than marqueeWidth runes scroll one rune per
// marqueeStep after marqueeDelay.
const (
	marqueeWidth = 30
	marqueeStep  = 120 * time.Millisecond
	marqueeDelay = time.Second
)

func marquee(s string, elapsed time.Duration) string {
	r := []rune(s)
	if len(r) <= marqueeWidth {
		return s
	}
	off := int((elapsed - marqueeDelay) / marqueeStep)
	off = max(0, min(off, len(r)-marqueeWidth))
	return string(r[off:])
}

// ---- view ----

func (m *Model) View(now time.Time) View {
	vis := m.visible()
	m.clampPage(len(vis))
	var v View

	v.Brightness = m.opts.Brightness
	if m.opts.DimAfter > 0 && now.Sub(m.lastActivity) >= m.opts.DimAfter {
		v.Brightness = m.opts.DimBrightness
	}

	v.Tray = Tray{Counts: map[model.Status]int{}, Paused: m.paused}
	for _, ws := range vis {
		st := wsStatus(ws)
		v.Tray.Counts[st]++
		v.Tray.Items = append(v.Tray.Items, TrayItem{WorkspaceID: ws.ID, Label: ws.Label, Status: st, Focused: ws.Focused})
	}
	v.Tray.Herdr = "connected"
	if !m.herdrUp {
		v.Tray.Herdr = "not connected"
		if m.herdrErr != "" {
			v.Tray.Herdr += ": " + m.herdrErr
		}
	}
	v.Tray.Deck = "connected"
	if !m.deckUp {
		v.Tray.Deck = "not connected"
		if m.deckErr != "" {
			v.Tray.Deck += ": " + m.deckErr
		}
	}

	for i := range v.Keys {
		v.Keys[i] = render.Tile{Empty: true}
	}
	if m.paused {
		v.Brightness = 0
		v.Bar = render.InfoBar{Line1: "herdeck paused"}
		return v
	}
	if !m.herdrUp {
		v.Bar = render.InfoBar{Line1: "herdr not connected", Line2: m.herdrErr, Emphasis: true}
		return v
	}

	labels := make([]string, len(vis))
	for i, ws := range vis {
		labels[i] = ws.Label
	}
	pulse := 0
	if m.opts.AnimateBlocked {
		pulse = 1 + int(now.UnixMilli()/400)%3
	}
	for i := 0; i < PageSize; i++ {
		j := m.page*PageSize + i
		if j >= len(vis) {
			break
		}
		ws := vis[j]
		st := wsStatus(ws)
		a, _ := urgentAgent(ws)
		title, prefix := splitLabel(ws.Label, labels)
		sub := a.CustomStatus
		if sub == "" {
			sub = prefix
		}
		if sub == "" {
			sub = a.Name
		}
		t := render.Tile{Title: title, Subtitle: sub, Status: st, Focused: ws.Focused, Count: len(ws.Agents)}
		if st == model.StatusBlocked {
			t.Pulse = pulse
		}
		if m.armed != nil && m.armed.wsID == ws.ID {
			t.Armed = true
		}
		if _, ok := m.alerts[ws.ID]; ok {
			t.Alert = true
		}
		v.Keys[i] = t
	}

	pages := pageCount(len(vis))
	if m.armed != nil {
		v.Touch = [2]TouchLight{TouchDeny, TouchDeny}
	} else {
		v.Touch[0] = m.touchLight(vis, 0, m.page*PageSize, m.page > 0)
		v.Touch[1] = m.touchLight(vis, (m.page+1)*PageSize, len(vis), m.page < pages-1)
	}

	v.Bar = m.bar(now, vis, pages)
	return v
}

func (m *Model) touchLight(vis []model.Workspace, from, to int, exists bool) TouchLight {
	if !exists {
		return TouchOff
	}
	for _, ws := range vis[from:min(to, len(vis))] {
		if wsStatus(ws) == model.StatusBlocked {
			return TouchAlert
		}
	}
	return TouchOn
}

func (m *Model) bar(now time.Time, vis []model.Workspace, pages int) render.InfoBar {
	if o := m.overlay; o != nil {
		line2 := o.line2
		if !o.shown.IsZero() {
			line2 = marquee(line2, now.Sub(o.shown))
		}
		return render.InfoBar{Line1: o.line1, Line2: line2, Emphasis: o.purpose == ReadPrompt}
	}
	counts := map[model.Status]int{}
	for _, ws := range vis {
		counts[wsStatus(ws)]++
	}
	var parts []string
	for _, st := range []model.Status{model.StatusBlocked, model.StatusWorking, model.StatusDone} {
		if counts[st] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[st], st))
		}
	}
	if len(parts) == 0 {
		if len(vis) == 0 {
			parts = []string{"no agents"}
		} else {
			parts = []string{fmt.Sprintf("%d idle", len(vis))}
		}
	}
	line1 := strings.Join(parts, " · ")
	if pages > 1 {
		line1 += fmt.Sprintf("  %d/%d", m.page+1, pages)
	}
	b := render.InfoBar{Line1: line1, Emphasis: counts[model.StatusBlocked] > 0}
	if m.msg != nil {
		b.Line2 = m.msg.text
		return b
	}
	if ws, ok := m.detailWorkspace(vis); ok {
		a, _ := urgentAgent(ws)
		detail := string(wsStatus(ws))
		if a.CustomStatus != "" {
			detail = a.CustomStatus
		}
		b.Line2 = ws.Label + " — " + detail
	}
	return b
}

// detailWorkspace picks the info bar's second-line subject: the last selected
// workspace, else the first blocked one, else the focused one.
func (m *Model) detailWorkspace(vis []model.Workspace) (model.Workspace, bool) {
	for _, ws := range vis {
		if ws.ID == m.selected {
			return ws, true
		}
	}
	for _, ws := range vis {
		if wsStatus(ws) == model.StatusBlocked {
			return ws, true
		}
	}
	for _, ws := range vis {
		if ws.Focused {
			return ws, true
		}
	}
	return model.Workspace{}, false
}
