// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// streamFuzzCase is the gap-free stream of a protocol's frames, as the
// engine's decoder meets it: peak samples at the protocol's bucket.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func streamFuzzCase(c streamCase, seed int64, frames int) (a, b []uint8, ss float64, closeN, maxN, leadN int, ok bool) {
	name := strings.TrimSuffix(c.name, "-carrier")
	feature, longest, quiet, ok := StreamTiming(name, c.baud)
	if !ok {
		return nil, nil, 0, 0, 0, 0, false
	}
	log, ok := StreamBucketLog(feature)
	if !ok {
		return nil, nil, 0, 0, 0, 0, false
	}
	rng := rand.New(rand.NewSource(seed))
	bucket := float64(uint64(1)<<log) * 2e-9
	const m = 8
	fine := newTwoLine(m / bucket)
	idle := [2]byte{1, 1}
	if name == "j1850" || c.name == "irnec-carrier" {
		idle[0] = 0
	}
	if c.idle != nil {
		idle = *c.idle
	}
	fine.add(idle[0], idle[1], quiet+2e-3)
	for i := 0; i < frames; i++ {
		c.gen(fine, i, rng)
	}
	fine.add(idle[0], idle[1], 2*quiet+longest)
	a, b = peakStream(fine.a.codes(), m, rng), peakStream(fine.b.codes(), m, rng)
	if c.lines != 2 {
		b = nil
	}
	ss = bucket / 2
	return a, b, ss, int(quiet / ss), int((longest + quiet) / ss), int(StreamLead(name, c.baud) / ss), true
}

// streamTranscript is the frames' content and starts (a frame's end can
// move a sample or two with what follows it in the window: a decoder ends
// its last span at the next edge it saw).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func streamTranscript(fs []StreamFrame) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = fmt.Sprintf("%d %s", f.I0, frameKey(f))
	}
	return out
}

// FuzzStreamChunks splits each gap-free protocol's stream at the places the
// fuzzer picks: the frames handed out are the stream's in one push, none
// lost, doubled or moved by a chunk boundary.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func FuzzStreamChunks(f *testing.F) {
	cases := append(append([]streamCase(nil), streamCases...), moreStreamCases...)
	for i := range cases {
		f.Add(uint8(i), int64(i), uint8(6), []byte{1, 200, 7, 64, 3})
	}
	f.Fuzz(func(t *testing.T, ci uint8, seed int64, frames uint8, sizes []byte) {
		c := cases[int(ci)%len(cases)]
		a, b, ss, closeN, maxN, leadN, ok := streamFuzzCase(c, seed, 3+int(frames)%10)
		if !ok {
			return
		}
		dec := func(x, y []uint8) Result { return c.dec(x, y, ss) }
		whole := NewStreamDecoder(dec, closeN, maxN)
		whole.SetLead(leadN)
		want := streamTranscript(append(whole.Push(a, b), whole.Flush()...))
		if len(sizes) == 0 {
			sizes = []byte{0}
		}
		d := NewStreamDecoder(dec, closeN, maxN)
		d.SetLead(leadN)
		var got []StreamFrame
		for at, k := 0, 0; at < len(a); k++ {
			s := sizes[k%len(sizes)]
			n := min(len(a)-at, 64+int(s&0x1f)<<(s>>5*2)) // 64 .. 64 + 31 << 14
			var bl []uint8
			if b != nil {
				bl = b[at : at+n]
			}
			got = append(got, d.Push(a[at:at+n], bl)...)
			at += n
		}
		have := streamTranscript(append(got, d.Flush()...))
		if strings.Join(have, "\n") != strings.Join(want, "\n") {
			t.Fatalf("%s: chunked\n%s\nin one push\n%s", c.name, strings.Join(have, "\n"), strings.Join(want, "\n"))
		}
	})
}
