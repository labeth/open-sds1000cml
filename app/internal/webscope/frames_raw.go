// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"encoding/binary"

	"github.com/labeth/open-dso-core/webserver/wire"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
)

// rawFrame assembles the raw-shape frame (?raw=1): the un-windowed,
// un-interpolated, pre-coupling record — what the browser super-resolution
// stacker aligns and drizzles. Payload = c1[cols] c2[cols] raw ADC codes,
// copied under the fan-out lock (the backing arrays are reused by the next
// tick); header carries sample_s and the engine's sub-sample edge_x. No
// measurements — the stacker computes its own statistics, and skipping the
// meas pass keeps the raw feed cheap next to the display path.
// TRLC-LINKS: REQ-SDS-163
func (s *Scope) rawFrame(since uint64) wire.RawFrame {
	off, vpc := s.vertScales()
	var rf wire.RawFrame
	s.sc.WithFrame(func(f *engine.Frame) {
		if f == nil || f.Seq == 0 || f.Seq == since {
			var seq uint64
			if f != nil {
				seq = f.Seq
			}
			rf = wire.RawFrame{Header: wire.FrameReply{Seq: seq, Unchanged: true, EdgeX: -1}, Unchanged: true}
			return
		}
		n := min(
			// clamp to the actual sample slices: Valid > len is an engine-invariant
			// violation, but a serve-path slice panic on one bad frame must never
			// crash the UI (frame-serve fuzz). C2 may be shorter than C1.
			min(

				f.Valid, len(f.C1),
			), len(f.C2))
		if n <= 0 {
			rf = wire.RawFrame{Header: wire.FrameReply{Seq: f.Seq, Unchanged: true, EdgeX: -1}, Unchanged: true, Empty: true}
			return
		}
		hdr := wire.FrameReply{
			Seq: f.Seq, EdgeX: f.EdgeX, Ptp: f.Ptp, TdivS: f.TdivS, WinCols: min(f.WinCols, n),
			DisplayedS: f.DisplayedS, Interp: f.Interp, Norm: f.Norm,
			Trigd: f.Trigd, Coherent: f.Coherent, IsEnv: f.IsEnv, PeakDetect: f.PeakDetect, Ordered: f.Ordered, CaptureSampleS: captureSampleS(f),
			Degraded: f.Degraded, // half-capture flag travels with the raw record it describes
			Cols:     n, ColSpanS: float64(n) * f.SampleS, SampleS: f.SampleS,
			EdgeFrac: -1, WinFrac: 1,
			Vpc1: vpc[0], Vpc2: vpc[1], Off1V: off[0], Off2V: off[1],
		}
		hdr.StreamSeq, hdr.WindowNs, hdr.GapNs = f.StreamSeq, f.WindowNs, f.GapNs
		hdr.BandwidthHz, hdr.FilterGuard, hdr.Filter = f.BandwidthHz, f.FilterGuard, f.Filter
		hdr.NoiseGainIdeal, hdr.PassbandHz = f.NoiseGainIdeal, f.PassbandHz
		hdr.Decimation, hdr.CaptureDepth, hdr.TriggerKind = f.Decimation, f.CaptureDepth, f.TriggerKind
		hdr.FullRate = f.FullRate
		if len(f.Q1) == n && len(f.Q2) == n {
			hdr.FractionBits = 8
			payload := make([]byte, 4*n)
			for ch, q := range [][]uint16{f.Q1, f.Q2} {
				for i, v := range q {
					binary.LittleEndian.PutUint16(payload[2*(ch*n+i):], v)
				}
			}
			rf = wire.RawFrame{Header: hdr, Precision: true, Payload: payload}
			return
		}
		payload := make([]byte, 2*n)
		copy(payload[:n], f.C1[:n])
		copy(payload[n:], f.C2[:n])
		rf = wire.RawFrame{Header: hdr, Payload: payload}
	})
	return rf
}
