package render

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
)

// Neo geometry.
const (
	KeySize = 96
	BarW    = 248
	BarH    = 58
)

// Key layout. The text inset is fixed regardless of borders so that pulse
// frames never reflow the title (no jitter while animating).
const (
	keyInset      = 8  // text never comes closer than this to the edge
	focusWidth    = 3  // white frame for the herdr-focused workspace
	armedWidth    = 6  // Armed-colored frame
	pulseStep     = 2  // pulse border width per phase (2, 4, 6 px)
	titleMinSize  = 13 // px, bold
	titleMaxSize  = 26
	titleMaxLines = 3
	subSize       = 13 // px, regular; also used for the count badge (bold)
	subBaseline   = 86 // bottom row baseline
	subTop        = 75 // title area ends here when a bottom row is shown
	badgeSize     = 11 // "ARMED" badge font, bold
	badgeH        = 17
)

var (
	white = color.RGBA{0xff, 0xff, 0xff, 0xff}
	black = color.RGBA{0, 0, 0, 0xff}
)

func fill(dst *image.RGBA, r image.Rectangle, c color.Color) {
	draw.Draw(dst, r, image.NewUniform(c), image.Point{}, draw.Src)
}

// frame draws a w-px border just inside r.
func frame(dst *image.RGBA, r image.Rectangle, w int, c color.Color) {
	fill(dst, image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+w), c)
	fill(dst, image.Rect(r.Min.X, r.Max.Y-w, r.Max.X, r.Max.Y), c)
	fill(dst, image.Rect(r.Min.X, r.Min.Y, r.Min.X+w, r.Max.Y), c)
	fill(dst, image.Rect(r.Max.X-w, r.Min.Y, r.Max.X, r.Max.Y), c)
}

// pill fills a rectangle with 2px-clipped corners (cheap rounded look).
func pill(dst *image.RGBA, r image.Rectangle, c color.Color) {
	fill(dst, image.Rect(r.Min.X+2, r.Min.Y, r.Max.X-2, r.Max.Y), c)
	fill(dst, image.Rect(r.Min.X+1, r.Min.Y+1, r.Max.X-1, r.Max.Y-1), c)
	fill(dst, image.Rect(r.Min.X, r.Min.Y+2, r.Max.X, r.Max.Y-2), c)
}

// Key renders a 96×96 key image for t.
func Key(t Tile, th Theme) image.Image {
	th = th.withDefaults()
	img := image.NewRGBA(image.Rect(0, 0, KeySize, KeySize))
	full := img.Bounds()
	if t.Empty {
		fill(img, full, th.Empty)
		return img
	}
	bg := th.statusColor(t.Status)
	if t.Alert {
		bg = th.Alert
	}
	if t.Pressed {
		bg = mix(bg, white, 0.35)
	}
	fg := textColor(bg)
	fill(img, full, bg)

	mu.Lock()
	defer mu.Unlock()

	// Borders, outermost semantics last so they win.
	if t.Pulse > 0 {
		p := min(t.Pulse, 3)
		// Darken rather than lighten so the pulse never looks like the
		// white focus frame.
		frame(img, full, p*pulseStep, mix(bg, black, 0.2+0.15*float64(p)))
	}
	if t.Armed {
		frame(img, full, armedWidth, th.Armed)
		if t.Focused {
			frame(img, full.Inset(armedWidth), 2, white)
		}
	} else if t.Focused {
		frame(img, full, focusWidth, white)
	}

	titleTop := keyInset
	if t.Armed {
		bfc := face(bold, badgeSize)
		const label = "ARMED"
		w := measure(bfc, label) + 12
		r := image.Rect((KeySize-w)/2, 0, (KeySize+w)/2, badgeH)
		pill(img, r, th.Armed)
		fill(img, image.Rect(r.Min.X, 0, r.Max.X, 3), th.Armed) // merge with top border
		drawString(img, bfc, textColor(th.Armed), r.Min.X+6, badgeH-4, label)
		titleTop = badgeH + 3
	}

	titleBottom := KeySize - keyInset
	if t.Subtitle != "" || t.Count > 1 {
		titleBottom = subTop
		textW := KeySize - 2*keyInset
		subRight := KeySize - keyInset
		if t.Count > 1 {
			cfc := face(bold, subSize)
			label := fmt.Sprintf("×%d", t.Count)
			w := measure(cfc, label) + 6
			r := image.Rect(subRight-w, subBaseline-12, subRight, subBaseline+3)
			pill(img, r, fg)
			drawString(img, cfc, bg, r.Min.X+3, subBaseline, label)
			subRight = r.Min.X - 4
			textW = subRight - keyInset
		}
		if s := sanitize(t.Subtitle); s != "" {
			sfc := face(regular, subSize)
			s = truncate(sfc, s, textW)
			x := keyInset
			if t.Count <= 1 {
				x = (KeySize - measure(sfc, s)) / 2
			}
			drawString(img, sfc, fg, x, subBaseline, s)
		}
	}

	w := KeySize - 2*keyInset
	h := titleBottom - titleTop
	b := fitBlock(t.Title, bold, titleMinSize, titleMaxSize, titleMaxLines, w, h)
	b.draw(img, image.Rect(keyInset, titleTop, KeySize-keyInset, titleBottom), fg)
	return img
}

// Placeholder renders a 96×96 key with a short transient message.
func Placeholder(text string, th Theme) image.Image {
	th = th.withDefaults()
	img := image.NewRGBA(image.Rect(0, 0, KeySize, KeySize))
	fill(img, img.Bounds(), th.Unknown)
	mu.Lock()
	defer mu.Unlock()
	const inset = 6
	r := img.Bounds().Inset(inset)
	b := fitBlock(text, bold, 11, 18, 4, r.Dx(), r.Dy())
	b.draw(img, r, textColor(th.Unknown))
	return img
}

// Bar layout.
const (
	barPad       = 8
	barLine1Size = 18 // bold
	barLine2Size = 15 // regular
	barLineGap   = 23 // baseline to baseline
)

var barLine2Color = color.RGBA{0xB4, 0xB4, 0xB4, 0xff}

// Bar renders the 248×58 info bar.
func Bar(b InfoBar, th Theme) image.Image {
	th = th.withDefaults()
	img := image.NewRGBA(image.Rect(0, 0, BarW, BarH))
	fill(img, img.Bounds(), black)
	mu.Lock()
	defer mu.Unlock()

	maxW := BarW - 2*barPad
	l1, l2 := sanitize(b.Line1), sanitize(b.Line2)
	f1, f2 := face(bold, barLine1Size), face(regular, barLine2Size)
	c1 := color.Color(white)
	if b.Emphasis {
		c1 = th.Blocked
	}
	cap1, cap2 := capHeight(f1), capHeight(f2)
	switch {
	case l1 != "" && l2 != "":
		top := (BarH - (barLineGap + cap2)) / 2
		y1 := top + cap1
		drawString(img, f1, c1, barPad, y1, truncate(f1, l1, maxW))
		drawString(img, f2, barLine2Color, barPad, y1+barLineGap, truncate(f2, l2, maxW))
	case l1 != "":
		drawString(img, f1, c1, barPad, (BarH+cap1)/2, truncate(f1, l1, maxW))
	case l2 != "":
		drawString(img, f2, barLine2Color, barPad, (BarH+cap2)/2, truncate(f2, l2, maxW))
	}
	return img
}
