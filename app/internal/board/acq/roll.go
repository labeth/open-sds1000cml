// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// Roll (ADR-SRAM-ROLL) on any image: the shipping fabric reads SRAM only
// from a frozen record, so the roll stream is short back-to-back decimated
// captures (PayloadChunks), each placed on the host clock after the one
// before; the samples between them, the re-arm and the readout, hold the
// last value and are marked Held. Beyond the largest decimation a software
// group averages captured samples, so a screen stays within
// rollScreenSamples. Settings go on while it rolls: each chunk is armed on
// the trigger staged then, the front end is written between chunks, and a
// chunk captured across a change of a channel's scale, or armed while the
// front end settles after one, is dropped (the next chunk's placement
// holds over it).

// rollScreenSamples bounds the roll view; a screen is decimated to fit.
const rollScreenSamples = 4096

// rollChunkS is the length of one capture: about 20 view updates a second.
const rollChunkS = 0.05

// RollPlan is the decimation and geometry of a roll view of captures.
type RollPlan struct {
	Log     uint8
	SampleS float64 // between view samples
	Screen  int     // view samples on the screen
	Chunk   int     // words a capture
	Group   int     // captured samples a view sample averages
}

// PlanRoll picks the finest decimation whose screen fits rollScreenSamples.
// TRLC-LINKS: REQ-SDS-010
func PlanRoll(tdiv float64) RollPlan {
	span := 10 * tdiv
	p := RollPlan{Log: 4, SampleS: 32e-9, Group: 1}
	for p.Log < 20 && span/p.SampleS > rollScreenSamples {
		p.Log++
		p.SampleS *= 2
	}
	hwS := p.SampleS
	p.Group = max(1, int(math.Ceil(span/hwS/rollScreenSamples)))
	p.SampleS = hwS * float64(p.Group)
	p.Screen = int(math.Max(2, math.Round(span/p.SampleS)))
	p.Chunk = max(8, min(int(math.Round(rollChunkS/hwS)), p.Screen*p.Group))
	return p
}

// rollPlanOf is the hal plan of a roll request.
// TRLC-LINKS: REQ-SDS-010
func rollPlanOf(r hal.Request) hal.Plan {
	p := PlanRoll(r.TimePerDivS)
	r.Channels = []int{0, 1} // the record holds both: a channel turned on keeps its view
	return hal.Plan{
		Request: r, Mode: hal.RollMode, SampleS: p.SampleS, Depth: p.Chunk, AcqDecim: 1 << p.Log,
		ETSFactor: 1, Segments: 1, WinSamples: p.Screen, Content: sample.ContentSetOf(sample.RawDecimated),
		Note: "roll: back-to-back decimated captures; gaps between them hold the last value",
	}
}

// rollState is a running stream of roll captures (owner only).
type rollState struct {
	running bool
	plan    RollPlan
	cfg     sramcapture.Config
	chans   []int
	// a chunk armed and not yet read (it outlived a lease), its arm time,
	// the scale writes seen then and the channels' scales it was armed at
	armed     bool
	armAt     time.Time
	armSeen   uint64
	armScales []sample.Scale
	lastEnd   time.Time // the host time of the newest sample
	next      uint64    // the next view sample's number
	last      [2]uint16 // the newest view sample, held over the gaps
	// the channels' scale the newest samples were captured at, the scale
	// writes seen and the settle after the newest
	scales    []sample.Scale
	scaleSeen uint64
	settle    time.Time
	// a partial software group carried to the next capture
	accN, accHeld int
	acc           [2]int64
	q             [2][]uint16
	held          []bool
	words         []uint16
	formats       []hal.CodeFormat
}

// rollStart starts the roll captures of the staged roll plan. OWNER.
// TRLC-LINKS: REQ-SDS-010
func (b *Board) rollStart(spec hal.StreamSpec) error {
	if b.plan.Mode != hal.RollMode {
		return hal.Refuse("roll", "stage a roll plan first")
	}
	if len(spec.Channels) == 0 || len(spec.Channels) > 2 || slices.ContainsFunc(spec.Channels, func(c int) bool { return c < 0 || c > 1 }) {
		return hal.Refuse("stream channels", "the record holds C1 and C2")
	}
	if b.imageLoaded == ImageStream {
		// the stream image streams; the chunks are captured on the general one
		if err := b.loadFresh(ImageGeneral); err != nil {
			return err
		}
	}
	p := PlanRoll(b.plan.Request.TimePerDivS)
	b.roll = rollState{
		running: true, plan: p, chans: slices.Clone(spec.Channels), scaleSeen: b.panel.scaleGen.Load(),
		cfg: sramcapture.Config{
			Source: sramcapture.ADC, DecimationLog2: p.Log, PreWords: uint32(p.Chunk - 1), PostWords: 1, //nolint:gosec // G115: a chunk is 8..Words words
		},
	}
	return nil
}

// rollLease writes what the front end staged, captures the next chunk on
// the trigger staged now and hands it out after the samples that held
// since the chunk before, as view samples (a software group's averages
// beyond the largest decimation), at the scale the front end stood at. An
// empty block, with the scale, when a whole group is not in yet or the
// chunk was dropped (captured across a scale change, or armed in the
// settle after one). A chunk that takes longer than ctx gives is left
// capturing: the lease ends with ctx's error and the next lease waits for
// that chunk, so a chunk longer than the owner's lease still arrives.
// OWNER.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-015
func (b *Board) rollLease(ctx context.Context) (hal.StreamBlock, error) {
	r := &b.roll
	if !r.armed {
		b.streamWrites() // the relays switch between chunks
		seen := b.panel.scaleGen.Load()
		if seen != r.scaleSeen {
			// a scale change: the partial group is dropped, the chunks armed in
			// its settle too
			r.scaleSeen, r.settle = seen, b.now().Add(rollSettle)
			r.accN, r.accHeld, r.acc = 0, 0, [2]int64{}
		}
		cfg := r.cfg
		t := b.StagedTrigger()
		cfg.TriggerChannel, cfg.TriggerLevel = t.TriggerChannel, t.TriggerLevel
		r.armScales = b.streamScales(r.chans)
		r.armAt, r.armSeen = b.now(), seen
		actx, cancel := context.WithTimeout(ctx, armTimeout)
		err := b.mem.Arm(actx, cfg)
		cancel()
		if err != nil {
			return hal.StreamBlock{}, err
		}
		r.armed = true
	}
	var m sramcapture.Metadata
	var err error
	for {
		if m, err = b.mem.Status(); err != nil || m.DataFault || (m.Frozen && m.Ready) {
			break
		}
		if err := ctx.Err(); err != nil {
			return hal.StreamBlock{}, err // the chunk goes on capturing for the next lease
		}
		b.pause(time.Millisecond)
	}
	r.armed = false
	scales, armAt, seen := r.armScales, r.armAt, r.armSeen
	end := b.now()
	if err != nil || m.DataFault {
		hctx, cancel := context.WithTimeout(context.Background(), haltTimeout)
		herr := b.mem.Halt(hctx)
		cancel()
		if err == nil {
			err = errors.New("SRAM acquisition FIFO overflow")
		}
		return hal.StreamBlock{}, errors.Join(err, herr)
	}
	var raw rawWords
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second) // the frozen chunk is read whole
	recall := b.mem.Recall
	if m.Revision == 10 {
		recall = b.mem.RecallForward
	}
	_, err = recall(rctx, 0, m.Length, &raw)
	cancel()
	if err == nil && len(raw.b) < 4 {
		err = errors.New("empty roll chunk")
	}
	if err != nil {
		return hal.StreamBlock{}, err
	}
	n := len(raw.b) / 4
	hwS := r.plan.SampleS / float64(r.plan.Group)
	gap := 0
	if !r.lastEnd.IsZero() {
		missing := end.Sub(r.lastEnd).Seconds() - float64(n)*hwS
		gap = max(0, int(math.Round(missing/hwS)))
	}
	if armAt.Before(r.settle) || b.panel.scaleGen.Load() != seen || !slices.Equal(scales, b.streamScales(r.chans)) {
		// captured across a scale change or while the front end settled:
		// the next chunk's placement holds over it
		r.rescale(scales)
		return r.empty(scales), nil
	}
	r.lastEnd = end
	r.rescale(scales)
	return r.block(gap, raw.b), nil
}

// rescale takes scales as the newest samples', the held value carried over
// in volts.
// TRLC-LINKS: REQ-SDS-010
func (r *rollState) rescale(scales []sample.Scale) {
	if r.scales != nil && scales != nil {
		for k := range min(len(scales), len(r.scales)) {
			ch := r.chans[k] & 1
			r.last[ch] = rescaleWord(r.last[ch], r.scales[k], scales[k])
		}
	}
	r.scales = scales
}

// empty is a block of no samples at scales.
// TRLC-LINKS: REQ-SDS-010
func (r *rollState) empty(scales []sample.Scale) hal.StreamBlock {
	r.formats = r.formats[:0]
	for range r.chans {
		r.formats = append(r.formats, q88Format)
	}
	return hal.StreamBlock{Payload: hal.PayloadChunks, Formats: r.formats, Scales: scales, First: r.next, SampleS: r.plan.SampleS}
}

// block is a chunk's words after gap held samples as view samples.
// TRLC-LINKS: REQ-SDS-010
func (r *rollState) block(gap int, raw []byte) hal.StreamBlock {
	g := r.plan.Group
	gap = min(gap, (r.plan.Screen+1)*g) // older than the view: drawn as held anyway
	if r.next == 0 && r.accN == 0 {
		r.last = [2]uint16{binary.LittleEndian.Uint16(raw), binary.LittleEndian.Uint16(raw[2:])}
	}
	first := r.next
	r.q[0], r.q[1], r.held = r.q[0][:0], r.q[1][:0], r.held[:0]
	add := func(k int, v [2]uint16, held bool) {
		for k > 0 {
			t := min(k, g-r.accN)
			r.acc[0] += int64(t) * int64(v[0])
			r.acc[1] += int64(t) * int64(v[1])
			r.accN += t
			if held {
				r.accHeld += t
			}
			k -= t
			if r.accN == g {
				r.q[0] = append(r.q[0], uint16(r.acc[0]/int64(g))) //nolint:gosec // G115: a mean of Q8.8 words
				r.q[1] = append(r.q[1], uint16(r.acc[1]/int64(g))) //nolint:gosec // G115: a mean of Q8.8 words
				r.held = append(r.held, 2*r.accHeld > g)
				r.accN, r.accHeld, r.acc = 0, 0, [2]int64{}
			}
		}
	}
	add(gap, r.last, true)
	for i := 0; i+3 < len(raw); i += 4 {
		add(1, [2]uint16{binary.LittleEndian.Uint16(raw[i:]), binary.LittleEndian.Uint16(raw[i+2:])}, false)
	}
	r.last = [2]uint16{binary.LittleEndian.Uint16(raw[len(raw)-4:]), binary.LittleEndian.Uint16(raw[len(raw)-2:])}
	n := len(r.held)
	r.next += uint64(n)
	r.words = slices.Grow(r.words[:0], n*len(r.chans))[:n*len(r.chans)]
	r.formats = r.formats[:0]
	for k, ch := range r.chans {
		copy(r.words[k*n:(k+1)*n], r.q[ch])
		r.formats = append(r.formats, q88Format)
	}
	return hal.StreamBlock{Payload: hal.PayloadChunks, Formats: r.formats, Words: r.words, Held: r.held, First: first, SampleS: r.plan.SampleS, Scales: r.scales}
}

// rollStop ends the roll captures: a capture in flight is halted. OWNER.
// TRLC-LINKS: REQ-SDS-010
func (b *Board) rollStop() error {
	b.roll.running, b.roll.armed = false, false
	ctx, cancel := context.WithTimeout(context.Background(), haltTimeout)
	defer cancel()
	if err := b.mem.Halt(ctx); err != nil {
		return fmt.Errorf("roll: %w", err)
	}
	return nil
}

// rawWords collects a recall's whole words.
type rawWords struct{ b []byte }

// TRLC-LINKS: REQ-SDS-010
func (w *rawWords) Write(p []byte) (int, error) {
	if len(p)%4 != 0 {
		return 0, errors.New("unaligned roll payload")
	}
	w.b = append(w.b, p...)
	return len(p), nil
}
