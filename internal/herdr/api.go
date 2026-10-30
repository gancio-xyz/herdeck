package herdr

// CONTRACT FILE — written by the integrator. Implementations must keep these
// exported signatures; add unexported helpers in other files.

import (
	"context"

	"github.com/gancio-xyz/herdeck/internal/model"
)

// UpdateKind tells which field of an Update is set.
type UpdateKind int

const (
	// UpdateSnapshot carries a fresh full Snapshot.
	UpdateSnapshot UpdateKind = iota + 1
	// UpdateStatus carries a single live agent status change.
	UpdateStatus
	// UpdateConnected is sent once per successful (re)connect, before the
	// first snapshot of that connection. Info is set.
	UpdateConnected
	// UpdateDisconnected is sent when herdr becomes unreachable. Err is set.
	UpdateDisconnected
)

// Update is one message from Watch.
type Update struct {
	Kind     UpdateKind
	Snapshot model.Snapshot
	Status   model.AgentStatusEvent
	Info     model.ServerInfo
	Err      error
}

// API is what the rest of herdeck needs from herdr. *Client implements it;
// tests can substitute a fake.
type API interface {
	Ping(ctx context.Context) (model.ServerInfo, error)
	Snapshot(ctx context.Context) (model.Snapshot, error)
	FocusWorkspace(ctx context.Context, workspaceID string) error
	FocusAgent(ctx context.Context, paneID string) error
	// Read returns the last `lines` lines of a pane's recent output as plain
	// text (ANSI stripped).
	Read(ctx context.Context, paneID string, lines int) (string, error)
	SendKeys(ctx context.Context, paneID string, keys []string) error
	// Watch runs until ctx is done, reconnecting with exponential backoff
	// (capped at 10 s) and re-snapshotting after each reconnect. The channel
	// is closed when ctx is done.
	Watch(ctx context.Context) <-chan Update
}

var _ API = (*Client)(nil)
