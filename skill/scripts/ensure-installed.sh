#!/usr/bin/env bash
# ensure-installed.sh — make `li-assist` runnable, fastest path first. Idempotent.
#
# Ordering (each step announces itself in one line to stderr):
#   0. already on $PATH        -> nothing to do
#   1. download prebuilt binary -> from the latest GitHub release, no toolchain (seconds)
#   2. build from source        -> prefer a local checkout (incl. $PWD), clone only if none; needs Go
#
# Contract: the LAST line on stdout is a verdict the caller acts on:
#   READY <version>   the binary is installed and runnable -> proceed
#   MISSING <reason>  install could not complete -> read the stderr detail, fall back to SKILL.md
# Human/announce/warn lines go to stderr so stdout stays a clean verdict.

set -uo pipefail

REPO="kevin-burns/linkedin-assist"
BIN="li-assist"

log() { printf '%s\n' "$*" >&2; }

# 0) Already installed? Then we're done.
if command -v "$BIN" >/dev/null 2>&1; then
  echo "READY $("$BIN" --version 2>/dev/null | head -1 || echo unknown)"
  exit 0
fi

# Pick an install dir that's already on $PATH; if none is, default and remember to warn.
on_path=1
BINDIR=""
for d in "$HOME/.local/bin" "$(go env GOPATH 2>/dev/null)/bin" "$HOME/bin"; do
  [ -n "$d" ] || continue
  case ":$PATH:" in *":$d:"*) BINDIR="$d"; break;; esac
done
if [ -z "$BINDIR" ]; then BINDIR="$HOME/.local/bin"; on_path=0; fi
mkdir -p "$BINDIR"

finish() { # $1 = path to installed binary
  chmod +x "$1" 2>/dev/null
  # macOS quarantine is only set by GUI/browser downloads; curl/gh/go never set it, so this is a
  # no-op for our two install paths. Kept as a harmless safety net for a hand-downloaded archive.
  command -v xattr >/dev/null 2>&1 && xattr -d com.apple.quarantine "$1" 2>/dev/null
  local ver; ver="$("$1" --version 2>/dev/null | head -1 || echo unknown)"
  [ "$on_path" -eq 0 ] && log "note: $BINDIR is not on \$PATH — add it:  export PATH=\"$BINDIR:\$PATH\""
  echo "READY ${ver:-unknown}"
  exit 0
}

# Map platform to goreleaser asset naming: li-assist_<version>_<os>_<arch>.tar.gz
os="$(uname -s | tr '[:upper:]' '[:lower:]')"   # darwin | linux
arch="$(uname -m)"
case "$arch" in
  x86_64|amd64)  arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
esac

# 1) PREFERRED: download the prebuilt release binary.
try_prebuilt() {
  local ver="" v asset tmp b url
  if command -v gh >/dev/null 2>&1; then
    ver="$(gh release view -R "$REPO" --json tagName --jq .tagName 2>/dev/null)"
  fi
  if [ -z "$ver" ] && command -v curl >/dev/null 2>&1; then
    ver="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null \
           | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1)"
  fi
  [ -n "$ver" ] || return 1

  v="${ver#v}"
  asset="${BIN}_${v}_${os}_${arch}.tar.gz"
  tmp="$(mktemp -d)"
  log "li-assist not installed — downloading prebuilt ${ver} (${os}/${arch})…"

  if command -v gh >/dev/null 2>&1 && \
     gh release download "$ver" -R "$REPO" -p "$asset" -D "$tmp" 2>/dev/null; then
    :
  elif command -v curl >/dev/null 2>&1; then
    url="https://github.com/$REPO/releases/download/${ver}/${asset}"
    curl -fsSL "$url" -o "$tmp/$asset" 2>/dev/null || { rm -rf "$tmp"; return 1; }
  else
    rm -rf "$tmp"; return 1
  fi

  tar -xzf "$tmp/$asset" -C "$tmp" 2>/dev/null || { rm -rf "$tmp"; return 1; }
  b="$(find "$tmp" -type f -name "$BIN" 2>/dev/null | head -1)"
  [ -n "$b" ] || { rm -rf "$tmp"; return 1; }
  mv "$b" "$BINDIR/$BIN" 2>/dev/null || { rm -rf "$tmp"; return 1; }
  rm -rf "$tmp"
  finish "$BINDIR/$BIN"
}

# 2) FALLBACK: build from source. Prefer an existing checkout (incl. $PWD); clone only if none.
try_source() {
  command -v go >/dev/null 2>&1 || return 1
  local src="" d tmp gb
  for d in "$PWD" "$HOME/Developer/linkedin-helper" "$HOME/Developer/linkedin-assist" "$HOME/src/linkedin-assist"; do
    if [ -f "$d/go.mod" ] && [ -d "$d/cmd/$BIN" ]; then src="$d"; break; fi
  done

  if [ -n "$src" ]; then
    log "li-assist not installed — building from local checkout at $src…"
    ( cd "$src" && GOBIN="$BINDIR" go install "./cmd/$BIN" ) 2>/dev/null || return 1
  else
    command -v git >/dev/null 2>&1 || return 1
    log "li-assist not installed — cloning source and building…"
    tmp="$(mktemp -d)"
    git clone --depth 1 "https://github.com/$REPO" "$tmp/src" 2>/dev/null || { rm -rf "$tmp"; return 1; }
    ( cd "$tmp/src" && GOBIN="$BINDIR" go install "./cmd/$BIN" ) 2>/dev/null || { rm -rf "$tmp"; return 1; }
    rm -rf "$tmp"
  fi

  [ -x "$BINDIR/$BIN" ] && finish "$BINDIR/$BIN"
  # If GOBIN was ignored, go install lands in GOPATH/bin — check there too.
  gb="$(go env GOPATH 2>/dev/null)/bin/$BIN"
  [ -x "$gb" ] && { case ":$PATH:" in *":$(dirname "$gb"):"*) on_path=1;; *) on_path=0;; esac; BINDIR="$(dirname "$gb")"; finish "$gb"; }
  return 1
}

try_prebuilt || true
try_source   || true

echo "MISSING could not install via prebuilt download or source build — see the skill's install section"
exit 1
