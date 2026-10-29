package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"herdeck/internal/app"
	"herdeck/internal/config"
	"herdeck/internal/deck"
	"herdeck/internal/herdr"
	"herdeck/internal/model"
)

// doctor prints one line per check and returns whether everything is usable.
func doctor(w io.Writer, cfg config.Config, cfgErr error) bool {
	ok := true
	pass := func(format string, a ...any) { fmt.Fprintf(w, "✓ "+format+"\n", a...) }
	warn := func(format string, a ...any) { fmt.Fprintf(w, "! "+format+"\n", a...) }
	fail := func(format string, a ...any) {
		ok = false
		fmt.Fprintf(w, "✗ "+format+"\n", a...)
	}

	// Config.
	switch _, statErr := os.Stat(config.Path()); {
	case cfgErr != nil:
		fail("config: %v", cfgErr)
	case statErr != nil:
		pass("config: using defaults (no %s; create it with `herdeck config print-default > %s`)", config.Path(), config.Path())
	default:
		pass("config: %s", config.Path())
	}

	// Another instance holds the deck?
	running := app.LockHolder()
	if running > 0 {
		pass("herdeck is running (pid %d)", running)
	}

	// Deck.
	infos, err := deck.List()
	switch {
	case err != nil:
		fail("deck: %v", err)
	case len(infos) == 0:
		fail("deck: no Stream Deck Neo found. Plug it in with a data-capable USB cable")
	default:
		for _, d := range infos {
			if cfg.Deck.Serial != "" && d.Serial != cfg.Deck.Serial {
				warn("deck: %s %s ignored (config deck.serial = %s)", d.Model, d.Serial, cfg.Deck.Serial)
				continue
			}
			switch {
			case d.Busy && running > 0:
				pass("deck: %s %s (in use by herdeck pid %d)", d.Model, d.Serial, running)
			case d.Busy:
				fail("deck: %s %s is busy. Quit the Elgato Stream Deck app and try again", d.Model, d.Serial)
			default:
				pass("deck: %s %s, firmware %s", d.Model, d.Serial, d.Firmware)
			}
		}
	}

	// herdr.
	sock, err := herdr.ResolveSocket(cfg.Herdr.Session)
	if err != nil {
		fail("herdr: %v", err)
		return ok
	}
	if _, err := os.Stat(sock); err != nil {
		fail("herdr: no socket at %s. Is herdr running? Start it with `herdr`", sock)
		return ok
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c := herdr.New(sock)
	info, err := c.Ping(ctx)
	if err != nil {
		fail("herdr: socket %s not answering: %v", sock, err)
		return ok
	}
	if info.Protocol < model.MinProtocol || info.Protocol > model.MaxProtocol {
		warn("herdr: version %s uses protocol %d; herdeck is tested with %d–%d. It may still work; please report issues",
			info.Version, info.Protocol, model.MinProtocol, model.MaxProtocol)
	} else {
		pass("herdr: version %s (protocol %d) at %s", info.Version, info.Protocol, sock)
	}

	snap, err := c.Snapshot(ctx)
	if err != nil {
		fail("herdr: reading state: %v", err)
		return ok
	}
	spaces, agents, unknown := 0, 0, 0
	for _, ws := range snap.Workspaces {
		if len(ws.Agents) > 0 {
			spaces++
		}
		for _, a := range ws.Agents {
			agents++
			if a.Status == model.StatusUnknown {
				unknown++
			}
		}
	}
	pass("herdr: %d agents in %d of %d workspaces", agents, spaces, len(snap.Workspaces))
	if unknown > 0 {
		warn("herdr: %d agents have unknown state. Install herdr's integration for accurate state, e.g. `herdr integration install claude`", unknown)
	}
	if cfg.Reply.Enabled {
		if len(cfg.Reply.Agents) == 0 {
			warn("reply: enabled but no [reply.agents.*] keys configured, so quick reply will never arm")
		} else {
			pass("reply: enabled for %d agent type(s)", len(cfg.Reply.Agents))
		}
	}
	return ok
}
