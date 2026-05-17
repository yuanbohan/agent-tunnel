# Relay API

这个文件是本仓库的本地指针，不再维护 public Relay API 的第二份详细说明。

当前跨仓库 SSOT：

- GitHub: `yuanbohan/agent-tunnel-protocols/docs/api.md`
- sibling checkout: `../agent-tunnel-protocols/docs/api.md`

修改以下内容时，先更新 protocols SSOT，或在同一组 PR 中同步更新：

- app-facing `/api/...` endpoint。
- auth、token、account policy。
- request/response shape。
- app-visible error code 或 message。
- `GET /api/connectivity/ws`。
- `GET /connectivity/computer/ws`。
- `GET /connectivity/tunnel/ws`。
- removed endpoint compatibility boundary。

本仓库实现入口：

- `internal/relay/handler/new.go`：router assembly 和当前 route inventory。
- `internal/relay/handler/api/`：app-facing HTTP handlers。
- `internal/relay/handler/connectivity/`：connectivity WebSocket handlers。
- `internal/relay/handler/agent/`：`/agent/ws`。
- `internal/relay/handler/device/`：`/device/ws`。
- `internal/relay/handler/response/`：response envelope 和 error codes。
- `internal/relay/auth/`：app sessions 和 agent tokens。
- `internal/relay/connectivity/`：pairing、rendezvous、fallback tunnel live state。
- `internal/relay/device/`：online computer launch routing。

本地实现改动后，推荐检查：

```bash
go test ./internal/relay/handler ./internal/relay/connectivity ./internal/relay/device ./internal/relay/auth
```
