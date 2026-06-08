# API Key Access Scope Design

## Goal

Add per-client API key authorization scopes so one CPA key can access every auth credential while another CPA key can be limited to specific auth files and/or AI providers. The management API and management page must expose this feature so operators can configure it without editing YAML by hand.

## Current Context

Client API keys are configured in `api-keys` on `SDKConfig`. `internal/access/config_access` registers a request auth provider that validates those keys and `internal/api/server.go` stores the authenticated key in `gin.Context` as `userApiKey`.

Runtime auth selection happens in `sdk/cliproxy/auth`. `Manager.SetConfig` already stores the latest config snapshot, and selection paths receive request context, so access scopes can be enforced just before selecting an auth candidate. Auth files and config-defined provider keys are represented as `coreauth.Auth` entries with provider, ID, filename, metadata, and stable auth index support for management responses.

The management control panel HTML is downloaded from `router-for-me/Cli-Proxy-API-Management-Center` via `internal/managementasset`. This repo owns the backend management API and asset updater; the UI source lives in the management center repo.

## Approaches Considered

1. Add scopes directly into `api-keys` by changing it from `[]string` to a list of objects. This is compact for new installs, but it risks breaking existing config parsing and every management client that expects a string list.

2. Add a parallel `api-key-access` map keyed by existing API key value. This preserves the current `api-keys` format, keeps old configs working, and lets operators add scopes incrementally. This is the recommended approach.

3. Add named API key IDs and reference those IDs from rules. This avoids duplicating raw key values in YAML, but it is a larger migration because the current config does not have API key identity objects.

Use approach 2 for the first implementation.

## Configuration

Add a top-level config field:

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

Rules are keyed by the exact client API key value to preserve the current `api-keys` model and avoid introducing key IDs before the project needs them.

Each rule has:

- `access`: optional string. `all` means unrestricted. Empty means restricted when any allow list is present.
- `providers`: optional list of provider names such as `claude`, `gemini`, `codex`, `xai`, `kimi`, `antigravity`, `openai-compatibility`, or plugin provider names.
- `auth-files`: optional list of auth IDs or filenames. For file-backed auths this is the auth filename. For generated config-key credentials the value can be the auth ID returned by management APIs. Management UI can display `auth-index`, but persisted scope rules should store auth IDs or filenames rather than display indexes.

Compatibility behavior:

- If `api-key-access` is absent, all configured API keys keep existing unrestricted behavior.
- If a client key has no rule, it is unrestricted by default.
- If a rule has `access: all`, it is unrestricted even if allow lists are present.
- If both `providers` and `auth-files` are set, an auth must match both.
- If a restricted rule has empty allow lists, no auth is available for that key.
- Rules for keys not present in `api-keys` are ignored at runtime but retained in config, so operators can stage changes.

## Runtime Enforcement

Add an access-scope helper in `sdk/cliproxy/auth` that:

- Extracts the authenticated client API key from the request context via the existing `ctx.Value("gin")` path.
- Reads the latest config snapshot from `Manager.runtimeConfig`.
- Resolves the rule for the client key.
- Filters auth candidates by provider and auth ID/filename.

Enforcement points:

- Legacy selection: filter `candidates` in `pickNextLegacy` before availability and selector logic.
- Scheduler fast path: add the same predicate to scheduler selection, either by extending scheduler predicate checks or by falling back to legacy when a scoped key is present. Prefer extending scheduler predicates if the change stays small.
- Mixed-provider selection and Home/runtime auth paths must use the same helper so behavior is consistent across OpenAI, Claude, Gemini, Codex, websocket, and Home execution flows.

The filter must happen after normal provider/model eligibility checks and before selecting a credential. This preserves OAuth login, auth file scanning, model registration, cooldown bookkeeping, and existing route/model validation.

When scope filtering removes every candidate, return an `auth_not_found` error with a message that identifies access scope as the reason without logging or returning the raw API key.

## Management API

Add management endpoints:

- `GET /v0/management/api-key-access`
- `PUT /v0/management/api-key-access`
- `PATCH /v0/management/api-key-access`
- `DELETE /v0/management/api-key-access?key=<url-encoded-key>`

Response shape should include:

- Raw config rules for persistence in authenticated management clients.
- Redacted/display API key labels for UI lists.
- Available auth targets from `authManager.List()`, including provider, ID, filename, label, email/project metadata when present, and `auth-index`.

Validation:

- Normalize provider names by trimming whitespace and lowercasing.
- Normalize auth file IDs by trimming whitespace and deduplicating.
- Reject malformed JSON bodies.
- Do not reject unknown providers or auth IDs, because auth files can be added later; the UI can mark them as currently missing.

Config diffs should summarize `api-key-access` changes without printing client API keys.

## Management Page

After backend API support is approved, fork and clone `router-for-me/Cli-Proxy-API-Management-Center`.

UI behavior:

- In the API Keys section, add an access-scope editor per key.
- Offer an `All` mode and a restricted mode.
- Restricted mode shows provider multi-select and auth file multi-select.
- Auth file options display safe labels such as provider, auth index, filename, email/project metadata, and status.
- Save through the new management API.

If the management center cannot be modified in the same PR, ship the backend API first and document that UI support requires a matching management center release. The backend must remain useful via YAML and API clients.

## Tests

Backend unit and integration coverage:

- Config parsing, normalization, and YAML round-trip for `api-key-access`.
- Request auth still accepts existing `api-keys`.
- Unconfigured keys remain unrestricted.
- `access: all` allows all providers/auth files.
- Provider-only rules filter candidates.
- Auth-file-only rules filter candidates.
- Combined provider and auth-file rules use intersection semantics.
- Restricted empty rules produce `auth_not_found`.
- Management API CRUD persists config and redacts keys in responses/loggable diff output.
- Scheduler and legacy selection paths apply the same restrictions.

Run at minimum:

```bash
go test ./internal/config ./internal/access/... ./internal/api/handlers/management ./sdk/cliproxy/auth
go build -o test-output ./cmd/server && rm test-output
```

Broaden to `go test ./...` if the touched implementation area grows beyond the planned packages.

## Open Questions

No open requirement questions remain for the first implementation. The first version will key rules by exact API key value and allow unrestricted behavior by default for backward compatibility.
