#!/usr/bin/env bash
set -euo pipefail

# Gua installer for Linux
# Usage: curl -sSfL https://raw.githubusercontent.com/CMGS/gua/refs/heads/master/scripts/install.sh | bash

VERSION="${GUA_VERSION:-0.1}"
REPO="CMGS/gua"
INSTALL_DIR="/usr/bin"

# --- Platform detection ---

OS=$(uname -s)
ARCH=$(uname -m)

if [ "$OS" != "Linux" ]; then
    echo "This installer only supports Linux."
    echo "For macOS, download manually from:"
    echo "  https://github.com/${REPO}/releases/download/v${VERSION}/gua_${VERSION}_Darwin_arm64.tar.gz"
    exit 1
fi

case "$ARCH" in
    x86_64)  ARCH_NAME="x86_64" ;;
    aarch64) ARCH_NAME="arm64" ;;
    arm64)   ARCH_NAME="arm64" ;;
    *)
        echo "Unsupported architecture: $ARCH"
        exit 1
        ;;
esac

TARBALL="gua_${VERSION}_Linux_${ARCH_NAME}.tar.gz"
URL="https://github.com/${REPO}/releases/download/v${VERSION}/${TARBALL}"

# --- Download and install ---

echo "==> Downloading gua v${VERSION} for Linux/${ARCH_NAME}..."
TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

curl -sSfL "$URL" -o "${TMPDIR}/${TARBALL}"
tar -xzf "${TMPDIR}/${TARBALL}" -C "$TMPDIR"

echo "==> Installing to ${INSTALL_DIR} (requires sudo)..."
sudo install -m 755 "${TMPDIR}/gua-server" "${INSTALL_DIR}/gua-server"
sudo install -m 755 "${TMPDIR}/gua-bridge" "${INSTALL_DIR}/gua-bridge"

echo "==> Installed:"
echo "    $(gua-server --version 2>/dev/null || echo "gua-server v${VERSION}")"
echo "    gua-bridge v${VERSION}"

# --- Systemd unit file ---

USER=$(whoami)
HOME_DIR=$(eval echo "~${USER}")
WORK_DIR="${HOME_DIR}/.gua/workspace"

cat <<UNIT | sudo tee /etc/systemd/system/gua.service > /dev/null
[Unit]
Description=Gua AI Agent Server
After=network.target

[Service]
Type=simple
User=${USER}
ExecStart=/usr/bin/gua-server start --backend wechat --agent claude --work-dir ${WORK_DIR} --bridge-bin /usr/bin/gua-bridge --model sonnet
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
UNIT

sudo systemctl daemon-reload

echo "==> Systemd service installed: gua.service"
echo ""

# --- Work directory ---

mkdir -p "$WORK_DIR"

# --- First-time setup ---

echo "============================================"
echo "  Installation complete!"
echo "============================================"
echo ""
echo "Next steps:"
echo ""
echo "  1. Make sure 'claude' CLI is installed and authenticated"
echo "     https://docs.anthropic.com/en/docs/claude-code"
echo ""
echo "  2. Make sure 'tmux' is installed"
echo "     sudo apt install tmux  # Debian/Ubuntu"
echo "     sudo yum install tmux  # CentOS/RHEL"
echo ""
echo "  3. Setup your first WeChat bot account:"
echo "     gua-server setup --backend wechat"
echo "     (Scan the QR code with WeChat)"
echo ""
echo "  4. Start the service:"
echo "     sudo systemctl start gua"
echo "     sudo systemctl enable gua  # auto-start on boot"
echo ""
echo "  5. Check status:"
echo "     sudo systemctl status gua"
echo "     journalctl -u gua -f  # follow logs"
echo ""
echo "  Work directory: ${WORK_DIR}"
echo "  Config:         ${HOME_DIR}/.gua/"
echo "  Service file:   /etc/systemd/system/gua.service"
echo ""
