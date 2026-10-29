# herdeck — Implementation Plan

> A small, open-source daemon that turns an **Elgato Stream Deck Neo** into a physical status board and controller for **herdr** (the terminal agent multiplexer).
>
> This document is written for a coding agent. Work phase by phase, commit at the end of each phase, and stop at every **🧑 Human check** to ask the human to verify on real hardware.

Working name: `herdeck` (rename freely before the first public release).

## Revisions (2026-10-29, agreed with the human; these override the sections below)

1. **One workspace per key**, not one agent per key. Show only workspaces that contain an agent. Color comes from herdr's aggregated `workspace.agent_status`. A press focuses the most urgent agent in that workspace (blocked first).
2. **Menu bar app (option A).** A single process runs core + deck driver + a `fyne.io/systray` menu bar icon. The icon shows the counts and a workspace list (click to focus), deck status, pause, "Open config" and "Launch at login". `herdeck run --headless` keeps the pure daemon for Linux and debugging. Built so a settings window can be added later. On macOS it ships as a `.app` bundle, and launch-at-login replaces the launchd plist in Phase 6.
3. **herdr 0.7.1 has no `herdr api schema`.** `docs/herdr-protocol.md` (captured by probing) is the source of truth instead.
4. **State strategy: refetch, don't apply deltas.** Lifecycle event subscriptions replay old history, so they only *trigger* a debounced refetch of `workspace.list` + `agent.list`. Low-latency status comes from per-pane `pane.agent_status_changed` subscriptions (re-subscribed when the pane set changes), plus a 2 s safety poll.
5. Default `focus.activate_app` to the terminal herdeck detects at install (the human uses Apple Terminal). Keep it configurable.
6. Key labels: workspace label with the prefix shared by all labels removed (e.g. `myrepo TICKET-320` → `TICKET-320`).

---

## 1. Goal

Glance at the Neo and know, without switching windows, which herdr agents are **working**, **blocked**, **done**, or **idle**. Touch the deck only when something needs you.

### In scope (v1)
- Mirror herdr agent state onto the Neo's 8 keys (one agent per key, color = state).
- Info bar shows a summary ("2 blocked · 3 working") and details for the selected/blocked agent.
- Key press → focus that agent in herdr.
- Touch points → page through agents when there are more than 8.
- Runs as a single binary on **macOS** and **Linux**. Windows is a stretch goal (see §9).
- Open source on GitHub, installable via Homebrew and release binaries.

### Out of scope (v1)
- Running code *on* the deck (it is a USB HID peripheral; all logic runs on the host).
- Any Stream Deck model other than the Neo (don't preclude them architecturally, but don't implement or test them).
- Co-existing with the official Elgato Stream Deck app. The deck allows only one client; herdeck requires the Elgato app to be quit.
- Sending input to agents. This is Phase 5 and ships behind a config flag that is **off** by default.

---

## 2. Tech choices

| Concern | Choice | Why |
|---|---|---|
| Language | **Go** | Single static binary per OS, trivial cross-compilation, no runtime for users to install. |
| Deck access | `rafaelmartins.com/p/streamdeck` | Pure Go (no libusb/hidapi dependency), runs on Linux/macOS/Windows, and explicitly supports the Neo's keys, touch points and info bar. |
| Fallback deck lib | `python-elgato-streamdeck` (reference only) | Use its Neo implementation and Elgato's official HID docs (`https://docs.elgato.com/streamdeck/hid/stream-deck-neo`) as protocol reference if the Go lib misbehaves. Do not switch languages without asking. |
| herdr access | Raw socket API (NDJSON over Unix socket) | Same surface the herdr CLI uses. No shelling out in the hot path. |
| Rendering | Go stdlib `image` + `golang.org/x/image/font` with embedded Go fonts | No system font dependency. |
| Config | TOML at `~/.config/herdeck/config.toml` | Matches herdr's convention. |
| CLI | `cobra` or stdlib `flag` — agent's choice, keep it small | |
| Release | `goreleaser` → GitHub Releases + Homebrew tap | |
| License | Apache-2.0 (matches herdr) | **Verify the deck library's license is compatible before Phase 1 ends.** |

Neo hardware facts (from Elgato's HID docs — re-verify):
- VID `0x0FD9`, PID `0x009A`
- 4×2 keys, 96×96 px each
- Info bar ("window") 248×58 px, **not** touch
- 2 capacitive touch sensors, reported as extra buttons appended after the 8 keys; their color is settable

---

## 3. Architecture

```
          ┌──────────────┐   NDJSON / Unix socket   ┌──────────┐
          │  herdr       │◀────────────────────────▶│ herdr    │
          │  client      │   snapshot + events       │ server   │
          └──────┬───────┘                           └──────────┘
                 │ state updates
          ┌──────▼───────┐
          │  core        │  pure logic: agent list, paging,
          │  (model)     │  selection, key→agent mapping
          └──┬────────▲──┘
     render  │        │ input events
          ┌──▼────────┴──┐
          │  deck        │  rendering + HID I/O
          │  driver      │
          └──────────────┘
```

Packages (suggested):
- `cmd/herdeck` — CLI entrypoint
- `internal/herdr` — socket client, request/response, subscriptions, reconnect
- `internal/core` — state model and reducer. **No I/O.** Fully unit-tested.
- `internal/deck` — `Deck` interface + Neo implementation + `FakeDeck`
- `internal/render` — pure functions: `KeyImage(agent) image.Image`, `InfoBarImage(summary) image.Image`
- `internal/config`

Define interfaces at the boundaries so everything except the two drivers can be tested without hardware or a running herdr:

```go
type Deck interface {
    SetKeyImage(idx int, img image.Image) error
    SetInfoBar(img image.Image) error
    SetTouchColor(idx int, c color.Color) error
    SetBrightness(pct int) error
    Events() <-chan InputEvent // key down/up, touch down/up, with timestamps
    Close() error
}

type Herdr interface {
    Snapshot(ctx) (Session, error)
    Subscribe(ctx, subs []Subscription) (<-chan Event, error)
    Call(ctx, method string, params any) (json.RawMessage, error)
}
```

The core is a single goroutine that owns the state. It receives herdr events and deck input on channels and emits render commands. Debounce rendering to about 30 fps max; only redraw keys whose content changed.

---

## 4. Phases

### Phase 0 — Spike and protocol capture (no product code yet)

1. Install herdr locally if it is missing (`brew install herdr`) and record `herdr --version` in `docs/compat.md`.
2. Run `herdr api schema --json --output docs/herdr-api.schema.json` and commit it. **This schema is the source of truth.** The method and event names in this plan come from public docs and differ between herdr versions; for example, pane ids are `1-1` in older versions and `w1:p1` in 0.7.x. Never hardcode a field this plan mentions without confirming it in the schema.
3. From the schema, document in `docs/herdr-protocol.md`:
   - socket path resolution
   - request/response envelope (`id`, `method`, `params`, error shape)
   - how to get the full state (`session.snapshot` or `agent.list`)
   - the event subscription for agent state changes (expected: `events.subscribe` with `pane.agent_status_changed` or the agent-scoped equivalent)
   - the method to focus an agent (expected: `agent.focus`)
   - the methods for reading an agent's recent output (`agent.read` / `pane.read`) and for sending keys (`agent.send_keys`)
4. Write a throwaway Go program that opens the Neo, paints each key a different color, paints the info bar, and prints raw input events.

**🧑 Human check:** quit the Elgato Stream Deck app, run the spike, and confirm that colors appear, the info bar renders, and all 8 keys plus both touch points print events.

**Done when:** schema and protocol notes are committed, and the spike works on the human's Mac.

### Phase 1 — Skeleton

- Go module, package layout from §3, CI with lint and tests on `macos-latest` and `ubuntu-latest`.
- CLI subcommands:
  - `herdeck run` — the daemon (foreground; logs to stderr)
  - `herdeck devices` — list connected Neo decks with serial numbers
  - `herdeck doctor` — checks: deck found? deck openable (if busy, print "quit the Elgato Stream Deck app")? herdr socket found? herdr version within the supported range?
- Config loading with defaults; `herdeck config print-default`.
- Deck selection: first Neo found, or `--serial`.

**Done when:** `herdeck doctor` gives correct, actionable output for each of these cases: no deck, deck busy, no herdr, all good.

### Phase 2 — herdr client

- Socket path resolution mirroring herdr's own order: `--session <name>` → `HERDR_SOCKET_PATH` → `HERDR_SESSION` → default session socket. Confirm the exact paths from the schema or docs in Phase 0.
- NDJSON framing, request id correlation, timeouts.
- Startup: snapshot → build state → subscribe to agent state events, plus agent and pane create/close events.
- Reconnect with exponential backoff (cap 10 s) when herdr restarts. Re-snapshot after every reconnect.
- Build a fake herdr server for tests: listen on a temp Unix socket, replay a scripted sequence of events, and assert on the requests it receives.

**Done when:** `herdeck run --dry-run` (no deck) logs a correct, live agent list while the human starts and stops agents in herdr, and keeps working across `herdr server stop` / restart.

### Phase 3 — Rendering

- **Key tile (96×96):** background color = state; agent name (truncated with an ellipsis); small workspace label; a subtle pulsing or animated border for `blocked`. Optional, and only if cheap.
- Default palette, overridable in config:
  - `working` blue
  - `blocked` amber/red (this is the one that must pop)
  - `done` green
  - `idle` dim gray
  - empty slot black
- **Info bar (248×58):** line 1 shows counts ("2 blocked · 3 working · 1 done"); line 2 shows the selected agent's name and custom status/message, if herdr provides one.
- **Touch points:** lit when there is a previous/next page; amber if a blocked agent exists on another page.
- Sorting: blocked first, then working, done, idle. Stable within groups, so keys don't jump around constantly. Make sorting configurable: `state` | `workspace` | `stable`.
- Golden-image tests for `render` (commit PNGs under `testdata/`).

**🧑 Human check:** with 3+ agents running, the deck reflects state changes within about 1 s and stays readable from normal desk distance.

### Phase 4 — Input

- **Key press (short)** → call the focus method on that agent. On macOS, optionally also bring the terminal app to the front via `focus.activate_app = "Ghostty"` (or iTerm, Terminal…) in config; default empty. Run `open -a <app>` on macOS and do nothing elsewhere.
- **Touch point** → previous/next page.
- **Long press (≥ 600 ms, configurable)** → "inspect": the info bar shows the last 2 lines of that agent's output (strip ANSI codes) for 8 s, or until another press.
- Brightness: config value; dim to 20% after N minutes with no state change, and restore on any change or press.
- Handle deck unplug/replug: detect the device going away, wait for it to come back, then repaint everything.

**🧑 Human check:** focus works, paging works, and long-press preview is readable. Unplug and replug the deck while running; it recovers within 3 s.

### Phase 5 — Quick reply (opt-in, off by default)

The goal is to answer a blocked agent's prompt from the deck. This is the risky feature, so it uses a two-step flow:

1. Long-press a **blocked** agent → the info bar shows the pending prompt text. The key enters an "armed" state, shown by a distinct color or border, for 8 s.
2. While armed, a short press on the same key sends `reply.approve_keys`. A touch point sends `reply.deny_keys`. Any other key or a timeout disarms.

Rules:
- Disabled unless `reply.enabled = true`.
- Keys are configured per agent type; there are **no hardcoded defaults** for what "approve" means. Ship a commented example for Claude Code in the default config, and the README should say to check it against the agent version in use.
- After sending, verify the agent's state left `blocked` within 3 s. If it didn't, flash the key red and show "reply may not have landed". An accepted send-keys request doesn't prove the agent received it.
- Never send to an agent that isn't currently `blocked`.

**Done when:** a fake herdr test covers armed → approve, armed → timeout, sending to a non-blocked agent (must be refused), and a state that doesn't change after a send.

### Phase 6 — Packaging and open-source polish

- `README.md`: GIF/photo, install, quick start, the "quit the Elgato app" note, config reference, troubleshooting (`herdeck doctor`).
- Install paths:
  - Homebrew tap (`brew install <user>/tap/herdeck`)
  - GitHub Release binaries for darwin/arm64, darwin/amd64, linux/amd64, linux/arm64
  - `go install`
- macOS: example `launchd` plist + `herdeck service install|uninstall` (writes and loads the plist in `~/Library/LaunchAgents`).
- Linux: ship `contrib/99-herdeck.rules` (udev rule granting the user access to VID `0fd9` / PID `009a`) and an example systemd user unit.
- `CONTRIBUTING.md`, issue templates, `docs/compat.md` (tested herdr versions, OS versions).
- Trademark hygiene: describe the tool as "for the Elgato Stream Deck Neo". Don't use "Elgato" or "Stream Deck" in the project name or logo, and don't use their artwork.

---

## 5. Config sketch (`~/.config/herdeck/config.toml`)

```toml
[herdr]
session = ""            # empty = default session

[deck]
serial = ""             # empty = first Neo found
brightness = 70
dim_after_minutes = 10
dim_brightness = 20

[layout]
sort = "state"          # state | workspace | stable
long_press_ms = 600

[colors]
working = "#2F6FEB"
blocked = "#F5A524"
done    = "#2DA44E"
idle    = "#3A3A3A"

[focus]
activate_app = ""       # macOS only, e.g. "Ghostty"

[reply]
enabled = false
# [reply.agents.claude]
# approve_keys = ["Enter"]   # VERIFY against your Claude Code version
# deny_keys    = ["Escape"]
```

---

## 6. Testing strategy

- `internal/core`: table-driven tests covering state reducer, paging, sorting, armed-state timers (use an injectable clock).
- `internal/render`: golden PNGs.
- `internal/herdr`: fake socket server; test reconnect, malformed lines, and unknown event types (must be ignored, not fatal).
- Integration test: fake herdr + `FakeDeck` running the full daemon loop.
- Hardware tests are manual and listed as 🧑 checks. CI never needs a deck.

---

## 7. Definition of done (v1.0)

- [ ] `brew install` → `herdeck doctor` → `herdeck run` works on a fresh Mac with the Neo and herdr.
- [ ] Builds and passes tests on macOS and Linux in CI.
- [ ] State changes appear on the deck in ≤ 1 s.
- [ ] Survives herdr restart and deck unplug without manual intervention.
- [ ] Phase 5 is present, off by default, and tested.
- [ ] README lets a stranger set it up in under 10 minutes.

---

## 8. Risks and open questions (agent: resolve or flag, don't guess)

1. **herdr protocol drift.** herdr moves fast, so pin a supported version range in `docs/compat.md`. On connect, read the version and warn (don't crash) outside that range.
2. **macOS HID permissions.** The Neo should use a vendor-defined HID usage page, which normally needs no Input Monitoring permission. Confirm this in Phase 0, and document it if a prompt appears.
3. **Deck library gaps.** If the Go library lacks something the Neo needs (e.g., sleep or info-bar quirks), first check its issues and upstream. Then consider a small local patch referencing Elgato's HID docs. Ask before forking.
4. **Agent state accuracy.** herdr infers state, either from heuristics or from integrations like `herdr integration install claude`. The README should recommend installing herdr's integrations for accurate state.
5. **Focus semantics.** Check whether focusing an agent only changes herdr's focused pane or also affects attached clients, and document the behavior.

## 9. Windows (stretch, post-v1)

herdr on Windows is beta and uses a named pipe instead of a Unix socket. Use `github.com/Microsoft/go-winio` behind the same `Herdr` transport interface. The deck library already claims Windows support. Ship Windows binaries marked "experimental" only after someone with Windows + Neo + herdr verifies them.
