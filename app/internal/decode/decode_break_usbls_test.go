package decode

import (
	"fmt"
	"math/rand"
	"testing"
)

// decode_break_usbls_test.go — adversarial red-team suite for DecodeUSBLS.
//
// It reuses the round-trip synthesizer (usbWave / usbPkt in decode_usbls_test.go)
// and adds a low-level bit synthesizer (brkWave) that lets us control the exact
// on-wire bit stream: idle padding, inter-packet gap, a corrupted SYNC, or a
// wrong PID complement.
//
// THREE attack classes (>=50 iterations each) plus edge/no-panic coverage, and a
// gated block that pins two REAL findings surfaced by this campaign:
//
//	FINDING 1 (false-negative / false-positive): DecodeUSBLS splits packets only
//	on an inter-packet idle wider than splitK=10 bit-times. Real USB LS/FS
//	captures place consecutive packets ~2 bit-times apart, so two well-formed,
//	adjacent packets MERGE into one segment: the decoder returns OK=true with a
//	payload that matches NEITHER packet and only ONE SYNC span (the 2nd packet is
//	lost). It neither recovers both frames nor reports an error — it fabricates
//	bytes and calls them valid.
//
//	FINDING 2 (false-positive): DecodeUSBLS performs NO USB CRC5/CRC16 check. A
//	frame whose CRC field is corrupted is emitted byte-for-byte as data with
//	OK=true and no error flagged. The only integrity checks are the SYNC pattern
//	and the PID ones-complement (which ARE enforced — a corrupted PID is flagged
//	frame-error, a corrupted SYNC is dropped).
//
// The known-bug assertions are guarded by pinKnownBugs so the rest of the suite
// runs green; flip it to true (or fix the decoder) to enforce correct behavior.
const pinKnownBugs = false

// brkWave NRZI-encodes a list of packets, each given as its raw logical bit list
// (SYNC + PID + data..., LSB-first per byte). It bit-stuffs (a 0 after six 1s),
// NRZI-encodes from idle J (high), appends a 2-bit SE0 EOP + `gap` idle bits
// after every packet, and `lead`/`trail` idle bits at the ends. hi=210, lo=40.
func brkWave(bitLists [][]int, spb, lead, gap, trail int) []uint8 {
	lo, hi := uint8(40), uint8(210)
	const idle = 1
	var w []uint8
	push := func(level, cells int) {
		v := lo
		if level == 1 {
			v = hi
		}
		for j := 0; j < cells*spb; j++ {
			w = append(w, v)
		}
	}
	push(idle, lead)
	for _, bits := range bitLists {
		var stuffed []int
		ones := 0
		for _, b := range bits {
			stuffed = append(stuffed, b)
			if b == 1 {
				if ones++; ones == 6 {
					stuffed = append(stuffed, 0)
					ones = 0
				}
			} else {
				ones = 0
			}
		}
		level := idle
		for _, b := range stuffed {
			if b == 0 {
				level = 1 - level
			}
			push(level, 1)
		}
		push(0, 2) // EOP: 2 bit-times of SE0 (D+ low)
		push(idle, gap)
	}
	push(idle, trail)
	return w
}

func brkSync() []int { return []int{0, 0, 0, 0, 0, 0, 0, 1} }

func brkBits(v, n int) []int {
	b := make([]int, n)
	for i := 0; i < n; i++ {
		b[i] = (v >> i) & 1
	}
	return b
}

// brkFrame builds SYNC + a correct PID byte (nibble + ones-complement) + data.
func brkFrame(pid int, data []int) []int {
	pidByte := (pid & 0xF) | ((^pid & 0xF) << 4)
	bits := append([]int{}, brkSync()...)
	bits = append(bits, brkBits(pidByte, 8)...)
	for _, d := range data {
		bits = append(bits, brkBits(d, 8)...)
	}
	return bits
}

func brkEqInts(a, b []int) bool {
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

func brkIdle(n int) []uint8 {
	s := make([]uint8, n)
	for i := range s {
		s[i] = 210
	}
	return s
}

// brkValidPIDs is the set of well-defined PID nibbles (all map to a name).
var brkValidPIDs = []int{0x1, 0x9, 0x5, 0xD, 0x3, 0xB, 0x7, 0xF, 0x2, 0xA, 0xE, 0x6, 0xC, 0x8, 0x4}

func TestBreakUsbls(t *testing.T) {

	// ---------------------------------------------------------------------
	// CLASS 1 — FALSE NEGATIVES: >=50 fully valid frames must round-trip
	// exactly. Vary payloads, spb across the legal range, single vs back-to-
	// back packets (with an in-contract >10-bit idle), explicit vs auto
	// bitrate, and a realistic random idle before/after the transmission.
	// ---------------------------------------------------------------------
	t.Run("false_negatives", func(t *testing.T) {
		rng := rand.New(rand.NewSource(0xF00D))
		spbs := []int{4, 5, 6, 7, 8, 10, 13, 16, 20, 32, 40, 64, 100}
		for iter := 0; iter < 60; iter++ {
			spb := spbs[rng.Intn(len(spbs))]
			colTimeS := 1.0 / (float64(spb) * 1.5e6)
			bitrate := 1500000

			npk := 1 + rng.Intn(3)
			var pkts []usbPkt
			var bitLists [][]int
			var want []int
			var wantNames []string
			for p := 0; p < npk; p++ {
				pid := brkValidPIDs[rng.Intn(len(brkValidPIDs))]
				nd := rng.Intn(7)
				var data []int
				for d := 0; d < nd; d++ {
					data = append(data, rng.Intn(256))
				}
				pkts = append(pkts, usbPkt{pid: pid, data: data})
				bitLists = append(bitLists, brkFrame(pid, data))
				want = append(want, data...)
				wantNames = append(wantNames, usbPIDName[pid])
			}

			lead := 5 + rng.Intn(200)   // realistic leading idle (never starts on a frame)
			gap := 12 + rng.Intn(48)    // in-contract inter-packet idle (>10 bit-times)
			trail := 12 + rng.Intn(200) // realistic trailing idle
			w := brkWave(bitLists, spb, lead, gap, trail)

			for _, mode := range []string{"explicit", "auto"} {
				cfg := USBLSCfg{Bitrate: bitrate}
				if mode == "auto" {
					cfg = USBLSCfg{}
				}
				r := DecodeUSBLS(w, colTimeS, cfg)
				if !r.OK {
					t.Errorf("FALSE NEGATIVE [%s] iter=%d spb=%d pids=%v: ok=false err=%q",
						mode, iter, spb, pidsList(pkts), r.Error)
					continue
				}
				if !brkEqInts(r.Bytes, want) {
					t.Errorf("FALSE NEGATIVE [%s] iter=%d spb=%d: payload got=%v want=%v",
						mode, iter, spb, r.Bytes, want)
				}
				var gotNames []string
				for _, s := range r.Spans {
					if s.Kind == "addr" || s.Kind == "frame-error" {
						gotNames = append(gotNames, s.Text)
					}
				}
				if fmt.Sprint(gotNames) != fmt.Sprint(wantNames) {
					t.Errorf("FALSE NEGATIVE [%s] iter=%d: PID names got=%v want=%v",
						mode, iter, gotNames, wantNames)
				}
			}
		}
	})

	// ---------------------------------------------------------------------
	// CLASS 2 — FALSE POSITIVES: >=50 non-frames must NOT decode as a valid
	// packet: noise, flat DC, a slow ramp, a Nyquist toggle, wrong-protocol
	// square/UART waves. Plus deterministic corruption checks: a flipped SYNC
	// bit must be dropped (not decoded), and a wrong PID complement must be
	// FLAGGED frame-error (not silently accepted as a clean PID).
	// ---------------------------------------------------------------------
	t.Run("false_positives", func(t *testing.T) {
		rng := rand.New(rand.NewSource(0xBADC0DE))
		cts := []float64{1e-8, 1.0 / (20 * 1.5e6)}
		fp := 0
		for iter := 0; iter < 60; iter++ {
			kind := iter % 6
			n := 300 + rng.Intn(4000)
			s := make([]uint8, n)
			switch kind {
			case 0: // random noise
				for i := range s {
					s[i] = uint8(rng.Intn(256))
				}
			case 1: // flat DC
				dc := uint8(rng.Intn(256))
				for i := range s {
					s[i] = dc
				}
			case 2: // slow ramp
				for i := range s {
					s[i] = uint8(i * 255 / n)
				}
			case 3: // Nyquist toggle
				for i := range s {
					s[i] = uint8((i % 2) * 255)
				}
			case 4: // wrong-protocol square wave, random period
				per := 5 + rng.Intn(70)
				for i := range s {
					if (i/per)%2 == 0 {
						s[i] = 210
					} else {
						s[i] = 40
					}
				}
			case 5: // UART-like framed bytes at spb=16 (start=low, stop=high)
				spb, idx := 16, 0
				for idx < n {
					for k := 0; k < spb*3 && idx < n; k++ {
						s[idx] = 210
						idx++
					}
					for k := 0; k < spb && idx < n; k++ {
						s[idx] = 40
						idx++
					}
					b := rng.Intn(256)
					for bit := 0; bit < 8; bit++ {
						lv := uint8(40)
						if (b>>bit)&1 == 1 {
							lv = 210
						}
						for k := 0; k < spb && idx < n; k++ {
							s[idx] = lv
							idx++
						}
					}
					for k := 0; k < spb && idx < n; k++ {
						s[idx] = 210
						idx++
					}
				}
			}
			for _, ct := range cts {
				for _, br := range []int{0, 1500000} {
					r := DecodeUSBLS(s, ct, USBLSCfg{Bitrate: br})
					if r.OK {
						fp++
						t.Errorf("FALSE POSITIVE iter=%d kind=%d ct=%g br=%d: garbage decoded ok=true bytes=%v",
							iter, kind, ct, br, r.Bytes)
					}
				}
			}
		}
		if fp == 0 {
			t.Logf("no false positives across %d adversarial inputs (noise/DC/ramp/nyquist/square/UART)", 60)
		}

		// Deterministic corruption checks -------------------------------------
		spb := 20
		colTimeS := 1.0 / (float64(spb) * 1.5e6)

		// (a) Corrupted SYNC: flip SYNC bit 3 (0 -> 1). The frame must be dropped.
		badSync := append([]int{}, brkFrame(0xD, []int{0x11, 0x22})...)
		badSync[3] = 1
		rs := DecodeUSBLS(brkWave([][]int{badSync}, spb, 20, 40, 40), colTimeS, USBLSCfg{})
		if rs.OK {
			t.Errorf("FALSE POSITIVE: frame with a corrupted SYNC decoded ok=true bytes=%v", rs.Bytes)
		}

		// (b) Truncated mid-frame (cut inside the PID, before EOP/idle): no packet.
		full := usbWave([]usbPkt{{pid: 0xD, data: []int{0x12, 0x34, 0x56}}}, spb)
		// lead idle (20 bits) + SYNC(8) lands the PID around bit 28..35 -> ~ (20+12)*spb.
		cut := (20 + 12) * spb // mid-PID
		rt := DecodeUSBLS(full[:cut], colTimeS, USBLSCfg{})
		if rt.OK {
			t.Errorf("FALSE POSITIVE: mid-PID truncation decoded ok=true bytes=%v", rt.Bytes)
		}

		// (c) Corrupted PID complement: MUST be flagged frame-error, not accepted
		// as a clean valid PID. (Decoder still returns OK=true, but the span is
		// marked frame-error and the token is prefixed "!".)
		good := (0xD & 0xF) | ((^0xD & 0xF) << 4)
		badPIDbits := append([]int{}, brkSync()...)
		badPIDbits = append(badPIDbits, brkBits(good^0x10, 8)...) // flip a complement bit
		badPIDbits = append(badPIDbits, brkBits(0x11, 8)...)
		rp := DecodeUSBLS(brkWave([][]int{badPIDbits}, spb, 20, 40, 40), colTimeS, USBLSCfg{})
		flagged := false
		for _, sp := range rp.Spans {
			if sp.Kind == "frame-error" {
				flagged = true
			}
		}
		if !flagged {
			t.Errorf("FALSE POSITIVE: corrupted PID complement NOT flagged (spans=%+v)", rp.Spans)
		}
	})

	// ---------------------------------------------------------------------
	// CLASS 3 — EDGE CASES: min/max legal bit rate, exactly one frame, in-
	// contract back-to-back frames, all-0x00 / all-0xFF payloads, shortest &
	// longest records, boundary sample counts, and hostile colTimeS/bitrate.
	// Assert no panic and sane behavior.
	// ---------------------------------------------------------------------
	t.Run("edge_cases", func(t *testing.T) {
		noPanic := func(name string, fn func()) {
			defer func() {
				if p := recover(); p != nil {
					t.Errorf("PANIC in %s: %v", name, p)
				}
			}()
			fn()
		}

		// min spb (=4, max bit rate) and a large spb (min bit rate).
		for _, spb := range []int{4, 5, 1000} {
			colTimeS := 1.0 / (float64(spb) * 1.5e6)
			w := brkWave([][]int{brkFrame(0x3, []int{0x5A})}, spb, 10, 40, 40)
			noPanic(fmt.Sprintf("spb=%d", spb), func() {
				r := DecodeUSBLS(w, colTimeS, USBLSCfg{})
				if !r.OK || !brkEqInts(r.Bytes, []int{0x5A}) {
					t.Errorf("edge spb=%d: ok=%v bytes=%v err=%q", spb, r.OK, r.Bytes, r.Error)
				}
			})
		}

		// exactly one minimal frame (ACK: SYNC+PID+EOP, no data).
		{
			spb := 20
			colTimeS := 1.0 / (float64(spb) * 1.5e6)
			r := DecodeUSBLS(usbWave([]usbPkt{{pid: 0x2}}, spb), colTimeS, USBLSCfg{})
			if !r.OK || len(r.Bytes) != 0 {
				t.Errorf("edge single ACK: ok=%v bytes=%v", r.OK, r.Bytes)
			}
		}

		// in-contract back-to-back frames (gap=12 > splitK) must both decode.
		{
			spb := 20
			colTimeS := 1.0 / (float64(spb) * 1.5e6)
			bl := [][]int{brkFrame(0xD, []int{0x11}), brkFrame(0x3, []int{0x22, 0x33}), brkFrame(0x2, nil)}
			r := DecodeUSBLS(brkWave(bl, spb, 20, 12, 40), colTimeS, USBLSCfg{})
			syncs := 0
			for _, sp := range r.Spans {
				if sp.Kind == "start" {
					syncs++
				}
			}
			if !r.OK || !brkEqInts(r.Bytes, []int{0x11, 0x22, 0x33}) || syncs != 3 {
				t.Errorf("edge back-to-back(gap=12): ok=%v bytes=%v syncs=%d", r.OK, r.Bytes, syncs)
			}
		}

		// all-0x00 and all-0xFF payloads (the latter forces heavy bit stuffing).
		for _, d := range [][]int{{0x00, 0x00, 0x00}, {0xFF, 0xFF, 0xFF}} {
			spb := 16
			colTimeS := 1.0 / (float64(spb) * 1.5e6)
			r := DecodeUSBLS(brkWave([][]int{brkFrame(0x3, d)}, spb, 12, 40, 40), colTimeS, USBLSCfg{})
			if !r.OK || !brkEqInts(r.Bytes, d) {
				t.Errorf("edge payload %v: ok=%v bytes=%v", d, r.OK, r.Bytes)
			}
		}

		// longest record: many back-to-back packets.
		{
			spb := 8
			colTimeS := 1.0 / (float64(spb) * 1.5e6)
			var bl [][]int
			var want []int
			for i := 0; i < 40; i++ {
				d := []int{i & 0xFF, (i * 7) & 0xFF}
				bl = append(bl, brkFrame(0x3, d))
				want = append(want, d...)
			}
			r := DecodeUSBLS(brkWave(bl, spb, 20, 20, 40), colTimeS, USBLSCfg{})
			if !r.OK || !brkEqInts(r.Bytes, want) {
				t.Errorf("edge long record: ok=%v len(bytes)=%d want=%d", r.OK, len(r.Bytes), len(want))
			}
		}

		// boundary sample counts + hostile colTimeS/bitrate: never panic, and a
		// degenerate/hostile input must set OK=false or a non-empty Error.
		degenerate := [][]uint8{nil, {}, {200}, {40, 210}, {40, 210, 40}}
		validW := brkWave([][]int{brkFrame(0xD, []int{0xAB})}, 20, 20, 40, 40)
		cts := []float64{0, -1, 1e-12, 1e-9, 1e6}
		brs := []int{0, -100, 1500000, 1 << 30}
		for _, in := range append(degenerate, validW) {
			for _, ct := range cts {
				for _, br := range brs {
					noPanic(fmt.Sprintf("len=%d ct=%g br=%d", len(in), ct, br), func() {
						r := DecodeUSBLS(in, ct, USBLSCfg{Bitrate: br})
						if !r.OK && r.Error == "" {
							t.Errorf("edge hostile: neither OK nor Error (len=%d ct=%g br=%d)", len(in), ct, br)
						}
					})
				}
			}
		}
	})

	// ---------------------------------------------------------------------
	// KNOWN BUGS (guarded by pinKnownBugs so the suite stays green; reported
	// separately). Logs run unconditionally so the true behavior is on record.
	// ---------------------------------------------------------------------
	t.Run("known_bug_tight_interpacket_gap", func(t *testing.T) {
		spb := 20
		colTimeS := 1.0 / (float64(spb) * 1.5e6)
		// Two well-formed packets 2 bit-times apart (a legal USB inter-packet
		// gap). p1 = SETUP + [0x11]; p2 = ACK (no data). Correct output: two SYNC
		// spans and payload [0x11].
		bl := [][]int{brkFrame(0xD, []int{0x11}), brkFrame(0x2, nil)}
		r := DecodeUSBLS(brkWave(bl, spb, 20, 2, 40), colTimeS, USBLSCfg{})
		syncs := 0
		for _, sp := range r.Spans {
			if sp.Kind == "start" {
				syncs++
			}
		}
		t.Logf("gap=2bit: ok=%v syncSpans=%d bytes=%v (want syncs=2, bytes=[17]) -- MERGE BUG",
			r.OK, syncs, r.Bytes)
		if pinKnownBugs {
			if syncs != 2 || !brkEqInts(r.Bytes, []int{0x11}) {
				t.Errorf("KNOWN BUG (tight gap merge): two legal packets 2 bit-times apart "+
					"decoded as ok=%v syncSpans=%d bytes=%v; want 2 SYNC spans and [0x11] (or an error)",
					r.OK, syncs, r.Bytes)
			}
		}
	})

	t.Run("known_bug_crc_not_validated", func(t *testing.T) {
		spb := 20
		colTimeS := 1.0 / (float64(spb) * 1.5e6)
		// DATA0 + 2 data bytes + a 2-byte "CRC"; corrupt the low CRC byte.
		base := append(brkSync(), brkBits((0x3&0xF)|((^0x3&0xF)<<4), 8)...)
		base = append(base, brkBits(0xAA, 8)...)
		base = append(base, brkBits(0x55, 8)...)
		bad := append(append([]int{}, base...), brkBits(0x12^0xFF, 8)...) // corrupted CRC lo
		bad = append(bad, brkBits(0x34, 8)...)
		r := DecodeUSBLS(brkWave([][]int{bad}, spb, 20, 40, 40), colTimeS, USBLSCfg{})
		errFlagged := false
		for _, sp := range r.Spans {
			if sp.Kind == "frame-error" || sp.Kind == "parity-error" {
				errFlagged = true
			}
		}
		t.Logf("corrupted CRC: ok=%v errFlagged=%v bytes=%v -- decoder does NOT validate CRC",
			r.OK, errFlagged, r.Bytes)
		if pinKnownBugs {
			if r.OK && !errFlagged {
				t.Errorf("KNOWN BUG (no CRC check): a frame with a corrupted CRC decoded ok=true "+
					"with no error flag (bytes=%v); USB CRC16/CRC5 is never checked", r.Bytes)
			}
		}
	})
}

func pidsList(pkts []usbPkt) []int {
	var o []int
	for _, p := range pkts {
		o = append(o, p.pid)
	}
	return o
}
