package decode

// USB low-speed vs the sigrok usb_signalling + usb_packet decoder stack.
//
// Channel mapping: sigrok wants the real differential pair (dp, dm) with the
// LOW-speed polarity J=(dp 0, dm 1), K=(dp 1, dm 0), SE0=(0,0). The repo
// decoder consumes ONE single-ended line — the one that idles HIGH and shows
// the EOP as ~2 low bit-times, so that EOP+idle bound each packet (see
// decode_usbls.go). For low speed that line is D- (for full speed it would be
// D+; the parameter is named dp for the FS case, mirroring decode.js). So the
// oracle and the repo decoder here consume the SAME capture: sigrok gets both
// lines, the repo gets the D- column.
//
// Granularity: the repo decoder emits the PID name plus ALL post-PID bytes as
// raw "data" spans — it does not split token fields nor verify CRCs. sigrok
// decodes addr/endp/framenum and checks CRC5/CRC16 itself. The intersection is
// still exact: token addr/endp/crc5 are reconstructed bit-for-bit from the
// repo's two raw token bytes, and CRC verdicts are recomputed in-test from the
// repo's raw payload+CRC bytes (using CRC routines ported from sigrok's own
// usb_packet pd.py) — so a corrupted CRC16 must be flagged by BOTH sides.

import (
	"strconv"
	"strings"
	"testing"
)

const usblsBitrate = 1_500_000 // USB low-speed is fixed at 1.5 Mbit/s

// usblsLSB expands v into n bits, LSB first — USB transmits every multi-bit
// field least-significant bit first.
func usblsLSB(v, n int) []int {
	out := make([]int, n)
	for i := 0; i < n; i++ {
		out[i] = (v >> i) & 1
	}
	return out
}

// usblsRev reverses the low `count` bits of num — ported from usb_packet
// pd.py's reverse_number, which maps the CRC shift register onto the LSB-first
// wire order.
func usblsRev(num, count int) int {
	out := 0
	for i := 0; i < count; i++ {
		if num>>i&1 == 1 {
			out |= 1 << (count - 1 - i)
		}
	}
	return out
}

// usblsCRC5 / usblsCRC16 are 1:1 ports of sigrok usb_packet's calc_crc5 /
// calc_crc16: bit-serial over the transmission-order bits, complemented and
// bit-reversed so the result compares directly against the LSB-first-decoded
// CRC field. (usblsCRC5(addr=0x15, ep=0xE bits) == 0x17, the USB spec example.)
func usblsCRC5(bits []int) int {
	crc := 0x1F
	for _, b := range bits {
		crc <<= 1
		if b != crc>>5 {
			crc ^= 0x25
		}
		crc &= 0x1F
	}
	return usblsRev(crc^0x1F, 5)
}

func usblsCRC16(bits []int) int {
	crc := 0xFFFF
	for _, b := range bits {
		crc <<= 1
		if b != crc>>16 {
			crc ^= 0x18005
		}
		crc &= 0xFFFF
	}
	return usblsRev(crc^0xFFFF, 16)
}

// usblsByteBits expands bytes to the LSB-first wire bit stream (the domain of
// usblsCRC16).
func usblsByteBits(data []int) []int {
	var bits []int
	for _, d := range data {
		bits = append(bits, usblsLSB(d, 8)...)
	}
	return bits
}

// usblsTokenBits builds the 16 post-PID bits of an OUT/IN/SETUP token:
// ADDR(7) + ENDP(4) + CRC5(5), each LSB first.
func usblsTokenBits(addr, ep int) []int {
	bits := append(usblsLSB(addr, 7), usblsLSB(ep, 4)...)
	return append(bits, usblsLSB(usblsCRC5(bits), 5)...)
}

// usblsDataBits builds the post-PID bits of a DATA packet: payload bytes +
// CRC16, LSB first. crcXor corrupts the TRANSMITTED CRC without touching the
// payload, so both decoders must still deliver the bytes and flag the check.
func usblsDataBits(data []int, crcXor int) []int {
	bits := usblsByteBits(data)
	return append(bits, usblsLSB(usblsCRC16(bits)^crcXor, 16)...)
}

// usblsPacketBits assembles one packet's logical bit stream — SYNC (00000001)
// + PID byte (4-bit PID + its complement) + payload bits — then bit-stuffs it.
func usblsPacketBits(pid int, payload []int) []int {
	return usblsPacketBitsRawPID((pid&0xF)|((^pid&0xF)<<4), payload)
}

// usblsPacketBitsRawPID is usblsPacketBits with the FULL 8-bit PID byte given
// verbatim — so a corrupted complement nibble can be put on the wire. The
// stream is bit-stuffed: a 0 is inserted after six consecutive 1s, with the
// ones count carried across field boundaries from the SYNC onward (its
// trailing 1 counts), exactly as sigrok's usb_signalling expects.
func usblsPacketBitsRawPID(pidByte int, payload []int) []int {
	bits := []int{0, 0, 0, 0, 0, 0, 0, 1} // SYNC
	bits = append(bits, usblsLSB(pidByte&0xFF, 8)...)
	bits = append(bits, payload...)
	var out []int
	ones := 0
	for _, b := range bits {
		out = append(out, b)
		if b == 1 {
			if ones++; ones == 6 {
				out = append(out, 0)
				ones = 0
			}
		} else {
			ones = 0
		}
	}
	return out
}

// oracleUSBLSWave renders packets as the low-speed differential pair. NRZI
// from idle J (a 0 bit toggles J<->K, a 1 holds), EOP = 2 bit-times SE0 + 1
// bit-time J, then gapBits of idle J between packets. Two lockstep timelines
// keep dp/dm sample-aligned even at fractional samples-per-bit.
func oracleUSBLSWave(sr float64, packets [][]int, gapBits float64) (dp, dm []byte) {
	bt := 1.0 / usblsBitrate
	wp, wm := newTimeline(sr), newTimeline(sr)
	sym := func(p, m byte, cells float64) {
		wp.add(p, cells*bt)
		wm.add(m, cells*bt)
	}
	stJ, stK := 1, 0
	emit := func(st int, cells float64) {
		if st == stJ {
			sym(0, 1, cells) // J: dp low, dm high (LOW-speed polarity)
		} else {
			sym(1, 0, cells) // K: dp high, dm low
		}
	}
	emit(stJ, 20) // lead idle
	for _, bits := range packets {
		st := stJ
		for _, b := range bits {
			if b == 0 {
				st = stJ + stK - st
			}
			emit(st, 1)
		}
		sym(0, 0, 2)         // EOP: SE0
		emit(stJ, 1+gapBits) // EOP's closing J, then inter-packet idle
	}
	emit(stJ, 20) // trail idle (repo needs >=2 bit-times after the last edge)
	return wp.bits, wm.bits
}

// oracleUSBLSWaveKA prepends a keep-alive train to the packet wave: nKA bare
// EOPs (2 bit-times SE0 + 1 J) spaced kaGapBits of idle J apart — an idle
// low-speed bus, where the hub emits a keep-alive every millisecond. Real
// spacing is ~1500 bit-times; the test uses a smaller one to keep the vector
// small — the estimator failure mode this guards (gap-COUNT flooding of a
// percentile) is spacing-independent.
func oracleUSBLSWaveKA(sr float64, nKA int, kaGapBits float64, packets [][]int, gapBits float64) (dp, dm []byte) {
	bt := 1.0 / usblsBitrate
	wp, wm := newTimeline(sr), newTimeline(sr)
	sym := func(p, m byte, cells float64) {
		wp.add(p, cells*bt)
		wm.add(m, cells*bt)
	}
	stJ, stK := 1, 0
	emit := func(st int, cells float64) {
		if st == stJ {
			sym(0, 1, cells) // J: dp low, dm high (LOW-speed polarity)
		} else {
			sym(1, 0, cells) // K
		}
	}
	emit(stJ, 20) // lead idle
	for k := 0; k < nKA; k++ {
		sym(0, 0, 2)           // keep-alive: bare EOP (2 bit-times SE0)
		emit(stJ, 1+kaGapBits) // closing J + inter-KA idle
	}
	for _, bits := range packets {
		st := stJ
		for _, b := range bits {
			if b == 0 {
				st = stJ + stK - st
			}
			emit(st, 1)
		}
		sym(0, 0, 2)         // EOP: SE0
		emit(stJ, 1+gapBits) // EOP's closing J, then inter-packet idle
	}
	emit(stJ, 20) // trail idle
	return wp.bits, wm.bits
}

// usblsSigrok runs the stacked usb_signalling->usb_packet decode for one
// usb_packet annotation class.
func usblsSigrok(t *testing.T, sr int, dp, dm []byte, class string) []ann {
	t.Helper()
	return sigrokDecode(t, sr, []string{"DP", "DM"}, [][]byte{dp, dm},
		"usb_signalling:dp=DP:dm=DM:signalling=low-speed,usb_packet:signalling=low-speed",
		"usb_packet="+class)
}

// usblsSignalling fetches one usb_signalling (not usb_packet) annotation class.
func usblsSignalling(t *testing.T, sr int, dp, dm []byte, class string) []ann {
	t.Helper()
	return sigrokDecode(t, sr, []string{"DP", "DM"}, [][]byte{dp, dm},
		"usb_signalling:dp=DP:dm=DM:signalling=low-speed",
		"usb_signalling="+class)
}

// usblsAnnNum strips a fixed prefix (and any 0x) from each annotation text and
// parses the remainder in the given base — "Address: 21" / "CRC16: 0xA917".
func usblsAnnNum(t *testing.T, anns []ann, prefix string, base int) []int {
	t.Helper()
	out := make([]int, 0, len(anns))
	for _, a := range anns {
		s, ok := strings.CutPrefix(a.Text, prefix)
		if !ok {
			t.Fatalf("annotation %q lacks prefix %q", a.Text, prefix)
		}
		s = strings.TrimPrefix(s, "0x")
		v, err := strconv.ParseInt(strings.TrimSpace(s), base, 32)
		if err != nil {
			t.Fatalf("annotation %q: %v", a.Text, err)
		}
		out = append(out, int(v))
	}
	return out
}

// usblsPIDs strips "PID: " from sigrok's pid annotations.
func usblsPIDs(t *testing.T, anns []ann) []string {
	t.Helper()
	out := make([]string, 0, len(anns))
	for _, a := range anns {
		s, ok := strings.CutPrefix(a.Text, "PID: ")
		if !ok {
			t.Fatalf("annotation %q lacks PID prefix", a.Text)
		}
		out = append(out, s)
	}
	return out
}

// usblsRepoPkt is one packet reassembled from the repo decoder's span stream:
// the PID name plus every post-PID byte (token fields / payload / CRC raw).
type usblsRepoPkt struct {
	name  string
	bytes []int
	i0s   []int // start sample of each byte's span, for alignment checks
}

func usblsRepoPackets(r Result) []usblsRepoPkt {
	var out []usblsRepoPkt
	for _, s := range r.Spans {
		switch s.Kind {
		case "start": // SYNC opens a packet
			out = append(out, usblsRepoPkt{})
		case "addr", "frame-error": // PID (frame-error = failed PID check)
			if len(out) > 0 {
				out[len(out)-1].name = strings.TrimPrefix(s.Text, "!")
			}
		case "data":
			if len(out) > 0 {
				p := &out[len(out)-1]
				p.bytes = append(p.bytes, s.Val)
				p.i0s = append(p.i0s, s.I0)
			}
		}
	}
	return out
}

// usblsTokenFields undoes the repo's LSB-first byte packing of a token's 16
// post-PID bits: bits 0..6 ADDR, 7..10 ENDP, 11..15 CRC5.
func usblsTokenFields(t *testing.T, p usblsRepoPkt) (addr, ep, crc5 int) {
	t.Helper()
	if len(p.bytes) != 2 {
		t.Fatalf("token %s: repo decoded %d bytes, want 2 (ADDR+ENDP+CRC5)", p.name, len(p.bytes))
	}
	b0, b1 := p.bytes[0], p.bytes[1]
	return b0 & 0x7F, (b0 >> 7) | ((b1 & 0x7) << 1), (b1 >> 3) & 0x1F
}

// usblsRepoCRC16 splits a repo DATA packet into payload + transmitted CRC16
// (little-endian trailing bytes) and reports whether the CRC checks out — the
// repo decoder exposes raw bytes, not a verdict, so the verdict is recomputed
// here from repo output alone and compared against sigrok's crc16-ok/err.
func usblsRepoCRC16(t *testing.T, p usblsRepoPkt) (payload []int, crc int, ok bool) {
	t.Helper()
	if len(p.bytes) < 2 {
		t.Fatalf("data packet %s: repo decoded %d bytes, want >=2 (CRC16)", p.name, len(p.bytes))
	}
	n := len(p.bytes)
	payload = p.bytes[:n-2]
	crc = p.bytes[n-2] | p.bytes[n-1]<<8
	return payload, crc, crc == usblsCRC16(usblsByteBits(payload))
}

func TestOracleUSBLS(t *testing.T) {
	needSigrok(t)
	const sr = 24_000_000 // 16 samples/bit at 1.5 Mbit/s
	const spb = sr / usblsBitrate
	cfg := USBLSCfg{Bitrate: usblsBitrate}

	decodeRepo := func(t *testing.T, dm []byte, c USBLSCfg) Result {
		t.Helper()
		r := DecodeUSBLS(bitsToCodes(dm), 1.0/float64(sr), c)
		if !r.OK {
			t.Fatalf("repo decode failed: %s", r.Error)
		}
		return r
	}

	t.Run("setup-data0-ack", func(t *testing.T) {
		// The canonical control-transfer shape: token + data + handshake.
		payload := []int{0x48, 0x69, 0x21, 0x00, 0x80, 0x7E}
		pkts := [][]int{
			usblsPacketBits(0xD, usblsTokenBits(21, 10)), // SETUP, spec's CRC5 example pair
			usblsPacketBits(0x3, usblsDataBits(payload, 0)),
			usblsPacketBits(0x2, nil), // ACK: SYNC+PID+EOP only
		}
		dp, dm := oracleUSBLSWave(sr, pkts, 40)
		r := decodeRepo(t, dm, cfg)
		rp := usblsRepoPackets(r)

		// PID sequence — repo span text vs sigrok pid annotations, same names.
		oraclePIDs := usblsPIDs(t, usblsSigrok(t, sr, dp, dm, "pid"))
		if len(rp) != 3 || len(oraclePIDs) != 3 {
			t.Fatalf("packet counts: repo %d, sigrok %d, want 3", len(rp), len(oraclePIDs))
		}
		for i, want := range []string{"SETUP", "DATA0", "ACK"} {
			if rp[i].name != want || oraclePIDs[i] != want {
				t.Fatalf("PID %d: repo %q, sigrok %q, want %q", i, rp[i].name, oraclePIDs[i], want)
			}
		}
		// One SYNC per packet on both sides.
		if n := len(usblsSigrok(t, sr, dp, dm, "sync-ok")); n != 3 || countSpans(r, "start") != 3 {
			t.Fatalf("SYNC counts: repo %d, sigrok %d, want 3", countSpans(r, "start"), n)
		}

		// Token fields: sigrok decodes ADDR/ENDP/CRC5; the repo's two raw token
		// bytes must reconstruct to the identical values.
		addr, ep, crc5 := usblsTokenFields(t, rp[0])
		eqBytes(t, "SETUP addr", []int{addr}, usblsAnnNum(t, usblsSigrok(t, sr, dp, dm, "addr"), "Address: ", 10))
		eqBytes(t, "SETUP endp", []int{ep}, usblsAnnNum(t, usblsSigrok(t, sr, dp, dm, "ep"), "Endpoint: ", 10))
		eqBytes(t, "SETUP crc5", []int{crc5}, usblsAnnNum(t, usblsSigrok(t, sr, dp, dm, "crc5-ok"), "CRC5: ", 16))

		// DATA0 payload: exact bytes, and each byte span aligned with sigrok's
		// Databyte annotation (repo also carries the 2 CRC bytes — the compare is
		// over the payload intersection, the CRC value is checked separately).
		dataAnns := usblsSigrok(t, sr, dp, dm, "data")
		repoPayload, repoCRC, crcOK := usblsRepoCRC16(t, rp[1])
		eqBytes(t, "DATA0 payload", repoPayload, usblsAnnNum(t, dataAnns, "Databyte: ", 16))
		for i := range repoPayload {
			if d := rp[1].i0s[i] - dataAnns[i].I0; d > spb || d < -spb {
				t.Fatalf("payload byte %d misaligned: repo starts at %d, sigrok at %d", i, rp[1].i0s[i], dataAnns[i].I0)
			}
		}
		// CRC16 verdict + value: clean on both sides, same transmitted value.
		if !crcOK {
			t.Fatalf("repo CRC16 check failed on clean packet (crc %04X over %02X)", repoCRC, repoPayload)
		}
		if n := len(usblsSigrok(t, sr, dp, dm, "crc16-err")); n != 0 {
			t.Fatalf("sigrok flagged %d CRC16 errors on clean traffic", n)
		}
		eqBytes(t, "CRC16 value", []int{repoCRC}, usblsAnnNum(t, usblsSigrok(t, sr, dp, dm, "crc16-ok"), "CRC16: ", 16))

		// Handshake carries nothing after the PID.
		if len(rp[2].bytes) != 0 {
			t.Fatalf("ACK: repo decoded %d payload bytes, want 0", len(rp[2].bytes))
		}

		// Auto bitrate (infer T from edge statistics) must reproduce the decode.
		ra := decodeRepo(t, dm, USBLSCfg{})
		eqBytes(t, "auto-bitrate byte stream", ra.Bytes, r.Bytes)
	})

	t.Run("bit-stuffing-payloads", func(t *testing.T) {
		// Payloads dense in six-ones runs, crossing byte boundaries, so both
		// sides must insert/drop stuff bits at the same positions or the byte
		// streams shear apart.
		p0 := []int{0xFF, 0xFF, 0xFF, 0x7F, 0xFE, 0x3F, 0xFC, 0xFF}
		p1 := []int{0x7E, 0xBF, 0xDF, 0xEF, 0xF7, 0xFB, 0xFD, 0xFE}
		pkts := [][]int{
			usblsPacketBits(0x3, usblsDataBits(p0, 0)), // DATA0
			usblsPacketBits(0xB, usblsDataBits(p1, 0)), // DATA1
		}
		dp, dm := oracleUSBLSWave(sr, pkts, 40)
		r := decodeRepo(t, dm, cfg)
		rp := usblsRepoPackets(r)
		oraclePIDs := usblsPIDs(t, usblsSigrok(t, sr, dp, dm, "pid"))
		if len(rp) != 2 || len(oraclePIDs) != 2 || rp[0].name != "DATA0" || rp[1].name != "DATA1" ||
			oraclePIDs[0] != "DATA0" || oraclePIDs[1] != "DATA1" {
			t.Fatalf("PID sequence: repo %d packets %+v, sigrok %v, want [DATA0 DATA1]", len(rp), rp, oraclePIDs)
		}
		pay0, _, ok0 := usblsRepoCRC16(t, rp[0])
		pay1, _, ok1 := usblsRepoCRC16(t, rp[1])
		oracleBytes := usblsAnnNum(t, usblsSigrok(t, sr, dp, dm, "data"), "Databyte: ", 16)
		eqBytes(t, "stuffed payloads", append(append([]int{}, pay0...), pay1...), oracleBytes)
		// The CRCs themselves force stuff bits too; both sides must still verify.
		if !ok0 || !ok1 {
			t.Fatalf("repo CRC16 verdicts on stuffed packets: %v %v, want both ok", ok0, ok1)
		}
		if n := len(usblsSigrok(t, sr, dp, dm, "crc16-ok")); n != 2 {
			t.Fatalf("sigrok CRC16-ok count %d, want 2", n)
		}
	})

	t.Run("crc16-corrupted-flagged-by-both", func(t *testing.T) {
		// Flip one transmitted CRC bit on the first packet; payload untouched.
		// Both sides must (a) still deliver the payload bytes and (b) flag the
		// packet — sigrok via crc16-err, the repo via its raw trailing CRC bytes
		// failing the in-test recomputation (see usblsRepoCRC16). The clean
		// DATA1 after it proves the failure doesn't poison the next packet.
		bad, good := []int{0xDE, 0xAD, 0xBE, 0xEF}, []int{0x01, 0x02}
		pkts := [][]int{
			usblsPacketBits(0x3, usblsDataBits(bad, 0x0001)),
			usblsPacketBits(0xB, usblsDataBits(good, 0)),
		}
		dp, dm := oracleUSBLSWave(sr, pkts, 40)
		r := decodeRepo(t, dm, cfg)
		rp := usblsRepoPackets(r)
		if len(rp) != 2 {
			t.Fatalf("repo packet count %d, want 2", len(rp))
		}
		pay0, crc0, ok0 := usblsRepoCRC16(t, rp[0])
		pay1, _, ok1 := usblsRepoCRC16(t, rp[1])
		if ok0 {
			t.Fatalf("repo CRC16 recomputation did NOT flag the corrupted packet")
		}
		if !ok1 {
			t.Fatalf("repo CRC16 flagged the clean packet")
		}
		errAnns := usblsSigrok(t, sr, dp, dm, "crc16-err")
		okAnns := usblsSigrok(t, sr, dp, dm, "crc16-ok")
		if len(errAnns) != 1 || len(okAnns) != 1 {
			t.Fatalf("sigrok CRC16 verdicts: %d err, %d ok, want 1+1", len(errAnns), len(okAnns))
		}
		// Same transmitted (wrong) CRC value seen by both.
		eqBytes(t, "corrupted CRC16 value", []int{crc0}, usblsAnnNum(t, errAnns, "CRC16 ERROR: ", 16))
		// Payloads intact on both sides despite the bad check.
		oracleBytes := usblsAnnNum(t, usblsSigrok(t, sr, dp, dm, "data"), "Databyte: ", 16)
		eqBytes(t, "payloads around bad CRC", append(append([]int{}, pay0...), pay1...), oracleBytes)
		eqBytes(t, "bad-packet payload", pay0, bad)
	})

	t.Run("token-addr-endp-extremes", func(t *testing.T) {
		// OUT/IN/SETUP across the ADDR/ENDP corners (0/0, 127/15, mixed) plus an
		// alternating-bit address; every CRC5 must verify on both sides.
		toks := []struct {
			pid, addr, ep int
			name          string
		}{
			{0x1, 0, 0, "OUT"},
			{0x9, 127, 15, "IN"},
			{0x1, 127, 0, "OUT"},
			{0x9, 0, 15, "IN"},
			{0xD, 85, 5, "SETUP"}, // 1010101b address
		}
		var pkts [][]int
		for _, tk := range toks {
			pkts = append(pkts, usblsPacketBits(tk.pid, usblsTokenBits(tk.addr, tk.ep)))
		}
		dp, dm := oracleUSBLSWave(sr, pkts, 40)
		r := decodeRepo(t, dm, cfg)
		rp := usblsRepoPackets(r)
		oraclePIDs := usblsPIDs(t, usblsSigrok(t, sr, dp, dm, "pid"))
		oracleAddr := usblsAnnNum(t, usblsSigrok(t, sr, dp, dm, "addr"), "Address: ", 10)
		oracleEP := usblsAnnNum(t, usblsSigrok(t, sr, dp, dm, "ep"), "Endpoint: ", 10)
		oracleCRC5 := usblsAnnNum(t, usblsSigrok(t, sr, dp, dm, "crc5-ok"), "CRC5: ", 16)
		if len(rp) != len(toks) || len(oraclePIDs) != len(toks) || len(oracleCRC5) != len(toks) {
			t.Fatalf("token counts: repo %d, sigrok pid %d, sigrok crc5-ok %d, want %d",
				len(rp), len(oraclePIDs), len(oracleCRC5), len(toks))
		}
		var repoAddr, repoEP, repoCRC5 []int
		for i, tk := range toks {
			if rp[i].name != tk.name || oraclePIDs[i] != tk.name {
				t.Fatalf("token %d PID: repo %q, sigrok %q, want %q", i, rp[i].name, oraclePIDs[i], tk.name)
			}
			a, e, c := usblsTokenFields(t, rp[i])
			repoAddr, repoEP, repoCRC5 = append(repoAddr, a), append(repoEP, e), append(repoCRC5, c)
		}
		eqBytes(t, "token addresses", repoAddr, oracleAddr)
		eqBytes(t, "token endpoints", repoEP, oracleEP)
		eqBytes(t, "token CRC5s", repoCRC5, oracleCRC5)
	})

	t.Run("eop-separates-packets", func(t *testing.T) {
		// Two data packets separated only by EOP + 12 bit-times of idle — just
		// past the repo's splitK=10 segmentation threshold. Both sides must
		// resolve two distinct packets with intact payloads (the EOP must not
		// leak into the byte stream as trailing garbage).
		p0, p1 := []int{0x11, 0x22, 0x33}, []int{0x44, 0x55}
		pkts := [][]int{
			usblsPacketBits(0x3, usblsDataBits(p0, 0)),
			usblsPacketBits(0x3, usblsDataBits(p1, 0)),
		}
		dp, dm := oracleUSBLSWave(sr, pkts, 12)
		r := decodeRepo(t, dm, cfg)
		rp := usblsRepoPackets(r)
		oraclePIDs := usblsPIDs(t, usblsSigrok(t, sr, dp, dm, "pid"))
		if len(rp) != 2 || len(oraclePIDs) != 2 {
			t.Fatalf("packet counts: repo %d, sigrok %d, want 2", len(rp), len(oraclePIDs))
		}
		pay0, _, ok0 := usblsRepoCRC16(t, rp[0])
		pay1, _, ok1 := usblsRepoCRC16(t, rp[1])
		if !ok0 || !ok1 {
			t.Fatalf("repo CRC16 verdicts %v %v — EOP bled into a packet", ok0, ok1)
		}
		oracleBytes := usblsAnnNum(t, usblsSigrok(t, sr, dp, dm, "data"), "Databyte: ", 16)
		eqBytes(t, "tight-gap payloads", append(append([]int{}, pay0...), pay1...), oracleBytes)
	})

	t.Run("min-gap-repo-merges-packets", func(t *testing.T) {
		// Real LS hosts may leave as little as 2 bit-times between packets.
		// sigrok delimits packets on the EOP itself, so it resolves them at ANY
		// gap; the repo decoder segments on inter-packet idle > splitK (10) bit
		// periods (decode_usbls.go) because on its single line the EOP is not
		// distinguishable from idle. At a 4-bit gap the repo merges the two
		// packets into one. Divergence is documented via Skip below; if the repo
		// ever learns EOP-based splitting this subtest starts asserting equality.
		p0, p1 := []int{0x11, 0x22, 0x33}, []int{0x44, 0x55}
		pkts := [][]int{
			usblsPacketBits(0x3, usblsDataBits(p0, 0)),
			usblsPacketBits(0x3, usblsDataBits(p1, 0)),
		}
		dp, dm := oracleUSBLSWave(sr, pkts, 4)
		oraclePIDs := usblsPIDs(t, usblsSigrok(t, sr, dp, dm, "pid"))
		if len(oraclePIDs) != 2 {
			t.Fatalf("sigrok resolved %d packets at 4-bit gap, want 2", len(oraclePIDs))
		}
		r := DecodeUSBLS(bitsToCodes(dm), 1.0/float64(sr), cfg)
		repoPkts := 0
		if r.OK {
			repoPkts = countSpans(r, "start")
		}
		if repoPkts == 2 {
			// Divergence healed — from here on hold the repo to full equality.
			rp := usblsRepoPackets(r)
			pay0, _, ok0 := usblsRepoCRC16(t, rp[0])
			pay1, _, ok1 := usblsRepoCRC16(t, rp[1])
			if !ok0 || !ok1 {
				t.Fatalf("repo split the packets but CRC16 verdicts are %v %v", ok0, ok1)
			}
			oracleBytes := usblsAnnNum(t, usblsSigrok(t, sr, dp, dm, "data"), "Databyte: ", 16)
			eqBytes(t, "min-gap payloads", append(append([]int{}, pay0...), pay1...), oracleBytes)
			return
		}
		// Pin the documented divergence exactly: the repo must still decode OK
		// and merge into precisely ONE packet — a regression to zero packets,
		// an error, or an oversplit is a real defect, not this divergence.
		if !r.OK || repoPkts != 1 {
			t.Fatalf("repo at 4-bit gap: ok=%v packets=%d, want the documented single merged packet", r.OK, repoPkts)
		}
		t.Skipf("DIVERGENCE: at a 4-bit inter-packet gap sigrok decodes 2 packets, repo %d "+
			"(single-line decoder needs idle > 10 bit-times to split; sigrok splits on EOP)", repoPkts)
	})

	t.Run("in-nak-setup-data0-stall", func(t *testing.T) {
		// Handshake PIDs beyond ACK, in the two shapes they occur on a real
		// bus: a NAKed IN poll (device not ready) and a control transfer the
		// device STALLs after the DATA0 stage. A handshake is SYNC+PID+EOP
		// only, so a decoder that mis-frames it either drops it or invents
		// payload — the PID sequence must match sigrok's exactly and both
		// sides must decode ZERO post-PID bytes for NAK/STALL.
		payload := []int{0x80, 0x06, 0x00, 0x01, 0x00, 0x00, 0x40, 0x00} // GET_DESCRIPTOR(device) setup stage
		want := []string{"IN", "NAK", "SETUP", "DATA0", "STALL"}
		pkts := [][]int{
			usblsPacketBits(0x9, usblsTokenBits(3, 1)),      // IN addr 3 ep 1
			usblsPacketBits(0xA, nil),                       // NAK (PID nibble 0xA)
			usblsPacketBits(0xD, usblsTokenBits(3, 0)),      // SETUP addr 3 ep 0
			usblsPacketBits(0x3, usblsDataBits(payload, 0)), // DATA0
			usblsPacketBits(0xE, nil),                       // STALL (PID nibble 0xE)
		}
		dp, dm := oracleUSBLSWave(sr, pkts, 40)
		r := decodeRepo(t, dm, cfg)
		rp := usblsRepoPackets(r)
		pidAnns := usblsSigrok(t, sr, dp, dm, "pid")
		oraclePIDs := usblsPIDs(t, pidAnns)
		if len(rp) != len(want) || len(oraclePIDs) != len(want) {
			t.Fatalf("packet counts: repo %d, sigrok %d, want %d", len(rp), len(oraclePIDs), len(want))
		}
		for i, w := range want {
			if rp[i].name != w || oraclePIDs[i] != w {
				t.Fatalf("PID %d: repo %q, sigrok %q, want %q", i, rp[i].name, oraclePIDs[i], w)
			}
		}
		// Every repo PID span sits on the same wire bits as sigrok's pid
		// annotation (all five PIDs are valid, so kind "addr" covers all).
		eqAligned(t, "PID spans", r, "addr", pidAnns, spb)
		// Handshakes close as pure SYNC+PID packets on both sides: sigrok's
		// packet-level summary is the bare PID name (any decoded payload would
		// print inside it), and the repo carries zero post-PID bytes.
		for _, hs := range []struct {
			class, name string
			idx         int
		}{{"packet-nak", "NAK", 1}, {"packet-stall", "STALL", 4}} {
			anns := usblsSigrok(t, sr, dp, dm, hs.class)
			if len(anns) != 1 || anns[0].Text != hs.name {
				t.Fatalf("sigrok %s: %+v, want exactly one bare %q summary", hs.class, anns, hs.name)
			}
			if n := len(rp[hs.idx].bytes); n != 0 {
				t.Fatalf("%s: repo decoded %d post-PID bytes, want 0", hs.name, n)
			}
		}
		// The surrounding token/data packets survive the interleaved
		// handshakes intact: fields agree with sigrok AND the generated truth.
		aIN, eIN, _ := usblsTokenFields(t, rp[0])
		aSU, eSU, _ := usblsTokenFields(t, rp[2])
		eqBytes(t, "token addrs", []int{aIN, aSU}, usblsAnnNum(t, usblsSigrok(t, sr, dp, dm, "addr"), "Address: ", 10))
		eqBytes(t, "token endps", []int{eIN, eSU}, usblsAnnNum(t, usblsSigrok(t, sr, dp, dm, "ep"), "Endpoint: ", 10))
		eqBytes(t, "token addrs vs truth", []int{aIN, aSU}, []int{3, 3})
		eqBytes(t, "token endps vs truth", []int{eIN, eSU}, []int{1, 0})
		pay, _, ok := usblsRepoCRC16(t, rp[3])
		if !ok {
			t.Fatalf("repo CRC16 check failed on the DATA0 stage")
		}
		eqBytes(t, "DATA0 payload", pay, usblsAnnNum(t, usblsSigrok(t, sr, dp, dm, "data"), "Databyte: ", 16))
		eqBytes(t, "DATA0 payload vs truth", pay, payload)
		if n := len(usblsSigrok(t, sr, dp, dm, "crc5-err")) + len(usblsSigrok(t, sr, dp, dm, "crc16-err")); n != 0 {
			t.Fatalf("sigrok flagged %d CRC errors on clean traffic", n)
		}
	})

	t.Run("zero-length-data", func(t *testing.T) {
		// Zero-length DATA packets — the status stage of every control
		// transfer and the ZLP short-transfer terminator: SYNC+PID+CRC16 with
		// an EMPTY payload. CRC16 over zero payload bits is 0x0000 on the
		// wire; both sides must still frame the packet, decode ZERO data
		// bytes, and pass the CRC check.
		if got := usblsCRC16(nil); got != 0x0000 {
			t.Fatalf("generated truth: CRC16 over empty payload = %04X, want 0000", got)
		}
		want := []string{"OUT", "DATA1", "ACK", "IN", "DATA0", "ACK"}
		pkts := [][]int{
			usblsPacketBits(0x1, usblsTokenBits(5, 2)),  // OUT addr 5 ep 2
			usblsPacketBits(0xB, usblsDataBits(nil, 0)), // DATA1 ZLP (status stage)
			usblsPacketBits(0x2, nil),                   // ACK
			usblsPacketBits(0x9, usblsTokenBits(5, 2)),  // IN
			usblsPacketBits(0x3, usblsDataBits(nil, 0)), // DATA0 ZLP
			usblsPacketBits(0x2, nil),                   // ACK
		}
		dp, dm := oracleUSBLSWave(sr, pkts, 40)
		r := decodeRepo(t, dm, cfg)
		rp := usblsRepoPackets(r)
		oraclePIDs := usblsPIDs(t, usblsSigrok(t, sr, dp, dm, "pid"))
		if len(rp) != len(want) || len(oraclePIDs) != len(want) {
			t.Fatalf("packet counts: repo %d, sigrok %d, want %d", len(rp), len(oraclePIDs), len(want))
		}
		for i, w := range want {
			if rp[i].name != w || oraclePIDs[i] != w {
				t.Fatalf("PID %d: repo %q, sigrok %q, want %q", i, rp[i].name, oraclePIDs[i], w)
			}
		}
		// Neither side invents payload bytes: sigrok's data row is empty
		// across the whole capture, and its packet summaries show the empty
		// brackets ("DATA1 [ ]") that prove the ZLPs framed as DATA packets.
		if n := len(usblsSigrok(t, sr, dp, dm, "data")); n != 0 {
			t.Fatalf("sigrok decoded %d data bytes from zero-length packets", n)
		}
		for _, cls := range []string{"packet-data1", "packet-data0"} {
			anns := usblsSigrok(t, sr, dp, dm, cls)
			wantTxt := strings.ToUpper(strings.TrimPrefix(cls, "packet-")) + " [ ]"
			if len(anns) != 1 || anns[0].Text != wantTxt {
				t.Fatalf("sigrok %s: %+v, want exactly one %q summary", cls, anns, wantTxt)
			}
		}
		// Both CRC16s check clean with the transmitted value 0x0000, and the
		// repo's two raw post-PID bytes ARE that CRC (empty payload). The CRC
		// field also sits where sigrok says it is.
		okAnns := usblsSigrok(t, sr, dp, dm, "crc16-ok")
		eqBytes(t, "ZLP CRC16 values (sigrok)", []int{0x0000, 0x0000}, usblsAnnNum(t, okAnns, "CRC16: ", 16))
		if n := len(usblsSigrok(t, sr, dp, dm, "crc16-err")); n != 0 {
			t.Fatalf("sigrok flagged %d CRC16 errors on clean ZLPs", n)
		}
		for k, idx := range []int{1, 4} { // the two ZLP DATA packets
			if n := len(rp[idx].bytes); n != 2 {
				t.Fatalf("%s ZLP: repo decoded %d post-PID bytes, want exactly 2 (the CRC16)", want[idx], n)
			}
			pay, crc, ok := usblsRepoCRC16(t, rp[idx])
			if len(pay) != 0 || crc != 0x0000 || !ok {
				t.Fatalf("%s ZLP: repo payload %02X crc %04X ok=%v, want empty/0000/true", want[idx], pay, crc, ok)
			}
			if d := rp[idx].i0s[0] - okAnns[k].I0; d > spb || d < -spb {
				t.Fatalf("%s ZLP CRC16 misaligned: repo starts at %d, sigrok at %d", want[idx], rp[idx].i0s[0], okAnns[k].I0)
			}
		}
		// The ACKs stay empty too — the ZLPs must not smear into them.
		if len(rp[2].bytes) != 0 || len(rp[5].bytes) != 0 {
			t.Fatalf("ACKs carry %d/%d bytes, want 0/0", len(rp[2].bytes), len(rp[5].bytes))
		}
	})

	t.Run("corrupted-pid-flagged-by-both", func(t *testing.T) {
		// DATA0's wire PID byte is 0xC3: nibble 0x3 plus its ones-complement
		// 0xC in the check field. Flipping one check-field bit gives 0x43, so
		// the complement test fails. sigrok's usb_packet knows only the 16
		// valid PID bytes -> the lookup misses: "PID: UNKNOWN" plus a
		// packet-invalid summary, and it decodes NO fields after the PID. The
		// repo checks the complement explicitly -> the PID span flips to kind
		// "frame-error" with text "!DATA0" (the base nibble still names it).
		// Both flag the packet; neither emits a valid-DATA0 annotation.
		payload := []int{0xDE, 0xAD}
		crc := usblsCRC16(usblsByteBits(payload)) // CRC left VALID: the corruption is PID-only
		pkts := [][]int{
			usblsPacketBitsRawPID(0x43, usblsDataBits(payload, 0)),
			usblsPacketBits(0xB, usblsDataBits([]int{0x01, 0x02}, 0)), // clean chaser
		}
		dp, dm := oracleUSBLSWave(sr, pkts, 40)
		r := decodeRepo(t, dm, cfg)
		rp := usblsRepoPackets(r)
		pidAnns := usblsSigrok(t, sr, dp, dm, "pid")
		oraclePIDs := usblsPIDs(t, pidAnns)
		if len(rp) != 2 || len(oraclePIDs) != 2 {
			t.Fatalf("packet counts: repo %d, sigrok %d, want 2", len(rp), len(oraclePIDs))
		}
		if oraclePIDs[0] != "UNKNOWN" || oraclePIDs[1] != "DATA1" {
			t.Fatalf("sigrok PIDs %v, want [UNKNOWN DATA1]", oraclePIDs)
		}
		// sigrok: exactly one invalid-packet summary, zero valid DATA0
		// packets, and SYNC still recognized on both frames — the rejection
		// happens at the PID check, not at framing.
		if anns := usblsSigrok(t, sr, dp, dm, "packet-invalid"); len(anns) != 1 || anns[0].Text != "UNKNOWN" {
			t.Fatalf("sigrok packet-invalid: %+v, want exactly one UNKNOWN summary", anns)
		}
		if n := len(usblsSigrok(t, sr, dp, dm, "packet-data0")); n != 0 {
			t.Fatalf("sigrok hallucinated %d valid DATA0 packets from the corrupt PID", n)
		}
		if n := len(usblsSigrok(t, sr, dp, dm, "sync-ok")); n != 2 {
			t.Fatalf("sigrok sync-ok count %d, want 2", n)
		}
		// repo: exactly one frame-error PID (the corrupt one) and one clean
		// PID (the DATA1), and the flagged span sits on the same wire bits as
		// sigrok's UNKNOWN pid annotation.
		if ne, na := countSpans(r, "frame-error"), countSpans(r, "addr"); ne != 1 || na != 1 {
			t.Fatalf("repo PID verdicts: %d frame-error + %d ok, want 1+1", ne, na)
		}
		for _, s := range r.Spans {
			if s.Kind != "frame-error" {
				continue
			}
			if s.Text != "!DATA0" || s.Val != 0x3 {
				t.Fatalf("repo frame-error span text %q val %X, want %q val 3", s.Text, s.Val, "!DATA0")
			}
			if d := s.I0 - pidAnns[0].I0; d > spb || d < -spb {
				t.Fatalf("repo flagged the PID at %d, sigrok at %d", s.I0, pidAnns[0].I0)
			}
		}
		// DIVERGENCE (granularity, pinned per side): after a bad PID sigrok
		// stops decoding fields — its data row carries ONLY the clean DATA1's
		// bytes and its sole CRC16 verdict is the DATA1's — while the repo
		// still dumps the corrupt packet's raw post-PID bytes (payload + the
		// still-valid CRC16, little-endian) under the flagged PID. If the repo
		// ever suppresses those bytes, or sigrok starts decoding them, these
		// pins trip and the intersection assertions must be revisited.
		eqBytes(t, "repo raw bytes of corrupt packet", rp[0].bytes, []int{0xDE, 0xAD, crc & 0xFF, crc >> 8})
		eqBytes(t, "sigrok data row (clean DATA1 only)",
			usblsAnnNum(t, usblsSigrok(t, sr, dp, dm, "data"), "Databyte: ", 16), []int{0x01, 0x02})
		if _, _, ok := usblsRepoCRC16(t, rp[0]); !ok {
			t.Fatalf("corrupt-PID packet's CRC16 must recompute clean (corruption is PID-only)")
		}
		// The clean chaser is unharmed on both sides.
		pay1, crc1, ok1 := usblsRepoCRC16(t, rp[1])
		if !ok1 {
			t.Fatalf("repo CRC16 flagged the clean DATA1")
		}
		eqBytes(t, "clean DATA1 payload", pay1, []int{0x01, 0x02})
		okAnns := usblsSigrok(t, sr, dp, dm, "crc16-ok")
		eqBytes(t, "clean DATA1 CRC16", []int{crc1}, usblsAnnNum(t, okAnns, "CRC16: ", 16))
	})

	t.Run("keep-alive-train-auto-bitrate", func(t *testing.T) {
		// The idle-bus killer the sigrok oracle exposed: a realistic keep-
		// alive train ahead of the traffic floods the edge-gap list with
		// 2-bit SE0 gaps and huge inter-KA gaps. The old blind low-percentile
		// estimator drifted past the packet's few 1-bit gaps, the refined
		// bit period collapsed toward 2 bits, SYNC never matched, and AUTO
		// decode hard-failed ("no USB packet found") on a capture sigrok
		// reads fine — while a pinned bitrate decoded normally. The cluster
		// walk (with >16-bit gaps excluded as non-evidence) must now infer
		// the true rate and decode identically to the pinned run and sigrok.
		const nKA = 60
		pkts := [][]int{usblsPacketBits(0xD, usblsTokenBits(21, 10))}
		dp, dm := oracleUSBLSWaveKA(sr, nKA, 100, pkts, 40)
		if kas := usblsSignalling(t, sr, dp, dm, "keep-alive"); len(kas) != nKA {
			t.Fatalf("sigrok saw %d keep-alives, want %d (vector broken?)", len(kas), nKA)
		}
		oraclePIDs := usblsPIDs(t, usblsSigrok(t, sr, dp, dm, "pid"))
		if len(oraclePIDs) != 1 || oraclePIDs[0] != "SETUP" {
			t.Fatalf("sigrok PIDs %v, want [SETUP]", oraclePIDs)
		}
		rAuto := DecodeUSBLS(bitsToCodes(dm), 1.0/float64(sr), USBLSCfg{}) // Bitrate 0: infer
		if !rAuto.OK {
			t.Fatalf("repo AUTO decode failed on a keep-alive capture: %s", rAuto.Error)
		}
		rPin := DecodeUSBLS(bitsToCodes(dm), 1.0/float64(sr), cfg)
		if !rPin.OK {
			t.Fatalf("repo pinned decode failed: %s", rPin.Error)
		}
		ap, pp := usblsRepoPackets(rAuto), usblsRepoPackets(rPin)
		if len(ap) != 1 || len(pp) != 1 || ap[0].name != pp[0].name {
			t.Fatalf("auto %d packets vs pinned %d", len(ap), len(pp))
		}
		eqBytes(t, "keep-alive auto vs pinned bytes", ap[0].bytes, pp[0].bytes)
	})

	t.Run("fractional-spb", func(t *testing.T) {
		// 25 MHz -> 16.667 samples/bit: cell boundaries drift through sample
		// positions like a real capture; both sides must stay locked for a full
		// token+data+handshake exchange.
		const fsr = 25_000_000
		payload := []int{0xA5, 0x5A, 0xFF, 0x00, 0xC3}
		pkts := [][]int{
			usblsPacketBits(0xD, usblsTokenBits(1, 2)),
			usblsPacketBits(0x3, usblsDataBits(payload, 0)),
			usblsPacketBits(0x2, nil),
		}
		dp, dm := oracleUSBLSWave(fsr, pkts, 40)
		r := DecodeUSBLS(bitsToCodes(dm), 1.0/float64(fsr), cfg)
		if !r.OK {
			t.Fatalf("repo decode failed: %s", r.Error)
		}
		rp := usblsRepoPackets(r)
		oraclePIDs := usblsPIDs(t, usblsSigrok(t, fsr, dp, dm, "pid"))
		if len(rp) != 3 || len(oraclePIDs) != 3 {
			t.Fatalf("packet counts: repo %d, sigrok %d, want 3", len(rp), len(oraclePIDs))
		}
		for i := range rp {
			if rp[i].name != oraclePIDs[i] {
				t.Fatalf("PID %d: repo %q, sigrok %q", i, rp[i].name, oraclePIDs[i])
			}
		}
		addr, ep, _ := usblsTokenFields(t, rp[0])
		eqBytes(t, "fractional addr/endp", []int{addr, ep},
			append(usblsAnnNum(t, usblsSigrok(t, fsr, dp, dm, "addr"), "Address: ", 10),
				usblsAnnNum(t, usblsSigrok(t, fsr, dp, dm, "ep"), "Endpoint: ", 10)...))
		pay, _, ok := usblsRepoCRC16(t, rp[1])
		if !ok {
			t.Fatalf("repo CRC16 check failed at fractional spb")
		}
		eqBytes(t, "fractional payload", pay, usblsAnnNum(t, usblsSigrok(t, fsr, dp, dm, "data"), "Databyte: ", 16))
		if n := len(usblsSigrok(t, fsr, dp, dm, "crc16-err")); n != 0 {
			t.Fatalf("sigrok flagged %d CRC16 errors at fractional spb", n)
		}
	})
}
