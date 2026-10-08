// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import (
	"fmt"
	"math/rand"
	"open-sds/app/internal/testenv"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// envelopeReference reduces words (after skip words) into the buffer entries
// the RTL writes: raw buckets pair up as {bucket 2j+1, bucket 2j}; a Q8.8
// bucket is {maxima, minima}. bucket and skip count words, both even.
// TRLC-LINKS: REQ-SDS-010
func envelopeReference(words []uint32, skip, bucket int, q8 bool) [][2]uint32 {
	words = words[skip:]
	var per []uint32
	var out [][2]uint32
	for b := 0; b+bucket <= len(words); b += bucket {
		mn := [2]uint32{0xffff, 0xffff}
		var mx [2]uint32
		for _, w := range words[b : b+bucket] {
			var v [2][2]uint32
			if q8 {
				v = [2][2]uint32{{w & 0xffff, w & 0xffff}, {w >> 16, w >> 16}}
			} else {
				v = [2][2]uint32{{w & 0xff, w >> 16 & 0xff}, {w >> 8 & 0xff, w >> 24}}
			}
			for ch := 0; ch < 2; ch++ {
				for _, x := range v[ch] {
					mn[ch], mx[ch] = min(mn[ch], x), max(mx[ch], x)
				}
			}
		}
		if q8 {
			out = append(out, [2]uint32{mx[1]<<16 | mx[0], mn[1]<<16 | mn[0]})
		} else {
			per = append(per, mx[1]<<24|mx[0]<<16|mn[1]<<8|mn[0])
		}
	}
	for i := 0; i+1 < len(per); i += 2 {
		out = append(out, [2]uint32{per[i+1], per[i]})
	}
	return out
}

// The envelope reducer matches a software min/max per bucket, writes buffer
// entries at consecutive addresses, counts its words and ignores warm-up words.
// TRLC-LINKS: REQ-SDS-010
func TestEnvelopeReduceRTL(t *testing.T) {
	for _, tool := range []string{"iverilog", "vvp"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("required RTL tool %s: %v", tool, err)
		}
	}
	dir := t.TempDir()
	rtl := testenv.RTLDir(t)
	image := filepath.Join(dir, "env.vvp")
	if out, err := exec.Command("iverilog", "-g2012", "-s", "tb_envelope_reduce", "-o", image,
		filepath.Join(rtl, "envelope_reduce.v"), filepath.Join(rtl, "sim", "tb_envelope_reduce.v")).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	rng := rand.New(rand.NewSource(3))
	for _, c := range []struct {
		bucket, buckets, skip int
		q8, gaps              bool
	}{{2, 8, 0, false, false}, {4, 10, 16, false, true}, {512, 16, 16, false, false}, {2, 9, 16, true, false}, {38, 20, 16, true, true}, {1000, 4, 16, false, true}} {
		t.Run(fmt.Sprintf("K%d/B%d/q8%v", c.bucket, c.buckets, c.q8), func(t *testing.T) {
			n := c.skip + c.bucket*c.buckets
			words := make([]uint32, n)
			var in strings.Builder
			for i := range words {
				words[i] = rng.Uint32()
				gap := 0
				if c.gaps && rng.Intn(4) == 0 {
					gap = rng.Intn(3)
				}
				fmt.Fprintf(&in, "%08x\n%x\n", words[i], gap)
			}
			path := filepath.Join(dir, "words.mem")
			if err := os.WriteFile(path, []byte(in.String()), 0600); err != nil {
				t.Fatal(err)
			}
			q := 0
			if c.q8 {
				q = 1
			}
			out, err := exec.Command("vvp", image, "+input="+path, fmt.Sprintf("+n=%d", n), fmt.Sprintf("+bucket=%d", c.bucket/2),
				fmt.Sprintf("+skip=%d", c.skip/2), fmt.Sprintf("+q8=%d", q)).CombinedOutput()
			if err != nil {
				t.Fatalf("simulate: %v\n%s", err, out)
			}
			var got [][2]uint32
			count := -1
			for _, line := range strings.Split(string(out), "\n") {
				var addr int
				var hi, lo uint32
				if _, err := fmt.Sscanf(line, "E %d %x %x", &addr, &hi, &lo); err == nil {
					if addr != len(got) {
						t.Fatalf("entry %d written at %d", len(got), addr)
					}
					got = append(got, [2]uint32{hi, lo})
				}
				fmt.Sscanf(line, "N %d", &count)
			}
			want := envelopeReference(words, c.skip, c.bucket, c.q8)
			if fmt.Sprint(got) != fmt.Sprint(want) || count != 2*len(want) {
				t.Fatalf("envelope (%d words)\n got %x\nwant %x", count, got, want)
			}
		})
	}
}
