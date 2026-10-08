// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"fmt"
	"math"
	"open-sds/app/internal/testenv"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// flexFrameBytes builds a complete frame: header with CRC-11, payload and
// the CRC-24 trailer.
// TRLC-LINKS: REQ-SDS-013
func flexFrameBytes(id, cycle int, sync, startup bool, payload []int) []int {
	n := len(payload) / 2
	hdr := uint64(0)
	if sync {
		hdr |= 1 << 36
	}
	if startup {
		hdr |= 1 << 35
	}
	hdr |= uint64(id&0x7ff) << 24
	hdr |= uint64(n&0x7f) << 17
	hdr |= uint64(flexHeaderCRC11(int(hdr>>36&1), int(hdr>>35&1), id, n)) << 6
	hdr |= uint64(cycle & 0x3f)
	var b []int
	for i := 4; i >= 0; i-- {
		b = append(b, int(hdr>>(8*i)&0xff))
	}
	b = append(b, payload...)
	crc := flexFrameCRC24(b)
	return append(b, crc>>16&0xff, crc>>8&0xff, crc&0xff)
}

// flexRenderRate lays frames out at a fractional bit width in samples.
// TRLC-LINKS: REQ-SDS-013
func flexRenderRate(frames [][]int, spb float64, tssBits int) []uint8 {
	var w []uint8
	pos := 0.0
	push := func(bit int, bits float64) {
		v := uint8(40)
		if bit == 1 {
			v = 210
		}
		pos += bits * spb
		for end := int(math.Round(pos)); len(w) < end; {
			w = append(w, v)
		}
	}
	push(1, 8)
	for _, bytes := range frames {
		push(0, float64(tssBits))
		push(1, 1)
		for _, b := range bytes {
			push(1, 1)
			push(0, 1)
			for d := 7; d >= 0; d-- {
				push((b>>d)&1, 1)
			}
		}
		push(0, 1)
		push(1, 1)
		push(1, 8)
	}
	return w
}

type flexRefFrame struct {
	data []int
	bad  bool
	tss  int
}

// Compare the FlexRay trigger RTL with DecodeFlexRay on the same line.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func TestFlexRayTriggerRTL(t *testing.T) {
	for _, tool := range []string{"iverilog", "vvp"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("required RTL tool %s: %v", tool, err)
		}
	}
	dir := t.TempDir()
	rtl := testenv.RTLDir(t)
	image := filepath.Join(dir, "flexray.vvp")
	if out, err := exec.Command("iverilog", "-g2012", "-s", "tb_flexray_trigger", "-o", image,
		filepath.Join(rtl, "flexray_trigger.v"), filepath.Join(rtl, "sim", "tb_flexray_trigger.v")).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	run := func(t *testing.T, codes []uint8, bitrate int, pattern []int, args ...string) []canRTLEvent {
		t.Helper()
		var in strings.Builder
		for _, c := range codes {
			if c >= 128 {
				in.WriteString("1\n")
			} else {
				in.WriteString("0\n")
			}
		}
		path := filepath.Join(dir, "line.mem")
		if err := os.WriteFile(path, []byte(in.String()), 0600); err != nil {
			t.Fatal(err)
		}
		var p uint64
		for _, b := range pattern {
			p = p<<8 | uint64(b)
		}
		argv := append([]string{image, "+input=" + path, fmt.Sprintf("+length=%d", len(codes)),
			fmt.Sprintf("+nominal=%d", int(math.Round(256/(float64(bitrate)*canRTLTicks)))),
			fmt.Sprintf("+pattern=%016x", p), fmt.Sprintf("+len=%d", len(pattern))}, args...)
		out, err := exec.Command("vvp", argv...).CombinedOutput()
		if err != nil {
			t.Fatalf("simulate: %v\n%s", err, out)
		}
		var events []canRTLEvent
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if !strings.HasPrefix(line, "E ") {
				continue
			}
			var e canRTLEvent
			if _, err := fmt.Sscanf(line, "E %d %x %x %d %d", &e.kind, &e.value, &e.count, &e.sample, &e.hit); err != nil {
				t.Fatalf("unexpected output %q: %v", line, err)
			}
			events = append(events, e)
		}
		return events
	}
	check := func(t *testing.T, codes []uint8, bitrate int, pattern []int, args ...string) []canRTLEvent {
		t.Helper()
		ref := DecodeFlexRay(codes, canRTLTicks, FlexRayCfg{Bitrate: bitrate, Threshold: 128, HaveThr: true})
		var frames []flexRefFrame
		for _, s := range ref.Spans {
			switch s.Kind {
			case "start":
				frames = append(frames, flexRefFrame{tss: s.I1})
			case "frame-error":
				frames[len(frames)-1].bad = true
			case "data":
				frames[len(frames)-1].data = append(frames[len(frames)-1].data, s.Val)
			}
		}
		events := run(t, codes, bitrate, pattern, args...)
		i := 0
		for fi, f := range frames {
			if i+len(f.data)+2 > len(events) {
				t.Fatalf("frame %d: %d events left, want %d (%+v)", fi, len(events)-i, len(f.data)+2, events[i:])
			}
			if s := events[i]; s.kind != 1 || math.Abs(float64(s.sample)-float64(f.tss)) > 2 {
				t.Fatalf("frame %d START %+v, reference FSS at %d", fi, s, f.tss)
			}
			for k, d := range f.data {
				if e := events[i+1+k]; e.kind != 2 || e.value != uint32(d) || e.count != uint32(k) {
					t.Fatalf("frame %d byte %d: %+v want %02x", fi, k, e, d)
				}
			}
			end := events[i+1+len(f.data)]
			wantKind, wantHit := 3, 0
			if f.bad || len(f.data) < 5 {
				wantKind = 4
			} else if canContains(f.data, pattern) {
				wantHit = 1
			}
			if end.kind != wantKind || end.hit != wantHit || end.count != uint32(len(f.data)) {
				t.Fatalf("frame %d end %+v want kind %d hit %d", fi, end, wantKind, wantHit)
			}
			if wantKind == 3 && end.value&0x7ff != uint32(f.data[0]&7)<<8|uint32(f.data[1]) {
				t.Fatalf("frame %d END identity %08x", fi, end.value)
			}
			i += len(f.data) + 2
		}
		if i != len(events) {
			t.Fatalf("extra RTL events %+v", events[i:])
		}
		return events
	}
	payload := func(n, seed int) []int {
		p := make([]int, 2*n)
		for i := range p {
			p[i] = (i*seed + 11) & 0xff
		}
		return p
	}
	good := [][]int{
		flexFrameBytes(1, 0, true, true, []int{0x48, 0x69, 0x20, 0x55, 0xAA, 0x0F, 0xF0, 0x0A}),
		flexFrameBytes(0x2A5, 17, false, false, payload(20, 37)),
		flexFrameBytes(0x7fe, 63, false, false, nil),
		flexFrameBytes(33, 5, false, false, []int{0, 0, 0, 0, 0, 0}),
	}
	for _, rate := range []int{10000000, 5000000, 2500000} {
		spb := 1 / (float64(rate) * canRTLTicks)
		codes := flexRenderRate(good, spb, 6)
		for _, pat := range [][]int{nil, {0x55, 0xAA, 0x0F, 0xF0}, {0, 0, 0, 0, 0, 0}, {0x12, 0x34}} {
			t.Run(fmt.Sprintf("good/%d/%x", rate, pat), func(t *testing.T) {
				events := check(t, codes, rate, pat)
				if len(events) == 0 {
					t.Fatal("no events")
				}
			})
		}
	}
	t.Run("drift", func(t *testing.T) {
		// A one percent slower transmitter still decodes via BSS resync.
		check(t, flexRenderRate(good, 12.5*1.01, 6), 10000000, []int{0xAA})
	})
	t.Run("stall", func(t *testing.T) {
		check(t, flexRenderRate(good, 25, 6), 5000000, []int{0xF0, 0x0A}, "+stall=1")
	})
	t.Run("inverted", func(t *testing.T) {
		codes := flexRenderRate(good, 25, 6)
		for i := range codes {
			codes[i] = 250 - codes[i]
		}
		events := run(t, codes, 5000000, []int{0xAA}, "+inverted=1")
		ref := run(t, flexRenderRate(good, 25, 6), 5000000, []int{0xAA})
		if len(events) != len(ref) {
			t.Fatalf("inverted: %d events, upright %d", len(events), len(ref))
		}
		for i := range ref {
			if events[i] != ref[i] {
				t.Fatalf("inverted event %d: %+v want %+v", i, events[i], ref[i])
			}
		}
	})
	t.Run("corrupt", func(t *testing.T) {
		badHeader := append([]int{}, good[0]...)
		badHeader[3] ^= 0x40 // header CRC bit
		badTrailer := append([]int{}, good[1]...)
		badTrailer[len(badTrailer)-1] ^= 1
		truncated := append([]int{}, good[1][:12]...) // complete header, short frame: valid like the reference
		short := append([]int{}, good[0][:3]...)      // no complete header: ERROR in hardware
		frames := [][]int{badHeader, badTrailer, truncated, short, good[0]}
		check(t, flexRenderRate(frames, 12.5, 6), 10000000, []int{0x48, 0x69})
	})
	t.Run("short-low", func(t *testing.T) {
		// A three-bit LOW run is not a TSS; later in-frame LOW runs of four
		// bits are, for the reference too, and fail validation.
		for _, e := range check(t, flexRenderRate(good[:1], 12.5, 3), 10000000, nil) {
			if e.hit != 0 || e.kind == 3 {
				t.Fatalf("short TSS frame accepted: %+v", e)
			}
		}
	})
}
