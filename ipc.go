package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

type Daemon struct {
	mu        sync.Mutex
	statePath string
	state     daemonState
	session   *proControllerSession
}

func NewDaemon(statePath string) (*Daemon, error) {
	d := &Daemon{
		statePath: statePath,
		state: daemonState{
			Mode:        "idle",
			ProfilePath: profilePath,
		},
	}
	d.loadState()
	return d, nil
}

func (d *Daemon) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.session != nil {
		_ = d.session.Close()
		d.session = nil
	}
	return nil
}

func (d *Daemon) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", d.handleStatus)
	mux.HandleFunc("/start", d.handleStart)
	mux.HandleFunc("/stop", d.handleStop)
	mux.HandleFunc("/input", d.handleInput)
	return mux
}

func (d *Daemon) handleStatus(w http.ResponseWriter, _ *http.Request) {
	d.mu.Lock()
	state := d.state
	d.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "data": state})
}

func (d *Daemon) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()
	var req startRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	if err := d.Start(req); err != nil {
		d.mu.Lock()
		state := d.state
		d.mu.Unlock()
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error(), "data": state})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (d *Daemon) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	d.mu.Lock()
	if d.session != nil {
		_ = d.session.Close()
		d.session = nil
	}
	d.state.Running = false
	d.state.Connected = false
	d.state.Pairing = false
	d.state.Ready = false
	d.state.Mode = "idle"
	d.state.PeerAddr = ""
	d.mu.Unlock()
	d.persistState()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (d *Daemon) handleInput(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		log.Printf("ipc /input: read body failed: %v", err)
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if os.Getenv("PROCON_DEBUG") == "1" {
		log.Printf("ipc /input: received %d bytes: %s", len(body), truncate(string(body), 200))
	}
	var env inputEnvelope
	if err := json.Unmarshal(body, &env); err == nil && env.GP != nil {
		d.mu.Lock()
		sess := d.session
		d.mu.Unlock()
		if sess == nil {
			log.Printf("ipc /input: session not running, dropping input")
			writeJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "session not running"})
			return
		}
		sess.SetInput(*env.GP)
		if os.Getenv("PROCON_DEBUG") == "1" {
			log.Printf("ipc /input: applied gp input: %s", summarizeProConInput(*env.GP))
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}

	var direct SwitchProConInput
	if err := json.Unmarshal(body, &direct); err != nil {
		log.Printf("ipc /input: invalid json: %v", err)
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	d.mu.Lock()
	sess := d.session
	d.mu.Unlock()
	if sess == nil {
		log.Printf("ipc /input: session not running (direct), dropping input")
		writeJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "session not running"})
		return
	}
	sess.SetInput(direct)
	if os.Getenv("PROCON_DEBUG") == "1" {
		log.Printf("ipc /input: applied direct input: %s", summarizeProConInput(direct))
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// truncate caps a string at n bytes for logging.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// summarizeProConInput returns a short human-readable summary of an input
// frame for diagnostic logging.
func summarizeProConInput(in SwitchProConInput) string {
	return fmt.Sprintf("dpad(u=%d d=%d l=%d r=%d) btn(a=%d b=%d x=%d y=%d l=%d r=%d zl=%d zr=%d plus=%d minus=%d home=%d cap=%d) stick(l=%.2f,%.2f r=%.2f,%.2f)",
		in.Dpad.Up, in.Dpad.Down, in.Dpad.Left, in.Dpad.Right,
		in.Button.A, in.Button.B, in.Button.X, in.Button.Y,
		in.Button.L, in.Button.R, in.Button.ZL, in.Button.ZR,
		in.Button.Plus, in.Button.Minus, in.Button.Home, in.Button.Capture,
		in.Stick.Left.X, in.Stick.Left.Y, in.Stick.Right.X, in.Stick.Right.Y)
}

func (d *Daemon) setState(fn func(*daemonState)) {
	d.mu.Lock()
	fn(&d.state)
	state := d.state
	d.mu.Unlock()
	if state.PairedSwitch != "" {
		d.persistState()
	}
}

func (d *Daemon) persistState() {
	d.mu.Lock()
	state := d.state
	d.mu.Unlock()
	b, _ := json.MarshalIndent(state, "", "  ")
	_ = os.WriteFile(d.statePath, b, 0o600)
}

func (d *Daemon) loadState() {
	b, err := os.ReadFile(d.statePath)
	if err != nil {
		return
	}
	var state daemonState
	if json.Unmarshal(b, &state) == nil && state.PairedSwitch != "" {
		d.state.PairedSwitch = state.PairedSwitch
	}
}

func (d *Daemon) Start(req startRequest) error {
	d.mu.Lock()
	if d.session != nil {
		_ = d.session.Close()
		d.session = nil
	}
	d.state = daemonState{
		Running:       true,
		Mode:          "starting",
		Adapter:       req.Adapter,
		ReconnectAddr: req.ReconnectAddr,
		ProfilePath:   profilePath,
		StartedAt:     time.Now().UTC().Format(time.RFC3339),
		PairedSwitch:  d.state.PairedSwitch,
	}

	// Track reconnect attempts for backoff
	reconnectAttempts := 0
	maxReconnectAttempts := 30 // ~5 minutes total with backoff
	lastReconnectTime := time.Time{}

	cb := sessionCallbacks{
		onPairing: func(v bool) {
			d.setState(func(s *daemonState) {
				s.Pairing = v
				if v {
					s.Mode = "pairing"
				}
			})
		},
		onConnected: func(v bool, peer string) {
			d.setState(func(s *daemonState) {
				s.Connected = v
				s.PeerAddr = peer
				if v {
					s.Mode = "connected"
					reconnectAttempts = 0 // Reset on successful connection
				} else if s.Running {
					s.Mode = "idle"
				}
			})

			// Auto-reconnect when connection is lost and we have a paired device
			if !v && peer == "" {
				d.mu.Lock()
				pairedSwitch := d.state.PairedSwitch
				running := d.state.Running
				d.mu.Unlock()

				if pairedSwitch != "" && running {
					// Rate limiting: minimum 10 seconds between reconnection attempts
					if time.Since(lastReconnectTime) < 10*time.Second {
						return
					}

					reconnectAttempts++
					if reconnectAttempts > maxReconnectAttempts {
						log.Printf("max reconnect attempts (%d) reached, stopping", maxReconnectAttempts)
						return
					}

					// Exponential backoff: 10s, 20s, 40s, up to 5 minutes
					backoff := time.Duration(min(10*(1<<reconnectAttempts), 300)) * time.Second
					lastReconnectTime = time.Now()

					log.Printf("connection lost, scheduling reconnect attempt %d/%d to %s in %v",
						reconnectAttempts, maxReconnectAttempts, pairedSwitch, backoff)

					time.AfterFunc(backoff, func() {
						d.mu.Lock()
						currentRunning := d.state.Running
						d.mu.Unlock()

						if currentRunning {
							log.Printf("attempting reconnect to %s", pairedSwitch)
							_ = d.Start(startRequest{
								Adapter:       req.Adapter,
								ReconnectAddr: pairedSwitch,
							})
						}
					})
				}
			}
		},
		onReady: func(v bool) {
			d.setState(func(s *daemonState) {
				s.Ready = v
			})
		},
		onError: func(err error) {
			d.setState(func(s *daemonState) {
				s.LastError = err.Error()
				s.Mode = "error"
			})
		},
		onPaired: func(addr string) {
			d.setState(func(s *daemonState) {
				s.PairedSwitch = addr
			})
		},
	}
	sess, err := newSession(sessionConfig{adapter: req.Adapter, reconnectAddr: req.ReconnectAddr}, cb)
	if err != nil {
		d.state.Running = false
		d.state.Mode = "error"
		d.state.LastError = err.Error()
		d.mu.Unlock()
		return err
	}
	d.state.AdapterAddr = sess.adapter.address
	d.session = sess
	d.mu.Unlock()

	if err := sess.Start(); err != nil {
		_ = sess.Close()
		d.mu.Lock()
		d.state.Running = false
		d.state.Mode = "error"
		d.state.LastError = err.Error()
		d.session = nil
		d.mu.Unlock()
		return err
	}

	d.setState(func(s *daemonState) {
		if s.ReconnectAddr != "" {
			s.Mode = "connected"
		} else {
			s.Mode = "pairing"
			s.Pairing = true
		}
	})
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
