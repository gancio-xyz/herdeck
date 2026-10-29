# Hardware checklist (manual, on a real Neo)

CI never touches hardware. Run through this before a release and after any
change to `internal/deck` or `internal/render`. Quit the Elgato Stream Deck
app first.

## Spike (`go run ./spike/deck`)
- [ ] Keys 1–8 show distinct colors and numbers in reading order (top-left = 1).
- [ ] Info bar shows two lines of text, the right way up.
- [ ] Touch points light up (left blue, right amber in the spike).
- [ ] Every key and both touch points log down/up with a plausible hold time.
- [ ] Holding one key doesn't delay events from another.
- [ ] No macOS permission prompt appears (Input Monitoring etc.).

## herdeck (`herdeck run`)
- [ ] `herdeck doctor` reports the deck and its firmware. With the Elgato app running it says **busy**; after quitting the app it's OK.
- [ ] With 3+ agents, state changes show on the deck within ~1 s and are readable from desk distance.
- [ ] Tap → herdr focuses that workspace **and the attached terminal view switches**, and Terminal comes to the front. (Note whether `agent.focus` changes the visible client view or only herdr's internal focus.)
- [ ] **Left** touch point = previous page, **right** = next (driver assumes `TOUCH_POINT_1` is left).
- [ ] Long-press shows agent output on the info bar; long lines scroll.
- [ ] Unplug while running: the menu bar shows "Deck: not connected". Replug: the deck fully repaints within 3 s.
- [ ] Ctrl+C / Quit leaves the deck showing "herdeck stopped" and exits promptly (closing the device unblocks the read loop).
- [ ] Dimming after `dim_after_minutes`, and it wakes on any change or touch.

## Quick reply (with `[reply] enabled = true` and keys configured)
- [ ] Long-press a blocked key → ARMED badge, magenta touch points, prompt text on the bar.
- [ ] Tap again → approve keys are sent and the agent leaves blocked.
- [ ] Touch point while armed → deny keys are sent.
- [ ] Waiting 8 s disarms; pressing another key disarms without acting.
