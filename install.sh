#!/usr/bin/env bash
# install.sh installs a prebuilt lana binary from the GitHub Releases of
# ridhomujizat/lana-harness.
#
# The repository is private, so the download needs GitHub access: an
# authenticated `gh` CLI (gh auth login) or a GITHUB_TOKEN with read access to
# the repository.
#
#   gh api -H 'Accept: application/vnd.github.raw' \
#     'repos/ridhomujizat/lana-harness/contents/install.sh?ref=development' | bash
#
# Environment:
#   LANA_VERSION      tag to install, e.g. v0.1.0-beta.1 (default: newest
#                     release, betas included)
#   LANA_INSTALL_DIR  target folder (default: ~/.local/bin)
#   LANA_REPO         owner/name (default: ridhomujizat/lana-harness)
#   LANA_FROM_DIR     install from a local folder holding lana-<os>-<arch> and
#                     SHA256SUMS (output of `make release`) instead of GitHub
#   GITHUB_TOKEN      token used when gh is not available

set -euo pipefail

REPO="${LANA_REPO:-ridhomujizat/lana-harness}"
INSTALL_DIR="${LANA_INSTALL_DIR:-$HOME/.local/bin}"
VERSION="${LANA_VERSION:-}"

say() { printf '%s\n' "$*"; }
fail() { printf 'error: %s\n' "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

detect_platform() {
  local os arch
  case "$(uname -s)" in
    Darwin) os=darwin ;;
    Linux) os=linux ;;
    *) fail "unsupported OS: $(uname -s) (lana supports macOS and Linux)" ;;
  esac
  case "$(uname -m)" in
    arm64 | aarch64) arch=arm64 ;;
    x86_64 | amd64) arch=amd64 ;;
    *) fail "unsupported CPU: $(uname -m)" ;;
  esac
  printf '%s-%s' "$os" "$arch"
}

sha256_of() {
  if have sha256sum; then
    sha256sum "$1" | awk '{print $1}'
  elif have shasum; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    fail "need sha256sum or shasum to verify the download"
  fi
}

gh_ready() { have gh && gh auth status -h github.com >/dev/null 2>&1; }

api() {
  # api <path> prints the JSON of a GitHub REST call.
  if gh_ready; then
    gh api "$1"
  elif [ -n "${GITHUB_TOKEN:-}" ]; then
    curl -fsSL -H "Authorization: Bearer $GITHUB_TOKEN" \
      -H 'Accept: application/vnd.github+json' "https://api.github.com/$1"
  else
    fail "no GitHub access: run 'gh auth login' or set GITHUB_TOKEN (the repository is private)"
  fi
}

latest_tag() {
  # The newest release, prereleases (betas) included.
  api "repos/$REPO/releases?per_page=1" |
    sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1
}

download_asset() {
  # download_asset <tag> <name> <dest>
  local tag="$1" name="$2" dest="$3"
  if gh_ready; then
    gh release download "$tag" -R "$REPO" -p "$name" -O "$dest" --clobber
    return
  fi
  local id
  id="$(api "repos/$REPO/releases/tags/$tag" | tr ',' '\n' |
    awk -v want="\"$name\"" '
      /"id":/ { gsub(/[^0-9]/, "", $0); last = $0 }
      /"name":/ { if (index($0, want)) { print last; exit } }')"
  [ -n "$id" ] || fail "release $tag has no asset $name"
  curl -fsSL -H "Authorization: Bearer $GITHUB_TOKEN" \
    -H 'Accept: application/octet-stream' \
    -o "$dest" "https://api.github.com/repos/$REPO/releases/assets/$id"
}

TMP_DIR=""
cleanup() { if [ -n "$TMP_DIR" ]; then rm -rf "$TMP_DIR"; fi; }
trap cleanup EXIT

main() {
  local platform asset tmp
  platform="$(detect_platform)"
  asset="lana-$platform"
  TMP_DIR="$(mktemp -d)"
  tmp="$TMP_DIR"

  if [ -n "${LANA_FROM_DIR:-}" ]; then
    say "Installing $asset from $LANA_FROM_DIR"
    [ -f "$LANA_FROM_DIR/$asset" ] || fail "$LANA_FROM_DIR/$asset not found (run make release)"
    cp "$LANA_FROM_DIR/$asset" "$tmp/$asset"
    [ -f "$LANA_FROM_DIR/SHA256SUMS" ] && cp "$LANA_FROM_DIR/SHA256SUMS" "$tmp/SHA256SUMS"
  else
    if [ -z "$VERSION" ]; then
      VERSION="$(latest_tag)"
      [ -n "$VERSION" ] || fail "no release found in $REPO"
    fi
    say "Installing lana $VERSION ($platform) from $REPO"
    download_asset "$VERSION" "$asset" "$tmp/$asset"
    download_asset "$VERSION" SHA256SUMS "$tmp/SHA256SUMS"
  fi

  if [ -f "$tmp/SHA256SUMS" ]; then
    local want got
    want="$(awk -v f="$asset" '$2 == f || $2 == "*"f {print $1}' "$tmp/SHA256SUMS")"
    [ -n "$want" ] || fail "SHA256SUMS has no entry for $asset"
    got="$(sha256_of "$tmp/$asset")"
    [ "$want" = "$got" ] || fail "checksum mismatch for $asset"
    say "✓ checksum verified"
  else
    say "! no SHA256SUMS next to the binary; skipping verification"
  fi

  chmod 0755 "$tmp/$asset"
  if [ "$(uname -s)" = Darwin ] && have xattr; then
    xattr -d com.apple.quarantine "$tmp/$asset" 2>/dev/null || true
  fi
  mkdir -p "$INSTALL_DIR"
  mv -f "$tmp/$asset" "$INSTALL_DIR/lana"
  say "✓ installed $("$INSTALL_DIR/lana" --version) to $INSTALL_DIR/lana"

  case ":$PATH:" in
    *":$INSTALL_DIR:"*) ;;
    *)
      say ""
      say "$INSTALL_DIR is not on your PATH. Add it:"
      say "  echo 'export PATH=\"$INSTALL_DIR:\$PATH\"' >> ~/.zshrc && exec zsh"
      ;;
  esac

  say ""
  say "Next:"
  if [ -f "${LANA_HOME:-$HOME/.lana}/config.toml" ]; then
    say "  restart the daemon: lana daemon"
  else
    say "  lana init --provider deepseek   # or openai, openrouter"
    say "  lana chat"
  fi
}

main "$@"
