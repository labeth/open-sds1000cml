// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"math"

	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/sample"
)

// AC coupling on this scope is a software transform: the front end
// passes DC, and an AC-coupled channel is shown with its mean at the
// centre, its offset not applied. A level set on that trace is a level
// above the channel's mean, and the fabric compares the raw codes; so an
// AC-coupled channel's codes are read relative to its mean (acZero): the
// trigger's level becomes that many codes above the mean, and the frames
// state the same scale, so the owner's placement on the frame finds the
// crossing where the fabric fired. The mean follows the records read,
// smoothed, and moves the trigger by whole codes only (acZero), each arm
// planning it again when it moved.

// acMean is the channels' smoothed mean codes of the records read, and the
// whole code an AC-coupled channel's scale is zeroed at (0: none yet).
// OWNER.
type acMean struct {
	mean [2]float64
	zero [2]float64
}

// acMeanSamples bounds the samples a record's mean reads (strided).
const acMeanSamples = 65536

// noteMeans folds frame f's channel means into the smoothed means, both
// channels whatever their coupling, so a switch to AC starts from a known
// mean; the zero an AC channel's scale uses moves once the mean is a whole
// code away. OWNER.
// TRLC-LINKS: REQ-SDS-011, REQ-SDS-096
func (b *Board) noteMeans(f *sample.Frame) {
	a := &b.acMean
	for i := range f.Traces {
		t := &f.Traces[i]
		ch := t.Ch & 1
		n := min(f.Len(), len(t.Codes8))
		if n <= 0 {
			continue
		}
		step := max(1, n/acMeanSamples)
		sum, cnt := 0, 0
		for k := 0; k < n; k += step {
			sum += int(t.Codes8[k])
			cnt++
		}
		mean := float64(sum) / float64(cnt)
		if old := a.mean[ch]; old > 0 {
			mean = old + .25*(mean-old)
		}
		a.mean[ch] = mean
		if a.zero[ch] == 0 || math.Abs(mean-a.zero[ch]) >= 1 {
			a.zero[ch] = math.Round(mean)
		}
	}
}

// acScale is channel ch's scale when it is AC-coupled and its mean known:
// codes relative to the mean, no offset.
// TRLC-LINKS: REQ-SDS-011, REQ-SDS-096
func (b *Board) acScale(ch int, fe hal.Channel) (sample.Scale, bool) {
	if fe.Coupling != hal.AC || b.acMean.zero[ch&1] <= 0 {
		return sample.Scale{}, false
	}
	return sample.Scale{Zero: b.acMean.zero[ch&1], PerCode: fe.VoltsDiv / CodesPerDiv}, true
}

// followACLevel plans the programmed trigger again when an AC-coupled
// channel's zero moved since it was planned (its level is codes above the
// channel's mean). OWNER, before an arm.
// TRLC-LINKS: REQ-SDS-011, REQ-SDS-096
func (b *Board) followACLevel() {
	k := &b.trig.kept
	if !k.ok || b.frontEnd == nil {
		return
	}
	if b.frontEnd.pending(0).Coupling != hal.AC && b.frontEnd.pending(1).Coupling != hal.AC {
		return
	}
	if in := b.trigInputs(); in == k.in {
		return
	}
	spec := k.spec
	if err := (*triggerFacet)(b).Program(spec); err != nil && b.log != nil {
		b.log("trigger: the AC level at the channel's mean: %v", err)
	}
}
