# Changelog

## v0.1.0 — first public release

herdeck turns an Elgato Stream Deck Neo into a physical status board for your [herdr](https://herdr.dev) agents. Glance at the deck to see what's blocked, working, done or idle, and tap a key to jump straight to it.

### Highlights

- **One key per herdr workspace**, colored by state: 🟠 blocked (pulsing), 🔵 working, 🟢 done, gray idle. Blocked workspaces sort first. Plain terminal workspaces are shown dimmed and last.
- **Tap to focus.** Tapping a key focuses the workspace's most urgent agent in herdr and brings your terminal to the front on macOS (detected automatically, or configurable).
- **Long-press to peek** at an agent's latest output on the info bar.
- **Info bar summary:** counts (`1 blocked · 2 working · 1 done`) plus the selected or blocked workspace.
- **Paging** with the two touch points when you have more than 8 workspaces. A touch point glows amber when a blocked workspace is on another page.
- **Menu bar app on macOS**, with counts, a clickable workspace list, connection status, pause, launch at login and quick access to the config.
- **Quick reply (opt-in, off by default):** long-press a blocked key to arm it, then tap to approve or use a touch point to deny. herdeck never sends to an agent that isn't blocked, and it checks that the reply landed.
- **Self-healing:** herdeck recovers by itself when herdr restarts or the deck is unplugged and replugged. The deck dims when nothing changes for a while.
- **`herdeck doctor`** checks the deck, herdr, agent integrations and your config, and tells you how to fix each problem.
- **Start at login** with `herdeck service install` (launchd on macOS, a systemd user unit on Linux).

### Install

- **Homebrew:** `brew install gancio-xyz/tap/herdeck`
- **Download:** pick the archive for your system below. `darwin_universal` works on any Mac. Unpack it, put `herdeck` on your `PATH`, and run `herdeck doctor`.
- **Go:** `go install github.com/gancio-xyz/herdeck/cmd/herdeck@v0.1.0`

See the [README](https://github.com/gancio-xyz/herdeck#readme) for the full setup.

### Before you start

- **Quit the Elgato Stream Deck app.** The deck accepts only one program at a time.
- **macOS:** the binary isn't notarized yet. If you downloaded it with a browser, run `xattr -d com.apple.quarantine /path/to/herdeck` once.
- **Linux:** install the udev rule shipped in the archive (`contrib/99-herdeck.rules`) so your user can access the deck.
- For accurate agent state, install herdr's integration for your agent, e.g. `herdr integration install claude`.

### Compatibility

- Tested with **herdr 0.7.1** (socket protocol 14). With other versions, herdeck warns but still runs. Please report what you find.
- macOS (Apple silicon and Intel) and Linux (amd64, arm64).
- Elgato Stream Deck Neo only.

### Known limitations

- The macOS binary is ad-hoc signed only, not notarized.
- Only the Stream Deck Neo is supported. Windows is not supported yet.
- On Linux, the tray icon is opt-in (`--tray`) and needs a StatusNotifier host.
