package main

import (
	"context"
	"errors"
	"fmt"
	"log"
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

type proControllerSession struct {
	cfg       sessionConfig
	cb        sessionCallbacks
	adapter   *btAdapter
	protocol  *controllerProtocol
	localAddr [6]byte

	mu     sync.Mutex
	input  SwitchProConInput
	ctlFD  int
	itrFD  int
	ctlLFD int
	itrLFD int
	stop   context.CancelFunc
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

	if err := registerAgent(s.adapter.conn); err != nil {
		cancel()
		return err
	}
	log.Printf("registered NoInputNoOutput agent")

	if s.cfg.reconnectAddr != "" {
		log.Printf("starting reconnect session adapter=%s target=%s", s.cfg.adapter, s.cfg.reconnectAddr)
		if err := s.connectReconnect(); err != nil {
			cancel()
			return err
		}
		peer := s.cfg.reconnectAddr
		if err := s.adapter.TrustDevice(peer); err != nil {
			log.Printf("failed to trust device: %v", err)
		} else {
			log.Printf("trusted device %s", peer)
		}
		s.protocol.markPeer(peer)
		if s.cb.onConnected != nil {
			s.cb.onConnected(true, peer)
		}
		go s.run(ctx)
		return nil
	}

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
}

func (s *proControllerSession) run(ctx context.Context) {
	defer s.cleanup()
	defer func() {
		if s.cb.onConnected != nil {
			s.cb.onConnected(false, "")
		}
		if s.cb.onReady != nil {
			s.cb.onReady(false)
		}
	}()

	readCh := make(chan []byte, 8)
	errCh := make(chan error, 1)
	go s.readLoop(readCh, errCh)

	emptyTicker := time.NewTicker(1 * time.Second)
	defer emptyTicker.Stop()
	stateTicker := time.NewTicker(15 * time.Millisecond)
	defer stateTicker.Stop()
	sentWakeups := 0

	if err := writeAll(s.itrFD, s.protocol.emptyReport()); err != nil {
		s.fail(err)
		return
	}
	sentWakeups = 1
	log.Printf("sent initial empty input report")

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
		case data := <-readCh:
			replies, err := s.protocol.handle(data)
			if err != nil {
				log.Printf("protocol handle: %v", err)
				continue
			}
			for _, reply := range replies {
				if err := writeAll(s.itrFD, reply); err != nil {
					s.fail(err)
					return
				}
			}
		case <-emptyTicker.C:
			if sentWakeups >= 10 || s.protocol.outputSeen {
				continue
			}
			if err := writeAll(s.itrFD, s.protocol.emptyReport()); err != nil {
				s.fail(err)
				return
			}
			sentWakeups++
			log.Printf("sent wakeup empty input report #%d", sentWakeups)
		case <-stateTicker.C:
			if s.protocol.reportMode == 0 {
				continue
			}
			if err := writeAll(s.itrFD, s.protocol.currentStateReport()); err != nil {
				s.fail(err)
				return
			}
		}
	}
}

func (s *proControllerSession) readLoop(out chan<- []byte, errCh chan<- error) {
	buf := make([]byte, 1024)
	for {
		n, err := unix.Read(s.itrFD, buf)
		if err != nil {
			errCh <- err
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

func (s *proControllerSession) cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, fd := range []*int{&s.ctlFD, &s.itrFD, &s.ctlLFD, &s.itrLFD} {
		if *fd >= 0 {
			_ = unix.Close(*fd)
			*fd = -1
		}
	}
	if s.adapter != nil {
		_ = s.adapter.Discoverable(false)
		_ = s.adapter.Pairable(false)
		_ = s.adapter.UnregisterProfile()
		unregisterAgent(s.adapter.conn)
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

func writeAll(fd int, data []byte) error {
	for len(data) > 0 {
		n, err := unix.Write(fd, data)
		if err != nil {
			return err
		}
		data = data[n:]
	}
	return nil
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
