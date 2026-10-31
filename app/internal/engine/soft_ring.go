// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"context"
	"fmt"
	"math"
	"time"

	"open-sds/app/internal/sramcapture"
)

// The history ring (ADR-STREAM-RING). On a gap-free digital stream that
// stops on a value, the bits image keeps the SRAM's record running as a
// ring of precision (analog) words while the stream goes to the host on
// chip. When the software decoder reads the value - for a decoded stream
// the owner's rule lets software be the trigger - the ring is stopped and
// shown with its trigger on the stop frame, placed through the ring's time
// stamp: the ring's newest word's time on the stream's own clock. The
// symbol trigger's record of the value coming again stays the fallback.

const (
	// softRingSlackWords is the stream's slack without the SRAM: the
	// drum's 4096-word FIFO. The direct path publishes a bank from 512
	// words, so the host's banks, both held when it stops taking, add
	// nothing to count on.
	softRingSlackWords = 4096
	// softRingStallS is the longest pump gap the on-chip stream must ride
	// out: the worker's longest gaps on the instrument were 4 to 27 ms.
	softRingStallS = 30e-3
	// softRingSealS is a bank's longest wait before it is published (the
	// drum's age limit).
	softRingSealS = 16.8e-3
	// softRingDecodeS is the decode's own time on the instrument's core and
	// the freeze's round trip.
	softRingDecodeS = 50e-3
	// softRingMarginS is the ring kept beyond the frame and the host's
	// latency: room around the frame for the screen and the host's slower
	// moments.
	softRingMarginS = 150e-3
	// softRingMinLog is the finest ring: the stream image's decimation floor.
	softRingMinLog = 8
	// softRingArmLagMax is the most raw words the ring's first word may
	// follow the stream's start (the record's priming, the decimator's first
	// word): 4 ms.
	softRingArmLagMax = 1 << 20
	// softRingStampLag is the raw words the stamp's latch runs ahead of the
	// precision decimator's output on top of its filter delay (register
	// stages and the 100 MHz crossing): a few tens of nanoseconds, under a
	// tenth of the finest ring word.
	softRingStampLag = 0
)

// softRingSpan is the history a ring at decimation n holds, in seconds.
// TRLC-LINKS: REQ-SDS-035, REQ-SDS-240
func softRingSpan(n int) float64 { return float64(sramcapture.Words) * float64(uint64(1)<<n) * 2e-9 }

// softRingPlan is the history ring's decimation for a digital stream at
// decimation streamLog (feature, longest and quiet the protocol's timing,
// pushS the time between decodes): ok false when the stream's words come
// too fast for the on-chip slack to ride out a pump gap, or when no ring
// both covers the host's latency, the frame and the margin and keeps three
// ring words in the protocol's shortest feature.
// TRLC-LINKS: REQ-SDS-035, REQ-SDS-240
func softRingPlan(streamLog int, feature, longest, quiet, pushS float64) (n int, why string, ok bool) {
	rate := 500e6 / float64(uint64(1)<<streamLog)
	if softRingSlackWords/rate < softRingStallS {
		return 0, fmt.Sprintf("the stream's %.0fk words/s outrun the on-chip slack (%.0f ms)", rate/1e3, 1e3*softRingSlackWords/rate), false
	}
	need := softRingSealS + pushS + quiet + softRingDecodeS + longest + softRingMarginS
	for n = softRingMinLog; n <= 20; n++ {
		if softRingSpan(n) >= need {
			break
		}
	}
	if n > 20 {
		return 0, fmt.Sprintf("no ring holds %.1f s", need), false
	}
	if word := float64(uint64(1)<<n) * 2e-9; 3*word > feature {
		return 0, fmt.Sprintf("a ring holding %.2g s has %.2g us words, coarser than a third of the %.2g us feature", need, word*1e6, feature*1e6), false
	}
	return n, "", true
}

// softRingDelay is the samples the precision decimator's word at 2^n lags
// its input's centre: its CIC3 stages' N(R-1)/2, and the stamp's lag.
// TRLC-LINKS: REQ-SDS-035, REQ-SDS-240, REQ-SDS-040
func softRingDelay(n int) float64 {
	return 1.5*float64(uint64(1)<<n-1) + 2*softRingStampLag
}

// softRingWord is the word of a frozen ring m (decimation n, newest word
// stamped last raw words from the stream's start) that holds stream sample
// ordinal ord (2 ns, from the same start); ok false when the ring no
// longer holds it. The stamp wraps at 2^32 raw words (17 s): it is taken
// as the first count at or after ord's raw word with those low bits - the
// ring stops seconds after the frame, never 17.
// TRLC-LINKS: REQ-SDS-035, REQ-SDS-240
func softRingWord(m sramcapture.Metadata, n int, last uint32, ord uint64) (uint32, bool) {
	if m.Length == 0 {
		return 0, false
	}
	raw := ord / 2
	newest := raw + uint64(last-uint32(raw)) // unwrapped: at or after ord's raw word
	centre := 2*float64(newest) - softRingDelay(n)
	k := math.Round((centre - float64(ord)) / float64(uint64(1)<<n))
	if k < 0 {
		k = 0 // after the newest word's centre: the frame begins in the newest word
	}
	if k >= float64(m.Length) {
		return 0, false
	}
	return m.Length - 1 - uint32(k), true
}

// softRingPace checks a frozen ring that has not yet lapped against its
// stamp: its words, at 2^(n-1) raw words each, must span the time since the
// stream's start less at most the ring's arm lag; a ring that took more or
// fewer words than its time is not placed by the stamp. A lapped ring has
// no start to check against.
// TRLC-LINKS: REQ-SDS-035, REQ-SDS-240
func softRingPace(m sramcapture.Metadata, n int, last uint32) error {
	if m.Length >= sramcapture.Words {
		return nil
	}
	span := uint32(uint64(m.Length) << (n - 1))
	if lag := last - span; lag > softRingArmLagMax {
		return fmt.Errorf("the ring's %d words span %d raw words, the stream's start was %d ago", m.Length, span, last)
	}
	return nil
}

// softRingRecord is the record a frozen ring shows for the stream's stop
// frame: the ring with its trigger on the frame's start; an error when the
// ring no longer holds the frame or does not keep the stream's time.
// TRLC-LINKS: REQ-SDS-035, REQ-SDS-240
func (s *softStream) softRingRecord(m sramcapture.Metadata, last uint32) (sramcapture.Metadata, error) {
	if err := softRingPace(m, int(s.ringLog), last); err != nil {
		return m, err
	}
	// The stop frame's ordinal on this stream session's clock: the decoder's
	// indices run on across a lost stream, the FPGA's stamp starts afresh.
	at := s.stopFirst - uint64(float64(s.sessBase)*s.ordStep)
	w, ok := softRingWord(m, int(s.ringLog), last, at)
	if !ok {
		return m, fmt.Errorf("the history ring (%.2g s) no longer holds the frame", softRingSpan(int(s.ringLog)))
	}
	m.TriggerIndex, m.Triggered, m.HostTrigger = w, true, true
	return m, nil
}

// softRingStop stops the history ring on the stop frame and ends the
// stream: the ring becomes the record shown, or, when it is not to be had,
// the symbol trigger's record of the value coming again is armed.
// TRLC-LINKS: REQ-SDS-035, REQ-SDS-240
func (e *Engine) softRingStop(s *softStream) {
	// The ring first, while the worker still drains the stream: a stream
	// left undrained overflows the drum's FIFO within 50 ms, and its fault
	// stops the acquisition.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	m, last, err := e.sram.FreezeRing(ctx)
	cancel()
	e.softStreamStop(s)
	// What places the record: the ring's geometry and pointer, its stamp,
	// the frame's ordinal on the session's clock and what the host received.
	e.logf("engine: history ring frozen: words=%d start=%d origin=%d position=%d (%d past the origin) stamp=%d (%.1f ms) frame=%d session=%d words received=%d after %v",
		m.Length, m.Start, m.Origin, m.Position, (m.Position-m.Origin)&(sramcapture.Words-1), last, float64(last)*4e-6,
		s.stopFirst, uint64(float64(s.sessBase)*s.ordStep), s.expected, e.clk.Now().Sub(s.sessAt).Round(time.Millisecond))
	s.softRingDone(m, last, err)
	if s.recOK {
		e.logf("engine: %s stream stopped on %X: history ring of %d words, trigger word %d", s.decoder.Name, s.value, s.recMeta.Length, s.recMeta.TriggerIndex)
	} else {
		e.logf("engine: %s stream stopped on %X: %v; the symbol trigger's record instead", s.decoder.Name, s.value, s.ringErr)
	}
}

// softRingDone takes the frozen ring (or the freeze's error): the record
// shown, or the symbol trigger's record armed next.
// TRLC-LINKS: REQ-SDS-035, REQ-SDS-240
func (s *softStream) softRingDone(m sramcapture.Metadata, last uint32, err error) {
	if err == nil {
		m, err = s.softRingRecord(m, last)
	}
	if err != nil {
		s.ringErr, s.phase = err, softRecord
		return
	}
	s.recCfg, s.recMeta, s.recOK, s.phase = s.ringCfg, m, true, softDone
}

// startFabric starts the stream's fabric: the history ring at its own
// decimation beside the stream (ring, when planned) or the stream alone
// (plain). A ring that does not start - an image without it, or any other
// refusal - leaves the stream to run as it would without one, and its stop
// to the symbol trigger's record: once, never tried again.
// TRLC-LINKS: REQ-SDS-035, REQ-SDS-240
func (s *softStream) startFabric(cfg sramcapture.Config, ring, plain func(sramcapture.Config) error, logf func(string, ...any)) error {
	if s.ring {
		c := cfg
		c.DecimationLog2 = s.ringLog
		err := ring(c)
		if err == nil {
			s.ringCfg = c
			return nil
		}
		s.ring, s.ringWhy = false, err.Error()
		logf("engine: %s stream: the history ring did not start (%v): the stop's record is the symbol trigger's", s.decoder.Name, err)
	}
	return plain(cfg)
}
