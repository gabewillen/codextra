#!/bin/sh
set -eu

repo="gabewillen/codextra"
binary="codextra"

need() {
	if ! command -v "$1" >/dev/null 2>&1; then
		echo "missing required command: $1" >&2
		exit 1
	fi
}

check_codex() {
	if [ -n "${CODEXTRA_CODEX_BIN:-}" ]; then
		case "$CODEXTRA_CODEX_BIN" in
		*/*)
			if [ -x "$CODEXTRA_CODEX_BIN" ]; then
				return
			fi
			;;
		*)
			if command -v "$CODEXTRA_CODEX_BIN" >/dev/null 2>&1; then
				return
			fi
			;;
		esac
		echo "CODEXTRA_CODEX_BIN does not point to an executable codex binary: $CODEXTRA_CODEX_BIN" >&2
		exit 1
	fi

	if command -v codex >/dev/null 2>&1; then
		return
	fi

	echo "codex is not installed or is not on PATH" >&2
	echo "install OpenAI Codex first, or set CODEXTRA_CODEX_BIN=/path/to/codex" >&2
	exit 1
}

platform() {
	case "$(uname -s)" in
	Darwin) echo "darwin" ;;
	Linux) echo "linux" ;;
	*)
		echo "unsupported OS: $(uname -s)" >&2
		exit 1
		;;
	esac
}

arch() {
	case "$(uname -m)" in
	x86_64 | amd64) echo "amd64" ;;
	arm64 | aarch64) echo "arm64" ;;
	*)
		echo "unsupported architecture: $(uname -m)" >&2
		exit 1
		;;
	esac
}

latest_version() {
	url="$(curl -fsSIL -o /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest")"
	tag="${url##*/}"
	if [ "$tag" = "latest" ] || [ -z "$tag" ]; then
		echo "could not resolve latest release" >&2
		exit 1
	fi
	printf '%s\n' "${tag#v}"
}

path_contains() {
	case ":$PATH:" in
	*":$1:"*) return 0 ;;
	*) return 1 ;;
	esac
}

choose_install_dir() {
	if [ -n "${INSTALL_DIR:-}" ]; then
		printf '%s\n' "$INSTALL_DIR"
		return
	fi

	old_ifs="$IFS"
	IFS=:
	for dir in $PATH; do
		IFS="$old_ifs"
		case "$dir" in
		"$HOME/.local/bin" | "$HOME/bin" | /opt/homebrew/bin | /usr/local/bin)
			if [ -d "$dir" ] && [ -w "$dir" ]; then
				printf '%s\n' "$dir"
				return
			fi
			;;
		esac
		IFS=:
	done
	IFS="$old_ifs"

	printf '%s\n' "$HOME/.local/bin"
}

install_binary() {
	src="$1"
	dst="$2"
	if command -v install >/dev/null 2>&1; then
		install -m 0755 "$src" "$dst"
	else
		cp "$src" "$dst"
		chmod 0755 "$dst"
	fi
}

need curl
need tar
need mktemp
check_codex

version="${VERSION:-$(latest_version)}"
os="$(platform)"
cpu="$(arch)"
install_dir="$(choose_install_dir)"
archive="${binary}_${version}_${os}_${cpu}.tar.gz"
url="https://github.com/$repo/releases/download/v$version/$archive"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT INT TERM

mkdir -p "$install_dir"
curl -fsSL "$url" -o "$tmp/$archive"
tar -xzf "$tmp/$archive" -C "$tmp" "$binary"
install_binary "$tmp/$binary" "$install_dir/$binary"

echo "installed $binary $version to $install_dir/$binary"

if ! path_contains "$install_dir"; then
	echo "$install_dir is not on PATH" >&2
	echo "add this to your shell profile:" >&2
	echo "  export PATH=\"\$PATH:$install_dir\"" >&2
fi
