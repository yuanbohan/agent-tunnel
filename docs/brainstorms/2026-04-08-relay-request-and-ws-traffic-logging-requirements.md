---
date: 2026-04-08
topic: relay-request-and-ws-traffic-logging
---

# Relay Request And WS Traffic Logging

## Problem Frame
When relay-side HTTP or WebSocket behavior is wrong, the current logs are too event-specific to explain what request came in, how it finished, how long it took, or how much data moved. That makes it harder to debug authentication failures, replay-range behavior, slow requests, and high-volume WebSocket sessions. The next change should make relay traffic observable without logging request or response bodies.

## Requirements

**HTTP Request Visibility**
- R1. The relay must emit a structured log entry for each non-health-check HTTP request it serves, including requests that later upgrade to WebSocket.
- R2. Each HTTP request log must omit request and response bodies.
- R3. Each HTTP request log must include enough metadata to identify and troubleshoot the request outcome, including method, request target, response status, and request duration.
- R4. Each HTTP request log must preserve the raw query string so operators can correlate replay-range parameters such as `from` and `to` with response size or behavior.
- R5. Each HTTP request log must include request and response size signals when those sizes are knowable at the HTTP layer.

**WebSocket Traffic Visibility**
- R6. The relay must emit structured connection lifecycle logs for relay-managed WebSocket endpoints, including `/api/updates/ws` and `/agent/ws`.
- R7. WebSocket lifecycle logging must omit message bodies and terminal content.
- R8. For each WebSocket connection, the relay must surface enough aggregate traffic data to reason about session volume, including connection duration, message counts, and byte counts in each direction.
- R9. WebSocket logging must make it possible to distinguish upgrade/setup failures from successfully established connections that later close or error.

**Scope And Noise Controls**
- R10. This logging work applies to relay-side HTTP and WebSocket entrypoints only; it does not add connector-side outbound request logging in this iteration.
- R11. `GET /healthz` must be excluded from the new request logging by default so routine probes do not dominate operator logs.
- R12. The new logs must fit the relay's existing structured logging style so they can be filtered and correlated alongside current relay events.

## Success Criteria
- An operator investigating a relay issue can identify which non-health-check HTTP requests were received, how they completed, how long they took, and what request target they used without inspecting bodies.
- An operator investigating WebSocket-heavy behavior can tell whether a failure happened during upgrade or during a live connection, and can estimate session traffic volume from aggregate counts and byte totals.
- Logging adds useful observability to replay and remote-session troubleshooting without exposing terminal content or request payloads.

## Scope Boundaries
- No request or response body logging.
- No terminal-content inspection, previews, or semantic derivation from WebSocket payloads.
- No connector-side or `agentunnel` local outbound request logging in this iteration.
- No requirement to persist logs or introduce metrics systems as part of this change.

## Key Decisions
- Relay-only scope: The relay is the meaningful HTTP/WS server boundary in the current product, so this iteration targets relay ingress rather than the local connector.
- Access log plus WebSocket aggregates: Plain HTTP access logs are not enough for this product because a large share of meaningful traffic happens after WebSocket upgrade.
- Raw query retained: Query parameters such as replay ranges materially affect behavior and data volume, so logging the full request target is more useful than path-only logging.
- `healthz` excluded: Health checks are intentionally omitted from the new request logs to keep operational logs focused on higher-signal traffic.
- Bodyless logging: The logging goal is observability and traffic sizing, not payload capture.

## Dependencies / Assumptions
- The relay continues to use the current structured JSON logger in `relay/logger.go`.
- The main relay HTTP and WebSocket entrypoints remain in `relay/server.go`.

## Outstanding Questions

### Resolve Before Planning

### Deferred to Planning
- [Affects R5][Technical] What exact source of truth should define HTTP request and response size when `Content-Length` is absent or when a response is streamed?
- [Affects R8][Technical] What byte-counting rule should WebSocket logs use so aggregate traffic numbers are useful and consistent without overpromising wire-level precision?
- [Affects R12][Technical] What event names and field names best fit the existing relay log taxonomy while keeping request logs and connection-summary logs easy to filter?

## Next Steps
→ /prompts:ce-plan for structured implementation planning
