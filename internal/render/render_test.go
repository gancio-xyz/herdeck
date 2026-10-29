package render

import (
	"bytes"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"herdeck/internal/model"
)

var update = flag.Bool("update", false, "regenerate golden images and the contact sheet")

type goldenKey struct {
	name string
	tile Tile
}

var goldenKeys = []goldenKey{
	{"working", Tile{Title: "TICKET-1213", Subtitle: "claude", Status: model.StatusWorking}},
	{"blocked", Tile{Title: "1.0.6", Subtitle: "needs input", Status: model.StatusBlocked}},
	{"done", Tile{Title: "herdeck", Subtitle: "codex", Status: model.StatusDone}},
	{"idle", Tile{Title: "dotfiles", Status: model.StatusIdle}},
	{"unknown", Tile{Title: "scratch", Subtitle: "?", Status: model.StatusUnknown}},
	{"two_words", Tile{Title: "myrepo feature-login-flow", Subtitle: "claude", Status: model.StatusWorking}},
	{"long_title", Tile{Title: "the quick brown fox jumps over the lazy dog again and again", Subtitle: "a very long subtitle that must be truncated", Status: model.StatusDone}},
	{"long_word", Tile{Title: "supercalifragilisticexpialidocious", Status: model.StatusIdle}},
	{"unicode", Tile{Title: "café ñandú Ωμέγα", Subtitle: "naïve — ok", Status: model.StatusWorking}},
	{"focused", Tile{Title: "api", Subtitle: "claude", Status: model.StatusWorking, Focused: true}},
	{"pulse1", Tile{Title: "infra", Subtitle: "claude", Status: model.StatusBlocked, Pulse: 1}},
	{"pulse2", Tile{Title: "infra", Subtitle: "claude", Status: model.StatusBlocked, Pulse: 2}},
	{"pulse3", Tile{Title: "infra", Subtitle: "claude", Status: model.StatusBlocked, Pulse: 3}},
	{"armed", Tile{Title: "TICKET-1213", Subtitle: "claude", Status: model.StatusBlocked, Armed: true}},
	{"armed_focused", Tile{Title: "web", Subtitle: "codex", Status: model.StatusWorking, Armed: true, Focused: true}},
	{"alert", Tile{Title: "TICKET-1213", Subtitle: "reply failed", Status: model.StatusBlocked, Alert: true}},
	{"count", Tile{Title: "monorepo", Subtitle: "claude", Status: model.StatusWorking, Count: 3}},
	{"count_only", Tile{Title: "x", Status: model.StatusDone, Count: 12}},
	{"focused_pulse", Tile{Title: "1.0.6", Subtitle: "claude", Status: model.StatusBlocked, Focused: true, Pulse: 3, Count: 2}},
	{"empty", Tile{Empty: true, Title: "ignored"}},
}

type goldenBar struct {
	name string
	bar  InfoBar
}

var goldenBars = []goldenBar{
	{"bar_two_lines", InfoBar{Line1: "3 working · 1 blocked", Line2: "TICKET-1213: claude needs input"}},
	{"bar_emphasis", InfoBar{Line1: "TICKET-1213 needs you", Line2: "Do you want to proceed? (y/n)", Emphasis: true}},
	{"bar_long", InfoBar{Line1: "a very long first line that will not fit the bar", Line2: "agent output\nwith newlines\tand tabs and a lot more text after that"}},
	{"bar_line1_only", InfoBar{Line1: "herdr offline"}},
	{"bar_empty", InfoBar{}},
}

var goldenPlaceholders = []struct{ name, text string }{
	{"ph_offline", "herdr offline"},
	{"ph_long", "protocol mismatch: herdr 0.9 unsupported"},
}

func encode(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func checkGolden(t *testing.T, name string, img image.Image) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name+".png")
	if *update {
		if err := os.WriteFile(path, encode(t, img), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/render -update)", err)
	}
	defer f.Close()
	want, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if !want.Bounds().Eq(img.Bounds()) {
		t.Fatalf("%s: bounds %v, want %v", name, img.Bounds(), want.Bounds())
	}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if g, w := color.RGBAModel.Convert(img.At(x, y)), color.RGBAModel.Convert(want.At(x, y)); g != w {
				t.Fatalf("%s: pixel (%d,%d) = %v, want %v (run -update if intended)", name, x, y, g, w)
			}
		}
	}
}

func TestGolden(t *testing.T) {
	th := DefaultTheme()
	var keys, bars []image.Image
	for _, g := range goldenKeys {
		img := Key(g.tile, th)
		if img.Bounds() != image.Rect(0, 0, KeySize, KeySize) {
			t.Errorf("%s: bounds %v", g.name, img.Bounds())
		}
		checkGolden(t, g.name, img)
		keys = append(keys, img)
	}
	for _, g := range goldenPlaceholders {
		img := Placeholder(g.text, th)
		checkGolden(t, g.name, img)
		keys = append(keys, img)
	}
	for _, g := range goldenBars {
		img := Bar(g.bar, th)
		if img.Bounds() != image.Rect(0, 0, BarW, BarH) {
			t.Errorf("%s: bounds %v", g.name, img.Bounds())
		}
		checkGolden(t, g.name, img)
		bars = append(bars, img)
	}
	if *update {
		writeContactSheet(t, keys, bars)
	}
}

// writeContactSheet lays keys out 4×2 per Neo "device" with a bar below,
// so the whole set can be eyeballed at once.
func writeContactSheet(t *testing.T, keys, bars []image.Image) {
	const gap, devPad = 8, 12
	devW := 4*KeySize + 3*gap + 2*devPad
	devH := 2*KeySize + gap + gap + BarH + 2*devPad
	nDev := (len(keys) + 7) / 8
	nDev = max(nDev, len(bars))
	sheet := image.NewRGBA(image.Rect(0, 0, 2*devW+gap*3, ((nDev+1)/2)*(devH+gap)+gap))
	draw.Draw(sheet, sheet.Bounds(), image.NewUniform(color.RGBA{0x60, 0x60, 0x60, 0xff}), image.Point{}, draw.Src)
	for d := 0; d < nDev; d++ {
		ox := gap + (d%2)*(devW+gap)
		oy := gap + (d/2)*(devH+gap)
		draw.Draw(sheet, image.Rect(ox, oy, ox+devW, oy+devH), image.NewUniform(color.RGBA{0x10, 0x10, 0x10, 0xff}), image.Point{}, draw.Src)
		for i := 0; i < 8; i++ {
			k := d*8 + i
			if k >= len(keys) {
				break
			}
			x := ox + devPad + (i%4)*(KeySize+gap)
			y := oy + devPad + (i/4)*(KeySize+gap)
			draw.Draw(sheet, image.Rect(x, y, x+KeySize, y+KeySize), keys[k], image.Point{}, draw.Src)
		}
		if d < len(bars) {
			x := ox + (devW-BarW)/2
			y := oy + devPad + 2*KeySize + 2*gap
			draw.Draw(sheet, image.Rect(x, y, x+BarW, y+BarH), bars[d], image.Point{}, draw.Src)
		}
	}
	if err := os.WriteFile(filepath.Join("testdata", "contact.png"), encode(t, sheet), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDeterministic(t *testing.T) {
	tile := goldenKeys[5].tile
	a, b := encode(t, Key(tile, Theme{})), encode(t, Key(tile, DefaultTheme()))
	if !bytes.Equal(a, b) {
		t.Fatal("same tile rendered differently (zero Theme should equal DefaultTheme)")
	}
}

func TestParseHex(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want color.RGBA
		err  bool
	}{
		{"#2F6FEB", color.RGBA{0x2F, 0x6F, 0xEB, 0xff}, false},
		{"f5a524", color.RGBA{0xF5, 0xA5, 0x24, 0xff}, false},
		{" #000000 ", color.RGBA{0, 0, 0, 0xff}, false},
		{"#FFF", color.RGBA{}, true},
		{"#GGGGGG", color.RGBA{}, true},
		{"", color.RGBA{}, true},
		{"#2F6FEB00", color.RGBA{}, true},
	} {
		got, err := ParseHex(tc.in)
		if (err != nil) != tc.err || got != tc.want {
			t.Errorf("ParseHex(%q) = %v, %v; want %v, err=%v", tc.in, got, err, tc.want, tc.err)
		}
	}
}

func TestWithDefaults(t *testing.T) {
	custom := color.RGBA{1, 2, 3, 0xff}
	got := Theme{Working: custom}.withDefaults()
	want := DefaultTheme()
	want.Working = custom
	if got != want {
		t.Fatalf("withDefaults = %+v, want %+v", got, want)
	}
	if (Theme{}).withDefaults() != DefaultTheme() {
		t.Fatal("zero theme should equal DefaultTheme")
	}
}

func TestTextColor(t *testing.T) {
	th := DefaultTheme()
	for _, tc := range []struct {
		name string
		bg   color.RGBA
		want color.RGBA
	}{
		{"working", th.Working, white},
		{"blocked", th.Blocked, black},
		{"done", th.Done, black},
		{"idle", th.Idle, white},
		{"unknown", th.Unknown, white},
		{"alert", th.Alert, white},
		{"white", white, black},
		{"black", black, white},
	} {
		if got := textColor(tc.bg); got != tc.want {
			t.Errorf("%s: textColor = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestArmedDistinct(t *testing.T) {
	th := DefaultTheme()
	dist := func(a, b color.RGBA) int {
		d := func(x, y uint8) int { v := int(x) - int(y); return v * v }
		return d(a.R, b.R) + d(a.G, b.G) + d(a.B, b.B)
	}
	for _, c := range []color.RGBA{th.Blocked, th.Alert, th.Working, th.Done} {
		if dist(th.Armed, c) < 100*100 {
			t.Errorf("armed %v too close to %v", th.Armed, c)
		}
	}
}

func TestTruncateAndSanitize(t *testing.T) {
	mu.Lock()
	defer mu.Unlock()
	fc := face(regular, 15)
	if got := truncate(fc, "short", 200); got != "short" {
		t.Errorf("truncate short = %q", got)
	}
	got := truncate(fc, "a very long line of agent output that overflows", 100)
	if measure(fc, got) > 100 || got[len(got)-len(ellipsis):] != ellipsis {
		t.Errorf("truncate long = %q (%dpx)", got, measure(fc, got))
	}
	if got := sanitize("  a\n\tb \x1b[0m c  "); got != "a b [0m c" {
		t.Errorf("sanitize = %q", got)
	}
}

func TestTitleFitsInsideKey(t *testing.T) {
	// Every laid-out title line must fit the text width at a size in range.
	mu.Lock()
	defer mu.Unlock()
	for _, g := range goldenKeys {
		b := fitBlock(g.tile.Title, bold, titleMinSize, titleMaxSize, titleMaxLines, KeySize-2*keyInset, subTop-keyInset)
		if len(b.lines) > titleMaxLines {
			t.Errorf("%s: %d lines", g.name, len(b.lines))
		}
		for _, l := range b.lines {
			if w := measure(b.face, l); w > KeySize-2*keyInset {
				t.Errorf("%s: line %q is %dpx", g.name, l, w)
			}
		}
		if testing.Verbose() {
			fmt.Printf("%-14s %2dpx %q\n", g.name, b.size, b.lines)
		}
	}
}

func TestTrayIcon(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(TrayIcon()))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != TrayIconSize || img.Bounds().Dy() != TrayIconSize {
		t.Fatalf("bounds %v", img.Bounds())
	}
	_, _, _, a := img.At(0, 0).RGBA()
	if a != 0 {
		t.Error("corner should be transparent")
	}
	checkGolden(t, "tray", img)
}
