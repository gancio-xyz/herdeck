// Package deck talks to the Stream Deck Neo hardware.
package deck

// CONTRACT FILE — written by the integrator. Implementations must keep these
// exported signatures; add unexported helpers in other files.

import (
	"context"
	"errors"
	"image"
	"image/color"
	"time"
)

// Neo geometry.
const (
	KeyCount   = 8
	TouchCount = 2
	KeySize    = 96
	InfoBarW   = 248
	InfoBarH   = 58
	VendorID   = 0x0FD9
	NeoProduct = 0x009A
	TouchPrev  = 0 // left touch point index
	TouchNext  = 1 // right touch point index
)

var (
	// ErrNotFound means no Neo is connected (or none with the requested serial).
	ErrNotFound = errors.New("no Stream Deck Neo found")
	// ErrBusy means the deck exists but could not be opened, usually because
	// the Elgato Stream Deck app holds it.
	ErrBusy = errors.New("Stream Deck Neo is busy (quit the Elgato Stream Deck app)")
)

// Deck is an open device. Key indexes are 0..7 in reading order (top-left is
// 0), touch indexes are TouchPrev/TouchNext. Implementations must be safe for
// use from one goroutine at a time (the app serialises calls).
type Deck interface {
	SetKeyImage(idx int, img image.Image) error
	SetInfoBar(img image.Image) error
	SetTouchColor(idx int, c color.Color) error
	SetBrightness(pct int) error
	Serial() string
	Close() error
}

// InputKind is the kind of a physical input event.
type InputKind int

const (
	KeyDown InputKind = iota + 1
	KeyUp
	TouchDown
	TouchUp
)

// InputEvent is a raw press or release.
type InputEvent struct {
	Kind  InputKind
	Index int
	At    time.Time
}

// EventKind tells which field of an Event is set.
type EventKind int

const (
	// EventConnected: Deck is set and open. The receiver must repaint everything.
	EventConnected EventKind = iota + 1
	// EventDisconnected: the previously connected deck went away or could not
	// be opened. Err is set (ErrNotFound / ErrBusy / other). The Deck from
	// the last EventConnected must no longer be used.
	EventDisconnected
	// EventInput: Input is set.
	EventInput
)

// Event is one message from a Connector.
type Event struct {
	Kind  EventKind
	Deck  Deck
	Input InputEvent
	Err   error
}

// Connector owns device discovery and hot-plug.
type Connector interface {
	// Run opens the deck (first Neo or the configured serial), emits
	// EventConnected, forwards input, and on unplug emits EventDisconnected
	// and keeps polling (about every 1 s) until the deck is back. The channel
	// is closed when ctx is done, after the deck is closed.
	Run(ctx context.Context) <-chan Event
}

// Info describes a connected deck, for `herdeck devices` and doctor.
type Info struct {
	Serial   string
	Model    string
	Firmware string // may be empty if the device could not be opened
	Busy     bool   // device present but not openable
}
