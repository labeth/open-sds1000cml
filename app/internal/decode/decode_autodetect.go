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
// i2cMinClockS is the shortest clock period an I2C bus runs (Ultra Fast-mode,
// 5 MHz, less a margin): a faster "clock" is edge ringing on another bus -
// ARINC 429's two lines at the full rate read as I2C on an 18 ns clock.
const i2cMinClockS = 180e-9

// i2cMinSetupS is how far an I2C START or STOP sits from SCL's edges at the
// least, under the fastest mode's 0.26 us set-up and hold times: SDA moving
// on an SCL edge is SPI's data (an FPGA's a nanosecond ahead of its clock, or
// a decimated record's a sample), which reads as STARTs and STOPs.
const i2cMinSetupS = 100e-9

// i2cConditionsOnEdges reports whether most of an I2C decode's STARTs and
// STOPs sit on an edge of SCL (sliced as scl): within i2cMinSetupS, and at
// least a sample and a half, of one.
// TRLC-LINKS: REQ-SDS-018
func i2cConditionsOnEdges(r Result, scl *sliced, colTimeS float64) bool {
	guard := 1.5
	if colTimeS > 0 {
		guard = math.Max(guard, i2cMinSetupS/colTimeS)
	}
	on, all := 0, 0
	for _, s := range r.Spans {
		if s.Kind != "start" && s.Kind != "stop" {
			continue
		}
		all++
		k := sort.Search(len(scl.edges), func(j int) bool { return scl.edges[j].i >= s.I0 })
		for _, j := range []int{k - 1, k} {
			if j >= 0 && j < len(scl.edges) && math.Abs(float64(scl.edges[j].i-s.I0)) <= guard {
				on++
				break
			}
		}
	}
	return all > 0 && 2*on > all
}

// uartMaxBaud is the fastest UART a detection names: the fast image's
// receiver's 62.5 MBd and a transmitter's 2 %. Ringing on another bus's
// edges reads as one faster at the full rate (ARINC 429 at 10ns/div: "UART
// 100058636 8N1").
const uartMaxBaud = 64e6

// sentDistinctFrames counts the different CRC-valid frames among a SENT
// decode's spans: each frame's nibbles from its SYNC to its CRC.
// TRLC-LINKS: REQ-SDS-018
func sentDistinctFrames(spans []Span) int {
	seen := map[string]bool{}
	var frame []byte
	for _, s := range spans {
		switch s.Kind {
		case "sync":
			frame = frame[:0]
		case "data", "frame-error":
			frame = append(frame, byte(s.Val))
		case "crc":
			seen[string(append(frame, byte(s.Val)))] = true
		}
	}
	return len(seen)
}

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
		// A START opens an addressed transfer: STARTs and STOPs with no
		// address between, outnumbering the transfers, are data edges while a
		// clock rests high (a 104 MHz SPI flash's decimated record read as
		// "I2C": one transfer among 200 bare START STOP pairs).
		if bare := starts - addrs; bare > 4 && bare > addrs {
			return -1e9
		}
		return float64(addrs*100 + acks*30 + datas*20 + starts*5)
	case "uart":
		// One byte is no UART: its stop bit checks on half of random lines - a
		// MIL-1553 bus at 1us/div (part of a word on screen) read as "UART
		// 2000000 8N1, 1 byte" on 3 screens of 8. Show more of the signal.
		if len(r.Bytes) < 2 {
			return -1e9
		}
		// Under 4 samples a bit a UART decode is guesswork: FlexRay at
		// 31 MS/s (3.3 samples a bit) reads as "UART 9.5 MBd".
		if r.SPB > 0 && r.SPB < 4 {
			return -1e9
		}
		if float64(r.Baud) > uartMaxBaud {
			return -1e9
		}
		// A few bytes at a rate no UART uses are not a UART: a partial SENT
		// frame read as "UART 66674 Bd, 5 bytes". Show more of the signal.
		std := nearStandardBaud(float64(r.Baud), 0.03)
		if len(r.Bytes) < 8 && !std {
			return -1e9
		}
		// Bytes of all zeros and all ones at a rate no UART uses are a pulse
		// train read as one: a 1-Wire bus's slots at 500us/div, "UART 141887
		// Bd" of 29 such bytes, outscored its reset and ROM command.
		if !std && pulseBytes(r.Bytes) {
			return -1e9
		}
		c := kinds("frame-error", "parity-error")
		// Over a fifth of the bytes misframed is another protocol read as UART,
		// at an odd rate (an undersampled SENT: "UART 45701 Bd") or a standard
		// one (part of a CAN frame: "UART 500000 8N1", 3 bytes, 1 misframed).
		if c[0]*5 > len(r.Bytes) {
			return -1e9
		}
		score := float64(len(r.Bytes)*10 - c[0]*35 - c[1]*18 + 15)
		// Clean at a rate UARTs use: every byte's stop bit checked.
		if std && c[0] == 0 && c[1] == 0 && len(r.Bytes) >= 8 {
			score += 100
		}
		return score
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
		// it is weighted between them; coding violations pull it down hard. One
		// byte is no evidence (part of a MIL-1553 word at 1us/div read as
		// "Manch, 1 byte"), as for a UART.
		if len(r.Bytes) < 2 {
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
		// The evidence is the distinct valid frames: a repeated message's frames
		// are one frame's chance. A UART repeating "Hi U" read as SENT had one
		// frame of two pass its CRC (C2: SYNC B 4 4 3 4 3 C 4) over 16 clean
		// 115200 Bd bytes. One frame weighs its nibbles, under a clean UART at
		// a standard rate and over SENT read as a UART at an odd one (a real
		// bus, bad, good, bad on screen, read "UART 69985 Bd").
		if sentDistinctFrames(r.Spans) < 2 {
			return float64(150 + c[1]*4 - c[2]*20)
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
		// ARINC 429 runs at 100 kbit/s or 12-14.5 kbit/s: words read at
		// another rate are another bus's pulses (a MIL-1553 bus at 500us/div
		// read as "ARINC 35.9kb/s").
		if !arincRate(float64(r.Baud)) {
			return -1e9
		}
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
	return scoreProto(r)
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
func midPhaseData(c *clockInfo, d *sliced) float64 {
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
func phaseLock(c *clockInfo, d *sliced) (share float64, n int) {
	if !c.ok || c.hp <= 0 || !d.ok {
		return 0, 0
	}
	// Phases from the interpolated crossings: whole samples quantize a fast
	// clock's cycle (4.8 samples at 104 MHz) coarser than the quarter the
	// lock is judged in, and the flash's data, 7 ns after the edge, read as
	// unlocked (a share of 0.57).
	var falls []float64
	for _, e := range c.s.edges {
		if e.dir < 0 {
			falls = append(falls, e.x)
		}
	}
	var bins [16]int
	j := 0
	for _, e := range d.edges {
		for j+1 < len(falls) && falls[j+1] <= e.x {
			j++
		}
		if j+1 >= len(falls) || falls[j] > e.x {
			continue
		}
		p := falls[j+1] - falls[j]
		if !(p > 0) || p > 3*c.hp { // a cycle is 2 half-periods; longer is a gap
			continue
		}
		bins[min(15, int(16*(e.x-falls[j])/p))]++
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
func launchEdges(c *clockInfo, d *sliced) (fall, rise int) {
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

// idleLevel is a clock line's idle level (SPI's CPOL): the level of its
// longest run without an edge when that is a gap between transfers - over
// twice the median run, a clock's runs being even - else the level it holds
// most, low when neither holds a run more: a screen of nothing but clocking
// holds both alike, and a vote of 12759 samples to 12753 read a mode 0 clock
// as mode 3. Low is SPI's usual idle (mode 0).
// TRLC-LINKS: REQ-SDS-018
func idleLevel(s *sliced) int {
	if !s.ok {
		return 0
	}
	median := 0
	if len(s.edges) >= 3 {
		runs := make([]int, 0, len(s.edges)+1)
		longest, at, prev := 0, 0, 0
		for k := 0; k <= len(s.edges); k++ { // the runs between edges and the record's ends
			end := s.n
			if k < len(s.edges) {
				end = s.edges[k].i
			}
			r := end - prev
			runs = append(runs, r)
			if r > longest {
				longest, at = r, prev
			}
			prev = end
		}
		sort.Ints(runs)
		median = runs[len(runs)/2]
		if longest > 2*median {
			if l := s.level[min(at+longest/2, s.n-1)]; l == 0 || l == 1 {
				return int(l)
			}
		}
	}
	hi, lo := 0, 0
	for _, l := range s.level {
		if l == 1 {
			hi++
		} else if l == 0 {
			lo++
		}
	}
	if hi > lo+median {
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

	// The helpers below take the channels' figures by address: copies of
	// them as arguments were merged by the compiler (go1.26) into the stack
	// slot of clk while a pointer into clk was still to be read, and the SPI
	// launch vote counted the clock against itself.
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
		share, n := phaseLock(&clk[ci], &clk[1-ci].s)
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
	var uartBaud [2]float64 // each line's UART rate, as read: MIDI's evidence
	if !locked {
		for _, k := range active {
			if !isClocky(k) {
				ru := DecodeUART(chans[k], colTimeS, UARTCfg{Bits: 8, Parity: "none", Format: format})
				if ru.OK {
					uartBaud[k] = float64(ru.Baud)
				}
				consider(ru, chName(k), line(k))
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
		// LIN through S/PDIF, each by its own check (decode_autodetect_proto.go)
		considerLines(chans[k], colTimeS, format, uartBaud[k], func(r Result) { consider(r, chName(k), line(k)) })
	}
	if len(active) >= 2 && !freeClock { // PS/2, SWD, MDIO, Wiegand: clock (D0) and data (D1) either way round
		clockLine := -1 // a clocked pair's clock: PS/2, SWD and MDIO are clocked by it, not by its data
		if clockedPair {
			clockLine = 0
			if u1 > u0 {
				clockLine = 1
			}
		}
		considerPairs(c1, c2, colTimeS, format, clockLine, func(r Result, a int) {
			consider(r, "CLK "+chName(a)+" DATA "+chName(1-a), Roles{ChA: a, ChB: 1 - a})
		})
	}
	if len(active) >= 2 && !freeClock {
		for _, ord := range [][2]int{{0, 1}, {1, 0}} { // I2C: scoring resolves SCL/SDA order
			r := DecodeI2C(chans[ord[0]], chans[ord[1]], colTimeS, I2CCfg{Format: format})
			if r.SPB > 0 && r.SPB*colTimeS < i2cMinClockS {
				continue
			}
			if r.OK && i2cConditionsOnEdges(r, &clk[ord[0]].s, colTimeS) {
				continue
			}
			consider(r, "SCL "+chName(ord[0])+" SDA "+chName(ord[1]), Roles{ChA: ord[0], ChB: ord[1]})
		}
		if clockedPair {
			ci := 0
			if u1 > u0 {
				ci = 1
			}
			di := 1 - ci
			if midPhaseData(&clk[ci], &clk[di].s) > 0.5 {
				return best // data moves between clock edges: I2C-like, never SPI
			}
			cpol := idleLevel(&clk[ci].s)
			// Data launched on the falling edge is sampled on the rising one
			// (modes 0 and 3, CPHA = CPOL), on the rising edge on the falling
			// one (modes 1 and 2); without a clear majority both are tried.
			cphas := []bool{false, true}
			if fall, rise := launchEdges(&clk[ci], &clk[di].s); fall >= 2*rise+2 {
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

// StandardBaud reports whether baud is a rate UARTs use (within 3 %).
// TRLC-LINKS: REQ-SDS-018
func StandardBaud(baud float64) bool { return nearStandardBaud(baud, 0.03) }

// arincRate reports a bit rate an ARINC 429 bus runs at, as read off a record:
// high speed 100 kbit/s, low speed 12 to 14.5 kbit/s, with a tenth's margin
// for a rate read off a decimated record's pulse spacing.
// TRLC-LINKS: REQ-SDS-018
func arincRate(baud float64) bool {
	return (baud >= 90e3 && baud <= 110e3) || (baud >= 10.8e3 && baud <= 16e3)
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

// pulseBytes reports bytes that are each all zeros or all ones.
// TRLC-LINKS: REQ-SDS-018
func pulseBytes(b []int) bool {
	for _, v := range b {
		if v&0xff != 0 && v&0xff != 0xff {
			return false
		}
	}
	return len(b) > 0
}
