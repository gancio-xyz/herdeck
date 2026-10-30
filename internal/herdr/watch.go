package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gancio-xyz/herdeck/internal/model"
)

// lifecycleTypes only trigger refetches; *.focused is left out because it
// dominates herdr's history replay.
var lifecycleTypes = []string{
	"workspace.created", "workspace.closed", "workspace.renamed",
	"pane.created", "pane.closed", "pane.agent_detected",
}

const statusType = "pane.agent_status_changed"

// eventName normalises herdr's two spellings (workspace_created vs
// pane.agent_status_changed) to the dotted subscription form.
func eventName(s string) string {
	if strings.Contains(s, ".") {
		return s
	}
	kind, rest, ok := strings.Cut(s, "_")
	if !ok {
		return s
	}
	return kind + "." + rest
}

// subConn is an open events.subscribe connection.
type subConn struct {
	conn    net.Conn
	closing atomic.Bool
	stop    func() bool
}

func (s *subConn) close() {
	s.closing.Store(true)
	s.stop()
	s.conn.Close()
}

// subscribe opens a connection, starts the subscription and then hands every
// event line to handle on a goroutine tracked by wg. A read failure that was
// not caused by close() is passed to fail.
func (c *Client) subscribe(ctx context.Context, wg *sync.WaitGroup, subs []map[string]string,
	handle func(name string, data json.RawMessage), fail func(error)) (*subConn, error) {
	conn, stop, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	sc := &subConn{conn: conn, stop: stop}
	br := bufio.NewReader(conn)
	conn.SetDeadline(time.Now().Add(c.callTimeout))
	if err := c.roundTrip(conn, br, "events.subscribe", map[string]any{"subscriptions": subs}, nil); err != nil {
		sc.close()
		return nil, fmt.Errorf("herdr events.subscribe: %w", err)
	}
	conn.SetDeadline(time.Time{})
	wg.Go(func() {
		for {
			line, err := br.ReadBytes('\n')
			if err != nil {
				if !sc.closing.Load() {
					fail(fmt.Errorf("herdr subscription: %w", err))
				}
				return
			}
			var env envelope
			if err := json.Unmarshal(line, &env); err != nil {
				log.Printf("herdr: ignoring malformed event line: %v", err)
				continue
			}
			if env.Event != "" {
				handle(eventName(env.Event), env.Data)
			}
		}
	})
	return sc, nil
}

// Watch streams herdr state until ctx is done; see API.Watch.
func (c *Client) Watch(ctx context.Context) <-chan Update {
	out := make(chan Update)
	go func() {
		defer close(out)
		backoff := c.backoffBase
		reportedDown := false
		for {
			established, err := c.session(ctx, out, &reportedDown)
			if ctx.Err() != nil {
				return
			}
			if established {
				backoff = c.backoffBase
			}
			if !reportedDown {
				reportedDown = true
				select {
				case out <- Update{Kind: UpdateDisconnected, Err: err}:
				case <-ctx.Done():
					return
				}
			}
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
			backoff = min(backoff*2, c.backoffCap)
		}
	}()
	return out
}

// session runs one connection lifetime. It returns when anything fails (or
// ctx is done), after all its goroutines have exited. established reports
// whether it got as far as the steady-state loop.
func (c *Client) session(parent context.Context, out chan<- Update, reportedDown *bool) (established bool, err error) {
	ctx, cancel := context.WithCancel(parent)
	var wg sync.WaitGroup
	defer wg.Wait()
	defer cancel()

	emit := func(u Update) error {
		select {
		case out <- u:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	info, err := c.Ping(ctx)
	if err != nil {
		return false, err
	}
	if info.Protocol < model.MinProtocol || info.Protocol > model.MaxProtocol {
		log.Printf("herdr: server %s speaks protocol %d, herdeck supports %d..%d; continuing",
			info.Version, info.Protocol, model.MinProtocol, model.MaxProtocol)
	}
	last, err := c.Snapshot(ctx)
	if err != nil {
		return false, err
	}

	errc := make(chan error, 1)
	fail := func(err error) {
		select {
		case errc <- err:
		default:
		}
	}
	trigger := make(chan struct{}, 1)
	statusc := make(chan model.AgentStatusEvent)

	lsubs := make([]map[string]string, len(lifecycleTypes))
	for i, t := range lifecycleTypes {
		lsubs[i] = map[string]string{"type": t}
	}
	_, err = c.subscribe(ctx, &wg, lsubs, func(name string, _ json.RawMessage) {
		if slices.Contains(lifecycleTypes, name) {
			select {
			case trigger <- struct{}{}:
			default:
			}
		}
	}, fail)
	if err != nil {
		return false, err
	}

	// Status subscription over the current agent panes; replaced whenever the
	// pane set changes.
	var (
		status      *subConn
		statusPanes []string
	)
	handleStatus := func(name string, data json.RawMessage) {
		if name != statusType {
			return
		}
		var d struct {
			Agent        string `json:"agent"`
			AgentStatus  string `json:"agent_status"`
			CustomStatus string `json:"custom_status"`
			PaneID       string `json:"pane_id"`
			WorkspaceID  string `json:"workspace_id"`
		}
		if err := json.Unmarshal(data, &d); err != nil || d.PaneID == "" {
			log.Printf("herdr: ignoring malformed %s event", statusType)
			return
		}
		select {
		case statusc <- model.AgentStatusEvent{
			PaneID: d.PaneID, WorkspaceID: d.WorkspaceID, Agent: d.Agent,
			Status: model.ParseStatus(d.AgentStatus), CustomStatus: d.CustomStatus,
		}:
		case <-ctx.Done():
		}
	}
	resubscribe := func(snap model.Snapshot) error {
		panes := agentPanes(snap)
		if slices.Equal(panes, statusPanes) && (status != nil || len(panes) == 0) {
			return nil
		}
		var next *subConn
		if len(panes) > 0 {
			subs := make([]map[string]string, len(panes))
			for i, p := range panes {
				subs[i] = map[string]string{"type": statusType, "pane_id": p}
			}
			var err error
			if next, err = c.subscribe(ctx, &wg, subs, handleStatus, fail); err != nil {
				return err
			}
		}
		if status != nil {
			status.close()
		}
		status, statusPanes = next, panes
		return nil
	}
	if err := resubscribe(last); err != nil {
		return false, err
	}

	// Only report connected once the whole session is up, so a server that
	// answers ping but fails later doesn't flap connected/disconnected.
	if err := emit(Update{Kind: UpdateConnected, Info: info}); err != nil {
		return false, err
	}
	*reportedDown = false
	if err := emit(Update{Kind: UpdateSnapshot, Snapshot: last}); err != nil {
		return false, err
	}

	poll := time.NewTicker(c.pollInterval)
	defer poll.Stop()
	var (
		pending   *time.Timer
		pendingC  <-chan time.Time
		lastFetch = time.Now()
	)
	defer func() {
		if pending != nil {
			pending.Stop()
		}
	}()
	refetch := func() error {
		lastFetch = time.Now()
		snap, err := c.Snapshot(ctx)
		if err != nil {
			return err
		}
		// Always emit: the consumer patches its copy with status events, and
		// a periodic full snapshot is what corrects any drift there.
		if err := emit(Update{Kind: UpdateSnapshot, Snapshot: snap}); err != nil {
			return err
		}
		return resubscribe(snap)
	}

	for {
		var err error
		select {
		case <-ctx.Done():
			return true, ctx.Err()
		case err = <-errc:
		case ev := <-statusc:
			err = emit(Update{Kind: UpdateStatus, Status: ev})
		case <-trigger:
			// Coalesce: the first trigger arms a timer; later ones ride on it.
			// The timer also honours minRefetch, so a replay burst yields at
			// most ~1/minRefetch refetches per second.
			if pendingC == nil {
				wait := max(c.debounce, time.Until(lastFetch.Add(c.minRefetch)))
				pending = time.NewTimer(wait)
				pendingC = pending.C
			}
		case <-pendingC:
			pendingC = nil
			err = refetch()
		case <-poll.C:
			err = refetch()
		case <-c.kick:
			err = refetch()
		}
		if err != nil {
			return true, err
		}
	}
}

// agentPanes returns the sorted pane ids of all agents in snap.
func agentPanes(snap model.Snapshot) []string {
	var panes []string
	for _, w := range snap.Workspaces {
		for _, a := range w.Agents {
			panes = append(panes, a.PaneID)
		}
	}
	slices.Sort(panes)
	return panes
}
