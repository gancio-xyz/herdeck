package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gancio-xyz/herdeck/internal/herdr/herdrtest"
	"github.com/gancio-xyz/herdeck/internal/model"
)

// fixtureArray returns result.<field> of a captured response in testdata.
func fixtureArray(t *testing.T, file, field string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Result map[string]json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		t.Fatal(err)
	}
	return string(env.Result[field])
}

func fixtureLines(t *testing.T, file string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func sampleServer(t *testing.T) *herdrtest.Server {
	s := herdrtest.New(t)
	s.SetWorkspaces(fixtureArray(t, "workspace.list.json", "workspaces"))
	s.SetAgents(fixtureArray(t, "agent.list.json", "agents"))
	return s
}

func TestCallEnvelope(t *testing.T) {
	s := herdrtest.New(t)
	s.Handle("agent.focus", func(p json.RawMessage) (any, *herdrtest.Error) {
		return nil, &herdrtest.Error{Code: "agent_not_found", Message: "no such agent"}
	})
	c := New(s.Path)
	ctx := context.Background()

	info, err := c.Ping(ctx)
	if err != nil || info != (model.ServerInfo{Version: "0.7.1", Protocol: 14}) {
		t.Fatalf("Ping = %+v, %v", info, err)
	}
	if req := s.Requests()[0]; req.Method != "ping" || string(req.Params) != "{}" || req.ID == "" {
		t.Errorf("ping request = %+v, want params {}", req)
	}

	err = c.FocusAgent(ctx, "w1:p1")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "agent_not_found" || apiErr.Message != "no such agent" {
		t.Fatalf("FocusAgent err = %v, want APIError agent_not_found", err)
	}
	if got := string(s.Requests()[1].Params); got != `{"target":"w1:p1"}` {
		t.Errorf("agent.focus params = %s", got)
	}

	err = c.Call(ctx, "no.such", nil, nil)
	if !errors.As(err, &apiErr) || apiErr.Code != "unknown_method" {
		t.Errorf("unknown method err = %v", err)
	}
}

func TestCallTimeout(t *testing.T) {
	s := herdrtest.New(t)
	block := make(chan struct{})
	defer close(block)
	s.Handle("ping", func(json.RawMessage) (any, *herdrtest.Error) { <-block; return nil, nil })
	c := New(s.Path)
	c.callTimeout = 50 * time.Millisecond
	start := time.Now()
	if _, err := c.Ping(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("timeout took %v", d)
	}
}

func TestActions(t *testing.T) {
	s := herdrtest.New(t)
	ok := func(json.RawMessage) (any, *herdrtest.Error) { return map[string]string{"type": "ok"}, nil }
	s.Handle("workspace.focus", ok)
	s.Handle("pane.send_keys", ok)
	s.Handle("pane.read", func(json.RawMessage) (any, *herdrtest.Error) {
		return map[string]any{"type": "pane_read", "read": map[string]string{
			"text": "\x1b[1;32mhello\x1b[0m world\n\x1b]0;title\x07done\x1b(B",
		}}, nil
	})
	c := New(s.Path)
	ctx := context.Background()
	if err := c.FocusWorkspace(ctx, "w2S"); err != nil {
		t.Fatal(err)
	}
	if err := c.SendKeys(ctx, "w2S:p1", []string{"Enter"}); err != nil {
		t.Fatal(err)
	}
	text, err := c.Read(ctx, "w2S:p1", 40)
	if err != nil {
		t.Fatal(err)
	}
	if text != "hello world\ndone" {
		t.Errorf("Read = %q", text)
	}
	want := []string{
		`{"workspace_id":"w2S"}`,
		`{"keys":["Enter"],"pane_id":"w2S:p1"}`,
		`{"format":"text","lines":40,"pane_id":"w2S:p1","source":"recent-unwrapped"}`,
	}
	for i, r := range s.Requests() {
		if string(r.Params) != want[i] {
			t.Errorf("%s params = %s, want %s", r.Method, r.Params, want[i])
		}
	}
}

func TestSnapshotFromSamples(t *testing.T) {
	s := sampleServer(t)
	// Shuffle workspace order to check sorting by Number.
	s.SetWorkspaces(`[{"workspace_id":"wB","number":2,"label":"b","agent_status":"bogus"},
		{"workspace_id":"wA","number":1,"label":"a","agent_status":"blocked","focused":true}]`)
	s.SetAgents(`[{"agent":"claude","agent_status":"blocked","pane_id":"wA:p2","workspace_id":"wA"},
		{"agent":"codex","agent_status":"done","pane_id":"wA:p1","workspace_id":"wA"},
		{"agent":"x","agent_status":"idle","pane_id":"wZ:p1","workspace_id":"wZ"}]`)
	snap, err := New(s.Path).Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Workspaces) != 2 || snap.Workspaces[0].ID != "wA" || snap.Workspaces[1].Status != model.StatusUnknown {
		t.Fatalf("snapshot = %+v", snap)
	}
	if a := snap.Workspaces[0].Agents; len(a) != 2 || a[0].PaneID != "wA:p2" || a[1].Status != model.StatusDone {
		t.Errorf("agents = %+v", a)
	}
	if snap.Workspaces[1].Agents != nil {
		t.Errorf("wB agents = %+v", snap.Workspaces[1].Agents)
	}

	// The real captures.
	s = sampleServer(t)
	snap, err = New(s.Path).Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Workspaces) != 7 {
		t.Fatalf("got %d workspaces", len(snap.Workspaces))
	}
	w := snap.Workspaces[4]
	want := model.Workspace{ID: "w2S", Number: 5, Label: "myrepo TICKET-1213", Status: model.StatusWorking, Focused: true,
		Agents: []model.Agent{{PaneID: "w2S:p1", TerminalID: "term_65eea954f5fd4c", WorkspaceID: "w2S", TabID: "w2S:t1",
			Name: "claude", Status: model.StatusWorking, Focused: true}}}
	if !jsonEqual(w, want) {
		t.Errorf("workspace 5 = %+v\nwant %+v", w, want)
	}
	for i, w := range snap.Workspaces {
		if w.Number != i+1 || len(w.Agents) != 1 {
			t.Errorf("workspace %d: number %d, %d agents", i, w.Number, len(w.Agents))
		}
	}
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func TestResolveSocket(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	calls := 0
	orig := sessionList
	t.Cleanup(func() { sessionList = orig })
	sessionList = func() ([]byte, error) {
		calls++
		return []byte(`{"sessions":[
			{"default":true,"name":"default","socket_path":"~/.config/herdr/herdr.sock"},
			{"name":"work","socket_path":"~/.config/herdr/sessions/work/herdr.sock"}]}`), nil
	}
	def := filepath.Join(home, ".config/herdr/herdr.sock")
	work := filepath.Join(home, ".config/herdr/sessions/work/herdr.sock")

	cases := []struct {
		name, env, envSession, session, want string
		wantErr                              bool
	}{
		{name: "env path wins", env: "/tmp/x.sock", envSession: "work", session: "work", want: "/tmp/x.sock"},
		{name: "default", want: def},
		{name: "default session", session: "default", want: def},
		{name: "named session", session: "work", want: work},
		{name: "flag beats HERDR_SESSION", envSession: "nope", session: "work", want: work},
		{name: "HERDR_SESSION", envSession: "work", want: work},
		{name: "HERDR_SESSION default", envSession: "default", want: def},
		{name: "missing session", session: "nope", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HERDR_SOCKET_PATH", tc.env)
			t.Setenv("HERDR_SESSION", tc.envSession)
			calls = 0
			got, err := ResolveSocket(tc.session)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("ResolveSocket(%q) = %q, %v; want %q (err %v)", tc.session, got, err, tc.want, tc.wantErr)
			}
			if calls > 1 {
				t.Errorf("session list ran %d times", calls)
			}
		})
	}
}
