// Package config loads ~/.config/herdeck/config.toml.
package config

import (
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

//go:embed default.toml
var defaultTOML string

// DefaultTOML is the commented default config printed by `herdeck config print-default`.
func DefaultTOML() string { return defaultTOML }

type Config struct {
	Herdr  Herdr  `toml:"herdr"`
	Deck   Deck   `toml:"deck"`
	Layout Layout `toml:"layout"`
	Colors Colors `toml:"colors"`
	Focus  Focus  `toml:"focus"`
	Reply  Reply  `toml:"reply"`
}

type Herdr struct {
	Session string `toml:"session"`
}

type Deck struct {
	Serial          string `toml:"serial"`
	Brightness      int    `toml:"brightness"`
	DimAfterMinutes int    `toml:"dim_after_minutes"` // 0 disables dimming
	DimBrightness   int    `toml:"dim_brightness"`
}

type Layout struct {
	Sort           string `toml:"sort"` // state | workspace | stable
	LongPressMS    int    `toml:"long_press_ms"`
	ShowIdle       bool   `toml:"show_idle"`
	ShowTerminals  bool   `toml:"show_terminals"` // workspaces without an agent
	AnimateBlocked bool   `toml:"animate_blocked"`
}

type Colors struct {
	Working string `toml:"working"`
	Blocked string `toml:"blocked"`
	Done    string `toml:"done"`
	Idle    string `toml:"idle"`
}

type Focus struct {
	// ActivateApp is the macOS app brought to the front after focusing:
	// "" disables, "auto" picks the first running known terminal.
	ActivateApp string `toml:"activate_app"`
}

type Reply struct {
	Enabled bool                  `toml:"enabled"`
	Agents  map[string]ReplyAgent `toml:"agents"`
}

type ReplyAgent struct {
	ApproveKeys []string `toml:"approve_keys"`
	DenyKeys    []string `toml:"deny_keys"`
}

func (l Layout) LongPress() time.Duration { return time.Duration(l.LongPressMS) * time.Millisecond }

func (d Deck) DimAfter() time.Duration { return time.Duration(d.DimAfterMinutes) * time.Minute }

// Default returns the built-in defaults (identical to default.toml).
func Default() Config {
	var c Config
	if _, err := toml.Decode(defaultTOML, &c); err != nil {
		panic("config: bad embedded default.toml: " + err.Error())
	}
	return c
}

// Path returns the config file location, honouring XDG_CONFIG_HOME.
func Path() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "herdeck", "config.toml")
}

// Load reads path on top of the defaults. A missing file is not an error.
func Load(path string) (Config, error) {
	c := Default()
	md, err := toml.DecodeFile(path, &c)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, fmt.Errorf("config %s: %w", path, err)
	}
	if keys := md.Undecoded(); len(keys) > 0 {
		return c, fmt.Errorf("config %s: unknown keys %v", path, keys)
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	switch c.Layout.Sort {
	case "state", "workspace", "stable":
	default:
		return fmt.Errorf("config: layout.sort must be state, workspace or stable (got %q)", c.Layout.Sort)
	}
	if c.Layout.LongPressMS < 100 {
		return fmt.Errorf("config: layout.long_press_ms must be >= 100")
	}
	for _, v := range []int{c.Deck.Brightness, c.Deck.DimBrightness} {
		if v < 0 || v > 100 {
			return fmt.Errorf("config: brightness values must be 0..100")
		}
	}
	return nil
}

// WriteDefault creates the config file with the default contents if it does
// not exist yet and returns its path.
func WriteDefault(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(defaultTOML), 0o644)
}
