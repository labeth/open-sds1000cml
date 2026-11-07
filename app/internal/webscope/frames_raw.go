// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"encoding/binary"
	"math"

	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-dso-core/webserver/wire"
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
	s.sc.WithFrame(func(f *sample.Frame) {
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

				f.Len(), len(f.Codes8(0)),
			), len(f.Codes8(1)))
		if n <= 0 {
			rf = wire.RawFrame{Header: wire.FrameReply{Seq: f.Seq, Unchanged: true, EdgeX: -1}, Unchanged: true, Empty: true}
			return
		}
		hdr := wire.FrameReply{
			Seq: f.Seq, EdgeX: f.TriggerAt, Ptp: f.SourcePtp, TdivS: f.Snapshot.TdivS, WinCols: min(f.WinPoints, n),
			DisplayedS: f.Snapshot.DisplayedS, Interp: f.Flags.Has(sample.Interp), Norm: f.Snapshot.Norm,
			Trigd: f.Status.Triggered, Coherent: f.Status.Coherent, IsEnv: f.Flags.Has(sample.Envelope), PeakDetect: f.Content == sample.PeakPairs, Ordered: f.Flags.Has(sample.Ordered), CaptureSampleS: captureSampleS(f),
			Degraded: f.Status.Degraded, // half-capture flag travels with the raw record it describes
			Cols:     n, ColSpanS: float64(n) * f.SampleS, SampleS: f.SampleS,
			EdgeFrac: -1, WinFrac: 1,
			Vpc1: vpc[0], Vpc2: vpc[1], Off1V: off[0], Off2V: off[1],
		}
		hdr.StreamSeq, hdr.WindowNs, hdr.GapNs = f.Stream.Seq, int64(math.Round(f.Stream.WindowS*1e9)), int64(math.Round(f.Stream.GapS*1e9))
		hdr.BandwidthHz, hdr.FilterGuard, hdr.Filter = f.Cond.BandwidthHz, f.Cond.Guard, f.Cond.Filter
		hdr.NoiseGainIdeal, hdr.PassbandHz = f.Cond.NoiseGain, f.Cond.PassbandHz
		hdr.Decimation, hdr.CaptureDepth, hdr.TriggerKind = uint32(f.AcqDecim), f.Depth, f.TriggerName()
		hdr.FullRate = f.Content == sample.RawFullRate
		if len(f.Precision(0)) == n && len(f.Precision(1)) == n {
			hdr.FractionBits = 8
			payload := make([]byte, 4*n)
			for ch, q := range [][]uint16{f.Precision(0), f.Precision(1)} {
				for i, v := range q {
					binary.LittleEndian.PutUint16(payload[2*(ch*n+i):], v)
				}
			}
			rf = wire.RawFrame{Header: hdr, Precision: true, Payload: payload}
			return
		}
		payload := make([]byte, 2*n)
		copy(payload[:n], f.Codes8(0)[:n])
		copy(payload[n:], f.Codes8(1)[:n])
		rf = wire.RawFrame{Header: hdr, Payload: payload}
	})
	return rf
}
