// Package herdrtest provides a scriptable fake herdr server on a unix socket.
package herdrtest

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
)

// Request is one request received by the server.
type Request struct {
	ID     string
	Method string
	Params json.RawMessage
}

// Error is a herdr error response.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Handler answers one method. A non-nil *Error is sent as the error response;
// otherwise result is marshalled as the result.
type Handler func(params json.RawMessage) (result any, err *Error)

// Subscription is one open events.subscribe connection.
type Subscription struct {
	Subs []map[string]string
}

// Server is a fake herdr. Default methods: ping, workspace.list, agent.list
// and events.subscribe; anything else needs Handle. All methods are safe for
// concurrent use.
type Server struct {
	Path string

	mu             sync.Mutex
	ln             net.Listener
	conns          map[*conn]struct{}
	wg             sync.WaitGroup
	version        string
	protocol       int
	workspaces     json.RawMessage
	agents         json.RawMessage
	backlog        []string
	replayInterval time.Duration
	handlers       map[string]Handler
	requests       []Request
}

type conn struct {
	net.Conn
	wmu  sync.Mutex
	subs []map[string]string // nil until events.subscribe
	done chan struct{}
}

func (c *conn) writeLine(b []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err := c.Write(append(b, '\n'))
	return err
}

// New starts a server on a fresh short temp path (macOS limits unix socket
// paths to ~104 bytes) and stops it at test cleanup.
func New(t testing.TB) *Server {
	t.Helper()
	dir, err := os.MkdirTemp("", "hd")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		Path:       filepath.Join(dir, "s.sock"),
		conns:      map[*conn]struct{}{},
		version:    "0.7.1",
		protocol:   14,
		workspaces: json.RawMessage("[]"),
		agents:     json.RawMessage("[]"),
		handlers:   map[string]Handler{},
	}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.Stop()
		os.RemoveAll(dir)
	})
	return s
}

// Start (re)listens on Path.
func (s *Server) Start() error {
	ln, err := net.Listen("unix", s.Path)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.ln = ln
	s.mu.Unlock()
	s.wg.Go(func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			c := &conn{Conn: nc, done: make(chan struct{})}
			s.mu.Lock()
			if s.ln != ln { // Stop raced with Accept
				s.mu.Unlock()
				nc.Close()
				return
			}
			s.conns[c] = struct{}{}
			s.mu.Unlock()
			s.wg.Go(func() { s.serve(c) })
		}
	})
	return nil
}

// Stop closes the listener and every open connection, removing the socket.
func (s *Server) Stop() {
	s.mu.Lock()
	if s.ln != nil {
		s.ln.Close()
		s.ln = nil
	}
	for c := range s.conns {
		c.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

// SetPing sets the version and protocol returned by ping.
func (s *Server) SetPing(version string, protocol int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.version, s.protocol = version, protocol
}

// SetWorkspaces sets the JSON array returned as workspace.list's workspaces.
func (s *Server) SetWorkspaces(raw string) {
	s.mu.Lock()
	s.workspaces = json.RawMessage(raw)
	s.mu.Unlock()
}

// SetAgents sets the JSON array returned as agent.list's agents.
func (s *Server) SetAgents(raw string) { s.mu.Lock(); s.agents = json.RawMessage(raw); s.mu.Unlock() }

// SetBacklog sets raw event lines replayed, interval apart, to every new
// subscription that includes a non-status type (herdr's history replay).
func (s *Server) SetBacklog(lines []string, interval time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.backlog, s.replayInterval = lines, interval
}

// Handle installs or overrides the handler for method.
func (s *Server) Handle(method string, h Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[method] = h
}

// Requests returns a copy of all requests received so far.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

// Count returns how many requests for method were received.
func (s *Server) Count(method string) int {
	n := 0
	for _, r := range s.Requests() {
		if r.Method == method {
			n++
		}
	}
	return n
}

// Subscriptions returns the currently open subscription connections.
func (s *Server) Subscriptions() []Subscription {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Subscription
	for c := range s.conns {
		if c.subs != nil {
			out = append(out, Subscription{Subs: c.subs})
		}
	}
	return out
}

// PushRaw writes line verbatim to every open subscription connection.
func (s *Server) PushRaw(line string) {
	s.mu.Lock()
	var subs []*conn
	for c := range s.conns {
		if c.subs != nil {
			subs = append(subs, c)
		}
	}
	s.mu.Unlock()
	for _, c := range subs {
		c.writeLine([]byte(line))
	}
}

// Push sends {"event":event,"data":data} to every open subscription.
func (s *Server) Push(event string, data any) {
	b, _ := json.Marshal(map[string]any{"event": event, "data": data})
	s.PushRaw(string(b))
}

func (s *Server) serve(c *conn) {
	defer func() {
		close(c.done)
		c.Close()
		s.mu.Lock()
		delete(s.conns, c)
		s.mu.Unlock()
	}()
	br := bufio.NewReader(c)
	for {
		line, err := br.ReadBytes('\n')
		if err != nil {
			return
		}
		var req struct {
			ID     string           `json:"id"`
			Method string           `json:"method"`
			Params *json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(line, &req); err != nil || req.Params == nil {
			// Mirrors herdr: unparseable requests are answered with id "".
			c.writeLine(mustJSON(map[string]any{"id": "", "error": Error{"invalid_request", "missing field `params`"}}))
			continue
		}
		s.mu.Lock()
		s.requests = append(s.requests, Request{req.ID, req.Method, *req.Params})
		s.mu.Unlock()

		result, herr := s.dispatch(c, req.Method, *req.Params)
		resp := map[string]any{"id": req.ID}
		if herr != nil {
			resp["error"] = herr
		} else {
			resp["result"] = result
		}
		c.writeLine(mustJSON(resp))
		if req.Method == "events.subscribe" && herr == nil {
			s.replay(c)
		}
	}
}

func (s *Server) dispatch(c *conn, method string, params json.RawMessage) (any, *Error) {
	s.mu.Lock()
	h, ok := s.handlers[method]
	s.mu.Unlock()
	if ok {
		return h(params)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch method {
	case "ping":
		return map[string]any{"type": "pong", "version": s.version, "protocol": s.protocol,
			"capabilities": map[string]bool{"live_handoff": true}}, nil
	case "workspace.list":
		return map[string]any{"type": "workspace_list", "workspaces": s.workspaces}, nil
	case "agent.list":
		return map[string]any{"type": "agent_list", "agents": s.agents}, nil
	case "events.subscribe":
		var p struct {
			Subscriptions []map[string]string `json:"subscriptions"`
		}
		if json.Unmarshal(params, &p) != nil || len(p.Subscriptions) == 0 {
			return nil, &Error{"invalid_request", "missing field `subscriptions`"}
		}
		for _, sub := range p.Subscriptions {
			if sub["type"] == "pane.agent_status_changed" && sub["pane_id"] == "" {
				return nil, &Error{"invalid_request", "missing field `pane_id`"}
			}
		}
		c.subs = p.Subscriptions
		return map[string]any{"type": "subscription_started"}, nil
	}
	return nil, &Error{"unknown_method", "unknown method " + method}
}

// replay streams the backlog to a lifecycle subscription in the background.
func (s *Server) replay(c *conn) {
	s.mu.Lock()
	lines, interval := s.backlog, s.replayInterval
	lifecycle := slices.ContainsFunc(c.subs, func(m map[string]string) bool { return m["type"] != "pane.agent_status_changed" })
	s.mu.Unlock()
	if !lifecycle || len(lines) == 0 {
		return
	}
	s.wg.Go(func() {
		for _, l := range lines {
			select {
			case <-c.done:
				return
			case <-time.After(interval):
			}
			if c.writeLine([]byte(l)) != nil {
				return
			}
		}
	})
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
