---
title: refactor: Align relay HTTP interaction format to API envelope contract
# plan
type: refactor
status: draft
date: 2026-04-13
---

# refactor: Align relay HTTP interaction format to API envelope contract

## Overview

The current worktree already moved relay handlers and middleware to envelope responses, but the test and e2e layers still assume legacy raw JSON/status patterns. This refactor makes the API contract end-to-end consistent with `docs/api.md`: every successful response is wrapped as `{ code, message, body }` and every client/parser path must decode this envelope first.

## Problem to solve

- `internal/relay/handler` now emits envelope JSON in many paths.
- `internal/relay/handler/rest_api_test.go` and `internal/relay/handler/ws_api_test.go` still decode raw JSON and expect HTTP `204` in places that now return `200 + envelope`.
- `internal/e2e/client.go` still decodes raw response bodies and treats `ChangePassword`/logout/revoke as no-content outcomes.

This creates drift between implementation and validation layers and makes future API changes fragile.

## Requirements trace

- R1. 统一响应格式必须使用 `code/message/body` envelope（参考 `docs/api.md`）。
- R2. 所有成功回包可含 `body`，可为 `null`；`http` 状态码统一保持 `200`（除明确需返回错误码场景）。
- R3. handler 测试与 ws 测试以 envelope 解码为唯一断言入口。
- R4. e2e 客户端将后端报错通过 `code/message` 暴露，并避免吞掉非零 code。
- R5. 与 API 一致的成功码、错误码与返回语义，确保 `logout/password-change/revoke` 不再用 `204`。

## Scope

- 涉及 server-side HTTP/WS handler 测试、e2e client 解码逻辑与通用测试 helper。
- 不涉及协议设计重构、数据库 schema、认证策略变更或终端渲染数据流重写。

## Technical approach

- 增加单一测试侧 envelope 解码 helper：`internal/relay/handler/test_helpers_test.go`。
- 将 handler 测试恢复为“先 decode envelope，再断言 `code==0` 与 `body` 内容”。
- 更新 `internal/e2e/client.go` 的 `doJSON`/`parse` 流程：
  - 先 decode envelope。
  - `code != 0` 时返回带 `code/message` 的错误。
  - `code==0` 时再 decode `body`。
- 调整所有 200/204 相关断言与分支，优先按 `docs/api.md` 与已有 handler 行为。

## Implementation units (ordered)

### Unit 1: 统一解析入口
1. 文件: `internal/relay/handler/test_helpers_test.go`
2. 内容:
   1. 增加 `decodeAPIEnvelopeBody(t, resp *httptest.ResponseRecorder, target any, expectCode ...int)`。
   2. 校验 `content-type`（可选）与 JSON 解码边界，返回 `code/message/body`。
   3. 提供 `requireEnvelopeOK(...)` 助手供后续测试复用。

### Unit 2: 重写 REST handler 测试
1. 文件: `internal/relay/handler/rest_api_test.go`
2. 内容:
   1. 用 `decodeAPIEnvelopeBody` 替换所有裸 JSON 反序列化断言。
   2. 将登录/刷新/会话列表/创建会话等成功断言改为 envelope.body 断言。
   3. 将 `logout/password-change/revoke` 的成功状态从 `204` 修改为 `200`（当 API 确认）。
   4. 失败路径断言 `code/message`，并保留 `http` 状态码的现有覆盖。

### Unit 3: 重写 WS 相关 handler 测试
1. 文件: `internal/relay/handler/ws_api_test.go`
2. 内容:
   1. 同步采用 envelope 解包路径。
   2. 修正 relogin/attach-sessions 等返回结构在 `code/body` 下的断言。
   3. 同步处理 200 响应语义迁移。

### Unit 4: 重写 e2e 客户端解码
1. 文件: `internal/e2e/client.go`
2. 内容:
   1. `doJSON` 改为先 decode envelope。
   2. 新增 `apiResponse` 内部结构复用，支持 `body == null`。
   3. `ChangePassword`/`Logout` 等方法与实际 handler 协议一致，不再期待 `StatusNoContent`。
   4. 错误分支返回 `code` 与 `message`（建议携带 `status` 或原始响应片段用于调试）。

### Unit 5: 执行一致性检查
1. 文件/目录:
   1. `internal/relay/handler/rest_api_test.go`
   2. `internal/relay/handler/ws_api_test.go`
   3. `internal/e2e/client.go`
2. 验证项:
   1. 成功响应全部可被 envelope 统一解码。
   2. `code==0` 与 `body` 解码一致。
   3. `code!=0` 时调用方返回失败而非静默解析 body。

## Acceptance criteria

- `logout/password-change/revoke` 相关用例成功路径走 `200 + envelope`。
- `rest_api_test.go` 与 `ws_api_test.go` 中不再直接将 HTTP body decode 到业务 DTO。
- `internal/e2e/client.go` 兼容 `code/message/body` 并传播错误。
- 所有修改通过 `go test`（尽量仅运行受影响包）即可在本地确认，不要求全量验证。

## Risks and non-goals

- 风险 1: 个别旧接口仍返回裸体，不在本次计划内；发现时需同步补充 `api.md` 并逐步收口。
- 风险 2: 更严格的 unknown fields/尾部数据校验会放大测试夹具差异，需要同步整理 fixture。
- 非目标: 引入新的客户端抽象或重构 transport stack。

## Dependencies

1. 先完成 Unit 1，再做 Unit 2/3。
2. Unit 4 与 Unit 2/3 可并行，但建议在测试 helper 稳定后再合并。
3. 依赖最新的 `docs/api.md` 与当前 handler envelope 定义（如有差异，先同步文档）。
