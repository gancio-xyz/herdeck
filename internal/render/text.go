package render

import (
	"image"
	"image/color"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

const ellipsis = "…"

type weight int

const (
	regular weight = iota
	bold
)

type faceKey struct {
	w    weight
	size int
}

// mu serialises all rendering: opentype faces carry internal scratch
// buffers and are not safe for concurrent use. It also guards the caches.
var (
	mu       sync.Mutex
	fonts    [2]*sfnt.Font
	faces    = map[faceKey]font.Face{}
	fontOnce sync.Once
)

// face returns the cached face for w at size px. Caller must hold mu.
func face(w weight, size int) font.Face {
	fontOnce.Do(func() {
		for i, ttf := range [][]byte{goregular.TTF, gobold.TTF} {
			f, err := opentype.Parse(ttf)
			if err != nil {
				panic("render: parse embedded font: " + err.Error())
			}
			fonts[i] = f
		}
	})
	k := faceKey{w, size}
	if fc, ok := faces[k]; ok {
		return fc
	}
	fc, err := opentype.NewFace(fonts[w], &opentype.FaceOptions{
		Size: float64(size), DPI: 72, Hinting: font.HintingFull,
	})
	if err != nil {
		panic("render: new face: " + err.Error())
	}
	faces[k] = fc
	return fc
}

func measure(fc font.Face, s string) int {
	return font.MeasureString(fc, s).Ceil()
}

// truncate shortens s so it fits in maxW px, appending "…" when cut.
func truncate(fc font.Face, s string, maxW int) string {
	if measure(fc, s) <= maxW {
		return s
	}
	return forceEllipsis(fc, s, maxW)
}

// forceEllipsis drops trailing runes from s until s+"…" fits in maxW.
func forceEllipsis(fc font.Face, s string, maxW int) string {
	s = strings.TrimRightFunc(s, unicode.IsSpace)
	for s != "" && measure(fc, s+ellipsis) > maxW {
		_, n := utf8.DecodeLastRuneInString(s)
		s = strings.TrimRightFunc(s[:len(s)-n], unicode.IsSpace)
	}
	return s + ellipsis
}

// sanitize collapses whitespace and drops control characters so arbitrary
// strings (e.g. agent output) render as a single clean line.
func sanitize(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			space = true
			continue
		case unicode.IsControl(r) || r == utf8.RuneError:
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}

// drawString draws s with its baseline-left at (x, y).
func drawString(dst *image.RGBA, fc font.Face, c color.Color, x, y int, s string) {
	d := font.Drawer{Dst: dst, Src: image.NewUniform(c), Face: fc, Dot: fixed.P(x, y)}
	d.DrawString(s)
}

// atom is an unbreakable run of text in a title.
type atom struct {
	text  string
	space bool // preceded by a space in the source
}

// Break tiers, from most to least natural.
const (
	breakSpaces = iota // only between words
	breakPunct         // also after - _ / . :
	breakAny           // also inside words too wide for a line on their own
	numTiers
)

// tierPenalty is subtracted from the font size when ranking layouts, so a
// slightly smaller font with natural line breaks beats a bigger one that
// splits words awkwardly. Splitting inside words is a last resort.
var tierPenalty = [numTiers]int{0, 3, 100}

// atomize splits s into unbreakable atoms for tier. For breakAny, atoms
// wider than maxW (in fc) are exploded into single runes.
func atomize(fc font.Face, s string, tier, maxW int) []atom {
	var out []atom
	for _, w := range strings.Fields(s) {
		first := true
		emit := func(t string) {
			if tier == breakAny && measure(fc, t) > maxW {
				for _, r := range t {
					out = append(out, atom{string(r), first})
					first = false
				}
				return
			}
			out = append(out, atom{t, first})
			first = false
		}
		if tier == breakSpaces {
			emit(w)
			continue
		}
		start := 0
		for i, r := range w {
			end := i + utf8.RuneLen(r)
			if strings.ContainsRune("-_/.:", r) && end < len(w) {
				emit(w[start:end])
				start = end
			}
		}
		emit(w[start:])
	}
	return out
}

// wrap greedily packs atoms into lines of at most maxW px. ok is false when
// some atom alone is wider than maxW.
func wrap(fc font.Face, atoms []atom, maxW int) (lines []string, ok bool) {
	ok = true
	cur := ""
	for _, a := range atoms {
		if cur == "" {
			cur = a.text
		} else {
			cand := cur
			if a.space {
				cand += " "
			}
			cand += a.text
			if measure(fc, cand) <= maxW {
				cur = cand
				continue
			}
			lines = append(lines, cur)
			cur = a.text
		}
		if measure(fc, cur) > maxW {
			ok = false
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines, ok
}

// textBlock is a laid-out multi-line label.
type textBlock struct {
	face  font.Face
	size  int
	lh    int // line advance
	lines []string
}

func lineHeight(size int) int { return (size*112 + 50) / 100 }

func (b textBlock) height() int {
	if len(b.lines) == 0 {
		return 0
	}
	m := b.face.Metrics()
	return (len(b.lines)-1)*b.lh + m.Ascent.Ceil() + m.Descent.Ceil()
}

// fitBlock lays s out in a w×h box using at most maxLines lines, choosing
// the largest font size in [minSize, maxSize] (adjusted by break-tier
// penalty). If nothing fits it falls back to minSize, breaking anywhere and
// ending the last line with "…".
func fitBlock(s string, wt weight, minSize, maxSize, maxLines, w, h int) textBlock {
	s = sanitize(s)
	if s == "" {
		return textBlock{}
	}
	best, bestScore := textBlock{}, -1<<31
	for tier := 0; tier < numTiers; tier++ {
		for size := maxSize; size >= minSize; size-- {
			if size-tierPenalty[tier] <= bestScore {
				break
			}
			fc := face(wt, size)
			lines, ok := wrap(fc, atomize(fc, s, tier, w), w)
			if !ok || len(lines) > maxLines {
				continue
			}
			b := textBlock{fc, size, lineHeight(size), lines}
			if b.height() > h {
				continue
			}
			best, bestScore = b, size-tierPenalty[tier]
			break
		}
	}
	if bestScore > -1<<31 {
		return best
	}
	fc := face(wt, minSize)
	b := textBlock{face: fc, size: minSize, lh: lineHeight(minSize)}
	lines, _ := wrap(fc, atomize(fc, s, breakAny, w), w)
	n := maxLines
	for n > 1 && b.withLines(lines[:n]).height() > h {
		n--
	}
	if len(lines) > n {
		lines = append(lines[:n-1:n-1], forceEllipsis(fc, lines[n-1], w))
	}
	b.lines = lines
	return b
}

func (b textBlock) withLines(l []string) textBlock { b.lines = l; return b }

// draw renders b centered horizontally in r and vertically centered on the
// visual (cap-height) extent of the block.
func (b textBlock) draw(dst *image.RGBA, r image.Rectangle, c color.Color) {
	if len(b.lines) == 0 {
		return
	}
	capH := capHeight(b.face)
	visual := (len(b.lines)-1)*b.lh + capH
	y := r.Min.Y + (r.Dy()-visual)/2 + capH
	for _, l := range b.lines {
		x := r.Min.X + (r.Dx()-measure(b.face, l))/2
		drawString(dst, b.face, c, x, y, l)
		y += b.lh
	}
}

// capHeight is the height of capital letters above the baseline.
func capHeight(fc font.Face) int {
	m := fc.Metrics()
	if c := m.CapHeight.Ceil(); c > 0 {
		return c
	}
	return m.Ascent.Ceil() * 7 / 10
}
