package decode

// FlexRay vs the sigrok `flexray` decoder. The two sides expose different
// granularity: the repo decoder emits the raw on-wire byte stream (5 header
// bytes + payload + 3 frame-CRC bytes) plus one "ID=.. LEN=.. CYC=.." header
// note per frame and verifies ONLY the 11-bit header CRC; sigrok splits the
// frame into typed fields (id/length/cycle/header-crc/data-byte/frame-crc)
// and verifies BOTH CRCs. The tests therefore compare the intersection —
// frame id, payload length, cycle count, payload bytes, header-CRC verdict —
// strictly, and use sigrok alone as the referee for the 24-bit frame CRC
// (checking that the generator's CRC-24 is one sigrok accepts, and that a
// corrupted one is rejected). sigrok reads the header LEN field and consumes
// exactly 2*LEN data bytes, so every generated frame is a well-formed
// static-segment frame: LEN = payloadBytes/2, valid CRC-24 trailer, FES.
//
// sigrok quirks accommodated (probed against sigrok-cli 0.7.2 / the flexray
// PD shipped with libsigrokdecode 0.5.x):
//   - bitrate is an enum option (10/5/2.5 Mbit/s), so the waves run at the
//     real 10 Mbit/s and the repo decoder gets the same rate pinned.
//   - after the FES the PD keeps consuming ~13 bit-times (DTS probe + 11-bit
//     channel-idle delimiter) before it re-arms for the next TSS, so frames
//     are separated by 14 idle bits — less and sigrok silently eats frame 2.
//   - annotation texts are prose ("Frame ID: 291", "Data byte 0: 0xde",
//     "Header CRC: 0x12F (OK|bad)"); orFlexAnn* parse the trailing values.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// orFlexCRC24 is the FlexRay 24-bit frame CRC (poly 0x5D6DCB, init 0xFEDCBA =
// channel A) over the 5 header bytes + payload, MSB-first — the trailer a real
// channel-A node transmits. The repo decoder does not compute this; it exists
// here so the generator can seal frames that sigrok's frame-CRC check accepts.
func orFlexCRC24(bytes []int) int {
	crc := 0xFEDCBA
	for _, b := range bytes {
		for d := 7; d >= 0; d-- {
			bit := ((crc >> 23) & 1) ^ ((b >> d) & 1)
			crc = (crc << 1) & 0xFFFFFF
			if bit == 1 {
				crc ^= 0x5D6DCB
			}
		}
	}
	return crc
}

// orFlexFrameFlags builds the full on-wire byte list of one frame with the
// sync/startup indicator bits under caller control: header (flags with NF=1
// "data frame", frameID, LEN=len(payload)/2, valid header CRC-11, cycle) +
// payload + valid frame CRC-24. sync/startup are bits 0..1 of the 20
// CRC-11-protected header bits, so BOTH CRCs are sealed over their actual
// values. Reuses the package's header packers (brFlexHeaderBytes/
// brFlexHeaderCRC11); payload length must be even because the header LEN
// field counts 2-byte words and sigrok consumes exactly 2*LEN data bytes.
func orFlexFrameFlags(sync, startup, frameID, cycle int, payload []int) []int {
	plen := len(payload) / 2
	hdr := brFlexHeaderBytes(sync, startup, frameID, plen, brFlexHeaderCRC11(sync, startup, frameID, plen), cycle)
	hdr[0] |= 0x20 // NF=1 (bit 37): a data frame, not a null frame; outside the header CRC
	body := append(hdr, payload...)
	c := orFlexCRC24(body)
	return append(body, (c>>16)&0xFF, (c>>8)&0xFF, c&0xFF)
}

// orFlexFrame is orFlexFrameFlags with sync=0/startup=0 — a plain
// static-segment data frame, the shape most subtests use.
func orFlexFrame(frameID, cycle int, payload []int) []int {
	return orFlexFrameFlags(0, 0, frameID, cycle, payload)
}

// orFlexCorruptHeaderCRC flips one bit of the transmitted header-CRC field
// (bit 8 of the 40-bit header = CRC bit 2) and re-seals the frame CRC-24 over
// the corrupted header, so the header CRC is the ONLY defect in the frame —
// sigrok's frame-crc verdict must stay OK while header-crc goes bad.
func orFlexCorruptHeaderCRC(fb []int) []int {
	out := append([]int(nil), fb...)
	out[3] ^= 0x01
	c := orFlexCRC24(out[:len(out)-3])
	out[len(out)-3], out[len(out)-2], out[len(out)-1] = (c>>16)&0xFF, (c>>8)&0xFF, c&0xFF
	return out
}

// orFlexBits renders frames on the single-ended bus line (idle HIGH): TSS (LOW
// run) -> FSS (1 HIGH) -> per byte BSS (HIGH,LOW) + 8 data bits MSB-first ->
// FES (LOW,HIGH) -> idle. Same shape as flexrayWave, but on the timeline so it
// yields the logic bits sigrok consumes.
func orFlexBits(sr, bitrate float64, frames [][]int, tssBits, idleBits float64) []byte {
	w := newTimeline(sr)
	bt := 1 / bitrate
	w.add(1, 8*bt) // lead idle
	for _, fb := range frames {
		w.add(0, tssBits*bt) // TSS
		w.add(1, bt)         // FSS
		for _, b := range fb {
			w.add(1, bt) // BSS bit0
			w.add(0, bt) // BSS bit1
			for d := 7; d >= 0; d-- {
				w.add(byte(b>>d)&1, bt)
			}
		}
		w.add(0, bt)          // FES low
		w.add(1, bt)          // FES high
		w.add(1, idleBits*bt) // inter-frame idle (>= 14: sigrok's DTS+CID tail)
	}
	w.add(1, 8*bt) // trail idle
	return w.bits
}

// orFlexBitsDTS renders ONE dynamic-segment frame: identical to orFlexBits up
// through the FES, but instead of going idle the node keeps the bus LOW for
// dtsLowBits — the Dynamic Trailing Sequence that pads the minislot out to
// the next action point — then releases with one HIGH bit before true idle.
// That LOW run is deliberately longer than a minimum TSS (>= 4 bit-times):
// the very shape a naive scanner could mistake for the start of a new frame.
func orFlexBitsDTS(sr, bitrate float64, fb []int, tssBits, dtsLowBits, idleBits float64) []byte {
	w := newTimeline(sr)
	bt := 1 / bitrate
	w.add(1, 8*bt)       // lead idle
	w.add(0, tssBits*bt) // TSS
	w.add(1, bt)         // FSS
	for _, b := range fb {
		w.add(1, bt) // BSS bit0
		w.add(0, bt) // BSS bit1
		for d := 7; d >= 0; d-- {
			w.add(byte(b>>d)&1, bt)
		}
	}
	w.add(0, bt)            // FES low
	w.add(1, bt)            // FES high
	w.add(0, dtsLowBits*bt) // DTS low phase
	w.add(1, bt)            // DTS closing high bit
	w.add(1, idleBits*bt)   // channel idle (covers sigrok's 11-bit CID)
	w.add(1, 8*bt)          // trail idle
	return w.bits
}

var (
	orFlexIntRe = regexp.MustCompile(`: (\d+)$`)                       // "Frame ID: 291" / "Payload length: 2" / "Cycle: 17"
	orFlexHexRe = regexp.MustCompile(`0x([0-9a-fA-F]+)$`)              // "Data byte 0: 0xde"
	orFlexCRCRe = regexp.MustCompile(`0x([0-9a-fA-F]+) \((OK|bad)\)$`) // "Header CRC: 0x12F (OK)"
)

// orFlexAnnInts pulls the trailing decimal out of prose annotations.
func orFlexAnnInts(t *testing.T, anns []ann) []int {
	t.Helper()
	out := make([]int, 0, len(anns))
	for _, a := range anns {
		m := orFlexIntRe.FindStringSubmatch(a.Text)
		if m == nil {
			t.Fatalf("annotation %q has no trailing integer", a.Text)
		}
		v, _ := strconv.Atoi(m[1])
		out = append(out, v)
	}
	return out
}

// orFlexAnnHex pulls the trailing 0x… value out of data-byte annotations.
func orFlexAnnHex(t *testing.T, anns []ann) []int {
	t.Helper()
	out := make([]int, 0, len(anns))
	for _, a := range anns {
		m := orFlexHexRe.FindStringSubmatch(a.Text)
		if m == nil {
			t.Fatalf("annotation %q has no trailing hex value", a.Text)
		}
		v, _ := strconv.ParseInt(m[1], 16, 32)
		out = append(out, int(v))
	}
	return out
}

// orFlexAnnCRCs parses "… CRC: 0x<val> (OK|bad)" annotations into transmitted
// values and pass/fail verdicts.
func orFlexAnnCRCs(t *testing.T, anns []ann) (vals []int, oks []bool) {
	t.Helper()
	for _, a := range anns {
		m := orFlexCRCRe.FindStringSubmatch(a.Text)
		if m == nil {
			t.Fatalf("annotation %q is not a CRC verdict", a.Text)
		}
		v, _ := strconv.ParseInt(m[1], 16, 32)
		vals = append(vals, int(v))
		oks = append(oks, m[2] == "OK")
	}
	return vals, oks
}

// orFlexNotes parses the repo header notes ("ID=%d LEN=%d CYC=%d", with a
// "!CRC " prefix when the header CRC failed) of the given span kind into
// (id, len, cycle) triples, in frame order.
func orFlexNotes(t *testing.T, r Result, kind string) (ids, lens, cycs []int) {
	t.Helper()
	for _, s := range r.Spans {
		if s.Kind != kind {
			continue
		}
		txt := strings.TrimPrefix(s.Text, "!CRC ")
		var id, ln, cy int
		if _, err := fmt.Sscanf(txt, "ID=%d LEN=%d CYC=%d", &id, &ln, &cy); err != nil {
			t.Fatalf("header note %q does not parse: %v", s.Text, err)
		}
		ids, lens, cycs = append(ids, id), append(lens, ln), append(cycs, cy)
	}
	return ids, lens, cycs
}

// orFlexDataSpans returns the repo's per-byte data spans in stream order (the
// repo emits one for EVERY on-wire byte: 5 header, payload, 3 frame-CRC).
func orFlexDataSpans(r Result) []Span {
	var out []Span
	for _, s := range r.Spans {
		if s.Kind == "data" {
			out = append(out, s)
		}
	}
	return out
}

func TestOracleFlexRay(t *testing.T) {
	needSigrok(t)
	const sr = 200_000_000 // 200 MSa/s
	const br = 10_000_000  // 10 Mbit/s — sigrok's bitrate option is an enum {10M,5M,2.5M}
	const spb = sr / br    // 20 samples/bit
	const colTime = 1.0 / sr
	const pdSpec = "flexray:channel=CH:bitrate=10000000"

	run := func(t *testing.T, bits []byte, class string) []ann {
		return sigrokDecode(t, sr, []string{"CH"}, [][]byte{bits}, pdSpec, "flexray="+class)
	}
	decode := func(t *testing.T, bits []byte) Result {
		r := DecodeFlexRay(bitsToCodes(bits), colTime, FlexRayCfg{Bitrate: br})
		if r.Error != "" && !r.OK {
			t.Logf("repo decode error: %s", r.Error)
		}
		return r
	}

	t.Run("static-frame-fields", func(t *testing.T) {
		frameID, cycle := 291, 17
		payload := []int{0xDE, 0xAD, 0xBE, 0xEF} // LEN=2 words
		fb := orFlexFrame(frameID, cycle, payload)
		bits := orFlexBits(sr, br, [][]int{fb}, 8, 14)

		r := decode(t, bits)
		if !r.OK {
			t.Fatalf("repo decode failed: %s", r.Error)
		}
		// The repo emits the raw stream: header + payload + frame CRC, byte-exact.
		eqBytes(t, "on-wire bytes", r.Bytes, fb)

		// Header fields: repo note vs sigrok's typed annotations vs constructed truth.
		ids, lens, cycs := orFlexNotes(t, r, "addr")
		sIDs := orFlexAnnInts(t, run(t, bits, "id"))
		sLens := orFlexAnnInts(t, run(t, bits, "length"))
		sCycs := orFlexAnnInts(t, run(t, bits, "cycle"))
		if len(ids) != 1 || len(sIDs) != 1 {
			t.Fatalf("frame counts differ: repo %d notes, sigrok %d ids", len(ids), len(sIDs))
		}
		if ids[0] != frameID || sIDs[0] != frameID {
			t.Fatalf("frame id: repo %d, sigrok %d, want %d", ids[0], sIDs[0], frameID)
		}
		if wantLen := len(payload) / 2; lens[0] != wantLen || sLens[0] != wantLen {
			t.Fatalf("payload length: repo %d, sigrok %d, want %d", lens[0], sLens[0], wantLen)
		}
		if cycs[0] != cycle || sCycs[0] != cycle {
			t.Fatalf("cycle: repo %d, sigrok %d, want %d", cycs[0], sCycs[0], cycle)
		}

		// Header CRC: both sides pass; sigrok's transmitted value must equal the
		// repo's own CRC-11 (same 20 protected bits, same poly/init).
		hVals, hOKs := orFlexAnnCRCs(t, run(t, bits, "header-crc"))
		if len(hOKs) != 1 || !hOKs[0] {
			t.Fatalf("sigrok header CRC verdict: %v, want one OK", hOKs)
		}
		if want := brFlexHeaderCRC11(0, 0, frameID, len(payload)/2); hVals[0] != want {
			t.Fatalf("header CRC value: sigrok 0x%X, repo CRC-11 0x%X", hVals[0], want)
		}
		if n := countSpans(r, "frame-error"); n != 0 {
			t.Fatalf("repo flagged %d header CRC errors on a clean frame", n)
		}

		// Frame CRC: repo has no CRC-24 check, so sigrok alone referees the
		// generator's trailer — it must accept it and echo the transmitted value.
		fVals, fOKs := orFlexAnnCRCs(t, run(t, bits, "frame-crc"))
		if len(fOKs) != 1 || !fOKs[0] {
			t.Fatalf("sigrok frame CRC verdict: %v, want one OK", fOKs)
		}
		if want := orFlexCRC24(fb[:len(fb)-3]); fVals[0] != want {
			t.Fatalf("frame CRC value: sigrok 0x%X, generator 0x%X", fVals[0], want)
		}

		// Payload bytes agree on both sides.
		dataAnns := run(t, bits, "data-byte")
		eqBytes(t, "payload (sigrok)", orFlexAnnHex(t, dataAnns), payload)
		eqBytes(t, "payload (repo)", r.Bytes[5:5+len(payload)], payload)

		// Placement: TSS spans coincide; the repo's per-byte spans start at the
		// byte's BSS while sigrok's data-byte annotations start at the first data
		// bit — a fixed 2-bit offset.
		eqAligned(t, "TSS", r, "start", run(t, bits, "tss"), spb)
		pSpans := orFlexDataSpans(r)[5 : 5+len(payload)]
		for i := range pSpans {
			if d := pSpans[i].I0 + 2*spb - dataAnns[i].I0; d > spb || d < -spb {
				t.Fatalf("payload byte %d misaligned: repo BSS at %d, sigrok data at %d", i, pSpans[i].I0, dataAnns[i].I0)
			}
		}
	})

	// BSS boundary stress: an all-0x00 payload makes each byte one long LOW run
	// (BSS low + 8 zeros = 9 bit-times LOW, longer than a legal minimum TSS), an
	// all-0xFF payload fuses each byte's data HIGH run into the next byte's BSS
	// HIGH bit — both must re-lock on the BSS on both sides.
	for _, tc := range []struct {
		name string
		fill int
	}{
		{"payload-zeros", 0x00},
		{"payload-ones", 0xFF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := []int{tc.fill, tc.fill, tc.fill, tc.fill, tc.fill, tc.fill} // LEN=3
			fb := orFlexFrame(1023, 62, payload)
			bits := orFlexBits(sr, br, [][]int{fb}, 8, 14)

			r := decode(t, bits)
			if !r.OK {
				t.Fatalf("repo decode failed: %s", r.Error)
			}
			eqBytes(t, "on-wire bytes", r.Bytes, fb)
			eqBytes(t, "payload (sigrok)", orFlexAnnHex(t, run(t, bits, "data-byte")), payload)

			_, hOKs := orFlexAnnCRCs(t, run(t, bits, "header-crc"))
			_, fOKs := orFlexAnnCRCs(t, run(t, bits, "frame-crc"))
			if len(hOKs) != 1 || !hOKs[0] || len(fOKs) != 1 || !fOKs[0] {
				t.Fatalf("sigrok CRC verdicts: header %v frame %v, want one OK each", hOKs, fOKs)
			}
			if n := countSpans(r, "frame-error"); n != 0 {
				t.Fatalf("repo flagged %d CRC errors on a clean frame", n)
			}
		})
	}

	t.Run("header-crc-corrupted-flagged-by-both", func(t *testing.T) {
		fb := orFlexFrame(291, 17, []int{0xDE, 0xAD, 0xBE, 0xEF})
		bad := orFlexCorruptHeaderCRC(fb) // ONLY the transmitted CRC-11 is wrong
		bits := orFlexBits(sr, br, [][]int{bad}, 8, 14)

		// sigrok: header-crc goes bad, frame-crc (re-sealed over the corrupted
		// header) stays OK — proving the corruption is isolated to the header CRC.
		hVals, hOKs := orFlexAnnCRCs(t, run(t, bits, "header-crc"))
		if len(hOKs) != 1 || hOKs[0] {
			t.Fatalf("sigrok header CRC verdict: %v, want one bad", hOKs)
		}
		// bit 8 of the header is CRC bit 2, so the transmitted value is valid^0x004.
		if want := brFlexHeaderCRC11(0, 0, 291, 2) ^ 0x004; hVals[0] != want {
			t.Fatalf("transmitted header CRC: sigrok 0x%X, want 0x%X", hVals[0], want)
		}
		_, fOKs := orFlexAnnCRCs(t, run(t, bits, "frame-crc"))
		if len(fOKs) != 1 || !fOKs[0] {
			t.Fatalf("sigrok frame CRC verdict: %v, want one OK (corruption must be isolated)", fOKs)
		}

		// repo: the frame is flagged (frame-error note, no addr note) and a record
		// whose only frame has a bad header CRC must not decode as OK.
		r := decode(t, bits)
		if r.OK {
			t.Fatal("repo returned OK for a frame with a corrupted header CRC")
		}
		if countSpans(r, "frame-error") != 1 || countSpans(r, "addr") != 0 {
			t.Fatalf("repo spans: %d frame-error, %d addr; want 1 and 0",
				countSpans(r, "frame-error"), countSpans(r, "addr"))
		}
		// Both sides still expose the (untrustworthy but well-framed) bytes.
		eqBytes(t, "bytes despite bad header CRC", r.Bytes, bad)
		eqBytes(t, "payload (sigrok)", orFlexAnnHex(t, run(t, bits, "data-byte")), []int{0xDE, 0xAD, 0xBE, 0xEF})
	})

	t.Run("frame-crc-corrupted", func(t *testing.T) {
		fb := orFlexFrame(291, 17, []int{0xDE, 0xAD, 0xBE, 0xEF})
		bad := append([]int(nil), fb...)
		bad[len(bad)-1] ^= 0x5A // corrupt the CRC-24 trailer only
		bits := orFlexBits(sr, br, [][]int{bad}, 8, 14)

		// sigrok flags it and echoes the corrupted transmitted value; the header
		// CRC stays OK.
		fVals, fOKs := orFlexAnnCRCs(t, run(t, bits, "frame-crc"))
		if len(fOKs) != 1 || fOKs[0] {
			t.Fatalf("sigrok frame CRC verdict: %v, want one bad", fOKs)
		}
		if want := orFlexCRC24(fb[:len(fb)-3]) ^ 0x5A; fVals[0] != want {
			t.Fatalf("transmitted frame CRC: sigrok 0x%X, want 0x%X", fVals[0], want)
		}
		_, hOKs := orFlexAnnCRCs(t, run(t, bits, "header-crc"))
		if len(hOKs) != 1 || !hOKs[0] {
			t.Fatalf("sigrok header CRC verdict: %v, want one OK", hOKs)
		}

		// Both sides flag it. The repo's frame CRC-24 check (flexFrameCRC24)
		// exists BECAUSE this oracle test exposed its absence: originally only
		// the header CRC-11 was verified and this corruption decoded as a clean
		// frame while sigrok flagged it. The lone frame is corrupt, so the
		// decode must not report OK, and the bytes stay available (flagged) for
		// display, corrupted trailer included.
		r := decode(t, bits)
		if r.OK {
			t.Fatal("repo decode reported OK for a frame with a corrupted frame CRC")
		}
		if n := countSpans(r, "frame-error"); n != 1 {
			t.Fatalf("repo frame-error spans = %d, want exactly 1", n)
		}
		eqBytes(t, "bytes incl corrupted trailer", r.Bytes, bad)
	})

	t.Run("two-frames-back-to-back", func(t *testing.T) {
		p1 := []int{0x12, 0x34}             // LEN=1
		p2 := []int{0xFF, 0x00, 0xAA, 0x55} // LEN=2
		f1 := orFlexFrame(55, 3, p1)
		f2 := orFlexFrame(1023, 62, p2)
		// 14 idle bits between the frames: the minimum that lets sigrok finish
		// frame 1's DTS probe + channel-idle delimiter before frame 2's TSS.
		bits := orFlexBits(sr, br, [][]int{f1, f2}, 8, 14)

		r := decode(t, bits)
		if !r.OK {
			t.Fatalf("repo decode failed: %s", r.Error)
		}
		eqBytes(t, "both frames' bytes", r.Bytes, append(append([]int{}, f1...), f2...))

		// Per-frame header fields, in order, on both sides.
		ids, lens, cycs := orFlexNotes(t, r, "addr")
		sIDs := orFlexAnnInts(t, run(t, bits, "id"))
		sLens := orFlexAnnInts(t, run(t, bits, "length"))
		sCycs := orFlexAnnInts(t, run(t, bits, "cycle"))
		eqBytes(t, "frame ids", ids, sIDs)
		eqBytes(t, "payload lengths", lens, sLens)
		eqBytes(t, "cycle counts", cycs, sCycs)
		eqBytes(t, "frame ids (truth)", sIDs, []int{55, 1023})
		eqBytes(t, "payload lengths (truth)", sLens, []int{1, 2})
		eqBytes(t, "cycle counts (truth)", sCycs, []int{3, 62})

		// Both frames' CRCs valid on sigrok; no repo error spans; the payload
		// byte stream (sigrok's data-byte concat vs the repo's payload slices)
		// matches across the frame boundary.
		_, hOKs := orFlexAnnCRCs(t, run(t, bits, "header-crc"))
		_, fOKs := orFlexAnnCRCs(t, run(t, bits, "frame-crc"))
		if len(hOKs) != 2 || !hOKs[0] || !hOKs[1] || len(fOKs) != 2 || !fOKs[0] || !fOKs[1] {
			t.Fatalf("sigrok CRC verdicts: header %v frame %v, want two OK each", hOKs, fOKs)
		}
		if n := countSpans(r, "frame-error"); n != 0 {
			t.Fatalf("repo flagged %d CRC errors on clean frames", n)
		}
		repoPayload := append(append([]int{}, r.Bytes[5:5+len(p1)]...), r.Bytes[len(f1)+5:len(f1)+5+len(p2)]...)
		eqBytes(t, "payloads across frames", repoPayload, orFlexAnnHex(t, run(t, bits, "data-byte")))

		// Both sides segment the record into exactly two frames at the same spots.
		eqAligned(t, "TSS per frame", r, "start", run(t, bits, "tss"), spb)
	})

	// Every other vector transmits sync=0/startup=0. The two indicator bits are
	// bits 0..1 of the 20 CRC-11-protected header bits (and inside the frame
	// CRC-24), so setting them exercises a genuinely different CRC input on
	// both sides — a decoder that ignored the flags when checking the header
	// CRC would flag these frames as corrupt. Frame 1 is SYNC-only, frame 2
	// SYNC+STARTUP (the spec requires sync=1 whenever startup=1).
	t.Run("sync-startup-flags", func(t *testing.T) {
		p1 := []int{0xDE, 0xAD}             // LEN=1
		p2 := []int{0x12, 0x34, 0x56, 0x78} // LEN=2
		f1 := orFlexFrameFlags(1, 0, 291, 17, p1)
		f2 := orFlexFrameFlags(1, 1, 800, 5, p2)
		bits := orFlexBits(sr, br, [][]int{f1, f2}, 8, 14)

		// Generator sanity: the flags must actually shift the CRC-11, otherwise
		// this subtest would silently degenerate into the flags-clear case.
		if brFlexHeaderCRC11(1, 0, 291, 1) == brFlexHeaderCRC11(0, 0, 291, 1) ||
			brFlexHeaderCRC11(1, 1, 800, 2) == brFlexHeaderCRC11(1, 0, 800, 2) {
			t.Fatal("header CRC-11 does not depend on the sync/startup bits")
		}

		r := decode(t, bits)
		if !r.OK {
			t.Fatalf("repo decode failed: %s", r.Error)
		}
		eqBytes(t, "on-wire bytes", r.Bytes, append(append([]int{}, f1...), f2...))

		// repo: the header notes carry the " SYNC"/" STARTUP" suffixes — pinned
		// as exact strings so suffix order and spacing can never drift.
		var notes []string
		for _, s := range r.Spans {
			if s.Kind == "addr" {
				notes = append(notes, s.Text)
			}
		}
		wantNotes := []string{"ID=291 LEN=1 CYC=17 SYNC", "ID=800 LEN=2 CYC=5 SYNC STARTUP"}
		if len(notes) != len(wantNotes) {
			t.Fatalf("repo header notes: got %d (%q), want %d", len(notes), notes, len(wantNotes))
		}
		for i := range notes {
			if notes[i] != wantNotes[i] {
				t.Fatalf("repo header note %d: got %q, want %q", i, notes[i], wantNotes[i])
			}
		}

		// sigrok: the typed indicator annotations, one per frame, in order.
		eqBytes(t, "sync flags (sigrok)", orFlexAnnInts(t, run(t, bits, "sync-frame")), []int{1, 1})
		eqBytes(t, "startup flags (sigrok)", orFlexAnnInts(t, run(t, bits, "startup-frame")), []int{0, 1})
		eqBytes(t, "frame ids (sigrok)", orFlexAnnInts(t, run(t, bits, "id")), []int{291, 800})

		// Header CRC: OK on sigrok AND the transmitted value equals the repo's
		// own CRC-11 over (sync,startup,id,len) — both sides fold the flag bits
		// into the CRC identically; the repo flags nothing.
		hVals, hOKs := orFlexAnnCRCs(t, run(t, bits, "header-crc"))
		if len(hOKs) != 2 || !hOKs[0] || !hOKs[1] {
			t.Fatalf("sigrok header CRC verdicts: %v, want two OK", hOKs)
		}
		if hVals[0] != brFlexHeaderCRC11(1, 0, 291, 1) || hVals[1] != brFlexHeaderCRC11(1, 1, 800, 2) {
			t.Fatalf("header CRC values: sigrok 0x%X/0x%X, repo CRC-11 0x%X/0x%X",
				hVals[0], hVals[1], brFlexHeaderCRC11(1, 0, 291, 1), brFlexHeaderCRC11(1, 1, 800, 2))
		}
		if n := countSpans(r, "frame-error"); n != 0 {
			t.Fatalf("repo flagged %d CRC errors on clean flagged frames", n)
		}

		// Frame CRC-24 (also sealed over the flag bits) verifies on sigrok, and
		// the payloads agree with the generated truth across both frames.
		_, fOKs := orFlexAnnCRCs(t, run(t, bits, "frame-crc"))
		if len(fOKs) != 2 || !fOKs[0] || !fOKs[1] {
			t.Fatalf("sigrok frame CRC verdicts: %v, want two OK", fOKs)
		}
		eqBytes(t, "payloads (sigrok)", orFlexAnnHex(t, run(t, bits, "data-byte")),
			append(append([]int{}, p1...), p2...))
	})

	// Dynamic-segment frame (ID 1500 > cStaticSlotIDMax=1023) with a DTS tail:
	// after the FES the node holds the bus LOW for 9 bit-times (padding its
	// minislot to the next action point) and releases with one HIGH bit. The
	// reviewer's concern: that LOW run is over twice the repo's 4-bit TSS
	// minimum, so a scanner that does not recognize the tail could hallucinate
	// a phantom second frame from it. sigrok names the run explicitly (its
	// "dts" annotation class); the repo must decode exactly one frame.
	t.Run("dynamic-frame-dts-tail", func(t *testing.T) {
		payload := []int{0xCA, 0xFE, 0x01, 0x02}
		fd := orFlexFrame(1500, 33, payload)
		const dtsLow = 9
		bits := orFlexBitsDTS(sr, br, fd, 8, dtsLow, 12)

		// sigrok decodes the frame's fields normally despite the DTS tail...
		eqBytes(t, "frame id (sigrok)", orFlexAnnInts(t, run(t, bits, "id")), []int{1500})
		eqBytes(t, "cycle (sigrok)", orFlexAnnInts(t, run(t, bits, "cycle")), []int{33})
		eqBytes(t, "payload (sigrok)", orFlexAnnHex(t, run(t, bits, "data-byte")), payload)
		_, hOKs := orFlexAnnCRCs(t, run(t, bits, "header-crc"))
		_, fOKs := orFlexAnnCRCs(t, run(t, bits, "frame-crc"))
		if len(hOKs) != 1 || !hOKs[0] || len(fOKs) != 1 || !fOKs[0] {
			t.Fatalf("sigrok CRC verdicts: header %v frame %v, want one OK each", hOKs, fOKs)
		}
		// ...and emits exactly ONE dts annotation whose extent matches the
		// generated truth: it starts at the FES->DTS falling edge and ends one
		// bit past the rising edge (the PD's putg pads each sample point by
		// half a bit, so the closing HIGH bit is included).
		dts := run(t, bits, "dts")
		if len(dts) != 1 {
			t.Fatalf("sigrok dts annotations: got %d, want 1", len(dts))
		}
		bitsBefore := 8 + 8 + 1 + 10*len(fd) + 2 // lead idle + TSS + FSS + 10 bits/byte + FES
		fall, rise := bitsBefore*spb, (bitsBefore+dtsLow)*spb
		if d := dts[0].I0 - fall; d > spb || d < -spb {
			t.Fatalf("sigrok DTS starts at %d, generated fall edge at %d", dts[0].I0, fall)
		}
		if d := dts[0].I1 - (rise + spb); d > spb || d < -spb {
			t.Fatalf("sigrok DTS ends at %d, generated rise edge + 1 bit at %d", dts[0].I1, rise+spb)
		}

		// repo (anchored against the generated truth): exactly one frame — one
		// TSS span, one header note, zero error spans, and the byte stream is
		// exactly the single frame's bytes. The DTS low run IS offered to the
		// TSS scanner (it exceeds the minimum), but with no BSS behind it no
		// phantom bytes or spans may appear.
		r := decode(t, bits)
		if !r.OK {
			t.Fatalf("repo decode failed: %s", r.Error)
		}
		eqBytes(t, "on-wire bytes", r.Bytes, fd)
		if countSpans(r, "start") != 1 || countSpans(r, "addr") != 1 || countSpans(r, "frame-error") != 0 {
			t.Fatalf("repo spans: %d start, %d addr, %d frame-error; want 1/1/0 (phantom frame from DTS?)",
				countSpans(r, "start"), countSpans(r, "addr"), countSpans(r, "frame-error"))
		}
		ids, lens, cycs := orFlexNotes(t, r, "addr")
		if ids[0] != 1500 || lens[0] != 2 || cycs[0] != 33 {
			t.Fatalf("repo header note: ID=%d LEN=%d CYC=%d, want 1500/2/33", ids[0], lens[0], cycs[0])
		}
		// Both sides put the one real TSS in the same place — a phantom TSS
		// span at the DTS would also break the count inside eqAligned.
		eqAligned(t, "TSS", r, "start", run(t, bits, "tss"), spb)
	})

	// Fractional samples-per-bit: 183 MSa/s at 10 Mbit/s = 18.3 samples/bit —
	// the only remaining protocol oracled purely at integer spb. Bit
	// boundaries now drift up to half a sample against the grid every bit, so
	// both decoders' resync paths do real work (repo: per-byte BSS snap;
	// sigrok: dom_edge resync on falling edges). Bitrate pinned on both sides.
	t.Run("fractional-samples-per-bit", func(t *testing.T) {
		const srF = 183_000_000
		const spbF = float64(srF) / br // 18.3
		frameID, cycle := 291, 17
		payload := []int{0xDE, 0xAD, 0xBE, 0xEF} // LEN=2
		fb := orFlexFrame(frameID, cycle, payload)
		bits := orFlexBits(srF, br, [][]int{fb}, 8, 14)

		runF := func(t *testing.T, class string) []ann {
			return sigrokDecode(t, srF, []string{"CH"}, [][]byte{bits}, pdSpec, "flexray="+class)
		}
		r := DecodeFlexRay(bitsToCodes(bits), 1.0/float64(srF), FlexRayCfg{Bitrate: br})
		if !r.OK {
			t.Fatalf("repo decode failed: %s", r.Error)
		}
		// The repo must carry the fractional bit period, not a rounded one:
		// T=18.3 exactly (pinned bitrate), and Baud rounds back to 10 Mbit/s.
		if d := r.SPB - spbF; d > 1e-9 || d < -1e-9 {
			t.Fatalf("repo SPB = %v, want %v", r.SPB, spbF)
		}
		if r.Baud != br {
			t.Fatalf("repo Baud = %d, want %d", r.Baud, br)
		}
		eqBytes(t, "on-wire bytes", r.Bytes, fb)

		// Field-level agreement, exactly like the integer-spb static frame.
		ids, lens, cycs := orFlexNotes(t, r, "addr")
		sIDs := orFlexAnnInts(t, runF(t, "id"))
		sLens := orFlexAnnInts(t, runF(t, "length"))
		sCycs := orFlexAnnInts(t, runF(t, "cycle"))
		if len(ids) != 1 || len(sIDs) != 1 {
			t.Fatalf("frame counts differ: repo %d notes, sigrok %d ids", len(ids), len(sIDs))
		}
		if ids[0] != frameID || sIDs[0] != frameID {
			t.Fatalf("frame id: repo %d, sigrok %d, want %d", ids[0], sIDs[0], frameID)
		}
		if wantLen := len(payload) / 2; lens[0] != wantLen || sLens[0] != wantLen {
			t.Fatalf("payload length: repo %d, sigrok %d, want %d", lens[0], sLens[0], wantLen)
		}
		if cycs[0] != cycle || sCycs[0] != cycle {
			t.Fatalf("cycle: repo %d, sigrok %d, want %d", cycs[0], sCycs[0], cycle)
		}
		hVals, hOKs := orFlexAnnCRCs(t, runF(t, "header-crc"))
		if len(hOKs) != 1 || !hOKs[0] {
			t.Fatalf("sigrok header CRC verdict: %v, want one OK", hOKs)
		}
		if want := brFlexHeaderCRC11(0, 0, frameID, len(payload)/2); hVals[0] != want {
			t.Fatalf("header CRC value: sigrok 0x%X, repo CRC-11 0x%X", hVals[0], want)
		}
		fVals, fOKs := orFlexAnnCRCs(t, runF(t, "frame-crc"))
		if len(fOKs) != 1 || !fOKs[0] {
			t.Fatalf("sigrok frame CRC verdict: %v, want one OK", fOKs)
		}
		if want := orFlexCRC24(fb[:len(fb)-3]); fVals[0] != want {
			t.Fatalf("frame CRC value: sigrok 0x%X, generator 0x%X", fVals[0], want)
		}
		if n := countSpans(r, "frame-error"); n != 0 {
			t.Fatalf("repo flagged %d CRC errors on a clean frame", n)
		}
		dataAnns := runF(t, "data-byte")
		eqBytes(t, "payload (sigrok)", orFlexAnnHex(t, dataAnns), payload)
		eqBytes(t, "payload (repo)", r.Bytes[5:5+len(payload)], payload)

		// Placement, with a ceil(18.3)=19-sample tolerance: TSS spans coincide;
		// the repo's per-byte spans lead sigrok's data-byte annotations by the
		// fixed 2-bit BSS offset (compared in float so the .3/bit drift does
		// not round away).
		tolF := 19
		eqAligned(t, "TSS", r, "start", runF(t, "tss"), tolF)
		pSpans := orFlexDataSpans(r)[5 : 5+len(payload)]
		for i := range pSpans {
			if d := float64(pSpans[i].I0) + 2*spbF - float64(dataAnns[i].I0); d > spbF || d < -spbF {
				t.Fatalf("payload byte %d misaligned: repo BSS at %d, sigrok data at %d", i, pSpans[i].I0, dataAnns[i].I0)
			}
		}

		// AUTO inference must survive the fractional grid — this is what
		// makes the bit-period refine loop and the per-byte BSS resync
		// load-bearing: the review proved by mutation that with either
		// disabled the whole suite still passed when only integer-spb
		// vectors existed (resync corrections were all zero-magnitude, and a
		// collapsed 18.0 estimate still decoded short frames). The inferred
		// period must be the true 18.3, not a rounded 18.0 (~10.28 Mbit).
		ra := DecodeFlexRay(bitsToCodes(bits), 1.0/float64(srF), FlexRayCfg{})
		if !ra.OK {
			t.Fatalf("repo AUTO decode failed: %s", ra.Error)
		}
		if d := ra.SPB - spbF; d > 0.15 || d < -0.15 {
			t.Fatalf("auto-inferred SPB %.3f, want %.1f (refine-loss regression?)", ra.SPB, spbF)
		}
		eqBytes(t, "fractional auto bytes", ra.Bytes, fb)
	})
}
