package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Token       string `yaml:"token"`
	Listen      string `yaml:"listen"`
	SFTPListen  string `yaml:"sftp_listen"`
	SFTPHostKey string `yaml:"sftp_host_key"`
	DataDir     string `yaml:"data_dir"`
	SitesDir    string `yaml:"sites_dir"`

	NginxSitesDir string `yaml:"nginx_sites_dir"`
	NginxReload   string `yaml:"nginx_reload"`

	PHPFPMPoolDir string `yaml:"phpfpm_pool_dir"`
	PHPFPMReload  string `yaml:"phpfpm_reload"`
	PHPVersion    string `yaml:"php_version"`

	CertbotEmail string `yaml:"certbot_email"`
}

func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()

	cfg := &Config{
		Listen:        ":8080",
		SFTPListen:    ":2022",
		SFTPHostKey:   "/etc/fortihost-agent/ssh_host_rsa_key",
		DataDir:       "/var/lib/fortihost-agent",
		SitesDir:      "/var/www/sites",
		NginxSitesDir: "/etc/nginx/sites-enabled",
		NginxReload:   "systemctl reload nginx",
		PHPFPMPoolDir: "/etc/php/8.3/fpm/pool.d",
		PHPFPMReload:  "systemctl reload php8.3-fpm",
		PHPVersion:    "8.3",
	}

	if err := yaml.NewDecoder(f).Decode(cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if cfg.Token == "" {
		return nil, fmt.Errorf("token must be set in config")
	}

	return cfg, nil
}
