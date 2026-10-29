package render

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"sync"
)

var (
	trayOnce sync.Once
	trayPNG  []byte
)

// TrayIconSize is the pixel size of TrayIcon. fyne.io/systray draws the
// menu-bar icon at 16×16 pt, so 32 px is crisp on Retina displays.
const TrayIconSize = 32

// TrayIcon returns a PNG template image (black on transparent) for the
// macOS menu bar: a 4×2 grid of rounded keys above an info-bar strip,
// suggesting the Stream Deck Neo.
func TrayIcon() []byte {
	trayOnce.Do(func() {
		img := image.NewNRGBA(image.Rect(0, 0, TrayIconSize, TrayIconSize))
		type box struct{ x0, y0, x1, y1, r float64 }
		var boxes []box
		const key, gap, left, top = 6.0, 2.0, 1.0, 6.0
		for row := 0; row < 2; row++ {
			for col := 0; col < 4; col++ {
				x := left + float64(col)*(key+gap)
				y := top + float64(row)*(key+gap)
				boxes = append(boxes, box{x, y, x + key, y + key, 1.5})
			}
		}
		boxes = append(boxes, box{left + key + gap, top + 2*key + 2*gap, left + 3*key + 3*gap - gap, top + 2*key + 2*gap + 4, 1})

		inside := func(b box, x, y float64) bool {
			if x < b.x0 || x > b.x1 || y < b.y0 || y > b.y1 {
				return false
			}
			cx := min(max(x, b.x0+b.r), b.x1-b.r)
			cy := min(max(y, b.y0+b.r), b.y1-b.r)
			dx, dy := x-cx, y-cy
			return dx*dx+dy*dy <= b.r*b.r
		}
		const ss = 4 // supersampling for antialiased corners
		for py := 0; py < TrayIconSize; py++ {
			for px := 0; px < TrayIconSize; px++ {
				n := 0
				for sy := 0; sy < ss; sy++ {
					for sx := 0; sx < ss; sx++ {
						x := float64(px) + (float64(sx)+0.5)/ss
						y := float64(py) + (float64(sy)+0.5)/ss
						for _, b := range boxes {
							if inside(b, x, y) {
								n++
								break
							}
						}
					}
				}
				img.SetNRGBA(px, py, color.NRGBA{0, 0, 0, uint8(n * 255 / (ss * ss))})
			}
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			panic("render: encode tray icon: " + err.Error())
		}
		trayPNG = buf.Bytes()
	})
	return bytes.Clone(trayPNG)
}
