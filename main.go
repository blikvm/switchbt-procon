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
	var forcePair bool
	var adapter string
	var reconnectAddr string

	flag.StringVar(&socketPath, "socket", "/tmp/switchbt-procon.sock", "unix socket path for IPC")
	flag.StringVar(&statePath, "state", filepath.Join(os.TempDir(), "switchbt-procon-state.json"), "state file path")
	flag.BoolVar(&forcePair, "force-pair", false, "force enter pairing mode regardless of existing pairings")
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

	// Determine startup mode
	autoStart := !forcePair && reconnectAddr == "" && flag.NFlag() == 0

	if autoStart || forcePair || reconnectAddr != "" {
		req := startRequest{Adapter: adapter, ReconnectAddr: reconnectAddr}
		
		if autoStart {
			// Auto mode: check for paired Switch and reconnect if found
			log.Printf("auto-start mode: checking for paired Switch devices")
			btAdapter, err := openAdapter(adapter)
			if err != nil {
				log.Printf("failed to open adapter: %v", err)
			} else {
				switchAddr, err := btAdapter.FindPairedSwitch()
				_ = btAdapter.Close()
				if err == nil && switchAddr != "" {
					log.Printf("found paired Switch: %s", switchAddr)
					req.ReconnectAddr = switchAddr
				} else {
					log.Printf("no paired Switch found, entering pairing mode")
					req.ReconnectAddr = ""
				}
			}
		} else if forcePair {
			log.Printf("force-pair mode: entering pairing mode")
			req.ReconnectAddr = ""
		}

		if err := daemon.Start(req); err != nil {
			log.Fatalf("autostart failed: %v", err)
		}
	}

	log.Printf("switchbt-procon listening on unix://%s", socketPath)
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		log.Fatalf("serve: %v", err)
	}
}
