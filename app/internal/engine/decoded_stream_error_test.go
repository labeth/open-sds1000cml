// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"testing"

	"open-sds/app/internal/decodedlines"
	"open-sds/app/internal/sramcapture"
)

// Every protocol that streams through the FPGA's decoded events stops "on
// error" in hardware: its stream's capture carries the decoder and a
// sequence whose one element is the error token, so the first error the
// decoder publishes once the record's history is in fires the trigger.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func TestDecodedStreamOnErrorPlansTheErrorToken(t *testing.T) {
	images := map[string]*Engine{
		"general": {hardwareUART: true, hardwareI2C: true, hardwareSPI: true, hardwareSequence: true},
		"packet": {hardwareSENT: true, hardwareMIL1553: true, hardwareSequence: true, sequenceOnly: true,
			hardwarePacket: 1<<serCAN | 1<<serARINC | 1<<serFlexRay},
		"line": {hardwareManchester: true, hardwareUSBLS: true, hardwareSequence: true, sequenceOnly: true},
	}
	for _, c := range []struct {
		image string
		p     SerialParams
	}{
		{"general", SerialParams{Proto: serUART, Baud: 115200, Bits: 8, Parity: "none"}},
		{"general", SerialParams{Proto: serMIDI, Baud: 31250}},
		{"general", SerialParams{Proto: serI2C, ChA: 0, ChB: 1}},
		{"general", SerialParams{Proto: serSPI, ChA: 0, ChB: 1, SPIClockHz: 1000000}},
		{"packet", SerialParams{Proto: serCAN, Baud: 500000}},
		{"packet", SerialParams{Proto: serARINC, Baud: 100000}},
		{"packet", SerialParams{Proto: serFlexRay, Baud: 10000000}},
		{"packet", SerialParams{Proto: serMIL1553, Baud: 1000000}},
		{"packet", SerialParams{Proto: serSENT, TickNs: 3000, Nibbles: 6}},
		{"line", SerialParams{Proto: serManchester, Baud: 1000000, Bits: 16}},
		{"line", SerialParams{Proto: serUSB, Baud: 12000000}},
	} {
		e := images[c.image]
		e.arincAux.Store(0x8d72a15c)
		p := c.p
		p.Threshold, p.HaveThr, p.ThresholdB, p.HaveThrB = 128, true, 128, true
		p.Addr, p.RW = -1, 2
		p.Pattern = []PatternElem{{Kind: patternError}}
		cfg, err := e.decodedStreamConfig(p, StreamOnError)
		if err != nil {
			t.Errorf("protocol %d on the %s image: %v", c.p.Proto, c.image, err)
			continue
		}
		if uint64(cfg.PreWords)*2 != decodedlines.HistorySamples {
			t.Errorf("protocol %d: a history of %d words, the transcript's window %d samples", c.p.Proto, cfg.PreWords, decodedlines.HistorySamples)
		}
		if cfg.Sequence.Length != 1 || cfg.Sequence.Elements[0].Kind != sramcapture.SequenceError || cfg.Sequence.Qualify || !cfg.Normal {
			t.Errorf("protocol %d (%s): sequence %+v", c.p.Proto, hardwareSerialName(cfg), cfg.Sequence)
		}
	}
}
