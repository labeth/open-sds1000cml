package emit

import (
	"math/rand"
	"testing"

	"open-sds/codegen/ifacedef"
)

// recGet/recSet mirror the LSB-first bit-slice helpers the bindings template
// emits into iface.go. Keeping a reference copy here lets the round-trip test
// exercise the exact packing the generated codecs use, over the field offsets the
// view computes — so a layout bug (overlap, gap, wrong offset, undersized word
// count) is caught in the codegen module without importing the app package.

func refGet(w []uint16, off, width uint) uint64 {
	var v uint64
	for i := uint(0); i < width; i++ {
		bit := off + i
		if int(bit/16) < len(w) && w[bit/16]&(1<<(bit%16)) != 0 {
			v |= 1 << i
		}
	}
	return v
}

func refSet(w []uint16, off, width uint, v uint64) {
	for i := uint(0); i < width; i++ {
		if v&(1<<i) != 0 {
			bit := off + i
			if int(bit/16) < len(w) {
				w[bit/16] |= 1 << (bit % 16)
			}
		}
	}
}

type recLayout struct {
	name   string
	words  uint
	bits   uint
	Fields []recFieldView
}

func layouts() []recLayout {
	v := newView(ifacedef.Standard())
	var out []recLayout
	for _, c := range v.Channels {
		out = append(out, recLayout{c.Name, c.Words, c.RecordBits, c.Fields})
	}
	out = append(out, recLayout{v.Descriptor.Name, v.Descriptor.Words, v.Descriptor.Bits, v.Descriptor.Fields})
	return out
}

// Fields must be contiguous, non-overlapping, LSB-first, and fit in Words words.
func TestRecordLayoutContiguous(t *testing.T) {
	for _, l := range layouts() {
		var off uint
		for _, f := range l.Fields {
			if f.Offset != off {
				t.Errorf("%s.%s: offset %d, want %d (fields must be contiguous LSB-first)", l.name, f.Name, f.Offset, off)
			}
			off += f.Width
		}
		if off != l.bits {
			t.Errorf("%s: fields span %d bits, record is %d", l.name, off, l.bits)
		}
		if want := (l.bits + 15) / 16; l.words != want {
			t.Errorf("%s: Words=%d, want %d for %d bits", l.name, l.words, want, l.bits)
		}
	}
}

// A record packed from random field values and unpacked back must be identical:
// this exercises the offsets the generator emits with the generated packing.
func TestCodecRoundTripFields(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for _, l := range layouts() {
		for iter := 0; iter < 200; iter++ {
			vals := make([]uint64, len(l.Fields))
			w := make([]uint16, l.words)
			for i, f := range l.Fields {
				vals[i] = rng.Uint64() & ((uint64(1) << f.Width) - 1)
				refSet(w, f.Offset, f.Width, vals[i])
			}
			for i, f := range l.Fields {
				if got := refGet(w, f.Offset, f.Width); got != vals[i] {
					t.Fatalf("%s.%s: round-trip %d != %d", l.name, f.Name, got, vals[i])
				}
			}
		}
	}
}

// The inverse: any random word buffer, decoded field-by-field and re-encoded,
// reproduces exactly the low record-bits (the union of the field masks covers
// [0,bits) with no gaps).
func TestCodecRoundTripWords(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for _, l := range layouts() {
		for iter := 0; iter < 200; iter++ {
			w := make([]uint16, l.words)
			for i := range w {
				w[i] = uint16(rng.Uint32())
			}
			out := make([]uint16, l.words)
			for _, f := range l.Fields {
				refSet(out, f.Offset, f.Width, refGet(w, f.Offset, f.Width))
			}
			for i := range w {
				want := w[i]
				// bits >= record width are not part of any field: mask them off.
				for bit := uint(0); bit < 16; bit++ {
					if uint(i)*16+bit >= l.bits {
						want &^= 1 << bit
					}
				}
				if out[i] != want {
					t.Fatalf("%s: word %d round-trip %#04x != %#04x", l.name, i, out[i], want)
				}
			}
		}
	}
}
