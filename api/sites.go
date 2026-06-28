package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"fortihost-agent/internal/nginx"
	"fortihost-agent/internal/phpfpm"
	sftpPkg "fortihost-agent/internal/sftp"
	"fortihost-agent/internal/ssl"
	"fortihost-agent/internal/store"
)

var validDomain = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?)*\.[a-zA-Z]{2,}$`)

type createSiteReq struct {
	Domain     string `json:"domain"`
	Type       string `json:"type"`       // "static" | "php"
	PHPVersion string `json:"php_version"` // e.g. "8.3"
}

type updateSiteReq struct {
	Domain     string `json:"domain"`
	PHPVersion string `json:"php_version"`
}

func (s *Server) listSites(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, map[string]any{"sites": s.store.GetAll()})
}

func (s *Server) getSite(w http.ResponseWriter, r *http.Request) {
	site, ok := siteOrError(s, w, r)
	if !ok {
		return
	}
	jsonOK(w, map[string]any{"site": site})
}

func (s *Server) createSite(w http.ResponseWriter, r *http.Request) {
	var req createSiteReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	req.Domain = strings.TrimSpace(strings.ToLower(req.Domain))
	if req.Domain == "" {
		jsonError(w, http.StatusBadRequest, "domain is required")
		return
	}
	if !validDomain.MatchString(req.Domain) {
		jsonError(w, http.StatusBadRequest, "invalid domain name")
		return
	}
	if req.Type != "static" && req.Type != "php" {
		jsonError(w, http.StatusBadRequest, "type must be 'static' or 'php'")
		return
	}
	if req.Type == "php" && req.PHPVersion == "" {
		req.PHPVersion = s.cfg.PHPVersion
	}

	id := randomID()
	siteDir := filepath.Join(s.cfg.SitesDir, id)

	for _, sub := range []string{"public", "logs", "tmp"} {
		if err := os.MkdirAll(filepath.Join(siteDir, sub), 0755); err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to create site directories")
			return
		}
	}

	// Place a starter index file.
	if req.Type == "php" {
		stub := []byte("<?php phpinfo(); ?>")
		os.WriteFile(filepath.Join(siteDir, "public", "index.php"), stub, 0644)
	} else {
		stub := []byte(fmt.Sprintf("<!DOCTYPE html><html><body><h1>%s is live!</h1></body></html>", req.Domain))
		os.WriteFile(filepath.Join(siteDir, "public", "index.html"), stub, 0644)
	}

	// Set www-data ownership so nginx can serve it.
	chownR(siteDir, "www-data")

	plainPass := randomPass()
	passHash, err := sftpPkg.HashPassword(plainPass)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	site := &store.Site{
		ID:           id,
		Domain:       req.Domain,
		Type:         req.Type,
		PHPVersion:   req.PHPVersion,
		SSLEnabled:   false,
		SFTPUser:     id,
		SFTPPassHash: passHash,
		CreatedAt:    time.Now(),
	}

	if req.Type == "php" {
		if err := phpfpm.WritePool(s.cfg, site); err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to write PHP-FPM pool: "+err.Error())
			return
		}
	}

	if err := nginx.WriteConfig(s.cfg, site); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to write nginx config: "+err.Error())
		return
	}

	if err := s.store.Create(site); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to save site")
		return
	}

	jsonCreated(w, map[string]any{
		"site":      site,
		"sftp_pass": plainPass,
	})
}

func (s *Server) updateSite(w http.ResponseWriter, r *http.Request) {
	site, ok := siteOrError(s, w, r)
	if !ok {
		return
	}

	var req updateSiteReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if req.Domain != "" {
		req.Domain = strings.TrimSpace(strings.ToLower(req.Domain))
		if !validDomain.MatchString(req.Domain) {
			jsonError(w, http.StatusBadRequest, "invalid domain name")
			return
		}
		site.Domain = req.Domain
	}
	if req.PHPVersion != "" {
		site.PHPVersion = req.PHPVersion
	}

	if err := nginx.WriteConfig(s.cfg, site); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to update nginx config: "+err.Error())
		return
	}
	if site.Type == "php" {
		if err := phpfpm.WritePool(s.cfg, site); err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to update PHP-FPM pool: "+err.Error())
			return
		}
	}

	if err := s.store.Update(site); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to update site")
		return
	}

	jsonOK(w, map[string]any{"site": site})
}

func (s *Server) deleteSite(w http.ResponseWriter, r *http.Request) {
	site, ok := siteOrError(s, w, r)
	if !ok {
		return
	}

	// Issue SSL revocation only if SSL was enabled.
	if site.SSLEnabled {
		ssl.Revoke(site.Domain) // best-effort
	}

	nginx.DeleteConfig(s.cfg, site.ID)
	phpfpm.DeletePool(s.cfg, site.ID)

	siteDir := filepath.Join(s.cfg.SitesDir, site.ID)
	os.RemoveAll(siteDir)

	if err := s.store.Delete(site.ID); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to delete site from store")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getSFTP(w http.ResponseWriter, r *http.Request) {
	site, ok := siteOrError(s, w, r)
	if !ok {
		return
	}
	host := strings.SplitN(s.cfg.SFTPListen, ":", 2)
	port := "2022"
	if len(host) == 2 {
		port = host[1]
	}
	jsonOK(w, map[string]any{
		"host": "<server-ip>",
		"port": port,
		"user": site.SFTPUser,
	})
}

func (s *Server) resetSFTP(w http.ResponseWriter, r *http.Request) {
	site, ok := siteOrError(s, w, r)
	if !ok {
		return
	}

	plain := randomPass()
	hash, err := sftpPkg.HashPassword(plain)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	site.SFTPPassHash = hash
	if err := s.store.Update(site); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to update password")
		return
	}

	jsonOK(w, map[string]string{"sftp_pass": plain})
}

func randomID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func randomPass() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func chownR(path, user string) {
	// Best-effort: run chown www-data:www-data recursively.
	// Ignore errors (e.g. in dev environments where www-data doesn't exist).
	os.Lchown(path, 33, 33) // 33 is typical www-data UID on Debian/Ubuntu
	filepath.Walk(path, func(p string, _ os.FileInfo, _ error) error {
		os.Lchown(p, 33, 33)
		return nil
	})
}
