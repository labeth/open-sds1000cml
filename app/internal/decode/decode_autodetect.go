// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"fmt"
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
// TRLC-LINKS: REQ-SDS-018
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
		if addrs == 1 && datas == 0 {
			// One stray START on SPI data (a data change while the clock is
			// high) reads as one bare address; it can outscore a real 18-byte
			// SPI decode. Keep it only as a last resort.
			return 5
		}
		return float64(addrs*100 + acks*30 + datas*20 + starts*5)
	case "uart":
		if len(r.Bytes) == 0 {
			return -1e9
		}
		// Under 4 samples a bit a UART decode is guesswork: FlexRay at
		// 31 MS/s (3.3 samples a bit) reads as "UART 9.5 MBd".
		if r.SPB > 0 && r.SPB < 4 {
			return -1e9
		}
		// A few bytes at a rate no UART uses are not a UART: a partial SENT
		// frame read as "UART 66674 Bd, 5 bytes". Show more of the signal.
		std := nearStandardBaud(float64(r.Baud), 0.03)
		if len(r.Bytes) < 8 && !std {
			return -1e9
		}
		c := kinds("frame-error", "parity-error")
		// An odd rate with over a fifth of its bytes misframed is another
		// protocol read as UART (an undersampled SENT: "UART 45701 Bd").
		if !std && c[0]*5 > len(r.Bytes) {
			return -1e9
		}
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
		// no framing -> weakest; Margin breaks CPHA. Text (three quarters
		// printable) breaks the bit order; anything less is binary, which ties
		// and keeps MSB first (the SPI default): a printable share alone read
		// binary payloads LSB-first half the time.
		text := 0.0
		if printable*4 >= len(r.Bytes)*3 {
			text = 6
		}
		return float64(len(r.Bytes)*2) + r.Margin*10 + text
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
		c := kinds("crc", "data", "frame-error", "sync")
		// CRC-4 passes one random frame in 16: a lone valid frame among many
		// syncs is chance, as a UART at under two samples a bit reads as
		// "SENT, 1 of 17 frames". A quarter of the
		// complete frames must check: half rejected a real bus alternating
		// bad and good CRCs (bad, good, bad on screen).
		if c[0] == 0 || c[0]*4 < c[3]-1 {
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
		// Even a bad-parity word supplies 32 correctly spaced bipolar RZ
		// pulses. Keep that framing evidence: partial boundary words can
		// otherwise add enough false UART bytes to outscore valid ARINC.
		return float64(good*150 + c[0]*50 - c[1]*40)
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

// TRLC-LINKS: REQ-SDS-018
type clockInfo struct {
	ok      bool
	uniFrac float64
	edges   int
	hp      float64 // dominant half-period in samples
	s       sliced
}

// clockScore measures how clock-like a channel is, robust to idle gaps: the
// dominant half-period is a low percentile of the edge gaps (ignoring big idle
// gaps); uniFrac is the fraction of gaps that ARE that half-period (~1 for a
// clock, low for a data line whose edges land on data-dependent bit boundaries).
// TRLC-LINKS: REQ-SDS-018
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
	return clockInfo{ok: true, uniFrac: float64(uni) / float64(len(gaps)), edges: len(s.edges), hp: hp, s: s}
}

// midPhaseData is the share of the data line's edges that sit away from every
// clock edge (beyond a quarter half-period). SPI launches data on a clock edge;
// I2C moves SDA in the middle of SCL low. Mostly mid-phase data is not SPI:
// an I2C window without its START reads as "SPI mode 3, 3 bytes" otherwise.
// TRLC-LINKS: REQ-SDS-018
func midPhaseData(c clockInfo, d sliced) float64 {
	if !c.ok || c.hp <= 0 || !d.ok || len(d.edges) == 0 || len(c.s.edges) == 0 {
		return 0
	}
	tol := math.Max(c.hp/4, 1.5)
	ce := c.s.edges
	mid, j := 0, 0
	for _, e := range d.edges {
		for j+1 < len(ce) && ce[j+1].i <= e.i {
			j++
		}
		dist := math.Abs(float64(e.i - ce[j].i))
		if j+1 < len(ce) {
			dist = math.Min(dist, math.Abs(float64(ce[j+1].i-e.i)))
		}
		if dist > tol {
			mid++
		}
	}
	return float64(mid) / float64(len(d.edges))
}

// phaseLock is how firmly the data line's edges keep one place in the clock's
// cycle: the largest share of them inside a quarter of the cycle (16 bins of
// falling edge to falling edge, the best 4 in a row, wrapping), with n the
// edges counted (only those inside a cycle: a burst's idle gap has no phase).
// A line this clock clocks is near 1: SPI launches data on an edge, I2C moves
// SDA at one delay into SCL low. A line that only shares the screen with a
// clock, a UART beside a free-running clock, spreads over the whole cycle.
// TRLC-LINKS: REQ-SDS-018
func phaseLock(c clockInfo, d sliced) (share float64, n int) {
	if !c.ok || c.hp <= 0 || !d.ok {
		return 0, 0
	}
	var falls []int
	for _, e := range c.s.edges {
		if e.dir < 0 {
			falls = append(falls, e.i)
		}
	}
	var bins [16]int
	j := 0
	for _, e := range d.edges {
		for j+1 < len(falls) && falls[j+1] <= e.i {
			j++
		}
		if j+1 >= len(falls) || falls[j] > e.i {
			continue
		}
		p := falls[j+1] - falls[j]
		if float64(p) > 3*c.hp { // a cycle is 2 half-periods; longer is a gap
			continue
		}
		bins[16*(e.i-falls[j])/p]++
		n++
	}
	if n == 0 {
		return 0, 0
	}
	best := 0
	for s := range bins {
		if w := bins[s] + bins[(s+1)%16] + bins[(s+2)%16] + bins[(s+3)%16]; w > best {
			best = w
		}
	}
	return float64(best) / float64(n), n
}

// launchEdges counts the data line's edges that sit on a falling and on a
// rising clock edge (the nearer one, within a quarter half-period). SPI
// launches each bit on one clock edge and samples it on the other, so the
// data's edges name the launch edge and with it CPHA, where the sampled
// amplitude cannot: on sharp edges every sample is a full rail either way.
// TRLC-LINKS: REQ-SDS-018
func launchEdges(c clockInfo, d sliced) (fall, rise int) {
	if !c.ok || c.hp <= 0 || !d.ok {
		return 0, 0
	}
	tol := math.Max(c.hp/4, 1.5)
	ce := c.s.edges
	j := 0
	for _, e := range d.edges {
		for j+1 < len(ce) && ce[j+1].i <= e.i {
			j++
		}
		near, dist := -1, math.Inf(1)
		for _, k := range []int{j, j + 1} {
			if k < len(ce) {
				if dd := math.Abs(float64(e.i - ce[k].i)); dd < dist {
					near, dist = k, dd
				}
			}
		}
		if near < 0 || dist > tol {
			continue
		}
		if ce[near].dir < 0 {
			fall++
		} else {
			rise++
		}
	}
	return fall, rise
}

// idleLevel is the rail a channel rests on most (a clock idles at its CPOL rail).
// TRLC-LINKS: REQ-SDS-018
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
// TRLC-LINKS: REQ-SDS-018
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
	// A detection needs more evidence than errors (score > 0): garbage UART
	// and Manchester decodes - more frame errors than words - would win
	// whenever nothing else qualifies, e.g. MIL-1553 at 50 us/div read as
	// Manchester and an undecodable SENT as "MANCH 51 kb/s".
	bestScore := 0.0
	chName := func(k int) string { return [2]string{"C1", "C2"}[k&1] }
	consider := func(r Result, src string, roles Roles) {
		if sc := scoreResult(r); sc > bestScore {
			bestScore, best = sc, r
			best.Src, best.Roles = src, roles
		}
	}
	line := func(k int) Roles { return Roles{ChA: k, ChB: 1 - k} }

	clk := [2]clockInfo{clockScore(c1), clockScore(c2)}
	u0, u1 := clk[0].uniFrac, clk[1].uniFrac
	hi, lo := math.Max(u0, u1), math.Min(u0, u1)
	clockedPair := len(active) >= 2 && hi > 0.72 && hi > lo+0.12
	// A clock beside a line it does not clock is no clocked bus: a UART next to
	// a free-running clock would read as SPI, I2C or nothing.
	// That line keeps no place in the clock's cycle, so it is tried on its own
	// and neither SPI nor I2C is claimed for the pair.
	freeClock := false
	if clockedPair {
		ci := 0
		if u1 > u0 {
			ci = 1
		}
		share, n := phaseLock(clk[ci], clk[1-ci].s)
		freeClock = n >= 12 && share < 0.6
	}
	locked := clockedPair && !freeClock
	// A near-perfectly uniform line is a clock even with few edges on screen: a
	// 3 kHz square at 500 us/div (~30 edges) would read as constant-bit
	// Manchester. Real Manchester data mixes T/2 and T gaps.
	isClocky := func(k int) bool {
		c := clk[k]
		return (c.uniFrac > 0.78 && c.edges >= 40) || (c.uniFrac >= 0.95 && c.edges >= 10)
	}

	// UART — async single-wire. Skip on a clocked pair (that's SPI) or a lone clock
	// (else auto-baud invents bogus 0x55s from the square wave).
	if !locked {
		for _, k := range active {
			if !isClocky(k) {
				consider(DecodeUART(chans[k], colTimeS, UARTCfg{Bits: 8, Parity: "none", Format: format}), chName(k), line(k))
			}
		}
	}
	// Manchester — heuristic like UART, and a bare square wave IS valid
	// constant-bit Manchester, so apply the same clock suppression: never claim
	// Manchester on a clocked pair (that's SPI) or on a lone clock line.
	if !locked {
		for _, k := range active {
			if !isClocky(k) {
				consider(DecodeManchester(chans[k], colTimeS, ManchesterCfg{IEEE: true, MSB: true, Format: format}), chName(k), line(k))
			}
		}
	}
	// The remaining single-wire protocols self-validate (CRC-4/CRC-15/CRC-11,
	// word parity, PID complement, strict framing), so their scoring gates make
	// false claims on foreign signals cosmically unlikely — try every channel.
	for _, k := range active {
		consider(DecodeSENT(chans[k], colTimeS, SENTCfg{}), chName(k), line(k))
		consider(DecodeCANFD(chans[k], colTimeS, CANFDCfg{DominantLow: true}), chName(k), line(k))
		consider(DecodeMIL1553(chans[k], colTimeS, MIL1553Cfg{}), chName(k), line(k))
		consider(DecodeARINC429(chans[k], colTimeS, ARINC429Cfg{}), chName(k), line(k))
		consider(DecodeUSBLS(chans[k], colTimeS, USBLSCfg{}), chName(k), line(k))
		consider(DecodeFlexRay(chans[k], colTimeS, FlexRayCfg{}), chName(k), line(k))
	}
	if len(active) >= 2 && !freeClock {
		for _, ord := range [][2]int{{0, 1}, {1, 0}} { // I2C: scoring resolves SCL/SDA order
			consider(DecodeI2C(chans[ord[0]], chans[ord[1]], colTimeS, I2CCfg{Format: format}),
				"SCL "+chName(ord[0])+" SDA "+chName(ord[1]), Roles{ChA: ord[0], ChB: ord[1]})
		}
		if clockedPair {
			ci := 0
			if u1 > u0 {
				ci = 1
			}
			di := 1 - ci
			if midPhaseData(clk[ci], clk[di].s) > 0.5 {
				return best // data moves between clock edges: I2C-like, never SPI
			}
			cpol := idleLevel(clk[ci].s)
			// Data launched on the falling edge is sampled on the rising one
			// (modes 0 and 3, CPHA = CPOL), on the rising edge on the falling
			// one (modes 1 and 2); without a clear majority both are tried.
			cphas := []bool{false, true}
			if fall, rise := launchEdges(clk[ci], clk[di].s); fall >= 2*rise+2 {
				cphas = []bool{cpol == 1}
			} else if rise >= 2*fall+2 {
				cphas = []bool{cpol == 0}
			}
			for _, cpha := range cphas {
				for _, msb := range []bool{true, false} { // msb first on a binary tie (SPI default)
					order, mode := "LSB", cpol<<1
					if msb {
						order = "MSB"
					}
					if cpha {
						mode |= 1
					}
					consider(DecodeSPI(chans[ci], chans[di], colTimeS,
						SPICfg{CPOL: cpol == 1, CPHA: cpha, MSB: msb, Format: format}),
						fmt.Sprintf("CLK %s DATA %s mode %d %s", chName(ci), chName(di), mode, order),
						Roles{ChA: ci, ChB: di, CPOL: cpol == 1, CPHA: cpha, MSB: msb})
				}
			}
		}
	}
	return best
}

// nearStandardBaud reports whether baud lies within tol (fraction) of a
// standard UART rate.
// TRLC-LINKS: REQ-SDS-018
func nearStandardBaud(baud, tol float64) bool {
	for _, std := range standardBauds {
		if math.Abs(baud-std) <= tol*std {
			return true
		}
	}
	return false
}
