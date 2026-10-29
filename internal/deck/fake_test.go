package deck

import (
	"context"
	"errors"
	"image"
	"image/color"
	"testing"
	"time"
)

var _ Deck = (*FakeDeck)(nil)
var _ Connector = (*FakeConnector)(nil)
var _ rawDevice = (*neoDeck)(nil)

func TestFakeDeck(t *testing.T) {
	d := NewFakeDeck("S1")
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	if d.Serial() != "S1" || d.Brightness() != -1 {
		t.Fatal("bad initial state")
	}
	if err := d.SetKeyImage(2, img); err != nil {
		t.Fatal(err)
	}
	if err := d.SetKeyImage(KeyCount, img); err == nil {
		t.Fatal("want index error")
	}
	d.SetInfoBar(img)
	d.SetTouchColor(TouchNext, color.White)
	d.SetBrightness(140)

	if d.Key(2) != img || d.Key(0) != nil || d.Key(-1) != nil {
		t.Error("key images not recorded")
	}
	if d.InfoBar() != img || d.TouchColor(TouchNext) != color.White || d.TouchColor(TouchPrev) != nil {
		t.Error("info bar / touch not recorded")
	}
	if d.Brightness() != 100 {
		t.Errorf("brightness = %d, want clamped 100", d.Brightness())
	}
	if d.Calls("SetKeyImage") != 2 {
		t.Errorf("SetKeyImage calls = %d, want 2", d.Calls("SetKeyImage"))
	}

	boom := errors.New("boom")
	d.Fail(boom)
	if err := d.SetBrightness(10); !errors.Is(err, boom) || d.Brightness() != 100 {
		t.Error("Fail not honoured")
	}
	d.Fail(nil)
	d.Close()
	if !d.Closed() || d.SetInfoBar(img) == nil {
		t.Error("closed deck should reject calls")
	}
}

func TestFakeConnector(t *testing.T) {
	c := NewFakeConnector()
	d := c.Connect(nil) // before Run: buffered
	ctx, cancel := context.WithCancel(context.Background())
	ch := c.Run(ctx)

	if ev := next(t, ch); ev.Kind != EventConnected || ev.Deck != d || c.Deck() != d {
		t.Fatalf("got %+v", ev)
	}

	c.Press(4, 600*time.Millisecond)
	down, up := next(t, ch).Input, next(t, ch).Input
	if down.Kind != KeyDown || up.Kind != KeyUp || down.Index != 4 || up.Index != 4 {
		t.Fatalf("got %+v %+v", down, up)
	}
	if !down.At.Equal(FakeEpoch) || up.At.Sub(down.At) != 600*time.Millisecond {
		t.Fatalf("bad timestamps %v %v", down.At, up.At)
	}

	c.Advance(time.Second)
	c.Touch(TouchPrev, 0)
	if in := next(t, ch).Input; in.Kind != TouchDown || !in.At.Equal(FakeEpoch.Add(1600*time.Millisecond)) {
		t.Fatalf("got %+v", in)
	}
	next(t, ch)

	c.Input(InputEvent{Kind: KeyDown, Index: 1})
	if in := next(t, ch).Input; !in.At.Equal(c.Now()) {
		t.Fatalf("zero At not stamped: %+v", in)
	}

	c.Disconnect(nil)
	if ev := next(t, ch); ev.Kind != EventDisconnected || !errors.Is(ev.Err, ErrNotFound) || !d.Closed() {
		t.Fatalf("got %+v", ev)
	}

	d2 := c.Connect(NewFakeDeck("S2"))
	next(t, ch)
	cancel()
	waitClosed(t, ch)
	if !d2.Closed() {
		t.Fatal("deck not closed on cancel")
	}
	c.Press(0, 0) // no-op after shutdown, must not panic
}

// A full buffer with no reader must not block shutdown.
func TestFakeConnectorCancelFullBuffer(t *testing.T) {
	c := NewFakeConnector()
	ctx, cancel := context.WithCancel(context.Background())
	ch := c.Run(ctx)
	done := make(chan struct{})
	go func() {
		for range cap(ch) + 5 {
			c.Input(InputEvent{Kind: KeyDown})
		}
		close(done)
	}()
	for len(ch) < cap(ch) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("sender blocked after cancel")
	}
	waitClosed(t, ch)
}
