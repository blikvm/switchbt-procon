package main

type SwitchProConInput struct {
	Dpad struct {
		Up    uint8 `json:"up"`
		Down  uint8 `json:"down"`
		Left  uint8 `json:"left"`
		Right uint8 `json:"right"`
	} `json:"dpad"`
	Button struct {
		A       uint8 `json:"a"`
		B       uint8 `json:"b"`
		X       uint8 `json:"x"`
		Y       uint8 `json:"y"`
		R       uint8 `json:"r"`
		ZR      uint8 `json:"zr"`
		L       uint8 `json:"l"`
		ZL      uint8 `json:"zl"`
		Home    uint8 `json:"home"`
		Plus    uint8 `json:"plus"`
		Minus   uint8 `json:"minus"`
		Capture uint8 `json:"capture"`
	} `json:"button"`
	Stick struct {
		Left struct {
			X     float64 `json:"x"`
			Y     float64 `json:"y"`
			Press uint8   `json:"press"`
		} `json:"left"`
		Right struct {
			X     float64 `json:"x"`
			Y     float64 `json:"y"`
			Press uint8   `json:"press"`
		} `json:"right"`
	} `json:"stick"`
}

type startRequest struct {
	Adapter       string `json:"adapter"`
	ReconnectAddr string `json:"reconnectAddr"`
}

type inputEnvelope struct {
	GP *SwitchProConInput `json:"gp"`
}

type daemonState struct {
	Running       bool   `json:"running"`
	Mode          string `json:"mode"`
	Pairing       bool   `json:"pairing"`
	Connected     bool   `json:"connected"`
	Ready         bool   `json:"ready"`
	Adapter       string `json:"adapter"`
	AdapterAddr   string `json:"adapterAddr"`
	PeerAddr      string `json:"peerAddr"`
	ReconnectAddr string `json:"reconnectAddr"`
	PairedSwitch  string `json:"pairedSwitch"`
	ProfilePath   string `json:"profilePath"`
	StartedAt     string `json:"startedAt"`
	LastError     string `json:"lastError"`
}
