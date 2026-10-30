package core

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"herdeck/internal/model"
)

var t0 = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

func ws(id string, n int, label string, statuses ...model.Status) model.Workspace {
	w := model.Workspace{ID: id, Number: n, Label: label}
	for i, st := range statuses {
		w.Agents = append(w.Agents, model.Agent{
			PaneID: fmt.Sprintf("%s:p%d", id, i+1), WorkspaceID: id, Name: "claude", Status: st,
		})
	}
	return w
}

// harness drives a Model with an explicit clock.
type harness struct {
	t   *testing.T
	m   *Model
	now time.Time
}

func newHarness(t *testing.T, opts Options, workspaces ...model.Workspace) *harness {
	h := &harness{t: t, now: t0}
	h.m = New(opts, t0)
	h.m.Update(h.now, HerdrMsg{Connected: true})
	h.m.Update(h.now, DeckMsg{Connected: true})
	h.snap(workspaces...)
	return h
}

func (h *harness) snap(workspaces ...model.Workspace) {
	h.m.Update(h.now, SnapshotMsg{Snapshot: model.Snapshot{Workspaces: workspaces}})
}

func (h *harness) advance(d time.Duration) []Cmd {
	var cmds []Cmd
	for end := h.now.Add(d); h.now.Before(end); {
		step := min(100*time.Millisecond, end.Sub(h.now))
		h.now = h.now.Add(step)
		cmds = append(cmds, h.m.Update(h.now, TickMsg{})...)
	}
	return cmds
}

func (h *harness) send(msg Msg) []Cmd { return h.m.Update(h.now, msg) }

// press holds key idx for hold, ticking in between like the app does.
func (h *harness) press(idx int, hold time.Duration) []Cmd {
	cmds := h.send(InputMsg{Kind: KeyDown, Index: idx})
	cmds = append(cmds, h.advance(hold)...)
	return append(cmds, h.send(InputMsg{Kind: KeyUp, Index: idx})...)
}

func (h *harness) touch(idx int) []Cmd {
	cmds := h.send(InputMsg{Kind: TouchDown, Index: idx})
	return append(cmds, h.send(InputMsg{Kind: TouchUp, Index: idx})...)
}

func (h *harness) view() View { return h.m.View(h.now) }

func (h *harness) titles() []string {
	var out []string
	for _, k := range h.view().Keys {
		if !k.Empty {
			out = append(out, k.Title)
		}
	}
	return out
}

func defaultOpts() Options {
	return Options{Sort: "state", ShowIdle: true, Brightness: 70, DimBrightness: 20}
}

func replyOpts() Options {
	o := defaultOpts()
	o.ReplyEnabled = true
	o.ReplyKeys = map[string]ReplyKeys{"claude": {Approve: []string{"Enter"}, Deny: []string{"Escape"}}}
	return o
}

func TestSorting(t *testing.T) {
	spaces := []model.Workspace{
		ws("a", 1, "alpha", model.StatusIdle),
		ws("b", 2, "bravo", model.StatusWorking),
		ws("c", 3, "charlie", model.StatusBlocked),
		ws("d", 4, "delta", model.StatusDone),
		ws("e", 5, "echo", model.StatusWorking),
		{ID: "x", Number: 6, Label: "no agents"},
	}
	for _, tc := range []struct {
		sort string
		want []string
	}{
		{"state", []string{"charlie", "bravo", "echo", "delta", "alpha"}},
		{"workspace", []string{"alpha", "bravo", "charlie", "delta", "echo"}},
		{"stable", []string{"alpha", "bravo", "charlie", "delta", "echo"}},
	} {
		o := defaultOpts()
		o.Sort = tc.sort
		h := newHarness(t, o, spaces...)
		if got := h.titles(); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("sort=%s: got %v want %v", tc.sort, got, tc.want)
		}
	}

	o := defaultOpts()
	o.ShowIdle = false
	h := newHarness(t, o, spaces...)
	if got := h.titles(); len(got) != 4 || got[3] != "delta" {
		t.Errorf("show_idle=false: %v", got)
	}
}

func TestStableSortKeepsFirstSeenOrder(t *testing.T) {
	o := defaultOpts()
	o.Sort = "stable"
	h := newHarness(t, o, ws("b", 2, "bravo", model.StatusIdle))
	h.snap(ws("a", 1, "alpha", model.StatusIdle), ws("b", 2, "bravo", model.StatusIdle))
	if got := h.titles(); !reflect.DeepEqual(got, []string{"bravo", "alpha"}) {
		t.Fatalf("got %v", got)
	}
}

func TestWorkspaceStatusIsMostUrgentAgent(t *testing.T) {
	h := newHarness(t, defaultOpts(), ws("a", 1, "alpha", model.StatusIdle, model.StatusBlocked, model.StatusWorking))
	k := h.view().Keys[0]
	if k.Status != model.StatusBlocked || k.Count != 3 {
		t.Fatalf("tile: %+v", k)
	}
}

func TestLabelsAndSubtitles(t *testing.T) {
	h := newHarness(t, defaultOpts(),
		ws("a", 1, "myrepo TICKET-320", model.StatusIdle),
		ws("b", 2, "myrepo 1.0.6", model.StatusIdle),
		ws("c", 3, "myrepo", model.StatusIdle),
		ws("d", 4, "herdeck", model.StatusIdle),
		ws("e", 5, "solo thing", model.StatusIdle),
	)
	keys := h.view().Keys
	for i, want := range [][2]string{
		{"TICKET-320", "myrepo"}, {"1.0.6", "myrepo"}, {"myrepo", "claude"}, {"herdeck", "claude"}, {"solo thing", "claude"},
	} {
		if keys[i].Title != want[0] || keys[i].Subtitle != want[1] {
			t.Errorf("key %d: got %q/%q want %q/%q", i, keys[i].Title, keys[i].Subtitle, want[0], want[1])
		}
	}

	// Custom status from an event wins as subtitle, and survives the next snapshot.
	h.send(StatusMsg{Event: model.AgentStatusEvent{PaneID: "a:p1", WorkspaceID: "a", Agent: "claude", Status: model.StatusWorking, CustomStatus: "running tests"}})
	h.snap(ws("a", 1, "myrepo TICKET-320", model.StatusWorking), ws("b", 2, "myrepo 1.0.6", model.StatusIdle))
	if k := h.view().Keys[0]; k.Subtitle != "running tests" || k.Status != model.StatusWorking {
		t.Errorf("custom status: %+v", k)
	}
}

func TestStatusEventUpdatesImmediately(t *testing.T) {
	h := newHarness(t, defaultOpts(), ws("a", 1, "alpha", model.StatusWorking), ws("b", 2, "bravo", model.StatusWorking))
	h.send(StatusMsg{Event: model.AgentStatusEvent{PaneID: "b:p1", WorkspaceID: "b", Agent: "claude", Status: model.StatusBlocked}})
	v := h.view()
	if v.Keys[0].Title != "bravo" || v.Keys[0].Status != model.StatusBlocked {
		t.Fatalf("blocked should sort first: %+v", v.Keys[0])
	}
	if !v.Bar.Emphasis || !strings.HasPrefix(v.Bar.Line1, "1 blocked · 1 working") {
		t.Fatalf("bar: %+v", v.Bar)
	}
	if !strings.Contains(v.Bar.Line2, "bravo") {
		t.Fatalf("bar detail should name the blocked workspace: %+v", v.Bar)
	}
}

func TestPagingAndTouchLights(t *testing.T) {
	var spaces []model.Workspace
	for i := 0; i < 10; i++ {
		spaces = append(spaces, ws(fmt.Sprintf("w%d", i), i+1, fmt.Sprintf("ws%d", i), model.StatusWorking))
	}
	spaces[9].Agents[0].Status = model.StatusDone
	o := defaultOpts()
	o.Sort = "workspace"
	h := newHarness(t, o, spaces...)

	v := h.view()
	if v.Touch != [2]TouchLight{TouchOff, TouchOn} || !strings.HasSuffix(v.Bar.Line1, "1/2") {
		t.Fatalf("page 1: touch=%v bar=%q", v.Touch, v.Bar.Line1)
	}
	h.touch(1)
	if got := h.titles(); !reflect.DeepEqual(got, []string{"ws8", "ws9"}) {
		t.Fatalf("page 2: %v", got)
	}
	h.touch(1) // no page 3
	if v := h.view(); v.Touch != [2]TouchLight{TouchOn, TouchOff} {
		t.Fatalf("page 2 touch: %v", v.Touch)
	}

	// A blocked workspace on the other page turns that touch point amber.
	h.send(StatusMsg{Event: model.AgentStatusEvent{PaneID: "w1:p1", Status: model.StatusBlocked}})
	if v := h.view(); v.Touch[0] != TouchAlert {
		t.Fatalf("want alert on prev: %v", v.Touch)
	}
	h.touch(0)
	if got := h.titles(); len(got) != 8 {
		t.Fatalf("back to page 1: %v", got)
	}

	// Shrinking the list clamps the page.
	h.touch(1)
	h.snap(spaces[:3]...)
	if got := h.titles(); len(got) != 3 {
		t.Fatalf("clamped: %v", got)
	}
}

func TestShortPressFocusesMostUrgentAgent(t *testing.T) {
	h := newHarness(t, defaultOpts(), ws("a", 1, "alpha", model.StatusIdle, model.StatusBlocked))
	cmds := h.press(0, 100*time.Millisecond)
	want := []Cmd{FocusCmd{WorkspaceID: "a", PaneID: "a:p2"}}
	if !reflect.DeepEqual(cmds, want) {
		t.Fatalf("got %#v", cmds)
	}
	if got := h.press(5, 100*time.Millisecond); got != nil {
		t.Fatalf("empty slot should do nothing: %#v", got)
	}
	if got := h.send(FocusRequestMsg{WorkspaceID: "a"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("tray focus: %#v", got)
	}
}

func TestPressTargetsWorkspaceUnderKeyAtKeyDown(t *testing.T) {
	h := newHarness(t, defaultOpts(), ws("a", 1, "alpha", model.StatusWorking), ws("b", 2, "bravo", model.StatusWorking))
	h.send(InputMsg{Kind: KeyDown, Index: 0})
	// bravo becomes blocked and jumps to slot 0 while the key is held.
	h.send(StatusMsg{Event: model.AgentStatusEvent{PaneID: "b:p1", Status: model.StatusBlocked}})
	cmds := h.send(InputMsg{Kind: KeyUp, Index: 0})
	if !reflect.DeepEqual(cmds, []Cmd{FocusCmd{WorkspaceID: "a", PaneID: "a:p1"}}) {
		t.Fatalf("got %#v", cmds)
	}
}

func TestLongPressInspect(t *testing.T) {
	h := newHarness(t, defaultOpts(), ws("a", 1, "alpha", model.StatusWorking))
	cmds := h.press(0, 700*time.Millisecond)
	want := []Cmd{ReadCmd{PaneID: "a:p1", Lines: 40, Purpose: ReadInspect}}
	if !reflect.DeepEqual(cmds, want) {
		t.Fatalf("got %#v", cmds)
	}
	h.send(ReadResultMsg{PaneID: "a:p1", Purpose: ReadInspect, Text: "building\n\n  step 2 of 3\n────────\n❯ \n────────\n"})
	if b := h.view().Bar; b.Line1 != "building" || b.Line2 != "step 2 of 3" {
		t.Fatalf("bar: %+v", b)
	}
	h.advance(9 * time.Second)
	if b := h.view().Bar; b.Line1 == "building" {
		t.Fatalf("overlay should expire: %+v", b)
	}

	// Another press dismisses it early.
	h.press(0, 700*time.Millisecond)
	h.send(ReadResultMsg{PaneID: "a:p1", Purpose: ReadInspect, Text: "x\ny"})
	h.press(0, 50*time.Millisecond)
	if b := h.view().Bar; b.Line2 == "y" {
		t.Fatalf("press should dismiss overlay: %+v", b)
	}
}

func TestLongPressWithoutTicks(t *testing.T) {
	// A release after the threshold counts as long even if no tick fired.
	h := newHarness(t, defaultOpts(), ws("a", 1, "alpha", model.StatusWorking))
	h.send(InputMsg{Kind: KeyDown, Index: 0})
	h.now = h.now.Add(time.Second)
	cmds := h.send(InputMsg{Kind: KeyUp, Index: 0})
	if len(cmds) != 1 || cmds[0].(ReadCmd).Purpose != ReadInspect {
		t.Fatalf("got %#v", cmds)
	}
}

func TestReplyDisabledLongPressBlockedInspects(t *testing.T) {
	h := newHarness(t, defaultOpts(), ws("a", 1, "alpha", model.StatusBlocked))
	cmds := h.press(0, 700*time.Millisecond)
	if cmds[0].(ReadCmd).Purpose != ReadInspect || h.view().Keys[0].Armed {
		t.Fatalf("reply off must never arm: %#v", cmds)
	}
}

func arm(t *testing.T, h *harness) {
	t.Helper()
	cmds := h.press(0, 700*time.Millisecond)
	if !reflect.DeepEqual(cmds, []Cmd{ReadCmd{PaneID: "a:p1", Lines: 40, Purpose: ReadPrompt}}) {
		t.Fatalf("arm: %#v", cmds)
	}
	v := h.view()
	if !v.Keys[0].Armed || v.Touch != [2]TouchLight{TouchDeny, TouchDeny} {
		t.Fatalf("not armed: %+v %v", v.Keys[0], v.Touch)
	}
}

func TestReplyArmedApprove(t *testing.T) {
	h := newHarness(t, replyOpts(), ws("a", 1, "alpha", model.StatusBlocked))
	arm(t, h)
	h.send(ReadResultMsg{PaneID: "a:p1", Purpose: ReadPrompt, Text: "Edit file\nDo you want to make this edit to main.go?\n❯ 1. Yes\n  2. No\n"})
	if b := h.view().Bar; b.Line2 != "Do you want to make this edit to main.go?" || !b.Emphasis {
		t.Fatalf("prompt bar: %+v", b)
	}
	cmds := h.press(0, 100*time.Millisecond)
	if !reflect.DeepEqual(cmds, []Cmd{SendKeysCmd{PaneID: "a:p1", Keys: []string{"Enter"}, Approve: true}}) {
		t.Fatalf("approve: %#v", cmds)
	}
	if h.view().Keys[0].Armed {
		t.Fatal("should disarm after send")
	}
	// The agent leaves blocked within 3 s: no alert.
	h.advance(time.Second)
	h.send(StatusMsg{Event: model.AgentStatusEvent{PaneID: "a:p1", Status: model.StatusWorking}})
	h.advance(3 * time.Second)
	if h.view().Keys[0].Alert {
		t.Fatal("unexpected alert")
	}
}

func TestReplyDenyViaTouch(t *testing.T) {
	h := newHarness(t, replyOpts(), ws("a", 1, "alpha", model.StatusBlocked))
	arm(t, h)
	cmds := h.touch(1)
	if !reflect.DeepEqual(cmds, []Cmd{SendKeysCmd{PaneID: "a:p1", Keys: []string{"Escape"}}}) {
		t.Fatalf("deny: %#v", cmds)
	}
}

func TestReplyArmedTimeout(t *testing.T) {
	h := newHarness(t, replyOpts(), ws("a", 1, "alpha", model.StatusBlocked))
	arm(t, h)
	h.advance(9 * time.Second)
	if h.view().Keys[0].Armed {
		t.Fatal("should time out")
	}
	cmds := h.press(0, 100*time.Millisecond)
	if _, ok := cmds[0].(FocusCmd); !ok || len(cmds) != 1 {
		t.Fatalf("after timeout a press focuses: %#v", cmds)
	}
}

func TestReplyOtherKeyDisarms(t *testing.T) {
	h := newHarness(t, replyOpts(), ws("a", 1, "alpha", model.StatusBlocked), ws("b", 2, "bravo", model.StatusWorking))
	arm(t, h)
	if cmds := h.press(1, 100*time.Millisecond); cmds != nil {
		t.Fatalf("other key should only disarm: %#v", cmds)
	}
	if h.view().Keys[0].Armed {
		t.Fatal("still armed")
	}
}

func TestReplyRefusedWhenNoLongerBlocked(t *testing.T) {
	h := newHarness(t, replyOpts(), ws("a", 1, "alpha", model.StatusBlocked))
	arm(t, h)
	h.send(StatusMsg{Event: model.AgentStatusEvent{PaneID: "a:p1", Status: model.StatusWorking}})
	if h.view().Keys[0].Armed {
		t.Fatal("unblocking must disarm immediately")
	}
	if b := h.view().Bar; !strings.Contains(b.Line2, "no longer blocked") {
		t.Fatalf("bar: %+v", b)
	}
	for _, c := range h.press(0, 100*time.Millisecond) {
		if _, ok := c.(SendKeysCmd); ok {
			t.Fatalf("must not send to a non-blocked agent: %#v", c)
		}
	}
}

func TestReplyReblockedNeedsRearm(t *testing.T) {
	// Armed on prompt A; agent unblocks then blocks on prompt B: a tap must not approve B.
	h := newHarness(t, replyOpts(), ws("a", 1, "alpha", model.StatusBlocked))
	arm(t, h)
	h.send(StatusMsg{Event: model.AgentStatusEvent{PaneID: "a:p1", Status: model.StatusWorking}})
	h.send(StatusMsg{Event: model.AgentStatusEvent{PaneID: "a:p1", Status: model.StatusBlocked}})
	for _, c := range h.press(0, 100*time.Millisecond) {
		if _, ok := c.(SendKeysCmd); ok {
			t.Fatalf("sent to a re-blocked prompt: %#v", c)
		}
	}
	// Same via snapshot, and via a long-press inspect after unblocking.
	arm(t, h)
	h.snap(ws("a", 1, "alpha", model.StatusWorking))
	h.press(0, 700*time.Millisecond) // inspect
	h.snap(ws("a", 1, "alpha", model.StatusBlocked))
	for _, c := range h.press(0, 100*time.Millisecond) {
		if _, ok := c.(SendKeysCmd); ok {
			t.Fatalf("stale arm survived: %#v", c)
		}
	}
}

func TestReplyPaneReusedDisarms(t *testing.T) {
	h := newHarness(t, replyOpts(), ws("a", 1, "alpha", model.StatusBlocked))
	arm(t, h)
	reused := ws("a", 1, "alpha", model.StatusBlocked)
	reused.Agents[0].TerminalID = "other-terminal"
	h.snap(reused)
	if h.view().Keys[0].Armed {
		t.Fatal("a different terminal in the same pane must disarm")
	}
}

func TestEmptySlotDisarms(t *testing.T) {
	h := newHarness(t, replyOpts(), ws("a", 1, "alpha", model.StatusBlocked))
	arm(t, h)
	h.press(7, 100*time.Millisecond)
	for _, c := range h.press(0, 100*time.Millisecond) {
		if _, ok := c.(SendKeysCmd); ok {
			t.Fatalf("empty-slot press should have disarmed: %#v", c)
		}
	}
}

func TestInputIgnoredWhileHerdrDown(t *testing.T) {
	h := newHarness(t, replyOpts(), ws("a", 1, "alpha", model.StatusBlocked))
	h.send(HerdrMsg{Connected: false})
	if cmds := h.press(0, 700*time.Millisecond); cmds != nil {
		t.Fatalf("offline long press: %#v", cmds)
	}
	if cmds := h.press(0, 100*time.Millisecond); cmds != nil {
		t.Fatalf("offline tap: %#v", cmds)
	}
}

func TestReplyNeverArmsNonBlocked(t *testing.T) {
	h := newHarness(t, replyOpts(), ws("a", 1, "alpha", model.StatusWorking))
	cmds := h.press(0, 700*time.Millisecond)
	if cmds[0].(ReadCmd).Purpose != ReadInspect || h.view().Keys[0].Armed {
		t.Fatalf("got %#v", cmds)
	}
}

func TestReplyWithoutKeysForAgent(t *testing.T) {
	o := replyOpts()
	o.ReplyKeys = map[string]ReplyKeys{"codex": {Approve: []string{"y"}}}
	h := newHarness(t, o, ws("a", 1, "alpha", model.StatusBlocked))
	cmds := h.press(0, 700*time.Millisecond)
	if cmds[0].(ReadCmd).Purpose != ReadInspect || h.view().Keys[0].Armed {
		t.Fatalf("got %#v", cmds)
	}
}

func TestReplyNotLanded(t *testing.T) {
	h := newHarness(t, replyOpts(), ws("a", 1, "alpha", model.StatusBlocked))
	arm(t, h)
	h.press(0, 100*time.Millisecond)
	h.advance(3100 * time.Millisecond) // still blocked
	v := h.view()
	if !v.Keys[0].Alert || !strings.Contains(v.Bar.Line2, "may not have landed") {
		t.Fatalf("want alert: %+v %+v", v.Keys[0], v.Bar)
	}
	h.advance(4 * time.Second)
	if h.view().Keys[0].Alert {
		t.Fatal("alert should clear")
	}
}

func TestDimming(t *testing.T) {
	o := defaultOpts()
	o.DimAfter = time.Minute
	h := newHarness(t, o, ws("a", 1, "alpha", model.StatusWorking))
	h.advance(59 * time.Second)
	if b := h.view().Brightness; b != 70 {
		t.Fatalf("brightness %d", b)
	}
	h.advance(2 * time.Second)
	if b := h.view().Brightness; b != 20 {
		t.Fatalf("should dim: %d", b)
	}
	h.send(StatusMsg{Event: model.AgentStatusEvent{PaneID: "a:p1", Status: model.StatusDone}})
	if b := h.view().Brightness; b != 70 {
		t.Fatalf("state change should restore: %d", b)
	}
	h.advance(61 * time.Second)
	h.send(InputMsg{Kind: TouchDown, Index: 0})
	if b := h.view().Brightness; b != 70 {
		t.Fatalf("press should restore: %d", b)
	}
}

func TestOfflineAndPaused(t *testing.T) {
	h := newHarness(t, defaultOpts(), ws("a", 1, "alpha", model.StatusWorking))
	h.send(HerdrMsg{Connected: false, Err: errors.New("dial unix: no such file")})
	v := h.view()
	if !v.Keys[0].Empty || v.Bar.Line1 != "herdr not connected" || !strings.Contains(v.Tray.Herdr, "no such file") {
		t.Fatalf("offline view: %+v", v)
	}
	h.send(HerdrMsg{Connected: true})
	h.send(PauseMsg{Paused: true})
	if v := h.view(); v.Brightness != 0 || !v.Keys[0].Empty {
		t.Fatalf("paused: %+v", v)
	}
	if cmds := h.press(0, 100*time.Millisecond); cmds != nil {
		t.Fatal("paused ignores input")
	}
}

func TestCmdFailedShowsMessage(t *testing.T) {
	h := newHarness(t, defaultOpts(), ws("a", 1, "alpha", model.StatusWorking))
	h.send(CmdFailedMsg{Cmd: FocusCmd{WorkspaceID: "a"}, Err: errors.New("agent_not_found")})
	if b := h.view().Bar; b.Line2 != "focus failed: agent_not_found" {
		t.Fatalf("bar: %+v", b)
	}
}

func TestMarquee(t *testing.T) {
	long := strings.Repeat("abcdefghij", 6) // 60 runes
	if got := marquee(long, 0); got != long {
		t.Fatal("no scroll before delay")
	}
	if got := marquee(long, marqueeDelay+10*marqueeStep); !strings.HasPrefix(got, "abcdefghij"[0:0]+long[10:20]) {
		t.Fatalf("scrolled: %q", got)
	}
	if got := marquee(long, time.Hour); len([]rune(got)) != marqueeWidth {
		t.Fatalf("should stop at end: %q", got)
	}
}

const claudeIdle = `  I have replies drafted for each of the three threads. I haven't posted them.

※ recap: TICKET-787 booking-widget personalization is implemented and both PRs are open. Next: post the replies.

────────────────────────────────────────────────────────────────────────────────
❯ '''
────────────────────────────────────────────────────────────────────────────────
  ⏵⏵ auto mode on (shift+tab to cycle)
`

func TestInspectPrefersRecap(t *testing.T) {
	l1, l2 := InspectLines(MeaningfulLines(claudeIdle), "TICKET-787")
	if l1 != "TICKET-787 · recap" || !strings.HasPrefix(l2, "TICKET-787 booking-widget") {
		t.Fatalf("got %q / %q", l1, l2)
	}
	l1, l2 = InspectLines(MeaningfulLines(strings.Replace(claudeIdle, "※ recap:", "", 1)), "t")
	if !strings.HasPrefix(l1, "I have replies") || !strings.HasPrefix(l2, "TICKET-787") {
		t.Fatalf("fallback: %q / %q", l1, l2)
	}
}

func TestReplySendFailedAlerts(t *testing.T) {
	h := newHarness(t, replyOpts(), ws("a", 1, "alpha", model.StatusBlocked))
	arm(t, h)
	cmds := h.press(0, 100*time.Millisecond)
	h.send(CmdFailedMsg{Cmd: cmds[0], Err: errors.New("pane_not_found")})
	v := h.view()
	if !v.Keys[0].Alert || v.Bar.Line2 != "approve failed: pane_not_found" {
		t.Fatalf("got %+v %+v", v.Keys[0], v.Bar)
	}
}

func TestTerminalsShownDimmedAndLast(t *testing.T) {
	o := defaultOpts()
	o.ShowTerminals = true
	h := newHarness(t, o, model.Workspace{ID: "t", Number: 1, Label: "myrepo"}, ws("a", 2, "alpha", model.StatusIdle))
	keys := h.view().Keys
	if keys[0].Title != "alpha" || keys[1].Title != "myrepo" || keys[1].Subtitle != "terminal" || keys[1].Status != model.StatusUnknown {
		t.Fatalf("keys: %+v %+v", keys[0], keys[1])
	}
	if cmds := h.press(1, 100*time.Millisecond); !reflect.DeepEqual(cmds, []Cmd{FocusCmd{WorkspaceID: "t"}}) {
		t.Fatalf("tap terminal: %#v", cmds)
	}
	if cmds := h.press(1, 700*time.Millisecond); cmds != nil {
		t.Fatalf("long-press terminal: %#v", cmds)
	}

	o.ShowTerminals = false
	h = newHarness(t, o, model.Workspace{ID: "t", Number: 1, Label: "myrepo"}, ws("a", 2, "alpha", model.StatusIdle))
	if got := h.titles(); !reflect.DeepEqual(got, []string{"alpha"}) {
		t.Fatalf("hidden: %v", got)
	}
}

func TestPressFeedbackAndOptimisticFocus(t *testing.T) {
	h := newHarness(t, defaultOpts(), ws("a", 1, "alpha", model.StatusWorking), ws("b", 2, "bravo", model.StatusWorking))
	h.send(InputMsg{Kind: KeyDown, Index: 1})
	if k := h.view().Keys[1]; !k.Pressed {
		t.Fatal("held key should render pressed")
	}
	h.send(InputMsg{Kind: KeyUp, Index: 1})
	v := h.view()
	if v.Keys[1].Pressed || !v.Keys[1].Focused || v.Keys[0].Focused {
		t.Fatalf("after tap: %+v %+v", v.Keys[0], v.Keys[1])
	}
	// A long press stops showing pressed once it fires.
	h.send(InputMsg{Kind: KeyDown, Index: 0})
	h.advance(700 * time.Millisecond)
	if h.view().Keys[0].Pressed {
		t.Fatal("pressed should clear when the long press fires")
	}
}
