# FortiHost Agent

A lightweight web hosting daemon written in Go. Manages static HTML and PHP sites on a single server — with nginx vhost generation, PHP-FPM pool isolation, Let's Encrypt SSL, a JSON file manager API, and a built-in SFTP server.

---

## Features

| Feature | Details |
|---|---|
| Site types | Static HTML, PHP (any version) |
| Web server | nginx (vhost config auto-generated) |
| PHP | PHP-FPM with per-site pool isolation |
| SSL | Let's Encrypt via certbot (webroot method) |
| File manager | REST API — list, read, write, upload, download, rename, delete |
| SFTP | Built-in server, per-site chroot, no OS users required |
| Auth | Shared-secret Bearer token |
| State | Single JSON file (`/var/lib/fortihost-agent/sites.json`) |

---

## Requirements

- **OS**: Debian 12 / Ubuntu 22.04+
- **RAM**: 256 MB minimum
- **Ports**: 80, 443, 2022 (SFTP), 8080 (API — internal only)

---

## Installation

```bash
# On the target server (as root):
git clone https://github.com/your-org/fortihost-agent /opt/fortihost-agent-src
cd /opt/fortihost-agent-src
sudo bash install.sh
```

The installer will:
1. Install Go, nginx, PHP 8.3-FPM, certbot, ufw
2. Build the binary → `/opt/fortihost-agent/fortihost-agent`
3. Generate a random API token and write it to `/etc/fortihost-agent/config.yaml`
4. Register and start a systemd service
5. Configure UFW (opens 80, 443, 22, 2022 — **not** 8080 by default)

> **Save the token** printed at the end — you need it to call the API.

### After installation

```bash
# Edit config (certbot_email is required for SSL):
nano /etc/fortihost-agent/config.yaml

# Restart after config changes:
systemctl restart fortihost-agent

# Tail logs:
journalctl -u fortihost-agent -f

# Health check:
curl http://localhost:8080/api/v1/health
```

---

## Configuration

`/etc/fortihost-agent/config.yaml` (full reference: [`config.yaml.example`](config.yaml.example))

| Key | Default | Description |
|---|---|---|
| `token` | — | **Required.** Bearer token for all API calls. |
| `listen` | `:8080` | HTTP API bind address. |
| `sftp_listen` | `:2022` | Built-in SFTP bind address. |
| `sftp_host_key` | `/etc/fortihost-agent/ssh_host_rsa_key` | RSA host key (auto-generated on first start). |
| `data_dir` | `/var/lib/fortihost-agent` | Agent state directory. |
| `sites_dir` | `/var/www/sites` | Root for all site files. |
| `nginx_sites_dir` | `/etc/nginx/sites-enabled` | Where nginx vhosts are written. |
| `nginx_reload` | `systemctl reload nginx` | Command to reload nginx. |
| `phpfpm_pool_dir` | `/etc/php/8.3/fpm/pool.d` | Where PHP-FPM pools are written. |
| `phpfpm_reload` | `systemctl reload php8.3-fpm` | Command to reload PHP-FPM. |
| `php_version` | `8.3` | Default PHP version for new sites. |
| `certbot_email` | — | **Required for SSL.** Email for Let's Encrypt. |

---

## API Reference

All endpoints (except `/api/v1/health`) require:
```
Authorization: Bearer <token>
```

### Sites

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/health` | Health check (no auth) |
| `GET` | `/api/v1/sites` | List all sites |
| `POST` | `/api/v1/sites` | Create site |
| `GET` | `/api/v1/sites/{id}` | Get site details |
| `PUT` | `/api/v1/sites/{id}` | Update domain / PHP version |
| `DELETE` | `/api/v1/sites/{id}` | Delete site (removes files, nginx config, FPM pool) |

**Create site body:**
```json
{
  "domain": "example.com",
  "type": "static",        // "static" | "php"
  "php_version": "8.3"     // PHP sites only; omit to use server default
}
```

**Response includes `sftp_pass`** (only shown once at creation):
```json
{
  "site": { "id": "a1b2c3", "domain": "example.com", ... },
  "sftp_pass": "f3e2a1..."
}
```

### SSL

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/v1/sites/{id}/ssl` | Issue Let's Encrypt cert + enable HTTPS |
| `DELETE` | `/api/v1/sites/{id}/ssl` | Revoke cert + revert to HTTP |

The domain must already resolve to the server's IP before calling `POST /ssl`.

### SFTP credentials

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/sites/{id}/sftp` | Get SFTP connection info |
| `POST` | `/api/v1/sites/{id}/sftp/reset` | Generate a new SFTP password |

### File manager

All file paths are relative to the site root (`/var/www/sites/{id}/`).

| Method | Path | Query / Body | Description |
|---|---|---|---|
| `GET` | `/api/v1/sites/{id}/files` | `?path=/` | List directory |
| `GET` | `/api/v1/sites/{id}/files/content` | `?path=/index.php` | Read file (max 5 MB) |
| `PUT` | `/api/v1/sites/{id}/files/content` | `?path=/index.php` + `{"content":"..."}` | Write file |
| `DELETE` | `/api/v1/sites/{id}/files/item` | `?path=/old.php` | Delete file or directory |
| `POST` | `/api/v1/sites/{id}/files/mkdir` | `{"path":"/uploads"}` | Create directory |
| `POST` | `/api/v1/sites/{id}/files/upload` | `?path=/` + multipart `files[]` | Upload files (max 64 MB) |
| `GET` | `/api/v1/sites/{id}/files/download` | `?path=/archive.zip` | Download file |
| `POST` | `/api/v1/sites/{id}/files/rename` | `{"from":"/a.php","to":"/b.php"}` | Rename / move |

---

## SFTP access

The built-in SFTP server does **not** use OS users. Each site gets its own username (the site ID) and bcrypt-hashed password. Files are chrooted to the site directory.

```bash
# Connect with any SFTP client:
sftp -P 2022 a1b2c3@your-server.com
```

To reset the SFTP password:
```bash
curl -s -X POST http://localhost:8080/api/v1/sites/a1b2c3/sftp/reset \
  -H "Authorization: Bearer $TOKEN"
```

---

## Site directory layout

```
/var/www/sites/{id}/
├── public/   ← document root (served by nginx, writable via SFTP/API)
├── logs/     ← nginx access.log + error.log
└── tmp/      ← PHP upload_tmp_dir, sys_temp_dir (PHP sites only)
```

---

## SSL certificate auto-renewal

certbot's systemd timer runs twice daily automatically. No manual cron setup needed.

```bash
# Verify timer is active:
systemctl status certbot.timer

# Manual renewal test:
certbot renew --dry-run
```

---

## Firewall

After install, UFW is active with these rules:
- 22/tcp — SSH
- 80/tcp — HTTP
- 443/tcp — HTTPS
- 2022/tcp — SFTP

The API port **8080 is not exposed by default**. Open it only to your panel server:

```bash
ufw allow from <panel-server-ip> to any port 8080 proto tcp
```

---

## Updating

```bash
cd /path/to/fortihost-agent-src
git pull
go build -ldflags="-s -w" -o /opt/fortihost-agent/fortihost-agent .
systemctl restart fortihost-agent
```
