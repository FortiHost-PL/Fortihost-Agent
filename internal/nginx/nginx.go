package nginx

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"fortihost-agent/internal/config"
	"fortihost-agent/internal/store"
)

type SiteParams struct {
	Domain    string
	PublicDir string
	LogDir    string
	IsPhP     bool
	FPMSocket string
	SSL       bool
}

func configPath(cfg *config.Config, id string) string {
	return filepath.Join(cfg.NginxSitesDir, id+".conf")
}

func buildConfig(p SiteParams) string {
	var b strings.Builder

	if p.SSL {
		// HTTP → HTTPS redirect
		fmt.Fprintf(&b, "server {\n")
		fmt.Fprintf(&b, "    listen 80;\n    listen [::]:80;\n")
		fmt.Fprintf(&b, "    server_name %s;\n", p.Domain)
		fmt.Fprintf(&b, "    return 301 https://$server_name$request_uri;\n")
		fmt.Fprintf(&b, "}\n\n")

		fmt.Fprintf(&b, "server {\n")
		fmt.Fprintf(&b, "    listen 443 ssl http2;\n    listen [::]:443 ssl http2;\n")
		fmt.Fprintf(&b, "    server_name %s;\n\n", p.Domain)
		fmt.Fprintf(&b, "    ssl_certificate /etc/letsencrypt/live/%s/fullchain.pem;\n", p.Domain)
		fmt.Fprintf(&b, "    ssl_certificate_key /etc/letsencrypt/live/%s/privkey.pem;\n", p.Domain)
		fmt.Fprintf(&b, "    ssl_protocols TLSv1.2 TLSv1.3;\n")
		fmt.Fprintf(&b, "    ssl_prefer_server_ciphers on;\n")
		fmt.Fprintf(&b, "    ssl_session_cache shared:SSL:10m;\n\n")
	} else {
		fmt.Fprintf(&b, "server {\n")
		fmt.Fprintf(&b, "    listen 80;\n    listen [::]:80;\n")
		fmt.Fprintf(&b, "    server_name %s;\n\n", p.Domain)
	}

	fmt.Fprintf(&b, "    root %s;\n", p.PublicDir)
	if p.IsPhP {
		fmt.Fprintf(&b, "    index index.php index.html;\n")
	} else {
		fmt.Fprintf(&b, "    index index.html index.htm;\n")
	}
	fmt.Fprintf(&b, "    access_log %s/access.log;\n", p.LogDir)
	fmt.Fprintf(&b, "    error_log %s/error.log;\n\n", p.LogDir)

	if p.IsPhP {
		fmt.Fprintf(&b, "    location / {\n        try_files $uri $uri/ /index.php?$query_string;\n    }\n\n")
		fmt.Fprintf(&b, "    location ~ \\.php$ {\n")
		fmt.Fprintf(&b, "        fastcgi_pass unix:%s;\n", p.FPMSocket)
		fmt.Fprintf(&b, "        fastcgi_index index.php;\n")
		fmt.Fprintf(&b, "        include fastcgi_params;\n")
		fmt.Fprintf(&b, "        fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;\n")
		fmt.Fprintf(&b, "        fastcgi_param PHP_VALUE \"upload_max_filesize=64M\\npost_max_size=64M\";\n")
		fmt.Fprintf(&b, "    }\n\n")
	} else {
		fmt.Fprintf(&b, "    location / {\n        try_files $uri $uri/ =404;\n    }\n\n")
	}

	fmt.Fprintf(&b, "    location ~ /\\. { deny all; }\n}\n")
	return b.String()
}

func WriteConfig(cfg *config.Config, site *store.Site) error {
	siteDir := filepath.Join(cfg.SitesDir, site.ID)
	p := SiteParams{
		Domain:    site.Domain,
		PublicDir: filepath.Join(siteDir, "public"),
		LogDir:    filepath.Join(siteDir, "logs"),
		IsPhP:     site.Type == "php",
		FPMSocket: fmt.Sprintf("/run/php/php%s-fpm-%s.sock", site.PHPVersion, site.ID),
		SSL:       site.SSLEnabled,
	}

	content := buildConfig(p)
	path := configPath(cfg, site.ID)

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return err
	}
	return reload(cfg)
}

func DeleteConfig(cfg *config.Config, id string) error {
	path := configPath(cfg, id)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return reload(cfg)
}

func reload(cfg *config.Config) error {
	parts := strings.Fields(cfg.NginxReload)
	cmd := exec.Command(parts[0], parts[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("nginx reload: %w\n%s", err, out)
	}
	return nil
}

func Test() error {
	cmd := exec.Command("nginx", "-t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("nginx -t: %w\n%s", err, out)
	}
	return nil
}
