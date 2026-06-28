package api

import (
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"runtime"
	"time"

	"fortihost-agent/internal/config"
	"fortihost-agent/internal/store"
)

type Server struct {
	cfg   *config.Config
	store *store.Store
	mux   *http.ServeMux
}

func NewServer(cfg *config.Config, db *store.Store) *Server {
	s := &Server{cfg: cfg, store: db, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) Start() error {
	srv := &http.Server{
		Addr:         s.cfg.Listen,
		Handler:      s.mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	log.Printf("API listening on %s", s.cfg.Listen)
	return srv.ListenAndServe()
}

func (s *Server) routes() {
	auth := s.auth

	s.mux.HandleFunc("GET /api/v1/health", s.health)

	s.mux.HandleFunc("GET /api/v1/sites", auth(s.listSites))
	s.mux.HandleFunc("POST /api/v1/sites", auth(s.createSite))
	s.mux.HandleFunc("GET /api/v1/sites/{id}", auth(s.getSite))
	s.mux.HandleFunc("PUT /api/v1/sites/{id}", auth(s.updateSite))
	s.mux.HandleFunc("DELETE /api/v1/sites/{id}", auth(s.deleteSite))

	s.mux.HandleFunc("POST /api/v1/sites/{id}/ssl", auth(s.issueSSL))
	s.mux.HandleFunc("DELETE /api/v1/sites/{id}/ssl", auth(s.revokeSSL))

	s.mux.HandleFunc("GET /api/v1/sites/{id}/sftp", auth(s.getSFTP))
	s.mux.HandleFunc("POST /api/v1/sites/{id}/sftp/reset", auth(s.resetSFTP))

	s.mux.HandleFunc("GET /api/v1/sites/{id}/files", auth(s.listFiles))
	s.mux.HandleFunc("GET /api/v1/sites/{id}/files/content", auth(s.getFile))
	s.mux.HandleFunc("PUT /api/v1/sites/{id}/files/content", auth(s.putFile))
	s.mux.HandleFunc("DELETE /api/v1/sites/{id}/files/item", auth(s.deleteFile))
	s.mux.HandleFunc("POST /api/v1/sites/{id}/files/mkdir", auth(s.mkdir))
	s.mux.HandleFunc("POST /api/v1/sites/{id}/files/upload", auth(s.uploadFile))
	s.mux.HandleFunc("GET /api/v1/sites/{id}/files/download", auth(s.downloadFile))
	s.mux.HandleFunc("POST /api/v1/sites/{id}/files/rename", auth(s.renameFile))
}

// auth is middleware that validates the Bearer token.
func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const prefix = "Bearer "
		header := r.Header.Get("Authorization")
		if len(header) <= len(prefix) {
			jsonError(w, http.StatusUnauthorized, "missing authorization header")
			return
		}
		token := header[len(prefix):]
		if subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.Token)) != 1 {
			jsonError(w, http.StatusUnauthorized, "invalid token")
			return
		}
		next(w, r)
	}
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, map[string]any{
		"status": "ok",
		"sites":  len(s.store.GetAll()),
		"go":     runtime.Version(),
		"time":   time.Now().UTC(),
	})
}

// ---- helpers ----

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func jsonCreated(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func siteOrError(s *Server, w http.ResponseWriter, r *http.Request) (*store.Site, bool) {
	id := r.PathValue("id")
	site, ok := s.store.GetByID(id)
	if !ok {
		jsonError(w, http.StatusNotFound, "site not found")
		return nil, false
	}
	return site, true
}
