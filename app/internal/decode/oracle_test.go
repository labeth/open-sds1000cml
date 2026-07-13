package decode

// Oracle cross-check harness: the Go decoders are validated against
// libsigrokdecode's protocol decoders (via sigrok-cli) on IDENTICAL synthetic
// waveforms — sigrok, with a decade of protocol-analyzer field use, is the
// reference implementation. Each oracle_<proto>_test.go generates traffic
// (normal + edge cases), decodes it with the repo decoder AND with sigrok,
// and asserts the two agree on payload bytes, error detection, and (where
// asserted) sample alignment.
//
// sigrok-cli sees the waveform as logic over its CSV input (one 0/1 column
// per line, samplerate given as an option); the repo decoders see the same
// waveform as two-level ADC codes. Skips when sigrok-cli is absent;
// CI_REQUIRE_SIGROK=1 turns the skip into a hard failure so the dedicated CI
// lane can never lose the suite silently. Protocols sigrok has no decoder
// for (Manchester, SENT, ARINC 429, MIL-STD-1553) cannot be oracled here —
// they are covered by the package's own round-trip and break tests.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const (
	oracleLo = uint8(56)  // logic-0 ADC code (well below the 128 threshold)
	oracleHi = uint8(200) // logic-1 ADC code
)

func needSigrok(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("sigrok-cli")
	if err != nil {
		if os.Getenv("CI_REQUIRE_SIGROK") == "1" {
			t.Fatal("CI_REQUIRE_SIGROK=1 but sigrok-cli is not installed")
		}
		t.Skip("sigrok-cli not installed; skipping oracle cross-check")
	}
	return p
}

// bitsToCodes maps logic levels to the two-level ADC codes the repo decoders
// consume.
func bitsToCodes(bits []byte) []uint8 {
	out := make([]uint8, len(bits))
	for i, b := range bits {
		if b != 0 {
			out[i] = oracleHi
		} else {
			out[i] = oracleLo
		}
	}
	return out
}

// timeline builds a logic waveform by appending levels for durations, with
// float sample positions floored at boundaries — so non-integer
// samples-per-bit accumulate exactly like a real capture of an async source.
type timeline struct {
	sr   float64 // samples per second
	t    float64 // current time, seconds
	bits []byte
}

func newTimeline(sampleRate float64) *timeline { return &timeline{sr: sampleRate} }

func (w *timeline) add(level byte, dur float64) {
	w.t += dur
	for int(len(w.bits)) < int(w.t*w.sr) {
		w.bits = append(w.bits, level)
	}
}

// ann is one parsed sigrok-cli annotation.
type ann struct {
	I0, I1 int
	Text   string
}

var annRe = regexp.MustCompile(`^(\d+)-(\d+) [\w-]+: (.*)$`)

// sigrokDecode writes the waveform channels as a logic CSV, runs one sigrok
// protocol decoder over it, and returns the annotations of ONE annotation
// class (invoked per class — sigrok-cli's -A output does not name the class
// per line, so mixing classes would be ambiguous). channels preserve order;
// names must match the -P channel assignments (e.g. rx=RX).
func sigrokDecode(t *testing.T, sampleRate int, names []string, chans [][]byte, pdSpec, annSpec string) []ann {
	t.Helper()
	needSigrok(t)
	n := 0
	for _, c := range chans {
		if len(c) > n {
			n = len(c)
		}
	}
	var sb strings.Builder
	sb.WriteString(strings.Join(names, ",") + "\n")
	for i := 0; i < n; i++ {
		for k, c := range chans {
			if k > 0 {
				sb.WriteByte(',')
			}
			v := byte(0)
			if i < len(c) {
				v = c[i]
			}
			sb.WriteByte('0' + v)
		}
		sb.WriteByte('\n')
	}
	dir := t.TempDir()
	csv := filepath.Join(dir, "wave.csv")
	if err := os.WriteFile(csv, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	fmtSpec := fmt.Sprintf("%dl", len(chans))
	args := []string{
		"-i", csv,
		"-I", fmt.Sprintf("csv:column_formats=%s:samplerate=%d", fmtSpec, sampleRate),
		"-P", pdSpec,
		"-A", annSpec,
		"--protocol-decoder-samplenum",
	}
	out, err := exec.Command("sigrok-cli", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("sigrok-cli %v failed: %v\n%s", args, err, out)
	}
	var anns []ann
	for _, line := range strings.Split(string(out), "\n") {
		m := annRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		i0, _ := strconv.Atoi(m[1])
		i1, _ := strconv.Atoi(m[2])
		anns = append(anns, ann{I0: i0, I1: i1, Text: strings.Trim(m[3], `"`)})
	}
	return anns
}

// annBytes parses each annotation text as a hex byte (the PDs' default data
// format) and returns the byte sequence.
func annBytes(t *testing.T, anns []ann) []int {
	t.Helper()
	out := make([]int, 0, len(anns))
	for _, a := range anns {
		v, err := strconv.ParseInt(strings.TrimSpace(a.Text), 16, 32)
		if err != nil {
			t.Fatalf("annotation %q is not a hex byte", a.Text)
		}
		out = append(out, int(v))
	}
	return out
}

// spanBytes extracts the decoded payload bytes from a repo decode Result.
func spanBytes(res Result, kind string) []int {
	var out []int
	for _, s := range res.Spans {
		if s.Kind == kind {
			out = append(out, s.Val)
		}
	}
	return out
}

// countSpans reports whether any span of the given kind exists.
func countSpans(res Result, kind string) int {
	n := 0
	for _, s := range res.Spans {
		if s.Kind == kind {
			n++
		}
	}
	return n
}

// eqBytes asserts two byte sequences match exactly, with a readable diff.
func eqBytes(t *testing.T, what string, repo, oracle []int) {
	t.Helper()
	if len(repo) != len(oracle) {
		t.Fatalf("%s: repo decoded %d bytes, sigrok %d\n repo:   %02X\n sigrok: %02X", what, len(repo), len(oracle), repo, oracle)
	}
	for i := range repo {
		if repo[i] != oracle[i] {
			t.Fatalf("%s: byte %d differs: repo %02X, sigrok %02X\n repo:   %02X\n sigrok: %02X", what, i, repo[i], oracle[i], repo, oracle)
		}
	}
}

// eqAligned asserts each repo span (of a kind) starts AND ends within tol
// samples of the corresponding oracle annotation — payload agreement AND
// placement. Checking both edges catches a decoder whose spans have the right
// anchor but the wrong extent (an off-by-one bit count reads correctly at the
// start and smears the end).
// An optional endTol overrides tol for the end edge only: some PDs render a
// systematically longer extent by convention (sigrok's i2c data annotation
// runs one SCL period further, through the ACK clock edge) — pass the
// convention delta there so real smearing still fails.
func eqAligned(t *testing.T, what string, res Result, kind string, anns []ann, tol int, endTol ...int) {
	t.Helper()
	et := tol
	if len(endTol) > 0 {
		et = endTol[0]
	}
	var spans []Span
	for _, s := range res.Spans {
		if s.Kind == kind {
			spans = append(spans, s)
		}
	}
	if len(spans) != len(anns) {
		t.Fatalf("%s: repo has %d %s spans, sigrok %d annotations", what, len(spans), kind, len(anns))
	}
	for i := range spans {
		if d := spans[i].I0 - anns[i].I0; d > tol || d < -tol {
			t.Fatalf("%s: item %d misaligned: repo starts at %d, sigrok at %d (tol %d)", what, i, spans[i].I0, anns[i].I0, tol)
		}
		if d := spans[i].I1 - anns[i].I1; d > et || d < -et {
			t.Fatalf("%s: item %d wrong extent: repo ends at %d, sigrok at %d (tol %d)", what, i, spans[i].I1, anns[i].I1, et)
		}
	}
}
