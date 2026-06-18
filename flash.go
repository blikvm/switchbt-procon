package main

type flashMemory struct {
	data []byte
}

func newFlashMemory() *flashMemory {
	data := make([]byte, 0x80000)
	for i := range data {
		data[i] = 0xFF
	}
	copy(data[0x603D:0x6046], []byte{0x00, 0x07, 0x70, 0x00, 0x08, 0x80, 0x00, 0x07, 0x70})
	copy(data[0x6046:0x604F], []byte{0x00, 0x08, 0x80, 0x00, 0x07, 0x70, 0x00, 0x07, 0x70})
	return &flashMemory{data: data}
}

func (f *flashMemory) slice(offset, size int) []byte {
	if offset < 0 {
		offset = 0
	}
	if size < 0 {
		size = 0
	}
	if offset > len(f.data) {
		offset = len(f.data)
	}
	end := offset + size
	if end > len(f.data) {
		end = len(f.data)
	}
	out := make([]byte, end-offset)
	copy(out, f.data[offset:end])
	return out
}

type stickCalibration struct {
	hCenter         int
	vCenter         int
	hMaxAboveCenter int
	vMaxAboveCenter int
	hMaxBelowCenter int
	vMaxBelowCenter int
}

func leftCalibrationFromBytes(b []byte) stickCalibration {
	return stickCalibration{
		hMaxAboveCenter: int((uint16(b[1])<<8)&0xF00 | uint16(b[0])),
		vMaxAboveCenter: int((uint16(b[2]) << 4) | (uint16(b[1]) >> 4)),
		hCenter:         int((uint16(b[4])<<8)&0xF00 | uint16(b[3])),
		vCenter:         int((uint16(b[5]) << 4) | (uint16(b[4]) >> 4)),
		hMaxBelowCenter: int((uint16(b[7])<<8)&0xF00 | uint16(b[6])),
		vMaxBelowCenter: int((uint16(b[8]) << 4) | (uint16(b[7]) >> 4)),
	}
}

func rightCalibrationFromBytes(b []byte) stickCalibration {
	return stickCalibration{
		hCenter:         int((uint16(b[1])<<8)&0xF00 | uint16(b[0])),
		vCenter:         int((uint16(b[2]) << 4) | (uint16(b[1]) >> 4)),
		hMaxBelowCenter: int((uint16(b[4])<<8)&0xF00 | uint16(b[3])),
		vMaxBelowCenter: int((uint16(b[5]) << 4) | (uint16(b[4]) >> 4)),
		hMaxAboveCenter: int((uint16(b[7])<<8)&0xF00 | uint16(b[6])),
		vMaxAboveCenter: int((uint16(b[8]) << 4) | (uint16(b[7]) >> 4)),
	}
}
