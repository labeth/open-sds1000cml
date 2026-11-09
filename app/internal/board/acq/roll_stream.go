// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"context"
	"math"
	"slices"
	"time"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/bus"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// Roll on the stream image (ADR-STREAM-IMAGE), the roll-stream preference
// on: one continuous peak-detect stream whose every word reaches the host
// in order through the stream banks, so the view has no gaps. A word is a
// bucket of 2^log samples as both channels' minimum and maximum
// ({max2,max1,min2,min1}), two view samples a bucket ordered to continue
// from the sample before; beyond the largest bucket a software group
// merges words. Data captured across a change of a channel's scale, or
// while the front end settles after one, is dropped, and what follows is
// placed by the host clock after the newest samples, the time between held
// at the last (min, max) pair: the stream goes on through a setting.

// rollSettle covers words still in flight at the old scale (the stream's
// buffering, a chunk armed before the change) and the front end's switching
// transient after a channel's scale changes.
const rollSettle = 250 * time.Millisecond

// RollPeakPlan is the decimation and geometry of a peak-detect roll view.
type RollPeakPlan struct {
	Log     uint8
	SampleS float64 // between view samples: two a bucket
	Screen  int     // view samples on the screen
	Group   int     // stream words a view pair merges, when the bucket tops out
}

// PlanRollPeak picks the finest bucket whose screen of (min, max) pairs fits
// rollScreenSamples, and a software group beyond the largest bucket.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-035
func PlanRollPeak(tdiv float64) RollPeakPlan {
	span := 10 * tdiv
	p := RollPeakPlan{Log: 8}
	for p.Log < 20 && span*500e6/float64(uint64(1)<<p.Log) > rollScreenSamples/2 {
		p.Log++
	}
	p.Group = max(1, int(math.Ceil(span*500e6/float64(uint64(1)<<p.Log)/(rollScreenSamples/2))))
	bucketS := float64(uint64(1)<<p.Log) * float64(p.Group) / 500e6
	p.SampleS = bucketS / 2
	p.Screen = int(math.Max(2, 2*math.Round(span/bucketS)))
	return p
}

// peakRollPlanOf is the hal plan of a roll request with the roll-stream
// preference on: both channels' peak pairs on the stream image.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-035
func peakRollPlanOf(r hal.Request) hal.Plan {
	p := PlanRollPeak(r.TimePerDivS)
	r.Channels = []int{0, 1}
	return hal.Plan{
		Request: r, Mode: hal.RollMode, SampleS: p.SampleS, Depth: p.Screen, AcqDecim: 1 << p.Log,
		ETSFactor: 1, Segments: 1, WinSamples: p.Screen, Content: sample.ContentSetOf(sample.PeakPairs),
		Note: "roll view: continuous peak-detect stream (stream image)",
	}
}

// SetRollStream chooses how the board rolls: off (the default) it captures
// short chunks on whatever image is loaded, so a timebase step into or out
// of roll is instant; on, it streams peak pairs on the stream image, gap
// free, at the cost of an image load whenever roll starts or ends. Its
// planner answers a roll request with the way chosen. ANY.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-035
func (b *Board) SetRollStream(on bool) { b.rollStream.Store(on) }

// RollStream is the roll-stream preference. ANY.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-035
func (b *Board) RollStream() bool { return b.rollStream.Load() }

// peakState is a running peak-detect roll stream (owner only).
type peakState struct {
	on        bool
	plan      RollPeakPlan
	chans     []int
	first     bool      // the next data is placed by the host clock after the newest samples
	lastEnd   time.Time // the host time of the newest sample
	settle    time.Time // data captured before this is dropped
	scaleSeen uint64    // the scale writes seen (panelPort.scaleGen)
	scales    []sample.Scale
	filled    bool
	last      [2][2]uint16 // each channel's two newest view samples, the newest second
	next      uint64       // the next view sample's number
	carry     []byte       // a partial software group's words
	q         [2][]uint16
	held      []bool
	words     []uint16
	formats   []hal.CodeFormat
}

// peakStart starts the peak-detect roll stream of the staged roll plan on
// the stream image (loaded first when another is in use or a loss left it
// faulted). OWNER, LONG when the image loads.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-035
func (b *Board) peakStart(spec hal.StreamSpec) error {
	if !b.HasImage(ImageStream) {
		return hal.Refuse("roll stream", "this build has no stream image")
	}
	st := &b.stream
	if b.imageLoaded != ImageStream || st.reload {
		st.reload = false
		if err := b.loadFresh(ImageStream); err != nil {
			return err
		}
	}
	p := PlanRollPeak(b.plan.Request.TimePerDivS)
	t := b.StagedTrigger()
	cfg := sramcapture.Config{
		Source: sramcapture.ADC, DecimationLog2: p.Log, PreWords: sramcapture.Words - 17, PostWords: 17,
		TriggerChannel: t.TriggerChannel, TriggerLevel: t.TriggerLevel,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	err := b.mem.StartStream(ctx, cfg, true, true, sramcapture.StreamPace{})
	cancel()
	if err != nil {
		return err
	}
	st.mu.Lock()
	st.running, st.spec, st.expected, st.faults = true, spec, 0, hal.StreamFaults{}
	st.sampleS = p.SampleS
	st.mu.Unlock()
	st.markMu.Lock()
	st.marksOn = false
	st.markMu.Unlock()
	b.peak = peakState{on: true, plan: p, chans: slices.Clone(spec.Channels), first: true, scaleSeen: b.panel.scaleGen.Load()}
	b.state = hal.Armed
	// With a bus worker (the instrument), the worker drains the banks at its
	// FIFO service into a ring, so the app's scheduling cannot lose words.
	if pm, ok := b.panel.bus.(streamPumper); ok {
		owned, err := pm.SetIsolatedStreamPump(true)
		if err != nil {
			_ = (*streamerFacet)(b).Stop()
			return err
		}
		if owned {
			st.pump = pm
		}
	}
	if dev, ok := b.panel.bus.(*bus.Dev); ok && st.pump == nil {
		if d, err := dev.StartDecodedDeadline(); err == nil {
			st.deadline = d
		}
	}
	return nil
}

// peakLease takes the stream's next words as view samples (PayloadChunks):
// data captured across a scale change or in its settle is dropped (an empty
// block brings the new scale), the data after it follows the held time the
// drop left. A failed drain ends the stream; one that failed before its
// first word loads the image again at the next start. OWNER.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-035
func (b *Board) peakLease(ctx context.Context) (hal.StreamBlock, error) {
	s := (*streamerFacet)(b)
	st := s.streamOf()
	pk := &b.peak
	b.streamWrites() // the front end staged meanwhile, between the stream's words
	st.mu.Lock()
	defer st.mu.Unlock()
	for {
		if g := b.panel.scaleGen.Load(); g != pk.scaleSeen {
			pk.scaleSeen, pk.settle, pk.carry = g, b.now().Add(rollSettle), nil
		}
		raw, err := s.nextWords()
		if err != nil {
			st.faults.Stalls++
			if st.expected == 0 {
				st.reload = true
			}
			return hal.StreamBlock{}, err
		}
		if len(raw) >= 4 {
			now := b.now()
			scales := b.streamScales(pk.chans)
			if now.Before(pk.settle) || b.panel.scaleGen.Load() != pk.scaleSeen || !slices.Equal(scales, pk.scales) && pk.scales != nil {
				// words at the old scale or while the front end settled:
				// dropped, what follows placed by the host clock
				pk.first = true
				pk.rescale(scales)
				return pk.empty(scales), nil
			}
			pk.rescale(scales)
			if raw = pk.group(raw); len(raw) == 0 {
				continue // the view moves once a whole group is in
			}
			return b.peakBlock(raw, now), nil
		}
		if err := ctx.Err(); err != nil {
			return hal.StreamBlock{}, err
		}
		if st.deadline != nil {
			_ = st.deadline.Pause()
		} else {
			sleepCtx(ctx, streamIdle)
		}
	}
}

// rescale takes scales as the stream's, the held samples' values carried
// over in volts.
// TRLC-LINKS: REQ-SDS-010
func (pk *peakState) rescale(scales []sample.Scale) {
	if pk.scales != nil && scales != nil {
		for k := range min(len(scales), len(pk.scales)) {
			ch := pk.chans[k] & 1
			for j := range pk.last[ch] {
				pk.last[ch][j] = rescaleWord(pk.last[ch][j], pk.scales[k], scales[k])
			}
		}
	}
	pk.scales = scales
}

// empty is a block of no samples at scales.
// TRLC-LINKS: REQ-SDS-010
func (pk *peakState) empty(scales []sample.Scale) hal.StreamBlock {
	pk.formats = pk.formats[:0]
	for range pk.chans {
		pk.formats = append(pk.formats, q88Format)
	}
	return hal.StreamBlock{Payload: hal.PayloadChunks, Formats: pk.formats, Scales: scales, First: pk.next, SampleS: pk.plan.SampleS}
}

// group merges plan.Group consecutive peak words into one, carrying a
// partial group to the next call.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-035
func (pk *peakState) group(raw []byte) []byte {
	g := pk.plan.Group
	if g <= 1 {
		return raw
	}
	pk.carry = append(pk.carry, raw...)
	data := pk.carry
	n := len(data) / 4 / g
	out := make([]byte, 4*n)
	for k := range n {
		w := data[4*g*k:]
		lo1, lo2, hi1, hi2 := w[0], w[1], w[2], w[3]
		for j := 1; j < g; j++ {
			x := w[4*j:]
			lo1, lo2, hi1, hi2 = min(lo1, x[0]), min(lo2, x[1]), max(hi1, x[2]), max(hi2, x[3])
		}
		out[4*k], out[4*k+1], out[4*k+2], out[4*k+3] = lo1, lo2, hi1, hi2
	}
	pk.carry = append([]byte(nil), data[4*g*n:]...)
	return out
}

// peakBlock is raw's peak words as view samples, after the held time a
// drop or the stream's start left when the data follows one: two samples a
// word, the converter offsets removed as for an envelope recall, each pair
// ordered to continue from the sample before (OrderEnvelope).
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-035
func (b *Board) peakBlock(raw []byte, now time.Time) hal.StreamBlock {
	pk := &b.peak
	n := len(raw) / 4
	gap := 0
	if pk.first {
		if !pk.lastEnd.IsZero() {
			missing := now.Sub(pk.lastEnd).Seconds() - float64(2*n)*pk.plan.SampleS
			gap = min(max(0, int(math.Round(missing/pk.plan.SampleS))), pk.plan.Screen+1)
		}
		pk.first = false
	}
	for ch := range 2 {
		c := make([]uint8, 2+2*n)
		q := make([]uint16, 2+2*n)
		for i := range n {
			w := raw[4*i : 4*i+4]
			c[2+2*i], c[3+2*i] = w[ch], w[2+ch] // min, max
		}
		body, qb := c[2:], q[2:]
		if cal := b.cond.cal; cal != nil {
			cal.DebiasEnvelope(ch, body, qb, false)
		} else {
			for i, v := range body {
				qb[i] = uint16(v) << 8
			}
		}
		// the held time holds the last (min, max) pair; the ordering
		// continues from the newest sample
		out := pk.q[ch][:0]
		for j := range gap {
			out = append(out, pk.last[ch][j%2])
		}
		if gap > 0 {
			pk.last[ch] = [2]uint16{pk.last[ch][gap%2], pk.last[ch][(gap+1)%2]}
		}
		q[1] = pk.last[ch][1]
		if !pk.filled {
			q[1] = qb[0]
		}
		c[1] = core.RoundQ8(q[1])
		OrderEnvelope(c, q)
		out = append(out, qb...)
		if k := len(out); k >= 2 {
			pk.last[ch] = [2]uint16{out[k-2], out[k-1]}
		}
		pk.q[ch] = out
	}
	pk.filled = true
	pk.lastEnd = now
	m := gap + 2*n
	pk.held = slices.Grow(pk.held[:0], m)[:m]
	for i := range pk.held {
		pk.held[i] = i < gap
	}
	pk.words = slices.Grow(pk.words[:0], m*len(pk.chans))[:m*len(pk.chans)]
	pk.formats = pk.formats[:0]
	for k, ch := range pk.chans {
		copy(pk.words[k*m:(k+1)*m], pk.q[ch&1])
		pk.formats = append(pk.formats, q88Format)
	}
	first := pk.next
	pk.next += uint64(m)
	return hal.StreamBlock{
		Payload: hal.PayloadChunks, Formats: pk.formats, Words: pk.words, Held: pk.held, First: first,
		SampleS: pk.plan.SampleS, Scales: pk.scales,
	}
}

// streamWrites writes, while a stream runs, what the front end staged and
// the relay and gain words its own setters left, between the stream's
// words or chunks, as the scope's own loops do. OWNER.
// TRLC-LINKS: REQ-SDS-015, REQ-SDS-010, REQ-SDS-265
func (b *Board) streamWrites() {
	if b.frontEnd == nil || !b.frontEnd.pendingWrite() {
		return
	}
	if _, err := b.writeFrontEnd(); err != nil && b.panel.writeErr != nil {
		b.panel.writeErr(err)
	}
}

// writeFrontEnd applies the front end's staged settings and writes its due
// hardware words, and reports whether anything reached the hardware; a
// write counts as a change of the channels' scale (panelPort.scaleGen).
// OWNER, with no capture armed (or between a stream's words).
// TRLC-LINKS: REQ-SDS-015
func (b *Board) writeFrontEnd() (bool, error) {
	wrote, err := b.frontEnd.write()
	if wrote {
		b.panel.scaleGen.Add(1)
	}
	return wrote, err
}

// streamScales is the volts of channels chans' Q8.8 words as the front end
// stands (applied); nil without a front end.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-265
func (b *Board) streamScales(chans []int) []sample.Scale {
	if b.frontEnd == nil {
		return nil
	}
	out := make([]sample.Scale, len(chans))
	for k, ch := range chans {
		fe := b.frontEnd.Get(ch & 1)
		if !(fe.VoltsDiv > 0) {
			return nil
		}
		out[k] = sample.Scale{Zero: float64(q88Format.Mid), PerCode: fe.VoltsDiv / (CodesPerDiv * 256), OffsetV: fe.OffsetV}
	}
	return out
}

// rescaleWord is word w at scale from as the word at scale to of the same
// volts, within the words' range.
// TRLC-LINKS: REQ-SDS-010
func rescaleWord(w uint16, from, to sample.Scale) uint16 {
	if !(from.PerCode > 0) || !(to.PerCode > 0) || from == to {
		return w
	}
	return uint16(min(max(math.Round(to.Word(from.Volts(float64(w)))), 0), 0xFFFF))
}
