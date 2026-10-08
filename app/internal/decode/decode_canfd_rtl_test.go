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

type canRTLEvent struct {
	kind   int
	value  uint32
	count  uint32
	sample uint64
	hit    int
}

// canRTLTicks is the receiver clock: one line sample per 8 ns.
const canRTLTicks = 8e-9

// canRenderRates lays out wire bits at a per-bit width in samples. Bits whose
// index is >= switchAt use dataSpb; widths are fractional and rounded on the
// running position so long frames keep their exact average rate.
// TRLC-LINKS: REQ-SDS-013
func canRenderRates(wire []int, spb, dataSpb float64, switchAt, lead, trail int) []uint8 {
	var codes []uint8
	pos := 0.0
	level := func(bit int) uint8 {
		if bit == 1 {
			return 210
		}
		return 40
	}
	push := func(bit int, width float64) {
		end := int(math.Round(pos + width))
		for len(codes) < end {
			codes = append(codes, level(bit))
		}
		pos += width
	}
	push(1, float64(lead)*spb)
	for i, b := range wire {
		if i >= switchAt {
			push(b, dataSpb)
		} else {
			push(b, spb)
		}
	}
	push(1, float64(trail)*spb)
	return codes
}

// canFDBRSFrame builds an FD base frame with a recessive BRS and reports the
// wire index of the first bit after BRS, where the data rate starts.
// TRLC-LINKS: REQ-SDS-013
func canFDBRSFrame(id, dlc int, data []int) (wire []int, switchAt int) {
	bits := []int{0}
	bits = append(bits, canBitsMSB(id, 11)...)
	bits = append(bits, 0, 0, 1, 0, 1, 0) // RRS IDE FDF res BRS ESI
	bits = append(bits, canBitsMSB(dlc, 4)...)
	for _, d := range data {
		bits = append(bits, canBitsMSB(d, 8)...)
	}
	stuffed, _, _ := canStuffCore(bits)
	// Find the wire position of the destuffed BRS (destuffed index 16).
	destuffed, runVal, runLen := 0, -1, 0
	for i, b := range stuffed {
		if runLen == 5 {
			runVal, runLen = b, 1
			continue
		}
		if b == runVal {
			runLen++
		} else {
			runVal, runLen = b, 1
		}
		if destuffed == 16 {
			switchAt = i + 1
		}
		destuffed++
	}
	wire = append(wire, stuffed...)
	for i := 0; i < 24; i++ {
		wire = append(wire, 1)
	}
	return wire, switchAt
}

// canRemoteFrame builds a classic standard remote frame.
// TRLC-LINKS: REQ-SDS-013
func canRemoteFrame(id, dlc int) []int {
	in := []int{0}
	in = append(in, canBitsMSB(id, 11)...)
	in = append(in, 1, 0, 0) // RTR IDE r0
	in = append(in, canBitsMSB(dlc, 4)...)
	crc := canCRC15(in)
	stuffed, rv, rl := canStuffCore(append(append([]int{}, in...), canBitsMSB(crc, 15)...))
	if rl == 5 {
		stuffed = append(stuffed, 1-rv)
	}
	wire := append(stuffed, 1, 0, 1)
	for i := 0; i < 10; i++ {
		wire = append(wire, 1)
	}
	return wire
}

type canRefFrame struct {
	identity uint32
	dlc      int
	data     []int
	bad      bool
	sof      int
}

// canReference splits the Go decoder's spans into frames.
// TRLC-LINKS: REQ-SDS-013
func canReference(r Result) []canRefFrame {
	var frames []canRefFrame
	for _, s := range r.Spans {
		if s.Kind == "sof" {
			frames = append(frames, canRefFrame{sof: s.I0})
			continue
		}
		f := &frames[len(frames)-1]
		switch s.Kind {
		case "id":
			f.identity |= uint32(s.Val)
		case "ide":
			f.identity |= uint32(s.Val) << 29
		case "fd":
			f.identity |= 1 << 30
		case "rtr":
			f.identity |= uint32(s.Val) << 31
		case "dlc":
			f.dlc = s.Val
		case "data":
			f.data = append(f.data, s.Val)
		case "frame-error":
			f.bad = true
		}
	}
	return frames
}

// TRLC-LINKS: REQ-SDS-013
func canContains(data, want []int) bool {
	if len(want) == 0 {
		return len(data) > 0
	}
	for i := 0; i+len(want) <= len(data); i++ {
		ok := true
		for k := range want {
			if data[i+k] != want[k] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// Compare the CAN / CAN FD trigger RTL with DecodeCANFD on the same line.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func TestCANTriggerRTL(t *testing.T) {
	for _, tool := range []string{"iverilog", "vvp"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("required RTL tool %s: %v", tool, err)
		}
	}
	dir := t.TempDir()
	rtl := testenv.RTLDir(t)
	image := filepath.Join(dir, "can.vvp")
	if out, err := exec.Command("iverilog", "-g2012", "-s", "tb_can_trigger", "-o", image,
		filepath.Join(rtl, "can_trigger.v"), filepath.Join(rtl, "sim", "tb_can_trigger.v")).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	run := func(t *testing.T, codes []uint8, nominal, data int, pattern []int, args ...string) []canRTLEvent {
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
		q8 := func(baud int) int { return int(math.Round(256 / (float64(baud) * canRTLTicks))) }
		argv := append([]string{image, "+input=" + path, fmt.Sprintf("+length=%d", len(codes)),
			fmt.Sprintf("+nominal=%d", q8(nominal)), fmt.Sprintf("+data=%d", q8(data)),
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
	check := func(t *testing.T, codes []uint8, nominal, data int, pattern []int, args ...string) []canRTLEvent {
		t.Helper()
		ref := DecodeCANFD(codes, canRTLTicks, CANFDCfg{NominalBaud: nominal, DataBaud: data, DominantLow: true, Threshold: 128, HaveThr: true})
		if !ref.OK {
			t.Fatalf("reference: %s", ref.Error)
		}
		frames := canReference(ref)
		events := run(t, codes, nominal, data, pattern, args...)
		i := 0
		for fi, f := range frames {
			if i+len(f.data)+2 > len(events) {
				t.Fatalf("frame %d: %d events left, want %d (%+v)", fi, len(events)-i, len(f.data)+2, events)
			}
			start := events[i]
			if start.kind != 1 || math.Abs(float64(start.sample)-float64(f.sof)) > 2 {
				t.Fatalf("frame %d START %+v, reference SOF at %d", fi, start, f.sof)
			}
			for k, d := range f.data {
				e := events[i+1+k]
				if e.kind != 2 || e.value != uint32(d) || e.count != uint32(k) {
					t.Fatalf("frame %d byte %d: %+v want %02x", fi, k, e, d)
				}
			}
			end := events[i+1+len(f.data)]
			wantKind, wantHit := 3, 0
			if f.bad {
				wantKind = 4
			} else if canContains(f.data, pattern) {
				wantHit = 1
			}
			if end.kind != wantKind || end.value != f.identity || int(end.count>>8) != f.dlc || end.hit != wantHit {
				t.Fatalf("frame %d end %+v want kind %d identity %08x dlc %d hit %d", fi, end, wantKind, f.identity, f.dlc, wantHit)
			}
			if last := events[i+len(f.data)]; end.sample <= last.sample {
				t.Fatalf("frame %d END precedes its data: %+v", fi, end)
			}
			i += len(f.data) + 2
		}
		if i != len(events) {
			t.Fatalf("extra RTL events %+v", events[i:])
		}
		return events
	}
	msg := []int{0x48, 0x69, 0x20, 0x55, 0xAA, 0x0F, 0xF0, 0x0A}
	classic := func(spb float64) []uint8 {
		var wire []int
		_, w := canStdFrame(0x123, 4, msg[3:7])
		wire = append(wire, w...)
		_, w = canExtFrame(0x1ABCDEF0&0x1fffffff, 8, msg)
		wire = append(wire, w...)
		wire = append(wire, canRemoteFrame(0x7ff, 2)...)
		_, w = canStdFrame(0x000, 0, nil)
		wire = append(wire, w...)
		_, w = canStdFrame(0x555, 8, []int{0, 0, 0, 0, 0xff, 0xff, 0xff, 0xff})
		wire = append(wire, w...)
		return canRenderRates(wire, spb, spb, len(wire), 12, 12)
	}
	for _, baud := range []int{125000, 500000, 1000000, 3000000} {
		spb := 1 / (float64(baud) * canRTLTicks)
		codes := classic(spb)
		for _, pat := range [][]int{nil, {0xAA}, {0x55, 0xAA, 0x0F, 0xF0}, msg, {0x0F, 0x55}, {0xff, 0xff, 0xff, 0xff, 0xff}} {
			t.Run(fmt.Sprintf("classic/%d/%x", baud, pat), func(t *testing.T) { check(t, codes, baud, baud, pat) })
		}
	}
	t.Run("stall", func(t *testing.T) {
		check(t, classic(125), 1000000, 1000000, []int{0xAA, 0x0F}, "+stall=1")
	})
	t.Run("inverted", func(t *testing.T) {
		codes := classic(125)
		for i := range codes {
			codes[i] = 250 - codes[i]
		}
		ref := DecodeCANFD(codes, canRTLTicks, CANFDCfg{NominalBaud: 1000000, DominantLow: false, Threshold: 128, HaveThr: true})
		events := run(t, codes, 1000000, 1000000, []int{0xAA}, "+inverted=1")
		want := 0
		for _, f := range canReference(ref) {
			want += len(f.data) + 2
		}
		if !ref.OK || len(events) != want || events[2].value != 0xAA || events[5].hit != 1 {
			t.Fatalf("inverted: reference ok=%v, %d events want %d: %+v", ref.OK, len(events), want, events)
		}
	})
	t.Run("bad-crc", func(t *testing.T) {
		in, _ := canStdFrame(0x123, 4, msg[3:7])
		crc := canCRC15(in) ^ 0x10
		stuffed, rv, rl := canStuffCore(append(append([]int{}, in...), canBitsMSB(crc, 15)...))
		if rl == 5 {
			stuffed = append(stuffed, 1-rv)
		}
		wire := append(stuffed, 1, 0, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1)
		_, good := canStdFrame(0x124, 4, msg[3:7])
		wire = append(wire, good...)
		events := check(t, canRenderRates(wire, 125, 125, len(wire), 12, 12), 1000000, 1000000, []int{0xAA})
		if events[5].kind != 4 || events[11].hit != 1 {
			t.Fatalf("bad CRC frame must not hit: %+v", events)
		}
	})
	t.Run("stuff-violation", func(t *testing.T) {
		_, wire := canStdFrame(0x000, 1, []int{0x00})
		// The identifier's first five dominant bits are followed by a
		// recessive stuff bit; make it dominant.
		if wire[5] != 1 {
			t.Fatalf("expected a stuff bit at wire 5: %v", wire[:8])
		}
		wire[5] = 0
		wire = append(append([]int{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}, wire...), 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1)
		events := run(t, canRenderRates(wire, 125, 125, len(wire), 12, 12), 1000000, 1000000, nil)
		if len(events) < 2 || events[0].kind != 1 || events[len(events)-1].kind != 4 {
			t.Fatalf("stuff violation must end in ERROR: %+v", events)
		}
		for _, e := range events {
			if e.hit != 0 {
				t.Fatalf("stuff violation hit: %+v", events)
			}
		}
	})
	t.Run("fd-nobrs", func(t *testing.T) {
		data := make([]int, 12)
		for i := range data {
			data[i] = (i*37 + 5) & 0xff
		}
		wire := canFDStdFrame(0x2A5, 9, data)
		check(t, canRenderRates(wire, 125, 125, len(wire), 12, 12), 1000000, 1000000, data[4:9])
	})
	for _, rates := range [][2]int{{500000, 2000000}, {1000000, 5000000}, {250000, 1000000}} {
		t.Run(fmt.Sprintf("fd-brs/%d/%d", rates[0], rates[1]), func(t *testing.T) {
			data := make([]int, 64)
			for i := range data {
				data[i] = (i*91 + 17) & 0xff
			}
			wire, at := canFDBRSFrame(0x3C1, 15, data)
			spb, dspb := 1/(float64(rates[0])*canRTLTicks), 1/(float64(rates[1])*canRTLTicks)
			wire2, at2 := canFDBRSFrame(0x3C2, 10, data[:16])
			codes := canRenderRates(wire, spb, dspb, at, 12, 12)
			codes = append(codes, canRenderRates(wire2, spb, dspb, at2, 0, 12)...)
			check(t, codes, rates[0], rates[1], data[60:64])
		})
	}
	t.Run("glitch", func(t *testing.T) {
		codes := classic(125)
		glitch := make([]uint8, 3000)
		for i := range glitch {
			glitch[i] = 210
		}
		for i := 1500; i < 1540; i++ {
			glitch[i] = 40
		}
		events := run(t, append(glitch, codes...), 1000000, 1000000, nil)
		ref := canReference(DecodeCANFD(codes, canRTLTicks, CANFDCfg{NominalBaud: 1000000, DominantLow: true, Threshold: 128, HaveThr: true}))
		want := 0
		for _, f := range ref {
			want += len(f.data) + 2
		}
		if len(events) != want {
			t.Fatalf("a short dominant glitch published events: %d want %d", len(events), want)
		}
	})
}
