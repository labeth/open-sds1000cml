// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import "math"

// Detection of the protocols beyond the first ten. Each one proves itself
// by its own check (a CRC, a checksum, parities, a fixed timing no other bus
// keeps) before it is named, and outscores the generic decoders (UART,
// Manchester, SPI, I2C) that read its line as something: a LIN frame's
// bytes, a Modbus frame's or a DMX512 packet's slots read as a UART too, a
// PS/2 line pair as I2C or SPI. A cheap look at each line's run lengths
// first skips the decoders whose time scale the line cannot hold.

// lineTiming is a line's shortest run and longest low, high and any run,
// in microseconds, and its edges.
type lineTiming struct {
	minRun, maxLow, maxHigh, maxRun float64
	edges                           int
}

// timingOf measures a sliced line's runs (the record's open first and last
// runs count for the longest only).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-238
func timingOf(S sliced, colTimeS float64) lineTiming {
	t := lineTiming{minRun: math.Inf(1), edges: len(S.edges)}
	for _, r := range levelRuns(S) {
		d := r.us(colTimeS)
		if !r.open {
			t.minRun = math.Min(t.minRun, d)
		}
		t.maxRun = math.Max(t.maxRun, d)
		if r.lvl == 0 {
			t.maxLow = math.Max(t.maxLow, d)
		} else {
			t.maxHigh = math.Max(t.maxHigh, d)
		}
	}
	return t
}

// near reports v within frac of want.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-238
func near(v, want, frac float64) bool { return math.Abs(v-want) <= frac*want }

// considerLines tries the single-line protocols beyond the first ten on
// line k, those its timing allows. uartBaud is a UART decode's rate on the
// line (0 none): MIDI is named only on a line at its 31250 Bd. S is the
// line's slicing at the automatic threshold.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-238
func considerLines(codes []uint8, S *sliced, colTimeS float64, format string, uartBaud float64, consider func(Result)) {
	if !S.ok || len(S.edges) < 4 || !(colTimeS > 0) {
		return
	}
	t := timingOf(*S, colTimeS)
	ir := IRCfg{Format: format}
	if t.maxLow >= 500 && t.minRun >= 30 {
		consider(DecodeLIN(codes, colTimeS, LINCfg{Format: format}))
	}
	if t.maxLow >= 80 && t.minRun >= 3 && t.minRun <= 4.8 {
		consider(DecodeDMX512(codes, colTimeS, DMXCfg{Format: format}))
	}
	considerSBUS(codes, colTimeS, t, format, consider)
	if near(uartBaud, 31250, 0.03) {
		consider(DecodeMIDI(codes, colTimeS, MIDICfg{Format: format}))
	}
	if t.minRun >= 0.9 && t.edges >= 40 {
		consider(DecodeModbus(codes, colTimeS, ModbusCfg{Format: format}))
	}
	if t.maxLow >= 40 && t.minRun <= 20 {
		consider(DecodeOneWire(codes, colTimeS, OneWireCfg{Format: format}))
	}
	if t.maxLow >= 40 && t.minRun <= 1.5 && t.minRun >= 0.1 {
		consider(DecodeWS281x(codes, colTimeS, WS281xCfg{Format: format}))
	}
	if t.maxLow >= 500 && t.minRun >= 15 {
		consider(DecodeDHT(codes, colTimeS, DHTCfg{Format: format}))
	}
	if t.maxLow >= 3400 {
		consider(DecodeCEC(codes, colTimeS, CECCfg{Format: format}))
	}
	if t.maxHigh >= 160 && t.minRun >= 30 {
		consider(DecodeJ1850(codes, colTimeS, J1850Cfg{Format: format}))
	}
	if t.maxRun >= 8000 && t.minRun >= 250 {
		consider(DecodeIRNEC(codes, colTimeS, ir))
	}
	if t.maxRun >= 2000 && t.minRun >= 300 {
		consider(DecodeIRSIRC(codes, colTimeS, ir))
		consider(DecodeIRRC5(codes, colTimeS, ir))
	}
	if t.maxRun >= 2000 && t.minRun >= 250 {
		consider(DecodeIRRC6(codes, colTimeS, ir))
	}
	if t.maxRun >= 1500 && t.minRun >= 300 && t.minRun <= 520 {
		consider(DecodeDALI(codes, colTimeS, DALICfg{Format: format}))
	}
	if t.minRun >= 1.2 && t.minRun <= 2.2 {
		consider(DecodeUSBPD(codes, colTimeS, USBPDCfg{Format: format}))
	}
	if t.minRun <= 0.3 && t.edges >= 64 {
		consider(DecodeSPDIF(codes, colTimeS, SPDIFCfg{Format: format}))
	}
	considerPhysLines(codes, colTimeS, format, t, consider)
}

// considerPairs tries the two-line protocols beyond SPI and I2C on both
// channel orders: PS/2, SWD and MDIO (clock and data), Wiegand (D0, D1).
// clockLine is the pair's clock when one line is clearly the clock (-1:
// neither): the clocked buses are not read with its data line as their clock
// (a 10 MHz SPI bus's data line clocking its clock read as MDIO frames).
// sl are the lines' slicings at the automatic threshold.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-238
func considerPairs(c1, c2 []uint8, sl [2]*sliced, colTimeS float64, format string, clockLine int, consider func(Result, int)) {
	if c1 == nil || c2 == nil || !(colTimeS > 0) {
		return
	}
	S := [2]sliced{*sl[0], *sl[1]}
	chans := [2][]uint8{c1, c2}
	for a := 0; a < 2; a++ {
		if !S[a].ok || !S[1-a].ok || len(S[a].edges) < 8 {
			continue
		}
		ta := timingOf(S[a], colTimeS)
		cfg := ClockedCfg{Format: format}
		x, y := chans[a], chans[1-a]
		if clockLine < 0 || clockLine == a {
			if ta.minRun >= 20 && ta.minRun <= 120 {
				consider(DecodePS2(x, y, colTimeS, cfg), a)
			}
			if !byteClocked(sl[a]) { // whole bytes a burst: SPI's
				consider(DecodeSWD(x, y, colTimeS, cfg), a)
			}
			consider(DecodeMDIO(x, y, colTimeS, cfg), a)
		}
		if ta.maxHigh >= 200 {
			consider(DecodeWiegand(x, y, colTimeS, cfg), a)
		}
		considerPhysPairs(x, y, colTimeS, format, ta, func(r Result) { consider(r, a) })
	}
}

// count tallies a decode's spans of each kind.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-238
func (r Result) count(kinds ...string) []int {
	out := make([]int, len(kinds))
	for _, s := range r.Spans {
		for i, k := range kinds {
			if s.Kind == k {
				out[i]++
			}
		}
	}
	return out
}

// scoreProto scores the protocols beyond the first ten (scoreResult): each
// needs the evidence its own check gives, and scores above the generic
// decoders' reading of the same line.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-238
func scoreProto(r Result) float64 {
	n := len(r.Bytes)
	c := r.count("sof", "frame-error", "parity-error", "data")
	frames, errs, data := c[0], c[1]+c[2], c[3]
	switch r.Proto {
	case "lin":
		// frames whose identifier's parity and checksum check
		good, hdr := linFrames(r)
		if good == 0 && hdr < 2 {
			return -1e9
		}
		return float64(good*300+hdr*80) - float64(errs*60)
	case "dmx512":
		// a break of 88 us and clean slots at 250 kbit/s; the slots read as a
		// UART too, at 10 a byte
		if errs*10 > n || data < 3 {
			return -1e9
		}
		return float64(frames*400 + data*12 - errs*40)
	case "midi":
		if c[1]*10 > n || n < 3 {
			return -1e9
		}
		return float64(n*12 + 60 - c[1]*40)
	case "modbus":
		good := modbusResultFrames(r)
		if good == 0 {
			return -1e9
		}
		return float64(good*400 + n*2 - errs*50)
	case "onewire":
		// a reset, its presence pulse and a ROM command after it
		good := 0
		for i, s := range r.Spans {
			if s.Kind == "ack" && i+1 < len(r.Spans) && r.Spans[i+1].Kind == "data" {
				if _, named := oneWireROM[r.Spans[i+1].Val]; named {
					good++
				}
			}
		}
		if good == 0 {
			return -1e9
		}
		return float64(good*200 + n*10 - errs*80)
	case "ws281x":
		return float64(frames*100 + n*20)
	case "dht":
		good := data / 5
		if good == 0 {
			return -1e9
		}
		return float64(good*500 - errs*100)
	case "cec":
		return float64(frames*150 + n*30 - errs*60)
	case "j1850":
		good := 0
		for i, s := range r.Spans {
			if s.Kind == "data" && (i+1 == len(r.Spans) || r.Spans[i+1].Kind == "sof" || r.Spans[i+1].Kind == "rw") {
				good++ // a frame's last byte, its CRC, checked
			}
		}
		if good == 0 {
			return -1e9
		}
		return float64(good*300 + n*4 - errs*80)
	case "irnec":
		return float64(data/4*400 + frames*20 - errs*100)
	case "irsirc", "irrc5", "irrc6":
		good := 0
		for _, s := range r.Spans {
			if s.Kind == "sof" {
				good++
			}
		}
		good -= errs
		if good <= 0 {
			return -1e9
		}
		return float64(good*150 + n*10 - errs*60)
	case "dali":
		if frames-errs <= 0 {
			return -1e9
		}
		return float64((frames-errs)*150 + n*10 - errs*60)
	case "usbpd":
		if errs > 0 && frames <= 1 {
			return -1e9
		}
		return float64(frames*600 + n*2 - errs*100)
	case "spdif":
		// subframes whose parity checks, framed by their preambles
		if data < 4 || errs*4 > data {
			return -1e9
		}
		return float64(data*20 - errs*40)
	case "ps2":
		// One whole frame is evidence SPI lacks: 11 clocks, a start 0, odd
		// parity and a stop 1, the clock idle high either side (a record at
		// 500us/div holds one or two of a keyboard's frames, and the SPI
		// reading of its byte won)
		if data < 1 || errs*3 > data || r.Cover < 0.8 {
			return -1e9
		}
		return float64(data*150 - errs*60)
	case "swd":
		// no debug probe clocks SWD past 60 MHz: a 104 MHz SPI flash's
		// data reads as a request and an OK now and then
		if r.Cover < 0.8 || r.Baud > 60e6 {
			return -1e9
		}
		// one transfer and no line reset is evidence random bits give: an SPI
		// stream of bytes back to back holds a request, an OK and a data
		// parity now and then, and outscored the SPI reading of every byte
		resets := 0
		for _, s := range r.Spans {
			if s.Kind == "rw" && s.Text == "LINERESET" {
				resets++
			}
		}
		if frames < 2 && resets == 0 {
			return float64(frames*40 + n*2 - errs*80)
		}
		return float64(frames*300 + n*4 - errs*80)
	case "mdio":
		if r.Cover < 0.8 {
			return -1e9
		}
		return float64(frames*300 - errs*100)
	case "wiegand":
		// 26- and 34-bit frames whose parities check
		good, cur := 0, 0
		for _, s := range r.Spans {
			switch {
			case s.Kind == "rw":
				good += cur
				cur = 0
				if s.Val == 26 || s.Val == 34 {
					cur = 1
				}
			case s.Kind == "parity-error":
				cur = 0
			}
		}
		good += cur
		if good == 0 {
			return -1e9
		}
		return float64(good*300 - errs*150)
	}
	return scorePhys(r)
}

// linFrames counts a LIN decode's frames whose identifier and checksum
// check (good) and those with a good identifier and no response (headers).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-238
func linFrames(r Result) (good, hdr int) {
	idOK, resp, bad := false, 0, false
	flush := func() {
		if idOK && !bad {
			if resp > 0 {
				good++
			} else {
				hdr++
			}
		}
		idOK, resp, bad = false, 0, false
	}
	for _, s := range r.Spans {
		switch {
		case s.Kind == "sof":
			flush()
		case len(s.Text) > 2 && s.Text[:2] == "ID":
			idOK = s.Kind == "data"
		case s.Kind == "data":
			resp++
		case s.Kind == "frame-error" || s.Kind == "parity-error":
			bad = true
		}
	}
	flush()
	return good, hdr
}

// modbusResultFrames counts a Modbus decode's frames without an error span
// (their CRC checked).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-238
func modbusResultFrames(r Result) int {
	good, open, bad := 0, false, false
	for _, s := range r.Spans {
		switch s.Kind {
		case "sof":
			if open && !bad {
				good++
			}
			open, bad = true, false
		case "frame-error", "parity-error":
			bad = true
		}
	}
	if open && !bad {
		good++
	}
	return good
}
