// Package herdr is a client for the herdr socket API (NDJSON over a unix
// socket). See docs/herdr-protocol.md for the protocol as observed.
package herdr

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"herdeck/internal/model"
)

const defaultCallTimeout = 3 * time.Second

// Client talks to one herdr server. It is safe for concurrent use.
type Client struct {
	socket string
	nextID atomic.Uint64

	// Timings, unexported so tests can shrink them.
	callTimeout  time.Duration // applied when ctx has no deadline
	debounce     time.Duration // coalescing window for lifecycle refetch triggers
	minRefetch   time.Duration // minimum spacing between triggered refetches
	pollInterval time.Duration // safety poll
	backoffBase  time.Duration
	backoffCap   time.Duration

	// kick asks the running Watch session for an immediate refetch, e.g.
	// right after a focus so the deck doesn't wait for the safety poll.
	kick chan struct{}
}

// New returns a client for the herdr socket at socketPath.
func New(socketPath string) *Client {
	return &Client{
		socket:       socketPath,
		callTimeout:  defaultCallTimeout,
		debounce:     150 * time.Millisecond,
		minRefetch:   250 * time.Millisecond,
		pollInterval: time.Second,
		backoffBase:  250 * time.Millisecond,
		backoffCap:   10 * time.Second,
		kick:         make(chan struct{}, 1),
	}
}

// Refresh makes Watch refetch the snapshot now. Never blocks.
func (c *Client) Refresh() {
	select {
	case c.kick <- struct{}{}:
	default:
	}
}

// SocketPath returns the socket the client connects to.
func (c *Client) SocketPath() string { return c.socket }

// APIError is an error returned by herdr in a response's "error" member.
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string { return "herdr: " + e.Code + ": " + e.Message }

// sessionList runs `herdr session list --json`; a var so tests can stub it.
var sessionList = func() ([]byte, error) {
	return exec.Command("herdr", "session", "list", "--json").Output()
}

// ResolveSocket picks the herdr socket path: $HERDR_SOCKET_PATH, else the
// named session (or $HERDR_SESSION) looked up via `herdr session list`, else
// the default ~/.config/herdr/herdr.sock.
func ResolveSocket(session string) (string, error) {
	if p := os.Getenv("HERDR_SOCKET_PATH"); p != "" {
		return expandHome(p), nil
	}
	if session == "" {
		session = os.Getenv("HERDR_SESSION")
	}
	if session != "" && session != "default" {
		return lookupSession(session)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "herdr", "herdr.sock"), nil
}

func lookupSession(name string) (string, error) {
	out, err := sessionList()
	if err != nil {
		return "", fmt.Errorf("herdr session list: %w", err)
	}
	var list struct {
		Sessions []struct {
			Name       string `json:"name"`
			SocketPath string `json:"socket_path"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return "", fmt.Errorf("herdr session list: %w", err)
	}
	for _, s := range list.Sessions {
		if s.Name == name && s.SocketPath != "" {
			return expandHome(s.SocketPath), nil
		}
	}
	return "", fmt.Errorf("herdr session %q not found", name)
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}

// envelope is any line herdr sends: a response (id + result/error) or an
// event (event + data).
type envelope struct {
	ID     *string         `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *APIError       `json:"error"`
	Event  string          `json:"event"`
	Data   json.RawMessage `json:"data"`
}

// dial opens a connection whose I/O deadline follows ctx (cancellation closes
// it). The returned stop func detaches ctx; call it before closing conn.
func (c *Client) dial(ctx context.Context) (net.Conn, func() bool, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.socket)
	if err != nil {
		return nil, nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	return conn, context.AfterFunc(ctx, func() { conn.Close() }), nil
}

// roundTrip sends one request on conn and reads lines until the matching
// response, which it decodes into out.
func (c *Client) roundTrip(conn net.Conn, br *bufio.Reader, method string, params, out any) error {
	if params == nil {
		params = struct{}{}
	}
	id := strconv.FormatUint(c.nextID.Add(1), 10)
	req, err := json.Marshal(struct {
		ID     string `json:"id"`
		Method string `json:"method"`
		Params any    `json:"params"`
	}{id, method, params})
	if err != nil {
		return err
	}
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return err
	}
	for {
		line, err := br.ReadBytes('\n')
		if err != nil {
			return err
		}
		var env envelope
		if json.Unmarshal(line, &env) != nil || env.ID == nil {
			continue // stray event or garbage
		}
		// herdr answers unparseable requests with an error and id "".
		if *env.ID == "" && env.Error != nil {
			return env.Error
		}
		if *env.ID != id {
			continue
		}
		if env.Error != nil {
			return env.Error
		}
		if out == nil || len(env.Result) == 0 {
			return nil
		}
		return json.Unmarshal(env.Result, out)
	}
}

// Call performs one request on a fresh connection and decodes the response's
// result into out (which may be nil). Nil params are sent as {}. If ctx has no
// deadline, a 3 s timeout applies.
func (c *Client) Call(ctx context.Context, method string, params, out any) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.callTimeout)
		defer cancel()
	}
	conn, stop, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	defer stop()
	err = c.roundTrip(conn, bufio.NewReader(conn), method, params, out)
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr
	}
	if err != nil {
		// Report the ctx error rather than the forced close / conn deadline
		// (which can fire a hair before ctx's own timer).
		if ctx.Err() != nil {
			err = ctx.Err()
		} else if errors.Is(err, os.ErrDeadlineExceeded) {
			err = context.DeadlineExceeded
		}
		return fmt.Errorf("herdr %s: %w", method, err)
	}
	return nil
}

func (c *Client) Ping(ctx context.Context) (model.ServerInfo, error) {
	var r struct {
		Version  string `json:"version"`
		Protocol int    `json:"protocol"`
	}
	if err := c.Call(ctx, "ping", nil, &r); err != nil {
		return model.ServerInfo{}, err
	}
	return model.ServerInfo{Version: r.Version, Protocol: r.Protocol}, nil
}

type wireWorkspace struct {
	ID          string `json:"workspace_id"`
	Number      int    `json:"number"`
	Label       string `json:"label"`
	AgentStatus string `json:"agent_status"`
	Focused     bool   `json:"focused"`
}

type wireAgent struct {
	Agent       string `json:"agent"`
	AgentStatus string `json:"agent_status"`
	PaneID      string `json:"pane_id"`
	TerminalID  string `json:"terminal_id"`
	WorkspaceID string `json:"workspace_id"`
	TabID       string `json:"tab_id"`
	Focused     bool   `json:"focused"`
}

// Snapshot fetches workspace.list and agent.list and joins them. Agents keep
// herdr's order within their workspace; agents of unknown workspaces are
// dropped.
func (c *Client) Snapshot(ctx context.Context) (model.Snapshot, error) {
	var ws struct {
		Workspaces []wireWorkspace `json:"workspaces"`
	}
	if err := c.Call(ctx, "workspace.list", nil, &ws); err != nil {
		return model.Snapshot{}, err
	}
	var as struct {
		Agents []wireAgent `json:"agents"`
	}
	if err := c.Call(ctx, "agent.list", nil, &as); err != nil {
		return model.Snapshot{}, err
	}
	return buildSnapshot(ws.Workspaces, as.Agents), nil
}

func buildSnapshot(ws []wireWorkspace, as []wireAgent) model.Snapshot {
	byWS := make(map[string][]model.Agent)
	for _, a := range as {
		byWS[a.WorkspaceID] = append(byWS[a.WorkspaceID], model.Agent{
			PaneID:      a.PaneID,
			TerminalID:  a.TerminalID,
			WorkspaceID: a.WorkspaceID,
			TabID:       a.TabID,
			Name:        a.Agent,
			Status:      model.ParseStatus(a.AgentStatus),
			Focused:     a.Focused,
		})
	}
	snap := model.Snapshot{Workspaces: make([]model.Workspace, 0, len(ws))}
	for _, w := range ws {
		snap.Workspaces = append(snap.Workspaces, model.Workspace{
			ID:      w.ID,
			Number:  w.Number,
			Label:   w.Label,
			Status:  model.ParseStatus(w.AgentStatus),
			Focused: w.Focused,
			Agents:  byWS[w.ID],
		})
	}
	slices.SortStableFunc(snap.Workspaces, func(a, b model.Workspace) int { return cmp.Compare(a.Number, b.Number) })
	return snap
}

func (c *Client) FocusWorkspace(ctx context.Context, workspaceID string) error {
	err := c.Call(ctx, "workspace.focus", map[string]string{"workspace_id": workspaceID}, nil)
	if err == nil {
		c.Refresh()
	}
	return err
}

func (c *Client) FocusAgent(ctx context.Context, paneID string) error {
	err := c.Call(ctx, "agent.focus", map[string]string{"target": paneID}, nil)
	if err == nil {
		c.Refresh()
	}
	return err
}

// ansiRE matches CSI, OSC (BEL or ST terminated) and other ESC sequences.
var ansiRE = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[ -/]*[0-~]`)

func (c *Client) Read(ctx context.Context, paneID string, lines int) (string, error) {
	var r struct {
		Read struct {
			Text string `json:"text"`
		} `json:"read"`
	}
	params := map[string]any{"pane_id": paneID, "source": "recent-unwrapped", "lines": lines, "format": "text"}
	if err := c.Call(ctx, "pane.read", params, &r); err != nil {
		return "", err
	}
	return ansiRE.ReplaceAllString(r.Read.Text, ""), nil
}

func (c *Client) SendKeys(ctx context.Context, paneID string, keys []string) error {
	return c.Call(ctx, "pane.send_keys", map[string]any{"pane_id": paneID, "keys": keys}, nil)
}
