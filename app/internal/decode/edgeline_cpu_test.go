// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

// CPU of the digital stream's decode over samples (the words unpacked to
// 0/255 samples, decoded sample by sample) and over edges (EdgeLine), a
// second of each edge protocol's dense traffic at the digital stream's
// period, in the engine's pushes. On the PC:
//
//	SDS_STREAM_CPU=1 go test ./internal/decode -run TestEdgeStreamCPU -v
//
// and on the scope, cross-compiled (GOOS=linux GOARCH=arm GOARM=7 go test -c):
//
//	SDS_STREAM_CPU=1 ./decode-arm.test -test.run TestEdgeStreamCPU -test.v
//
// SDS_STREAM_CPU_ONLY=maple,swd picks protocols, SDS_STREAM_CPU_SECS the
// traffic's length (0.25 s).

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// unpackWords is the engine's unpackBits: sixteen samples a word of line
// ch, 0 or 255.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func unpackWords(dst []uint8, raw []byte, ch int) []uint8 {
	o := 2 * (ch & 1)
	for i := 0; i+4 <= len(raw); i += 4 {
		w := uint16(raw[i+o]) | uint16(raw[i+o+1])<<8
		for k := 0; k < 16; k++ {
			dst = append(dst, uint8(0-(w>>k&1)))
		}
	}
	return dst
}

// levelWords packs two lines of levels (0 or 1) into digital words: a on
// CH1, b on CH2.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func levelWords(a, b []byte) []byte {
	n := min(len(a), len(b)) / 16
	raw := make([]byte, 4*n)
	for i := 0; i < n; i++ {
		var wa, wb uint16
		for k := 0; k < 16; k++ {
			wa |= uint16(a[16*i+k]&1) << k
			wb |= uint16(b[16*i+k]&1) << k
		}
		raw[4*i], raw[4*i+1], raw[4*i+2], raw[4*i+3] = byte(wa), byte(wa>>8), byte(wb), byte(wb>>8)
	}
	return raw
}

// edgeCPURun decodes the words as the engine's stream does, over samples
// or edges, pushing pushN samples at a time, and returns the frames.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func edgeCPURun(tc streamCPUCase, raw []byte, ss float64, edges bool, edec func(a, b *EdgeLine, s float64) Result) int {
	closeN, maxN := int(tc.quiet/ss), int((tc.longest+tc.quiet)/ss)
	pushN := min(int(math.Max(0.2, math.Min(1, 2*tc.quiet))/ss), 1<<18)
	words := max(1, pushN/16)
	two := tc.c.lines == 2
	frames := 0
	if edges {
		d := NewEdgeStreamDecoder(func(x, y *EdgeLine) Result { return edec(x, y, ss) }, closeN, maxN)
		d.SetLead(int(tc.lead / ss))
		d.SetSubframes(tc.c.name == "spdif")
		var a, b EdgeLine
		for at := 0; at < len(raw)/4; at += words {
			w := raw[4*at : 4*min(len(raw)/4, at+words)]
			a.Reset()
			a.AppendWords(w, 0)
			var bl *EdgeLine
			if two {
				b.Reset()
				b.AppendWords(w, 1)
				bl = &b
			}
			frames += len(d.PushEdges(&a, bl, 0, a.N))
		}
		return frames + len(d.Flush())
	}
	d := NewStreamDecoder(func(x, y []uint8) Result { return tc.c.dec(x, y, ss) }, closeN, maxN)
	d.SetLead(int(tc.lead / ss))
	d.SetSubframes(tc.c.name == "spdif")
	var a, b []uint8
	for at := 0; at < len(raw)/4; at += words {
		w := raw[4*at : 4*min(len(raw)/4, at+words)]
		a = unpackWords(a[:0], w, 0)
		var bl []uint8
		if two {
			b = unpackWords(b[:0], w, 1)
			bl = b
		}
		frames += len(d.Push(a, bl))
	}
	return frames + len(d.Flush())
}

// TestEdgeStreamCPU reports each edge protocol's decode CPU a second of
// dense stream over samples and over edges, and the edges a second.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func TestEdgeStreamCPU(t *testing.T) {
	if os.Getenv("SDS_STREAM_CPU") == "" {
		t.Skip("SDS_STREAM_CPU=1 to measure")
	}
	names := make([]string, 0, len(edgeStreamDecs))
	for n := range edgeStreamDecs {
		names = append(names, n)
	}
	sort.Strings(names)
	if only := os.Getenv("SDS_STREAM_CPU_ONLY"); only != "" {
		names = strings.Split(only, ",")
	}
	secs := 0.25
	if v, err := strconv.ParseFloat(os.Getenv("SDS_STREAM_CPU_SECS"), 64); err == nil && v > 0 {
		secs = v
	}
	var sb strings.Builder
	p := func(f string, a ...any) { fmt.Fprintf(&sb, f, a...) }
	p("%s/%s; CPU ms a second of dense stream (%% of one core)\n", runtime.GOOS, runtime.GOARCH)
	p("%-9s %9s %5s %10s %10s | %9s %9s %7s | %8s\n", "protocol", "period", "lines", "samples/s", "edges/s", "samples", "edges", "x", "ns/edge")
	for _, name := range names {
		tc := streamCPUCaseOf(t, name)
		ss := edgeStreamPeriod(tc)
		dur := max(secs, 3*(tc.longest+tc.quiet))
		la, lb := streamBitsLines(tc, 7, dur, ss, false)
		raw := levelWords(la, lb)
		lines := tc.c.lines
		var edges int
		for ch := 0; ch < lines; ch++ {
			var l EdgeLine
			l.AppendWords(raw, ch)
			edges += len(l.At)
		}
		span := float64(len(raw)/4*16) * ss
		fs, fe := edgeCPURun(tc, raw, ss, false, nil), edgeCPURun(tc, raw, ss, true, edgeStreamDecs[name])
		if fs != fe {
			t.Errorf("%s: %d frames over edges, %d over samples", name, fe, fs)
		}
		sNs := cpuOf(func() { edgeCPURun(tc, raw, ss, false, nil) })
		eNs := cpuOf(func() { edgeCPURun(tc, raw, ss, true, edgeStreamDecs[name]) })
		p("%-9s %7.3gus %5d %10.3g %10.3g | %9.2f %9.2f %7.1f | %8.0f\n", name, ss*1e6, lines, 1/ss, float64(edges)/span,
			sNs/span/1e6, eNs/span/1e6, sNs/eNs, eNs/float64(max(edges, 1)))
	}
	t.Log("\n" + sb.String())
	if path := os.Getenv("SDS_STREAM_CPU_OUT"); path != "" {
		_ = os.WriteFile(path, []byte(sb.String()), 0o644)
	}
}
