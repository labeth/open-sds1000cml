// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import "strings"

// Detection of the sigrok protocols beyond the first thirty. Those with a
// check (a CRC, a checksum, parities, an inverse) are named on it; the rest
// on the structure no other bus keeps (a quadrature encoder's lines a
// quarter cycle apart, a stepper's short STEP pulses with a DIR that seldom
// changes, a PWM line's steady period and moving duty cycle, Miller's bit
// and a half spacings), and they score low: any bus with a check that reads
// the same lines wins. A look at each line's run lengths first skips the
// decoders whose time scale the line cannot hold.

// considerPhysLines tries the single-line protocols beyond the first
// thirty on a line, those its timing (t, microseconds) allows.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-256
func considerPhysLines(codes []uint8, colTimeS float64, format string, t lineTiming, consider func(Result)) {
	lc := LineCfg{Format: format}
	if t.edges >= 12 {
		consider(DecodePWM(codes, colTimeS, lc))
	}
	if t.edges >= 50 && t.minRun >= 50 {
		consider(DecodeRCEncode(codes, colTimeS, lc))
	}
	if t.edges >= 40 && t.minRun >= 50 && t.minRun <= 2000 {
		consider(DecodeOOK(codes, colTimeS, lc))
		consider(DecodeEM4100(codes, colTimeS, lc))
	}
	if t.edges >= 16 && t.minRun <= 100 {
		consider(DecodeMiller(codes, colTimeS, lc))
	}
	if t.edges >= 6 && t.minRun >= 1000 {
		consider(DecodeMorse(codes, colTimeS, lc))
	}
	if t.edges >= 10 && t.minRun >= 200 {
		consider(DecodeDCF77(codes, colTimeS, lc))
	}
	if t.edges >= 20 && t.maxRun >= 100 && t.minRun <= 300 {
		consider(DecodeEM4305(codes, colTimeS, lc))
		consider(DecodeT55xx(codes, colTimeS, lc))
	}
	if t.edges >= 20 && t.minRun <= 3 {
		consider(DecodeSWIM(codes, colTimeS, lc))
	}
	if t.edges >= 20 && t.minRun <= 6 && t.maxLow >= 12 {
		consider(DecodeSDQ(codes, colTimeS, lc))
	}
	if t.edges >= 20 && t.minRun >= 15 && t.minRun <= 70 {
		consider(DecodePJDL(codes, colTimeS, lc))
	}
	if t.edges >= 18 && t.minRun >= 600 && t.minRun <= 1000 && t.maxRun >= 3000 {
		consider(DecodeDSI(codes, colTimeS, lc))
	}
	if t.edges >= 30 && t.minRun >= 150 && t.minRun <= 350 {
		consider(DecodeQi(codes, colTimeS, lc))
	}
	if t.edges >= 20 && t.minRun >= 150 && t.maxRun >= 3000 {
		consider(DecodeIRMP(codes, colTimeS, lc))
	}
}

// considerPhysPairs tries the two-line protocols beyond the first thirty
// with line x as the clock (A, STEP, TCKC, SDCKA) and y the data.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-256
func considerPhysPairs(x, y []uint8, colTimeS float64, format string, tx lineTiming, consider func(Result)) {
	cfg := ClockedCfg{Format: format}
	consider(DecodeQuadrature(x, y, colTimeS, cfg))
	consider(DecodeStepper(x, y, colTimeS, cfg))
	if tx.edges >= 96 {
		consider(DecodeSDCard(x, y, colTimeS, cfg))
		consider(DecodePDI(x, y, colTimeS, cfg))
		consider(DecodeCJTAG(x, y, colTimeS, cfg))
	}
	consider(DecodeMaple(x, y, colTimeS, cfg))
	if tx.edges >= 48 && tx.maxRun >= 10000 {
		consider(DecodeCaliper(x, y, colTimeS, cfg))
	}
}

// scorePhys scores the protocols beyond the first thirty (scoreProto).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-256
func scorePhys(r Result) float64 {
	n := len(r.Bytes)
	c := r.count("sof", "frame-error", "parity-error", "data")
	frames, errs, data := c[0], c[1]+c[2], c[3]
	// errors between the first and the last good unit: a record cuts a
	// frame at either end (a PDI instruction's first characters, a DSI
	// frame's last bits), which is no evidence against the bus
	inner := innerErrs(r)
	switch r.Proto {
	case "quad":
		// steps without a glitch (a phase jump of two: lines that change
		// together are no encoder)
		if errs > 0 || n < 8 || r.Cover < 0.8 {
			return -1e9
		}
		return float64(30 * min(n, 40))
	case "stepper":
		// short STEP pulses and a DIR that changes seldom, and both ways
		turns, ups := 0, 0
		for i, v := range r.Bytes {
			ups += v
			if i > 0 && v != r.Bytes[i-1] {
				turns++
			}
		}
		if n < 8 || ups == 0 || ups == n || turns*4 > n || !(r.Feature > 0) || r.Feature > 0.35*r.SPB || r.Cover > 0.25 {
			return -1e9
		}
		return float64(25 * min(n, 40))
	case "pwm":
		// a steady period, a duty cycle that moves: a clock's does not
		// (a sample's jitter of the edges moves a duty cycle by 256/SPB)
		lo, hi := 255, 0
		for _, v := range r.Bytes {
			lo, hi = min(lo, v), max(hi, v)
		}
		if n < 6 || float64(hi-lo) < 13+512/r.SPB || !pwmSteady(r) {
			return -1e9
		}
		return float64(60 + min(n, 20))
	case "rcencode":
		good := 0
		cur := true
		for _, s := range r.Spans {
			switch s.Kind {
			case "sof":
				cur = true
			case "frame-error":
				cur = false
			case "stop":
				if cur {
					good++
				}
			}
		}
		if good == 0 {
			return -1e9
		}
		return float64(good * 250)
	case "ook", "qi", "pjdl", "maple":
		// bursts, packets and frames whose checksum or CRC checks; an
		// Oregon burst the record cut after its sync and eight nibbles (a
		// 100 ms message at 5 ms/div) stands on the sync alone
		bad := c[1]
		good := frames - bad
		if good <= 0 && r.Proto == "ook" && r.Cover > 0 && n >= 8 {
			return 300
		}
		if good <= 0 {
			return -1e9
		}
		return float64(good*400 + n*2)
	case "miller":
		// modified Miller's pauses, or a classic line's bit-and-a-half
		// spacings (Manchester and NRZ have none)
		if frames < 2 || inner > 0 || (!r.Inverted && r.Cover < 0.15) {
			return -1e9
		}
		return float64(frames*100 + n*4)
	case "morse":
		// dots and dashes of one and three units, letters of several
		long := 0
		for _, s := range r.Spans {
			if s.Kind == "data" && len(MorseOf(byte(s.Val))) > 1 {
				long++
			}
		}
		if data < 3 || errs*4 > data || r.Cover < 0.9 || long == 0 {
			return -1e9
		}
		return float64(15 * min(data, 40))
	case "dcf77":
		if data == 0 {
			return -1e9
		}
		return float64(data*200 - errs*100)
	case "em4100", "em4305":
		good := 0
		cur, open := true, false
		for _, s := range r.Spans {
			if s.Kind == "sof" {
				if open && cur {
					good++
				}
				cur, open = true, true
			} else if s.Kind == "frame-error" || s.Kind == "parity-error" {
				cur = false
			}
		}
		if open && cur {
			good++
		}
		if good == 0 {
			return -1e9
		}
		return float64(good*500 + n*2)
	case "t55xx":
		if r.FrameBits != 70 && r.FrameBits != 38 {
			return -1e9
		}
		return float64(frames*250 + n*2)
	case "swim":
		// a whole transaction: a read or write on the fly, its count, the
		// three address bytes and a byte, each acknowledged with its parity
		if !swimTransaction(r) {
			return -1e9
		}
		return float64(data*60 - errs*40)
	case "sdq":
		if frames == 0 || n < 2 || inner > 0 {
			return -1e9
		}
		return float64(50 + 5*min(n, 20))
	case "dsi":
		if data < 2 || inner > 0 {
			return -1e9
		}
		return float64(60 * min(data, 10))
	case "sdcard":
		good := frames - c[1]
		if good < 2 || c[1]*2 > frames {
			return -1e9
		}
		return float64(good*400 + n*3)
	case "pdi":
		// instructions in a row; the cut ones at the record's ends few
		if frames < 3 || n < 6 || inner > 0 || errs > 4 {
			return -1e9
		}
		return float64(frames*40 + n*8)
	case "cjtag":
		scans := 0
		for _, s := range r.Spans {
			if s.Kind == "data" {
				scans++
			}
		}
		if scans == 0 || !strings.Contains(r.Text, "ESC6") || r.Cover < 0.8 {
			return -1e9
		}
		// an escape (six data edges within one clock high) is no I2C
		// transaction nor any other bus: its STOPs and STARTs read as one
		return float64(1000 + scans*300)
	case "caliper":
		good := 0
		for i, s := range r.Spans {
			if s.Kind == "sof" && i+3 < len(r.Spans) && r.Spans[i+3].Val&0x6F == 0 {
				good++
			}
		}
		if good < 2 {
			return -1e9
		}
		return float64(good * 150)
	case "irmp":
		good := 0
		for _, s := range r.Spans {
			if s.Kind == "data" && strings.HasPrefix(s.Text, "C") {
				good++
			}
		}
		if good == 0 {
			return -1e9
		}
		return float64(good*300 - errs*50)
	}
	return -1e9
}

// swimTransaction reports a SWIM decode holding a whole read or write on
// the fly.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-256
func swimTransaction(r Result) bool {
	step := 0
	for _, s := range r.Spans {
		switch {
		case s.Kind == "sof":
			step = 0
		case s.Kind != "data":
			step = -9
		case step == 0 && (s.Text == "ROTF" || s.Text == "WOTF"):
			step = 1
		case step == 1 && s.Text[0] == 'N':
			step = 2
		case step >= 2 && step <= 4 && s.Text[0] == '@':
			step++
		case step == 5 && (s.Text[0] == 'R' || s.Text[0] == 'W'):
			return true
		default:
			step = -9
		}
	}
	return false
}

// pwmSteady reports a PWM decode's periods all within 5 % of their median.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-256
func pwmSteady(r Result) bool {
	var per []float64
	for _, s := range r.Spans {
		if s.Kind == "data" {
			per = append(per, float64(s.I1-s.I0))
		}
	}
	m := medianOf(per)
	if !(m > 0) {
		return false
	}
	for _, p := range per {
		if p < 0.95*m-1 || p > 1.05*m+1 {
			return false
		}
	}
	return true
}

// innerErrs is r's error spans between its first and its last good unit
// ("data" spans).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-256
func innerErrs(r Result) int {
	first, last := -1, -1
	for i, s := range r.Spans {
		if s.Kind == "data" {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	n := 0
	for i := first + 1; first >= 0 && i < last; i++ {
		if k := r.Spans[i].Kind; k == "frame-error" || k == "parity-error" {
			n++
		}
	}
	return n
}
