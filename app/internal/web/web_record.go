// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"encoding/binary"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"open-sds/app/internal/engine"
)

// recordReply is /api/record.bin's header: the record's samples a channel,
// their interval, and how a code reads as volts on each channel (off is the
// offset applied, vpc the volts a code).
type recordReply struct {
	Seq     uint64  `json:"seq"`
	N       int     `json:"n"`
	SampleS float64 `json:"sample_s"`
	TdivS   float64 `json:"tdiv_s"`
	Vpc1    float64 `json:"vpc1"`
	Vpc2    float64 `json:"vpc2"`
	Off1V   float64 `json:"off1_v"`
	Off2V   float64 `json:"off2_v"`
	Err     string  `json:"err,omitempty"`
}

// recordWaitMax bounds /api/record.bin's wait for a record.
const recordWaitMax = 10 * time.Second

// recordMin is the fewest samples a record served holds.
const recordMin = 1024

// hRecord serves one whole sampled record of both channels, taken every
// ?sample_s or finer, captured after the request (?wait_ms, at most 10 s,
// 4 s by default): the BUS Visual view decodes digital audio from it (the
// live frames carry a screen's few thousand values, the record up to the
// memory's half million a channel). It leases the engine's bus samples
// (LeaseBusSamples) while it waits. The reply is a little-endian uint32
// header length, the JSON header (recordReply) and the codes, C1's then C2's.
// One record is taken at a time. A record refused (another under way, none
// in time, none this acquisition can serve) is a 409 naming why.
// TRLC-LINKS: REQ-SDS-280
func (s *Server) hRecord(w http.ResponseWriter, r *http.Request) {
	want, err := strconv.ParseFloat(r.URL.Query().Get("sample_s"), 64)
	if err != nil || !(want >= 2e-9) || want > 1 {
		http.Error(w, "sample_s: the sample interval wanted, in seconds (2e-9 or more)", http.StatusBadRequest)
		return
	}
	if !s.recordBusy.CompareAndSwap(false, true) {
		w.Header().Set("Retry-After", "1")
		recordRefused(w, "another record is being taken: one at a time")
		return
	}
	defer s.recordBusy.Store(false)
	wait := 4 * time.Second
	if ms, err := strconv.Atoi(r.URL.Query().Get("wait_ms")); err == nil && ms > 0 {
		wait = min(time.Duration(ms)*time.Millisecond, recordWaitMax)
	}
	lease, ok := s.sc.(interface{ LeaseBusSamples(float64, time.Duration) })
	if !ok {
		recordRefused(w, recordNever)
		return
	}
	var start uint64
	s.sc.WithFrame(func(f *engine.Frame) {
		if f != nil {
			start = f.Seq
		}
	})
	deadline := time.Now().Add(wait)
	var hdr recordReply
	var c1, c2 []byte
	got := false
	renewed := time.Time{}
	for !got && time.Now().Before(deadline) && r.Context().Err() == nil {
		if time.Since(renewed) > time.Second {
			lease.LeaseBusSamples(want, 3*time.Second)
			renewed = time.Now()
		}
		s.sc.WithFrame(func(f *engine.Frame) {
			// a frame captured after the request (the one in flight then may
			// be of the screen before), sampled - no envelope - every want or finer
			if f == nil || f.Seq <= start+1 || f.IsEnv || f.PeakDetect || !(f.SampleS > 0) || f.SampleS > want*1.01 {
				return
			}
			n := min(f.Valid, len(f.C1), len(f.C2))
			if n < recordMin {
				return
			}
			hdr = recordReply{Seq: f.Seq, N: n, SampleS: f.SampleS, TdivS: f.TdivS}
			c1 = append([]byte(nil), f.C1[:n]...)
			c2 = append([]byte(nil), f.C2[:n]...)
			got = true
		})
		if !got {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if !got {
		// the scope's state, not the server, keeps the record from coming: a conflict, said why
		why := "no record sampled that finely came in time: a waiting or streaming scope captures none, and the memory bounds the record (a slower timebase at this rate does not fit)"
		if !s.sc.Snapshot().Running {
			why = "the scope is stopped: it captures no record (Run it)"
		}
		recordRefused(w, why)
		return
	}
	off, vpc := s.vertScales()
	hdr.Vpc1, hdr.Vpc2, hdr.Off1V, hdr.Off2V = vpc[0], vpc[1], off[0], off[1]
	js, _ := json.Marshal(hdr)
	out := make([]byte, 4, 4+len(js)+len(c1)+len(c2))
	binary.LittleEndian.PutUint32(out, uint32(len(js)))
	out = append(out, js...)
	out = append(out, c1...)
	out = append(out, c2...)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(out)
}

// recordNever is the refusal no retry changes: this acquisition serves no
// sampled records (the page's Live audio stops on it).
const recordNever = "this acquisition cannot serve sampled records"

// recordRefused answers a record refused for the scope's state: 409 and why.
// TRLC-LINKS: REQ-SDS-280
func recordRefused(w http.ResponseWriter, why string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(recordReply{Err: why})
}
