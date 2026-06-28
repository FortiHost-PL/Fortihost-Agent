package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type fileEntry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	IsDir   bool      `json:"is_dir"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
	Mode    string    `json:"mode"`
}

// safePath resolves a user-supplied path relative to the site root,
// preventing path traversal.
func safePath(root, p string) (string, error) {
	clean := filepath.Clean(filepath.Join(root, filepath.FromSlash(p)))
	rootClean := filepath.Clean(root)
	if clean != rootClean && !strings.HasPrefix(clean, rootClean+string(filepath.Separator)) {
		return "", fmt.Errorf("path outside site root")
	}
	return clean, nil
}

func (s *Server) sitePublicDir(id string) string {
	return filepath.Join(s.cfg.SitesDir, id)
}

func (s *Server) listFiles(w http.ResponseWriter, r *http.Request) {
	site, ok := siteOrError(s, w, r)
	if !ok {
		return
	}
	root := s.sitePublicDir(site.ID)
	reqPath := r.URL.Query().Get("path")
	if reqPath == "" {
		reqPath = "/"
	}

	abs, err := safePath(root, reqPath)
	if err != nil {
		jsonError(w, http.StatusForbidden, err.Error())
		return
	}

	entries, err := os.ReadDir(abs)
	if err != nil {
		jsonError(w, http.StatusNotFound, "directory not found")
		return
	}

	files := make([]fileEntry, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		relPath := "/" + strings.TrimPrefix(filepath.ToSlash(filepath.Join(filepath.FromSlash(reqPath), e.Name())), "/")
		files = append(files, fileEntry{
			Name:    e.Name(),
			Path:    relPath,
			IsDir:   e.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
			Mode:    info.Mode().String(),
		})
	}

	sort.Slice(files, func(i, j int) bool {
		if files[i].IsDir != files[j].IsDir {
			return files[i].IsDir
		}
		return files[i].Name < files[j].Name
	})

	jsonOK(w, map[string]any{"path": reqPath, "files": files})
}

func (s *Server) getFile(w http.ResponseWriter, r *http.Request) {
	site, ok := siteOrError(s, w, r)
	if !ok {
		return
	}
	root := s.sitePublicDir(site.ID)

	abs, err := safePath(root, r.URL.Query().Get("path"))
	if err != nil {
		jsonError(w, http.StatusForbidden, err.Error())
		return
	}

	info, err := os.Stat(abs)
	if err != nil {
		jsonError(w, http.StatusNotFound, "file not found")
		return
	}
	if info.IsDir() {
		jsonError(w, http.StatusBadRequest, "path is a directory")
		return
	}
	if info.Size() > 5*1024*1024 {
		jsonError(w, http.StatusRequestEntityTooLarge, "file too large to read via API (max 5 MB)")
		return
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to read file")
		return
	}

	jsonOK(w, map[string]any{
		"path":    r.URL.Query().Get("path"),
		"content": string(data),
		"size":    info.Size(),
	})
}

func (s *Server) putFile(w http.ResponseWriter, r *http.Request) {
	site, ok := siteOrError(s, w, r)
	if !ok {
		return
	}
	root := s.sitePublicDir(site.ID)

	abs, err := safePath(root, r.URL.Query().Get("path"))
	if err != nil {
		jsonError(w, http.StatusForbidden, err.Error())
		return
	}

	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to create directories")
		return
	}
	if err := os.WriteFile(abs, []byte(req.Content), 0644); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to write file")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteFile(w http.ResponseWriter, r *http.Request) {
	site, ok := siteOrError(s, w, r)
	if !ok {
		return
	}
	root := s.sitePublicDir(site.ID)

	abs, err := safePath(root, r.URL.Query().Get("path"))
	if err != nil {
		jsonError(w, http.StatusForbidden, err.Error())
		return
	}
	if abs == filepath.Clean(root) {
		jsonError(w, http.StatusForbidden, "cannot delete site root")
		return
	}

	if err := os.RemoveAll(abs); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to delete")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) mkdir(w http.ResponseWriter, r *http.Request) {
	site, ok := siteOrError(s, w, r)
	if !ok {
		return
	}
	root := s.sitePublicDir(site.ID)

	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	abs, err := safePath(root, req.Path)
	if err != nil {
		jsonError(w, http.StatusForbidden, err.Error())
		return
	}
	if err := os.MkdirAll(abs, 0755); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to create directory")
		return
	}
	jsonCreated(w, map[string]string{"path": req.Path})
}

func (s *Server) uploadFile(w http.ResponseWriter, r *http.Request) {
	site, ok := siteOrError(s, w, r)
	if !ok {
		return
	}
	root := s.sitePublicDir(site.ID)

	r.ParseMultipartForm(64 << 20) // 64 MB max memory

	dir := r.URL.Query().Get("path")
	if dir == "" {
		dir = "/"
	}
	absDir, err := safePath(root, dir)
	if err != nil {
		jsonError(w, http.StatusForbidden, err.Error())
		return
	}
	if err := os.MkdirAll(absDir, 0755); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to create upload directory")
		return
	}

	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		jsonError(w, http.StatusBadRequest, "no files in request")
		return
	}

	uploaded := make([]string, 0, len(files))
	for _, fh := range files {
		// Sanitize filename — no path components allowed.
		name := filepath.Base(fh.Filename)
		dest := filepath.Join(absDir, name)

		src, err := fh.Open()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to open uploaded file")
			return
		}

		dst, err := os.Create(dest)
		if err != nil {
			src.Close()
			jsonError(w, http.StatusInternalServerError, "failed to save uploaded file")
			return
		}
		io.Copy(dst, src)
		src.Close()
		dst.Close()
		uploaded = append(uploaded, name)
	}

	jsonOK(w, map[string]any{"uploaded": uploaded})
}

func (s *Server) downloadFile(w http.ResponseWriter, r *http.Request) {
	site, ok := siteOrError(s, w, r)
	if !ok {
		return
	}
	root := s.sitePublicDir(site.ID)

	abs, err := safePath(root, r.URL.Query().Get("path"))
	if err != nil {
		jsonError(w, http.StatusForbidden, err.Error())
		return
	}

	info, err := os.Stat(abs)
	if err != nil {
		jsonError(w, http.StatusNotFound, "file not found")
		return
	}
	if info.IsDir() {
		jsonError(w, http.StatusBadRequest, "cannot download a directory")
		return
	}

	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, info.Name()))
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, abs)
}

func (s *Server) renameFile(w http.ResponseWriter, r *http.Request) {
	site, ok := siteOrError(s, w, r)
	if !ok {
		return
	}
	root := s.sitePublicDir(site.ID)

	var req struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	from, err := safePath(root, req.From)
	if err != nil {
		jsonError(w, http.StatusForbidden, err.Error())
		return
	}
	to, err := safePath(root, req.To)
	if err != nil {
		jsonError(w, http.StatusForbidden, err.Error())
		return
	}

	if err := os.Rename(from, to); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to rename: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
