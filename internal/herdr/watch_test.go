package herdr

import (
	"context"
	"slices"
	"testing"
	"time"

	"herdeck/internal/herdr/herdrtest"
	"herdeck/internal/model"
)

const waitTimeout = 3 * time.Second

func fastClient(path string) *Client {
	c := New(path)
	c.callTimeout = time.Second
	c.debounce = 20 * time.Millisecond
	c.minRefetch = 50 * time.Millisecond
	c.pollInterval = time.Hour
	c.backoffBase = 10 * time.Millisecond
	c.backoffCap = 50 * time.Millisecond
	return c
}

func watch(t *testing.T, c *Client) <-chan Update {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ch := c.Watch(ctx)
	t.Cleanup(func() {
		cancel()
		for range ch { // drain so Watch can exit
		}
	})
	return ch
}

// waitFor returns the next update of kind, skipping others. An unexpected
// UpdateDisconnected fails the test.
func waitFor(t *testing.T, ch <-chan Update, kind UpdateKind) Update {
	t.Helper()
	timeout := time.After(waitTimeout)
	for {
		select {
		case u, ok := <-ch:
			if !ok {
				t.Fatalf("channel closed waiting for kind %d", kind)
			}
			if u.Kind == kind {
				return u
			}
			if u.Kind == UpdateDisconnected {
				t.Fatalf("unexpected disconnect: %v", u.Err)
			}
		case <-timeout:
			t.Fatalf("timed out waiting for kind %d", kind)
		}
	}
}

// eventually polls cond until it holds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// statusPaneSets returns the pane ids of each open status subscription.
func statusPaneSets(s *herdrtest.Server) [][]string {
	var out [][]string
	for _, sub := range s.Subscriptions() {
		var panes []string
		for _, m := range sub.Subs {
			if m["type"] == statusType {
				panes = append(panes, m["pane_id"])
			}
		}
		if panes != nil {
			slices.Sort(panes)
			out = append(out, panes)
		}
	}
	return out
}

func TestWatchConnectSnapshotAndStatus(t *testing.T) {
	s := sampleServer(t)
	ch := watch(t, fastClient(s.Path))

	u := waitFor(t, ch, UpdateConnected)
	if u.Info.Protocol != 14 || u.Info.Version != "0.7.1" {
		t.Errorf("info = %+v", u.Info)
	}
	u = waitFor(t, ch, UpdateSnapshot)
	if len(u.Snapshot.Workspaces) != 7 {
		t.Fatalf("snapshot has %d workspaces", len(u.Snapshot.Workspaces))
	}
	eventually(t, "status subscription", func() bool { return len(statusPaneSets(s)) == 1 })
	if got := statusPaneSets(s)[0]; len(got) != 7 || got[0] != "w2E:p1" {
		t.Errorf("status subscription panes = %v", got)
	}
	// Lifecycle subscription must not include *.focused.
	for _, sub := range s.Subscriptions() {
		for _, m := range sub.Subs {
			if m["type"] == "workspace.focused" || m["type"] == "pane.focused" {
				t.Errorf("subscribed to %s", m["type"])
			}
		}
	}

	lines := fixtureLines(t, "pane.agent_status_changed.ndjson")
	want := []model.AgentStatusEvent{
		{PaneID: "w2X:p1", WorkspaceID: "w2X", Agent: "probe", Status: model.StatusWorking, CustomStatus: "probing"},
		{PaneID: "w2X:p1", WorkspaceID: "w2X", Agent: "probe", Status: model.StatusBlocked},
		{PaneID: "w2X:p1", WorkspaceID: "w2X", Status: model.StatusUnknown},
	}
	for i, l := range lines {
		s.PushRaw(l)
		if got := waitFor(t, ch, UpdateStatus).Status; got != want[i] {
			t.Errorf("status %d = %+v, want %+v", i, got, want[i])
		}
	}
}

func TestWatchResubscribesOnNewAgent(t *testing.T) {
	s := herdrtest.New(t)
	s.SetWorkspaces(`[{"workspace_id":"w1","number":1,"label":"one","agent_status":"idle"}]`)
	ch := watch(t, fastClient(s.Path))
	waitFor(t, ch, UpdateSnapshot)
	// No agents: lifecycle subscription only.
	eventually(t, "lifecycle subscription", func() bool { return len(s.Subscriptions()) == 1 })
	if n := len(statusPaneSets(s)); n != 0 {
		t.Fatalf("%d status subscriptions with zero agents", n)
	}

	s.SetAgents(`[{"agent":"claude","agent_status":"working","pane_id":"w1:p1","workspace_id":"w1"}]`)
	s.Push("pane_agent_detected", map[string]string{"type": "pane_agent_detected", "pane_id": "w1:p1", "workspace_id": "w1", "agent": "claude"})
	u := waitFor(t, ch, UpdateSnapshot)
	if a := u.Snapshot.Workspaces[0].Agents; len(a) != 1 || a[0].Status != model.StatusWorking {
		t.Fatalf("agents = %+v", a)
	}
	eventually(t, "status subscription for w1:p1", func() bool {
		sets := statusPaneSets(s)
		return len(sets) == 1 && slices.Equal(sets[0], []string{"w1:p1"})
	})

	s.SetAgents(`[{"agent":"claude","agent_status":"working","pane_id":"w1:p1","workspace_id":"w1"},
		{"agent":"codex","agent_status":"idle","pane_id":"w1:p2","workspace_id":"w1"}]`)
	s.Push("pane_created", map[string]any{"type": "pane_created"})
	waitFor(t, ch, UpdateSnapshot)
	eventually(t, "old status subscription replaced", func() bool {
		sets := statusPaneSets(s)
		return len(sets) == 1 && slices.Equal(sets[0], []string{"w1:p1", "w1:p2"})
	})
	s.Push(statusType, map[string]string{"pane_id": "w1:p2", "workspace_id": "w1", "agent": "codex", "agent_status": "blocked"})
	if got := waitFor(t, ch, UpdateStatus).Status; got.PaneID != "w1:p2" || got.Status != model.StatusBlocked {
		t.Errorf("status = %+v", got)
	}

	// Agent gone: status connection dropped entirely.
	s.SetAgents(`[]`)
	s.Push("pane_closed", map[string]any{"type": "pane_closed"})
	waitFor(t, ch, UpdateSnapshot)
	eventually(t, "status subscription closed", func() bool { return len(statusPaneSets(s)) == 0 })
}

func TestWatchBacklogBurstCollapses(t *testing.T) {
	s := sampleServer(t)
	backlog := fixtureLines(t, "lifecycle-replay.ndjson")
	// ~40 lines over ~120 ms, faster than the 20 ms debounce + 50 ms spacing.
	s.SetBacklog(backlog, 3*time.Millisecond)
	ch := watch(t, fastClient(s.Path))
	waitFor(t, ch, UpdateSnapshot)

	eventually(t, "a triggered refetch", func() bool { return s.Count("workspace.list") >= 2 })
	time.Sleep(300 * time.Millisecond) // let the burst and any trailing refetch finish
	n := s.Count("workspace.list")
	// 1 initial + at most one per 50 ms over the ~120 ms replay (+1 trailing).
	if n > 5 {
		t.Errorf("%d workspace.list calls for a %d-line backlog; want burst collapsed", n, len(backlog))
	}
	t.Logf("%d refetches for %d backlog lines", n-1, len(backlog))
}

func TestWatchIgnoresGarbage(t *testing.T) {
	s := sampleServer(t)
	ch := watch(t, fastClient(s.Path))
	waitFor(t, ch, UpdateSnapshot)
	eventually(t, "subscriptions", func() bool { return len(s.Subscriptions()) == 2 })

	s.PushRaw(`this is not json`)
	s.PushRaw(`{"event":"something.new","data":{"x":1}}`)
	s.PushRaw(`{"event":"pane.agent_status_changed","data":"oops"}`)
	s.PushRaw(`{"unexpected":true}`)
	s.Push(statusType, map[string]string{"pane_id": "w2E:p1", "workspace_id": "w2E", "agent": "claude", "agent_status": "done"})
	if got := waitFor(t, ch, UpdateStatus).Status; got.PaneID != "w2E:p1" || got.Status != model.StatusDone {
		t.Errorf("status = %+v", got)
	}
}

func TestWatchSafetyPoll(t *testing.T) {
	s := sampleServer(t)
	c := fastClient(s.Path)
	c.pollInterval = 20 * time.Millisecond
	ch := watch(t, c)
	waitFor(t, ch, UpdateSnapshot)

	// Unchanged state is still re-emitted on every poll: that is what lets
	// the consumer correct drift from patched-in status events.
	before := s.Count("workspace.list")
	for i := 0; i < 3; i++ {
		waitFor(t, ch, UpdateSnapshot)
	}
	if s.Count("workspace.list") < before+3 {
		t.Errorf("safety poll not running")
	}

	s.SetWorkspaces(`[{"workspace_id":"w9","number":1,"label":"new","agent_status":"idle"}]`)
	if u := waitFor(t, ch, UpdateSnapshot); u.Snapshot.Workspaces[0].ID != "w9" {
		t.Errorf("snapshot = %+v", u.Snapshot)
	}
}

func TestWatchReconnects(t *testing.T) {
	s := sampleServer(t)
	ch := watch(t, fastClient(s.Path))
	waitFor(t, ch, UpdateSnapshot)

	s.Stop()
	u := waitFor(t, ch, UpdateDisconnected)
	if u.Err == nil {
		t.Error("disconnect without error")
	}
	// Retries while down must not emit further disconnects.
	time.Sleep(150 * time.Millisecond)
	s.SetWorkspaces(`[{"workspace_id":"w9","number":1,"label":"after","agent_status":"idle"}]`)
	s.SetAgents(`[]`)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, ch, UpdateConnected) // fails on any extra UpdateDisconnected
	if u := waitFor(t, ch, UpdateSnapshot); len(u.Snapshot.Workspaces) != 1 || u.Snapshot.Workspaces[0].Label != "after" {
		t.Errorf("snapshot after reconnect = %+v", u.Snapshot)
	}
}

func TestWatchInitiallyDown(t *testing.T) {
	s := sampleServer(t)
	s.Stop()
	ch := watch(t, fastClient(s.Path))
	waitFor(t, ch, UpdateDisconnected)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, ch, UpdateConnected)
	waitFor(t, ch, UpdateSnapshot)
}

func TestWatchCancelClosesChannel(t *testing.T) {
	s := sampleServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	ch := fastClient(s.Path).Watch(ctx)
	waitFor(t, ch, UpdateSnapshot)
	eventually(t, "subscriptions", func() bool { return len(s.Subscriptions()) == 2 })
	cancel()
	timeout := time.After(waitTimeout)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				// Watch waits for its goroutines before closing, so the
				// server sees the subscription connections go away.
				eventually(t, "subscriptions closed", func() bool { return len(s.Subscriptions()) == 0 })
				return
			}
		case <-timeout:
			t.Fatal("channel not closed after cancel")
		}
	}
}
