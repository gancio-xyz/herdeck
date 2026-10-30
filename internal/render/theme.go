package render

import (
	"fmt"
	"image/color"
	"math"
	"strconv"
	"strings"

	"github.com/gancio-xyz/herdeck/internal/model"
)

// DefaultTheme is the built-in palette.
func DefaultTheme() Theme {
	return Theme{
		Working: color.RGBA{0x2F, 0x6F, 0xEB, 0xff},
		Blocked: color.RGBA{0xF5, 0xA5, 0x24, 0xff},
		Done:    color.RGBA{0x2D, 0xA4, 0x4E, 0xff},
		Idle:    color.RGBA{0x3A, 0x3A, 0x3A, 0xff},
		Unknown: color.RGBA{0x26, 0x26, 0x26, 0xff},
		Empty:   color.RGBA{0x00, 0x00, 0x00, 0xff},
		Armed:   color.RGBA{0xE0, 0x40, 0xFB, 0xff}, // magenta: distinct from blocked amber and alert red
		Alert:   color.RGBA{0xD0, 0x30, 0x2F, 0xff},
	}
}

// withDefaults returns t with every zero-valued field replaced by the
// corresponding DefaultTheme color. Empty is allowed to be black, so only a
// fully zero (transparent) value counts as unset.
func (t Theme) withDefaults() Theme {
	d := DefaultTheme()
	fill := func(c *color.RGBA, def color.RGBA) {
		if *c == (color.RGBA{}) {
			*c = def
		}
	}
	fill(&t.Working, d.Working)
	fill(&t.Blocked, d.Blocked)
	fill(&t.Done, d.Done)
	fill(&t.Idle, d.Idle)
	fill(&t.Unknown, d.Unknown)
	fill(&t.Empty, d.Empty)
	fill(&t.Armed, d.Armed)
	fill(&t.Alert, d.Alert)
	return t
}

// statusColor maps a status to its background color.
func (t Theme) statusColor(s model.Status) color.RGBA {
	switch s {
	case model.StatusWorking:
		return t.Working
	case model.StatusBlocked:
		return t.Blocked
	case model.StatusDone:
		return t.Done
	case model.StatusIdle:
		return t.Idle
	default:
		return t.Unknown
	}
}

// ParseHex parses "#RRGGBB" or "RRGGBB" into an opaque color.
func ParseHex(s string) (color.RGBA, error) {
	h := strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(h) != 6 {
		return color.RGBA{}, fmt.Errorf("render: invalid hex color %q: want #RRGGBB", s)
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return color.RGBA{}, fmt.Errorf("render: invalid hex color %q: %w", s, err)
	}
	return color.RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 0xff}, nil
}

// luminance is the WCAG relative luminance of c, in [0,1].
func luminance(c color.RGBA) float64 {
	lin := func(v uint8) float64 {
		f := float64(v) / 255
		if f <= 0.04045 {
			return f / 12.92
		}
		return math.Pow((f+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

// lumThreshold separates "light" backgrounds (black text) from dark ones
// (white text). Pure WCAG contrast ratio would put the crossover at ~0.18,
// which flips our working blue to black text; 0.25 keeps white on blue and
// gray while choosing black on amber and green.
const lumThreshold = 0.25

// textColor picks black or white text for legibility on bg.
func textColor(bg color.RGBA) color.RGBA {
	if luminance(bg) > lumThreshold {
		return color.RGBA{0, 0, 0, 0xff}
	}
	return color.RGBA{0xff, 0xff, 0xff, 0xff}
}

// mix linearly interpolates a toward b by f in [0,1] (sRGB space; good
// enough for borders).
func mix(a, b color.RGBA, f float64) color.RGBA {
	l := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*f + 0.5) }
	return color.RGBA{l(a.R, b.R), l(a.G, b.G), l(a.B, b.B), 0xff}
}
