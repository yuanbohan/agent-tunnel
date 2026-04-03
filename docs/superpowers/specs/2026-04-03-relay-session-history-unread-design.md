# Relay Session History And Unread Design

**Date:** 2026-04-03
**Status:** Approved

## Goal

Upgrade the relay web UI from a real-time-only session list into a lightweight live monitoring surface with:
- compact session cards
- official launcher favicons for Claude, Gemini, and OpenAI
- a readable mini terminal preview in the dashboard
- replayable live-session history in the session detail page
- shared per-session unread tracking with a jump-to-first-unread action

The feature remains intentionally live-only. If a session disconnects, it disappears along with its history and read state.

## Product Decisions

| Decision | Choice |
|----------|--------|
| History retention | **Live-only, in memory** |
| History size | **10 MB per live session** |
| History unit | **Whole output frames** |
| Overflow policy | **Evict oldest whole frames only** |
| Read-state scope | **Shared per session, not per device** |
| Unread metric | **Unread output-frame count** |
| Mark-as-read trigger | **After session detail initial history render completes and live attach is active** |
| Dashboard preview | **Only the latest output frame** |
| Dashboard preview rendering | **Mini xterm in wrap mode** |
| Session detail initial load | **Newest page only, not full history** |
| Older history loading | **Lazy backward paging on scroll-up** |
| Revisit action | **Jump to first unread frame, not resume exact scroll position** |
| Offline sessions | **Not retained** |

## Problems To Solve

### 1. Dashboard cards are noisy and repetitive

The current session list repeats the same launcher and project information across:
- label
- launcher name
- command preview
- full working directory

This makes the list harder to scan and wastes limited horizontal space.

### 2. Dashboard preview degrades terminal output

The current `last_preview` field is plain text derived from stripped terminal output. This loses:
- ANSI styling
- cursor movement
- clear-line and repaint behavior
- multi-line terminal layout

As a result, TUI-heavy output often appears with broken symbols or unreadable formatting.

### 3. Session detail pages lose context on refresh

The current detail page only shows new output that arrives after the WebSocket attaches. If no new output arrives, the page is blank after refresh.

### 4. There is no shared notion of unread progress

The operator wants to know:
- how many messages arrived since the last time the session was meaningfully viewed
- where the unread range begins
- how to jump directly to that boundary

## Scope

### In Scope

- Extend relay session state with rolling output history
- Add per-frame sequence numbers
- Track shared `last_read_seq` per live session
- Add dashboard unread badge
- Replace dashboard plain-text preview with a mini xterm preview
- Simplify dashboard card layout
- Add session history paging endpoint
- Add session read-marker endpoint
- Extend live output frames sent to browsers with `seq`
- Load newest history on session page entry
- Backfill older history on upward scroll
- Add a `Jump to first unread` control

### Out Of Scope

- Persistent history across relay restarts
- Viewing disconnected sessions
- Per-device read tracking
- Search within terminal history
- Exact restoration of the last visual scroll position
- Full-width terminal fidelity in the dashboard preview

## User Experience

## Dashboard

Each session card becomes a compact monitoring tile.

### Card Header

Display only:
- launcher favicon
- primary title: `label`, or if missing, the basename of `cwd`
- launcher name: `Claude`, `Gemini`, or `OpenAI`
- relative last-active time
- unread badge when `unread_count > 0`

Do not display:
- `command_preview`
- full `cwd`
- duplicate launcher labels in multiple places

The full `cwd` may be exposed only as a tooltip or low-emphasis attribute.

### Card Preview

Each card has a read-only mini terminal preview:
- render only the latest output frame
- when a newer frame arrives, replace the previous preview entirely
- force wrap to the card width
- never introduce horizontal page scrolling
- cap preview height and allow internal vertical scrolling if the latest frame is tall

The dashboard preview is intentionally readability-first. It does not preserve the real PTY width semantics of the live session.

## Session Detail Page

The detail page becomes a replayable live terminal.

### Initial Load

On page open:
1. fetch the newest history page
2. render that page into xterm in chronological order
3. connect the live WebSocket stream
4. once both steps are complete, mark the session read up to the current `latest_seq`

This prevents blank pages after refresh while avoiding a large first paint.

### Lazy Backward Paging

The detail page does not load the full 10 MB at once.

Instead:
- fetch only the newest page first
- when the user scrolls near the top, fetch the next older page
- prepend older frames in chronological order
- continue until `has_more` becomes false

### Jump To First Unread

If the session has unread output when opened:
- show a floating control such as `Jump to 17 unread`
- target the first unread frame, defined as `last_read_seq + 1`
- if that frame is not yet loaded locally, page backward until it is available
- scroll to that frame and visually highlight it briefly

This is not “resume exact position.” It is “jump to the first unread message boundary.”

## Data Model

Each live session in relay memory tracks:

```go
type historyFrame struct {
	Seq  uint64
	Data []byte
	Size int
}

type liveSession struct {
	info          protocol.SessionInfo
	peer          AgentPeer
	sinks         map[string]session.OutputSink
	history       []historyFrame
	historyBytes  int
	latestSeq     uint64
	lastReadSeq   uint64
	previewSeq    uint64
	previewData   []byte
}
```

Notes:
- `history` stores whole browser-visible output messages, one frame per agent `output` chunk
- `historyBytes` tracks total retained bytes
- `latestSeq` increments by one for each output frame
- `lastReadSeq` is the shared read marker for the session
- `previewData` is the newest output frame and powers the dashboard preview

## History Retention Rules

- Maximum retained history per live session: `10 MB`
- The retention limit applies to raw output-frame bytes only
- On overflow, evict oldest frames until the session is within budget
- Never split or truncate a frame
- If a single output frame itself exceeds `10 MB`, retain only that newest frame and drop all older frames

This preserves message boundaries and aligns unread counting with retained history.

## Read-State Rules

- Unread count is based on output-frame count, not lines or characters
- `unread_count = latest_seq - last_read_seq`
- The dashboard never mutates read state
- The session detail page updates read state only after the initial history page is rendered and the live attach is active
- Read state is monotonically increasing
- If a client submits a lower sequence number than the current `last_read_seq`, the relay ignores the regression

## API Design

### `GET /api/sessions`

Continue returning live session metadata, with new fields added:

```json
[
  {
    "session_id": "sess-1",
    "launcher": "gemini",
    "label": "demo",
    "cwd": "/Users/example/project",
    "started_at": "2026-04-03T10:00:00Z",
    "last_active_at": "2026-04-03T10:05:00Z",
    "latest_seq": 42,
    "last_read_seq": 37,
    "unread_count": 5,
    "preview_seq": 42,
    "preview_b64": "SGVsbG8gZnJvbSB0aGUgbGF0ZXN0IGNo..."
  }
]
```

Field notes:
- `preview_b64` is the latest raw output frame for dashboard rendering
- `preview_seq` identifies the frame shown in the dashboard
- `command_preview` can remain in the protocol for compatibility, but the new dashboard UI will stop displaying it

### `GET /api/sessions/:id/history`

Fetch one page of retained history.

Query parameters:
- `before` optional: fetch frames with `seq < before`
- `limit` optional: target number of frames
- `max_bytes` optional: target payload size

Response:

```json
{
  "messages": [
    { "seq": 38, "data_b64": "..." },
    { "seq": 39, "data_b64": "..." },
    { "seq": 40, "data_b64": "..." }
  ],
  "has_more": true,
  "latest_seq": 42,
  "last_read_seq": 37
}
```

Paging rules:
- no `before` means “return the newest page”
- server targets at least five frames when possible
- server should also respect a payload budget such as `64 KB`
- returned `messages` are ordered oldest to newest for direct rendering

### `POST /api/sessions/:id/read`

Advance the shared read marker.

Request:

```json
{
  "seq": 42
}
```

Behavior:
- update `last_read_seq = max(last_read_seq, seq)`
- return the resulting read state

### `GET /api/sessions/:id/ws`

Keep the existing live browser attach WebSocket, but extend output frames:

```json
{
  "type": "output",
  "seq": 43,
  "data": "SGVsbG8="
}
```

This gives the session detail page stable message anchors for unread jumps.

## Frontend Rendering Strategy

## Shared xterm Wrapper

The existing terminal wrapper already supports display modes. Extend it to support:
- normal session attach rendering for the detail page
- wrap-to-container rendering for dashboard mini previews

The dashboard preview should:
- disable input
- fit to card width
- preserve terminal styling where possible
- allow vertical overflow inside the preview pane

## Dashboard Card Hierarchy

The card answers only three questions:
- which session is this
- which launcher owns it
- what is the most recent output

Everything else is removed or downgraded.

## Session Detail Anchoring

The detail page must track where each `seq` lands in the rendered history so that:
- `Jump to first unread` can target `last_read_seq + 1`
- older pages can be prepended without losing the user’s visible position

Exact internal xterm anchoring can be implemented with lightweight DOM markers or a parallel frame-to-offset structure. The design does not require a specific anchoring mechanism as long as the unread jump stays accurate.

## Failure Handling

- If history fetch fails, show a recoverable placeholder above the terminal instead of rendering an empty session
- If read-marker update fails, keep the UI usable and retry on the next successful attach or explicit refresh
- If a session disappears between dashboard view and detail open, the detail page should show a not-found state
- If a requested unread target has been evicted from the rolling buffer, jump to the oldest retained frame and indicate that older unread content is no longer available

## Testing Requirements

### Relay

- history retention respects whole-frame boundaries
- overflow evicts oldest complete frames
- unread count updates with `latest_seq` and `last_read_seq`
- read marker never moves backward
- history endpoint returns newest and older pages correctly
- live output frames sent to browsers include `seq`

### Web

- dashboard card renders the compact header shape
- dashboard title falls back to the basename of `cwd`
- dashboard unread badge appears only when needed
- dashboard preview container wraps and does not trigger horizontal scrolling
- session page fetches recent history before opening the live attach
- session page can page older history upward
- unread jump targets the first unread frame

## Open Questions Resolved

| Question | Resolution |
|----------|------------|
| Per-device vs shared unread state | **Shared per session** |
| Live-only vs offline history | **Live-only** |
| Initial history load size | **Newest page only** |
| Dashboard preview retention | **Latest frame only** |
| Resume exact position vs first unread | **Jump to first unread** |
| Wide terminal behavior in dashboard | **Wrap to card width** |

## Rollout Order

1. Add rolling history, `latest_seq`, `last_read_seq`, and preview state to relay sessions
2. Extend session list responses and browser output frames
3. Add history and read-marker endpoints
4. Add initial history replay to the session detail page
5. Add backward paging and unread jump behavior
6. Simplify dashboard cards and switch to official favicons
7. Replace plain-text preview with mini xterm preview
