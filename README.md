# codextra

`codextra` is a thin wrapper around `codex`.

It starts a local proxy, launches the real `codex` binary with the proxy URL in
the environment, and otherwise passes arguments through unchanged. The proxy is
where account rotation will live.

This repo intentionally uses only the Go standard library.

## Usage

```sh
codextra [codex args...]
```

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
