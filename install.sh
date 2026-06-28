#!/usr/bin/env bash
# FortiHost Agent installer
# Run as root on a fresh Debian/Ubuntu 22.04+ server.

set -euo pipefail

AGENT_VERSION="1.0.0"
AGENT_USER="fortihost"
AGENT_DIR="/opt/fortihost-agent"
CONFIG_DIR="/etc/fortihost-agent"
DATA_DIR="/var/lib/fortihost-agent"
SITES_DIR="/var/www/sites"
GO_VERSION="1.22.5"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'

info()  { echo -e "${GREEN}[INFO]${NC}  $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*"; exit 1; }

[[ $EUID -ne 0 ]] && error "Run as root (sudo bash install.sh)"

# ─── Detect OS ────────────────────────────────────────────────────────────────
. /etc/os-release
if [[ "$ID" != "ubuntu" && "$ID" != "debian" ]]; then
    warn "This installer targets Debian/Ubuntu. Proceed with caution."
fi

info "Updating package list..."
apt-get update -qq

# ─── System dependencies ──────────────────────────────────────────────────────
info "Installing system dependencies..."
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq \
    curl wget git unzip \
    nginx \
    php8.3-fpm php8.3-cli php8.3-curl php8.3-mbstring php8.3-xml php8.3-zip php8.3-gd php8.3-intl php8.3-bcmath \
    certbot \
    ufw

# ─── Go ───────────────────────────────────────────────────────────────────────
if ! command -v go &>/dev/null || [[ "$(go version | awk '{print $3}')" != "go${GO_VERSION}" ]]; then
    info "Installing Go ${GO_VERSION}..."
    ARCH=$(dpkg --print-architecture)
    case $ARCH in
        amd64) GOARCH="amd64" ;;
        arm64) GOARCH="arm64" ;;
        *)     error "Unsupported architecture: $ARCH" ;;
    esac
    curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${GOARCH}.tar.gz" -o /tmp/go.tar.gz
    rm -rf /usr/local/go
    tar -C /usr/local -xzf /tmp/go.tar.gz
    rm /tmp/go.tar.gz
    export PATH="/usr/local/go/bin:$PATH"
    echo 'export PATH="/usr/local/go/bin:$PATH"' >> /etc/profile.d/go.sh
fi
info "Go version: $(go version)"

# ─── Service user ────────────────────────────────────────────────────────────
if ! id "$AGENT_USER" &>/dev/null; then
    info "Creating system user: $AGENT_USER"
    useradd --system --shell /usr/sbin/nologin --home "$AGENT_DIR" "$AGENT_USER"
fi

# ─── Directories ─────────────────────────────────────────────────────────────
info "Creating directories..."
mkdir -p "$AGENT_DIR" "$CONFIG_DIR" "$DATA_DIR" "$SITES_DIR"
chown "$AGENT_USER:$AGENT_USER" "$DATA_DIR" "$SITES_DIR"
chmod 750 "$DATA_DIR"

# ─── Build the daemon ─────────────────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

info "Building fortihost-agent..."
cd "$SCRIPT_DIR"
/usr/local/go/bin/go mod download
/usr/local/go/bin/go build -ldflags="-s -w" -o "$AGENT_DIR/fortihost-agent" .
chown "$AGENT_USER:$AGENT_USER" "$AGENT_DIR/fortihost-agent"
chmod 750 "$AGENT_DIR/fortihost-agent"

# ─── Config file ─────────────────────────────────────────────────────────────
if [[ ! -f "$CONFIG_DIR/config.yaml" ]]; then
    info "Creating default config at $CONFIG_DIR/config.yaml"
    TOKEN=$(openssl rand -hex 32)
    sed "s/CHANGE_ME_USE_OPENSSL_RAND_HEX_32/$TOKEN/" "$SCRIPT_DIR/config.yaml.example" \
        > "$CONFIG_DIR/config.yaml"
    chmod 600 "$CONFIG_DIR/config.yaml"
    chown "$AGENT_USER:$AGENT_USER" "$CONFIG_DIR/config.yaml"
    info ""
    info "  ┌─────────────────────────────────────────────────────────┐"
    info "  │  YOUR API TOKEN (save this — it won't be shown again)   │"
    info "  │                                                         │"
    info "  │  $TOKEN  │"
    info "  └─────────────────────────────────────────────────────────┘"
    info ""
    info "Edit $CONFIG_DIR/config.yaml to set certbot_email and other options."
else
    info "Config file already exists — skipping token generation."
fi

# ─── nginx ────────────────────────────────────────────────────────────────────
info "Configuring nginx..."
# Remove the default site so port 80 doesn't conflict.
rm -f /etc/nginx/sites-enabled/default
systemctl enable nginx
systemctl reload nginx 2>/dev/null || systemctl start nginx

# ─── PHP-FPM ─────────────────────────────────────────────────────────────────
info "Configuring PHP-FPM..."
systemctl enable php8.3-fpm
systemctl start php8.3-fpm

# ─── Let the agent reload nginx/php-fpm without a password ───────────────────
info "Configuring sudoers for the agent..."
cat > /etc/sudoers.d/fortihost-agent <<'EOF'
# Allow fortihost-agent to reload nginx and PHP-FPM without a password.
fortihost ALL=(ALL) NOPASSWD: /usr/bin/systemctl reload nginx
fortihost ALL=(ALL) NOPASSWD: /usr/bin/systemctl reload php8.3-fpm
EOF
chmod 440 /etc/sudoers.d/fortihost-agent

# ─── systemd unit ────────────────────────────────────────────────────────────
info "Installing systemd service..."
cat > /etc/systemd/system/fortihost-agent.service <<EOF
[Unit]
Description=FortiHost Agent
After=network.target nginx.service php8.3-fpm.service

[Service]
Type=simple
User=$AGENT_USER
Group=$AGENT_USER
ExecStart=$AGENT_DIR/fortihost-agent -config $CONFIG_DIR/config.yaml
Restart=always
RestartSec=5
AmbientCapabilities=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable fortihost-agent
systemctl restart fortihost-agent

# ─── Firewall ────────────────────────────────────────────────────────────────
info "Configuring UFW firewall..."
ufw --force enable
ufw allow ssh
ufw allow http
ufw allow https
ufw allow 2022/tcp comment "FortiHost SFTP"
# Port 8080 (API) should only be open to your panel server.
# Uncomment and replace with your panel IP:
# ufw allow from YOUR_PANEL_IP to any port 8080 proto tcp

# ─── Certbot auto-renew ──────────────────────────────────────────────────────
info "Setting up certbot auto-renew..."
systemctl enable certbot.timer 2>/dev/null || true

# ─── Done ─────────────────────────────────────────────────────────────────────
info ""
info "Installation complete."
info ""
info "Next steps:"
info "  1. Edit /etc/fortihost-agent/config.yaml — set certbot_email and verify paths."
info "  2. If the API port (8080) must be reachable from your panel, open it:"
info "     ufw allow from <panel-ip> to any port 8080 proto tcp"
info "  3. Restart the agent after any config change:"
info "     systemctl restart fortihost-agent"
info "  4. Check logs:"
info "     journalctl -u fortihost-agent -f"
info ""
info "API health check:"
info "  curl http://localhost:8080/api/v1/health"
