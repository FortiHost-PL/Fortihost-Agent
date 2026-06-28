package ssl

import (
	"fmt"
	"os/exec"
)

// Issue requests a Let's Encrypt certificate for domain using the webroot method.
func Issue(domain, email, webroot string) error {
	cmd := exec.Command("certbot", "certonly",
		"--webroot",
		"--webroot-path", webroot,
		"--domain", domain,
		"--email", email,
		"--agree-tos",
		"--non-interactive",
		"--keep-until-expiring",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("certbot issue: %w\n%s", err, out)
	}
	return nil
}

// Revoke removes the Let's Encrypt certificate for domain.
func Revoke(domain string) error {
	cmd := exec.Command("certbot", "delete",
		"--cert-name", domain,
		"--non-interactive",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("certbot revoke: %w\n%s", err, out)
	}
	return nil
}

// Renew renews all certificates that are near expiry.
func Renew() error {
	cmd := exec.Command("certbot", "renew", "--non-interactive")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("certbot renew: %w\n%s", err, out)
	}
	return nil
}
