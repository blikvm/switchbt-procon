package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	var socketPath string
	var statePath string
	var autoPair bool
	var adapter string
	var reconnectAddr string

	flag.StringVar(&socketPath, "socket", "/tmp/switchbt-procon.sock", "unix socket path for IPC")
	flag.StringVar(&statePath, "state", filepath.Join(os.TempDir(), "switchbt-procon-state.json"), "state file path")
	flag.BoolVar(&autoPair, "auto-pair", false, "automatically start pairing on launch")
	flag.StringVar(&adapter, "adapter", "hci0", "bluetooth adapter name or address")
	flag.StringVar(&reconnectAddr, "reconnect", "", "reconnect to a previously paired Switch bluetooth address")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	daemon, err := NewDaemon(statePath)
	if err != nil {
		log.Fatalf("create daemon: %v", err)
	}
	defer daemon.Close()

	if err := os.RemoveAll(socketPath); err != nil {
		log.Fatalf("cleanup socket: %v", err)
	}
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		log.Fatalf("listen on %s: %v", socketPath, err)
	}
	defer func() {
		_ = ln.Close()
		_ = os.Remove(socketPath)
	}()
	if err := os.Chmod(socketPath, 0o666); err != nil {
		log.Fatalf("chmod socket: %v", err)
	}

	srv := &http.Server{
		Handler:      daemon.Routes(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	if autoPair || reconnectAddr != "" {
		if err := daemon.Start(startRequest{Adapter: adapter, ReconnectAddr: reconnectAddr}); err != nil {
			log.Fatalf("autostart failed: %v", err)
		}
	}

	log.Printf("switchbt-procon listening on unix://%s", socketPath)
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		log.Fatalf("serve: %v", err)
	}
}
