// Package render turns view state into deck images. Pure functions, no I/O.
package render

// CONTRACT FILE — written by the integrator. Implementations must keep these
// exported signatures; add unexported helpers in other files.

import (
	"image/color"

	"herdeck/internal/model"
)

// Theme holds the palette. Zero-valued fields fall back to DefaultTheme.
type Theme struct {
	Working color.RGBA
	Blocked color.RGBA
	Done    color.RGBA
	Idle    color.RGBA
	Unknown color.RGBA
	Empty   color.RGBA // unused slot background
	Armed   color.RGBA // border/accent for quick-reply armed state
	Alert   color.RGBA // "reply may not have landed" flash
}

// Tile is everything a key image depends on. Equal tiles must render to
// identical images, so the app can skip redraws by comparing Tiles.
type Tile struct {
	Empty    bool         // render a blank slot; other fields ignored
	Title    string       // main label (workspace label, prefix-stripped)
	Subtitle string       // small second line, e.g. agent name or custom status
	Status   model.Status // drives background color
	Focused  bool         // the workspace currently focused in herdr: thin white frame
	Armed    bool         // quick-reply armed: thick Armed-colored border + "ARMED" badge
	Alert    bool         // reply failed: Alert-colored background
	Pulse    int          // 0..3 animation phase for blocked tiles' border; 0 = no border highlight
	Count    int          // number of agents in the workspace; badge shown when > 1
}

// InfoBar is the content of the 248x58 info bar: up to two lines.
type InfoBar struct {
	Line1 string
	Line2 string
	// Emphasis colors Line1 with the Blocked color (e.g. something needs you).
	Emphasis bool
}
