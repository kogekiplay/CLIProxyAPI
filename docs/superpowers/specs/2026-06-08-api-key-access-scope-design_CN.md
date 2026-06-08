# API Key 访问范围设计

## 目标

给每个 CPA 客户端 API key 增加独立的授权范围。这样可以做到：

- 第一个 key 可以访问所有认证文件和所有 AI provider。
- 第二个 key 只能访问指定认证文件，或指定 AI provider。
- 管理 API 和管理页面都支持配置这个功能，不需要用户手写 YAML。

## 当前项目情况

现在客户端 API key 配在 `api-keys` 里。请求进来后，`internal/access/config_access` 会校验 key，`internal/api/server.go` 会把通过认证的 key 放到 Gin 上下文里的 `userApiKey`。

真正选择某个认证文件或 provider 的逻辑在 `sdk/cliproxy/auth`。这里已经能拿到请求上下文，也已经保存了最新配置，所以适合在“选择认证文件之前”做访问范围过滤。

认证文件和配置里的 provider key 最终都会变成 `coreauth.Auth`，里面有 provider、ID、文件名、元数据和管理页面用的稳定 `auth-index`。

管理页面本身不是这个仓库里的普通前端源码。CPA 会从 `router-for-me/Cli-Proxy-API-Management-Center` 下载 `management.html`。所以这个仓库负责后端管理 API；页面 UI 要去管理中心那个仓库改。

访问范围是授权硬边界，不是调度偏好。额度耗尽、冷却、重试、模型 fallback、provider fallback、认证文件 fallback，都只能在当前 API key 被授权的认证文件和 provider 里面发生。

## 方案选择

1. 直接把 `api-keys` 从字符串列表改成对象列表。
   这个方式对新配置很直观，但会破坏现有配置兼容性，也会影响已经依赖 `api-keys` 是字符串列表的管理客户端。

2. 新增一个并行配置 `api-key-access`，用现有 API key 作为规则 key。
   这个方式最稳。现有 `api-keys` 不变，老用户无感升级，也可以逐个 key 增加访问范围。推荐使用这个方案。

3. 给每个 API key 增加一个名字或 ID，再让规则引用这个 ID。
   这个方式更漂亮，但需要更大的配置迁移。当前项目还没有 API key identity 概念，第一版不建议这样做。

第一版采用方案 2。

## 配置格式

新增顶层配置：

```yaml
api-key-access:
  key-all:
    access: all
  key-limited:
    providers:
      - claude
      - gemini
    auth-files:
      - claude-a@example.com.json
      - user@gmail.com-project.json
```

含义：

- `key-all` 对应的 API key 可以访问全部。
- `key-limited` 对应的 API key 只能访问 `claude` 和 `gemini`，并且只能使用列出的认证文件。

字段说明：

- `access`: 可选。设置成 `all` 表示不限制。
- `providers`: 可选。允许访问的 provider 列表，例如 `claude`、`gemini`、`codex`、`xai`、`kimi`、`antigravity`、`openai-compatibility`，也可以是插件 provider。
- `auth-files`: 可选。允许访问的认证文件 ID 或文件名。普通认证文件用文件名；配置生成的 key 可以用管理 API 返回的 auth ID。管理页面可以展示 `auth-index`，但配置里保存 auth ID 或文件名，不保存展示用 index。

兼容规则：

- 没有配置 `api-key-access` 时，所有现有 API key 保持老行为，也就是都可以访问全部。
- 某个 API key 没有对应规则时，默认也可以访问全部。
- 如果规则里写了 `access: all`，即使同时写了 `providers` 或 `auth-files`，也按全量访问处理。
- 如果同时写了 `providers` 和 `auth-files`，必须两个条件都满足，才允许使用某个认证文件。
- 如果规则是受限模式，但 `providers` 和 `auth-files` 都是空的，那么这个 key 不能使用任何认证文件。
- 如果 `api-key-access` 里出现了不在 `api-keys` 中的 key，运行时忽略，但配置保留。这样用户可以提前准备规则。

## 运行时怎么限制

在 `sdk/cliproxy/auth` 增加一个访问范围 helper：

- 从请求上下文里取出当前请求使用的 API key，也就是已有的 `userApiKey`。
- 从 `Manager.runtimeConfig` 读取最新配置。
- 找到这个 API key 对应的访问规则。
- 按 provider 和认证文件 ID/文件名过滤候选认证文件。

过滤位置：

- legacy 选择路径：在 `pickNextLegacy` 里过滤候选 `candidates`。
- scheduler 快路径：给 scheduler 的选择 predicate 加同样的过滤。如果改动太大，就在有访问范围规则时回退到 legacy 路径。
- mixed-provider、Home runtime、websocket 相关选择路径也要走同一个 helper，保证行为一致。

过滤要发生在普通 provider/model 匹配之后、真正选中认证文件之前。这样不会影响 OAuth 登录、认证文件扫描、模型注册、冷却状态和已有模型匹配逻辑。

如果某个 key 因为访问范围过滤后没有任何可用认证文件，返回 `auth_not_found`，错误信息说明是 access scope 导致，但不能暴露原始 API key。

你刚问的例子也按这个规则处理：如果密钥 1 只授权了认证文件 1，认证文件 1 和认证文件 2 都有 `gpt5.5`，但认证文件 1 的 `gpt5.5` 额度没了，那么密钥 1 不能去用认证文件 2 的 `gpt5.5` 额度。它只能在密钥 1 被授权的范围内 fallback，比如用认证文件 1 仍然可用的其他模型，或者直接返回无可用认证。

## 管理 API

新增管理接口：

- `GET /v0/management/api-key-access`
- `PUT /v0/management/api-key-access`
- `PATCH /v0/management/api-key-access`
- `DELETE /v0/management/api-key-access?key=<url-encoded-key>`

返回内容包括：

- 当前配置规则。
- 用于页面展示的 API key 标签，展示时要脱敏。
- 当前可选的认证文件目标，包括 provider、ID、文件名、标签、email/project 元数据和 `auth-index`。

校验规则：

- provider 名字 trim 后转小写。
- auth file ID trim 并去重。
- JSON body 格式错误时返回 400。
- 不因为 provider 或 auth file 当前不存在就拒绝保存，因为用户可能想先配置规则，之后再添加认证文件。页面可以显示“当前不存在”。

配置 diff 日志要显示 `api-key-access` 有变化，但不能打印原始 API key。

## 管理页面

后端 API 做完后，需要 fork 并 clone `router-for-me/Cli-Proxy-API-Management-Center`。

页面行为：

- 在 API Keys 页面给每个 key 增加访问范围编辑器。
- 提供 `All` 模式和受限模式。
- 受限模式可以多选 provider，也可以多选认证文件。
- 认证文件选项显示安全标签，例如 provider、auth index、文件名、email/project、状态。
- 保存时调用新的管理 API。

如果管理中心页面不能和后端在同一个 PR 里完成，后端先上线也可以。用户仍然可以通过 YAML 或管理 API 使用这个功能，但 UI 需要等待对应的管理中心 release。

## 测试范围

后端需要覆盖：

- `api-key-access` 的配置解析、规范化和 YAML round-trip。
- 原有 `api-keys` 认证仍然正常。
- 未配置规则的 key 默认仍然可以访问全部。
- `access: all` 能访问全部 provider 和认证文件。
- 只配置 provider 时，只按 provider 限制。
- 只配置 auth file 时，只按认证文件限制。
- 同时配置 provider 和 auth file 时，使用交集语义。
- 受限规则为空时，返回 `auth_not_found`。
- 管理 API 的增删改查能保存配置，并且响应和日志 diff 不泄露原始 key。
- scheduler 路径和 legacy 路径限制结果一致。
- 额度耗尽和 fallback 不会逃逸当前 key 的授权范围。只授权认证文件 1 的 key，不能因为认证文件 2 有额度就使用认证文件 2。

至少运行：

```bash
go test ./internal/config ./internal/access/... ./internal/api/handlers/management ./sdk/cliproxy/auth
go build -o test-output ./cmd/server && rm test-output
```

如果实际改动范围变大，再跑 `go test ./...`。

## 当前没有未定问题

第一版按“用原始 API key 作为规则 key，默认向后兼容、未配置规则等于全量访问”的方向实现。
