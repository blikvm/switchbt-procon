package main

import "math"

type inputReport struct {
	data []byte
}

func newInputReport() *inputReport {
	data := make([]byte, 364)
	data[0] = 0xA1
	return &inputReport{data: data}
}

func (r *inputReport) setReportID(id byte) { r.data[1] = id }
func (r *inputReport) setTimer(timer byte) { r.data[2] = timer }
func (r *inputReport) setMisc()            { r.data[3] = 0x8E }
func (r *inputReport) setAck(ack byte)     { r.data[14] = ack }
func (r *inputReport) replyToSubcommand(id byte) {
	r.data[15] = id
}
func (r *inputReport) setVibration() { r.data[13] = 0x80 }
func (r *inputReport) set6AxisZero() {
	for i := 14; i < 50; i++ {
		r.data[i] = 0
	}
}
func (r *inputReport) setButtonStatus(b1, b2, b3 byte) {
	r.data[4] = b1
	r.data[5] = b2
	r.data[6] = b3
}
func (r *inputReport) setStickStatus(left, right [3]byte) {
	copy(r.data[7:10], left[:])
	copy(r.data[10:13], right[:])
}
func (r *inputReport) subDeviceInfo(mac [6]byte) {
	offset := 16
	r.data[offset] = 0x04
	r.data[offset+1] = 0x00
	r.data[offset+2] = 0x03
	r.data[offset+3] = 0x02
	copy(r.data[offset+4:offset+10], mac[:])
	r.data[offset+10] = 0x01
	r.data[offset+11] = 0x01
}
func (r *inputReport) subSPIFlashRead(offset int, payload []byte) {
	r.data[16] = byte(offset)
	r.data[17] = byte(offset >> 8)
	r.data[18] = byte(offset >> 16)
	r.data[19] = byte(offset >> 24)
	r.data[20] = byte(len(payload))
	copy(r.data[21:], payload)
}
func (r *inputReport) subTriggerButtonsElapsed() {
	r.data[16] = 0x2c
	r.data[17] = 0x01
	r.data[18] = 0x2c
	r.data[19] = 0x01
}
func (r *inputReport) subNFCIRMCUConfig() {
	data := []byte{
		0x01, 0x00, 0xff, 0x00, 0x08, 0x00, 0x1b, 0x01,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0xc8,
	}
	copy(r.data[16:], data)
}
func (r *inputReport) bytes() []byte {
	switch r.data[1] {
	case 0x30:
		return append([]byte(nil), r.data[:14]...)
	case 0x31:
		return append([]byte(nil), r.data[:363]...)
	case 0x21:
		// All subcommand replies (report id 0x21) are 51 bytes on the
		// wire. The Switch waits for the full 51 bytes before it will
		// ack the corresponding output subcommand - sending 49 bytes
		// causes it to keep retrying 0x21, 0x30, 0x40, 0x48 etc.
		return append([]byte(nil), r.data[:51]...)
	default:
		return append([]byte(nil), r.data[:49]...)
	}
}

type outputReport struct {
	data []byte
}

func parseOutputReport(data []byte) *outputReport {
	if len(data) < 2 || data[0] != 0xA2 {
		return nil
	}
	buf := make([]byte, len(data))
	copy(buf, data)
	return &outputReport{data: buf}
}

func (r *outputReport) reportID() byte {
	if len(r.data) < 2 {
		return 0
	}
	return r.data[1]
}

func (r *outputReport) subcommand() byte {
	if len(r.data) < 12 {
		return 0
	}
	return r.data[11]
}

func (r *outputReport) subcommandData() []byte {
	if len(r.data) < 12 {
		return nil
	}
	return r.data[12:]
}

func buttonBytes(in SwitchProConInput) (byte, byte, byte) {
	set := func(v uint8, bit uint) byte {
		if v == 0 {
			return 0
		}
		return 1 << bit
	}
	b1 := set(in.Button.Y, 0) |
		set(in.Button.X, 1) |
		set(in.Button.B, 2) |
		set(in.Button.A, 3) |
		set(in.Button.R, 6) |
		set(in.Button.ZR, 7)
	b2 := set(in.Button.Minus, 0) |
		set(in.Button.Plus, 1) |
		set(in.Stick.Right.Press, 2) |
		set(in.Stick.Left.Press, 3) |
		set(in.Button.Home, 4) |
		set(in.Button.Capture, 5)
	b3 := set(in.Dpad.Down, 0) |
		set(in.Dpad.Up, 1) |
		set(in.Dpad.Right, 2) |
		set(in.Dpad.Left, 3) |
		set(in.Button.L, 6) |
		set(in.Button.ZL, 7)
	return b1, b2, b3
}

func clamp(v float64) float64 {
	if v < -1 {
		return -1
	}
	if v > 1 {
		return 1
	}
	return v
}

func packStick(cal stickCalibration, x, y float64) [3]byte {
	x = clamp(x)
	y = clamp(y)

	h := cal.hCenter
	v := cal.vCenter
	if x >= 0 {
		h += int(math.Round(x * float64(cal.hMaxAboveCenter)))
	} else {
		h += int(math.Round(x * float64(cal.hMaxBelowCenter)))
	}
	if y >= 0 {
		v += int(math.Round(y * float64(cal.vMaxAboveCenter)))
	} else {
		v += int(math.Round(y * float64(cal.vMaxBelowCenter)))
	}
	if h < 0 {
		h = 0
	}
	if h > 0xFFF {
		h = 0xFFF
	}
	if v < 0 {
		v = 0
	}
	if v > 0xFFF {
		v = 0xFFF
	}
	return [3]byte{
		byte(h & 0xFF),
		byte((h>>8)&0x0F | ((v & 0x0F) << 4)),
		byte((v >> 4) & 0xFF),
	}
}
