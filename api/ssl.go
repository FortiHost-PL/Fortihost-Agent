package api

import (
	"net/http"
	"path/filepath"

	"fortihost-agent/internal/nginx"
	"fortihost-agent/internal/ssl"
)

func (s *Server) issueSSL(w http.ResponseWriter, r *http.Request) {
	site, ok := siteOrError(s, w, r)
	if !ok {
		return
	}
	if s.cfg.CertbotEmail == "" {
		jsonError(w, http.StatusBadRequest, "certbot_email not configured")
		return
	}

	webroot := filepath.Join(s.cfg.SitesDir, site.ID, "public")

	if err := ssl.Issue(site.Domain, s.cfg.CertbotEmail, webroot); err != nil {
		jsonError(w, http.StatusInternalServerError, "certbot failed: "+err.Error())
		return
	}

	site.SSLEnabled = true
	if err := s.store.Update(site); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to update store")
		return
	}

	if err := nginx.WriteConfig(s.cfg, site); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to update nginx config: "+err.Error())
		return
	}

	jsonOK(w, map[string]any{"ssl_enabled": true, "domain": site.Domain})
}

func (s *Server) revokeSSL(w http.ResponseWriter, r *http.Request) {
	site, ok := siteOrError(s, w, r)
	if !ok {
		return
	}
	if !site.SSLEnabled {
		jsonError(w, http.StatusBadRequest, "SSL is not enabled for this site")
		return
	}

	if err := ssl.Revoke(site.Domain); err != nil {
		jsonError(w, http.StatusInternalServerError, "certbot revoke failed: "+err.Error())
		return
	}

	site.SSLEnabled = false
	if err := s.store.Update(site); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to update store")
		return
	}

	if err := nginx.WriteConfig(s.cfg, site); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to update nginx config: "+err.Error())
		return
	}

	jsonOK(w, map[string]any{"ssl_enabled": false})
}
