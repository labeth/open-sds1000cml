// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"fmt"
	"math"
	"math/bits"
	"slices"
	"sort"
	"strings"
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

// sentOnLine is r refused where its one frame (of distinct valid ones) is a
// chance among the line's other edges: a sensor sends its frames back to
// back, and they span the line from its first edge to its last; frames
// spanning under half of it, one of them valid, are CRC-4's one in sixteen
// (a Maple bus's line, cut at both ends, read as SENT).
// TRLC-LINKS: REQ-SDS-018
func sentOnLine(r Result, s *sliced) Result {
	if !r.OK || sentDistinctFrames(r.Spans) >= 2 || len(s.edges) < 2 {
		return r
	}
	// every frame the decode read counts, its CRC checking or not (a bus
	// alternating bad and good CRCs): from its SYNC to its last span
	active := s.edges[len(s.edges)-1].i - s.edges[0].i
	covered, start, end := 0, -1, -1
	for _, sp := range r.Spans {
		if sp.Kind == "sync" {
			if start >= 0 {
				covered += end - start
			}
			start = sp.I0
		}
		end = max(end, sp.I1)
	}
	if start >= 0 {
		covered += end - start
	}
	if active > 0 && covered*2 < active {
		r.OK, r.Error = false, "one SENT frame among the line's other edges"
	}
	return r
}

// loneFrameOnLine is r refused where it read one frame ("sof") and that
// frame spans under half of the line from its first edge to its last: a
// remote repeats its frames and a sensor sends them back to back, so one
// frame among a line's other edges is the protocol's chance match (an
// Oregon sensor's bursts, cut, read as one RC-5 frame).
// TRLC-LINKS: REQ-SDS-018
func loneFrameOnLine(r Result, s *sliced) Result {
	if !r.OK || len(s.edges) < 2 {
		return r
	}
	frames, from, to := 0, -1, -1
	for _, sp := range r.Spans {
		if sp.Kind == "sof" {
			frames++
			if from < 0 {
				from = sp.I0
			}
		}
		to = max(to, sp.I1)
	}
	active := s.edges[len(s.edges)-1].i - s.edges[0].i
	if frames == 1 && from >= 0 && active > 0 && (to-from)*2 < active {
		r.OK, r.Error = false, "one "+r.Proto+" frame among the line's other edges"
	}
	return r
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
		// START STOP START STOP START with nothing between: data edges in
		// one clock high, a cJTAG escape (no I2C master sends that)
		if i2cEscape(r) {
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
		// After a NAK an I2C master stops or starts again: a transfer that
		// runs on past NAKs is another bus's bytes read nine bits at a time
		// (SPI mode 3, whose data changes while the clock rests high between
		// transfers, read as one START and a run of ACK and NAK at random).
		if n := i2cDataPastNak(r); n >= 2 && n*4 > datas {
			return -1e9
		}
		// An address no target acknowledged ends the transfer: a byte sent
		// after it is another bus read nine bits at a time (an MDIO frame
		// cut to one turnaround, "START 1E W NAK 4A STOP")
		if i2cDataPastAddrNak(r) {
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
		// Bytes of all zeros and all ones are a pulse train read as one, at
		// any rate: a 1-Wire bus's slots at 500us/div, "UART 141887 Bd" of 29
		// such bytes, outscored its reset and ROM command; a Wiegand reader's
		// pulses read "UART 19200 8N1", FF a byte
		if pulseBytes(r.Bytes) || clockOrPulseBytes(r.Bytes) {
			return -1e9
		}
		c := kinds("frame-error", "parity-error")
		// A UART at a rate none uses frames clean (an ESP8266's 74880 Bd boot
		// log does): misframed bytes there are another line's (an IR remote's
		// pulses read "UART 1777 Bd", one byte in twelve misframed)
		if !std && c[0] > 0 {
			return -1e9
		}
		// Over a fifth of the bytes misframed is another protocol read as UART,
		// at an odd rate (an undersampled SENT: "UART 45701 Bd") or a standard
		// one (part of a CAN frame: "UART 500000 8N1", 3 bytes, 1 misframed).
		if c[0]*5 > len(r.Bytes) {
			return -1e9
		}
		score := float64(len(r.Bytes)*10 - c[0]*35 - c[1]*18 + 15)
		// Clean at a rate UARTs use: every byte's stop bit checked. S.BUS's
		// framing (considerSBUS) is tried only on a line its frames prove.
		sbus := r.Inverted && r.Parity == "even" && r.Baud == sbusBaud
		if (std || sbus) && c[0] == 0 && c[1] == 0 && len(r.Bytes) >= 8 {
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
		// and more good nibbles than bad: a random line's edges read as
		// SYNCs and nibbles, two CRCs checking by chance, misread the most
		if c[0] == 0 || c[0]*4 < c[3]-1 || c[2] > c[1] {
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
		// a nominal rate past CAN's 1 Mbit/s is another bus's line whose
		// bits passed a CRC-15 by chance (a Maple bus's SDCKB read at 4 Mbit/s)
		if r.Baud > 1100000 {
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
	// periodFrac is the fraction of same-way edge gaps (rise to rise, fall to
	// fall) at the dominant period, period that period in samples: a clock
	// of any duty, where uniFrac halves with short pulses (a bit-banged 1 MHz
	// SCLK, 125 ns low, read 0.50)
	periodFrac, period float64
	edges              int
	hp                 float64 // dominant half-period in samples
	s                  sliced
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
	pf, period := periodUniformity(s.edges)
	return clockInfo{ok: true, uniFrac: float64(uni) / float64(len(gaps)), periodFrac: pf, period: period, edges: len(s.edges), hp: hp, s: s}
}

// periodUniformity is the fraction of same-way edge gaps (rise to rise, fall
// to fall) within 15% of their dominant one (a low percentile, as clockScore
// takes it: robust to the idle between bytes), and that gap.
// TRLC-LINKS: REQ-SDS-018
func periodUniformity(edges []edge) (float64, float64) {
	var gaps []int
	last := [2]int{-1, -1}
	for _, e := range edges {
		d := 0
		if e.dir > 0 {
			d = 1
		}
		if last[d] >= 0 {
			gaps = append(gaps, e.i-last[d])
		}
		last[d] = e.i
	}
	if len(gaps) < 4 {
		return 0, 0
	}
	sorted := append([]int(nil), gaps...)
	sort.Ints(sorted)
	p := float64(sorted[len(sorted)*20/100])
	if p <= 0 {
		return 0, 0
	}
	tol, uni := math.Max(0.15*p, 2.5), 0
	for _, g := range gaps {
		if math.Abs(float64(g)-p) <= tol {
			uni++
		}
	}
	return float64(uni) / float64(len(gaps)), p
}

// pulseClock reports a clock of short pulses: its same-way edges even (seven
// in ten: a bit-banged one holds a gap after each byte and stalls now and
// then), one
// phase under a quarter of its period (the half-period gaps then alternate,
// half of them the short one). A bit-banged SCLK is one; a data line's
// rises fall on its bits and are not even, I2C's SCL is near half duty.
// TRLC-LINKS: REQ-SDS-018
func (c *clockInfo) pulseClock() bool {
	return c.ok && c.periodFrac > 0.7 && c.period > 0 && c.hp*4 < c.period && c.uniFrac > 0.35 && c.uniFrac < 0.65
}

// clockLike is how clock-like a channel is: the evenness of its edges, or of
// its same-way edges for a clock of short pulses (pulseClock).
// TRLC-LINKS: REQ-SDS-018
func (c *clockInfo) clockLike() float64 {
	if c.pulseClock() {
		return c.periodFrac
	}
	return c.uniFrac
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
			if l := s.levels()[min(at+longest/2, s.n-1)]; l == 0 || l == 1 {
				return int(l)
			}
		}
	}
	hi, lo := 0, 0
	for _, l := range s.levels() {
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
	// The helpers below take the channels' figures by address: copies of
	// them as arguments were merged by the compiler (go1.26) into the stack
	// slot of clk while a pointer into clk was still to be read, and the SPI
	// launch vote counted the clock against itself.
	// Each channel is sliced once here (clk[k].s): the activity check and the
	// line and pair checks below read that slicing.
	clk := [2]clockInfo{clockScore(c1), clockScore(c2)}
	var active []int
	for k := 0; k < 2; k++ {
		if chans[k] == nil {
			continue
		}
		if s := &clk[k].s; s.ok && len(s.edges) >= 2 {
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
	spiBytes := 0 // the most bytes an SPI reading of the pair took
	// One whole SWD transfer and no line reset beside SPI's reading of eight
	// bytes or more of the same pair is either: a transfer is five bytes'
	// clocks, and an SPI stream holds one, every parity checking, one time
	// in a few hundred places. Neither is named; a longer record shows more
	// transfers or a line reset.
	settle := func() Result {
		if best.Proto == "swd" && spiBytes >= 8 && swdWholeReads(best) < 2 && !strings.Contains(best.Text, "LINERESET") {
			return Result{Proto: "off", Error: "one SWD transfer or SPI bytes: show more of the signal"}
		}
		return best
	}
	consider := func(r Result, src string, roles Roles) {
		if r.Proto == "spi" && r.OK {
			spiBytes = max(spiBytes, len(r.Bytes))
		}
		if sc := scoreResult(r); sc > bestScore {
			bestScore, best = sc, r
			best.Src, best.Roles = src, roles
		}
	}
	line := func(k int) Roles { return Roles{ChA: k, ChB: 1 - k} }

	u0, u1 := clk[0].clockLike(), clk[1].clockLike()
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
		u := c.clockLike()
		return (u > 0.78 && c.edges >= 40) || (u >= 0.95 && c.edges >= 10)
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
	} else {
		// A clock that keeps step with a UART (a square at its bit rate)
		// locks the pair: the data line is still a UART where it frames
		// clean at a rate UARTs use, eight bytes and more, every stop bit
		// checked - which an SPI bus's data does by chance one time in 256
		k := 0
		if u1 < u0 {
			k = 1
		}
		if ru := DecodeUART(chans[k], colTimeS, UARTCfg{Bits: 8, Parity: "none", Format: format}); ru.OK && len(ru.Bytes) >= 8 &&
			nearStandardBaud(float64(ru.Baud), 0.03) && ru.count("frame-error")[0] == 0 {
			uartBaud[k] = float64(ru.Baud)
			consider(ru, chName(k), line(k))
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
		consider(sentOnLine(DecodeSENT(chans[k], colTimeS, SENTCfg{}), &clk[k].s), chName(k), line(k))
		consider(DecodeCANFD(chans[k], colTimeS, CANFDCfg{DominantLow: true}), chName(k), line(k))
		consider(DecodeMIL1553(chans[k], colTimeS, MIL1553Cfg{}), chName(k), line(k))
		consider(DecodeARINC429(chans[k], colTimeS, ARINC429Cfg{}), chName(k), line(k))
		consider(DecodeUSBLS(chans[k], colTimeS, USBLSCfg{}), chName(k), line(k))
		consider(DecodeFlexRay(chans[k], colTimeS, FlexRayCfg{}), chName(k), line(k))
		// LIN through S/PDIF, each by its own check (decode_autodetect_proto.go)
		considerLines(chans[k], &clk[k].s, colTimeS, format, uartBaud[k], func(r Result) {
			// the line that clocks the other is a clock, its duty moved
			// by noise and slow edges: no PWM, and its bursts no Miller
			// line code (an MDIO bus's MDC, cut to a frame)
			if (r.Proto == "pwm" || r.Proto == "miller") && locked && clk[k].clockLike() > clk[1-k].clockLike() {
				return
			}
			consider(r, chName(k), line(k))
		})
	}
	if len(active) >= 2 && !freeClock { // PS/2, SWD, MDIO, Wiegand: clock (D0) and data (D1) either way round
		clockLine := -1 // a clocked pair's clock: PS/2, SWD and MDIO are clocked by it, not by its data
		if clockedPair {
			clockLine = 0
			if u1 > u0 {
				clockLine = 1
			}
		}
		considerPairs(c1, c2, [2]*sliced{&clk[0].s, &clk[1].s}, colTimeS, format, clockLine, func(r Result, a int) {
			// MDIO moves at MDC's rise, the station's bits and the PHY's
			// alike: a line that keeps that place no better than now and
			// then (an IR code beside a square) carries no MDIO frame
			if r.Proto == "mdio" {
				if share, n := phaseLock(&clk[a], &clk[1-a].s); n >= 12 && share < 0.8 {
					return
				}
			}
			// PDI_CLK runs on from the programmer, one period throughout:
			// a line of random edges or a line code's two widths clocks none
			if r.Proto == "pdi" && clk[a].clockLike() < 0.7 {
				return
			}
			// near half duty, its data moving at one place in the cycle:
			// an SPI clock's short pulses, or a square beside a carrier,
			// clocks no PDI
			if r.Proto == "pdi" {
				if share, n := phaseLock(&clk[a], &clk[1-a].s); clk[a].pulseClock() || (n >= 12 && share < 0.6) {
					return
				}
			}
			// a driver's STEP pulses are one width, its timer's, however
			// the rate ramps: a line code's highs of several widths (a
			// Miller line read as STEP) are no steps
			if r.Proto == "stepper" {
				if st := runStats(clk[a].s); st.highs >= 4 && st.highUniform < 0.7 {
					return
				}
			}
			// D0 and D1 idle and pulse briefly (some 50 us a bit of a
			// millisecond or two): lines whose runs are alike either way
			// (a random line, I2C's SCL and SDA) carry no Wiegand bits
			if r.Proto == "wiegand" {
				for _, k := range []int{a, 1 - a} {
					if st := runStats(clk[k].s); st.lows >= 3 && st.highs >= 3 && 4*min(st.medLow, st.medHigh) > max(st.medLow, st.medHigh) {
						return
					}
				}
			}
			consider(r, "CLK "+chName(a)+" DATA "+chName(1-a), Roles{ChA: a, ChB: 1 - a})
		})
	}
	if len(active) >= 2 && !freeClock {
		var i2cRead [2]Result                          // each line read as SCL
		for _, ord := range [][2]int{{0, 1}, {1, 0}} { // I2C: scoring resolves SCL/SDA order
			r := DecodeI2C(chans[ord[0]], chans[ord[1]], colTimeS, I2CCfg{Format: format, Deglitch: true})
			i2cRead[ord[0]] = r
			if r.SPB > 0 && r.SPB*colTimeS < i2cMinClockS {
				continue
			}
			if r.OK && i2cConditionsOnEdges(r, &clk[ord[0]].s, colTimeS) {
				continue
			}
			if byteClocked(&clk[ord[0]].s) { // whole bytes a burst: SPI, not 9 clocks a byte
				continue
			}
			// SCL is a clock near half duty, stretched now and then: a line
			// whose edges keep no half period (an RC-5 code beside a random
			// line read as SCL and SDA) or a clock of short pulses (DCF77's)
			// clocks no I2C
			if clk[ord[0]].clockLike() < 0.35 || clk[ord[0]].pulseClock() {
				continue
			}
			// and SDA moves at one delay into SCL low: a line that keeps no
			// place in its cycle (DCF77's pulses beside a square) is no SDA
			if share, n := phaseLock(&clk[ord[0]], &clk[ord[1]].s); n >= 12 && share < 0.6 {
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
			// data moving between the clock's edges is I2C-like, never SPI -
			// but for a clock of short pulses: bit-banged SPI sets its data
			// well before each one (a PCD8544 display at 1 MHz), and I2C's SCL
			// is near half duty
			if midPhaseData(&clk[ci], &clk[di].s) > 0.5 && !clk[ci].pulseClock() {
				return settle()
			}
			// addressed I2C transfers that a device acknowledged, each ended
			// by a STOP: an I2C bus that noise kept from its own checks (an
			// ACK read as a NAK), never SPI - which reads bytes off any pair
			if i2cAckedTransfers(i2cRead[ci]) >= 2 {
				// SPI's bytes still weigh against a lone SWD transfer
				if r := DecodeSPI(chans[ci], chans[di], colTimeS, SPICfg{MSB: true, Format: format}); r.OK {
					spiBytes = max(spiBytes, len(r.Bytes))
				}
				return settle()
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
	return settle()
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

// lineRuns is a sliced line's closed runs: how many lows and highs, their
// middle lengths in samples, and the share of the highs within a quarter
// of their middle one.
type lineRuns struct {
	lows, highs     int
	medLow, medHigh float64
	highUniform     float64
}

// runStats measures a sliced line's closed runs (lineRuns).
// TRLC-LINKS: REQ-SDS-018
func runStats(S sliced) lineRuns {
	var lo, hi []float64
	for _, r := range levelRuns(S) {
		if r.open {
			continue
		}
		if r.lvl == 0 {
			lo = append(lo, r.x1-r.x0)
		} else {
			hi = append(hi, r.x1-r.x0)
		}
	}
	st := lineRuns{lows: len(lo), highs: len(hi)}
	if len(lo) > 0 {
		st.medLow = nthValue(append([]float64(nil), lo...), len(lo)/2)
	}
	if len(hi) > 0 {
		st.medHigh = nthValue(append([]float64(nil), hi...), len(hi)/2)
		in := 0
		for _, h := range hi {
			if math.Abs(h-st.medHigh) <= 0.25*st.medHigh {
				in++
			}
		}
		st.highUniform = float64(in) / float64(len(hi))
	}
	return st
}

// swdWholeReads counts an SWD reading's whole transfers: a data word whose
// parity checks (a transfer the record cuts, or one a WAIT or FAULT ends,
// carries none).
// TRLC-LINKS: REQ-SDS-018
func swdWholeReads(r Result) int {
	n := 0
	for _, s := range r.Spans {
		if s.Kind == "data" && len(s.Text) == 8 {
			n++
		}
	}
	return n
}

// i2cAckedTransfers counts an I2C reading's whole transfers: an address a
// device acknowledged, a byte or more after it, a NAK on the last byte at
// most (a master reading ends so), and a STOP. None where STARTs with no
// address after them outnumber the addresses (data edges while a clock
// rests high, as scoreResult reads them).
// TRLC-LINKS: REQ-SDS-018
func i2cAckedTransfers(r Result) int {
	n, addrs, starts := 0, 0, 0
	open, datas, nak := false, 0, false
	for k, s := range r.Spans {
		switch s.Kind {
		case "addr":
			addrs++
			open, datas, nak = k+2 < len(r.Spans) && r.Spans[k+2].Kind == "ack", 0, false
		case "data":
			if nak { // a byte after a NAK: no master's
				open = false
			}
			datas++
		case "nak":
			if datas > 0 {
				nak = true
			}
		case "start":
			starts++
			open = false
		case "stop":
			if open && datas > 0 {
				n++
			}
			open = false
		}
	}
	if bare := starts - addrs; bare > 4 && bare > addrs {
		return 0
	}
	return n
}

// clockOrPulseBytes reports a UART reading of eight bytes or more that is a
// clock or a pulse train: seven in ten 55 (a square at half the rate, a
// cJTAG probe's TCKC under noise), or eight in ten holding one 0 bit at
// most (a line of short low pulses, a Wiegand reader's, each read as a
// start bit and a byte of ones).
// TRLC-LINKS: REQ-SDS-018
func clockOrPulseBytes(b []int) bool {
	if len(b) < 8 {
		return false
	}
	sq, pulse := 0, 0
	for _, v := range b {
		v &= 0xff
		if v == 0x55 {
			sq++
		}
		if bits.OnesCount8(uint8(v)) >= 7 {
			pulse++
		}
	}
	return sq*10 >= len(b)*7 || pulse*10 >= len(b)*8
}

// periodicBits reports bytes whose bits, LSB first in order, repeat with a
// period of eight bits or fewer through nine tenths of them: a square wave
// read as a line code (Miller's "92 24 49" over and over), no data.
// TRLC-LINKS: REQ-SDS-018
func periodicBits(b []int) bool {
	var bs []int
	for _, v := range b {
		for k := 0; k < 8; k++ {
			bs = append(bs, v>>k&1)
		}
	}
	if len(bs) < 48 {
		return false
	}
	for p := 1; p <= 8; p++ {
		same := 0
		for i := p; i < len(bs); i++ {
			if bs[i] == bs[i-p] {
				same++
			}
		}
		if same*10 >= (len(bs)-p)*9 {
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

// byteClocked reports a clock line whose bursts each hold whole bytes: two or
// more bursts between idle gaps, every one a multiple of 8 pulses and none
// what an I2C transfer clocks (9 a byte, plus a rise for its STOP and its
// repeated STARTs; a set of bursts that all may be I2C's says nothing).
// That is SPI clocking
// bytes out; I2C and SWD read on it only by chance (SPI mode 3, its data
// changing while the clock rests high between transfers, read as I2C STARTs;
// short transfers as SWD requests with an OK). A burst the record cuts at
// either end is left out. A single long burst says nothing (a debug probe's
// SWD runs its clock on).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-238
func byteClocked(s *sliced) bool {
	var rise []int
	for _, e := range s.edges {
		if e.dir > 0 {
			rise = append(rise, e.i)
		}
	}
	if len(rise) < 17 {
		return false
	}
	gaps := make([]int, len(rise)-1)
	for i := range gaps {
		gaps[i] = rise[i+1] - rise[i]
	}
	med := quickMedianInts(gaps)
	if med <= 0 {
		return false
	}
	var bursts []int
	n := 1
	for _, g := range gaps {
		if g > 4*med {
			bursts = append(bursts, n)
			n = 1
			continue
		}
		n++
	}
	bursts = append(bursts, n)
	if rise[0] < 4*med { // the record began inside the first burst
		bursts = bursts[1:]
	}
	if len(bursts) > 0 && s.n-rise[len(rise)-1] < 4*med { // and ended inside the last
		bursts = bursts[:len(bursts)-1]
	}
	// a first or last burst that is no whole bytes is one the record cut,
	// though the record began or ended in a gap between its bytes (a
	// bit-banged display's 3 clocks of a byte, the gap before them longer
	// than the split): left out where whole bursts remain beside it
	if len(bursts) >= 3 && bursts[0]%8 != 0 {
		bursts = bursts[1:]
	}
	if len(bursts) >= 3 && bursts[len(bursts)-1]%8 != 0 {
		bursts = bursts[:len(bursts)-1]
	}
	if len(bursts) < 2 {
		return false
	}
	// I2C clocks 9 a byte, plus a rise for its STOP and one for a repeated
	// START: a burst 0 to 3 past a multiple of 9 may be I2C. Whole bytes in
	// every burst are SPI unless every one of them may be I2C (48 clocks, six
	// SPI bytes, is five I2C bytes and a STOP and a START).
	notI2C := false
	for _, b := range bursts {
		if b%8 != 0 {
			return false
		}
		notI2C = notI2C || b%9 > 3
	}
	return notI2C
}

// quickMedianInts is the median of v (reordering a copy).
// TRLC-LINKS: REQ-SDS-018
func quickMedianInts(v []int) int {
	c := slices.Clone(v)
	slices.Sort(c)
	return c[len(c)/2]
}

// i2cDataPastNak counts the data bytes an I2C decode reads after a NAK in
// the same transfer, where an I2C master sends STOP or a repeated START.
// TRLC-LINKS: REQ-SDS-018
func i2cDataPastNak(r Result) int {
	n, nak := 0, false
	for _, s := range r.Spans {
		switch s.Kind {
		case "nak":
			nak = true
		case "data":
			if nak {
				n++
			}
			nak = false
		case "start", "stop", "addr":
			nak = false
		}
	}
	return n
}

// i2cDataPastAddrNak reports an I2C decode reading a data byte after an
// address a NAK answered, in the same transfer.
// TRLC-LINKS: REQ-SDS-018
func i2cDataPastAddrNak(r Result) bool {
	addr, nak := false, false
	for _, s := range r.Spans {
		switch s.Kind {
		case "addr":
			addr, nak = true, false
		case "rw":
		case "nak":
			nak = addr
			addr = false
		case "ack":
			addr = false
		case "data":
			if nak {
				return true
			}
		case "start", "stop":
			addr, nak = false, false
		}
	}
	return false
}

// i2cEscape reports an I2C decode holding five STARTs and STOPs in a row
// with nothing between them: a cJTAG escape's TMSC edges while TCKC is high.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-256
func i2cEscape(r Result) bool {
	run, last := 0, ""
	for _, s := range r.Spans {
		switch {
		case (s.Kind == "start" || s.Kind == "stop") && s.Kind != last:
			run++
		case s.Kind == "start" || s.Kind == "stop":
			run = 1
		default:
			run = 0
		}
		last = s.Kind
		if run >= 5 {
			return true
		}
	}
	return false
}
