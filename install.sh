#!/usr/bin/env bash
# FortiHost Agent installer
# Run as root on a fresh Debian/Ubuntu 22.04+ server.

set -euo pipefail

AGENT_USER="fortihost"
AGENT_DIR="/opt/fortihost-agent"
CONFIG_DIR="/etc/fortihost-agent"
DATA_DIR="/var/lib/fortihost-agent"
SITES_DIR="/var/www/sites"
GO_VERSION="1.22.5"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'

info()  { echo -e "${GREEN}[INFO]${NC}  $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*"; exit 1; }
step()  { echo -e "\n${CYAN}━━━ $* ${NC}"; }

[[ $EUID -ne 0 ]] && error "Run as root (sudo bash install.sh)"

# ─── Welcome ──────────────────────────────────────────────────────────────────
echo -e "${CYAN}"
echo "  ███████╗ ██████╗ ██████╗ ████████╗██╗██╗  ██╗ ██████╗ ███████╗████████╗"
echo "  ██╔════╝██╔═══██╗██╔══██╗╚══██╔══╝██║██║  ██║██╔═══██╗██╔════╝╚══██╔══╝"
echo "  █████╗  ██║   ██║██████╔╝   ██║   ██║███████║██║   ██║███████╗   ██║   "
echo "  ██╔══╝  ██║   ██║██╔══██╗   ██║   ██║██╔══██║██║   ██║╚════██║   ██║   "
echo "  ██║     ╚██████╔╝██║  ██║   ██║   ██║██║  ██║╚██████╔╝███████║   ██║   "
echo "  ╚═╝      ╚═════╝ ╚═╝  ╚═╝   ╚═╝   ╚═╝╚═╝  ╚═╝ ╚═════╝ ╚══════╝   ╚═╝   "
echo "                                        Agent Installer"
echo -e "${NC}"

# ─── Interactive prompts ──────────────────────────────────────────────────────
step "Configuration"

read -rp "Node domain (e.g. node1.fortihost.pl) — leave empty to skip SSL setup: " NODE_DOMAIN
NODE_DOMAIN="${NODE_DOMAIN:-}"

if [[ -n "$NODE_DOMAIN" ]]; then
    read -rp "Email for Let's Encrypt (required for SSL): " CERTBOT_EMAIL
    [[ -z "$CERTBOT_EMAIL" ]] && error "Email is required when a domain is provided."
else
    CERTBOT_EMAIL=""
fi

# ─── Detect OS ────────────────────────────────────────────────────────────────
. /etc/os-release
if [[ "$ID" != "ubuntu" && "$ID" != "debian" ]]; then
    warn "This installer targets Debian/Ubuntu. Proceed with caution."
fi

step "Installing system packages"
apt-get update -qq
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq \
    curl wget git unzip \
    nginx \
    php8.3-fpm php8.3-cli php8.3-curl php8.3-mbstring php8.3-xml php8.3-zip php8.3-gd php8.3-intl php8.3-bcmath \
    certbot python3-certbot-nginx \
    ufw
info "Packages installed."

# ─── Go ───────────────────────────────────────────────────────────────────────
step "Installing Go ${GO_VERSION}"
if ! command -v /usr/local/go/bin/go &>/dev/null || \
   [[ "$(/usr/local/go/bin/go version 2>/dev/null | awk '{print $3}')" != "go${GO_VERSION}" ]]; then
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
    echo 'export PATH="/usr/local/go/bin:$PATH"' > /etc/profile.d/go.sh
fi
export PATH="/usr/local/go/bin:$PATH"
info "Go version: $(go version)"

# ─── Service user ────────────────────────────────────────────────────────────
step "Creating system user"
if ! id "$AGENT_USER" &>/dev/null; then
    useradd --system --shell /usr/sbin/nologin --home "$AGENT_DIR" "$AGENT_USER"
    info "User '$AGENT_USER' created."
else
    info "User '$AGENT_USER' already exists."
fi

# ─── Directories ─────────────────────────────────────────────────────────────
step "Creating directories"
mkdir -p "$AGENT_DIR" "$CONFIG_DIR" "$DATA_DIR" "$SITES_DIR"
# Config dir must be owned by the agent user so it can write the SSH host key at first start.
chown "$AGENT_USER:$AGENT_USER" "$CONFIG_DIR" "$DATA_DIR" "$SITES_DIR"
chmod 750 "$CONFIG_DIR" "$DATA_DIR"
info "Directories ready."

# ─── Build ────────────────────────────────────────────────────────────────────
step "Building fortihost-agent"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"
info "Downloading Go module dependencies..."
go mod tidy
info "Compiling..."
go build -ldflags="-s -w" -o "$AGENT_DIR/fortihost-agent" .
chown "$AGENT_USER:$AGENT_USER" "$AGENT_DIR/fortihost-agent"
chmod 750 "$AGENT_DIR/fortihost-agent"
info "Binary built: $AGENT_DIR/fortihost-agent"

# ─── Config file ─────────────────────────────────────────────────────────────
step "Generating configuration"
TOKEN=$(openssl rand -hex 32)

# API listens on localhost only — nginx proxies it with SSL if a domain is set.
API_LISTEN="127.0.0.1:8080"

if [[ ! -f "$CONFIG_DIR/config.yaml" ]]; then
    cat > "$CONFIG_DIR/config.yaml" <<YAML
token: "${TOKEN}"
listen: "${API_LISTEN}"
sftp_listen: ":2022"
sftp_host_key: "/etc/fortihost-agent/ssh_host_rsa_key"
data_dir: "/var/lib/fortihost-agent"
sites_dir: "/var/www/sites"
nginx_sites_dir: "/etc/nginx/sites-enabled"
nginx_reload: "sudo systemctl reload nginx"
phpfpm_pool_dir: "/etc/php/8.3/fpm/pool.d"
phpfpm_reload: "sudo systemctl reload php8.3-fpm"
php_version: "8.3"
certbot_email: "${CERTBOT_EMAIL}"
YAML
    chmod 600 "$CONFIG_DIR/config.yaml"
    chown "$AGENT_USER:$AGENT_USER" "$CONFIG_DIR/config.yaml"
    info "Config written to $CONFIG_DIR/config.yaml"
else
    TOKEN=$(grep '^token:' "$CONFIG_DIR/config.yaml" | awk '{print $2}' | tr -d '"')
    info "Config already exists — keeping existing token."
fi

# ─── nginx base setup ────────────────────────────────────────────────────────
step "Configuring nginx"
rm -f /etc/nginx/sites-enabled/default
systemctl enable nginx
nginx -t && systemctl reload nginx 2>/dev/null || systemctl start nginx
info "nginx ready."

# ─── nginx reverse proxy for node API ────────────────────────────────────────
if [[ -n "$NODE_DOMAIN" ]]; then
    step "Setting up nginx reverse proxy for ${NODE_DOMAIN}"

    # HTTP-only config first (needed for certbot webroot challenge)
    cat > "/etc/nginx/sites-available/fortihost-api" <<NGINX
server {
    listen 80;
    listen [::]:80;
    server_name ${NODE_DOMAIN};

    location /.well-known/acme-challenge/ {
        root /var/www/html;
    }

    location / {
        return 301 https://\$host\$request_uri;
    }
}
NGINX
    ln -sf /etc/nginx/sites-available/fortihost-api /etc/nginx/sites-enabled/fortihost-api
    nginx -t && systemctl reload nginx

    info "Issuing Let's Encrypt certificate for ${NODE_DOMAIN}..."
    mkdir -p /var/www/html
    certbot certonly \
        --webroot --webroot-path /var/www/html \
        --domain "$NODE_DOMAIN" \
        --email "$CERTBOT_EMAIL" \
        --agree-tos --non-interactive --keep-until-expiring

    # HTTPS config with reverse proxy
    cat > "/etc/nginx/sites-available/fortihost-api" <<NGINX
server {
    listen 80;
    listen [::]:80;
    server_name ${NODE_DOMAIN};
    return 301 https://\$host\$request_uri;
}

server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name ${NODE_DOMAIN};

    ssl_certificate /etc/letsencrypt/live/${NODE_DOMAIN}/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/${NODE_DOMAIN}/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_prefer_server_ciphers on;
    ssl_session_cache shared:SSL:10m;

    # Security headers
    add_header X-Frame-Options DENY;
    add_header X-Content-Type-Options nosniff;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_read_timeout 60s;
        proxy_send_timeout 60s;
        client_max_body_size 128m;
    }
}
NGINX
    nginx -t && systemctl reload nginx
    info "SSL reverse proxy configured — API available at https://${NODE_DOMAIN}"
fi

# ─── PHP-FPM ─────────────────────────────────────────────────────────────────
step "Configuring PHP-FPM"
systemctl enable php8.3-fpm
systemctl start php8.3-fpm
info "PHP 8.3-FPM running."

# ─── sudoers ────────────────────────────────────────────────────────────────
step "Configuring sudoers"
cat > /etc/sudoers.d/fortihost-agent <<'EOF'
fortihost ALL=(ALL) NOPASSWD: /usr/bin/systemctl reload nginx
fortihost ALL=(ALL) NOPASSWD: /usr/bin/systemctl reload php8.3-fpm
EOF
chmod 440 /etc/sudoers.d/fortihost-agent
info "sudoers configured."

# ─── systemd service ─────────────────────────────────────────────────────────
step "Installing systemd service"
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
NoNewPrivileges=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable fortihost-agent
systemctl restart fortihost-agent
info "Service started."

# ─── UFW ─────────────────────────────────────────────────────────────────────
step "Configuring UFW firewall"
ufw --force enable
ufw allow ssh
ufw allow http
ufw allow https
ufw allow 2022/tcp comment "FortiHost SFTP"
# Port 8080 is now on localhost only — no need to expose it if nginx proxies it.
# If you are NOT using the nginx proxy, restrict it to your panel IP:
# ufw allow from YOUR_PANEL_IP to any port 8080 proto tcp
info "Firewall configured."

# ─── Certbot auto-renew ──────────────────────────────────────────────────────
step "Certbot auto-renew"
systemctl enable certbot.timer 2>/dev/null || true
info "Certbot timer enabled."

# ─── Summary ─────────────────────────────────────────────────────────────────
echo ""
echo -e "${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${GREEN}  Installation complete!${NC}"
echo -e "${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""
echo "  API token (add to FortiHost panel):"
echo -e "  ${YELLOW}${TOKEN}${NC}"
echo ""
if [[ -n "$NODE_DOMAIN" ]]; then
    echo "  API endpoint:"
    echo -e "  ${CYAN}https://${NODE_DOMAIN}${NC}"
    echo ""
    echo "  In the FortiHost panel, set:"
    echo "    Hostname: ${NODE_DOMAIN}"
    echo "    API port: 443"
    echo "    Token:    (above)"
else
    echo "  API endpoint (localhost only):"
    echo -e "  ${CYAN}http://127.0.0.1:8080${NC}"
    echo ""
    warn "No domain set — API is only accessible from localhost."
    warn "To expose it securely, re-run the installer with a domain or set up nginx manually."
fi
echo ""
echo "  SFTP server: port 2022"
echo ""
echo "  Useful commands:"
echo "    journalctl -u fortihost-agent -f      # logs"
echo "    systemctl restart fortihost-agent     # restart"
echo "    curl http://localhost:8080/api/v1/health  # health check"
echo ""
