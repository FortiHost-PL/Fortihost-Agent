package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Site struct {
	ID           string    `json:"id"`
	Domain       string    `json:"domain"`
	Type         string    `json:"type"` // "static" | "php"
	PHPVersion   string    `json:"php_version,omitempty"`
	SSLEnabled   bool      `json:"ssl_enabled"`
	SFTPUser     string    `json:"sftp_user"`
	SFTPPassHash string    `json:"sftp_pass_hash"`
	CreatedAt    time.Time `json:"created_at"`
}

type Store struct {
	mu   sync.RWMutex
	path string
	data map[string]*Site
}

func New(path string) (*Store, error) {
	s := &Store{path: path, data: make(map[string]*Site)}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	f, err := os.Open(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer f.Close()

	var sites []*Site
	if err := json.NewDecoder(f).Decode(&sites); err != nil {
		return fmt.Errorf("decode store: %w", err)
	}
	for _, site := range sites {
		s.data[site.ID] = site
	}
	return nil
}

func (s *Store) save() error {
	sites := make([]*Site, 0, len(s.data))
	for _, v := range s.data {
		sites = append(sites, v)
	}

	tmp := s.path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(f).Encode(sites); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	f.Close()
	return os.Rename(tmp, s.path)
}

func (s *Store) GetAll() []*Site {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := make([]*Site, 0, len(s.data))
	for _, v := range s.data {
		list = append(list, v)
	}
	return list
}

func (s *Store) GetByID(id string) (*Site, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	site, ok := s.data[id]
	return site, ok
}

func (s *Store) GetBySFTPUser(user string) (*Site, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, site := range s.data {
		if site.SFTPUser == user {
			return site, true
		}
	}
	return nil, false
}

func (s *Store) Create(site *Site) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[site.ID] = site
	return s.save()
}

func (s *Store) Update(site *Site) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[site.ID] = site
	return s.save()
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, id)
	return s.save()
}

func EnsureDir(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0755)
}
