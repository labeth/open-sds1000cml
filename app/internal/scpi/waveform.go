package scpi

import (
	"encoding/binary"
	"fmt"
	"math"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
)

// waveform implements Cn:WF? DAT2|DESC (spec 11 §4): the byte-exact LeCroy
// block. Reply shape: "Cn:WF ALL,#9<9-digit count><payload>\n".
func (h *Handler) waveform(ch int, arg string) []byte {
	sel := arg
	if sel != "DAT2" && sel != "DESC" && sel != "ALL" {
		return errTok(errHeader)
	}

	var payload []byte
	var ok bool
	h.sc.WithFrame(func(f *engine.Frame) {
		if f == nil || f.Valid == 0 {
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
	if !ok {
		return errTok(errOutOfRange)
	}
	if payload == nil {
		// FP beyond the record (the factory firmware CRASHES here — we
		// reject instead, spec 11 §3.5 trap).
		return errTok(errOutOfRange)
	}
	head := fmt.Sprintf("C%d:WF ALL,#9%09d", ch+1, len(payload))
	out := make([]byte, 0, len(head)+len(payload)+1)
	out = append(out, head...)
	out = append(out, payload...)
	out = append(out, '\n')
	return out
}

// dat2 extracts the (sparsed, windowed) 8-bit codes. Deep-frame scale
// (centred 128); roll-ring codes are half-scale and must be rescaled before
// export (spec 11 §4 code-scale trap).
func (h *Handler) dat2(ch int, f *engine.Frame) []byte {
	sig := f.C1
	if ch == 1 {
		sig = f.C2
	}
	sig = sig[:f.Valid]
	if h.wfFP >= len(sig) {
		return nil // reject at readout time; never index past the record
	}
	sp := h.wfSP
	if sp < 1 {
		sp = 1
	}
	max := (len(sig) - h.wfFP + sp - 1) / sp
	np := h.wfNP
	if np == 0 || np > max {
		np = max
	}
	out := make([]byte, np)
	for i := 0; i < np; i++ {
		v := sig[h.wfFP+i*sp]
		if f.RollCodes {
			// Roll FIFO codes are ~Vdiv/25 — HALF the deep 50-codes/div
			// scale — so a roll deviation d represents 2·d deep codes. Double
			// the deviation (clamped) so it reads correctly under the DESC's
			// Vdiv/50 gain (spec 11 §4).
			nv := 128 + (int(v)-128)*2
			if nv < 0 {
				nv = 0
			}
			if nv > 255 {
				nv = 255
			}
			out[i] = uint8(nv)
		} else {
			out[i] = v
		}
	}
	return out
}

// wavedesc builds the 346-byte WAVEDESC (little-endian, COMM_ORDER=1).
func (h *Handler) wavedesc(ch int, f *engine.Frame) []byte {
	d := make([]byte, 346)
	copy(d[0:], "WAVEDESC")
	copy(d[16:], "DSO")
	le16 := func(off int, v int16) { binary.LittleEndian.PutUint16(d[off:], uint16(v)) }
	le32 := func(off int, v int32) { binary.LittleEndian.PutUint32(d[off:], uint32(v)) }
	f32 := func(off int, v float32) { binary.LittleEndian.PutUint32(d[off:], math.Float32bits(v)) }
	f64 := func(off int, v float64) { binary.LittleEndian.PutUint64(d[off:], math.Float64bits(v)) }

	le16(32, 0) // COMM_TYPE: 8-bit
	le16(34, 1) // COMM_ORDER: LOFIRST
	le32(36, 346)
	n := int32(f.Valid)
	le32(60, n) // WAVE_ARRAY_1 = count × 1 byte
	le32(116, n)
	le32(120, int32(f.WinCols))
	le32(124, 0)
	le32(128, n-1)
	le32(132, int32(h.wfFP))
	le32(136, int32(h.wfSP))
	le32(140, int32(h.wfSN))
	le32(144, 1)

	vdiv := 1.0
	if h.fe != nil {
		idx, _ := h.fe.Snapshot()
		vdiv = analog.Detents[idx[ch&1]].VdivV
	}
	gain := float32(vdiv / 50 * h.attn[ch&1]) // WF? scale: 50 codes/div
	f32(156, gain)

	off := 0.0
	st := h.sc.Snapshot()
	code := st.OffC1
	if ch == 1 {
		code = st.OffC2
	}
	if code != 0 && h.fe != nil {
		off = h.fe.OffsetVolts(ch, code)
	}
	f32(160, float32(off))
	f32(164, 127)
	f32(168, -128)
	f32(176, float32(f.SampleS))                  // HORIZ_INTERVAL = 1/SARA
	f64(180, -(float64(f.Valid) * f.SampleS / 2)) // trigger at centre
	f64(188, -(float64(f.Valid) * f.SampleS / 2)) // first-pixel offset
	copy(d[196:], "V")                            // VERTUNIT
	copy(d[244:], "s")                            // HORUNIT
	le16(344, int16(ch))                          // WAVE_SOURCE (0-based)
	return d
}
