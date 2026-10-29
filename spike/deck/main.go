// Command deck is a throwaway Phase 0 spike: it opens the first Stream Deck
// Neo, paints every key, the info bar and both touch points, then prints raw
// input events until Ctrl+C.
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"log"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"rafaelmartins.com/p/streamdeck"
)

var keyColors = []color.RGBA{
	{0xF5, 0xA5, 0x24, 0xff}, // blocked
	{0x2F, 0x6F, 0xEB, 0xff}, // working
	{0x2D, 0xA4, 0x4E, 0xff}, // done
	{0x3A, 0x3A, 0x3A, 0xff}, // idle
	{0xD0, 0x30, 0x30, 0xff},
	{0x8A, 0x3F, 0xFC, 0xff},
	{0x00, 0xA3, 0xA3, 0xff},
	{0xEE, 0xEE, 0xEE, 0xff},
}

func face(size float64) font.Face {
	f, err := opentype.Parse(gobold.TTF)
	if err != nil {
		log.Fatal(err)
	}
	fc, err := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		log.Fatal(err)
	}
	return fc
}

func textImage(r image.Rectangle, bg, fg color.Color, fc font.Face, lines ...string) image.Image {
	img := image.NewRGBA(r)
	draw.Draw(img, r, image.NewUniform(bg), image.Point{}, draw.Src)
	d := &font.Drawer{Dst: img, Src: image.NewUniform(fg), Face: fc}
	lh := fc.Metrics().Height.Ceil()
	y := (r.Dy()-lh*len(lines))/2 + fc.Metrics().Ascent.Ceil()
	for _, l := range lines {
		d.Dot = fixed.P((r.Dx()-d.MeasureString(l).Ceil())/2, y)
		d.DrawString(l)
		y += lh
	}
	return img
}

func main() {
	serial := ""
	if len(os.Args) > 1 {
		serial = os.Args[1]
	}
	dev, err := streamdeck.GetDevice(serial)
	if err != nil {
		log.Fatalf("get device: %v", err)
	}
	if err := dev.Open(); err != nil {
		log.Fatalf("open device (is the Elgato Stream Deck app running? quit it): %v", err)
	}
	defer dev.Close()

	fw, _ := dev.GetFirmwareVersion()
	log.Printf("model=%s serial=%s firmware=%s keys=%d touch=%d infobar=%v",
		dev.GetModelName(), dev.GetSerialNumber(), fw, dev.GetKeyCount(), dev.GetTouchPointCount(), dev.GetInfoBarSupported())

	if err := dev.SetBrightness(70); err != nil {
		log.Printf("brightness: %v", err)
	}

	keyRect, err := dev.GetKeyImageRectangle()
	if err != nil {
		log.Fatal(err)
	}
	big, small := face(28), face(16)
	err = dev.ForEachKey(func(k streamdeck.KeyID) error {
		i := int(k - streamdeck.KEY_1)
		c := keyColors[i%len(keyColors)]
		fg := color.Color(color.White)
		if i == 7 {
			fg = color.Black
		}
		return dev.SetKeyImage(k, textImage(keyRect, c, fg, big, fmt.Sprint(i+1)))
	})
	if err != nil {
		log.Fatalf("paint keys: %v", err)
	}

	if dev.GetInfoBarSupported() {
		barRect, _ := dev.GetInfoBarImageRectangle()
		bar := textImage(barRect, color.Black, color.White, small, "herdeck spike", "1 blocked · 1 working")
		if err := dev.SetInfoBarImage(bar); err != nil {
			log.Printf("info bar: %v", err)
		}
	}

	dev.ForEachTouchPoint(func(tp streamdeck.TouchPointID) error {
		c := color.RGBA{0x2F, 0x6F, 0xEB, 0xff}
		if tp == streamdeck.TOUCH_POINT_2 {
			c = color.RGBA{0xF5, 0xA5, 0x24, 0xff}
		}
		if err := dev.SetTouchPointColor(tp, c); err != nil {
			log.Printf("touch %s color: %v", tp, err)
		}
		return dev.AddTouchPointHandler(tp, func(d *streamdeck.Device, t *streamdeck.TouchPoint) error {
			log.Printf("%s down", t)
			log.Printf("%s up after %v", t, t.WaitForRelease())
			return nil
		})
	})

	dev.ForEachKey(func(k streamdeck.KeyID) error {
		return dev.AddKeyHandler(k, func(d *streamdeck.Device, key *streamdeck.Key) error {
			log.Printf("%s down", key)
			log.Printf("%s up after %v", key, key.WaitForRelease())
			return nil
		})
	})

	errCh := make(chan error, 8)
	go func() {
		for err := range errCh {
			log.Printf("handler error: %v", err)
		}
	}()
	done := make(chan error, 1)
	go func() { done <- dev.Listen(errCh) }()

	log.Println("press keys and touch points; unplug the deck to see the error; Ctrl+C to exit")
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case <-sig:
	case err := <-done:
		log.Printf("listen ended: %v", err)
	}
}
