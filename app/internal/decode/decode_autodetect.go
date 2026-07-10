package decode

import (
	"math"
	"sort"
)

// scoreResult ranks competing protocol/role hypotheses. The discriminators are
// structural and layered by how hard each protocol's own validity signals are
// to forge: CRC-validated protocols (CAN's CRC-15, FlexRay's header CRC-11,
// SENT's CRC-4) outscore parity/structure-validated ones (MIL-1553B word
// parity, ARINC 429 word parity, USB's PID complement, I2C's START/addr/ACK
// framing), which outscore the purely heuristic ones (UART stop bits,
// Manchester mid-cell coding, and SPI — no framing at all, the fallback).
func scoreResult(r Result) float64 {
	if !r.OK {
		return -1e9
	}
	kinds := func(ks ...string) []int {
		out := make([]int, len(ks))
		for _, s := range r.Spans {
			for i, k := range ks {
				if s.Kind == k {
					out[i]++
				}
			}
		}
		return out
	}
	switch r.Proto {
	case "i2c":
		c := kinds("addr", "ack", "nak", "data", "start")
		addrs, acks, datas, starts := c[0], c[1]+c[2], c[3], c[4]
		if addrs == 0 { // no addressed device -> a channel-swap mis-read, not real I2C
			return -1e9
		}
		return float64(addrs*100 + acks*30 + datas*20 + starts*5)
	case "uart":
		if len(r.Bytes) == 0 {
			return -1e9
		}
		c := kinds("frame-error", "parity-error")
		return float64(len(r.Bytes)*10 - c[0]*35 - c[1]*18 + 15)
	case "spi":
		if len(r.Bytes) == 0 {
			return -1e9
		}
		printable := 0
		for _, b := range r.Bytes {
			if b >= 0x20 && b < 0x7f {
				printable++
			}
		}
		pf := float64(printable) / float64(len(r.Bytes))
		// no framing -> weakest; Margin breaks CPHA, printable breaks bit-order.
		return float64(len(r.Bytes)*2) + r.Margin*10 + pf*6
	case "manchester":
		// Heuristic (no CRC): must clear real UART/SPI on their own signals, so
		// it is weighted between them; coding violations pull it down hard.
		if len(r.Bytes) == 0 {
			return -1e9
		}
		c := kinds("start", "frame-error")
		return float64(len(r.Bytes)*8 + c[0]*10 - c[1]*30)
	case "sent":
		// OK already gates on >=1 CRC-4-valid frame; score by validated frames.
		c := kinds("crc", "data", "frame-error")
		if c[0] == 0 {
			return -1e9
		}
		return float64(c[0]*300 + c[1]*4 - c[2]*20)
	case "canfd":
		// Require a CRC-15-validated classic frame (FD trailers are best-effort
		// and carry no verified CRC here — never auto-claim CAN on those alone).
		c := kinds("crc", "frame-error")
		if c[0] == 0 {
			return -1e9
		}
		return float64(c[0]*500 + len(r.Bytes)*2 - c[1]*60)
	case "mil1553":
		// One "data" span per word; a parity-failed word adds one frame-error.
		c := kinds("data", "frame-error")
		good := c[0] - c[1]
		if good <= 0 {
			return -1e9
		}
		return float64(good*150 + c[0]*10 - c[1]*40)
	case "arinc429":
		// One "addr" (label) span per word; a parity-failed word adds "!P".
		c := kinds("addr", "frame-error")
		good := c[0] - c[1]
		if good <= 0 {
			return -1e9
		}
		return float64(good*150 + c[0]*15 - c[1]*40)
	case "usbls":
		// A valid PID (nibble + matching complement) spans "addr"; bad -> error.
		c := kinds("addr", "frame-error")
		if c[0] == 0 {
			return -1e9
		}
		return float64(c[0]*120 + len(r.Bytes)*4 - c[1]*30)
	case "flexray":
		// The header note span is "addr" only when the header CRC-11 verified.
		c := kinds("addr", "frame-error")
		if c[0] == 0 {
			return -1e9
		}
		return float64(c[0]*400 + len(r.Bytes)*2 - c[1]*50)
	}
	return -1e9
}

type clockInfo struct {
	ok      bool
	uniFrac float64
	edges   int
	s       sliced
}

// clockScore measures how clock-like a channel is, robust to idle gaps: the
// dominant half-period is a low percentile of the edge gaps (ignoring big idle
// gaps); uniFrac is the fraction of gaps that ARE that half-period (~1 for a
// clock, low for a data line whose edges land on data-dependent bit boundaries).
func clockScore(codes []uint8) clockInfo {
	s := sliceChannel(codes, 0, false)
	if !s.ok || len(s.edges) < 6 {
		return clockInfo{s: s}
	}
	gaps := make([]int, 0, len(s.edges)-1)
	for k := 1; k < len(s.edges); k++ {
		gaps = append(gaps, s.edges[k].i-s.edges[k-1].i)
	}
	sorted := append([]int(nil), gaps...)
	sort.Ints(sorted)
	hp := float64(sorted[len(sorted)*20/100])
	if hp <= 0 {
		return clockInfo{s: s}
	}
	tol := math.Max(0.4*hp, 2.5) // floor: ±1-sample quantization eats a fast clock's band
	uni := 0
	for _, g := range gaps {
		if math.Abs(float64(g)-hp) <= tol {
			uni++
		}
	}
	return clockInfo{ok: true, uniFrac: float64(uni) / float64(len(gaps)), edges: len(s.edges), s: s}
}

// idleLevel is the rail a channel rests on most (a clock idles at its CPOL rail).
func idleLevel(s sliced) int {
	if !s.ok {
		return 0
	}
	hi, lo := 0, 0
	for _, l := range s.level {
		if l == 1 {
			hi++
		} else if l == 0 {
			lo++
		}
	}
	if hi > lo {
		return 1
	}
	return 0
}

// Autodetect tries every plausible protocol / channel-role / sub-setting against
// the two channels (c1=index 0, c2=index 1) and returns the best-scoring decoded
// Result, formatted per `format`. A Result with Proto=="off" means nothing matched.
func Autodetect(c1, c2 []uint8, colTimeS float64, format string) Result {
	chans := [2][]uint8{c1, c2}
	var active []int
	for k := 0; k < 2; k++ {
		if chans[k] == nil {
			continue
		}
		if s := sliceChannel(chans[k], 0, false); s.ok && len(s.edges) >= 2 {
			active = append(active, k)
		}
	}
	best := Result{Proto: "off", Error: "no protocol matched"}
	if len(active) == 0 {
		best.Error = "no active signal"
		return best
	}
	bestScore := -1e8
	consider := func(r Result) {
		if sc := scoreResult(r); sc > bestScore {
			bestScore, best = sc, r
		}
	}

	clk := [2]clockInfo{clockScore(c1), clockScore(c2)}
	u0, u1 := clk[0].uniFrac, clk[1].uniFrac
	hi, lo := math.Max(u0, u1), math.Min(u0, u1)
	clockedPair := len(active) >= 2 && hi > 0.72 && hi > lo+0.12
	isClocky := func(k int) bool { return clk[k].uniFrac > 0.78 && clk[k].edges >= 40 }

	// UART — async single-wire. Skip on a clocked pair (that's SPI) or a lone clock
	// (else auto-baud invents bogus 0x55s from the square wave).
	if !clockedPair {
		for _, k := range active {
			if !isClocky(k) {
				consider(DecodeUART(chans[k], colTimeS, UARTCfg{Bits: 8, Parity: "none", Format: format}))
			}
		}
	}
	// Manchester — heuristic like UART, and a bare square wave IS valid
	// constant-bit Manchester, so apply the same clock suppression: never claim
	// Manchester on a clocked pair (that's SPI) or on a lone clock line.
	if !clockedPair {
		for _, k := range active {
			if !isClocky(k) {
				consider(DecodeManchester(chans[k], colTimeS, ManchesterCfg{IEEE: true, MSB: true, Format: format}))
			}
		}
	}
	// The remaining single-wire protocols self-validate (CRC-4/CRC-15/CRC-11,
	// word parity, PID complement, strict framing), so their scoring gates make
	// false claims on foreign signals cosmically unlikely — try every channel.
	for _, k := range active {
		consider(DecodeSENT(chans[k], colTimeS, SENTCfg{}))
		consider(DecodeCANFD(chans[k], colTimeS, CANFDCfg{DominantLow: true}))
		consider(DecodeMIL1553(chans[k], colTimeS, MIL1553Cfg{}))
		consider(DecodeARINC429(chans[k], colTimeS, ARINC429Cfg{}))
		consider(DecodeUSBLS(chans[k], colTimeS, USBLSCfg{}))
		consider(DecodeFlexRay(chans[k], colTimeS, FlexRayCfg{}))
	}
	if len(active) >= 2 {
		for _, ord := range [][2]int{{0, 1}, {1, 0}} { // I2C: scoring resolves SCL/SDA order
			consider(DecodeI2C(chans[ord[0]], chans[ord[1]], colTimeS, I2CCfg{Format: format}))
		}
		if clockedPair {
			ci := 0
			if u1 > u0 {
				ci = 1
			}
			di := 1 - ci
			cpol := idleLevel(clk[ci].s)
			for _, cpha := range []bool{false, true} {
				for _, msb := range []bool{true, false} { // msb first on a binary tie (SPI default)
					consider(DecodeSPI(chans[ci], chans[di], colTimeS,
						SPICfg{CPOL: cpol == 1, CPHA: cpha, MSB: msb, Format: format}))
				}
			}
		}
	}
	return best
}
