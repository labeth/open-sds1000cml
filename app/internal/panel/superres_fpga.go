// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"context"
	"fmt"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/sramcapture"
	"open-sds/app/internal/superres"
)

// srStopFPGA is the fourth Stop-on mode: stack Target records in the FPGA
// stacking image (ADR-STACKING-IMAGE-SPLIT) instead of software stacking.
const srStopFPGA = 3

// srStopFPGAMatch additionally qualifies hits with the shape around the
// trigger in the current frame (ADR-STACKING-TEMPLATE-QUALIFIER).
const srStopFPGAMatch = 4

// fpgaMatchPoints is the template length taken from the current frame.
const fpgaMatchPoints = 64

// FPGA window: the stack spans fpgaWindow samples centred on each edge-locked
// hit, on the Grid ×K fine grid. Records are bounded to fpgaRecordWords because
// the FPGA rescans each record once per 32-bin tile.
const (
	fpgaWindow      = 32
	fpgaRecordWords = 65536
)

type fpgaStacker interface {
	FPGAStack(context.Context, engine.FPGAStackRequest) (engine.FPGAStackResult, error)
}

// fpgaLevel picks the crossing level and hysteresis from the align channel of
// the current frame: mid-scale between its extremes, an eighth of the swing.
// TRLC-LINKS: REQ-SDS-141
func fpgaLevel(sig []uint8) (level, hysteresis uint8, ok bool) {
	if len(sig) < 8 {
		return 0, 0, false
	}
	lo, hi := sig[0], sig[0]
	for _, v := range sig {
		lo, hi = min(lo, v), max(hi, v)
	}
	if hi-lo < 16 {
		return 0, 0, false
	}
	return uint8((int(lo) + int(hi)) / 2), max(uint8((hi-lo)/8), 2), true
}

// srFPGARun starts an FPGA stacking session from the current settings and
// shows the result in the super-res review when it completes.
// TRLC-LINKS: REQ-SDS-141
func (c *Controller) srFPGARun() {
	fs, ok := c.eng.(fpgaStacker)
	if !ok || c.frameFn == nil {
		c.srSetStatus("FPGA stacking unavailable")
		return
	}
	c.mu.Lock()
	k, ch, records, busy, match := c.srK, c.srCh, int(c.srStopVal+0.5), c.srFPGABusy, c.srStopMode == srStopFPGAMatch
	c.mu.Unlock()
	if busy {
		return
	}
	var sig []uint8
	var fine []float64
	var sampleS, edgeX float64
	c.frameFn(func(f *engine.Frame) {
		if f == nil || f.IsEnv || f.Valid < 8 || f.Valid > len(f.C1) {
			return
		}
		src, q := f.C1, f.Q1
		if ch == 1 && len(f.C2) >= f.Valid {
			src, q = f.C2, f.Q2
		}
		sig = append([]uint8(nil), src[:f.Valid]...)
		fine = make([]float64, f.Valid)
		for i := range fine {
			if len(q) >= f.Valid {
				fine[i] = float64(q[i]) / 256
			} else {
				fine[i] = float64(src[i])
			}
		}
		sampleS, edgeX = f.SampleS, f.EdgeX
	})
	level, hysteresis, ok := fpgaLevel(sig)
	if !ok {
		c.srSetStatus("FPGA: no signal swing on the channel")
		return
	}
	req := engine.FPGAStackRequest{Records: records, RecordWords: fpgaRecordWords, Stack: sramcapture.StackConfig{
		Channel: uint8(ch), Level: level, Hysteresis: hysteresis,
		PreSamples: fpgaWindow / 2, MinSeparation: fpgaWindow,
		Bins: fpgaWindow * k, Factor: k, ChannelMask: 3}}
	if match {
		tpl, err := superres.BuildFPGATemplate(fine, sampleS, edgeX, float64(level), false, fpgaMatchPoints, 0)
		if err != nil {
			c.srSetStatus("FPGA match: " + err.Error())
			return
		}
		req.Stack.Template = make([]int, len(tpl.Values))
		for i, v := range tpl.Values {
			req.Stack.Template[i] = int(v)
		}
		req.Stack.TemplateStride, req.Stack.TemplatePre, req.Stack.TemplateThreshold = tpl.Stride, tpl.Pre, tpl.Threshold
	}
	c.mu.Lock()
	if c.srStop != nil { // the software stacker yields to the FPGA session
		close(c.srStop)
		c.srStop = nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.srFPGACancel = cancel
	c.srFPGAGen++
	generation := c.srFPGAGen
	c.srActive, c.srFPGABusy, c.srFocus = true, true, 0
	c.srStatus = fmt.Sprintf("FPGA: stacking %d records...", records)
	c.mu.Unlock()
	go func() {
		defer cancel()
		res, err := fs.FPGAStack(ctx, req)
		c.mu.Lock()
		defer c.mu.Unlock()
		c.srFPGABusy = false
		c.srFPGACancel = nil
		if generation != c.srFPGAGen {
			return
		}
		if err != nil {
			c.srStatus = "FPGA: " + err.Error()
			return
		}
		mean, mean2 := res.Result.Mean, res.Result.Mean2
		c.srStack, c.srMean, c.srMean2 = res.Stack, mean, mean2
		c.srWinLo, c.srWinHi, c.srPeriod = 0, res.Stack.N, 0
		c.srBits, c.srFrames, c.srRejected, c.srFocus = res.Result.BitsGained, int(res.Hits), 0, 3
		reduction := 0.0
		if res.BusReads > 0 {
			reduction = float64(res.RawWords*2) / float64(res.BusReads)
		}
		c.srStatus = fmt.Sprintf("FPGA %d rec %d hits +%.1fb bus/%.0f", res.Records, res.Hits, res.Result.BitsGained, reduction)
		if len(req.Stack.Template) > 0 {
			c.srStatus += fmt.Sprintf(" rej %d", res.Rejected)
		}
	}()
}
