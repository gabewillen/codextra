# codextra

`codextra` is a thin wrapper around `codex`.

It starts a local proxy, launches the real `codex` binary with the proxy URL in
Codex's `chatgpt_base_url` config override, and otherwise passes arguments
through unchanged. The proxy is where account rotation will live.

This repo intentionally uses only the Go standard library.

## Why Proxy

This started as a feature added directly to a Codex fork. That worked, but Codex
moves quickly and Rust compile times made the fork expensive to keep current.

`codextra` keeps the account-rotation behavior outside the Codex codebase. The
goal is to preserve a small maintenance surface: launch Codex normally, point its
ChatGPT backend traffic at a local proxy, and keep rotation policy in this repo
instead of repeatedly merging a large upstream project.

## Usage

```sh
codextra [codex args...]
```

`codextra` intercepts account-management commands and passes everything else
through to `codex`.

```sh
codextra login personal
codextra login work --device-auth
```

`login <alias>` runs the normal `codex login`, imports the resulting active
Codex auth from `$CODEX_HOME/auth.json` or `~/.codex/auth.json`, and stores it
under the alias.

Only `login` and the internal `serve-proxy` command are reserved by `codextra`.
All other arguments are passed to `codex` unchanged after injecting the
`chatgpt_base_url` override.

By default, `codextra` looks for `codex` on `PATH`. Override it with:

```sh
CODEXTRA_CODEX_BIN=/path/to/codex codextra
```

The proxy listens on a random localhost port and forwards to:

```sh
CODEXTRA_UPSTREAM=https://chatgpt.com
```

Account metadata is stored at:

```sh
~/.codextra/accounts.json
```

The account store is scaffolded for rotation, but login/token acquisition is not
implemented yet.
