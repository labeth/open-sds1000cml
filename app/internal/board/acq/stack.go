// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"context"
	"errors"
	"fmt"
	"math/bits"
	"time"

	"github.com/labeth/open-dso-core/superres"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// The stacking image accumulates many records' samples against a template
// (a level, a window, a qualifier) in the FPGA: hal.Stacker's record count
// alone cannot ask for it, so the board runs the session itself as LONG
// work on the stacking image (StackRecords), the template in the request.

// FPGAStack is a whole stacking session for LONG work the core schedules
// (the UI model's and the web's stacking session, panel.BoardStacker):
// req.Records raw records of the staged depth (req.RecordWords words when
// it names them) auto-triggered and stacked, as the engine's session does
// (StackSession). OWNER, LONG.
// TRLC-LINKS: REQ-SDS-141
func (b *Board) FPGAStack(ctx context.Context, req superres.StackRequest) (superres.StackResult, error) {
	st := b.Staged()
	// Raw samples at the current depth. Hits come from the stacker's own
	// level crossings, so records auto-trigger once prehistory exists.
	cfg := sramcapture.Config{Source: sramcapture.ADC, PreWords: st.PreWords, PostWords: st.PostWords}
	if w := req.RecordWords; w != 0 {
		w = min(max(w, 2), sramcapture.Words)
		cfg.PreWords, cfg.PostWords = w/2, w-w/2
	}
	return b.StackSession(ctx, cfg, req)
}

// StackSession is a whole stacking session of cfg's records: the fabric
// halted, the stacking image loaded, req.Records raw captures of cfg
// stacked, and the general image always loaded again before it returns.
// OWNER, LONG.
// TRLC-LINKS: REQ-SDS-141
func (b *Board) StackSession(ctx context.Context, cfg sramcapture.Config, req superres.StackRequest) (res superres.StackResult, err error) {
	if !b.HasImage(ImageStack) {
		return res, errors.New("FPGA stacking image not available in this build")
	}
	if req.Records < 1 || req.Records > 100000 {
		return res, fmt.Errorf("records %d out of range", req.Records)
	}
	halt, cancel := context.WithTimeout(context.Background(), haltTimeout)
	err = b.mem.Halt(halt)
	cancel()
	b.beat()
	if err != nil {
		return res, err
	}
	start := time.Now()
	defer func() {
		if restore := b.LoadImage(ImageGeneral); restore != nil {
			b.logf("FPGA stack: general image restore failed: %v", restore)
			if err == nil {
				err = fmt.Errorf("general image restore: %w", restore)
			}
		}
		b.beat()
		b.reprobe()
		b.beat()
	}()
	if err = b.LoadImage(ImageStack); err != nil {
		return res, fmt.Errorf("stacking image: %w", err)
	}
	loadS := time.Since(start).Seconds()
	b.beat()
	b.reprobe()
	if b.panel.bus != nil {
		if _, err = sramcapture.New(b.panel.bus); err != nil { // the fabric answers on the new image
			return res, err
		}
	}
	res, err = b.StackRecords(ctx, cfg, req)
	res.LoadS = loadS
	return res, err
}

// StackRecords stacks req.Records captures of cfg (raw, auto-triggered)
// in the FPGA against req's template, tile by tile, and returns the
// result; the stacking image must be loaded. OWNER, LONG.
// TRLC-LINKS: REQ-SDS-141
func (b *Board) StackRecords(ctx context.Context, cfg sramcapture.Config, req superres.StackRequest) (res superres.StackResult, err error) {
	tile, err := b.mem.StackBegin(req.Stack)
	if err != nil {
		return res, err
	}
	// Every tile must see the same frozen records and hit parity. Retain each
	// tile's moments on the ARM while another tile occupies the FPGA RAM.
	moments := make([]superres.FPGAStackMoments, 2*req.Stack.Bins)
	scratch := make([]superres.FPGAStackMoments, 2*tile)
	stackStart, beats := time.Now(), b.mem.Beats()
	var m sramcapture.Metadata
	for r := range req.Records {
		t0 := time.Now()
		if m, err = b.captureOne(ctx, cfg); err != nil {
			return res, fmt.Errorf("record %d: %w", r, err)
		}
		res.CaptureS += time.Since(t0).Seconds()
		var recordHits, recordCrossings, recordRejected uint32
		for first := 0; first < req.Stack.Bins; first += tile {
			n := min(tile, req.Stack.Bins-first)
			state := moments[2*first : 2*(first+n)]
			// Every scan starts from a zeroed tile and its moments are added on
			// the ARM: uploading the running state before each scan costs one bus
			// round trip a word, half the session (20 records take minutes with
			// the display frozen).
			if err = b.mem.StackResetTile(ctx); err != nil {
				return res, err
			}
			t0 = time.Now()
			hits, crossings, rejected, scanErr := b.mem.StackScan(ctx, first, n, res.Hits)
			if scanErr != nil {
				return res, fmt.Errorf("tile %d record %d: %w", first/tile, r, scanErr)
			}
			res.ScanS += time.Since(t0).Seconds()
			if first == 0 {
				recordHits, recordCrossings, recordRejected = hits, crossings, rejected
			} else if hits != recordHits || crossings != recordCrossings || rejected != recordRejected {
				return res, fmt.Errorf("stack tiles disagree on hits/crossings/rejections at record %d tile %d", r, first/tile)
			}
			t0 = time.Now()
			part := scratch[:len(state)]
			if err = b.mem.StackDownload(ctx, part); err != nil {
				return res, err
			}
			addTileMoments(state, part)
			res.DownloadS += time.Since(t0).Seconds()
			b.beat() // a tile of a record stacked
		}
		if req.Progress != nil {
			req.Progress(r+1, req.Records)
		}
		res.Hits = recordHits
		res.Crossings += uint64(recordCrossings)
		res.Rejected += uint64(recordRejected)
		res.RawWords += uint64(m.Length)
	}
	res.BusReads = b.mem.Beats() - beats
	res.StackS = time.Since(stackStart).Seconds()
	stack, err := superres.FPGAStack(moments, req.Stack.Factor, int(req.Stack.Channel), int(res.Hits), req.Records, 1/m.SampleRateHz)
	if err != nil {
		return res, err
	}
	res.Result, res.Stack = stack.Result(false, 1), stack
	res.Records = req.Records
	res.SampleS, res.Factor = 1/m.SampleRateHz, req.Stack.Factor
	return res, nil
}

// addTileMoments is superres.AddFPGAMoments over a tile, by pointer: a whole
// downloaded tile is folded in per record with the owner loop held.
// TRLC-LINKS: REQ-SDS-141
func addTileMoments(state, part []superres.FPGAStackMoments) {
	part = part[:len(state)]
	for i := range state {
		a, b := &state[i], &part[i]
		var c uint64
		a.Sum[0], c = bits.Add64(a.Sum[0], b.Sum[0], 0)
		a.Sum[1] += b.Sum[1] + c
		a.SumSquares[0], c = bits.Add64(a.SumSquares[0], b.SumSquares[0], 0)
		a.SumSquares[1] += b.SumSquares[1] + c
		a.SumOdd[0], c = bits.Add64(a.SumOdd[0], b.SumOdd[0], 0)
		a.SumOdd[1] += b.SumOdd[1] + c
		a.Count += b.Count
		a.CountOdd += b.CountOdd
	}
}

// captureOne arms an auto-triggered raw capture and waits until it is frozen.
// TRLC-LINKS: REQ-SDS-141
func (b *Board) captureOne(ctx context.Context, cfg sramcapture.Config) (sramcapture.Metadata, error) {
	arm, cancel := context.WithTimeout(ctx, 3*time.Second)
	err := b.mem.Arm(arm, cfg)
	cancel()
	if err != nil {
		return sramcapture.Metadata{}, err
	}
	wait, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	for {
		m, err := b.mem.Status()
		if err != nil {
			return m, err
		}
		if m.DataFault {
			return m, errors.New("SRAM acquisition FIFO overflow")
		}
		if m.Frozen && m.Ready {
			return m, nil
		}
		if err := wait.Err(); err != nil {
			halt, cancel := context.WithTimeout(context.Background(), time.Second)
			haltErr := b.mem.Halt(halt)
			cancel()
			return m, errors.Join(err, haltErr)
		}
		b.pause(time.Millisecond)
	}
}
