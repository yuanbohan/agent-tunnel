# Browser Input TUI Parity

**Date:** 2026-04-02
**Status:** Rejected

## Outcome

This design is no longer being pursued.

`agentunnel` keeps the input path byte-transparent:

- external client `input` frame
- relay WebSocket handler
- registry route-to-agent
- connector inbound loop
- `session.Hub.WriteInput`
- PTY stdin

We are not introducing a built-in `input_filter` contract or reference implementation in this repo. Any terminal-emulator-specific suppression remains a private client concern rather than part of the relay, agent, or protocol design.
