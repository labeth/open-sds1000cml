// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"context"
	"errors"
	"time"

	"github.com/labeth/open-dso-core/webserver/wire"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
)

// busSampler is the engine's lease of bus samples: while it holds, the
// frames are records sampled at the interval leased.
type busSampler interface {
	LeaseBusSamples(float64, time.Duration)
}

// recordMin is the fewest samples a record served holds.
const recordMin = 1024

// records takes whole sampled records of both channels for the BUS Visual
// view (the live frames carry a screen's few thousand values, the record up
// to the memory's half million a channel).
type records struct {
	s     *Scope
	lease busSampler
}

// Take leases the engine's bus samples while it waits for a record captured
// after the call (the one in flight then may be of the screen before),
// sampled — no envelope — every sampleS or finer. A record that does not
// come is refused with why: the scope's state, not the server, keeps it.
// TRLC-LINKS: REQ-SDS-280
func (rs records) Take(ctx context.Context, want float64, wait time.Duration) (wire.Record, error) {
	s := rs.s
	var start uint64
	s.sc.WithFrame(func(f *engine.Frame) {
		if f != nil {
			start = f.Seq
		}
	})
	deadline := time.Now().Add(wait)
	var rec wire.Record
	got := false
	renewed := time.Time{}
	for !got && time.Now().Before(deadline) && ctx.Err() == nil {
		if time.Since(renewed) > time.Second {
			rs.lease.LeaseBusSamples(want, 3*time.Second)
			renewed = time.Now()
		}
		s.sc.WithFrame(func(f *engine.Frame) {
			// a frame captured after the request, sampled - no envelope - every want or finer
			if f == nil || f.Seq <= start+1 || f.IsEnv || f.PeakDetect || !(f.SampleS > 0) || f.SampleS > want*1.01 {
				return
			}
			n := min(f.Valid, len(f.C1), len(f.C2))
			if n < recordMin {
				return
			}
			rec = wire.Record{Seq: f.Seq, SampleS: f.SampleS, TdivS: f.TdivS}
			rec.C1 = append([]byte(nil), f.C1[:n]...)
			rec.C2 = append([]byte(nil), f.C2[:n]...)
			got = true
		})
		if !got {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if !got {
		if !s.sc.Snapshot().Running {
			return wire.Record{}, errors.New("the scope is stopped: it captures no record (Run it)")
		}
		return wire.Record{}, errors.New("no record sampled that finely came in time: a waiting or streaming scope captures none, and the memory bounds the record (a slower timebase at this rate does not fit)")
	}
	rec.Off, rec.Vpc = s.vertScales()
	return rec, nil
}
