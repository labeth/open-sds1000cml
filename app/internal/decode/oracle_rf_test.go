// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

// Oracle cross-checks of the radio and timing line codes against sigrok's
// rc_encode, ook with ook_oregon, miller, morse and dcf77 decoders
// (oracle_test.go).

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// sigrokOption reports whether the installed sigrok's decoder pd takes
// option opt (a later release's): the run is tried on a one-sample capture.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
func sigrokOption(t *testing.T, pd, ch, opt string) bool {
	t.Helper()
	needSigrok(t)
	dir := t.TempDir()
	csv := dir + "/w.csv"
	if err := os.WriteFile(csv, []byte("D\n0\n0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := exec.Command("sigrok-cli", "-i", csv, "-I", "csv:column_formats=1l:samplerate=1000000",
		"-P", pd+":"+ch+"=D:"+opt, "-A", pd).Run()
	return err == nil
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
func TestOracleRCEncode(t *testing.T) {
	const sr = 200000
	const a = 350e-6
	w := newTimeline(sr)
	w.add(0, 40*a)
	frames := [][]int{{0, 1, 2, 0, 0, 1, 1, 2, 0, 1, 0, 0}, {2, 2, 2, 2, 2, 2, 0, 0, 0, 1, 1, 1}, {1, 0, 1, 0, 1, 0, 1, 0, 2, 0, 2, 1}}
	for _, f := range frames {
		rcFrame2262(w, f, a)
	}
	r := DecodeRCEncode(w.codes(), 1.0/sr, LineCfg{})
	if !r.OK || r.FrameBits != 12 {
		t.Fatalf("%v %q", r.FrameBits, r.Error)
	}
	var repo []string
	for _, sp := range r.Spans {
		switch {
		case sp.Text == "SYNC":
			repo = append(repo, "Sync")
		case sp.Kind == "data":
			repo = append(repo, strings.ToLower(sp.Text))
		}
	}
	anns := sigrokDecode(t, sr, []string{"D"}, [][]byte{w.bits}, "rc_encode:data=D", "rc_encode=bits")
	var orc []string
	for _, x := range sortAnns(anns) {
		orc = append(orc, x.Text)
	}
	if fmt.Sprint(repo) != fmt.Sprint(orc) {
		t.Fatalf("RC encode differs:\n repo   %v\n sigrok %v", repo, orc)
	}
	if !sigrokOption(t, "rc_encode", "data", "linecoding=xx1527") {
		return // the 0.5 releases read the PT2262 coding only
	}
	w = newTimeline(sr)
	w.add(0, 40*a)
	for _, v := range []int{0x5A3C81, 0x000FFF, 0xA5A5A5} {
		rcFrame1527(w, v, a)
	}
	r = DecodeRCEncode(w.codes(), 1.0/sr, LineCfg{Variant: 24})
	if !r.OK || r.FrameBits != 24 {
		t.Fatalf("1527: %v %q", r.FrameBits, r.Error)
	}
	repo = nil
	for _, sp := range r.Spans {
		switch {
		case sp.Text == "SYNC":
			repo = append(repo, "Sync")
		case sp.Kind == "data":
			for k := 0; k < 8; k++ {
				repo = append(repo, strconv.Itoa(sp.Val>>k&1))
			}
		}
	}
	anns = sigrokDecode(t, sr, []string{"D"}, [][]byte{w.bits}, "rc_encode:data=D:linecoding=xx1527", "rc_encode=bits")
	orc = nil
	for _, x := range sortAnns(anns) {
		orc = append(orc, x.Text)
	}
	if fmt.Sprint(repo) != fmt.Sprint(orc) {
		t.Fatalf("EV1527 differs:\n repo   %v\n sigrok %v", repo, orc)
	}
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
func TestOracleOOKOregon(t *testing.T) {
	const sr = 100000
	const hp = 1 / 2048.0
	w := newTimeline(sr)
	w.add(0, 20e-3)
	// protocol 3: THGN800 (F824) channel 1, temperature and humidity
	oregonBurst(w, 3, []int{0xF, 0x8, 0x2, 0x4, 0x1, 0x3, 0xA, 0x0, 0x5, 0x3, 0x2, 0x0, 0x4, 0x5, 0x0}, hp, false, 40e-3)
	// protocol 2.1: THGR228N (1A2D)
	oregonBurst(w, 21, []int{0x1, 0xA, 0x2, 0xD, 0x2, 0x7, 0x1, 0x8, 0x2, 0x6, 0x1, 0x0, 0x4, 0x6}, hp/2, true, 40e-3)
	r := DecodeOOK(w.codes(), 1.0/sr, LineCfg{})
	if !r.OK {
		t.Fatal(r.Error)
	}
	var repo []string
	cur := ""
	for _, sp := range r.Spans {
		switch sp.Kind {
		case "sof":
			if cur != "" {
				repo = append(repo, cur)
			}
			cur = ""
		case "data", "frame-error":
			cur += strings.ToLower(strings.TrimPrefix(sp.Text, "!"))
		}
	}
	repo = append(repo, cur)
	anns := sigrokDecode(t, sr, []string{"D"}, [][]byte{w.bits}, "ook:data=D,ook_oregon", "ook_oregon=field")
	var orc []string
	cur = ""
	for _, x := range sortAnns(anns) {
		f := strings.Fields(x.Text)
		switch {
		case strings.Contains(x.Text, "Preamble"):
			if cur != "" {
				orc = append(orc, cur)
			}
			cur = ""
		case x.Text == "Sync":
		default:
			cur += f[len(f)-1]
		}
	}
	orc = append(orc, cur)
	// sigrok's fields cover the burst's whole nibbles: the checksum too
	if fmt.Sprint(repo) != fmt.Sprint(orc) {
		t.Fatalf("Oregon differs:\n repo   %v\n sigrok %v", repo, orc)
	}
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
func TestOracleMiller(t *testing.T) {
	const sr = 4000000
	const baud = 106000
	T := 1.0 / baud
	msgs := [][]int{{1, 0, 1, 1, 0, 0, 1, 0, 0, 0, 1, 1, 1, 0, 1, 0}, {0, 0, 0, 0, 1, 1, 1, 1, 0, 1, 0, 1, 1, 0}, {1, 1, 0, 1, 0, 0, 1, 1, 0}}
	for _, mode := range []struct {
		edge  string
		pause float64
	}{{"falling", 2e-6}, {"either", 0}} {
		w := newTimeline(sr)
		w.add(1, 40e-6)
		for _, m := range msgs {
			millerMessage(w, m, T, mode.pause, 60e-6)
		}
		r := DecodeMiller(w.codes(), 1.0/sr, LineCfg{})
		if !r.OK {
			t.Fatal(mode.edge, r.Error)
		}
		var repo []string
		var bits []int
		flush := func() {
			if bits != nil {
				repo = append(repo, fmt.Sprint(bits))
			}
			bits = nil
		}
		for _, sp := range r.Spans {
			switch sp.Kind {
			case "sof":
				flush()
				bits = []int{}
			case "data":
				for k := 0; k < 8; k++ {
					bits = append(bits, sp.Val>>k&1)
				}
			}
		}
		flush()
		anns := sigrokDecode(t, sr, []string{"D"}, [][]byte{w.bits}, fmt.Sprintf("miller:data=D:baudrate=%d:edge=%s", baud, mode.edge), "miller=bitstring")
		var orc []string
		for _, x := range sortAnns(anns) {
			var b []int
			for _, c := range strings.ReplaceAll(x.Text, " ", "") {
				b = append(b, int(c-'0'))
			}
			orc = append(orc, fmt.Sprint(b))
		}
		if len(repo) != len(orc) {
			t.Fatalf("Miller %s: %d messages, sigrok %d\n repo   %v\n sigrok %v", mode.edge, len(repo), len(orc), repo, orc)
		}
		for i := range orc {
			// our last byte is whole: sigrok's bits are its first ones
			o := strings.Trim(orc[i], "[]")
			if !strings.HasPrefix(strings.Trim(repo[i], "[]"), o) {
				t.Fatalf("Miller %s message %d:\n repo   %v\n sigrok %v", mode.edge, i, repo[i], orc[i])
			}
		}
	}
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
func TestOracleMorse(t *testing.T) {
	const sr = 2000
	w := newTimeline(sr)
	w.add(0, 0.5)
	morseText(w, "SOS CQ DE SM5 TEST 73 ABC.", 0.1, 12)
	r := DecodeMorse(w.codes(), 1.0/sr, LineCfg{})
	if !r.OK {
		t.Fatal(r.Error)
	}
	var repo []string
	for _, sp := range r.Spans {
		if sp.Kind == "data" {
			repo = append(repo, strings.ToLower(sp.Text))
		}
	}
	anns := sigrokDecode(t, sr, []string{"D"}, [][]byte{w.bits}, "morse:data=D", "morse=letter")
	var orc []string
	for _, x := range sortAnns(anns) {
		orc = append(orc, x.Text)
	}
	if fmt.Sprint(repo) != fmt.Sprint(orc) {
		t.Fatalf("Morse differs:\n repo   %v\n sigrok %v", repo, orc)
	}
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
func TestOracleDCF77(t *testing.T) {
	const sr = 1000
	w := newTimeline(sr)
	w.add(0, 1.3)
	dcf77Minute(w, 0x00, 0x00, 0x01, 0x01, 0x01, 0x00, false) // a lead-in minute: no marker before it
	dcf77Minute(w, 0x37, 0x12, 0x18, 0x7, 0x10, 0x26, false)
	dcf77Minute(w, 0x38, 0x12, 0x18, 0x7, 0x10, 0x26, true)
	dcf77Minute(w, 0x59, 0x23, 0x31, 0x5, 0x12, 0x99, false)
	r := DecodeDCF77(w.codes(), 1.0/sr, LineCfg{})
	if !r.OK {
		t.Fatal(r.Error)
	}
	bcd := func(v int) int { return v>>4*10 + v&15 }
	var repo []string
	for _, sp := range r.Spans {
		if sp.Kind == "data" || sp.Kind == "parity-error" {
			name := strings.TrimRight(strings.TrimPrefix(sp.Text, "!"), "0123456789ABCDEF")
			repo = append(repo, fmt.Sprintf("%s%d", name, bcd(sp.Val)))
			if sp.Kind == "parity-error" {
				repo = append(repo, "BAD")
			}
		}
	}
	anns := sigrokDecode(t, sr, []string{"D"}, [][]byte{w.bits}, "dcf77:data=D", "dcf77=fields")
	names := map[string]string{"Minutes": "MIN", "Hours": "HR", "Day": "DAY", "Day of week": "DOW", "Month": "MON", "Year": "YR"}
	var orc []string
	for _, x := range sortAnns(anns) {
		k := strings.Index(x.Text, ": ")
		if k < 0 {
			continue
		}
		key, val := x.Text[:k], strings.Fields(x.Text[k+2:])[0]
		if strings.Contains(key, "parity") {
			if val != "OK" {
				orc = append(orc, "BAD")
			}
			continue
		}
		if nm, ok := names[key]; ok {
			orc = append(orc, nm+val)
		}
	}
	// the parity flag follows its field (sigrok: the parity bit after it)
	if fmt.Sprint(repo) != fmt.Sprint(orc) {
		t.Fatalf("DCF77 differs:\n repo   %v\n sigrok %v", repo, orc)
	}
}
