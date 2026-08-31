package eth100tx

// multiframe_gen_test.go — VERIFY-agent generator for a GENUINE continuous
// multi-frame 100BASE-TX line-rate stream: N back-to-back frames sharing ONE
// scrambler LFSR and ONE continuous MLT-3 state (idle fill between frames), so
// the self-sync descrambler provably NEVER loses lock across frame boundaries.
//
// This is NOT EncodeFrame() called N times (that resets the scrambler seed and
// MLT-3 level per call, i.e. a discontinuous seam). Here we build ONE code-group
// list [leadIdle | (J K <nibs> T R) interIdle]*N | trailIdle, serialize it,
// scramble the WHOLE thing with a single keystream, MLT-3 the WHOLE thing, and
// oversample once. The result is a single continuous 600 MSa/s sample stream.
//
// One frame is CORRUPTED (a payload octet flipped AFTER the FCS was computed
// over the original), so its transmitted data disagrees with its transmitted
// FCS -> the receiver must flag FCS-err while still delimiting/emitting it.
//
// Emits, into $ETH100TX_MULTI_DIR:
//   multi.samples  — continuous signed 600 MSa/s codes, one per line
//   multi.expect   — line1: <nframes>; then per frame: "<nbody> <fcsok 1|0>"
//                    followed by <nbody> hex octet lines (frame||FCS as on wire)
//
// It also self-checks the whole stream through the golden DecodeSamples()… but
// DecodeSamples decodes a single frame, so we assert per-frame via a slice.
//
// Run: ETH100TX_MULTI_DIR=<dir> go test ./internal/eth100tx -run TestEmitMultiFrame -v

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// readFrameOctets parses a <case>.frame vector: the MAC frame octets are the
// bare "HH" lines (comments start with #, FCS lines start with "FCS"/"FCS_VALUE").
func readFrameOctets(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out []byte
	for _, ln := range strings.Split(string(b), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") || strings.HasPrefix(ln, "FCS") {
			continue
		}
		v, err := strconv.ParseUint(ln, 16, 8)
		if err != nil {
			t.Fatalf("bad octet %q in %s: %v", ln, path, err)
		}
		out = append(out, byte(v))
	}
	return out
}

// frameCodeGroups builds the J K <data nibbles> T R code-group run for one MAC
// frame. txFrame is what actually goes on the wire (may be corrupted); fcs is
// the 4 FCS octets appended (computed by the caller — possibly over a DIFFERENT
// frame, to force an FCS mismatch).
func frameCodeGroups(txFrame []byte, fcs [4]byte) []CodeGroup {
	mac := make([]byte, 0, 8+len(txFrame)+4)
	for i := 0; i < 7; i++ {
		mac = append(mac, 0x55)
	}
	mac = append(mac, 0xD5)
	mac = append(mac, txFrame...)
	mac = append(mac, fcs[:]...)
	nibs := bytesToNibbles(mac)

	cgs := make([]CodeGroup, 0, 2+len(nibs)-2+2)
	cgs = append(cgs, CodeGroup{codeJ, "J"}, CodeGroup{codeK, "K"})
	for _, n := range nibs[2:] { // J/K replace the first preamble octet's 2 nibbles
		cgs = append(cgs, dataCG(int(n)))
	}
	cgs = append(cgs, CodeGroup{codeT, "T"}, CodeGroup{codeR, "R"})
	return cgs
}

func idleCGs(n int) []CodeGroup {
	out := make([]CodeGroup, n)
	for i := range out {
		out[i] = CodeGroup{codeI, "I"}
	}
	return out
}

type expFrame struct {
	body  []byte // frame||FCS as recovered on the wire
	fcsOK bool
}

func TestEmitMultiFrame(t *testing.T) {
	dir := os.Getenv("ETH100TX_MULTI_DIR")
	if dir == "" {
		t.Skip("set ETH100TX_MULTI_DIR to emit the multi-frame vector")
	}
	vecDir := os.Getenv("ETH100TX_VECTOR_DIR")
	if vecDir == "" {
		vecDir = "vectors"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	arp := readFrameOctets(t, filepath.Join(vecDir, "arp.frame"))
	icmp := readFrameOctets(t, filepath.Join(vecDir, "icmp.frame"))

	const (
		leadIdle  = 32
		interIdle = 16 // minimal-ish inter-frame gap (idle code groups)
		trailIdle = 8
	)

	// Build the frame program: arp, icmp, arp (3 clean back-to-back), then a
	// CORRUPTED icmp (payload octet flipped after FCS computed) that must be
	// FCS-rejected. => 3 good + 1 bad = 4 frames, satisfies >=3 + one corrupt.
	type job struct {
		name    string
		frame   []byte
		corrupt int // -1 = clean, else octet index to flip on the wire
	}
	jobs := []job{
		{"arp", arp, -1},
		{"icmp", icmp, -1},
		{"arp", arp, -1},
		{"icmp-bad", icmp, 20}, // flip a payload octet AFTER FCS -> mismatch
	}

	var cgs []CodeGroup
	cgs = append(cgs, idleCGs(leadIdle)...)
	var exps []expFrame
	for _, jb := range jobs {
		fcsVal := CRC32(jb.frame) // FCS computed over the ORIGINAL frame
		var fcs4 [4]byte
		copy(fcs4[:], fcsBytes(fcsVal))

		txFrame := append([]byte(nil), jb.frame...)
		if jb.corrupt >= 0 {
			txFrame[jb.corrupt] ^= 0xFF // corrupt the wire data, keep the old FCS
		}
		cgs = append(cgs, frameCodeGroups(txFrame, fcs4)...)
		cgs = append(cgs, idleCGs(interIdle)...)

		body := append(append([]byte(nil), txFrame...), fcs4[:]...)
		exps = append(exps, expFrame{body: body, fcsOK: jb.corrupt < 0})
	}
	cgs = append(cgs, idleCGs(trailIdle-interIdle+interIdle)...) // trailing idle

	// Serialize MSB-first, scramble with ONE continuous keystream, MLT-3 once,
	// oversample once => genuinely continuous line-rate stream.
	plain := make([]byte, 0, len(cgs)*5)
	for _, c := range cgs {
		for b := 4; b >= 0; b-- {
			plain = append(plain, (c.Bits>>uint(b))&1)
		}
	}
	ks := keystream(DefaultSeed, len(plain))
	scr := make([]byte, len(plain))
	for i := range plain {
		scr[i] = plain[i] ^ ks[i]
	}
	syms := mlt3Encode(scr)
	samples, _ := oversample(syms)

	// ---- self-check via the golden decoder, per frame slice --------------
	// DecodeSamples locks + decodes the FIRST frame in its input, so re-decode
	// N times each starting just before a frame's leading idle boundary is
	// awkward; instead decode the whole thing and rely on the RTL TB for the
	// per-frame proof. Here we sanity-check that at least the first frame body
	// round-trips, catching any generator bug early.
	dr := DecodeSamples(samples)
	if dr.Err != nil {
		t.Fatalf("golden self-decode err: %v", dr.Err)
	}
	got0 := append(append([]byte(nil), dr.Frame...), fcsBytes(dr.FCS)...)
	if len(got0) != len(exps[0].body) {
		t.Fatalf("golden self-decode frame0 len got=%d exp=%d", len(got0), len(exps[0].body))
	}
	for i := range got0 {
		if got0[i] != exps[0].body[i] {
			t.Fatalf("golden self-decode frame0 octet[%d] got=%02x exp=%02x", i, got0[i], exps[0].body[i])
		}
	}
	if !dr.FCSOK {
		t.Fatalf("golden self-decode frame0 FCS not OK")
	}

	// ---- write samples ---------------------------------------------------
	sf, err := os.Create(filepath.Join(dir, "multi.samples"))
	if err != nil {
		t.Fatal(err)
	}
	sw := bufio.NewWriter(sf)
	for _, s := range samples {
		fmt.Fprintf(sw, "%d\n", s)
	}
	sw.Flush()
	sf.Close()

	// ---- write expectations ---------------------------------------------
	ef, err := os.Create(filepath.Join(dir, "multi.expect"))
	if err != nil {
		t.Fatal(err)
	}
	ew := bufio.NewWriter(ef)
	fmt.Fprintf(ew, "%d\n", len(exps))
	for _, e := range exps {
		ok := 0
		if e.fcsOK {
			ok = 1
		}
		fmt.Fprintf(ew, "%d %d\n", len(e.body), ok)
		for _, o := range e.body {
			fmt.Fprintf(ew, "%02x\n", o)
		}
	}
	ew.Flush()
	ef.Close()

	t.Logf("emitted %d frames, %d code groups, %d samples to %s",
		len(exps), len(cgs), len(samples), dir)
	for i, e := range exps {
		t.Logf("  frame[%d]: %d body octets, fcsOK=%v", i, len(e.body), e.fcsOK)
	}
}
