#!/usr/bin/env bash
set -euo pipefail

# Gua installer for Linux and macOS (idempotent)
# Usage: curl -sSfL https://raw.githubusercontent.com/CMGS/gua/refs/heads/master/scripts/install.sh | bash

VERSION="${GUA_VERSION:-0.1}"
REPO="CMGS/gua"

# --- Platform detection ---

OS=$(uname -s)
ARCH=$(uname -m)

case "$OS" in
    Linux)  OS_NAME="Linux" ;;
    Darwin) OS_NAME="Darwin" ;;
    *)      echo "Unsupported OS: $OS"; exit 1 ;;
esac

case "$ARCH" in
    x86_64)  ARCH_NAME="x86_64" ;;
    aarch64) ARCH_NAME="arm64" ;;
    arm64)   ARCH_NAME="arm64" ;;
    *)       echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

if [ "$OS_NAME" = "Darwin" ]; then
    INSTALL_DIR="/usr/local/bin"
else
    INSTALL_DIR="/usr/bin"
fi

CURRENT_USER=$(whoami)
HOME_DIR=$(eval echo "~${CURRENT_USER}")
WORK_DIR="${HOME_DIR}/.gua/workspace"
TARBALL="gua_${VERSION}_${OS_NAME}_${ARCH_NAME}.tar.gz"
URL="https://github.com/${REPO}/releases/download/v${VERSION}/${TARBALL}"

# --- Stop running service before upgrade ---

if [ "$OS_NAME" = "Linux" ] && systemctl is-active --quiet gua 2>/dev/null; then
    echo "==> Stopping running gua service..."
    sudo systemctl stop gua
fi

if [ "$OS_NAME" = "Darwin" ] && launchctl list 2>/dev/null | grep -q com.gua.server; then
    echo "==> Stopping running gua service..."
    launchctl stop com.gua.server 2>/dev/null || true
fi

# --- Download and install binaries ---

echo "==> Downloading gua v${VERSION} for ${OS_NAME}/${ARCH_NAME}..."
DL_DIR=$(mktemp -d)
trap 'rm -rf "$DL_DIR"' EXIT

curl -sSfL "$URL" -o "${DL_DIR}/${TARBALL}"
tar -xzf "${DL_DIR}/${TARBALL}" -C "$DL_DIR"

echo "==> Installing to ${INSTALL_DIR}..."
sudo install -m 755 "${DL_DIR}/gua-server" "${INSTALL_DIR}/gua-server"
sudo install -m 755 "${DL_DIR}/gua-bridge" "${INSTALL_DIR}/gua-bridge"

echo "    $("${INSTALL_DIR}/gua-server" --version 2>/dev/null || echo "gua-server v${VERSION}")"

# --- Work directory ---

mkdir -p "$WORK_DIR"

# --- Service configuration (idempotent: always overwrite) ---

if [ "$OS_NAME" = "Linux" ]; then
    cat <<UNIT | sudo tee /etc/systemd/system/gua.service > /dev/null
[Unit]
Description=Gua AI Agent Server
After=network.target

[Service]
Type=simple
User=${CURRENT_USER}
ExecStart=${INSTALL_DIR}/gua-server start --backend wechat --agent claude --work-dir ${WORK_DIR} --bridge-bin ${INSTALL_DIR}/gua-bridge --model sonnet
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
UNIT
    sudo systemctl daemon-reload
    echo "==> Systemd service configured: gua.service"
fi

if [ "$OS_NAME" = "Darwin" ]; then
    PLIST_DIR="${HOME_DIR}/Library/LaunchAgents"
    PLIST_FILE="${PLIST_DIR}/com.gua.server.plist"
    mkdir -p "$PLIST_DIR"
    cat <<PLIST > "$PLIST_FILE"
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.gua.server</string>
    <key>ProgramArguments</key>
    <array>
        <string>${INSTALL_DIR}/gua-server</string>
        <string>start</string>
        <string>--backend</string>
        <string>wechat</string>
        <string>--agent</string>
        <string>claude</string>
        <string>--work-dir</string>
        <string>${WORK_DIR}</string>
        <string>--bridge-bin</string>
        <string>${INSTALL_DIR}/gua-bridge</string>
        <string>--model</string>
        <string>sonnet</string>
    </array>
    <key>RunAtLoad</key>
    <false/>
    <key>KeepAlive</key>
    <dict>
        <key>SuccessfulExit</key>
        <false/>
    </dict>
    <key>StandardOutPath</key>
    <string>${HOME_DIR}/.gua/gua.log</string>
    <key>StandardErrorPath</key>
    <string>${HOME_DIR}/.gua/gua.log</string>
</dict>
</plist>
PLIST
    echo "==> LaunchAgent configured: com.gua.server"
fi

# --- Done ---

echo ""
echo "============================================"
echo "  Installation complete!"
echo "============================================"
echo ""
echo "Prerequisites:"
echo "  - claude CLI: https://docs.anthropic.com/en/docs/claude-code"
if [ "$OS_NAME" = "Linux" ]; then
    echo "  - tmux: sudo apt install tmux"
else
    echo "  - tmux: brew install tmux"
fi
echo ""
echo "Quick start:"
echo "  gua-server setup --backend wechat"
echo ""
if [ "$OS_NAME" = "Linux" ]; then
    echo "  sudo systemctl start gua"
    echo "  sudo systemctl enable gua"
    echo "  journalctl -u gua -f"
else
    echo "  launchctl load ~/Library/LaunchAgents/com.gua.server.plist"
    echo "  launchctl start com.gua.server"
    echo "  tail -f ~/.gua/gua.log"
fi
echo ""
