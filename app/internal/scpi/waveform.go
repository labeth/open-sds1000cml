// ENGMODEL-OWNER-UNIT: FU-APP-SCPI

package scpi

import (
	"fmt"

	"github.com/labeth/open-dso-core/sample"
	core "github.com/labeth/open-dso-core/scpi"
	"github.com/labeth/open-dso-core/scpi/wave"
	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
)

// waveform implements Cn:WF? DAT2|DESC (spec 11 §4): the byte-exact LeCroy
// block. Reply shape: "Cn:WF ALL,#9<9-digit count><payload>\n", whatever
// CHDR says.
// TRLC-LINKS: REQ-SDS-024
func (h *Handler) waveform(c *core.Call, ch int, sel string) error {
	if sel != "DAT2" && sel != "DESC" && sel != "ALL" {
		return c.Error(core.ErrHeader)
	}
	var payload []byte
	var ok bool
	h.sc.WithFrame(func(f *sample.Frame) {
		if f == nil || f.Len() == 0 {
			return
		}
		ok = true
		switch sel {
		case "DESC":
			payload = h.wavedesc(ch, f)
		default: // DAT2 (ALL treated as DAT2 for v1)
			payload = h.dat2(ch, f)
		}
	})
	if !ok || payload == nil {
		// FP beyond the record: the factory firmware CRASHES here — we
		// reject instead (spec 11 §3.5 trap).
		return c.Error(core.ErrDataOutOfRange)
	}
	c.Raw(append(wave.Block9(fmt.Sprintf("C%d:WF ALL,", ch+1), payload), '\n'))
	return nil
}

// window is WFSU's selection.
// TRLC-LINKS: REQ-SDS-024
func (h *Handler) window() wave.Window {
	return wave.Window{First: h.wfFP, Sparse: h.wfSP, Points: h.wfNP}
}

// dat2 extracts the (sparsed, windowed) 8-bit codes as signed bytes about
// the grid centre: COMM_TYPE 0 is a signed byte, and LeCroy/Siglent hosts
// read volts = code·gain − offset with codes above 127 taken as negative.
// Deep-frame scale (centred 128); roll-ring codes are half-scale and must
// be rescaled before export (spec 11 §4 code-scale trap).
// TRLC-LINKS: REQ-SDS-024
func (h *Handler) dat2(ch int, f *sample.Frame) []byte {
	sig := f.Codes8(0)
	if ch == 1 {
		sig = f.Codes8(1)
	}
	// A Valid>len invariant slip must not panic the VXI readout.
	sig = sig[:max(min(f.Len(), len(sig)), 0)]
	first, step, n, ok := h.window().Span(len(sig))
	if !ok {
		return nil // reject at readout time; never index past the record
	}
	if !acq.HalfScale(f) {
		return wave.Signed8(sig, first, step, n)
	}
	out := make([]byte, n)
	for i := range out {
		v := sig[first+i*step]
		// Legacy roll FIFO codes are half-scale, so a roll deviation d
		// represents 2·d deep codes. Double the deviation (clamped) so it
		// reads correctly under the DESC's Vdiv/25 gain (spec 11 §4). No
		// current engine path makes half-scale frames.
		out[i] = uint8(min(max(128+(int(v)-128)*2, 0), 255)) ^ 0x80
	}
	return out
}

// wavedesc builds the 346-byte WAVEDESC (little-endian, COMM_ORDER=1).
// TRLC-LINKS: REQ-SDS-024
func (h *Handler) wavedesc(ch int, f *sample.Frame) []byte {
	n := f.Len()
	vdiv := 1.0
	if h.fe != nil {
		vdiv = h.fe.RangeV(ch & 1) // codes are on the analog range (zoomed detents)
	}
	off := 0.0
	if h.fe != nil {
		off = h.lv.OffsetIn(ch, h.sc.Snapshot()) * h.probe(ch) // tip volts, like the gain
	}
	// HORIZ_OFFSET: time of sample 0 from the trigger, at its exact
	// (sub-sample) position; a record without a trigger keeps the centre.
	trig := float64(n) / 2
	if f.TriggerAt >= 0 && f.TriggerAt < float64(n) {
		trig = f.TriggerAt
	}
	d := wave.Desc{
		Template:        "DSO",
		CommType:        0, // 8-bit
		CommOrder:       1,
		WaveArray1:      descLong(n), // count × 1 byte
		WaveArrayCount:  descLong(n),
		PointsPerScreen: descLong(f.WinPoints),
		LastValid:       descLong(n - 1),
		FirstPoint:      descLong(h.wfFP),
		Sparsing:        descLong(h.wfSP),
		SegmentIndex:    descLong(h.wfSN),
		SweepsPerAcq:    1,
		VerticalGain:    float32(vdiv / 25 * h.probe(ch)), // WF? scale: 25 codes/div (spec 11 §4)
		VerticalOffset:  float32(off),
		MaxValue:        127,
		MinValue:        -128,
		HorizInterval:   float32(f.SampleS), // 1/SARA
		HorizOffset:     -trig * f.SampleS,
		PixelOffset:     -(float64(n) * f.SampleS / 2), // first-pixel offset
		VertUnit:        "V",
		HorUnit:         "s",
		WaveSource:      int16(ch), //nolint:gosec // G115: 0-based channel index (C1, C2)
	}
	return d.Encode()
}

// descLong is v as a WAVEDESC long field. The fields are record lengths and
// the WFSU setup values, which setWFSU bounds to 0..2^31-1.
// TRLC-LINKS: REQ-SDS-024
func descLong(v int) int32 {
	return int32(v) //nolint:gosec // G115: record lengths and WFSU values (bounded by setWFSU) fit 32 bits
}
