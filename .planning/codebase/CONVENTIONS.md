# Repository Conventions

## Scope

This repo is mixed-language, but the center of gravity is the Rust workspace in `codex-rs/`. The strongest conventions are either:

- written down in `AGENTS.md`
- enforced by workspace lint config in `codex-rs/Cargo.toml` and `codex-rs/clippy.toml`
- encoded into protocol types in `codex-rs/app-server-protocol/src/protocol/common.rs` and `codex-rs/app-server-protocol/src/protocol/v2.rs`
- repeated across large implementation files such as `codex-rs/app-server/src/codex_message_processor.rs`, `codex-rs/core/tests/common/test_codex.rs`, and `codex-rs/tui/src/bottom_pane/approval_overlay.rs`

## Naming And Layout

- Rust crate names are prefixed with `codex-`. The workspace manifest in `codex-rs/Cargo.toml` maps directory names like `core`, `tui`, and `app-server` to crates such as `codex-core`, `codex-tui`, and `codex-app-server`.
- Integration tests are usually aggregated through `tests/all.rs` plus `tests/suite/mod.rs`, for example `codex-rs/tui/tests/all.rs`, `codex-rs/app-server/tests/all.rs`, and `codex-rs/core/tests/all.rs`.
- Test support code lives in per-crate shared helpers under `tests/common`, for example `codex-rs/core/tests/common/` and `codex-rs/app-server/tests/common/`.
- TypeScript packages are isolated subprojects under `sdk/typescript/` and `shell-tool-mcp/`, each with its own `package.json`, Jest config, and `src/` plus `tests/` layout.

## Rust Style Rules

- The Rust workspace uses edition 2024 and centralizes lint policy in `codex-rs/Cargo.toml`.
- Clippy is intentionally strict. `codex-rs/Cargo.toml` denies `unwrap_used`, `expect_used`, `redundant_closure_for_method_calls`, `uninlined_format_args`, and many "manual_*" lints.
- `AGENTS.md` adds repo-specific expectations on top of Clippy: collapse nested `if` statements, inline `format!` arguments (`format!("{name}")` rather than positional arguments), prefer method references over trivial closures, avoid wildcard match arms when an exhaustive match is practical, and avoid one-off helper methods.
- `codex-rs/rustfmt.toml` keeps formatting mostly standard, with `imports_granularity = "Item"` so imports are typically one item per line instead of grouped trees.
- When test code really needs `expect` or `unwrap`, it is usually allowed explicitly at the module level. Examples: `codex-rs/core/tests/common/lib.rs` uses `#![expect(clippy::expect_used)]`, and `codex-rs/core/tests/suite/tools.rs` opts into `#![allow(clippy::unwrap_used, clippy::expect_used)]`.

## Error Handling Norms

- Library-facing error types usually use `thiserror`. Representative files: `codex-rs/codex-client/src/error.rs` and `codex-rs/cloud-tasks-client/src/api.rs`.
- Test and orchestration code frequently returns `anyhow::Result<()>` and uses `anyhow::Context` / `anyhow::ensure!` to keep failures readable. Examples: `codex-rs/core/tests/common/lib.rs`, `codex-rs/tui/tests/suite/model_availability_nux.rs`, and `codex-rs/app-server/tests/suite/v2/turn_start.rs`.
- IO-heavy code favors explicit stringified context instead of opaque errors. Example patterns show up throughout `codex-rs/login/src/server.rs` and `codex-rs/cloud-tasks-client/src/http.rs`.
- Panics are mostly reserved for invariants, fixture corruption, or helper misuse, not normal runtime control flow. The helper types in `codex-rs/core/tests/common/responses.rs` panic when a test asks for a missing request or malformed body because that is a test bug.

## API Wire Conventions

- `codex-rs/app-server-protocol/src/protocol/common.rs` defines the RPC surface with macros rather than hand-writing repetitive request enums. New client methods are added through the `client_request_definitions!` macro.
- Active app-server API work is v2-first. The v2 types in `codex-rs/app-server-protocol/src/protocol/v2.rs` consistently derive `Serialize`, `Deserialize`, `JsonSchema`, and `TS`.
- Wire naming is explicit and synchronized between Rust and generated TypeScript. Common patterns:
  - `#[serde(rename_all = "camelCase")]` for v2 payloads
  - `#[ts(export_to = "v2/")]` for generated TypeScript placement
  - matching `#[serde(rename = "...")]` and `#[ts(rename = "...")]` when a field deviates
- Request/response type names are systematic: `*Params`, `*Response`, and `*Notification`. This is visible throughout `codex-rs/app-server-protocol/src/protocol/v2.rs`.
- RPC method names use `<resource>/<method>` with singular resources, e.g. the methods registered in `codex-rs/app-server-protocol/src/protocol/common.rs` such as `"thread/start"`, `"thread/read"`, `"plugin/install"`, and `"app/list"`.
- Config RPCs are the main naming exception: `AGENTS.md` explicitly calls out snake_case payloads there to mirror `config.toml`.

## TUI-Specific Style

- `codex-rs/tui/styles.md` defines a tight ANSI palette: default text, `cyan` for tips and selection, `green` for success, `red` for errors, and `magenta` for Codex. Blue, yellow, black, white, and custom RGB colors are generally avoided.
- `codex-rs/clippy.toml` backs this up by disallowing `Color::Rgb`, `Color::Indexed`, `.white()`, `.black()`, and `.yellow()` unless a file opts out.
- TUI code prefers `ratatui::style::Stylize` helpers over manual style construction. `AGENTS.md` calls out forms like `"text".dim()` and `"text".bold()`, and `codex-rs/tui/src/bottom_pane/approval_overlay.rs` shows this style in practice.
- Plain-string wrapping should go through `textwrap`, and ratatui line wrapping should go through `codex-rs/tui/src/wrapping.rs`. Prefixing wrapped lines is centralized in `codex-rs/tui/src/render/line_utils.rs`.
- TUI implementation style leans toward small local render helpers and owned `Line<'static>` transformations instead of large view-model layers. Files like `codex-rs/tui/src/bottom_pane/approval_overlay.rs`, `codex-rs/tui/src/history_cell.rs`, and `codex-rs/tui/src/bottom_pane/list_selection_view.rs` are representative.

## TypeScript Style

- TypeScript packages are ESM packages (`"type": "module"` in `sdk/typescript/package.json`), and Node built-ins are imported with the `node:` prefix, e.g. `sdk/typescript/tests/responsesProxy.ts` and `shell-tool-mcp/src/index.ts`.
- ESLint for the SDK is minimal but opinionated: `sdk/typescript/eslint.config.js` enforces `node-import/prefer-node-protocol` and ignores unused variables only when they are underscore-prefixed.
- Source files favor simple classes and typed object literals over deep framework abstractions. Representative files: `sdk/typescript/src/codex.ts`, `sdk/typescript/src/thread.ts`, and `shell-tool-mcp/src/bashSelection.ts`.
- The TS packages keep side effects small and explicit. `shell-tool-mcp/src/index.ts` is a good example: resolve inputs, call a small pure helper layer, print one result, and exit.

## Repeated Implementation Patterns

- Builder-style setup objects are common in test harnesses and configuration-heavy flows. `codex-rs/core/tests/common/test_codex.rs` uses `TestCodexBuilder` to layer auth, model, hooks, and home directory setup.
- Shared helper modules are preferred over copy-pasted protocol or fixture logic. Examples:
  - `codex-rs/core/tests/common/responses.rs`
  - `codex-rs/app-server/tests/common/responses.rs`
  - `sdk/typescript/tests/responsesProxy.ts`
- Cross-platform branches are written inline with `cfg!` / `#[cfg(...)]` rather than hidden behind deep adapter stacks. See `codex-rs/app-server/tests/suite/v2/turn_start.rs`, `codex-rs/core/tests/common/lib.rs`, and `codex-rs/tui/tests/suite/model_availability_nux.rs`.
- Large or async test ergonomics are sometimes abstracted with macros. `codex-rs/test-macros/src/lib.rs` provides `#[large_stack_test]`, which swaps in a large-stack thread and a Tokio runtime for async bodies.
- Comments are usually sparse and tactical. The long explanatory comments in `codex-rs/tui/src/wrapping.rs` are typical of when the repo adds comments: only when behavior is subtle, heuristic, or easy to misuse.

## Change-Management Conventions

- User-visible behavior changes are expected to update docs. `docs/contributing.md` explicitly calls this out for README, help text, and examples.
- Protocol shape changes usually imply generated artifact refreshes. `AGENTS.md` calls out `just write-app-server-schema` for app-server protocol changes and `just write-config-schema` for `ConfigToml` changes.
- Dependency changes in `codex-rs/` carry Bazel lockfile maintenance expectations through `AGENTS.md`: update `MODULE.bazel.lock` and run the lock checks.

## Practical Guidance For Future Edits

- Start by matching the local style of the target crate or file rather than inventing a new abstraction level.
- In Rust, prefer explicit enums, derives, and typed helpers over generic utility layers unless the pattern already exists elsewhere.
- In app-server protocol code, copy an existing v2 request/response pair and keep serde and TS annotations aligned.
- In TUI code, use ANSI-safe Stylize helpers, wrapping helpers, and snapshot-backed render tests rather than ad hoc formatting logic.
- In tests, it is acceptable to use stronger assertions and panic-oriented helpers, but production code is expected to surface structured errors and preserve context.
