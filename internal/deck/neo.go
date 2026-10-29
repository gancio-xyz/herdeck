package deck

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"sync"
	"time"

	"rafaelmartins.com/p/streamdeck"
)

// rawDevice is what the connector needs from a discovered (not yet open)
// deck. neoDeck implements it over the real library; tests use a fake.
type rawDevice interface {
	Deck
	Open() error
	Model() string
	Firmware() (string, error)
	// Listen registers input handlers, then blocks until the device fails
	// (unplug) or is closed. emit may be called from several goroutines.
	Listen(emit func(InputEvent)) error
}

// finder enumerates the Neos currently attached.
type finder func() ([]rawDevice, error)

func checkKey(idx int) error {
	if idx < 0 || idx >= KeyCount {
		return fmt.Errorf("deck: key index %d out of range [0,%d)", idx, KeyCount)
	}
	return nil
}

func checkTouch(idx int) error {
	if idx < 0 || idx >= TouchCount {
		return fmt.Errorf("deck: touch index %d out of range [0,%d)", idx, TouchCount)
	}
	return nil
}

func clampPct(pct int) int { return min(max(pct, 0), 100) }

// neoDeck is a Deck over a real Stream Deck Neo.
type neoDeck struct {
	dev *streamdeck.Device
}

func keyID(idx int) streamdeck.KeyID { return streamdeck.KEY_1 + streamdeck.KeyID(idx) }

// touchID maps TouchPrev to TOUCH_POINT_1 and TouchNext to TOUCH_POINT_2.
// Assumption: TOUCH_POINT_1 is the left pad. Needs a hardware check.
func touchID(idx int) streamdeck.TouchPointID {
	return streamdeck.TOUCH_POINT_1 + streamdeck.TouchPointID(idx)
}

func (n *neoDeck) SetKeyImage(idx int, img image.Image) error {
	if err := checkKey(idx); err != nil {
		return err
	}
	return n.dev.SetKeyImage(keyID(idx), img)
}

func (n *neoDeck) SetInfoBar(img image.Image) error { return n.dev.SetInfoBarImage(img) }

func (n *neoDeck) SetTouchColor(idx int, c color.Color) error {
	if err := checkTouch(idx); err != nil {
		return err
	}
	return n.dev.SetTouchPointColor(touchID(idx), c)
}

func (n *neoDeck) SetBrightness(pct int) error { return n.dev.SetBrightness(byte(clampPct(pct))) }
func (n *neoDeck) Serial() string              { return n.dev.GetSerialNumber() }
func (n *neoDeck) Close() error                { return n.dev.Close() }
func (n *neoDeck) Open() error                 { return n.dev.Open() }
func (n *neoDeck) Model() string               { return n.dev.GetModelName() }
func (n *neoDeck) Firmware() (string, error)   { return n.dev.GetFirmwareVersion() }

// Listen runs the library listen loop. The library starts every handler in
// its own goroutine per press, so blocking in WaitForRelease does not stall
// other inputs. If the deck is unplugged while a key is held, the release
// never arrives and that handler goroutine stays parked; the connector drops
// anything it emits after the session ends.
func (n *neoDeck) Listen(emit func(InputEvent)) error {
	for i := range KeyCount {
		err := n.dev.AddKeyHandler(keyID(i), func(_ *streamdeck.Device, k *streamdeck.Key) error {
			at := time.Now()
			emit(InputEvent{Kind: KeyDown, Index: i, At: at})
			emit(InputEvent{Kind: KeyUp, Index: i, At: at.Add(k.WaitForRelease())})
			return nil
		})
		if err != nil {
			return err
		}
	}
	for i := range TouchCount {
		err := n.dev.AddTouchPointHandler(touchID(i), func(_ *streamdeck.Device, t *streamdeck.TouchPoint) error {
			at := time.Now()
			emit(InputEvent{Kind: TouchDown, Index: i, At: at})
			emit(InputEvent{Kind: TouchUp, Index: i, At: at.Add(t.WaitForRelease())})
			return nil
		})
		if err != nil {
			return err
		}
	}
	// Handlers never return errors, so there is nothing to collect here.
	return n.dev.Listen(make(chan error, 1))
}

// findNeos enumerates attached Stream Decks and keeps only Neos.
func findNeos() ([]rawDevice, error) {
	devs, err := streamdeck.Enumerate()
	if err != nil {
		return nil, err
	}
	var out []rawDevice
	for _, d := range devs {
		if d.GetModelID() == "neo" {
			out = append(out, &neoDeck{dev: d})
		}
	}
	return out, nil
}

// List describes every attached Neo. Each one is opened briefly to read the
// firmware version; a deck that cannot be opened is reported as Busy.
func List() ([]Info, error) { return list(findNeos) }

func list(find finder) ([]Info, error) {
	devs, err := find()
	if err != nil {
		return nil, err
	}
	infos := make([]Info, 0, len(devs))
	for _, d := range devs {
		info := Info{Serial: d.Serial(), Model: d.Model()}
		if err := d.Open(); err != nil {
			info.Busy = true
		} else {
			info.Firmware, _ = d.Firmware()
			d.Close()
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// NewConnector returns a Connector for the Neo with the given serial, or the
// first Neo found when serial is empty.
func NewConnector(serial string) Connector {
	return &connector{serial: serial, find: findNeos, poll: time.Second}
}

type connector struct {
	serial string
	find   finder
	poll   time.Duration
}

// open finds and opens the configured deck. Errors wrap ErrNotFound or
// ErrBusy where they apply.
func (c *connector) open() (rawDevice, error) {
	devs, err := c.find()
	if err != nil {
		return nil, fmt.Errorf("deck: enumerate: %w", err)
	}
	for _, d := range devs {
		if c.serial != "" && d.Serial() != c.serial {
			continue
		}
		if err := d.Open(); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrBusy, err)
		}
		return d, nil
	}
	if c.serial != "" {
		return nil, fmt.Errorf("%w (serial %q)", ErrNotFound, c.serial)
	}
	return nil, ErrNotFound
}

// errKind buckets errors so repeated identical failures are reported once.
func errKind(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, ErrNotFound):
		return 1
	case errors.Is(err, ErrBusy):
		return 2
	default:
		return 3
	}
}

func (c *connector) Run(ctx context.Context) <-chan Event {
	ch := make(chan Event, 16)
	go c.loop(ctx, ch)
	return ch
}

func (c *connector) loop(ctx context.Context, ch chan<- Event) {
	defer close(ch)
	send := func(ev Event) bool {
		select {
		case ch <- ev:
			return true
		case <-ctx.Done():
			return false
		}
	}
	var last error // last reported failure; nil while connected
	for ctx.Err() == nil {
		dev, err := c.open()
		if err == nil {
			last = nil
			if !send(Event{Kind: EventConnected, Deck: dev}) {
				dev.Close()
				return
			}
			err = c.serve(ctx, dev, ch)
			if ctx.Err() != nil {
				return
			}
			err = fmt.Errorf("deck: disconnected: %w", err)
		}
		if errKind(err) != errKind(last) {
			last = err
			if !send(Event{Kind: EventDisconnected, Err: err}) {
				return
			}
		}
		select {
		case <-time.After(c.poll):
		case <-ctx.Done():
		}
	}
}

// serve forwards input from an open deck until it fails or ctx is done,
// then closes it.
func (c *connector) serve(ctx context.Context, dev rawDevice, ch chan<- Event) error {
	s := &session{ctx: ctx, ch: ch}
	done := make(chan error, 1)
	go func() { done <- dev.Listen(s.emit) }()

	var err error
	select {
	case err = <-done:
		if err == nil {
			err = errors.New("listener stopped")
		}
		s.end()
		dev.Close()
	case <-ctx.Done():
		s.end()
		dev.Close()
		// Closing stops the listener; don't hang shutdown if the library
		// doesn't unblock promptly.
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	}
	return err
}

// session gates input from library handler goroutines: once ended (deck gone
// or ctx done), late events are dropped, so nothing is sent for a stale deck
// or on the closed channel.
type session struct {
	ctx  context.Context
	ch   chan<- Event
	mu   sync.Mutex
	dead bool
}

func (s *session) emit(in InputEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dead {
		return
	}
	select {
	case s.ch <- Event{Kind: EventInput, Input: in}:
	case <-s.ctx.Done():
	}
}

func (s *session) end() {
	s.mu.Lock()
	s.dead = true
	s.mu.Unlock()
}
