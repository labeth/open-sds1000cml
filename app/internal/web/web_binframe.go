package web

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"open-sds/app/internal/engine"
	"time"
)

// encodeBinFrame serializes a built reply to the binary wire format. It runs
// after WithFrame returns — rep owns its data, so no lock is held here.
func encodeBinFrame(rep frameReply) []byte {
	var flags byte
	var segs [][]int16
	head, tail := 0, 0
	switch {
	case rep.Unchanged:
		flags |= binUnchanged
	case rep.IsEnv:
		flags |= binEnv
		segs = [][]int16{rep.E1Min, rep.E1Max, rep.E2Min, rep.E2Max}
	default:
		if rep.Depth > 0 {
			flags |= binDeep
		}
		n := len(rep.C1)
		for head < n && rep.C1[head] < 0 {
			head++
		}
		if head == n { // whole array is margin (defensive: Valid<1 never publishes)
			flags |= binEmpty
		} else {
			for rep.C1[n-1-tail] < 0 {
				tail++
			}
			segs = [][]int16{rep.C1, rep.C2}
		}
	}
	hdr := rep
	hdr.C1, hdr.C2 = nil, nil
	hdr.E1Min, hdr.E1Max, hdr.E2Min, hdr.E2Max = nil, nil, nil, nil
	hdr.Head, hdr.Tail = head, tail
	hj, err := json.Marshal(hdr)
	if err != nil { // unreachable with finite inputs; keep the wire well-formed
		flags = binUnchanged
		segs, head, tail = nil, 0, 0
		hj = []byte(`{"seq":0,"unchanged":true,"edge_x":-1}`)
	}
	segLen := 0
	if len(segs) > 0 {
		segLen = len(segs[0]) - head - tail
	}
	buf := make([]byte, 8, 8+len(hj)+segLen*len(segs))
	buf[0] = binMagic
	buf[1] = flags
	binary.LittleEndian.PutUint32(buf[4:8], uint32(len(hj)))
	buf = append(buf, hj...)
	for _, seg := range segs {
		for _, v := range seg[head : head+segLen] {
			buf = append(buf, uint8(v))
		}
	}
	return buf
}

// hFrameBin is the long-poll binary frame endpoint: same reply as /api/frame
// (built by the shared buildReply), encoded as a small JSON header + raw
// uint8 payload — ~1 ms on the device versus 50-150 ms of reflective JSON
// over int16 arrays, which is what capped the browser at a few fps. With
// since= and waitms= the request parks (no locks held) until the fan-out
// snapshots a newer frame, so delivery latency is one response write and the
// client needs no poll timer: request-when-ready IS the backpressure, and a
// slow client simply skips to the newest frame.
func (s *Server) hFrameBin(w http.ResponseWriter, r *http.Request) {
	var since uint64
	fmt.Sscanf(r.URL.Query().Get("since"), "%d", &since)
	cols := screenCols
	if c := r.URL.Query().Get("cols"); c != "" {
		fmt.Sscanf(c, "%d", &cols)
	}
	if cols < 64 {
		cols = 64
	}
	if cols > 4096 {
		cols = 4096
	}
	full := r.URL.Query().Get("full") == "1"
	waitms := 0
	fmt.Sscanf(r.URL.Query().Get("waitms"), "%d", &waitms)
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
		timeout := time.Duration(waitms) * time.Millisecond
		if fw, ok := s.sc.(frameWaiter); ok {
			fw.WaitNextFrame(since, timeout)
		} else { // test doubles: degrade to a short seq poll
			for deadline := time.Now().Add(timeout); time.Now().Before(deadline); {
				var seq uint64
				s.sc.WithFrame(func(f *engine.Frame) {
					if f != nil {
						seq = f.Seq
					}
				})
				if seq != since {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
		}
	}
	var buf []byte
	if r.URL.Query().Get("raw") == "1" {
		buf = s.rawBinMsg(since)
	} else {
		off, vpc := s.vertScales()
		st := s.sc.Snapshot()
		posFrac := st.TrigPosFrac
		if posFrac <= 0 {
			posFrac = 0.5
		}
		var rep frameReply
		s.sc.WithFrame(func(f *engine.Frame) {
			rep = s.buildReply(f, cols, full, since, off, vpc, posFrac, st.Running && !st.Single)
		})
		buf = encodeBinFrame(rep) // encode + write strictly outside the fan-out lock
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	// Bound the write: the server sets no global timeouts (long-poll depends
	// on that), so a half-open peer must not hold this goroutine for minutes.
	// CLEAR the deadline afterwards — with WriteTimeout=0 net/http never
	// resets it, and the keep-alive connection is reused by other handlers
	// that set none (an absolute deadline left armed would fail them later).
	rc := http.NewResponseController(w)
	rc.SetWriteDeadline(time.Now().Add(5 * time.Second))
	w.Write(buf)
	rc.SetWriteDeadline(time.Time{})
}

// rawBinMsg assembles the raw-shape binary message (?raw=1): the un-windowed,
// un-interpolated, pre-coupling record — what the browser super-resolution
// stacker aligns and drizzles. Payload = c1[cols] c2[cols] raw ADC codes,
// copied under the fan-out lock (the backing arrays are reused by the next
// tick); header carries sample_s and the engine's sub-sample edge_x. No
// measurements — the stacker computes its own statistics, and skipping the
// meas pass keeps the raw feed cheap next to the display path.
func (s *Server) rawBinMsg(since uint64) []byte {
	off, vpc := s.vertScales()
	var hdr frameReply
	var payload []byte
	var flags byte = binRaw
	s.sc.WithFrame(func(f *engine.Frame) {
		if f == nil || f.Seq == 0 || f.Seq == since {
			var seq uint64
			if f != nil {
				seq = f.Seq
			}
			hdr = frameReply{Seq: seq, Unchanged: true, EdgeX: -1}
			flags |= binUnchanged
			return
		}
		n := f.Valid
		// clamp to the actual sample slices: Valid > len is an engine-invariant
		// violation, but a serve-path slice panic on one bad frame must never
		// crash the UI (frame-serve fuzz). C2 may be shorter than C1.
		if n > len(f.C1) {
			n = len(f.C1)
		}
		if n > len(f.C2) {
			n = len(f.C2)
		}
		if n <= 0 {
			hdr = frameReply{Seq: f.Seq, Unchanged: true, EdgeX: -1}
			flags |= binUnchanged | binEmpty
			return
		}
		hdr = frameReply{
			Seq: f.Seq, EdgeX: f.EdgeX, Ptp: f.Ptp, TdivS: f.TdivS,
			DisplayedS: f.DisplayedS, Interp: f.Interp, Norm: f.Norm,
			Trigd: f.Trigd, Coherent: f.Coherent, IsEnv: f.IsEnv,
			Cols: n, ColSpanS: float64(n) * f.SampleS, SampleS: f.SampleS,
			EdgeFrac: -1, WinFrac: 1,
			Vpc1: vpc[0], Vpc2: vpc[1], Off1V: off[0], Off2V: off[1],
		}
		hdr.StreamSeq, hdr.WindowNs, hdr.GapNs = f.StreamSeq, f.WindowNs, f.GapNs
		payload = make([]byte, 2*n)
		copy(payload[:n], f.C1[:n])
		copy(payload[n:], f.C2[:n])
	})
	hj, err := json.Marshal(hdr)
	if err != nil { // unreachable with finite inputs
		hj, payload, flags = []byte(`{"seq":0,"unchanged":true,"edge_x":-1}`), nil, binRaw|binUnchanged
	}
	buf := make([]byte, 8, 8+len(hj)+len(payload))
	buf[0] = binMagic
	buf[1] = flags
	binary.LittleEndian.PutUint32(buf[4:8], uint32(len(hj)))
	buf = append(buf, hj...)
	return append(buf, payload...)
}
