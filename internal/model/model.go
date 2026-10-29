// Package model holds the plain data types shared by the herdr client, the
// core state machine and the renderer. It has no behaviour beyond small
// helpers and no I/O.
package model

// Status is an agent (or aggregated workspace) state as reported by herdr.
type Status string

const (
	StatusIdle    Status = "idle"
	StatusWorking Status = "working"
	StatusBlocked Status = "blocked"
	StatusDone    Status = "done"
	StatusUnknown Status = "unknown"
)

// Rank orders statuses by urgency: lower is more urgent.
func (s Status) Rank() int {
	switch s {
	case StatusBlocked:
		return 0
	case StatusWorking:
		return 1
	case StatusDone:
		return 2
	case StatusIdle:
		return 3
	default:
		return 4
	}
}

// ParseStatus maps a herdr status string to a Status, defaulting to unknown.
func ParseStatus(s string) Status {
	switch st := Status(s); st {
	case StatusIdle, StatusWorking, StatusBlocked, StatusDone:
		return st
	default:
		return StatusUnknown
	}
}

// Agent is one agent-bearing pane.
type Agent struct {
	PaneID      string
	TerminalID  string
	WorkspaceID string
	TabID       string
	Name        string // agent label, e.g. "claude"
	Status      Status
	// CustomStatus is only delivered through pane.agent_status_changed
	// events; snapshots leave it empty.
	CustomStatus string
	Focused      bool
}

// Workspace is a herdr workspace with the agents it contains.
type Workspace struct {
	ID      string
	Number  int
	Label   string
	Status  Status // aggregated by herdr across the workspace's panes
	Focused bool
	Agents  []Agent
}

// Snapshot is the full herdr state relevant to herdeck.
type Snapshot struct {
	Workspaces []Workspace // in herdr order (by Number); includes workspaces without agents
}

// AgentStatusEvent is a pane.agent_status_changed event.
type AgentStatusEvent struct {
	PaneID       string
	WorkspaceID  string
	Agent        string // empty when the agent was released
	Status       Status
	CustomStatus string
}

// ServerInfo is the result of a herdr ping.
type ServerInfo struct {
	Version  string
	Protocol int
}

// Supported herdr socket protocol range (inclusive).
const (
	MinProtocol = 14
	MaxProtocol = 14
)
