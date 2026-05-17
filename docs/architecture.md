# Architecture

这个文件是本仓库的本地指针，不再维护跨仓库 system architecture 的第二份详细说明。

当前跨仓库 SSOT：

- GitHub: `yuanbohan/agent-tunnel-protocols/docs/architecture.md`
- sibling checkout: `../agent-tunnel-protocols/docs/architecture.md`

修改以下内容时，先更新 protocols SSOT，或在同一组 PR 中同步更新：

- `tunnel run`、`tunnel daemon`、Relay、PostgreSQL、STUN、Android companion 的 ownership boundary。
- direct-first + Relay fallback data flow。
- trusted computer list projection。
- mobile session list、preview、detail、input 的 ownership。
- pairing、trust store、revocation、uninstall/re-pairing behavior。
- Relay 不作为 terminal data plane 的安全边界。

本仓库实现入口：

- `cmd/tunnel`：local CLI。
- `cmd/relay`：Relay server。
- `internal/tunnel/session/`：PTY lifecycle、local terminal、terminal mirror。
- `internal/tunnel/connector/`：`/agent/ws` connector。
- `internal/tunnel/daemon/`：daemon control、broker、pairing、tmux workspace、connectivity。
- `internal/protocol/`：Relay-facing wire types 和 daemon transport payloads。
- `internal/relay/auth/`：auth、app sessions、agent tokens。
- `internal/relay/device/`：online computer routing 和 launch correlation。
- `internal/relay/session/`：live `/agent/ws` owner metadata。
- `internal/relay/connectivity/`：pairing visibility、direct rendezvous、fallback tunnel routing。
- `internal/relay/handler/`：Gin router、REST、WebSockets。
- `internal/relay/store/postgres/`：Relay durable state。

本地实现改动后，推荐检查：

```bash
go test ./internal/tunnel/... ./internal/protocol ./internal/relay/...
```
