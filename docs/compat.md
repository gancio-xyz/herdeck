# Compatibility

| Component | Tested | Notes |
|---|---|---|
| herdr | 0.7.1 (protocol 14) | `herdr api schema` does not exist in 0.7.1; protocol notes captured by hand in `herdr-protocol.md` |
| macOS | Darwin 25.6 (arm64) | |
| Stream Deck Neo | VID `0x0FD9` / PID `0x009A` | |
| Go | 1.26.4 | |

Supported herdr protocol range: **14** (to be widened as new versions are verified).

## Libraries

| Library | License | Use |
|---|---|---|
| `rafaelmartins.com/p/streamdeck` | BSD-3-Clause | deck HID access (Neo keys, info bar, touch points) |
| `fyne.io/systray` | Apache-2.0 | macOS/Linux menu bar icon (cgo on macOS) |
| `golang.org/x/image` | BSD-3-Clause | fonts and drawing (embedded Go fonts) |
| `github.com/BurntSushi/toml` | MIT | config |

herdr itself is AGPL-3.0-or-later; herdeck only uses its socket API and links none of its code.
