package decode

import (
	"math"
	"math/rand"
	"testing"
)

// ---------------------------------------------------------------------------
// Red-team harness for DecodeI2C (app/internal/decode/decode_i2c_spi.go).
//
// I2C carries no CRC/checksum, so its integrity signal is FRAMING: a confident
// valid transaction is START ... addr/data ... STOP with no frame-error span.
// The decoder always returns OK=true once it can slice both channels and find
// >=2 SCL rising edges, so OK alone is NOT the confidence signal — the span
// stream is. Helpers below extract the framing so the asserts can be strict.
// ---------------------------------------------------------------------------

// bkTxn is one synthesized I2C transaction.
type bkTxn struct {
	addr7 int
	rw    int // 0=W, 1=R
	data  []int
	naks  []bool // per-byte ack level (index 0=addr, 1..=data); true=NAK(high)
	stop  bool   // emit a STOP condition at the end
}

// bkI2CBuild synthesizes one or more I2C transactions. It mirrors the timing of
// the existing i2cWave() synthesizer (SDA set up while SCL low, sampled on SCL
// rising; each bit is h low + h high samples). All SDA transitions happen while
// SCL is low EXCEPT the intentional START (SDA falls, SCL high) and STOP (SDA
// rises, SCL high), so a clean frame never emits a spurious START/STOP.
func bkI2CBuild(txns []bkTxn, h, leadIdle, interGap, trailIdle int) (scl, sda []uint8) {
	lo, hi := uint8(40), uint8(210)
	seg := func(c, d uint8, n int) {
		for i := 0; i < n; i++ {
			scl = append(scl, c)
			sda = append(sda, d)
		}
	}
	pushByte := func(v int, ackHigh bool) {
		for k := 7; k >= 0; k-- {
			b := lo
			if (v>>k)&1 == 1 {
				b = hi
			}
			seg(lo, b, h) // SCL low: set up bit
			seg(hi, b, h) // SCL high: sampled on rising edge
		}
		ab := lo
		if ackHigh {
			ab = hi
		}
		seg(lo, ab, h) // 9th clock ACK/NAK
		seg(hi, ab, h)
	}
	seg(hi, hi, leadIdle) // bus idle (both high)
	for ti, t := range txns {
		if ti > 0 {
			seg(hi, hi, interGap)
		}
		seg(hi, lo, h) // START: SDA falls while SCL high
		ackHigh := len(t.naks) > 0 && t.naks[0]
		pushByte(t.addr7<<1|(t.rw&1), ackHigh)
		for di, d := range t.data {
			ah := len(t.naks) > di+1 && t.naks[di+1]
			pushByte(d, ah)
		}
		if t.stop {
			seg(lo, lo, h)   // SCL low, SDA low
			seg(hi, lo, h/2) // SCL high, SDA still low
			seg(hi, hi, h*2) // SDA rises while SCL high = STOP
		}
	}
	if len(txns) > 0 && !txns[len(txns)-1].stop {
		seg(lo, lo, trailIdle) // no-STOP tail: keep SCL low so no false STOP forms
	} else {
		seg(hi, hi, trailIdle)
	}
	return
}

func i2cCountKind(r Result, kind string) int {
	c := 0
	for _, s := range r.Spans {
		if s.Kind == kind {
			c++
		}
	}
	return c
}

func i2cHasKind(r Result, kind string) bool { return i2cCountKind(r, kind) > 0 }

// i2cAddrVals returns the decoded 7-bit addresses in span order.
func i2cAddrVals(r Result) []int {
	var out []int
	for _, s := range r.Spans {
		if s.Kind == "addr" {
			out = append(out, s.Val)
		}
	}
	return out
}

// i2cConfident is the FRAMING integrity check: a genuine, intact transaction.
func i2cConfident(r Result) bool {
	return r.OK && i2cHasKind(r, "start") && i2cHasKind(r, "stop") &&
		len(r.Bytes) > 0 && !i2cHasKind(r, "frame-error")
}

func eqInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func randData(rng *rand.Rand, n int) []int {
	d := make([]int, n)
	for i := range d {
		d[i] = rng.Intn(256)
	}
	return d
}

func TestBreakI2c(t *testing.T) {
	rng := rand.New(rand.NewSource(0x12C0FFEE))

	// ===================================================================
	// CLASS 1: FALSE NEGATIVES — >=50 fully VALID frames must decode EXACTLY.
	// ===================================================================
	fnBad := 0
	for it := 0; it < 120; it++ {
		h := 2 + rng.Intn(40) // samples per half-clock; colsPerClock = 2h >= 4
		nTxn := 1 + rng.Intn(3)
		// A real bus is idle-high before START, so a capture that begins the
		// instant SDA is already low has no observable START edge. Keep >=h.
		lead := h + rng.Intn(6*h)
		trail := rng.Intn(6 * h)
		interGap := rng.Intn(6 * h)

		var txns []bkTxn
		var wantAddr []int
		var wantRW []int
		var wantBytes []int
		for k := 0; k < nTxn; k++ {
			addr := rng.Intn(128)
			rw := rng.Intn(2)
			nd := rng.Intn(5) // 0..4 data bytes
			data := randData(rng, nd)
			// vary ACK/NAK levels — legal either way; must not change payload
			naks := make([]bool, 1+nd)
			for i := range naks {
				naks[i] = rng.Intn(4) == 0
			}
			txns = append(txns, bkTxn{addr7: addr, rw: rw, data: data, naks: naks, stop: true})
			wantAddr = append(wantAddr, addr)
			wantRW = append(wantRW, rw)
			wantBytes = append(wantBytes, data...)
		}

		var scl, sda []uint8
		// Use the shipped i2cWave() synthesizer for a subset (reuse), our
		// multi-txn builder for the rest.
		if nTxn == 1 && rng.Intn(2) == 0 {
			scl, sda = i2cWave(txns[0].addr7, txns[0].rw, txns[0].data, h)
		} else {
			scl, sda = bkI2CBuild(txns, h, lead, interGap, trail)
		}

		r := DecodeI2C(scl, sda, 2e-7, I2CCfg{})
		if !r.OK {
			t.Errorf("[FN it=%d h=%d] valid frame did not decode: %s", it, h, r.Error)
			fnBad++
			continue
		}
		if i2cHasKind(r, "frame-error") {
			t.Errorf("[FN it=%d h=%d] valid frame flagged frame-error; toks=%q", it, h, r.Text)
			fnBad++
		}
		if got := i2cCountKind(r, "start"); got != nTxn {
			t.Errorf("[FN it=%d] START count %d, want %d; toks=%q", it, got, nTxn, r.Text)
			fnBad++
		}
		if got := i2cCountKind(r, "stop"); got != nTxn {
			t.Errorf("[FN it=%d] STOP count %d, want %d; toks=%q", it, got, nTxn, r.Text)
			fnBad++
		}
		if got := i2cAddrVals(r); !eqInts(got, wantAddr) {
			t.Errorf("[FN it=%d] addrs %v, want %v; toks=%q", it, got, wantAddr, r.Text)
			fnBad++
		}
		if !eqInts(r.Bytes, wantBytes) {
			t.Errorf("[FN it=%d h=%d] bytes %v, want %v; toks=%q", it, h, r.Bytes, wantBytes, r.Text)
			fnBad++
		}
		// rw span text must match, in order
		var gotRW []int
		for _, s := range r.Spans {
			if s.Kind == "rw" {
				if s.Text == "R" {
					gotRW = append(gotRW, 1)
				} else {
					gotRW = append(gotRW, 0)
				}
			}
		}
		if !eqInts(gotRW, wantRW) {
			t.Errorf("[FN it=%d] rw %v, want %v; toks=%q", it, gotRW, wantRW, r.Text)
			fnBad++
		}
	}
	if fnBad == 0 {
		t.Logf("FALSE-NEGATIVE class: 120 valid frames all decoded exactly")
	}

	// FALSE NEGATIVES, part 2: valid frames re-railed to asymmetric levels with
	// additive noise (stresses auto threshold + hysteresis) — must still decode
	// exactly. All wave samples are exactly 40 or 210, so remap is unambiguous.
	for it := 0; it < 60; it++ {
		h := 4 + rng.Intn(24)
		addr := rng.Intn(128)
		rw := rng.Intn(2)
		nd := 1 + rng.Intn(5)
		data := randData(rng, nd)
		txns := []bkTxn{{addr7: addr, rw: rw, data: data, naks: make([]bool, 1+nd), stop: true}}
		scl0, sda0 := bkI2CBuild(txns, h, 3*h, 0, 3*h)

		newLo := 20 + rng.Intn(50)  // 20..69
		newHi := 180 + rng.Intn(55) // 180..234
		amp := newHi - newLo
		// Keep noise well inside the rail-to-threshold margin so rail samples
		// never cross the (mid) threshold: margin ~ amp/2, use up to ~0.30*amp.
		noiseMax := amp * 30 / 100
		remap := func(src []uint8) []uint8 {
			out := make([]uint8, len(src))
			for i, v := range src {
				base := newLo
				if v == 210 {
					base = newHi
				}
				nz := 0
				if noiseMax > 0 {
					nz = rng.Intn(2*noiseMax+1) - noiseMax
				}
				c := base + nz
				if c < 0 {
					c = 0
				}
				if c > 255 {
					c = 255
				}
				out[i] = uint8(c)
			}
			return out
		}
		scl, sda := remap(scl0), remap(sda0)
		r := DecodeI2C(scl, sda, 2e-7, I2CCfg{})
		if !r.OK {
			t.Errorf("[FN2 it=%d lo=%d hi=%d] noisy re-railed frame failed: %s", it, newLo, newHi, r.Error)
			fnBad++
			continue
		}
		if i2cHasKind(r, "frame-error") || i2cCountKind(r, "start") != 1 || i2cCountKind(r, "stop") != 1 {
			t.Errorf("[FN2 it=%d lo=%d hi=%d noise=%d] framing wrong; toks=%q", it, newLo, newHi, noiseMax, r.Text)
			fnBad++
		}
		if !eqInts(r.Bytes, data) {
			t.Errorf("[FN2 it=%d lo=%d hi=%d noise=%d h=%d] bytes %v, want %v; toks=%q",
				it, newLo, newHi, noiseMax, h, r.Bytes, data, r.Text)
			fnBad++
		}
		if got := i2cAddrVals(r); len(got) != 1 || got[0] != addr {
			t.Errorf("[FN2 it=%d] addr %v, want [%d]; toks=%q", it, got, addr, r.Text)
			fnBad++
		}
	}
	if fnBad == 0 {
		t.Logf("FALSE-NEGATIVE part 2: 60 noisy/re-railed frames decoded exactly")
	}

	// ===================================================================
	// CLASS 2: FALSE POSITIVES — adversarial input must not read as a
	// confident (START..STOP, no frame-error) transaction.
	// ===================================================================
	// Flat/DC and degenerate shapes: must yield NO decoded bytes at all.
	for it := 0; it < 20; it++ {
		n := 500 + rng.Intn(3000)
		lvl := uint8(rng.Intn(256))
		flat := make([]uint8, n)
		for i := range flat {
			flat[i] = lvl
		}
		r := DecodeI2C(flat, flat, 2e-7, I2CCfg{})
		if len(r.Bytes) != 0 {
			t.Errorf("[FP flat it=%d lvl=%d] flat DC decoded %d bytes: %v", it, lvl, len(r.Bytes), r.Bytes)
		}
		if i2cConfident(r) {
			t.Errorf("[FP flat it=%d] flat DC reported a confident transaction", it)
		}
	}

	// Slow ramps and Nyquist toggles on both channels: no confident frame.
	for it := 0; it < 20; it++ {
		n := 500 + rng.Intn(3000)
		a := make([]uint8, n)
		b := make([]uint8, n)
		switch it % 3 {
		case 0: // ramp
			for i := range a {
				a[i] = uint8(i * 255 / max(1, n-1))
				b[i] = uint8((n - 1 - i) * 255 / max(1, n-1))
			}
		case 1: // Nyquist toggle
			for i := range a {
				a[i] = uint8(i % 2 * 255)
				b[i] = uint8((i + 1) % 2 * 255)
			}
		case 2: // DC clock + Nyquist data
			for i := range a {
				a[i] = 210
				b[i] = uint8(i % 2 * 255)
			}
		}
		r := DecodeI2C(a, b, 2e-7, I2CCfg{})
		if i2cConfident(r) {
			t.Errorf("[FP shape it=%d kind=%d] degenerate shape read as confident txn; toks=%q", it, it%3, r.Text)
		}
	}

	// Wrong-protocol square wave with NO START (SDA only changes while SCL
	// low, and never falls while SCL high): must decode ZERO bytes because no
	// transaction is ever opened.
	for it := 0; it < 20; it++ {
		h := 4 + rng.Intn(20)
		lo, hi := uint8(40), uint8(210)
		var scl, sda []uint8
		seg := func(c, d uint8, k int) {
			for i := 0; i < k; i++ {
				scl = append(scl, c)
				sda = append(sda, d)
			}
		}
		nbit := 8 + rng.Intn(40)
		seg(lo, lo, h*3)
		for k := 0; k < nbit; k++ {
			d := lo
			if rng.Intn(2) == 0 {
				d = hi
			}
			seg(lo, d, h) // SDA settles while SCL low
			seg(hi, d, h) // SCL high; SDA constant -> no START/STOP
		}
		seg(lo, lo, h*3)
		r := DecodeI2C(scl, sda, 2e-7, I2CCfg{})
		if len(r.Bytes) != 0 {
			t.Errorf("[FP no-start it=%d] clock/data with no START decoded %d bytes: %v", it, len(r.Bytes), r.Bytes)
		}
		if i2cConfident(r) {
			t.Errorf("[FP no-start it=%d] no-START square wave read as confident txn", it)
		}
	}

	// TRUNCATED / no-STOP valid frames: a real START + payload but the STOP is
	// cut off or never emitted. The decoder MUST flag this (frame-error "(no
	// STOP)") and MUST NOT report it as a confident transaction.
	truncMissed := 0
	truncExercised := 0 // iterations that actually reached the open-frame state
	for it := 0; it < 30; it++ {
		h := 3 + rng.Intn(20)
		addr := rng.Intn(128)
		nd := 2 + rng.Intn(4)
		data := randData(rng, nd)
		var scl, sda []uint8
		if it%2 == 0 {
			// Build a full frame, then slice off the tail before the STOP.
			txns := []bkTxn{{addr7: addr, rw: rng.Intn(2), data: data, naks: make([]bool, 1+nd), stop: true}}
			scl, sda = bkI2CBuild(txns, h, 2*h, 0, 2*h)
			cut := len(scl) * (55 + rng.Intn(30)) / 100
			if cut < 1 {
				cut = 1
			}
			scl, sda = scl[:cut], sda[:cut]
		} else {
			// Build a frame that simply never emits a STOP (ends SCL low).
			txns := []bkTxn{{addr7: addr, rw: rng.Intn(2), data: data, naks: make([]bool, 1+nd), stop: false}}
			scl, sda = bkI2CBuild(txns, h, 2*h, 0, 2*h)
		}
		r := DecodeI2C(scl, sda, 2e-7, I2CCfg{})
		if i2cConfident(r) {
			t.Errorf("[FP trunc it=%d] truncated frame read as confident txn; toks=%q", it, r.Text)
			truncMissed++
		}
		// The open transaction should be reported as a frame-error, not swallowed.
		if r.OK && i2cHasKind(r, "start") && !i2cHasKind(r, "stop") {
			truncExercised++
			if !i2cHasKind(r, "frame-error") {
				t.Errorf("[FP trunc it=%d] open (no-STOP) frame NOT flagged frame-error; toks=%q", it, r.Text)
				truncMissed++
			}
		}
	}
	// Guard against a vacuous test: the frame-error path must actually be hit.
	if truncExercised < 20 {
		t.Errorf("[FP trunc] only %d/30 truncations reached the open-frame state; test too weak", truncExercised)
	}
	if truncMissed == 0 {
		t.Logf("FALSE-POSITIVE class: flat/ramp/nyquist/no-start/truncated all handled (%d open frames flagged)", truncExercised)
	}

	// ===================================================================
	// CLASS 3: EDGE CASES — no panic, sane behavior.
	// ===================================================================
	// Boundary sample counts.
	for _, n := range []int{0, 1, 2, 3, 7, 8} {
		s := make([]uint8, n)
		for i := range s {
			s[i] = uint8(40 + (i%2)*170)
		}
		r := DecodeI2C(s, s, 2e-7, I2CCfg{})
		if r.OK && len(r.Bytes) > 0 {
			t.Errorf("[EDGE n=%d] tiny record produced bytes %v", n, r.Bytes)
		}
	}

	// Mismatched channel lengths must not panic and must not over-read.
	{
		a := make([]uint8, 100)
		b := make([]uint8, 3)
		for i := range a {
			a[i] = uint8(40 + (i%2)*170)
		}
		_ = DecodeI2C(a, b, 2e-7, I2CCfg{}) // must not panic
		_ = DecodeI2C(b, a, 2e-7, I2CCfg{})
	}

	// Minimum legal bit rate (h=2 -> colsPerClock=4) and a large h.
	for _, h := range []int{2, 3, 200} {
		txns := []bkTxn{{addr7: 0x24, rw: 0, data: []int{0x55, 0xAA}, naks: make([]bool, 3), stop: true}}
		scl, sda := bkI2CBuild(txns, h, 3*h, 0, 3*h)
		r := DecodeI2C(scl, sda, 2e-7, I2CCfg{})
		if !r.OK {
			t.Errorf("[EDGE h=%d] min/max rate frame failed: %s", h, r.Error)
			continue
		}
		if !eqInts(r.Bytes, []int{0x55, 0xAA}) {
			t.Errorf("[EDGE h=%d] bytes %v, want [85 170]", h, r.Bytes)
		}
		if !i2cConfident(r) {
			t.Errorf("[EDGE h=%d] clean single frame not confident; toks=%q", h, r.Text)
		}
	}

	// Near-minimum amplitude: amp just above minAmp(=20) must still decode; amp
	// below minAmp must be rejected (not decoded as garbage).
	{
		txns := []bkTxn{{addr7: 0x2A, rw: 0, data: []int{0x39, 0xC6}, naks: make([]bool, 3), stop: true}}
		scl0, sda0 := bkI2CBuild(txns, 10, 20, 0, 20)
		rerail := func(lo, hi int) ([]uint8, []uint8) {
			m := func(src []uint8) []uint8 {
				out := make([]uint8, len(src))
				for i, v := range src {
					if v == 210 {
						out[i] = uint8(hi)
					} else {
						out[i] = uint8(lo)
					}
				}
				return out
			}
			return m(scl0), m(sda0)
		}
		// amp = 24 (> 20): should decode exactly.
		s, d := rerail(118, 142)
		if r := DecodeI2C(s, d, 2e-7, I2CCfg{}); !eqInts(r.Bytes, []int{0x39, 0xC6}) {
			t.Errorf("[EDGE amp24] bytes %v, want [57 198]; err=%q toks=%q", r.Bytes, r.Error, r.Text)
		}
		// amp = 14 (< 20): must be rejected, not silently decoded.
		s, d = rerail(121, 135)
		if r := DecodeI2C(s, d, 2e-7, I2CCfg{}); r.OK && len(r.Bytes) > 0 {
			t.Errorf("[EDGE amp14] sub-minAmp decoded bytes %v (err=%q)", r.Bytes, r.Error)
		}
	}

	// h=1 -> colsPerClock=2 < 3: must be rejected, not decoded.
	{
		txns := []bkTxn{{addr7: 0x10, rw: 0, data: []int{0x01}, naks: make([]bool, 2), stop: true}}
		scl, sda := bkI2CBuild(txns, 1, 4, 0, 4)
		r := DecodeI2C(scl, sda, 2e-7, I2CCfg{})
		if r.OK && len(r.Bytes) > 0 {
			t.Errorf("[EDGE h=1] sub-3-cols/clock still decoded bytes %v", r.Bytes)
		}
	}

	// Exactly one frame; back-to-back with NO gap; all-0x00 and all-0xFF payloads.
	{
		one := []bkTxn{{addr7: 0x3C, rw: 1, data: []int{0x00, 0xFF, 0x00, 0xFF}, naks: make([]bool, 5), stop: true}}
		scl, sda := bkI2CBuild(one, 12, 24, 0, 24)
		if r := DecodeI2C(scl, sda, 2e-7, I2CCfg{}); !eqInts(r.Bytes, []int{0x00, 0xFF, 0x00, 0xFF}) {
			t.Errorf("[EDGE one-frame] bytes %v, want [0 255 0 255]; toks=%q", r.Bytes, r.Text)
		}

		zeros := make([]int, 6)
		ffs := make([]int, 6)
		for i := range ffs {
			ffs[i] = 0xFF
		}
		b2b := []bkTxn{
			{addr7: 0x00, rw: 0, data: zeros, naks: make([]bool, 7), stop: true},
			{addr7: 0x7F, rw: 1, data: ffs, naks: make([]bool, 7), stop: true},
		}
		scl, sda = bkI2CBuild(b2b, 10, 20, 0 /*no gap*/, 20)
		r := DecodeI2C(scl, sda, 2e-7, I2CCfg{})
		wantBB := append(append([]int{}, zeros...), ffs...)
		if !eqInts(r.Bytes, wantBB) {
			t.Errorf("[EDGE b2b] bytes %v, want %v; toks=%q", r.Bytes, wantBB, r.Text)
		}
		if got := i2cCountKind(r, "start"); got != 2 {
			t.Errorf("[EDGE b2b] START count %d, want 2; toks=%q", got, r.Text)
		}
		if got := i2cAddrVals(r); !eqInts(got, []int{0x00, 0x7F}) {
			t.Errorf("[EDGE b2b] addrs %v, want [0 127]", got)
		}
	}

	// Very long record.
	{
		long := make([]int, 200)
		for i := range long {
			long[i] = (i * 7) & 0xff
		}
		txns := []bkTxn{{addr7: 0x55, rw: 0, data: long, naks: make([]bool, 1+len(long)), stop: true}}
		scl, sda := bkI2CBuild(txns, 6, 12, 0, 12)
		r := DecodeI2C(scl, sda, 2e-7, I2CCfg{})
		if !eqInts(r.Bytes, long) {
			t.Errorf("[EDGE long] %d bytes decoded (want %d) mismatch", len(r.Bytes), len(long))
		}
	}

	// Degenerate colTimeS values (I2C derives timing from the clock, so these
	// must be harmless) and pathological thresholds — must not panic.
	{
		txns := []bkTxn{{addr7: 0x24, rw: 0, data: []int{0x5A}, naks: make([]bool, 2), stop: true}}
		scl, sda := bkI2CBuild(txns, 10, 20, 0, 20)
		for _, ct := range []float64{0, -1, 1e300, math.Inf(1), math.Inf(-1), math.NaN()} {
			_ = DecodeI2C(scl, sda, ct, I2CCfg{})
		}
		for _, thr := range []float64{-1e9, 0, 125, 1e9, math.NaN()} {
			_ = DecodeI2C(scl, sda, 2e-7, I2CCfg{Threshold: thr, HaveThr: true})
		}
	}

	// Fuzz: random noise on both channels must never panic (mirrors the
	// package fuzz) and — being checksum-less — we only require no panic here.
	for it := 0; it < 60; it++ {
		n := rng.Intn(4000)
		a := make([]uint8, n)
		b := make([]uint8, n)
		for i := range a {
			a[i] = uint8(rng.Intn(256))
			b[i] = uint8(rng.Intn(256))
		}
		_ = DecodeI2C(a, b, 2e-7, I2CCfg{Format: []string{"", "hex", "dec", "bin", "ascii", "both"}[rng.Intn(6)]})
	}

	if !t.Failed() {
		t.Logf("all I2C attack classes survived")
	}
}