// ENGMODEL-OWNER-UNIT: FU-APP-APP

package app

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/labeth/open-dso-core/engine/fanout"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-dso-core/scopeapp"
	"github.com/labeth/open-dso-core/uimodel/panel"
	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
)

// scopeSource is the HAL scope's engine (the instrument model's controls
// with the owner's parts) with the frames from the fan-out (the cycle's
// one consumer slot belongs to the fan-out alone), as SCPI and the web
// take it. The engine's optional parts they look for are forwarded.
// TRLC-LINKS: REQ-SDS-167
type scopeSource struct {
	panel.Engine
	fo *fanout.Fanout[sample.Frame]
}

// TRLC-LINKS: REQ-SDS-167
func (s scopeSource) WithFrame(fn func(*sample.Frame)) { s.fo.WithFrame(fn) }

// WaitNextFrame lets the web layer park a long-poll until the fan-out
// snapshots a frame newer than last.
// TRLC-LINKS: REQ-SDS-167
func (s scopeSource) WaitNextFrame(last uint64, timeout time.Duration) uint64 {
	return s.fo.WaitNext(last, timeout)
}

// SincePublish is the time since the cycle published a frame (the waiting
// notice, SCPI's trigger state).
// TRLC-LINKS: REQ-SDS-167, REQ-SDS-168
func (s scopeSource) SincePublish() time.Duration {
	if x, ok := s.Engine.(interface{ SincePublish() time.Duration }); ok {
		return x.SincePublish()
	}
	return 0
}

// ForceTrigger forces a capture (FORCE, SCPI's FRTR).
// TRLC-LINKS: REQ-SDS-167, REQ-SDS-011
func (s scopeSource) ForceTrigger() {
	if x, ok := s.Engine.(interface{ ForceTrigger() }); ok {
		x.ForceTrigger()
	}
}

// webEngine is the HAL scope's engine as the web scope takes it
// (webscope.Engine): the instrument model's controls and the owner's
// parts, the cycle's quiet lock, the board's bus errors and lamp lag in
// the status, and the web's command notes. The fabric's records publish as
// they complete, with no frame period, and decoded streams run as the
// board's sessions, never the stitched stream mode: those controls answer
// as the SRAM engine answered them.
type webEngine struct {
	scopeSource
	board     *acq.Board
	quiet     scopeapp.QuietPacer // nil: no quiet lock
	ledLag    *atomic.Int64       // the lamp latch's last lag, ns
	lcdFrames *atomic.Uint64      // the pictures the display put up
	mask      maskFails           // nil: no software mask test

	mu   sync.Mutex
	cmds [cmdNotes]engine.CmdNote
	head int
}

// maskFails is the software mask test's failure ring.
type maskFails interface {
	MaskFails() []engine.MaskFail
}

// cmdNotes is how many web commands the status keeps, as the engine kept
// them.
const cmdNotes = 64

// Snapshot is the HAL scope's state with the board's bus errors, the lamp
// latch's lag and the pictures the display put up.
// TRLC-LINKS: REQ-SDS-164, REQ-SDS-127
func (w *webEngine) Snapshot() engine.Stats {
	st := w.Engine.Snapshot()
	st.BusErrors = w.board.BusErrors()
	st.LEDLagMs = float64(w.ledLag.Load()) / float64(time.Millisecond)
	st.LCDFrames = w.lcdFrames.Load()
	return st
}

// QuietRLock holds off the cycle's load-sensitive windows while a reply is
// serialised.
// TRLC-LINKS: REQ-SDS-163
func (w *webEngine) QuietRLock() {
	if w.quiet != nil {
		w.quiet.QuietRLock()
	}
}

// TRLC-LINKS: REQ-SDS-163
func (w *webEngine) QuietRUnlock() {
	if w.quiet != nil {
		w.quiet.QuietRUnlock()
	}
}

// SetFramePeriod is 0 whatever asked: the fabric publishes each capture as
// it completes.
// TRLC-LINKS: REQ-SDS-162
func (w *webEngine) SetFramePeriod(int) int { return 0 }

// SetStreamMode is never on: decoded streams are the board's sessions.
// TRLC-LINKS: REQ-SDS-162
func (w *webEngine) SetStreamMode(bool) bool { return false }

// SetZones stages the zones.
// TRLC-LINKS: REQ-SDS-014
func (w *webEngine) SetZones(z []engine.Zone) {
	if x, ok := w.Engine.(interface{ SetZones([]engine.Zone) }); ok {
		x.SetZones(z)
	}
}

// SetZoneMode arms or disarms the zones.
// TRLC-LINKS: REQ-SDS-014
func (w *webEngine) SetZoneMode(m int) {
	if x, ok := w.Engine.(interface{ SetZoneMode(int) }); ok {
		x.SetZoneMode(m)
	}
}

// MaskFails is the software mask test's failures.
// TRLC-LINKS: REQ-SDS-014
func (w *webEngine) MaskFails() []engine.MaskFail {
	if w.mask == nil {
		return nil
	}
	return w.mask.MaskFails()
}

// SetSerialParams stages the serial trigger's request.
// TRLC-LINKS: REQ-SDS-013
func (w *webEngine) SetSerialParams(p engine.SerialParams) {
	if x, ok := w.Engine.(interface{ SetSerialParams(engine.SerialParams) }); ok {
		x.SetSerialParams(p)
	}
}

// SetSerialMode arms or disarms the serial trigger.
// TRLC-LINKS: REQ-SDS-013
func (w *webEngine) SetSerialMode(m int) {
	if x, ok := w.Engine.(interface{ SetSerialMode(int) }); ok {
		x.SetSerialMode(m)
	}
}

// SerialConfig is the serial trigger's request as staged.
// TRLC-LINKS: REQ-SDS-013
func (w *webEngine) SerialConfig() engine.SerialParams {
	if x, ok := w.Engine.(interface{ SerialConfig() engine.SerialParams }); ok {
		return x.SerialConfig()
	}
	return engine.SerialParams{}
}

// SetBodeMode arms the frequency-response sweep.
// TRLC-LINKS: REQ-SDS-066
func (w *webEngine) SetBodeMode(on bool, refCh, dutCh int) {
	if x, ok := w.Engine.(interface{ SetBodeMode(bool, int, int) }); ok {
		x.SetBodeMode(on, refCh, dutCh)
	}
}

// ClearBode empties the sweep's curve.
// TRLC-LINKS: REQ-SDS-066
func (w *webEngine) ClearBode() {
	if x, ok := w.Engine.(interface{ ClearBode() }); ok {
		x.ClearBode()
	}
}

// BodePoints is the sweep's curve.
// TRLC-LINKS: REQ-SDS-066
func (w *webEngine) BodePoints() []engine.BodePoint {
	if x, ok := w.Engine.(interface{ BodePoints() []engine.BodePoint }); ok {
		return x.BodePoints()
	}
	return nil
}

// AcqLog is the capture log, which the SRAM fabric's captures never
// wrote: none, at no half-rate share.
// TRLC-LINKS: REQ-SDS-127
func (w *webEngine) AcqLog(int) ([]engine.AcqSample, float64) { return []engine.AcqSample{}, 0 }

// NoteCmd keeps a web control's name and value with the published frame's
// sequence number at the time.
// TRLC-LINKS: REQ-SDS-127
func (w *webEngine) NoteCmd(name string, val float64) {
	seq := w.Engine.Snapshot().Seq
	w.mu.Lock()
	w.cmds[w.head] = engine.CmdNote{Name: name, Val: val, Seq: seq}
	w.head = (w.head + 1) % len(w.cmds)
	w.mu.Unlock()
}

// CmdLog is the last n command notes, the most recent last.
// TRLC-LINKS: REQ-SDS-127
func (w *webEngine) CmdLog(n int) []engine.CmdNote {
	w.mu.Lock()
	defer w.mu.Unlock()
	avail := 0
	for i := range len(w.cmds) {
		if w.cmds[(w.head-1-i+len(w.cmds))%len(w.cmds)].Name == "" {
			break
		}
		avail++
	}
	n = min(max(n, 0), avail)
	out := make([]engine.CmdNote, n)
	for i := range n {
		out[i] = w.cmds[(w.head-n+i+len(w.cmds))%len(w.cmds)]
	}
	return out
}

// SetRollStream is the roll-stream preference (the board's).
// TRLC-LINKS: REQ-SDS-010
func (w *webEngine) SetRollStream(on bool) {
	if x, ok := w.Engine.(interface{ SetRollStream(bool) }); ok {
		x.SetRollStream(on)
	}
}

// SetPrecisionRate sets the precision words' rate and returns it.
// TRLC-LINKS: REQ-SDS-012
func (w *webEngine) SetPrecisionRate(hz float64) float64 {
	if x, ok := w.Engine.(interface{ SetPrecisionRate(float64) float64 }); ok {
		return x.SetPrecisionRate(hz)
	}
	return 0
}

// EndWebDecodedCapture ends a web page's decoded capture.
// TRLC-LINKS: REQ-SDS-013
func (w *webEngine) EndWebDecodedCapture(ctx context.Context) (bool, error) {
	if x, ok := w.Engine.(interface {
		EndWebDecodedCapture(context.Context) (bool, error)
	}); ok {
		return x.EndWebDecodedCapture(ctx)
	}
	return false, nil
}

// LeaseDecodeView, LeaseSpectrumView, LeaseSampleView, LeaseSearchView,
// LeaseBusSamples and LeaseViewWindow are a page's leases on the records
// the cycle reads, the HAL scope's own.
// TRLC-LINKS: REQ-SDS-163
func (w *webEngine) LeaseDecodeView(d time.Duration) {
	if x, ok := w.Engine.(interface{ LeaseDecodeView(time.Duration) }); ok {
		x.LeaseDecodeView(d)
	}
}

// TRLC-LINKS: REQ-SDS-163
func (w *webEngine) LeaseSpectrumView(d time.Duration) {
	if x, ok := w.Engine.(interface{ LeaseSpectrumView(time.Duration) }); ok {
		x.LeaseSpectrumView(d)
	}
}

// TRLC-LINKS: REQ-SDS-163
func (w *webEngine) LeaseSampleView(d time.Duration) {
	if x, ok := w.Engine.(interface{ LeaseSampleView(time.Duration) }); ok {
		x.LeaseSampleView(d)
	}
}

// TRLC-LINKS: REQ-SDS-163
func (w *webEngine) LeaseSearchView(d time.Duration) {
	if x, ok := w.Engine.(interface{ LeaseSearchView(time.Duration) }); ok {
		x.LeaseSearchView(d)
	}
}

// TRLC-LINKS: REQ-SDS-163
func (w *webEngine) LeaseBusSamples(sampleS float64, d time.Duration) {
	if x, ok := w.Engine.(interface{ LeaseBusSamples(float64, time.Duration) }); ok {
		x.LeaseBusSamples(sampleS, d)
	}
}

// TRLC-LINKS: REQ-SDS-163
func (w *webEngine) LeaseViewWindow(sf engine.ViewSurface, a, b float64, cols int, d time.Duration) {
	if x, ok := w.Engine.(interface {
		LeaseViewWindow(engine.ViewSurface, float64, float64, int, time.Duration)
	}); ok {
		x.LeaseViewWindow(sf, a, b, cols, d)
	}
}

// boardMatrix is the front panel's key matrix and lamp latch on the
// board's panel plane: the matrix rebuilt from the panel scan (any
// goroutine), the lamp word staged for the owner's next commit point.
type boardMatrix struct{ b *acq.Board }

// TRLC-LINKS: REQ-SDS-135
func (m boardMatrix) ReadMatrix() ([5]uint16, bool) { return m.b.ReadMatrix() }

// TRLC-LINKS: REQ-SDS-135
func (m boardMatrix) SetLEDs(word uint16) { m.b.StageLamps(word) }
