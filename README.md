# herdeck

**A physical status board for [herdr](https://herdr.dev) agents, on the Elgato Stream Deck Neo.**

Glance at the deck and know which of your agent workspaces are **blocked**, **working**, **done** or **idle**, without switching windows. Tap a key to jump to that workspace. Long-press to peek at what the agent is doing. Optionally answer a blocked agent's permission prompt from the deck.

herdeck is an independent project for the Elgato Stream Deck Neo. It is not affiliated with Elgato or Corsair.

```
┌────────────┬────────────┬────────────┬────────────┐
│ TICKET-1213│   1.0.6    │  herdeck   │ TICKET-320 │   one key per herdr workspace
│  BLOCKED   │  working   │  working   │    done    │   that has an agent
├────────────┼────────────┼────────────┼────────────┤   color = state
│   myrepo   │   …        │            │            │   blocked first
└────────────┴────────────┴────────────┴────────────┘
 ◀ touch      [ 1 blocked · 2 working · 1 done      ]  touch ▶
              [ myrepo TICKET-1213 — blocked        ]
```

It runs as a **menu bar app** on macOS. The menu bar shows the same counts (🟠1 🔵2 🟢1) and a clickable workspace list, so it's useful even without the deck. On Linux it runs headless (optionally with a tray icon).

## Requirements

- An **Elgato Stream Deck Neo** (other Stream Deck models are not supported yet).
- [herdr](https://herdr.dev) **0.7.x** (socket protocol 14) running.
- macOS 12+ or Linux.
- For accurate agent state, install herdr's integration for your agent, e.g. `herdr integration install claude`.

> **Quit the Elgato Stream Deck app first.** The deck accepts only one client at a time.

## Install

```sh
brew install OWNER/tap/herdeck       # macOS / Linux
# or
go install github.com/OWNER/herdeck/cmd/herdeck@latest   # needs a C toolchain on macOS (menu bar)
```

Or download a release archive from GitHub Releases (`darwin` universal, `linux` amd64/arm64).

## Quick start

```sh
herdeck doctor     # checks the deck, herdr and your config
herdeck            # starts the menu bar app (macOS) / daemon (Linux)
herdeck service install   # start at login (launchd agent / systemd user unit)
```

`herdeck doctor` tells you exactly what's wrong: no deck, deck busy (the Elgato app is running), herdr not running, unsupported herdr version, agents without an integration.

## Using it

| On the deck | Does |
|---|---|
| **Key color** | 🟠 amber = blocked (needs you; pulses) · 🔵 blue = working · 🟢 green = done · gray = idle |
| **Key text** | Workspace label. A repo name shared by several labels moves to the small second line; an agent's custom status replaces it when the agent reports one. `×2` means the workspace has 2 agents and shows the most urgent. White frame = focused in herdr. |
| **Tap a key** | Focuses that workspace's most urgent agent in herdr and brings your terminal to the front. |
| **Long-press a key** (≥ 600 ms) | Peek: the info bar shows the agent's latest output for about 8 s, preferring Claude Code's `※ recap:` line. Long lines scroll. |
| **Touch points** | Previous / next page when you have more than 8 agent workspaces. A dim light means there's a page that way; amber means a blocked workspace is on it. |
| **Info bar** | Counts on the first line; the selected or blocked workspace on the second. |

The deck dims after 10 minutes without changes and wakes on any change or touch. It recovers by itself when herdr restarts or the deck is unplugged and plugged back in.

**Menu bar:** counts in the title, every agent workspace in the menu (click to focus), herdr and deck connection status, *Pause deck*, *Launch at login*, *Open config…*.

### Quick reply (optional, off by default)

Answer a blocked agent's prompt from the deck:

1. **Long-press a blocked key.** The key shows **ARMED**, the touch points light up, and the info bar shows the pending question. This lasts about 8 s.
2. **Tap the same key** to send your configured *approve* keys, or **touch a touch point** to send the *deny* keys. Any other key, or waiting, disarms.

herdeck refuses to send to an agent that is no longer blocked. After sending, it checks that the agent actually left the blocked state within 3 s. If it didn't, the key flashes red and the info bar says *"reply may not have landed"*.

Keys are configured per agent type and **there are no built-in defaults**. Enable it in the config:

```toml
[reply]
enabled = true

[reply.agents.claude]
approve_keys = ["Enter"]    # VERIFY against your Claude Code version
deny_keys    = ["Escape"]
```

Check what your agent's prompt expects before enabling this. Prompts change between agent versions.

## Configuration

`~/.config/herdeck/config.toml` (or `$XDG_CONFIG_HOME/herdeck/config.toml`). Every key is optional. `herdeck config print-default` prints the full commented default:

```toml
[herdr]
session = ""             # herdr session name; empty = default

[deck]
serial = ""              # pick a deck by serial (`herdeck devices`); empty = first Neo
brightness = 70
dim_after_minutes = 10   # 0 disables dimming
dim_brightness = 20

[layout]
sort = "state"           # state (blocked first) | workspace (herdr order) | stable (first seen)
long_press_ms = 600
show_idle = true
animate_blocked = true

[colors]
working = "#2F6FEB"
blocked = "#F5A524"
done    = "#2DA44E"
idle    = "#3A3A3A"

[focus]
activate_app = "auto"    # terminal to bring forward on macOS: "auto" detects the one running herdr,
                         # or name it ("Terminal", "Ghostty", "iTerm"); "" disables

[reply]
enabled = false
```

Flags for `herdeck run`: `--config`, `--session`, `--serial`, `--headless`, `--tray`, `--dry-run` (no deck: logs what the deck would show).

## Troubleshooting

| Symptom | Fix |
|---|---|
| `deck … is busy` | Quit the Elgato Stream Deck app (menu bar → Quit), or another herdeck: `herdeck doctor` shows its pid. |
| `no Stream Deck Neo found` | Use a data-capable USB-C cable. On Linux, install the udev rule (below). |
| `herdr: no socket` | Start herdr (`herdr`). For a named session use `--session name` or `[herdr] session`. |
| Keys stay gray / unknown | Install the herdr integration for your agent: `herdr integration install claude`. |
| Tap doesn't bring the terminal forward | Set `[focus] activate_app = "Ghostty"` (or your terminal). |
| herdr upgraded and things look off | `herdeck doctor` warns about untested protocol versions. Please open a compatibility issue. |

Logs: `herdeck run` writes to stderr. As a macOS service they go to `~/Library/Logs/herdeck.log`.

### Linux

Allow your user to access the deck:

```sh
sudo cp contrib/99-herdeck.rules /etc/udev/rules.d/
sudo udevadm control --reload-rules && sudo udevadm trigger
```

Then `herdeck service install` (systemd user unit), or see `contrib/herdeck.service`.

## How it works

herdeck talks to herdr over its local socket API (NDJSON over a Unix socket), the same API the `herdr` CLI uses. It subscribes to per-agent status events for instant updates and re-reads the full workspace list on lifecycle events and every 2 s as a safety net. It drives the deck over USB HID with the pure-Go [`rafaelmartins.com/p/streamdeck`](https://rafaelmartins.com/p/streamdeck/) library, with no libusb or hidapi needed. See [`docs/herdr-protocol.md`](docs/herdr-protocol.md) and [`docs/compat.md`](docs/compat.md).

## Development

```sh
make test        # go test -race ./...
make build       # bin/herdeck
make app         # dist/herdeck.app (macOS menu bar bundle)
make golden      # regenerate render golden images after a visual change
```

Everything except the two drivers is tested without hardware or a running herdr: `internal/core` is a pure state machine, `internal/herdr/herdrtest` is a fake herdr server, and `deck.FakeDeck` stands in for the hardware. See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

Apache-2.0. herdr itself is a separate project with its own license. herdeck only talks to it over its public socket API.
