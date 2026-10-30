# herdeck

**A physical status board for [herdr](https://herdr.dev) agents, on the Elgato Stream Deck Neo.**

Glance at the deck and see which of your agent workspaces are **blocked**, **working**, **done** or **idle**, without switching windows. Tap a key to jump to that workspace. Long-press to peek at what the agent is doing. Optionally, answer a blocked agent's permission prompt from the deck.

```
┌────────────┬────────────┬────────────┬────────────┐
│ TICKET-1213│   1.0.6    │  herdeck   │ TICKET-320 │   one key per herdr workspace
│  BLOCKED   │  working   │  working   │    done    │   color = state
├────────────┼────────────┼────────────┼────────────┤   blocked first
│   myrepo   │  terminal  │            │            │
└────────────┴────────────┴────────────┴────────────┘
 ◀ touch      [ 1 blocked · 2 working · 1 done      ]  touch ▶
              [ myrepo TICKET-1213 — blocked        ]
```

On macOS it runs as a **menu bar app** that shows the same counts (🟠1 🔵2 🟢1) and a clickable workspace list, so it's useful even without the deck plugged in. On Linux it runs as a background daemon.

> herdeck is an independent project for the Elgato Stream Deck Neo. It is not affiliated with Elgato or Corsair.

## What you need

- An **Elgato Stream Deck Neo**. Other Stream Deck models are not supported yet.
- [herdr](https://herdr.dev) **0.7.x**, running.
- macOS 12+ or Linux (amd64 or arm64).
- Recommended: herdr's integration for your agent, so herdr reports accurate state. For Claude Code, run `herdr integration install claude`.

> [!IMPORTANT]
> **Quit the Elgato Stream Deck app first.** The deck accepts only one program at a time. Quit it from its menu bar icon, and turn off its "launch at login" setting if you want herdeck to own the deck permanently.

## Install

### Homebrew (macOS and Linux)

```sh
brew install gancio-xyz/tap/herdeck
```

### Download a release

Grab the archive for your system from the [latest release](https://github.com/gancio-xyz/herdeck/releases/latest):

| System | File |
|---|---|
| macOS, any Mac | `herdeck_<version>_darwin_universal.tar.gz` |
| macOS, Apple silicon only | `herdeck_<version>_darwin_arm64.tar.gz` |
| macOS, Intel only | `herdeck_<version>_darwin_amd64.tar.gz` |
| Linux x86-64 | `herdeck_<version>_linux_amd64.tar.gz` |
| Linux ARM64 | `herdeck_<version>_linux_arm64.tar.gz` |

Then unpack it and put `herdeck` on your `PATH`:

```sh
tar -xzf herdeck_*.tar.gz
mkdir -p ~/.local/bin && mv herdeck ~/.local/bin/
herdeck version
```

If `~/.local/bin` isn't on your `PATH`, add `export PATH="$HOME/.local/bin:$PATH"` to your shell profile, or move the binary to `/usr/local/bin` instead.

**macOS:** the binary isn't notarized yet. If you downloaded it with a browser, macOS will refuse to open it ("cannot be opened because the developer cannot be verified"). Remove the quarantine flag once:

```sh
xattr -d com.apple.quarantine ~/.local/bin/herdeck
```

**Linux:** let your user access the deck without root (the rule file is in the archive):

```sh
sudo cp contrib/99-herdeck.rules /etc/udev/rules.d/
sudo udevadm control --reload-rules && sudo udevadm trigger
```

Unplug the deck and plug it back in afterwards.

### From source

```sh
go install github.com/gancio-xyz/herdeck/cmd/herdeck@latest
```

You need Go 1.26+. On macOS you also need a C toolchain (`xcode-select --install`) for the menu bar.

## First run

```sh
herdeck doctor
```

`doctor` checks everything herdeck depends on and tells you how to fix what's wrong:

- the deck isn't found
- the deck is busy because the Elgato app or another herdeck is running
- herdr isn't running
- your herdr version is untested
- your agents have no integration installed
- your config file has errors

When everything shows ✓, start it:

```sh
herdeck                    # macOS: menu bar app · Linux: daemon in the foreground
```

To have it start automatically when you log in:

```sh
herdeck service install    # macOS launchd agent / Linux systemd user unit
herdeck service uninstall  # undo
```

On macOS you can also toggle **Launch at login** from the menu bar icon.

## Using it

### On the deck

| You see / do | What it means |
|---|---|
| 🟠 **Amber key, pulsing** | The workspace is **blocked**: an agent is waiting for you. |
| 🔵 **Blue key** | An agent is working. |
| 🟢 **Green key** | An agent finished. |
| **Gray key** | Idle. Dim keys labeled `terminal` are workspaces with no agent (plain shells). |
| **White frame** | The workspace currently focused in herdr. |
| `×2` on a key | The workspace has 2 agents. The key shows the most urgent one. |
| **Tap a key** | Focuses that workspace in herdr (its most urgent agent) and brings your terminal to the front. |
| **Long-press a key** (≥ 0.6 s) | Peek: the info bar shows the agent's latest output for about 8 seconds. Long lines scroll. |
| **Touch points** (left/right) | Previous/next page when you have more than 8 workspaces. A dim light means there's a page that way. Amber means a blocked workspace is on it. |
| **Info bar** | Counts on the first line, and the selected or blocked workspace on the second. |

Key labels are herdr's workspace labels, with any prefix shared by all of them removed. For example, `myrepo TICKET-320` shows as `TICKET-320`, with `myrepo` on the small second line.

The deck dims after 10 minutes without changes and wakes on any change or touch. It recovers by itself when herdr restarts or the deck is unplugged and plugged back in.

### Menu bar (macOS)

The title shows the counts. The menu lists every workspace (click one to focus it), shows herdr and deck connection status, and has **Pause deck**, **Launch at login**, **Open config…** and **Quit herdeck**.

### Quick reply (optional, off by default)

Answer a blocked agent's prompt without touching the keyboard:

1. **Long-press a blocked (amber) key.** It shows **ARMED**, the touch points light up, and the info bar shows the pending question. This lasts about 8 seconds.
2. **Tap the same key** to send your *approve* keys, or **tap a touch point** to send your *deny* keys. Pressing any other key, or waiting, cancels.

Safety rules:

- herdeck refuses to send anything to an agent that is no longer blocked.
- herdeck ignores all input while it's disconnected from herdr.
- After sending, herdeck checks that the agent actually left the blocked state within 3 seconds. If it didn't, the key flashes red and the info bar says *"reply may not have landed"*.

There are **no built-in keys**. You configure them per agent type:

```toml
[reply]
enabled = true

[reply.agents.claude]
approve_keys = ["Enter"]    # VERIFY against your Claude Code version
deny_keys    = ["Escape"]
```

Check what your agent's prompt expects before turning this on. Prompts change between agent versions.

## Configuration

The config file is `~/.config/herdeck/config.toml` (or `$XDG_CONFIG_HOME/herdeck/config.toml`). It's optional, and every key in it is optional.

```sh
herdeck config path            # where herdeck looks for it
herdeck config print-default   # the full default, with comments
herdeck config print-default > ~/.config/herdeck/config.toml   # start from the default
```

The most useful settings:

```toml
[herdr]
session = ""             # herdr session name; empty = default session

[deck]
serial = ""              # pick a deck by serial (see `herdeck devices`); empty = first Neo
brightness = 70
dim_after_minutes = 10   # 0 = never dim
dim_brightness = 20

[layout]
sort = "state"           # state (blocked first) | workspace (herdr order) | stable (first seen)
long_press_ms = 600
show_idle = true         # show workspaces whose agents are all idle
show_terminals = true    # show workspaces with no agent, dimmed and last
animate_blocked = true   # pulse blocked keys

[colors]
working = "#2F6FEB"
blocked = "#F5A524"
done    = "#2DA44E"
idle    = "#3A3A3A"

[focus]
activate_app = "auto"    # macOS: terminal to bring forward on tap. "auto" detects it;
                         # or name it: "Terminal", "Ghostty", "iTerm". "" disables.
```

Restart herdeck after you edit the file.

## Commands

| Command | What it does |
|---|---|
| `herdeck` / `herdeck run` | Start (menu bar app on macOS, daemon on Linux). |
| `herdeck doctor` | Check the deck, herdr and the config. |
| `herdeck devices` | List connected Stream Deck Neo decks and their serial numbers. |
| `herdeck config print-default` | Print the default config. |
| `herdeck config path` | Print where the config file lives. |
| `herdeck service install` / `uninstall` | Start/stop starting at login. |
| `herdeck version` | Print the version. |

Flags for `run`:

| Flag | What it does |
|---|---|
| `--session NAME` | Use a named herdr session. |
| `--serial SERIAL` | Use a specific deck. |
| `--config PATH` | Use a different config file. |
| `--headless` | No menu bar icon (the default on Linux). |
| `--tray` | Force a tray icon on Linux (needs a StatusNotifier host). |
| `--dry-run` | Don't open the deck. Log what it would show instead. Handy for checking herdr. |

## Troubleshooting

Start with `herdeck doctor`. It catches most problems.

| Problem | Fix |
|---|---|
| `deck … is busy` | Quit the Elgato Stream Deck app. If another herdeck is running, `herdeck doctor` shows its pid. |
| `no Stream Deck Neo found` | Use a data-capable USB-C cable (some are charge-only). On Linux, install the udev rule (see [Install](#install)). |
| `herdr: no socket` | Start herdr. For a named session, use `--session NAME` or set `[herdr] session`. |
| Keys stay gray or "unknown" | Install herdr's integration for your agent, e.g. `herdr integration install claude`. |
| Tapping a key doesn't bring the terminal forward | Set `[focus] activate_app` to your terminal's name, e.g. `"Ghostty"`. |
| macOS says the app can't be opened | Run `xattr -d com.apple.quarantine "$(which herdeck)"`. |
| Things look off after upgrading herdr | `herdeck doctor` warns about untested protocol versions. Please [open a compatibility issue](https://github.com/gancio-xyz/herdeck/issues/new/choose). |

**Logs:** in the foreground, herdeck logs to the terminal. As a macOS login service it logs to `~/Library/Logs/herdeck.log`. On Linux, run `journalctl --user -u herdeck`.

## Uninstall

```sh
herdeck service uninstall
rm ~/.local/bin/herdeck         # or: brew uninstall herdeck
rm -r ~/.config/herdeck         # optional: your config
```

## How it works

herdeck talks to herdr over its local socket API (NDJSON over a Unix socket), the same API the `herdr` CLI uses. It subscribes to per-agent status events for instant updates, and re-reads the workspace list on lifecycle events and every second as a safety net. It drives the deck over USB HID with the pure-Go [`rafaelmartins.com/p/streamdeck`](https://rafaelmartins.com/p/streamdeck/) library, so you don't need libusb or hidapi. See [`docs/herdr-protocol.md`](docs/herdr-protocol.md) and [`docs/compat.md`](docs/compat.md).

## Contributing

Bug reports, compatibility reports for new herdr versions, and PRs are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md).

```sh
make test     # go test -race ./...
make build    # bin/herdeck
make app      # dist/herdeck.app (macOS menu bar bundle)
```

## License

Apache-2.0. herdr is a separate project with its own license. herdeck only talks to it over its public socket API and includes none of its code.
