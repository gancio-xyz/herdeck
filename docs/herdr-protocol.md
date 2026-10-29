# herdr socket protocol (as observed)

herdr 0.7.1 has **no `herdr api schema` command**, so there is no machine-readable
schema to commit. Everything below was captured by probing a live 0.7.1 server
(protocol 14) on 2026-10-28. Re-verify on every herdr upgrade.

## Socket path

- Default session: `~/.config/herdr/herdr.sock` (not `$XDG_RUNTIME_DIR`).
- `HERDR_SOCKET_PATH` overrides it (same as the herdr CLI).
- Named sessions: `herdr session list --json` returns `socket_path` per session.
  Shell out to it once at startup to resolve `--session <name>`; never in the hot path.

```json
{"sessions":[{"default":true,"name":"default","running":true,
  "session_dir":"~/.config/herdr","socket_path":"~/.config/herdr/herdr.sock"}]}
```

## Envelope (NDJSON, one JSON object per line)

Request:

```json
{"id":"1","method":"agent.list","params":{}}
```

`params` is required, even if it's empty (`{}`).

Success: `{"id":"1","result":{"type":"<result_type>", ...}}`

Error:   `{"id":"1","error":{"code":"agent_not_found","message":"..."}}`

If the request fails to parse, `id` comes back as `""` with
`code: "invalid_request"`. The message names the missing field, which is how
param names were discovered. An unknown method returns the full list of valid
methods in the message.

## Version check

```json
→ {"id":"1","method":"ping","params":{}}
← {"id":"1","result":{"type":"pong","version":"0.7.1","protocol":14,"capabilities":{"live_handoff":true}}}
```

Gate compatibility on `protocol` (integer), and log `version`.

## State

`workspace.list` → `result.workspaces[]`:

| field | example | notes |
|---|---|---|
| `workspace_id` | `w2S` | stable while the workspace lives |
| `number` | `5` | position shown in herdr UI; changes on reorder/close |
| `label` | `myrepo TICKET-1213` | user-facing name |
| `agent_status` | `blocked` | **aggregated by herdr** across panes |
| `focused` | `false` | |
| `active_tab_id`, `tab_count`, `pane_count` | | |

`agent.list` → `result.agents[]` (only panes with a detected or reported agent):

| field | example |
|---|---|
| `agent` | `claude` |
| `agent_status` | `idle` · `working` · `blocked` · `done` · `unknown` |
| `pane_id` | `w2S:p1` |
| `terminal_id` | `term_65eea954f5fd4c` |
| `workspace_id`, `tab_id` | |
| `cwd`, `foreground_cwd` | |
| `focused` | |
| `agent_session` | `{agent, kind, source, value}` (optional) |

`custom_status` is **not** in `agent.list`, `agent.get` or `pane.get`. It only
shows up in `pane.agent_status_changed` events. The `message` passed to
`report-agent` is not exposed anywhere observed.

## Events

```json
→ {"id":"s","method":"events.subscribe","params":{"subscriptions":[{"type":"pane.agent_status_changed","pane_id":"w2S:p1"}]}}
← {"id":"s","result":{"type":"subscription_started"}}
← {"event":"pane.agent_status_changed","data":{"agent":"probe","agent_status":"working","custom_status":"probing","pane_id":"w2X:p1","workspace_id":"w2X"}}
```

- The connection stays open and streams events after `subscription_started`.
  Open a dedicated connection for each subscription set.
- **`pane.agent_status_changed` requires a `pane_id`** per subscription. You can't
  subscribe to "all panes", so re-subscribe on a new connection whenever the agent
  set changes. Events arrive about 50 ms after the change, nothing is replayed,
  and `custom_status` is included when set. When an agent is released, the event
  has no `agent` field and its status is `unknown`.
- Lifecycle types that need no params: `workspace.created`, `workspace.updated`,
  `workspace.closed`, `workspace.renamed`, `workspace.focused`, `tab.*`,
  `pane.created`, `pane.closed`, `pane.focused`, `pane.agent_detected`.
  Note the `event` field spelling differs: lifecycle events use underscores
  (`workspace_created`), agent status uses a dot (`pane.agent_status_changed`).
- ⚠️ **Lifecycle subscriptions replay history.** Right after subscribing, herdr
  streams old events (including ones for workspaces that no longer exist) at
  about 10 events/s, for well over 8 s. Live events are mixed in with the replay
  and have no marker to tell them apart. **Never apply lifecycle events as state
  deltas.** Treat them only as signals to refetch, and avoid `*.focused` (it
  dominates the replay).
- `workspace.updated` did **not** fire when an agent's status changed.

### Strategy used by herdeck

1. Refetch `workspace.list` + `agent.list` (about 1 ms each locally) on startup,
   on any refetch signal (debounced), and on a slow safety poll (every 2 s).
2. Keep one subscription connection for `pane.agent_status_changed` covering
   every current agent pane. Replace it when the set of panes changes.
3. Keep one lifecycle connection (`workspace.created/closed/renamed`,
   `pane.created/closed`, `pane.agent_detected`) whose events only trigger a
   debounced refetch.

## Actions

| method | params | notes |
|---|---|---|
| `workspace.focus` | `{"workspace_id":"w2S"}` | |
| `agent.focus` | `{"target":"w2S:p1"}` | target accepts a pane id, terminal id or unique agent name |
| `agent.read` / `pane.read` | `{"target"/"pane_id", "source":"visible\|recent\|recent-unwrapped", "lines":N, "format":"text\|ansi"}` | returns `result.read.text` |
| `pane.send_keys` | `{"pane_id":"…","keys":["Enter"]}` | errors with `pane_not_found` for a bad id |
| `pane.send_text` | `{"pane_id":"…","text":"…"}` | |
| `notification.show` | | |

`agent.read` with `source: recent, lines: 3` on a Claude Code pane returns only
TUI chrome (prompt box, borders, footer). Reading useful output needs a larger
`lines` value plus filtering out box-drawing and footer lines.

Raw sample lines are in `docs/samples/`.
