package sftp

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	pkgsftp "github.com/pkg/sftp"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/ssh"

	"fortihost-agent/internal/config"
	"fortihost-agent/internal/store"
)

type Server struct {
	cfg   *config.Config
	store *store.Store
}

func NewServer(cfg *config.Config, store *store.Store) *Server {
	return &Server{cfg: cfg, store: store}
}

func (s *Server) Start() error {
	hostKey, err := s.loadOrCreateHostKey()
	if err != nil {
		return fmt.Errorf("host key: %w", err)
	}

	sshCfg := &ssh.ServerConfig{
		PasswordCallback: func(conn ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			site, ok := s.store.GetBySFTPUser(conn.User())
			if !ok {
				return nil, fmt.Errorf("unknown user")
			}
			if err := bcrypt.CompareHashAndPassword([]byte(site.SFTPPassHash), pass); err != nil {
				return nil, fmt.Errorf("bad password")
			}
			return &ssh.Permissions{
				Extensions: map[string]string{"site_id": site.ID},
			}, nil
		},
	}
	sshCfg.AddHostKey(hostKey)

	ln, err := net.Listen("tcp", s.cfg.SFTPListen)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	log.Printf("SFTP listening on %s", s.cfg.SFTPListen)

	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go s.handleConn(conn, sshCfg)
	}
}

func (s *Server) handleConn(conn net.Conn, cfg *ssh.ServerConfig) {
	defer conn.Close()

	sc, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	defer sc.Close()

	siteID := sc.Permissions.Extensions["site_id"]
	root := filepath.Join(s.cfg.SitesDir, siteID, "public")

	go ssh.DiscardRequests(reqs)

	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			newChan.Reject(ssh.UnknownChannelType, "only session channels supported")
			continue
		}
		ch, requests, err := newChan.Accept()
		if err != nil {
			return
		}
		go func(ch ssh.Channel, reqs <-chan *ssh.Request) {
			defer ch.Close()
			for req := range reqs {
				if req.Type == "subsystem" && len(req.Payload) > 4 {
					name := string(req.Payload[4:])
					if name == "sftp" {
						req.Reply(true, nil)
						h := &siteHandler{root: root}
						srv := pkgsftp.NewRequestServer(ch, pkgsftp.Handlers{
							FileGet:  h,
							FilePut:  h,
							FileCmd:  h,
							FileList: h,
						})
						srv.Serve()
						return
					}
				}
				if req.WantReply {
					req.Reply(false, nil)
				}
			}
		}(ch, requests)
	}
}

func (s *Server) loadOrCreateHostKey() (ssh.Signer, error) {
	keyPath := s.cfg.SFTPHostKey
	if err := os.MkdirAll(filepath.Dir(keyPath), 0700); err != nil {
		return nil, err
	}

	data, err := os.ReadFile(keyPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		// Generate new key
		key, err := rsa.GenerateKey(rand.Reader, 4096)
		if err != nil {
			return nil, err
		}
		data = pem.EncodeToMemory(&pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: x509.MarshalPKCS1PrivateKey(key),
		})
		if err := os.WriteFile(keyPath, data, 0600); err != nil {
			return nil, err
		}
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("invalid PEM in host key file")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	return ssh.NewSignerFromKey(key)
}

// HashPassword bcrypt-hashes a plain-text SFTP password.
func HashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	return string(b), err
}

// ---- chrooted SFTP handler ----

type siteHandler struct {
	root string
}

func (h *siteHandler) safePath(p string) (string, error) {
	clean := filepath.Clean(filepath.Join(h.root, p))
	rootClean := filepath.Clean(h.root)
	if clean != rootClean && !strings.HasPrefix(clean, rootClean+string(filepath.Separator)) {
		return "", fmt.Errorf("path outside site root")
	}
	return clean, nil
}

func (h *siteHandler) Fileread(r *pkgsftp.Request) (io.ReaderAt, error) {
	p, err := h.safePath(r.Filepath)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}

func (h *siteHandler) Filewrite(r *pkgsftp.Request) (io.WriterAt, error) {
	p, err := h.safePath(r.Filepath)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return nil, err
	}
	return os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
}

func (h *siteHandler) Filecmd(r *pkgsftp.Request) error {
	switch r.Method {
	case "Setstat":
		return nil
	case "Rename":
		from, err := h.safePath(r.Filepath)
		if err != nil {
			return err
		}
		to, err := h.safePath(r.Target)
		if err != nil {
			return err
		}
		return os.Rename(from, to)
	case "Mkdir":
		p, err := h.safePath(r.Filepath)
		if err != nil {
			return err
		}
		return os.MkdirAll(p, 0755)
	case "Remove":
		p, err := h.safePath(r.Filepath)
		if err != nil {
			return err
		}
		return os.Remove(p)
	case "Rmdir":
		p, err := h.safePath(r.Filepath)
		if err != nil {
			return err
		}
		return os.Remove(p)
	case "Symlink":
		return fmt.Errorf("symlinks are not supported")
	}
	return fmt.Errorf("unsupported method: %s", r.Method)
}

func (h *siteHandler) Filelist(r *pkgsftp.Request) (pkgsftp.ListerAt, error) {
	p, err := h.safePath(r.Filepath)
	if err != nil {
		return nil, err
	}

	switch r.Method {
	case "List":
		entries, err := os.ReadDir(p)
		if err != nil {
			return nil, err
		}
		infos := make([]os.FileInfo, 0, len(entries))
		for _, e := range entries {
			info, err := e.Info()
			if err != nil {
				continue
			}
			infos = append(infos, info)
		}
		return listerat(infos), nil

	case "Stat":
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		return listerat{info}, nil

	case "Readlink":
		target, err := os.Readlink(p)
		if err != nil {
			return nil, err
		}
		return listerat{&fakeInfo{name: target}}, nil
	}

	return nil, fmt.Errorf("unsupported list method: %s", r.Method)
}

type listerat []os.FileInfo

func (l listerat) ListAt(ls []os.FileInfo, offset int64) (int, error) {
	if offset >= int64(len(l)) {
		return 0, io.EOF
	}
	n := copy(ls, l[offset:])
	if n < len(ls) {
		return n, io.EOF
	}
	return n, nil
}

type fakeInfo struct{ name string }

func (f *fakeInfo) Name() string      { return f.name }
func (f *fakeInfo) Size() int64       { return 0 }
func (f *fakeInfo) Mode() os.FileMode { return 0644 }
func (f *fakeInfo) ModTime() time.Time { return time.Time{} }
func (f *fakeInfo) IsDir() bool       { return false }
func (f *fakeInfo) Sys() any          { return nil }
