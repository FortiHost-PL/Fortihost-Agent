package phpfpm

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"fortihost-agent/internal/config"
	"fortihost-agent/internal/store"
)

func poolPath(cfg *config.Config, id string) string {
	return filepath.Join(cfg.PHPFPMPoolDir, id+".conf")
}

func socketPath(cfg *config.Config, id string) string {
	return fmt.Sprintf("/run/php/php%s-fpm-%s.sock", cfg.PHPVersion, id)
}

func buildPool(cfg *config.Config, site *store.Site) string {
	siteDir := filepath.Join(cfg.SitesDir, site.ID)
	socket := socketPath(cfg, site.ID)
	tmpDir := filepath.Join(siteDir, "tmp")

	var b strings.Builder
	fmt.Fprintf(&b, "[%s]\n", site.ID)
	fmt.Fprintf(&b, "user = www-data\n")
	fmt.Fprintf(&b, "group = www-data\n")
	fmt.Fprintf(&b, "listen = %s\n", socket)
	fmt.Fprintf(&b, "listen.owner = www-data\n")
	fmt.Fprintf(&b, "listen.group = www-data\n")
	fmt.Fprintf(&b, "pm = dynamic\n")
	fmt.Fprintf(&b, "pm.max_children = 10\n")
	fmt.Fprintf(&b, "pm.start_servers = 2\n")
	fmt.Fprintf(&b, "pm.min_spare_servers = 1\n")
	fmt.Fprintf(&b, "pm.max_spare_servers = 3\n")
	fmt.Fprintf(&b, "chdir = %s\n", filepath.Join(siteDir, "public"))
	fmt.Fprintf(&b, "php_admin_value[upload_tmp_dir] = %s\n", tmpDir)
	fmt.Fprintf(&b, "php_admin_value[sys_temp_dir] = %s\n", tmpDir)
	fmt.Fprintf(&b, "php_admin_value[open_basedir] = %s:/tmp\n", siteDir)
	return b.String()
}

func WritePool(cfg *config.Config, site *store.Site) error {
	if site.Type != "php" {
		return nil
	}

	path := poolPath(cfg, site.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(buildPool(cfg, site)), 0644); err != nil {
		return err
	}
	return reload(cfg)
}

func DeletePool(cfg *config.Config, id string) error {
	path := poolPath(cfg, id)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return reload(cfg)
}

func reload(cfg *config.Config) error {
	parts := strings.Fields(cfg.PHPFPMReload)
	cmd := exec.Command(parts[0], parts[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("php-fpm reload: %w\n%s", err, out)
	}
	return nil
}

func SocketPath(cfg *config.Config, id string) string {
	return socketPath(cfg, id)
}
