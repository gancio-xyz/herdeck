package deck

import (
	"context"
	"errors"
	"image"
	"image/color"
	"sync"
	"time"
)

var errFakeClosed = errors.New("deck: fake deck is closed")

// FakeDeck is an in-memory Deck for tests. It records the last image per
// key, the info bar image, touch colours, brightness and per-method call
// counts. It is safe for concurrent use. After Close, every method except
// Close returns an error, which catches use of a stale deck.
type FakeDeck struct {
	mu         sync.Mutex
	serial     string
	keys       [KeyCount]image.Image
	infoBar    image.Image
	touch      [TouchCount]color.Color
	brightness int
	calls      map[string]int
	closed     bool
	err        error
}

// NewFakeDeck returns an open fake deck with the given serial.
func NewFakeDeck(serial string) *FakeDeck {
	return &FakeDeck{serial: serial, brightness: -1, calls: map[string]int{}}
}

// Fail makes every subsequent call return err; Fail(nil) clears it.
func (f *FakeDeck) Fail(err error) {
	f.mu.Lock()
	f.err = err
	f.mu.Unlock()
}

// begin counts a call and returns the error it should fail with, if any.
// The caller must hold f.mu.
func (f *FakeDeck) begin(method string) error {
	f.calls[method]++
	if f.err != nil {
		return f.err
	}
	if f.closed {
		return errFakeClosed
	}
	return nil
}

func (f *FakeDeck) SetKeyImage(idx int, img image.Image) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin("SetKeyImage"); err != nil {
		return err
	}
	if err := checkKey(idx); err != nil {
		return err
	}
	f.keys[idx] = img
	return nil
}

func (f *FakeDeck) SetInfoBar(img image.Image) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin("SetInfoBar"); err != nil {
		return err
	}
	f.infoBar = img
	return nil
}

func (f *FakeDeck) SetTouchColor(idx int, c color.Color) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin("SetTouchColor"); err != nil {
		return err
	}
	if err := checkTouch(idx); err != nil {
		return err
	}
	f.touch[idx] = c
	return nil
}

func (f *FakeDeck) SetBrightness(pct int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin("SetBrightness"); err != nil {
		return err
	}
	f.brightness = clampPct(pct)
	return nil
}

func (f *FakeDeck) Serial() string { return f.serial }

// Close marks the deck closed. It is idempotent and ignores Fail.
func (f *FakeDeck) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls["Close"]++
	f.closed = true
	return nil
}

// Key returns the last image set on key idx (nil if none or out of range).
func (f *FakeDeck) Key(idx int) image.Image {
	f.mu.Lock()
	defer f.mu.Unlock()
	if checkKey(idx) != nil {
		return nil
	}
	return f.keys[idx]
}

// InfoBar returns the last info bar image.
func (f *FakeDeck) InfoBar() image.Image {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.infoBar
}

// TouchColor returns the last colour set on touch point idx.
func (f *FakeDeck) TouchColor(idx int) color.Color {
	f.mu.Lock()
	defer f.mu.Unlock()
	if checkTouch(idx) != nil {
		return nil
	}
	return f.touch[idx]
}

// Brightness returns the last brightness set (clamped), or -1 if never set.
func (f *FakeDeck) Brightness() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.brightness
}

// Calls returns how many times the named method (e.g. "SetKeyImage") was
// called, including failed calls.
func (f *FakeDeck) Calls(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[method]
}

// Closed reports whether Close was called.
func (f *FakeDeck) Closed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// FakeEpoch is the initial time of a FakeConnector's clock.
var FakeEpoch = time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)

// FakeConnector is a Connector driven by the test. Events are buffered, so
// Connect/Press/... can be called before or after Run without a reader.
// Input timestamps come from a manual clock starting at FakeEpoch, which only
// moves via Press/Touch (by the hold time) and Advance.
//
// Run may be called once. When its ctx is done, the connected deck is
// closed, the channel is closed, and later calls are no-ops.
type FakeConnector struct {
	mu   sync.Mutex
	ch   chan Event
	done chan struct{}
	deck *FakeDeck
	now  time.Time
	ran  bool
	shut bool
}

// NewFakeConnector returns a FakeConnector with a 64-event buffer.
func NewFakeConnector() *FakeConnector {
	return &FakeConnector{ch: make(chan Event, 64), done: make(chan struct{}), now: FakeEpoch}
}

// Run implements Connector.
func (c *FakeConnector) Run(ctx context.Context) <-chan Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ran {
		panic("deck: FakeConnector.Run called twice")
	}
	c.ran = true
	go func() {
		<-ctx.Done()
		close(c.done) // unblocks a send waiting on a full buffer
		c.mu.Lock()
		defer c.mu.Unlock()
		c.shut = true
		if c.deck != nil {
			c.deck.Close()
		}
		close(c.ch)
	}()
	return c.ch
}

// send queues ev; the caller must hold c.mu.
func (c *FakeConnector) send(ev Event) {
	if c.shut {
		return
	}
	select {
	case c.ch <- ev:
	case <-c.done:
	}
}

// Connect emits EventConnected for d (a fresh NewFakeDeck("FAKE") if nil)
// and returns it.
func (c *FakeConnector) Connect(d *FakeDeck) *FakeDeck {
	if d == nil {
		d = NewFakeDeck("FAKE")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deck = d
	c.send(Event{Kind: EventConnected, Deck: d})
	return d
}

// Disconnect closes the connected deck (if any) and emits EventDisconnected
// with err, or ErrNotFound if err is nil.
func (c *FakeConnector) Disconnect(err error) {
	if err == nil {
		err = ErrNotFound
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.deck != nil {
		c.deck.Close()
		c.deck = nil
	}
	c.send(Event{Kind: EventDisconnected, Err: err})
}

// Deck returns the currently connected fake deck, or nil.
func (c *FakeConnector) Deck() *FakeDeck {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.deck
}

// Input emits a raw input event. A zero At is stamped with the clock.
func (c *FakeConnector) Input(in InputEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if in.At.IsZero() {
		in.At = c.now
	}
	c.send(Event{Kind: EventInput, Input: in})
}

// Press emits KeyDown now and KeyUp hold later, advancing the clock by hold.
func (c *FakeConnector) Press(idx int, hold time.Duration) {
	c.pressRelease(KeyDown, KeyUp, idx, hold)
}

// Touch emits TouchDown now and TouchUp hold later, advancing the clock by
// hold.
func (c *FakeConnector) Touch(idx int, hold time.Duration) {
	c.pressRelease(TouchDown, TouchUp, idx, hold)
}

func (c *FakeConnector) pressRelease(down, up InputKind, idx int, hold time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.send(Event{Kind: EventInput, Input: InputEvent{Kind: down, Index: idx, At: c.now}})
	c.now = c.now.Add(hold)
	c.send(Event{Kind: EventInput, Input: InputEvent{Kind: up, Index: idx, At: c.now}})
}

// Now returns the fake clock.
func (c *FakeConnector) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the fake clock forward by d.
func (c *FakeConnector) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}
