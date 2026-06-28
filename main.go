package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"fortihost-agent/api"
	"fortihost-agent/internal/config"
	"fortihost-agent/internal/sftp"
	"fortihost-agent/internal/store"
)

func main() {
	cfgPath := flag.String("config", "/etc/fortihost-agent/config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	if err := os.MkdirAll(cfg.DataDir, 0750); err != nil {
		log.Fatalf("data dir: %v", err)
	}

	db, err := store.New(cfg.DataDir + "/sites.json")
	if err != nil {
		log.Fatalf("store: %v", err)
	}

	httpSrv  := api.NewServer(cfg, db)
	sftpSrv  := sftp.NewServer(cfg, db)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	errCh := make(chan error, 2)

	go func() { errCh <- httpSrv.Start() }()
	go func() { errCh <- sftpSrv.Start() }()

	select {
	case <-ctx.Done():
		log.Println("shutting down...")
	case err := <-errCh:
		log.Fatalf("server error: %v", err)
	}
}
