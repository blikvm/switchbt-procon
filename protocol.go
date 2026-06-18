package main

import (
	"errors"
	"log"
	"sync"
)

type controllerProtocol struct {
	mu         sync.Mutex
	input      SwitchProConInput
	reportMode byte
	timer      byte
	ready      bool
	flash      *flashMemory
	leftCal    stickCalibration
	rightCal   stickCalibration
	localAddr  [6]byte
	onReady    func()
	onPaired   func(string)
	lastPeer   string
	outputSeen bool
}

func newControllerProtocol(localAddr [6]byte, flash *flashMemory) *controllerProtocol {
	return &controllerProtocol{
		flash:     flash,
		leftCal:   leftCalibrationFromBytes(flash.slice(0x603D, 9)),
		rightCal:  rightCalibrationFromBytes(flash.slice(0x6046, 9)),
		localAddr: localAddr,
	}
}

// resetForReconnect clears the protocol state so the session can be reused
// after a reconnect. Calibration data and flash contents are preserved.
func (p *controllerProtocol) resetForReconnect() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reportMode = 0
	p.timer = 0
	p.ready = false
	p.outputSeen = false
}

func (p *controllerProtocol) setInput(in SwitchProConInput) {
	p.mu.Lock()
	p.input = in
	p.mu.Unlock()
}

func (p *controllerProtocol) markPeer(addr string) {
	p.mu.Lock()
	p.lastPeer = addr
	p.mu.Unlock()
	if p.onPaired != nil && addr != "" {
		p.onPaired(addr)
	}
}

func (p *controllerProtocol) currentStateReport() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()

	r := newInputReport()
	r.setReportID(p.reportMode)
	if p.reportMode == 0 {
		r.setReportID(0x30)
	}
	r.setTimer(p.timer)
	p.timer++
	r.setMisc()
	r.setVibration()
	r.set6AxisZero()
	b1, b2, b3 := buttonBytes(p.input)
	r.setButtonStatus(b1, b2, b3)
	r.setStickStatus(
		packStick(p.leftCal, p.input.Stick.Left.X, p.input.Stick.Left.Y),
		packStick(p.rightCal, p.input.Stick.Right.X, p.input.Stick.Right.Y),
	)
	return r.bytes()
}

func (p *controllerProtocol) emptyReport() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := newInputReport()
	r.setReportID(0x30)
	r.setTimer(p.timer)
	p.timer++
	r.setMisc()
	r.setVibration()
	r.set6AxisZero()
	r.setStickStatus(packStick(p.leftCal, 0, 0), packStick(p.rightCal, 0, 0))
	return r.bytes()
}

func (p *controllerProtocol) handle(data []byte) ([][]byte, error) {
	p.mu.Lock()
	p.outputSeen = true
	p.mu.Unlock()

	out := parseOutputReport(data)
	if out == nil {
		return nil, errors.New("invalid output report")
	}
	switch out.reportID() {
	case 0x01:
		return p.handleSubcommand(out)
	case 0x10:
		return nil, nil
	default:
		log.Printf("ignoring output report id=0x%02x", out.reportID())
		return nil, nil
	}
}

func (p *controllerProtocol) handleSubcommand(out *outputReport) ([][]byte, error) {
	sub := out.subcommand()
	data := out.subcommandData()
	log.Printf("handling subcommand 0x%02x payloadLen=%d", sub, len(data))

	reply := func(ack, subID byte) *inputReport {
		r := newInputReport()
		r.setReportID(0x21)
		p.mu.Lock()
		r.setTimer(p.timer)
		p.timer++
		p.mu.Unlock()
		r.setMisc()
		r.setVibration()
		b1, b2, b3 := buttonBytes(p.input)
		r.setButtonStatus(b1, b2, b3)
		r.setStickStatus(
			packStick(p.leftCal, p.input.Stick.Left.X, p.input.Stick.Left.Y),
			packStick(p.rightCal, p.input.Stick.Right.X, p.input.Stick.Right.Y),
		)
		r.setAck(ack)
		r.replyToSubcommand(subID)
		return r
	}

	switch sub {
	case 0x02:
		r := reply(0x82, 0x02)
		r.subDeviceInfo(p.localAddr)
		return [][]byte{r.bytes()}, nil
	case 0x03:
		if len(data) < 1 {
			return nil, errors.New("missing input report mode")
		}
		p.mu.Lock()
		p.reportMode = data[0]
		p.mu.Unlock()
		log.Printf("input report mode set to 0x%02x", data[0])
		r := reply(0x80, 0x03)
		return [][]byte{r.bytes()}, nil
	case 0x04:
		r := reply(0x83, 0x04)
		r.subTriggerButtonsElapsed()
		return [][]byte{r.bytes()}, nil
	case 0x08:
		r := reply(0x80, 0x08)
		return [][]byte{r.bytes()}, nil
	case 0x10:
		if len(data) < 5 {
			return nil, errors.New("invalid spi flash read request")
		}
		offset := int(data[0]) | int(data[1])<<8 | int(data[2])<<16 | int(data[3])<<24
		size := int(data[4])
		payload := p.flash.slice(offset, size)
		log.Printf("spi flash read: offset=0x%08x requested=%d actual=%d flashLen=%d", offset, size, len(payload), len(p.flash.data))
		r := reply(0x90, 0x10)
		r.subSPIFlashRead(offset, payload)
		out := r.bytes()
		log.Printf("spi flash reply: sent=%d bytes sizeByteInReport=%d", len(out), out[20])
		return [][]byte{out}, nil
	case 0x30:
		p.mu.Lock()
		wasReady := p.ready
		p.ready = true
		p.mu.Unlock()
		if !wasReady && p.onReady != nil {
			log.Printf("player lights acknowledged; controller ready")
			p.onReady()
		}
		r := reply(0x80, 0x30)
		return [][]byte{r.bytes()}, nil
	case 0x40:
		r := reply(0x80, 0x40)
		return [][]byte{r.bytes()}, nil
	case 0x48:
		r := reply(0x80, 0x48)
		return [][]byte{r.bytes()}, nil
	case 0x21:
		r := reply(0xA0, 0x21)
		r.subNFCIRMCUConfig()
		return [][]byte{r.bytes()}, nil
	case 0x22:
		if len(data) < 1 {
			return nil, errors.New("missing nfc ir mcu state")
		}
		switch data[0] {
		case 0x00, 0x01:
			r := reply(0x80, 0x22)
			return [][]byte{r.bytes()}, nil
		default:
			return nil, errors.New("unsupported nfc ir mcu state")
		}
	default:
		log.Printf("ignoring unimplemented subcommand 0x%02x", sub)
		return nil, nil
	}
}
