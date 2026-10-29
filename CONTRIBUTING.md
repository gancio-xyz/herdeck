# Contributing

Thanks for helping! herdeck is small on purpose. Keep changes focused.

## Layout

| Package | What | I/O? |
|---|---|---|
| `cmd/herdeck` | CLI: `run`, `devices`, `doctor`, `config`, `service` | yes |
| `internal/app` | event loop wiring herdr + core + render + deck; terminal activation; single-instance lock | yes |
| `internal/core` | **pure** state machine: sorting, paging, presses, inspect, quick reply, dimming | no |
| `internal/render` | **pure** key / info bar images (golden-tested) | no |
| `internal/herdr` | socket client, subscriptions, reconnect; `herdrtest` fake server | yes |
| `internal/deck` | Neo driver, hot-plug; `FakeDeck` / `FakeConnector` | yes |
| `internal/tray` | menu bar (fyne.io/systray) | yes |
| `internal/config`, `internal/service`, `internal/model` | config file, launch-at-login, shared types | |

## Rules of thumb

- **herdr protocol:** never assume a method, field or event shape. Confirm it against a live server and update `docs/herdr-protocol.md` (and `docs/compat.md` when testing a new herdr version). Unknown events and fields must be ignored, never fatal.
- **core stays pure:** no goroutines, no clock, no I/O. Pass `now` in and assert on `View` / `Cmd`.
- **Visual changes:** run `make golden` and look at `internal/render/testdata/contact.png` before committing.
- **Quick reply is safety-critical:** never send keys to an agent that isn't blocked, never ship default keys, and keep it off by default.
- `make test` must pass on macOS and Linux. CI never needs a deck.

## Hardware testing

Changes to `internal/deck` or rendering need a manual check on a real Neo: colors, info bar, all 8 keys plus both touch points, and unplug/replug while running. Say in the PR what you tested on.
