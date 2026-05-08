# codextra

`codextra` is a thin wrapper around `codex`.

It starts a local proxy, launches the real `codex` binary with the proxy URL in
Codex's `chatgpt_base_url` config override while preserving Codex's
`/backend-api` base path, and otherwise passes arguments through unchanged. The
proxy owns account selection and rotation.

This repo intentionally uses only the Go standard library.

## Why Proxy

This started as a feature added directly to a Codex fork. That worked, but Codex
moves quickly and Rust compile times made the fork expensive to keep current.

`codextra` keeps the account-rotation behavior outside the Codex codebase. The
goal is to preserve a small maintenance surface: launch Codex normally, point its
ChatGPT backend traffic at a local proxy, and keep rotation policy in this repo
instead of repeatedly merging a large upstream project.

## Usage

Install from a release archive for your platform on the
[releases page](https://github.com/gabewillen/codextra/releases/latest).

Or build from source:

```sh
go install github.com/gabewillen/codextra/cmd/codextra@latest
```

```sh
codextra [codex args...]
```

`codextra` intercepts account-management commands and passes everything else
through to `codex`.

```sh
codextra login personal
codextra login work --device-auth
codextra --account work
```

`login <alias>` runs the normal `codex login`, imports the resulting active
Codex auth from `$CODEX_HOME/auth.json` or `~/.codex/auth.json`, and stores it
under the alias.

Only `login` and the internal `serve-proxy` command are reserved by `codextra`.
All other arguments are passed to `codex` unchanged after injecting the
`chatgpt_base_url` override.

Use `--account <alias>` or `--account=<alias>` to switch the active codextra
account before launching Codex. The flag is consumed by `codextra` and is not
passed through to `codex`. Selecting an account only updates codextra's account
registry; proxied requests get their `Authorization` and `ChatGPT-Account-ID`
headers from the active codextra account instead of relying on Codex's
`auth.json`. For Codex UI/status metadata, `codextra` launches the child process
with a temporary `CODEX_HOME` that mirrors the normal Codex home but contains an
isolated `auth.json` for the selected alias. The real Codex auth file is not
modified.

After rotation, Codex's `/status` screen can show mixed account information:
the `Account` field comes from Codex's startup auth snapshot, while usage limits
and model requests come from the currently selected codextra proxy account.

By default, `codextra` looks for `codex` on `PATH`. Override it with:

```sh
CODEXTRA_CODEX_BIN=/path/to/codex codextra
```

The proxy listens on a random localhost port and forwards to:

```sh
CODEXTRA_UPSTREAM=https://chatgpt.com
```

Proxy diagnostics are written to `~/.codextra/proxy.log` as structured `slog`
text records. The file is capped at 1 MiB by default and truncates before a
write that would exceed the cap.

```sh
CODEXTRA_PROXY_LOG_MAX_BYTES=1048576
```

The proxy stays alive while at least one `codextra` process has an attached
client stream open. After the last client disconnects, the proxy exits after a
short grace period.

```sh
CODEXTRA_PROXY_IDLE_GRACE_SECONDS=10
```

Account metadata is stored at:

```sh
~/.codextra/accounts.json
```

The proxy and account store are scaffolded for rotation. `login <alias>` imports
the account token set from an ordinary Codex login.

## Releases

Tagged releases are built by GoReleaser for macOS, Linux, and Windows on amd64
and arm64. Push a `v*` tag to publish a GitHub release with archives and
checksums.
