// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"errors"
	"fmt"
	"time"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
)

// probeSerialHardware records the loaded image's hardware decoders. It runs
// at start and after every image switch.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) probeSerialHardware() {
	e.hw().ProbeImage(e.logf)
}

// caps is the loaded image's capabilities, as the board probed them:
// immutable while the owner loop runs on the image.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) caps() acq.Caps { return e.hw().ImageCaps() }

// hasImage reports an embedded protocol image.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) hasImage(name string) bool {
	switch name {
	case acq.ImagePacket:
		return e.images.HasPacket()
	case acq.ImageLine:
		return e.images.HasLine()
	case acq.ImageFrame:
		return e.images.HasFrame()
	case acq.ImageStream:
		return e.images.HasStream()
	case acq.ImageBits:
		b, ok := e.images.(BitsImages)
		return ok && b.HasBits()
	case acq.ImageQualify:
		return e.images.HasQualify()
	case acq.ImageFast:
		return e.images.HasFast()
	}
	return true
}

// wantImage: a hardware-plannable Manchester, CAN, ARINC or FlexRay trigger,
// or SPI past the level receiver, needs its protocol image; everything else
// runs on the general image, which also keeps precision decimation.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) wantImage() string {
	if e.images == nil {
		return ""
	}
	// The audio stream runs on the stream image while it is leased or still
	// streaming: a trigger's image would replace the fabric under the stream.
	if (e.soundWanted() || e.soundStreaming()) && e.images.HasStream() {
		return acq.ImageStream
	}
	// Roll streaming (opt-in) runs gap-free on the stream image; a stop keeps
	// it, so RUN resumes without a reload.
	if e.rollStream.Load() && e.images.HasStream() && (e.rollActive() || (!e.running.Load() && e.protocolImage == acq.ImageStream)) {
		return acq.ImageStream
	}
	if e.serialMode.Load() != SerialTrigger {
		// A pulse, slope or video trigger runs on the qualifier image.
		e.mu.Lock()
		typ := e.tp.Typ
		e.mu.Unlock()
		if (typ != TrigEdge || e.zonesOn()) && e.images.HasQualify() { // and the zone trigger
			return acq.ImageQualify
		}
		// The qualifier image captures and triggers on an edge as the general
		// one does (precision decimation too); held, an edge stays on it.
		if e.holdQualify.Load() && e.protocolImage == acq.ImageQualify {
			return acq.ImageQualify
		}
		return ""
	}
	e.ser.Mu.Lock()
	p := e.ser.Params
	e.ser.Mu.Unlock()
	// The request as the matcher takes it: a threshold the engine learned
	// (a request from the scope names none) makes it plannable.
	p, why := e.serialPlanParams(p)
	// An SPI clock measured again stays on the image it ran on: the fast
	// image's captures measure it as well, without two loads.
	if why == core.SPIMeasuring && e.protocolImage == acq.ImageFast {
		return acq.ImageFast
	}
	// (protocolImage plans SPI itself: its clock, not a baud.)
	if name := acq.ProtocolImage(p); name != "" && (name == acq.ImageFast || acq.SerialThresholdOK(&p)) && e.hasImage(name) {
		return name
	}
	return ""
}

// HoldQualifyImage keeps a loaded qualifier image while the trigger is an
// edge, until released: AUTO in BUS measures and detects edge-triggered and
// ends on a UART's idle qualifier, and the general image between cost two
// loads of about four seconds. The image's UART, I2C and SPI receivers are
// the general image's only; a serial trigger or a stream still loads it.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-223
func (e *Engine) HoldQualifyImage(on bool) { e.holdQualify.Store(on) }

// serviceImage loads the image the serial trigger needs, between
// acquisitions. It reports whether the fabric was replaced. A failed load
// returns to the general image and is not retried until the choice changes.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-005
func (e *Engine) serviceImage() bool {
	want := e.wantImage()
	if want != e.failedImage {
		e.failedImage = ""
	}
	if want == e.protocolImage || (want != "" && want == e.failedImage) {
		return false
	}
	return e.loadImage(want)
}

// imageLoader is the switcher's load of a protocol image, or of the general
// image for "": the image alone (the engine probes, scans and brings the
// converters up itself after it).
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-005
func (e *Engine) imageLoader(name string) func() error {
	return func() error { return e.hw().LoadImage(name) }
}

// loadFeature loads the image with feature f alone.
// TRLC-LINKS: REQ-SDS-005
func (e *Engine) loadFeature(f string) error {
	switch f {
	case "general":
		return e.hw().LoadImage(acq.ImageGeneral)
	case "stack":
		return e.hw().LoadImage(acq.ImageStack)
	}
	return fmt.Errorf("no image has %s", f)
}

// beatWhile keeps the health beat going for the seconds an image load takes
// without bus reads, so the OTA agent does not judge the app hung and
// restart it; the returned func stops it.
// TRLC-LINKS: REQ-SDS-005, REQ-SDS-025
func (e *Engine) beatWhile() func() {
	beating := make(chan struct{})
	go func() {
		tick := time.NewTicker(200 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-beating:
				return
			case <-tick.C:
				e.beatN.Add(1)
			}
		}
	}()
	return func() { close(beating) }
}

// loadImage switches to a protocol image, or the general image for "", and
// brings the converters up on it (startConverters), as at start.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-005, REQ-SDS-231
func (e *Engine) loadImage(name string) bool {
	load := e.imageLoader(name)
	shown := name
	if shown == "" {
		shown = acq.ImageGeneralName
	}
	e.logf("engine: loading the %s image", shown)
	e.setBusy("loading "+shown+" image", 0) // the loading indicator covers the reload
	e.noteBackendsLoading(name)
	defer e.beatWhile()()
	t0 := time.Now()
	err := load()
	tLoad := time.Now()
	if err != nil {
		e.logf("engine: %s image: %v", shown, err)
		if name != "" {
			e.failedImage = name
			if restore := e.loadFeature("general"); restore != nil {
				e.busErr(restore)
			}
		} else {
			e.busErr(err)
		}
		name, shown = "", acq.ImageGeneralName
	}
	e.protocolImage = name
	e.probeSerialHardware()
	if err := e.applyPanelScan(); err != nil {
		e.logf("engine: panel scan: %v", err)
	}
	tProbe := time.Now()
	e.mu.Lock()
	e.stats.Image = shown
	e.mu.Unlock()
	e.startConverters("loading the " + shown + " image")
	e.logf("engine: the %s image in %v (configure %v, probes %v, converters %v)", shown, time.Since(t0).Round(time.Millisecond),
		tLoad.Sub(t0).Round(time.Millisecond), tProbe.Sub(tLoad).Round(time.Millisecond), time.Since(tProbe).Round(time.Millisecond))
	return true
}

// reloadImage configures the loaded image again (a converter recovery):
// the same load as a switch, with the hardware probes after it.
// TRLC-LINKS: REQ-SDS-231, REQ-SDS-005
func (e *Engine) reloadImage() error {
	if e.images == nil {
		return errors.New("no image switcher")
	}
	shown := e.protocolImage
	if shown == "" {
		shown = acq.ImageGeneralName
	}
	e.setBusy("reloading "+shown+" image", 0)
	defer e.beatWhile()()
	if err := e.imageLoader(e.protocolImage)(); err != nil {
		if e.protocolImage == "" {
			return err
		}
		// as a failed switch: back to the general image
		e.failedImage, e.protocolImage = e.protocolImage, ""
		if restore := e.loadFeature("general"); restore != nil {
			return restore
		}
		e.mu.Lock()
		e.stats.Image = acq.ImageGeneralName
		e.mu.Unlock()
	}
	e.probeSerialHardware()
	if err := e.applyPanelScan(); err != nil {
		e.logf("engine: panel scan: %v", err)
	}
	return nil
}

// noteARINCLevels takes the ARINC slicer levels from a sampled record the
// first time the current decode parameters need them; hardware ARINC
// triggering and streaming wait for them.
// TRLC-LINKS: REQ-SDS-018
func (e *Engine) noteARINCLevels(f *sample.Frame) {
	if e.arincAux.Load() != 0 {
		return
	}
	e.ser.Mu.Lock()
	p := e.ser.Params
	e.ser.Mu.Unlock()
	// Plannable once a threshold is known (the scope's request learns it);
	// the slicer levels are the decoder's own from a sampled record. A live
	// envelope's (min, max) pairs give wrong ones, kept until the request
	// changes, and the matcher never fires: the levels wait for a sampled
	// record, which a short lease asks for - a decimated one where it resolves
	// the bits, read in milliseconds where the full-rate record of 200us/div
	// takes seconds (and a decimated envelope asked for none: the trigger
	// waited for a record something else asked for).
	if planned, _ := e.serialPlanParams(p); p.Proto != core.SerARINC || !acq.SerialThresholdOK(&planned) || f.Len() < 8 {
		return
	}
	resolves := uint32(f.AcqDecim) <= 1 || p.Baud > 0 && f.SampleS > 0 && f.SampleS*acq.ArincBitSamples <= 1/float64(p.Baud) //nolint:gosec // G115: AcqDecim holds the fabric's 32-bit decimation word
	switch {
	case (f.Content == sample.PeakPairs || f.Flags.Has(sample.Envelope)) && p.Baud > 0:
		e.lease(&e.sampleLease, time.Second)
		return
	case f.Content == sample.PeakPairs || f.Flags.Has(sample.Envelope) || !resolves:
		e.lease(&e.decodeLease, time.Second)
		return
	}
	codes := f.Codes8(0)[:f.Len()]
	if p.ChA == 1 {
		codes = f.Codes8(1)[:f.Len()]
	}
	e.arincAux.Store(acq.ArincLevels(codes, p))
}

// noteBackendsLoading says, for the seconds an image load takes, that the
// pulse, slope or video trigger and the zones wait for it: their status is
// otherwise set by the next capture (the TRIG tab would name nothing during a
// load).
// TRLC-LINKS: REQ-SDS-011, REQ-SDS-014
func (e *Engine) noteBackendsLoading(image string) {
	shown := map[string]string{acq.ImageQualify: "qualifier", acq.ImagePacket: "packet", acq.ImageLine: "line", acq.ImageStream: "stream", acq.ImageBits: "digital stream", "": acq.ImageGeneralName}[image]
	why := "none: loading the " + shown + " FPGA image"
	zones := e.zonesOn()
	serial := e.serialMode.Load() == SerialTrigger
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.tp.Typ != TrigEdge && !serial {
		e.stats.TriggerBackend = why
	}
	if zones && !serial {
		e.stats.ZoneBackend = why
	}
}
