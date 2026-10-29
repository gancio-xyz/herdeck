package deck

import (
	"context"
	"errors"
	"image"
	"image/color"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeRaw stands in for a discovered library device.
type fakeRaw struct {
	*FakeDeck
	openErr error
	inputs  chan InputEvent
	fail    chan error
	closed  chan struct{}
	once    sync.Once
}

func newRaw(serial string, openErr error) *fakeRaw {
	return &fakeRaw{
		FakeDeck: NewFakeDeck(serial),
		openErr:  openErr,
		inputs:   make(chan InputEvent),
		fail:     make(chan error, 1),
		closed:   make(chan struct{}),
	}
}

func (r *fakeRaw) Open() error               { return r.openErr }
func (r *fakeRaw) Model() string             { return "Stream Deck Neo" }
func (r *fakeRaw) Firmware() (string, error) { return "1.2.3", nil }

func (r *fakeRaw) Close() error {
	r.once.Do(func() { close(r.closed) })
	return r.FakeDeck.Close()
}

func (r *fakeRaw) Listen(emit func(InputEvent)) error {
	for {
		select {
		case in := <-r.inputs:
			emit(in)
		case err := <-r.fail:
			return err
		case <-r.closed:
			return nil
		}
	}
}

// bus is a fake USB bus: the set of plugged devices and a find counter.
type bus struct {
	mu    sync.Mutex
	devs  []rawDevice
	err   error
	finds atomic.Int32
}

func (b *bus) plug(devs ...rawDevice) {
	b.mu.Lock()
	b.devs = devs
	b.mu.Unlock()
}

func (b *bus) find() ([]rawDevice, error) {
	b.finds.Add(1)
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]rawDevice(nil), b.devs...), b.err
}

func newTestConnector(b *bus, serial string) *connector {
	return &connector{serial: serial, find: b.find, poll: 2 * time.Millisecond}
}

func next(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("channel closed")
		}
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
	}
	return Event{}
}

// quiet asserts no event arrives while the connector polls a few more times.
func quiet(t *testing.T, b *bus, ch <-chan Event) {
	t.Helper()
	target := b.finds.Load() + 5
	for b.finds.Load() < target {
		select {
		case ev := <-ch:
			t.Fatalf("unexpected event %+v", ev)
		case <-time.After(time.Millisecond):
		}
	}
}

func waitClosed(t *testing.T, ch <-chan Event) {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-timeout:
			t.Fatal("channel not closed")
		}
	}
}

func TestRunLifecycle(t *testing.T) {
	b := &bus{}
	d1 := newRaw("A", nil)
	b.plug(d1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := newTestConnector(b, "").Run(ctx)

	ev := next(t, ch)
	if ev.Kind != EventConnected || ev.Deck != d1 {
		t.Fatalf("want connected d1, got %+v", ev)
	}

	in := InputEvent{Kind: KeyDown, Index: 3, At: time.Unix(1, 0)}
	d1.inputs <- in
	if ev := next(t, ch); ev.Kind != EventInput || ev.Input != in {
		t.Fatalf("want input %+v, got %+v", in, ev)
	}

	// Unplug: listener fails, bus is empty.
	b.plug()
	d1.fail <- errors.New("io error")
	ev = next(t, ch)
	if ev.Kind != EventDisconnected || ev.Err == nil || errors.Is(ev.Err, ErrNotFound) {
		t.Fatalf("want disconnected with listen error, got %+v", ev)
	}
	if !d1.Closed() {
		t.Fatal("unplugged device not closed")
	}
	// The kind changes to not-found once, then repeats are suppressed.
	if ev := next(t, ch); ev.Kind != EventDisconnected || !errors.Is(ev.Err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %+v", ev)
	}
	quiet(t, b, ch)

	d2 := newRaw("A", nil)
	b.plug(d2)
	if ev := next(t, ch); ev.Kind != EventConnected || ev.Deck != d2 {
		t.Fatalf("want connected d2, got %+v", ev)
	}

	cancel()
	waitClosed(t, ch)
	if !d2.Closed() {
		t.Fatal("device not closed on cancel")
	}
}

func TestRunBusyDedupAndRecover(t *testing.T) {
	b := &bus{}
	lock := errors.New("locked by another application")
	b.plug(newRaw("A", lock))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := newTestConnector(b, "").Run(ctx)

	ev := next(t, ch)
	if ev.Kind != EventDisconnected || !errors.Is(ev.Err, ErrBusy) || !errors.Is(ev.Err, lock) {
		t.Fatalf("want ErrBusy wrapping cause, got %+v", ev)
	}
	quiet(t, b, ch)

	b.plug()
	if ev := next(t, ch); !errors.Is(ev.Err, ErrNotFound) {
		t.Fatalf("want ErrNotFound after kind change, got %+v", ev)
	}

	b.plug(newRaw("A", nil))
	if ev := next(t, ch); ev.Kind != EventConnected {
		t.Fatalf("want connected, got %+v", ev)
	}
}

func TestRunEnumerateError(t *testing.T) {
	b := &bus{err: errors.New("iokit")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := newTestConnector(b, "").Run(ctx)
	ev := next(t, ch)
	if ev.Kind != EventDisconnected || errors.Is(ev.Err, ErrNotFound) || errors.Is(ev.Err, ErrBusy) {
		t.Fatalf("want other error, got %+v", ev)
	}
	quiet(t, b, ch)
}

func TestRunSerialFilter(t *testing.T) {
	b := &bus{}
	a, want := newRaw("A", errors.New("busy")), newRaw("B", nil)
	b.plug(a, want)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := newTestConnector(b, "B").Run(ctx)
	if ev := next(t, ch); ev.Deck != want {
		t.Fatalf("want deck B, got %+v", ev)
	}

	ch2 := newTestConnector(&bus{}, "Z").Run(ctx)
	if ev := next(t, ch2); !errors.Is(ev.Err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %+v", ev)
	}
}

// Cancelling while nobody reads must not deadlock, even with input pending.
func TestRunCancelWithoutReader(t *testing.T) {
	b := &bus{}
	d := newRaw("A", nil)
	b.plug(d)
	ctx, cancel := context.WithCancel(context.Background())
	ch := newTestConnector(b, "").Run(ctx)
	next(t, ch)

	// Fill the buffer so further emits block.
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case d.inputs <- InputEvent{Kind: KeyDown}:
			case <-stop:
				return
			}
		}
	}()
	for len(ch) < cap(ch) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	waitClosed(t, ch)
	close(stop)
	if !d.Closed() {
		t.Fatal("device not closed")
	}
}

func TestList(t *testing.T) {
	b := &bus{}
	b.plug(newRaw("A", nil), newRaw("B", errors.New("locked")))
	infos, err := list(b.find)
	if err != nil {
		t.Fatal(err)
	}
	want := []Info{
		{Serial: "A", Model: "Stream Deck Neo", Firmware: "1.2.3"},
		{Serial: "B", Model: "Stream Deck Neo", Busy: true},
	}
	if len(infos) != len(want) || infos[0] != want[0] || infos[1] != want[1] {
		t.Fatalf("got %+v, want %+v", infos, want)
	}
	if !b.devs[0].(*fakeRaw).Closed() {
		t.Fatal("List left device open")
	}
}

func TestNeoIndexValidation(t *testing.T) {
	// Out-of-range indexes are rejected before the (nil) library device is
	// touched.
	n := &neoDeck{}
	img := image.NewRGBA(image.Rect(0, 0, KeySize, KeySize))
	for _, idx := range []int{-1, KeyCount} {
		if err := n.SetKeyImage(idx, img); err == nil {
			t.Errorf("SetKeyImage(%d): want error", idx)
		}
	}
	for _, idx := range []int{-1, TouchCount} {
		if err := n.SetTouchColor(idx, color.White); err == nil {
			t.Errorf("SetTouchColor(%d): want error", idx)
		}
	}
	if keyID(7) != keyID(0)+7 || touchID(TouchNext) != touchID(TouchPrev)+1 {
		t.Error("bad id mapping")
	}
}

func TestClampPct(t *testing.T) {
	for in, want := range map[int]int{-5: 0, 0: 0, 50: 50, 100: 100, 250: 100} {
		if got := clampPct(in); got != want {
			t.Errorf("clampPct(%d) = %d, want %d", in, got, want)
		}
	}
}
