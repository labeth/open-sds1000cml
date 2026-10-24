// ENGMODEL-OWNER-UNIT: FU-APP-DIAG
package diag

import (
	"fmt"
	"testing"

	"open-sds/app/internal/iface"
)

// TRLC-LINKS: REQ-SDS-144
func BenchmarkLaneName(b *testing.B) {
	for b.Loop() {
		for i := 0; i < 0x7e; i++ {
			_ = LaneName(i)
		}
	}
}

// TRLC-LINKS: REQ-SDS-149
func BenchmarkBusProbeDecode(b *testing.B) {
	words := make([]uint16, 2*4096)
	for i := range words {
		words[i] = uint16(i*2654435761) ^ uint16(i>>3)
	}
	for b.Loop() {
		var r BusProbeResult
		r.decode(words, BusProbeOptions{OeMask: 0x5555})
	}
}

// TRLC-LINKS: REQ-SDS-150
func BenchmarkQuietListen(b *testing.B) {
	d, _ := newTestDiag()
	for b.Loop() {
		if _, err := d.QuietListen(QuietOptions{PairEn: 0x1f, EncOn: true, Wide: true}); err != nil {
			b.Fatal(err)
		}
	}
}

// TRLC-LINKS: REQ-SDS-150
func BenchmarkSramCrank(b *testing.B) {
	d, _ := newTestDiag()
	for b.Loop() {
		if _, err := d.SramCrank(CrankOptions{Ball: "none"}); err != nil {
			b.Fatal(err)
		}
	}
}

// TRLC-LINKS: REQ-SDS-147
func BenchmarkLiveMask(b *testing.B) {
	regs := iface.Registers()
	for b.Loop() {
		for _, r := range regs {
			_ = liveMask(r)
		}
	}
}

// TRLC-LINKS: REQ-SDS-143
func BenchmarkResolveDiagIdx(b *testing.B) {
	for b.Loop() {
		if _, _, err := ResolveDiagIdx("0x40"); err != nil {
			b.Fatal(err)
		}
	}
}

// TRLC-LINKS: REQ-SDS-147
func BenchmarkSchemaCheck(b *testing.B) {
	d, _ := newTestDiag()
	for b.Loop() {
		if _, err := d.SchemaCheck(SchemaOptions{Writes: 1}); err != nil {
			b.Fatal(err)
		}
	}
}

// decodeRef is the per-ball decode as first written.
// TRLC-LINKS: REQ-SDS-149
func decodeRef(r *BusProbeResult, words []uint16, o BusProbeOptions) {
	n := len(words) / 2
	if o.Samples > 0 && o.Samples < n {
		n = o.Samples
	}
	r.Samples = n
	busv := make([]uint32, n)
	auxv := make([]uint16, n)
	for i := 0; i < n; i++ {
		lo, hi := uint32(words[2*i]), uint32(words[2*i+1])
		busv[i] = lo | (hi&0x07ff)<<16
		auxv[i] = uint16(hi>>11) & 0x1f
	}
	anyPhase := o.OePhase != 0 || o.OeInv
	mask := o.OeMask & 0x07ffffff
	if o.OeMask == 0 {
		mask = 0x07ffffff
	}
	track := func(name string, bit func(int) uint8, isDriven bool) BallActivity {
		a := BallActivity{Ball: name, Driven: isDriven}
		if n == 0 {
			return a
		}
		a.First = bit(0)
		prev := a.First
		for i := 0; i < n; i++ {
			v := bit(i)
			if v == 1 {
				a.High++
			}
			if i > 0 && v != prev {
				a.Edges++
			}
			prev = v
		}
		a.Last = prev
		a.Changed = a.Edges > 0
		return a
	}
	for k, ball := range BusBalls {
		drivenK := anyPhase && mask&(1<<uint(k)) != 0
		a := track(ball, func(i int) uint8 { return uint8((busv[i] >> uint(k)) & 1) }, drivenK)
		r.Balls = append(r.Balls, a)
		if a.Changed && !drivenK {
			r.Moving = append(r.Moving, ball)
		}
	}
	for k := 0; k < 4; k++ {
		r.Singles = append(r.Singles, track(SingleBalls[k], func(i int) uint8 { return uint8((auxv[i] >> uint(1+k)) & 1) }, false))
	}
	r.P6 = track("P6", func(i int) uint8 { return uint8(auxv[i] & 1) }, false)
}

// decode reports every ball as the per-ball decode did.
// TRLC-LINKS: REQ-SDS-149
func TestBusProbeDecodeIsTheReference(t *testing.T) {
	for _, n := range []int{0, 1, 2, 7, 500} {
		words := make([]uint16, 2*n)
		for i := range words {
			words[i] = uint16(i*40503) ^ uint16(i*i*7)
		}
		for _, o := range []BusProbeOptions{{}, {OeMask: 0x5555, OePhase: 1}, {Samples: 3, OeInv: true}} {
			var got, want BusProbeResult
			got.decode(words, o)
			decodeRef(&want, words, o)
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("n %d %+v:\n got %+v\nwant %+v", n, o, got, want)
			}
		}
	}
}

// hex04 is %#04x.
// TRLC-LINKS: REQ-SDS-150
func TestHex04IsTheFormat(t *testing.T) {
	for _, v := range []uint16{0, 5, 0x10, 0xab, 0x123, 0xffff} {
		if got, want := hex04(v), fmt.Sprintf("%#04x", v); got != want {
			t.Fatalf("%d: %s, want %s", v, got, want)
		}
	}
}
