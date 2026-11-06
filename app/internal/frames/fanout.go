// Package frames is the SDS1000CML+ engine's frame fan-out: the core's
// fanout over engine.Frame, with the copy that knows the engine frame's
// buffers. The arena's Consume contract permits ONE consumer; the fan-out
// is that consumer, and everyone else works on its deep copy.
// ENGMODEL-OWNER-UNIT: FU-APP-FRAMES
package frames

import (
	"github.com/labeth/open-dso-core/engine/fanout"

	"github.com/labeth/open-sds1000cml/app/internal/engine"
)

// Source is the single-consumer frame producer (the engine).
// TRLC-LINKS: REQ-SDS-020
type Source = fanout.Source[engine.Frame]

// Fanout holds the newest engine frame for every reader (LCD, web) and wakes
// those waiting for a fresh one.
// TRLC-LINKS: REQ-SDS-020
type Fanout = fanout.Fanout[engine.Frame]

// New returns an empty fan-out.
// TRLC-LINKS: REQ-SDS-020
func New() *Fanout { return fanout.New(CopyFrame, frameSeq) }

// frameSeq is a frame's publish sequence.
// TRLC-LINKS: REQ-SDS-020
func frameSeq(f *engine.Frame) uint64 { return f.Seq }

// CopyFrame deep-copies src into dst, reusing dst's buffers: the
// snapshot's copy, and the copy a reader takes for long work (a bus search)
// so that it does not hold the read lock meanwhile.
// TRLC-LINKS: REQ-SDS-020
func CopyFrame(dst, src *engine.Frame) {
	c1, c2 := dst.C1, dst.C2
	q1, q2 := dst.Q1, dst.Q2
	e1n, e1x, e2n, e2x := dst.EnvMin, dst.EnvMax, dst.EnvMin2, dst.EnvMax2
	*dst = *src
	dst.Q1 = reuse(q1, src.Q1)
	dst.Q2 = reuse(q2, src.Q2)
	dst.C1 = reuse(c1, src.C1[:src.Valid])
	dst.C2 = reuse(c2, src.C2[:src.Valid])
	dst.EnvMin = reuse(e1n, src.EnvMin)
	dst.EnvMax = reuse(e1x, src.EnvMax)
	dst.EnvMin2 = reuse(e2n, src.EnvMin2)
	dst.EnvMax2 = reuse(e2x, src.EnvMax2)
}

// reuse copies src into buf's storage, growing it only when it is short.
// TRLC-LINKS: REQ-SDS-020
func reuse[T any](buf, src []T) []T { return append(buf[:0], src...) }
