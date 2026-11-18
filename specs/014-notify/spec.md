# Feature Specification: 通知（企微 / 钉钉 / 飞书群机器人 + 通用 Webhook）

**Feature Branch**: `014-notify`

**Created**: 2026-11-18

**Status**: Done

**Input**: 待开发功能清单 F2——执行结束推送到群机器人。用户当前没有真实 webhook，需用本地接收端完成联调。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 配一个群机器人，执行结束收到消息 (Priority: P1)

在「通知设置」里选择渠道（企业微信 / 钉钉 / 飞书 / 通用 Webhook），粘贴群机器人 webhook 地址（钉钉/飞书可选填签名密钥），保存后点「发送测试」：群里收到一条测试消息。

**Independent Test**: 用本地 mock 接收端，分别配置 4 个渠道 → 4 种 payload 都能被正确解析（企微 `msgtype=markdown`、钉钉 `msgtype=markdown + title`、飞书 `msg_type=text`、通用 `{event,run,...}`）。

### User Story 2 - 执行结束自动推送，失败必推 (Priority: P1)

执行进入终态时自动推送：**失败/错误/取消总是推送**；成功是否推送由「成功也通知」开关控制。消息含项目、执行号、状态、通过/失败/错误数、耗时。

**Independent Test**: 分别触发一次成功与一次失败执行 → 失败必有一条推送；关闭「成功也通知」时成功不推送。

### User Story 3 - 签名与投递结果可查 (Priority: P1)

钉钉/飞书填了密钥时按各自算法签名；每次投递记录结果（成功/失败、HTTP 状态、响应片段、尝试次数），设置页可查最近投递，失败原因不用去翻日志。

**Independent Test**: mock 端独立计算签名与请求比对一致；mock 返回 500 时重试 3 次并记录 `failed`。

### Edge Cases

- 同一项目多个通知配置：逐个发送，互不影响（一个失败不阻断其它）。
- 没有配置 / 全部为「仅失败通知」但本次成功：不发送，也不写投递记录。
- webhook 地址非法（非 http/https）：保存时拒绝（40000），避免执行时才炸。
- 密钥掩码：非管理员读取时 `secret` 显示 `***`；回传 `***` 表示不修改。
- 投递超时：单次 5s；最多 3 次尝试（间隔 500ms、1s），全失败记 `failed`。
- 响应体过大：日志只存前 512 字符。
- 执行被取消：按失败类推送（用户主动终止也需要知道结果）。
- 通知发送是**异步**的：绝不能因为群机器人不可用拖慢或影响执行收尾。
- 通用 Webhook：可选密钥时用 `X-Hub-Signature-256: sha256=<hmac>` 签名 body。

## Requirements *(mandatory)*

### Functional Requirements

- **FR-1**：沿用 `notify_config`（project_id/name/channel/config/on_success），config 存 `{webhook_url, secret}`；新增 `notify_log` 表（project_id/config_id/run_id/channel/status/http_status/response/attempts/error）。
- **FR-2**：CRUD：`GET/POST /projects/{id}/notifications`、`PUT/DELETE /notifications/{id}`；写操作仅管理员，读对非管理员掩码 secret。
- **FR-3**：`POST /notifications/{id}/test` 立即发送测试消息（同步返回结果，便于"配完就知道通不通"）。
- **FR-4**：渠道 payload：
  - 企微：`{"msgtype":"markdown","markdown":{"content":...}}`
  - 钉钉：`{"msgtype":"markdown","markdown":{"title":...,"text":...}}`；有 secret 时 URL 追加 `timestamp` 与 `sign`（HMAC-SHA256(key=secret, data=`timestamp\nsecret`) → base64 → urlencode）
  - 飞书：`{"msg_type":"text","content":{"text":...}}`；有 secret 时 body 追加 `timestamp` 与 `sign`（HMAC-SHA256(key=`timestamp\nsecret`, data="") → base64）
  - 通用：`{"event":"run_finished|test","text":...,"project":...,"run":{...}}`；有 secret 时加 `X-Hub-Signature-256`
- **FR-5**：执行终态钩子：失败/错误/取消必推；成功按 `on_success`。异步执行，失败只记日志与投递记录。
- **FR-6**：投递：5s 超时、最多 3 次尝试；每次终态记录 `notify_log`。
- **FR-7**：`GET /projects/{id}/notifications/logs?limit=50` 返回最近投递（含 mask 后的 webhook？不需要，只存 channel）。
- **FR-8**：前端「通知设置」页：配置列表（渠道/是否成功通知/掩码地址）、新建/编辑、发送测试、最近投递表。
- **FR-9**：文档：契约章节、清单 F2 ✅、README、验收边界表（注明按官方机器人协议实现，真实群需用户自测）。

### Key Entities

- **NotifyConfig**：渠道 + webhook + secret + 是否成功通知。
- **NotifyLog**：一次投递结果。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-1**：4 种渠道 payload 结构正确（mock 接收端断言）。
- **SC-2**：钉钉/飞书签名可被独立复算验证一致。
- **SC-3**：mock 返回 500 时重试 3 次，日志 `status=failed`。
- **SC-4**：关闭「成功也通知」后，成功执行不产生投递；失败执行必产生。
- **SC-5**：`scripts/check.sh` + CI 全绿。
