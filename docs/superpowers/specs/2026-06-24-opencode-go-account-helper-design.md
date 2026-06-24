# OpenCode Go 账号助手安全 MVP 设计

## 目标

在 CPA 中增加 OpenCode Go 账号管理能力，用于把用户已登录的 OpenCode Go 账号同步为 CPA 可调度的 OpenAI-compatible API 账号，并展示 rolling / weekly / monthly 用量。

本方案不在 CPA 服务器上启动浏览器，不要求安装本机 helper，不做自动注册或自动领奖。浏览器登录态采集和切号由 Tampermonkey 脚本在 `https://opencode.ai/*` 页面内完成。

## 非目标

- 不做自动注册流程。
- 不做协议级自动领取邀请奖励。
- 不在 CPA 管理页提供“打开工作空间”功能。
- 不静默读取或上传 Cookie。
- 不尝试绕过 OpenCode、浏览器或 Tampermonkey 的安全限制。

## 组件

### CPA 后端

新增 OpenCode Go 管理接口，负责接收 Tampermonkey 脚本同步的数据，保存账号资料、可选 Cookie、workspaceId、Go API key 和 usage 快照。

CPA 后端还负责把 Go API key 写入现有 `openai-compatibility` 配置，生成可调度的 provider，例如 `opencode-go`。API 请求只依赖 `sk-...` 和 base URL，不依赖 Cookie。

### CPA 管理页

新增 OpenCode Go 页面，展示：

- 账号别名、邮箱或用户名。
- workspaceId。
- API key 同步状态，脱敏显示。
- rolling / weekly / monthly 用量和重置时间。
- 最近同步时间。
- “复制脚本配置”或“安装脚本”入口。

管理页不负责打开 OpenCode 工作空间，不负责切换浏览器账号。

### Tampermonkey 脚本

脚本名：`opencode go账号助手`

脚本作为独立项目维护，不放进 CPA 后端仓库。建议后续新建项目文件夹，例如 `/Users/kogeki/dev/opencode-go-account-helper-userscript`，用于保存脚本源码、构建配置、README 和生成后的 `.user.js`。

脚本只匹配 `https://opencode.ai/*`。它在 OpenCode 页面内提供轻量浮窗或菜单命令：

- 同步当前账号到 CPA。
- 查询并同步当前 usage。
- 从 OpenCode keys 页面或相关页面提取 Go API key。
- 从 CPA 拉取账号列表。
- 用户手动确认后切换到指定账号。

Cookie 同步默认关闭，必须用户主动点击确认。普通 `document.cookie` 读不到 HttpOnly Cookie；如果脚本环境支持 `GM_cookie`，可尝试读取 `opencode.ai` 域 Cookie，否则提示只能同步 API key / usage / workspaceId。

## 数据流

### 同步当前账号

1. 用户在 OpenCode 页面登录目标账号。
2. Tampermonkey 脚本识别当前 URL 中的 `workspaceId`。
3. 脚本从页面或请求结果提取 Go API key。
4. 脚本调用 CPA 后端同步接口，上传 workspaceId、账号显示信息、API key、usage 快照，以及可选 Cookie。
5. CPA 后端保存账号记录。
6. CPA 后端按配置创建或更新 `openai-compatibility` provider 条目。
7. CPA 热重载后，API 请求可以通过 `opencode-go` provider 使用该账号额度。

### 切号

1. 用户在 OpenCode 页面打开脚本面板。
2. 脚本从 CPA 获取已保存账号列表。
3. 用户选择账号并点击切换。
4. 脚本显示确认提示，说明会覆盖当前 `opencode.ai` Cookie。
5. 用户确认后，脚本写入该账号 Cookie 并刷新页面。
6. 如果缺少 HttpOnly Cookie 或写入失败，脚本提示重新手动登录或重新同步 Cookie。

### 用量展示

1. Tampermonkey 脚本在 OpenCode 页面内查询 rolling / weekly / monthly 用量。
2. 脚本将 usage 快照同步到 CPA。
3. CPA 管理页显示最新快照和更新时间。
4. CPA 后端可提供手动刷新接口，但不把 usage 查询放入 API 请求热路径。

## CPA 后端接口草案

- `GET /v0/management/opencode-go/accounts`
  返回账号列表，敏感字段脱敏。

- `POST /v0/management/opencode-go/sync`
  接收脚本同步的账号资料、workspaceId、API key、usage 快照和可选 Cookie。

- `POST /v0/management/opencode-go/accounts/:id/sync-provider`
  将指定账号写入或更新 `openai-compatibility` provider。

- `DELETE /v0/management/opencode-go/accounts/:id`
  删除 CPA 保存的 OpenCode Go 账号资料；是否同时删除 provider 条目由请求参数控制。

- `GET /v0/management/opencode-go/userscript-config`
  返回脚本需要的 CPA 地址、接口路径和当前管理认证方式提示，不返回明文敏感 token。

Tampermonkey 脚本调用 CPA 时使用用户在脚本配置中填写的管理认证信息或专用同步 token。CPA 不在公开页面直接下发可用于写入账号的明文 token。

## 存储模型草案

每个 OpenCode Go 账号保存：

- `id`
- `alias`
- `email`
- `username`
- `workspace_id`
- `api_key_encrypted`
- `cookie_encrypted`，可选
- `usage_snapshot`
- `provider_name`
- `base_url`
- `created_at`
- `updated_at`
- `last_synced_at`

管理页和 API 响应中，API key 与 Cookie 永远脱敏。

## OpenAI-compatible 导入规则

默认创建或更新一个 OpenAI-compatible provider：

- `name`: `opencode-go`
- `base-url`: 由用户在 CPA 管理页配置；如果缺失，账号资料仍保存，但 provider 同步应失败并提示先配置 base URL。
- `api-keys`: 每个同步账号一个 key。
- `models`: 使用管理页配置或从端点拉取。

如果 CPA 现有 provider 名已存在，则追加或更新对应 API key；如果 API key 已存在，则只刷新别名、workspaceId、usage 和最近同步时间。

## 安全与权限

- Cookie 上传必须用户主动启用。
- 切号必须用户手动确认。
- 脚本只运行在 `https://opencode.ai/*`。
- 脚本配置 CPA 地址时需要用户输入或从 CPA 管理页复制。
- 后端接口沿用 CPA 管理认证，不新增匿名写接口。
- 所有日志必须避免打印 Cookie、完整 API key 和认证 token。

## 错误处理

- 无法读取 HttpOnly Cookie：提示使用 API key-only 模式或支持 `GM_cookie` 的 Tampermonkey 环境。
- API key 提取失败：提示用户进入 OpenCode keys 页面后重试。
- usage 查询失败：保留旧快照，并显示失败原因和时间。
- provider 写入失败：保留账号同步数据，但标记 provider 同步失败。
- 切号失败：提示用户重新手动登录并重新同步 Cookie。

## 测试计划

- 后端单测：账号同步、去重、脱敏、provider 写入、删除行为。
- 后端单测：usage 快照保存和失败时保留旧数据。
- 管理页测试：账号列表、同步状态、脱敏显示、错误提示。
- 脚本手动测试：当前账号同步、API key 提取、usage 同步、切号确认流程。
- 回归测试：现有 `openai-compatibility` provider 和 API key 访问授权不受影响。

## 分期

### 第一期

- CPA 后端账号同步与 provider 写入。
- CPA 管理页 OpenCode Go 账号列表。
- Tampermonkey 脚本同步当前账号、usage 和 API key。
- 脚本切号基础流程。

### 第二期

- 更完善的 Cookie 能力检测。
- 多账号批量 usage 刷新。
- provider 模型列表自动拉取与管理页联动。
- 更好的脚本安装和配置引导。
