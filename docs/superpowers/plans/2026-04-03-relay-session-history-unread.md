# Relay Session History And Unread Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add live-only in-memory session history, shared unread tracking, a jump-to-first-unread session detail flow, and a compact dashboard with favicon-based launcher identity plus a wrapped mini terminal preview.

**Architecture:** The relay registry becomes the source of truth for rolling output history, frame sequence numbers, preview state, and shared read markers. The web app consumes the expanded session list payload, fetches paged history before attaching to live output, and renders a reading-optimized dashboard preview with xterm wrap mode while preserving a full live terminal in the detail page.

**Tech Stack:** Go 1.25, TypeScript, Vite, xterm.js, Vitest, gorilla/websocket

---

### Task 1: Extend relay wire types for history and unread metadata

**Files:**
- Modify: `protocol/message.go`
- Modify: `protocol/relay_types_test.go`
- Test: `protocol/relay_types_test.go`

- [ ] **Step 1: Write the failing protocol test**

Add assertions to `protocol/relay_types_test.go` that a `SessionInfo` JSON payload round-trips the new fields:

```go
func TestSessionInfoJSONIncludesHistoryMetadata(t *testing.T) {
	now := time.Date(2026, 4, 3, 10, 0, 0, 0, time.UTC)
	info := SessionInfo{
		SessionID:    "sess-1",
		Launcher:     "gemini",
		CWD:          "/tmp/demo",
		StartedAt:    now,
		LatestSeq:    42,
		LastReadSeq:  37,
		UnreadCount:  5,
		PreviewSeq:   42,
		PreviewB64:   "SGVsbG8=",
		LastActiveAt: &now,
	}

	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	text := string(data)
	for _, want := range []string{
		`"latest_seq":42`,
		`"last_read_seq":37`,
		`"unread_count":5`,
		`"preview_seq":42`,
		`"preview_b64":"SGVsbG8="`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("Marshal() missing %q in %s", want, text)
		}
	}
}
```

- [ ] **Step 2: Run the protocol test to verify it fails**

Run: `go test ./protocol/...`
Expected: FAIL because `SessionInfo` does not yet define the new fields.

- [ ] **Step 3: Write the minimal protocol implementation**

Add these fields to `protocol.SessionInfo` in `protocol/message.go`:

```go
LatestSeq   uint64 `json:"latest_seq,omitempty"`
LastReadSeq uint64 `json:"last_read_seq,omitempty"`
UnreadCount uint64 `json:"unread_count,omitempty"`
PreviewSeq  uint64 `json:"preview_seq,omitempty"`
PreviewB64  string `json:"preview_b64,omitempty"`
```

- [ ] **Step 4: Run the protocol test to verify it passes**

Run: `go test ./protocol/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add protocol/message.go protocol/relay_types_test.go
git commit -m "feat(protocol): add session history metadata fields"
```

---

### Task 2: Add rolling history and read state to relay sessions

**Files:**
- Modify: `relay/registry.go`
- Create: `relay/history.go`
- Create: `relay/history_test.go`
- Test: `relay/history_test.go`

- [ ] **Step 1: Write the failing relay history tests**

Create `relay/history_test.go` with tests for sequence assignment, whole-frame eviction, and read-state math:

```go
func TestHistoryBufferEvictsWholeFrames(t *testing.T) {
	buf := newHistoryBuffer(10)
	buf.append([]byte("1234"))
	buf.append([]byte("5678"))
	buf.append([]byte("90AB"))

	if got := buf.totalBytes(); got != 8 {
		t.Fatalf("totalBytes() = %d, want 8 after whole-frame eviction", got)
	}
	frames := buf.frames()
	if len(frames) != 2 || string(frames[0].Data) != "5678" || string(frames[1].Data) != "90AB" {
		t.Fatalf("frames = %#v, want newest two whole frames", frames)
	}
}

func TestLiveSessionUnreadCountUsesSeqDelta(t *testing.T) {
	live := &liveSession{}
	live.latestSeq = 12
	live.lastReadSeq = 9
	if got := live.unreadCount(); got != 3 {
		t.Fatalf("unreadCount() = %d, want 3", got)
	}
}
```

- [ ] **Step 2: Run the relay tests to verify they fail**

Run: `go test ./relay/...`
Expected: FAIL because the history buffer and unread helpers do not exist.

- [ ] **Step 3: Write the minimal relay history implementation**

Create `relay/history.go` with:
- a `historyFrame` struct containing `Seq`, `Data`, and `Size`
- a `historyBuffer` type that stores frames up to `10 << 20` bytes
- append logic that evicts oldest whole frames only
- helper methods for newest-page and older-page lookup

Add state to `liveSession` in `relay/registry.go`:

```go
history      *historyBuffer
latestSeq    uint64
lastReadSeq  uint64
previewSeq   uint64
previewData  []byte
```

Add a helper:

```go
func (s *liveSession) unreadCount() uint64 {
	if s.latestSeq <= s.lastReadSeq {
		return 0
	}
	return s.latestSeq - s.lastReadSeq
}
```

- [ ] **Step 4: Integrate output-touch logic with history state**

Update `touchOutput` in `relay/registry.go` so that each accepted output frame:
- increments `latestSeq`
- appends the raw chunk to the history buffer
- updates `previewSeq`
- copies the raw chunk into `previewData`
- writes `LatestSeq`, `LastReadSeq`, `UnreadCount`, `PreviewSeq`, and `PreviewB64` into `live.info`

Use:

```go
live.latestSeq++
seq := live.latestSeq
live.history.appendWithSeq(seq, chunk)
live.previewSeq = seq
live.previewData = append(live.previewData[:0], chunk...)
live.info.LatestSeq = live.latestSeq
live.info.LastReadSeq = live.lastReadSeq
live.info.UnreadCount = live.unreadCount()
live.info.PreviewSeq = live.previewSeq
live.info.PreviewB64 = base64.StdEncoding.EncodeToString(live.previewData)
```

- [ ] **Step 5: Run the relay tests to verify they pass**

Run: `go test ./relay/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add relay/registry.go relay/history.go relay/history_test.go
git commit -m "feat(relay): add rolling session history and unread state"
```

---

### Task 3: Add history and read-marker HTTP endpoints

**Files:**
- Modify: `relay/server.go`
- Modify: `relay/server_test.go`
- Test: `relay/server_test.go`

- [ ] **Step 1: Write the failing server tests**

Add tests to `relay/server_test.go` for:
- `GET /api/sessions/:id/history`
- `POST /api/sessions/:id/read`

Use shapes like:

```go
func TestHistoryEndpointReturnsNewestPage(t *testing.T) {
	// register sess-1, feed three output frames, call /api/sessions/sess-1/history
	// expect newest frames in chronological order plus latest_seq/last_read_seq
}

func TestReadEndpointAdvancesReadSeqWithoutRegression(t *testing.T) {
	// set latestSeq = 5, post seq 3 then seq 2
	// expect last_read_seq stays 3 and unread_count remains 2
}
```

- [ ] **Step 2: Run the server tests to verify they fail**

Run: `go test ./relay/...`
Expected: FAIL because the endpoints do not exist.

- [ ] **Step 3: Implement registry helpers for history paging and read updates**

Add methods on `Registry` in `relay/registry.go`:

```go
func (r *Registry) HistoryPage(sessionID string, before uint64, limit int, maxBytes int) (HistoryPage, error)
func (r *Registry) MarkRead(sessionID string, seq uint64) (protocol.SessionInfo, error)
```

`HistoryPage` should include:

```go
type HistoryPage struct {
	Messages    []HistoryMessage `json:"messages"`
	HasMore     bool             `json:"has_more"`
	LatestSeq   uint64           `json:"latest_seq"`
	LastReadSeq uint64           `json:"last_read_seq"`
}

type HistoryMessage struct {
	Seq     uint64 `json:"seq"`
	DataB64 string `json:"data_b64"`
}
```

- [ ] **Step 4: Implement the HTTP handlers**

Add routes in `relay/server.go`:
- `GET /api/sessions/:id/history`
- `POST /api/sessions/:id/read`

Handler rules:
- parse `before`, `limit`, and `max_bytes` with sane defaults
- reject invalid methods with `405`
- return `404` for missing sessions
- `POST /read` accepts `{"seq": <number>}` JSON
- `MarkRead` must update `live.info.LastReadSeq` and `live.info.UnreadCount`

- [ ] **Step 5: Run the server tests to verify they pass**

Run: `go test ./relay/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add relay/server.go relay/server_test.go relay/registry.go
git commit -m "feat(relay): add session history and read APIs"
```

---

### Task 4: Add sequence numbers to browser live output frames

**Files:**
- Modify: `protocol/message.go`
- Modify: `relay/server.go`
- Modify: `web/src/protocol.ts`
- Modify: `relay/server_test.go`
- Test: `relay/server_test.go`

- [ ] **Step 1: Write the failing integration test**

Add a relay WebSocket test that attaches a browser, emits one output frame, and expects the browser-side JSON payload to include `seq`.

- [ ] **Step 2: Run the relay tests to verify they fail**

Run: `go test ./relay/...`
Expected: FAIL because browser output payloads do not yet include `seq`.

- [ ] **Step 3: Extend the message shape**

Add `Seq` to `protocol.Message` in `protocol/message.go`:

```go
Seq uint64 `json:"seq,omitempty"`
```

Populate it in the browser output path in `relay/server.go` from the current frame sequence number.

Mirror the field in `web/src/protocol.ts`:

```ts
export type Message =
  | { type: 'output'; data: string; seq?: number }
  | { type: 'input'; data: string }
  | { type: 'resize'; cols: number; rows: number }
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./relay/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add protocol/message.go relay/server.go relay/server_test.go web/src/protocol.ts
git commit -m "feat(protocol): include sequence numbers in browser output frames"
```

---

### Task 5: Teach the web app about history and read APIs

**Files:**
- Modify: `web/src/api.ts`
- Modify: `web/src/types.ts`
- Create: `web/src/history.ts`
- Create: `web/src/history.test.ts`
- Test: `web/src/history.test.ts`

- [ ] **Step 1: Write the failing web tests**

Create `web/src/history.test.ts` covering:
- session title fallback to basename
- unread-count helpers
- history-page merge order

Example:

```ts
it('keeps messages in chronological order when prepending older history', () => {
  const current = [{ seq: 3, data_b64: 'Mw==' }, { seq: 4, data_b64: 'NA==' }]
  const older = [{ seq: 1, data_b64: 'MQ==' }, { seq: 2, data_b64: 'Mg==' }]
  expect(prependHistoryPage(current, older).map((m) => m.seq)).toEqual([1, 2, 3, 4])
})
```

- [ ] **Step 2: Run the web test to verify it fails**

Run: `cd web && npm test -- --run web/src/history.test.ts`
Expected: FAIL because the helpers do not exist.

- [ ] **Step 3: Implement the API/types layer**

Update `web/src/types.ts` with:
- `latest_seq`
- `last_read_seq`
- `unread_count`
- `preview_seq`
- `preview_b64`
- `HistoryMessage`
- `HistoryPage`

Extend `web/src/api.ts` with:

```ts
export async function fetchSessionHistory(sessionId: string, before?: number): Promise<HistoryPage>
export async function markSessionRead(sessionId: string, seq: number): Promise<void>
```

Create `web/src/history.ts` with:
- `sessionDisplayTitle(session)`
- `prependHistoryPage(current, older)`
- `firstUnreadSeq(page)`

- [ ] **Step 4: Run the web test to verify it passes**

Run: `cd web && npm test -- --run web/src/history.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/api.ts web/src/types.ts web/src/history.ts web/src/history.test.ts
git commit -m "feat(web): add history and unread client models"
```

---

### Task 6: Replay recent history in the session detail page

**Files:**
- Modify: `web/src/app.ts`
- Modify: `web/src/terminal.ts`
- Modify: `web/src/session_page.ts`
- Create: `web/src/session_page.test.ts`
- Test: `web/src/session_page.test.ts`

- [ ] **Step 1: Write the failing session-page tests**

Add tests for:
- initial history fetch before live attach
- mark-read after initial render
- unread button label generation

Example:

```ts
it('builds a jump button label from unread count', () => {
  expect(unreadJumpLabel(12)).toBe('Jump to 12 unread')
})
```

- [ ] **Step 2: Run the web tests to verify they fail**

Run: `cd web && npm test -- --run web/src/session_page.test.ts`
Expected: FAIL because the session history helpers do not exist.

- [ ] **Step 3: Implement session-page history bootstrap**

Update `web/src/app.ts` so `renderSession(sessionId)`:
- fetches the newest history page before attaching live output
- writes those messages into the terminal in order
- records the current `latest_seq`
- connects the WebSocket only after history render starts
- calls `markSessionRead(sessionId, latestSeq)` once both the history render and live attach are ready

Use helper signatures in `web/src/session_page.ts` such as:

```ts
export function unreadJumpLabel(unreadCount: number): string
export function firstUnreadSeq(latestSeq: number, unreadCount: number): number | null
```

- [ ] **Step 4: Run the web tests to verify they pass**

Run: `cd web && npm test -- --run web/src/session_page.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/app.ts web/src/terminal.ts web/src/session_page.ts web/src/session_page.test.ts
git commit -m "feat(web): replay recent history before live session attach"
```

---

### Task 7: Add backward paging and jump-to-first-unread behavior

**Files:**
- Modify: `web/src/app.ts`
- Modify: `web/src/session_page.ts`
- Modify: `web/src/style.css`
- Test: `web/src/session_page.test.ts`

- [ ] **Step 1: Write the failing tests**

Add tests for:
- determining the target sequence for the first unread frame
- preserving chronological order while prepending older pages
- visibility rules for the floating unread button

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd web && npm test -- --run web/src/session_page.test.ts`
Expected: FAIL because the paging and unread-jump helpers do not exist.

- [ ] **Step 3: Implement upward paging and unread jump**

Update the session page so that:
- scrolling near the top triggers `fetchSessionHistory(sessionId, oldestLoadedSeq)`
- older frames prepend before the current terminal buffer
- a floating button appears when `unread_count > 0`
- clicking the button targets `last_read_seq + 1`
- if that sequence is not loaded yet, keep fetching older pages until it is or `has_more` becomes false

Add CSS for the floating unread action in `web/src/style.css`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd web && npm test -- --run web/src/session_page.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/app.ts web/src/session_page.ts web/src/style.css web/src/session_page.test.ts
git commit -m "feat(web): add unread jump and backward session paging"
```

---

### Task 8: Simplify the dashboard card UI and add official favicons

**Files:**
- Modify: `web/src/dashboard.ts`
- Modify: `web/src/dashboard.test.ts`
- Modify: `web/src/style.css`
- Create: `web/public/launchers/openai.ico`
- Create: `web/public/launchers/gemini.ico`
- Create: `web/public/launchers/claude.ico`
- Test: `web/src/dashboard.test.ts`

- [ ] **Step 1: Write the failing dashboard tests**

Add tests that assert:
- the card title prefers `label`, then basename of `cwd`
- the card does not render `command_preview`
- the card renders an unread badge when `unread_count > 0`
- the card renders an image-based launcher icon path

- [ ] **Step 2: Run the dashboard tests to verify they fail**

Run: `cd web && npm test -- --run web/src/dashboard.test.ts`
Expected: FAIL because the dashboard still uses text initials and renders duplicate metadata.

- [ ] **Step 3: Implement the compact dashboard card**

Update `web/src/dashboard.ts` so each card renders:
- favicon image
- title
- launcher name
- time
- unread badge
- mini preview container

Remove:
- command preview line
- full cwd line

Add launcher favicon mapping:

```ts
export function launcherFavicon(launcher: string): string {
  switch (launcher.trim().toLowerCase()) {
    case 'openai':
    case 'codex':
      return '/launchers/openai.ico'
    case 'gemini':
      return '/launchers/gemini.ico'
    case 'claude':
      return '/launchers/claude.ico'
    default:
      return '/launchers/openai.ico'
  }
}
```

- [ ] **Step 4: Add the favicon assets**

Download the official favicon files into:
- `web/public/launchers/openai.ico`
- `web/public/launchers/gemini.ico`
- `web/public/launchers/claude.ico`

- [ ] **Step 5: Run the dashboard tests to verify they pass**

Run: `cd web && npm test -- --run web/src/dashboard.test.ts`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/src/dashboard.ts web/src/dashboard.test.ts web/src/style.css web/public/launchers
git commit -m "feat(web): simplify dashboard cards and add launcher favicons"
```

---

### Task 9: Replace dashboard plain-text preview with a wrapped mini xterm

**Files:**
- Modify: `web/src/app.ts`
- Modify: `web/src/dashboard.ts`
- Modify: `web/src/terminal.ts`
- Modify: `web/src/style.css`
- Create: `web/src/dashboard_preview.ts`
- Create: `web/src/dashboard_preview.test.ts`
- Test: `web/src/dashboard_preview.test.ts`

- [ ] **Step 1: Write the failing preview tests**

Create `web/src/dashboard_preview.test.ts` with tests for:
- decoding `preview_b64`
- replacing the preview content when a new frame arrives
- using wrap mode instead of horizontal overflow

- [ ] **Step 2: Run the preview tests to verify they fail**

Run: `cd web && npm test -- --run web/src/dashboard_preview.test.ts`
Expected: FAIL because the dashboard preview renderer does not exist.

- [ ] **Step 3: Implement mini-terminal preview mounting**

Create `web/src/dashboard_preview.ts` with a helper like:

```ts
export function mountDashboardPreview(container: HTMLElement, previewB64?: string): TerminalHandle
```

Behavior:
- create a read-only terminal
- set display mode to `wrap`
- write only the latest preview frame
- expose a method to replace the current preview when the session card updates

Update `web/src/app.ts` after `list.innerHTML = sessions.map(renderSessionCard).join('')` to find each preview container and mount a mini terminal using `session.preview_b64`.

- [ ] **Step 4: Run the preview tests to verify they pass**

Run: `cd web && npm test -- --run web/src/dashboard_preview.test.ts`
Expected: PASS.

- [ ] **Step 5: Run full project verification**

Run:
- `go test ./...`
- `cd web && npm test`
- `cd web && npm run build`

Expected:
- all Go tests pass
- all Vitest tests pass
- Vite build succeeds

- [ ] **Step 6: Commit**

```bash
git add relay web protocol
git commit -m "feat(web): render wrapped mini terminal previews in dashboard"
```

---

### Spec Coverage Check

- Rolling live-only history: covered by Tasks 2 and 3
- Shared unread state and unread counts: covered by Tasks 2, 3, and 7
- History replay on detail-page refresh: covered by Task 6
- Backward paging: covered by Task 7
- Jump to first unread: covered by Task 7
- Compact dashboard cards: covered by Task 8
- Official launcher favicons: covered by Task 8
- Wrapped mini xterm preview with latest frame only: covered by Task 9
- Sequence numbers in browser output frames: covered by Task 4

### Placeholder Scan

No `TODO`, `TBD`, or deferred placeholders were left in task steps. Each task names exact files, concrete commands, and the target behavior to implement and verify.

### Type Consistency Check

The plan uses one consistent naming set across Go and TypeScript:
- `latest_seq`
- `last_read_seq`
- `unread_count`
- `preview_seq`
- `preview_b64`
- `seq`
- `data_b64`

Plan complete and saved to `docs/superpowers/plans/2026-04-03-relay-session-history-unread.md`. Two execution options:

**1. Subagent-Driven (recommended)** - I dispatch a fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** - Execute tasks in this session using executing-plans, batch execution with checkpoints

Which approach?
