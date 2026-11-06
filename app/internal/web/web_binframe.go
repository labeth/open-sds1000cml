// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package web

import (
	"encoding/binary"
	"fmt"
	"net/http"
	"time"

	"github.com/labeth/open-dso-core/webserver"
	"github.com/labeth/open-dso-core/webserver/wire"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
)

// FrameClient reports whether a page asked for a frame within the last d:
// a page showing the scope keeps /api/frame.bin's long-poll open (it waits
// up to 1.5 s a poll), and one that closed, or hid, stops asking.
// TRLC-LINKS: REQ-SDS-163, REQ-SDS-168
func (s *Server) FrameClient(d time.Duration) bool {
	at := s.frameAt.Load()
	return at != 0 && time.Since(time.Unix(0, at)) < d
}

// hFrameBin is the long-poll binary frame endpoint: same reply as /api/frame
// (built by the shared buildReply), encoded as a small JSON header + raw
// uint8 payload — ~1 ms on the device versus 50-150 ms of reflective JSON
// over int16 arrays, which is what capped the browser at a few fps. With
// since= and waitms= the request parks (no locks held) until the fan-out
// snapshots a newer frame, so delivery latency is one response write and the
// client needs no poll timer: request-when-ready IS the backpressure, and a
// slow client simply skips to the newest frame.
// TRLC-LINKS: REQ-SDS-163
func (s *Server) hFrameBin(w http.ResponseWriter, r *http.Request) {
	if s.claims.Superseded(r) {
		w.WriteHeader(http.StatusConflict) // 409 — a newer browser claimed the device
		return
	}
	s.frameAt.Store(time.Now().UnixNano())
	// A consumer that needs true samples renews a lease before waiting (a
	// closed analyzer lets it expire): lease=raw for full-rate samples (eye,
	// super-res), lease=spectrum for the frames a spectrum is taken of (FFT,
	// spectrogram: the LCD's SPECTRUM asks for the same), lease=samples for
	// evenly sampled ones, lease=search for the whole records BUS's search
	// reads at slow timebases (no roll). Live frames are otherwise time-ordered envelopes,
	// fast whatever is enabled.
	switch r.URL.Query().Get("lease") {
	case "raw":
		if sc, ok := s.sc.(interface{ LeaseDecodeView(time.Duration) }); ok {
			if r.URL.Query().Get("end") == "1" { // a bus search done with it
				sc.LeaseDecodeView(0)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			sc.LeaseDecodeView(3 * time.Second)
		}
	case "spectrum":
		if sc, ok := s.sc.(interface{ LeaseSpectrumView(time.Duration) }); ok {
			if r.URL.Query().Get("end") == "1" { // the FFT view closed: no frame wanted
				sc.LeaseSpectrumView(0)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			sc.LeaseSpectrumView(3 * time.Second)
		}
	case "samples":
		if sc, ok := s.sc.(interface{ LeaseSampleView(time.Duration) }); ok {
			if r.URL.Query().Get("end") == "1" { // a bus search done with it: the live view back at once
				sc.LeaseSampleView(0)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			sc.LeaseSampleView(3 * time.Second)
		}
	case "search":
		if sc, ok := s.sc.(interface{ LeaseSearchView(time.Duration) }); ok {
			if r.URL.Query().Get("end") == "1" { // the records the scope's bus search reads, no longer wanted
				sc.LeaseSearchView(0)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			sc.LeaseSearchView(3 * time.Second)
		}
	}
	// The page zoomed into a live envelope asks for its stretch at full detail
	// (view=a,b,cols: screen widths from the trigger and the canvas columns,
	// renewed with every poll); view=off ends it.
	if v := r.URL.Query().Get("view"); v != "" {
		if sc, ok := s.sc.(interface {
			LeaseViewWindow(engine.ViewSurface, float64, float64, int, time.Duration)
		}); ok {
			var a, b float64
			var cols int
			if n, _ := fmt.Sscanf(v, "%g,%g,%d", &a, &b, &cols); n == 3 {
				sc.LeaseViewWindow(engine.ViewWeb, a, b, cols, 2*time.Second)
			} else {
				sc.LeaseViewWindow(engine.ViewWeb, 0, 0, 0, 0)
			}
		}
	}
	var since uint64
	// An absent or bad number leaves its default.
	_, _ = fmt.Sscanf(r.URL.Query().Get("since"), "%d", &since)
	cols := screenCols
	if c := r.URL.Query().Get("cols"); c != "" {
		_, _ = fmt.Sscanf(c, "%d", &cols)
	}
	if cols < 64 {
		cols = 64
	}
	if cols > 4096 {
		cols = 4096
	}
	full := r.URL.Query().Get("full") == "1"
	waitms := 0
	_, _ = fmt.Sscanf(r.URL.Query().Get("waitms"), "%d", &waitms)
	if waitms < 0 {
		waitms = 0
	}
	if waitms > 2000 {
		waitms = 2000
	}
	// Park even for since=0: WaitNext returns immediately once ANY frame has
	// published, and blocks when none has — otherwise a fresh page against an
	// idle engine hot-loops instant unchanged replies at the client's tick.
	if waitms > 0 {
		s.waitFrame(since, time.Duration(waitms)*time.Millisecond)
	}
	var buf []byte
	// Serialize under the quiet READ-lock so this CPU burst pauses during the
	// engine's arm-settle+drain (which it would otherwise corrupt on the single
	// core). Held only for the ~ms of serialization, not the network write.
	func() {
		s.sc.QuietRLock()
		defer s.sc.QuietRUnlock()
		if r.URL.Query().Get("raw") == "1" {
			buf = s.rawBinMsg(since)
			return
		}
		off, vpc := s.vertScales()
		st := s.sc.Snapshot()
		posFrac := st.TrigPosFrac
		if !(posFrac >= 0 && posFrac <= 1) {
			posFrac = 0.5
		}
		var rep wire.FrameReply
		s.sc.WithFrame(func(f *engine.Frame) {
			rep = s.buildReply(f, cols, full, since, off, vpc, posFrac, st.Running && !st.Single)
		})
		buf = wire.EncodeBinFrame(rep) // encode inside the quiet lock; network write is outside
	}()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	// Bound the write: the server sets no global timeouts (long-poll depends
	// on that), so a half-open peer must not hold this goroutine for minutes.
	webserver.WriteWithin(w, buf, 5*time.Second)
}

// waitFrame parks until a frame after since has published, or timeout.
// TRLC-LINKS: REQ-SDS-163
func (s *Server) waitFrame(since uint64, timeout time.Duration) {
	if fw, ok := s.sc.(frameWaiter); ok {
		fw.WaitNextFrame(since, timeout)
		return
	}
	// test doubles: degrade to a short seq poll
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); {
		var seq uint64
		s.sc.WithFrame(func(f *engine.Frame) {
			if f != nil {
				seq = f.Seq
			}
		})
		if seq != since {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// rawBinMsg assembles the raw-shape binary message (?raw=1): the un-windowed,
// un-interpolated, pre-coupling record — what the browser super-resolution
// stacker aligns and drizzles. Payload = c1[cols] c2[cols] raw ADC codes,
// copied under the fan-out lock (the backing arrays are reused by the next
// tick); header carries sample_s and the engine's sub-sample edge_x. No
// measurements — the stacker computes its own statistics, and skipping the
// meas pass keeps the raw feed cheap next to the display path.
// TRLC-LINKS: REQ-SDS-163
func (s *Server) rawBinMsg(since uint64) []byte {
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
	return wire.EncodeRawFrame(rf)
}
