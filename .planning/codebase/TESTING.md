# Testing Patterns

## Scope

Testing in this repo is strongest in the Rust workspace under `codex-rs/`, with additional Jest coverage in `sdk/typescript/` and `shell-tool-mcp/`. The practical patterns are easiest to understand by looking at:

- `codex-rs/core/tests/common/`
- `codex-rs/app-server/tests/common/`
- `codex-rs/tui/` snapshot-heavy unit tests
- `sdk/typescript/tests/`
- `shell-tool-mcp/tests/`

## Primary Frameworks

- Rust tests use the built-in test harness plus `tokio::test` for async work. Multi-thread async tests are common in integration-heavy crates, for example `codex-rs/core/tests/suite/tools.rs` and `codex-rs/app-server/tests/suite/v2/turn_start.rs`.
- Rust assertions commonly use `pretty_assertions::assert_eq`, especially where structured diffs matter. Representative files: `codex-rs/app-server/tests/suite/v2/turn_start.rs`, `codex-rs/cli/tests/features.rs`, and many unit test modules under `codex-rs/app-server/src/`.
- UI and transcript rendering use `insta` snapshots. This is especially dense in `codex-rs/tui/src/`, but it also appears in `codex-rs/core/src/guardian_tests.rs` and core integration suites such as `codex-rs/core/tests/suite/model_visible_layout.rs`.
- HTTP mocking in Rust is mostly done with `wiremock`. Shared wrappers live in `codex-rs/core/tests/common/responses.rs`, and raw `MockServer::start()` usage appears in suites like `codex-rs/login/tests/suite/device_code_login.rs`.
- TypeScript packages use Jest. `sdk/typescript/jest.config.cjs` uses `ts-jest` with ESM support, while `shell-tool-mcp/jest.config.cjs` uses a simpler `ts-jest` setup.

## Test Organization

- Rust crates usually expose one top-level integration entrypoint such as `codex-rs/core/tests/all.rs`, which re-exports modules from `tests/suite/`.
- Shared harness code is intentionally factored out of the individual test files:
  - `codex-rs/core/tests/common/lib.rs`
  - `codex-rs/core/tests/common/test_codex.rs`
  - `codex-rs/app-server/tests/common/lib.rs`
  - `codex-rs/app-server/tests/common/responses.rs`
- App-server tests are grouped by RPC area under `codex-rs/app-server/tests/suite/v2/`, which makes it easy to find behavior by method name.
- TUI tests are split between classic integration tests under `codex-rs/tui/tests/suite/` and inline module tests inside `codex-rs/tui/src/` for rendering-focused components.
- TypeScript tests live under package-local `tests/` directories and mostly mirror product entrypoints: `sdk/typescript/tests/run.test.ts`, `sdk/typescript/tests/runStreamed.test.ts`, and `shell-tool-mcp/tests/bashSelection.test.ts`.

## Shared Rust Harness Patterns

- `codex-rs/core/tests/common/test_codex.rs` is the core harness for end-to-end thread tests. `TestCodexBuilder` lets tests override config, auth, model, and filesystem setup without rebuilding the world by hand.
- `codex-rs/core/tests/common/lib.rs` centralizes deterministic test mode setup, path helpers, regex assertions, fixture loading, and config creation.
- `codex-rs/test-macros/src/lib.rs` provides `#[large_stack_test]` for unusually deep or stack-heavy tests; the macro creates a dedicated large-stack thread and, for async bodies, an internal Tokio runtime.
- Temporary directories are the default isolation boundary. `TempDir::new()?` shows up throughout `codex-rs/core/tests/`, `codex-rs/app-server/tests/`, `codex-rs/cli/tests/`, and `codex-rs/tui/tests/`.

## Mocking And Fake Servers

- The dominant integration pattern is "fake Responses API + assert outbound request body". `codex-rs/core/tests/common/responses.rs` captures requests and exposes typed helpers like `single_request()`, `body_json()`, `function_call_output()`, and `message_input_texts()`.
- `ResponseMock` is used to assert the exact tool-call outputs sent back to the model, not just final user-visible events. This is visible in `codex-rs/core/tests/suite/tools.rs`, `codex-rs/core/tests/suite/js_repl.rs`, and many client tests in `codex-rs/core/tests/suite/client.rs`.
- App-server tests reuse the same SSE-building idiom through wrapper helpers in `codex-rs/app-server/tests/common/responses.rs`, which construct realistic response streams for shell, exec, apply_patch, and request-user-input flows.
- Websocket-specific harnesses also exist. `codex-rs/core/tests/common/responses.rs` contains websocket server helpers, and app-server websocket coverage appears in files like `codex-rs/app-server/tests/suite/v2/connection_handling_websocket.rs`.
- TypeScript uses lightweight in-process test doubles rather than a heavy mocking framework:
  - `sdk/typescript/tests/responsesProxy.ts` spins up a real `node:http` server that records requests and returns SSE responses.
  - `sdk/typescript/tests/codexExecSpy.ts` monkey-patches `child_process.spawn` to assert CLI invocation arguments and environment variables.

## Fixtures And Resources

- SSE fixtures are stored as checked-in files when realism matters. Examples: `codex-rs/core/tests/cli_responses_fixture.sse` and `codex-rs/exec/tests/fixtures/cli_responses_fixture.sse`.
- Scenario-based parser tests keep input, patch, and expected output as filesystem fixtures. The clearest example is `codex-rs/apply-patch/tests/fixtures/scenarios/`.
- TUI integration tests use recorded conversation fixtures under `codex-rs/tui/tests/fixtures/`, for example `codex-rs/tui/tests/fixtures/oss-story.jsonl`.
- Bazel-aware fixture resolution is a deliberate convention. `AGENTS.md` points contributors at `codex_utils_cargo_bin::cargo_bin` and `codex_utils_cargo_bin::find_resource!`, and `codex-rs/utils/cargo-bin/README.md` explains why. A concrete example is `codex-rs/tui/tests/suite/model_availability_nux.rs`.

## Snapshot Testing

- Snapshot testing is a first-class requirement for TUI-visible output. `AGENTS.md` explicitly says any intentional user-visible UI change should add or update `insta` coverage.
- Most TUI snapshots are inline-unit-test driven, not only integration-test driven. Common patterns include:
  - rendering a widget or buffer
  - sanitizing environment-specific paths
  - `assert_snapshot!(terminal.backend())` or `assert_snapshot!(format!("{buf:?}"))`
- Representative snapshot files and sources:
  - `codex-rs/tui/src/bottom_pane/approval_overlay.rs`
  - `codex-rs/tui/src/history_cell.rs`
  - `codex-rs/tui/src/status/tests.rs`
  - `codex-rs/tui/src/bottom_pane/mod.rs`
  - `codex-rs/tui/src/snapshots/`
- Core also uses snapshots when serialized shapes are the main contract. See `codex-rs/core/tests/suite/model_visible_layout.rs` and the matching snapshot files in `codex-rs/core/tests/suite/snapshots/`.

## Assertion Style

- Tests prefer asserting whole values or stable structured fragments instead of manually inspecting many individual fields. Examples include full `assert_eq!` comparisons in `codex-rs/app-server/tests/suite/v2/turn_start.rs` and `codex-rs/cli/tests/features.rs`.
- Where outputs are noisy or partially dynamic, the repo typically asserts a stable subset rather than brittle full-string equality:
  - request helpers pick out specific fields from JSON bodies in `codex-rs/core/tests/common/responses.rs`
  - regex-based matching is centralized in `codex-rs/core/tests/common/lib.rs`
  - TypeScript tests use `expect.any(String)` and partial JSON inspection in `sdk/typescript/tests/run.test.ts`
- Platform-sensitive tests normalize paths or skip platform-specific branches instead of maintaining separate snapshots for every environment. `codex-rs/tui/src/status/tests.rs` replaces Windows path separators before snapshotting; `codex-rs/tui/tests/suite/model_availability_nux.rs` exits early on Windows because of PTY limits.

## Async And Process Testing Norms

- Long-running async tests usually wrap waits in `tokio::time::timeout` to fail quickly and clearly. This pattern is pervasive in `codex-rs/app-server/tests/suite/v2/turn_start.rs`, `codex-rs/tui/tests/suite/model_availability_nux.rs`, and many websocket tests.
- Spawned-binary tests should resolve first-party binaries through `codex_utils_cargo_bin::cargo_bin("...")`, not raw relative paths. Good examples: `codex-rs/cli/tests/features.rs`, `codex-rs/tui/tests/suite/no_panic_on_startup.rs`, and `codex-rs/app-server/tests/suite/v2/connection_handling_websocket.rs`.
- PTY-driven tests exist where terminal behavior matters. `codex-rs/tui/tests/suite/model_availability_nux.rs` and `codex-rs/tui/tests/suite/no_panic_on_startup.rs` spawn the real CLI and interact with it at the terminal level.
- Some suites intentionally run only under feature flags or certain OSes. Examples: `codex-rs/tui/tests/suite/vt100_history.rs` is gated behind `feature = "vt100-tests"`, and many tests branch on `cfg!(windows)` or `#[cfg(...)]`.

## Hermeticity And Environment Handling

- Tests try to keep filesystem state under a temporary `CODEX_HOME` rather than touching a developer's real config. `codex-rs/core/tests/common/lib.rs` documents this explicitly.
- Some end-to-end tests intentionally skip when the sandbox cannot provide required network behavior. The `skip_if_no_network!` macro appears throughout `codex-rs/core/tests/suite/`, `codex-rs/login/tests/suite/`, and `codex-rs/mcp-server/tests/suite/`.
- Determinism is improved centrally rather than per test. `codex-rs/core/tests/common/lib.rs` uses `ctor` hooks to enable deterministic process IDs and configure `INSTA_WORKSPACE_ROOT` early.

## Observable Coverage Tendencies

- `codex-rs/core/` has the broadest behavioral coverage. The `tests/suite/` directory covers tool execution, approvals, streaming, collaboration, resume/fork flows, web search, image flows, protocol transitions, and compaction behavior.
- `codex-rs/app-server/` is strongly covered around RPC behavior and transport-level notifications. The `tests/suite/v2/` tree mirrors the public API surface closely.
- `codex-rs/tui/` is heavily protected against visual regressions through snapshots and a smaller number of PTY-level integration tests.
- CLI crates such as `codex-rs/cli/` lean more on process-level command assertions than deep internal mocking.
- TypeScript package coverage is narrower and contract-focused: the SDK verifies thread execution, argument forwarding, and request shaping; `shell-tool-mcp` mostly verifies pure selection logic rather than end-to-end shell interception.

## Practical Guidance For Future Tests

- If the change affects model I/O, start with the helpers in `codex-rs/core/tests/common/responses.rs` or `codex-rs/app-server/tests/common/responses.rs` instead of rolling your own HTTP mock.
- If the change affects TUI rendering, add or update an `insta` snapshot in the nearest source module and sanitize machine-specific values before snapshotting.
- If the test needs a repo-owned binary or fixture, use `codex_utils_cargo_bin::cargo_bin` and `codex_utils_cargo_bin::find_resource!` so the test still works under Bazel runfiles.
- Prefer per-test temp homes and explicit timeouts.
- When assertions are against JSON or recorded requests, assert the structured field that proves the behavior rather than comparing the whole blob unless the blob itself is the contract.
