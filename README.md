# codextra

`codextra` is a thin wrapper around `codex`.

It starts a local proxy, launches the real `codex` binary with the proxy URL in
Codex's `chatgpt_base_url` config override while preserving Codex's
`/backend-api` base path, and otherwise passes arguments through unchanged. The
proxy owns account selection and rotation.

`codextra` is intended for switching between paid personal ChatGPT
subscriptions that you personally control. **It is not intended to encourage or
support cycling through free accounts, shared accounts, trial accounts, or other
accounts used to evade usage limits or service terms.**

This repo intentionally uses only the Go standard library.

## Why Proxy

This started as a feature added directly to a Codex fork. That worked, but Codex
moves quickly and Rust compile times made the fork expensive to keep current.

`codextra` keeps the account-rotation behavior outside the Codex codebase. The
goal is to preserve a small maintenance surface: launch Codex normally, point its
ChatGPT backend traffic at a local proxy, and keep rotation policy in this repo
instead of repeatedly merging a large upstream project.

## Usage

Install the latest release on macOS or Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/gabewillen/codextra/refs/heads/main/install.sh | sh
```

Install the latest release from PowerShell:

```powershell
irm https://raw.githubusercontent.com/gabewillen/codextra/refs/heads/main/install.ps1 | iex
```

The installer requires `codex` to already be installed and available on `PATH`.
If your Codex binary lives somewhere else, set `CODEXTRA_CODEX_BIN`:

```sh
curl -fsSL https://raw.githubusercontent.com/gabewillen/codextra/refs/heads/main/install.sh | CODEXTRA_CODEX_BIN=/path/to/codex sh
```

```powershell
$env:CODEXTRA_CODEX_BIN = "C:\path\to\codex.exe"; irm https://raw.githubusercontent.com/gabewillen/codextra/refs/heads/main/install.ps1 | iex
```

The installer puts `codextra` in a writable directory already on `PATH` when it
can. Override the target directory with:

```sh
curl -fsSL https://raw.githubusercontent.com/gabewillen/codextra/refs/heads/main/install.sh | INSTALL_DIR=/usr/local/bin sh
```

```powershell
$env:INSTALL_DIR = "$HOME\bin"; irm https://raw.githubusercontent.com/gabewillen/codextra/refs/heads/main/install.ps1 | iex
```

Or build from source:

```sh
go install github.com/gabewillen/codextra/cmd/codextra@latest
```

If you want tray support on macOS:

```sh
CGO_ENABLED=0 go install github.com/gabewillen/codextra/cmd/codextra@latest
```

```sh
codextra [codex args...]
```

`codextra` intercepts account-management commands and passes everything else
through to `codex`.

```sh
codextra login personal-plus
codextra login personal-pro --device-auth
codextra login --tag
codextra --account personal-pro
codextra --desktop .
```

`login <alias>` runs the normal `codex login`, imports the resulting active
Codex auth from `$CODEX_HOME/auth.json` or `~/.codex/auth.json`, and stores it
under the alias. `login --tag` skips the login step and stores the current
Codex auth, using the auth email as the alias when available and otherwise
using the account ID. Use `login --tag <alias>` to choose the alias yourself.
Use aliases for your own paid personal subscriptions; do not use `codextra` to
manage pools of free or throwaway accounts.

Only `login`, `install-app`, and the internal `serve-proxy` command are reserved
by `codextra`.
All other arguments are passed to `codex` unchanged after injecting the
`chatgpt_base_url` override.

Use `--account <alias>` or `--account=<alias>` to switch the active codextra
account before launching Codex. The flag is consumed by `codextra` and is not
passed through to `codex`. Selecting an account only updates codextra's account
registry; proxied requests get their `Authorization` and `ChatGPT-Account-ID`
headers from the active codextra account instead of relying on Codex's
`auth.json`. `codextra` does not replace `CODEX_HOME`, so Codex session
history, resume state, config, and other local files stay in the normal Codex
home. Because Codex still reads the normal `auth.json` locally, UI and status
metadata can show the account logged in through Codex itself rather than the
alias selected with `--account`; proxied model requests still use the selected
codextra account.

After rotation, Codex's `/status` screen can show mixed account information:
the `Account` field comes from Codex's startup auth snapshot, while usage limits
and model requests come from the currently selected codextra proxy account.

Use `--desktop` to launch the Codex desktop app with the codextra proxy instead
of launching the terminal UI. The flag is consumed by `codextra`; remaining
arguments are passed to `codex app` after the proxy config overrides, so a path
argument opens that workspace in Codex Desktop. Keep the `codextra --desktop`
process running while using the desktop app, because it keeps the local proxy
alive.

### Desktop launcher app (macOS)

To launch Codex Desktop behind the proxy by clicking an icon instead of running
`codextra --desktop` from a terminal, install a launcher app:

```sh
codextra install-app
```

This creates `~/Applications/Codextra.app` — a small bundle that runs
`codextra --desktop` when opened from Finder, Launchpad, or the Dock, using the
codextra logo as its icon. It bakes in the resolved paths to `codextra` and
`codex` so it works even though macOS launches apps with a minimal `PATH`. The
launcher runs as a menu-bar agent (the tray), and Codex Desktop opens as usual.
Re-run `codextra install-app` after upgrading codextra to refresh the bundle,
and remove it with `rm -rf ~/Applications/Codextra.app`.

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

When the installer replaces the codextra binary, it sends `SIGUSR1` to running
`codextra` processes on Unix-like systems. The running wrapper defers restart
until the proxy reports no active request traffic, then relaunches itself with the
new binary.

```sh
CODEXTRA_UPGRADE_WAIT_SECONDS=10
```

`CODEXTRA_UPGRADE_WAIT_SECONDS` controls how long codextra waits for traffic to
go idle before giving up and restarting anyway. Windows does not support this
signal-based upgrade path in the current release.

Account metadata is stored at:

```sh
~/.codextra/accounts.json
```

When an account becomes temporarily unavailable due to usage availability or
authentication state, the proxy can switch to another configured account owned
by the user.

### macOS system tray

On macOS, codextra shows a menu bar icon while running.

- `Current`: account used for proxy requests (`eligible` account selection skips
  tokenless and temporarily limited accounts),
- `Selected`: alias set by `--account` when it differs from the currently active
  account,
- one menu item for each account with status (`ready`, `missing token`,
  `limited (reason) until <timestamp>`), with a checkmark on the current one.

Selecting an account switches the active codextra account immediately.

Disable the menu with:

```sh
CODEXTRA_NO_TRAY=1 codextra ...
```

Tray support is only available in macOS builds with `CGO_ENABLED=0`.
Tray backend is implemented in-repo (pure-Go/macOS ObjC dynamic bridge), so this
is the supported mode for menu bar tray behavior.

## Releases

Tagged releases are built by GoReleaser for macOS, Linux, and Windows on amd64
and arm64. Push a `v*` tag to publish a GitHub release with archives and
checksums.
