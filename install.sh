#!/bin/sh
set -eu

# inlet standalone installer script
# Supports macOS (darwin) and Linux on arm64 and amd64.

REPO="ynui/inlet"
INSTALL_DIR="${INLET_INSTALL_DIR:-}"

# Style helpers
if [ -t 1 ]; then
  BOLD="$(printf '\033[1m')"
  DIM="$(printf '\033[2m')"
  GREEN="$(printf '\033[32m')"
  YELLOW="$(printf '\033[33m')"
  RED="$(printf '\033[31m')"
  RESET="$(printf '\033[0m')"
  IS_TTY=1
else
  BOLD=""
  DIM=""
  GREEN=""
  YELLOW=""
  RED=""
  RESET=""
  IS_TTY=0
fi

info() {
  printf "  ${DIM}%s${RESET} %s\n" "•" "$1"
}

success() {
  printf "  ${GREEN}✓${RESET} ${BOLD}%s${RESET}\n" "$1"
}

warn() {
  printf "  ${YELLOW}!${RESET} %s\n" "$1"
}

error() {
  printf "\n  ${RED}✗${RESET} %s\n\n" "$1" >&2
  exit 1
}

# Run a command with an animated braille spinner on interactive terminals
spin() {
  label="$1"
  shift

  if [ "$IS_TTY" -ne 1 ]; then
    printf "  • %s...\n" "$label"
    "$@"
    return $?
  fi

  # Run the background command
  "$@" >/dev/null 2>&1 &
  cmd_pid=$!

  # Spinner loop
  frames='⠋ ⠙ ⠹ ⠸ ⠼ ⠴ ⠦ ⠧ ⠇ ⠏'
  printf "\033[?25l" # hide cursor
  while kill -0 "$cmd_pid" 2>/dev/null; do
    for frame in $frames; do
      printf "\r  ${GREEN}%s${RESET} %s..." "$frame" "$label"
      sleep 0.08
      if ! kill -0 "$cmd_pid" 2>/dev/null; then
        break
      fi
    done
  done

  wait "$cmd_pid"
  status=$?
  printf "\033[?25h" # restore cursor

  if [ "$status" -eq 0 ]; then
    printf "\r\033[2K  ${GREEN}✓${RESET} %s\n" "$label"
  else
    printf "\r\033[2K  ${RED}✗${RESET} %s (failed)\n" "$label"
  fi
  return "$status"
}

# Determine OS
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$OS" in
  linux*)  OS="linux" ;;
  darwin*) OS="darwin" ;;
  *)
    error "Unsupported operating system: $(uname -s). Prebuilt releases are at: https://github.com/$REPO/releases"
    ;;
esac

# Determine architecture
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64)   ARCH="amd64" ;;
  arm64|aarch64)  ARCH="arm64" ;;
  *)
    error "Unsupported architecture: $(uname -m). Prebuilt releases are at: https://github.com/$REPO/releases"
    ;;
esac

# Find latest version if not specified via VERSION
VERSION="${VERSION:-}"
if [ -z "$VERSION" ]; then
  if command -v curl >/dev/null 2>&1; then
    VERSION="$(curl -fsSL -H "Accept: application/vnd.github.v3+json" "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null | grep '"tag_name":' | head -n 1 | sed -E 's/.*"([^"]+)".*/\1/' || true)"
  elif command -v wget >/dev/null 2>&1; then
    VERSION="$(wget -qO- --header="Accept: application/vnd.github.v3+json" "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null | grep '"tag_name":' | head -n 1 | sed -E 's/.*"([^"]+)".*/\1/' || true)"
  fi
fi

if [ -z "$VERSION" ]; then
  error "Unable to determine latest release from GitHub. Specify explicitly: VERSION=v0.1.0 curl -fsSL ... | bash"
fi

TARBALL="inlet-${VERSION}-${OS}-${ARCH}.tar.gz"
DOWNLOAD_URL="https://github.com/$REPO/releases/download/${VERSION}/${TARBALL}"

printf "\n  ${BOLD}inlet${RESET} ${DIM}installer${RESET}\n\n"

# Create a temporary directory for download
TMP_DIR="$(mktemp -d 2>/dev/null || mktemp -d -t 'inlet-install')"
cleanup() {
  printf "\033[?25h" 2>/dev/null || true # ensure cursor restored
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT INT TERM

# Download archive with spinner
download_asset() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$DOWNLOAD_URL" -o "$TMP_DIR/$TARBALL"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$TMP_DIR/$TARBALL" "$DOWNLOAD_URL"
  else
    return 1
  fi
}

spin "Downloading ${BOLD}${VERSION}${RESET} for ${OS}/${ARCH}" download_asset || error "Failed to download inlet archive."

# Extract
tar -xzf "$TMP_DIR/$TARBALL" -C "$TMP_DIR"
if [ ! -f "$TMP_DIR/inlet" ]; then
  error "Unexpected archive contents: binary 'inlet' not found."
fi
chmod +x "$TMP_DIR/inlet"

# Select install destination
if [ -z "$INSTALL_DIR" ]; then
  if [ -w "/usr/local/bin" ]; then
    INSTALL_DIR="/usr/local/bin"
    SUDO=""
  elif command -v sudo >/dev/null 2>&1 && [ -d "/usr/local/bin" ] && [ -t 0 ]; then
    INSTALL_DIR="/usr/local/bin"
    SUDO="sudo"
  else
    # Non-root fallback without interactive sudo or writable /usr/local/bin
    if [ -d "$HOME/.local/bin" ] || echo "$PATH" | grep -q "$HOME/.local/bin"; then
      INSTALL_DIR="$HOME/.local/bin"
    elif [ -d "$HOME/bin" ] || echo "$PATH" | grep -q "$HOME/bin"; then
      INSTALL_DIR="$HOME/bin"
    else
      INSTALL_DIR="$HOME/.local/bin"
    fi
    SUDO=""
  fi
fi

mkdir -p "$INSTALL_DIR" 2>/dev/null || {
  if [ -n "${SUDO:-}" ]; then
    $SUDO mkdir -p "$INSTALL_DIR"
  else
    mkdir -p "$INSTALL_DIR"
  fi
}

install_bin() {
  if [ -n "${SUDO:-}" ]; then
    $SUDO cp "$TMP_DIR/inlet" "$INSTALL_DIR/inlet"
  else
    cp "$TMP_DIR/inlet" "$INSTALL_DIR/inlet"
  fi
}

spin "Installing binary to ${BOLD}$INSTALL_DIR/inlet${RESET}" install_bin || error "Failed to copy binary to $INSTALL_DIR/inlet."

success "Installed inlet ${VERSION}"

# Check PATH
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    printf "\n"
    warn "${BOLD}$INSTALL_DIR${RESET} is not in your PATH."
    printf "    Add it to your shell profile (~/.bashrc or ~/.zshrc):\n"
    printf "    ${DIM}export PATH=\"\$PATH:$INSTALL_DIR\"${RESET}\n"
    ;;
esac

printf "\n  ${BOLD}Next steps:${RESET}\n"
printf "    ${DIM}1.${RESET} Link device:   ${BOLD}inlet login${RESET}\n"
printf "    ${DIM}2.${RESET} Start tunnel:  ${BOLD}inlet http 3000${RESET}\n\n"
