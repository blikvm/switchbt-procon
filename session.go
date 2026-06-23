package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"runtime"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

type sessionConfig struct {
	adapter       string
	reconnectAddr string
}

type sessionCallbacks struct {
	onPairing   func(bool)
	onConnected func(bool, string)
	onReady     func(bool)
	onError     func(error)
	onPaired    func(string)
}

type receivedPacket struct {
	fd   int
	name string
	data []byte
}

type proControllerSession struct {
	cfg       sessionConfig
	cb        sessionCallbacks
	adapter   *btAdapter
	protocol  *controllerProtocol
	localAddr [6]byte

	mu              sync.Mutex
	input           SwitchProConInput
	ctlFD           int
	itrFD           int
	ctlLFD          int
	itrLFD          int
	stop            context.CancelFunc
	agentRegistered bool
}

func newSession(cfg sessionConfig, cb sessionCallbacks) (*proControllerSession, error) {
	adapter, err := openAdapter(cfg.adapter)
	if err != nil {
		return nil, err
	}
	addr, err := parseBTAddress(adapter.address)
	if err != nil {
		_ = adapter.Close()
		return nil, err
	}
	flash := newFlashMemory()
	proto := newControllerProtocol(addr, flash)
	proto.onReady = func() {
		if cb.onReady != nil {
			cb.onReady(true)
		}
	}
	proto.onPaired = func(addr string) {
		if cb.onPaired != nil {
			cb.onPaired(addr)
		}
	}
	return &proControllerSession{
		cfg:       cfg,
		cb:        cb,
		adapter:   adapter,
		protocol:  proto,
		localAddr: addr,
		ctlFD:     -1,
		itrFD:     -1,
		ctlLFD:    -1,
		itrLFD:    -1,
	}, nil
}

func (s *proControllerSession) Start() error {
	ctx, cancel := context.WithCancel(context.Background())
	s.stop = cancel

	if s.cfg.reconnectAddr != "" {
		log.Printf("starting reconnect session adapter=%s target=%s", s.cfg.adapter, s.cfg.reconnectAddr)
		if err := s.connectReconnect(); err != nil {
			cancel()
			return err
		}
		peer := s.cfg.reconnectAddr
		s.protocol.markPeer(peer)
		if s.cb.onConnected != nil {
			s.cb.onConnected(true, peer)
		}
		go s.run(ctx)
		return nil
	}

	if err := registerAgent(s.adapter.conn); err != nil {
		cancel()
		return err
	}
	s.agentRegistered = true
	log.Printf("registered NoInputNoOutput agent")

	log.Printf("starting pairing session adapter=%s", s.cfg.adapter)
	if err := s.preparePairing(); err != nil {
		cancel()
		return err
	}
	if s.cb.onPairing != nil {
		s.cb.onPairing(true)
	}
	go s.acceptAndRun(ctx)
	return nil
}

func (s *proControllerSession) preparePairing() error {
	var err error
	s.ctlLFD, err = listenL2CAP(s.localAddr, 17)
	if err != nil {
		return err
	}
	log.Printf("listening on L2CAP control PSM 17")
	s.itrLFD, err = listenL2CAP(s.localAddr, 19)
	if err != nil {
		_ = unix.Close(s.ctlLFD)
		s.ctlLFD = -1
		return err
	}
	log.Printf("listening on L2CAP interrupt PSM 19")
	if err := s.adapter.Powered(true); err != nil {
		return err
	}
	if err := s.adapter.Pairable(true); err != nil {
		return err
	}
	if err := s.adapter.Alias("Pro Controller"); err != nil {
		return err
	}
	_ = s.adapter.SetClass("0x002508")
	if err := s.adapter.RegisterProfile(); err != nil {
		return err
	}
	log.Printf("registered HID profile on adapter %s (%s)", s.adapter.name, s.adapter.address)
	if err := s.adapter.Discoverable(true); err != nil {
		return err
	}
	log.Printf("adapter discoverable and pairable; waiting for Switch in Change Grip/Order")
	return nil
}

func (s *proControllerSession) connectReconnect() error {
	remote, err := parseBTAddress(s.cfg.reconnectAddr)
	if err != nil {
		return err
	}
	s.ctlFD, err = connectL2CAP(remote, 17)
	if err != nil {
		return err
	}
	log.Printf("connected L2CAP control PSM 17 to %s", s.cfg.reconnectAddr)
	s.itrFD, err = connectL2CAP(remote, 19)
	if err != nil {
		return err
	}
	log.Printf("connected L2CAP interrupt PSM 19 to %s", s.cfg.reconnectAddr)
	return nil
}

func (s *proControllerSession) acceptAndRun(ctx context.Context) {
	defer s.cleanup()

	ctlFD, ctlPeer, err := acceptL2CAP(s.ctlLFD)
	if err != nil {
		s.fail(err)
		return
	}
	s.ctlFD = ctlFD
	log.Printf("accepted control channel from %s", ctlPeer)
	itrFD, itrPeer, err := acceptL2CAP(s.itrLFD)
	if err != nil {
		s.fail(err)
		return
	}
	s.itrFD = itrFD
	log.Printf("accepted interrupt channel from %s", itrPeer)
	if s.cb.onPairing != nil {
		s.cb.onPairing(false)
	}
	_ = s.adapter.Discoverable(false)
	_ = s.adapter.Pairable(false)

	peer := itrPeer
	if peer == "" {
		peer = ctlPeer
	}
	log.Printf("switch connected peer=%s", peer)
	if err := s.adapter.TrustDevice(peer); err != nil {
		log.Printf("failed to trust device: %v", err)
	} else {
		log.Printf("trusted device %s", peer)
	}
	s.protocol.markPeer(peer)
	if s.cb.onConnected != nil {
		s.cb.onConnected(true, peer)
	}
	s.run(ctx)

	// 当前这里还有bug，虚拟出来的蓝牙首次配对连接完后，当退出"Change Grip/Order"后，手柄会自动掉线，
	//目前是先用再次重连兜底，后续再排查。
	if peer == "" {
		return
	}
	log.Printf("pairing session ended, auto-reconnecting to %s", peer)
	s.closeChannels()
	s.protocol.resetForReconnect()
	time.Sleep(500 * time.Millisecond)
	s.cfg.reconnectAddr = peer
	if err := s.connectReconnect(); err != nil {
		log.Printf("auto-reconnect failed: %v", err)
		s.fail(err)
		return
	}
	log.Printf("auto-reconnected to %s", peer)
	if s.cb.onConnected != nil {
		s.cb.onConnected(true, peer)
	}
	s.run(ctx)
}

func (s *proControllerSession) run(ctx context.Context) {
	// Lock this goroutine to an OS thread to prevent Go runtime from
	// interrupting system calls with SIGPROF signals during scheduling
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	defer s.closeChannels()
	defer func() {
		if s.cb.onConnected != nil {
			s.cb.onConnected(false, "")
		}
		if s.cb.onReady != nil {
			s.cb.onReady(false)
		}
	}()

	readCh := make(chan receivedPacket, 16)
	errCh := make(chan error, 1)

	// The Switch sends output reports (subcommands) on the control channel
	// (PSM 17) AND the interrupt channel (PSM 19). If we only read from the
	// interrupt channel, the control channel's receive buffer fills up and
	// the Switch blocks, unable to process our replies. Read from both.
	var readWG sync.WaitGroup
	readWG.Add(2)
	go func() {
		defer readWG.Done()
		s.readLoopFD(s.ctlFD, "control", readCh)
	}()
	go func() {
		defer readWG.Done()
		s.readLoopFD(s.itrFD, "interrupt", readCh)
	}()
	go func() {
		readWG.Wait()
		select {
		case errCh <- errors.New("bluetooth channels closed"):
		default:
		}
	}()

	emptyTicker := time.NewTicker(1 * time.Second)
	defer emptyTicker.Stop()
	stateTicker := time.NewTicker(15 * time.Millisecond)
	defer stateTicker.Stop()
	sentWakeups := 0
	reconnectMode := s.cfg.reconnectAddr != ""

	// Track the last time we received a subcommand. State reports (0x30)
	// are paused for a short window after each subcommand so the reply
	// doesn't get stuck behind a flood of 14-byte state reports in the
	// L2CAP send buffer.
	lastSubcmdTime := time.Time{}
	const subcmdPause = 200 * time.Millisecond

	// Pairing mode expects an immediate 0x30 wakeup. Bonded reconnects
	// are more sensitive: match joycontrol by sending a neutral 51-byte
	// HID input report until the Switch starts talking.
	if reconnectMode {
		if err := writeAll(s.itrFD, s.protocol.reconnectWakeupReport()); err != nil {
			s.fail(err)
			return
		}
		sentWakeups = 1
		log.Printf("sent reconnect wakeup %d", sentWakeups)
	} else {
		if err := writeAll(s.itrFD, s.protocol.emptyReport()); err != nil {
			s.fail(err)
			return
		}
		sentWakeups = 1
		log.Printf("sent initial empty input report")
	}

	for {
		select {
		case <-ctx.Done():
			return
		case err := <-errCh:
			if !errors.Is(err, unix.EBADF) {
				log.Printf("read loop exiting: %v", err)
				s.fail(err)
			}
			return
		case pkt := <-readCh:
			// log.Printf("rx on %s channel: %s", pkt.name, outputReportSummary(pkt.data))
			replies, err := s.protocol.handle(pkt.data)
			if err != nil {
				log.Printf("protocol handle: %v", err)
				continue
			}
			// Record the time so state reports are paused while the
			// Switch processes our reply.
			lastSubcmdTime = time.Now()
			for _, reply := range replies {
				// Send subcommand replies on the SAME channel the request
				// came from. The Switch expects replies on the control
				// channel when it sends the subcommand via SET_REPORT on
				// PSM 17, and on the interrupt channel otherwise.
				if err := writeAll(pkt.fd, reply); err != nil {
					s.fail(err)
					return
				}
				hexDump := make([]byte, len(reply))
				copy(hexDump, reply)
				log.Printf("sent reply: %d bytes on %s channel: % x", len(reply), pkt.name, hexDump[:min(20, len(hexDump))])
			}
		case <-emptyTicker.C:
			if sentWakeups >= 10 || s.protocol.outputSeen {
				continue
			}
			var wakeup []byte
			if reconnectMode {
				wakeup = s.protocol.reconnectWakeupReport()
			} else {
				wakeup = s.protocol.emptyReport()
			}
			if err := writeAll(s.itrFD, wakeup); err != nil {
				s.fail(err)
				return
			}
			sentWakeups++
			if reconnectMode {
				log.Printf("sent reconnect wakeup %d", sentWakeups)
			} else {
				log.Printf("sent wakeup %d", sentWakeups)
			}
		case <-stateTicker.C:
			// Wait until SET_INPUT_REPORT_MODE (subcommand 0x03) has been
			// received before streaming state reports. The Switch does not
			// accept continuous 0x30 reports during the initial pairing
			// handshake, and will close the interrupt channel if it sees
			// them before the report mode has been negotiated.
			if s.protocol.reportMode == 0 {
				continue
			}
			// Pause state reports briefly after each subcommand to avoid
			// flooding the Switch while it processes our reply.
			if time.Since(lastSubcmdTime) < subcmdPause {
				continue
			}
			if err := writeAll(s.itrFD, s.protocol.currentStateReport()); err != nil {
				log.Printf("state report write failed: %v", err)
				// Don't fail immediately - try to recover
				continue
			}
		}
	}
}

func (s *proControllerSession) readLoopFD(fd int, name string, out chan<- receivedPacket) {
	// Lock this goroutine to an OS thread to prevent Go runtime from
	// interrupting system calls with SIGPROF signals during scheduling
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	buf := make([]byte, 1024)
	for {
		n, err := unix.Read(fd, buf)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				log.Printf("%s channel read interrupted by signal (EINTR), retrying", name)
				continue
			}
			log.Printf("%s channel read error: %v", name, err)
			return
		}
		if n == 0 {
			log.Printf("%s channel closed", name)
			return
		}
		pkt := make([]byte, n)
		copy(pkt, buf[:n])
		out <- receivedPacket{fd: fd, name: name, data: pkt}
	}
}

func (s *proControllerSession) readLoop(out chan<- []byte, errCh chan<- error) {
	// Lock this goroutine to an OS thread to prevent Go runtime from
	// interrupting system calls with SIGPROF signals during scheduling
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	buf := make([]byte, 1024)
	for {
		n, err := unix.Read(s.itrFD, buf)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				log.Printf("read interrupted by signal (EINTR), retrying")
				continue
			}
			errCh <- fmt.Errorf("read error: %w", err)
			return
		}
		if n == 0 {
			errCh <- errors.New("bluetooth interrupt channel closed")
			return
		}
		pkt := make([]byte, n)
		copy(pkt, buf[:n])
		out <- pkt
	}
}

func (s *proControllerSession) SetInput(in SwitchProConInput) {
	s.protocol.setInput(in)
}

func (s *proControllerSession) Close() error {
	if s.stop != nil {
		s.stop()
	}
	s.cleanup()
	return nil
}

func (s *proControllerSession) closeChannels() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, fd := range []*int{&s.ctlFD, &s.itrFD, &s.ctlLFD, &s.itrLFD} {
		if *fd >= 0 {
			_ = unix.Close(*fd)
			*fd = -1
		}
	}
}

func (s *proControllerSession) cleanup() {
	s.closeChannels()
	if s.adapter != nil {
		_ = s.adapter.Discoverable(false)
		_ = s.adapter.Pairable(false)
		_ = s.adapter.UnregisterProfile()
		if s.agentRegistered {
			unregisterAgent(s.adapter.conn)
			s.agentRegistered = false
		}
		_ = s.adapter.Close()
		s.adapter = nil
	}
}

func (s *proControllerSession) fail(err error) {
	log.Printf("session error: %v", err)
	if s.cb.onError != nil {
		s.cb.onError(err)
	}
}

func listenL2CAP(local [6]byte, psm uint16) (int, error) {
	fd, err := unix.Socket(unix.AF_BLUETOOTH, unix.SOCK_SEQPACKET, unix.BTPROTO_L2CAP)
	if err != nil {
		return -1, err
	}
	_ = unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_REUSEADDR, 1)
	if err := unix.Bind(fd, &unix.SockaddrL2{PSM: psm, Addr: local}); err != nil {
		_ = unix.Close(fd)
		return -1, err
	}
	if err := unix.Listen(fd, 1); err != nil {
		_ = unix.Close(fd)
		return -1, err
	}
	return fd, nil
}

func connectL2CAP(remote [6]byte, psm uint16) (int, error) {
	fd, err := unix.Socket(unix.AF_BLUETOOTH, unix.SOCK_SEQPACKET, unix.BTPROTO_L2CAP)
	if err != nil {
		return -1, err
	}
	if err := unix.Connect(fd, &unix.SockaddrL2{PSM: psm, Addr: remote}); err != nil {
		_ = unix.Close(fd)
		return -1, err
	}
	return fd, nil
}

func acceptL2CAP(fd int) (int, string, error) {
	nfd, sa, err := unix.Accept(fd)
	if err != nil {
		return -1, "", err
	}
	if l2, ok := sa.(*unix.SockaddrL2); ok {
		return nfd, formatBTAddress(l2.Addr), nil
	}
	return nfd, "", nil
}

func writeAll(fd int, data []byte) error {
	for len(data) > 0 {
		n, err := unix.Write(fd, data)
		if err != nil {
			// EINTR means the write was interrupted by a signal, retry
			if errors.Is(err, unix.EINTR) {
				log.Printf("write interrupted by signal, retrying")
				continue
			}
			return fmt.Errorf("write error: %w", err)
		}
		data = data[n:]
	}
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func outputReportSummary(data []byte) string {
	if len(data) < 2 || data[0] != 0xA2 {
		return fmt.Sprintf("invalid-output len=%d", len(data))
	}
	id := data[1]
	if id == 0x01 && len(data) > 11 {
		return fmt.Sprintf("output id=0x%02x len=%d sub=0x%02x", id, len(data), data[11])
	}
	return fmt.Sprintf("output id=0x%02x len=%d", id, len(data))
}
