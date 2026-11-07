// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"testing"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
	"github.com/labeth/open-sds1000cml/app/internal/decodedlines"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// Every protocol that streams through the FPGA's decoded events stops "on
// error" in hardware: its stream's capture carries the decoder and a
// sequence whose one element is the error token, so the first error the
// decoder publishes once the record's history is in fires the trigger.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func TestDecodedStreamOnErrorPlansTheErrorToken(t *testing.T) {
	images := map[string]*Engine{}
	for name, c := range map[string]acq.Caps{
		"general": {UART: true, I2C: true, SPI: true, Sequence: true},
		"packet": {
			SENT: true, MIL1553: true, Sequence: true, SequenceOnly: true,
			Packet: 1<<core.SerCAN | 1<<core.SerARINC | 1<<core.SerFlexRay,
		},
		"line": {Manchester: true, USBLS: true, Sequence: true, SequenceOnly: true},
	} {
		e := &Engine{}
		e.hw().SetImageCaps(c)
		images[name] = e
	}
	for _, c := range []struct {
		image string
		p     SerialParams
	}{
		{"general", SerialParams{Proto: core.SerUART, Baud: 115200, Bits: 8, Parity: "none"}},
		{"general", SerialParams{Proto: core.SerMIDI, Baud: 31250}},
		{"general", SerialParams{Proto: core.SerI2C, ChA: 0, ChB: 1}},
		{"general", SerialParams{Proto: core.SerSPI, ChA: 0, ChB: 1, SPIClockHz: 1000000}},
		{"packet", SerialParams{Proto: core.SerCAN, Baud: 500000}},
		{"packet", SerialParams{Proto: core.SerARINC, Baud: 100000}},
		{"packet", SerialParams{Proto: core.SerFlexRay, Baud: 10000000}},
		{"packet", SerialParams{Proto: core.SerMIL1553, Baud: 1000000}},
		{"packet", SerialParams{Proto: core.SerSENT, TickNs: 3000, Nibbles: 6}},
		{"line", SerialParams{Proto: core.SerManchester, Baud: 1000000, Bits: 16}},
		{"line", SerialParams{Proto: core.SerUSB, Baud: 12000000}},
	} {
		e := images[c.image]
		e.arincAux.Store(0x8d72a15c)
		p := c.p
		p.Threshold, p.HaveThr, p.ThresholdB, p.HaveThrB = 128, true, 128, true
		p.Addr, p.RW = -1, 2
		p.Pattern = []PatternElem{{Kind: core.PatternError}}
		cfg, err := e.decodedStreamConfig(p, StreamOnError)
		if err != nil {
			t.Errorf("protocol %d on the %s image: %v", c.p.Proto, c.image, err)
			continue
		}
		if uint64(cfg.PreWords)*2 != decodedlines.HistorySamples {
			t.Errorf("protocol %d: a history of %d words, the transcript's window %d samples", c.p.Proto, cfg.PreWords, decodedlines.HistorySamples)
		}
		if cfg.Sequence.Length != 1 || cfg.Sequence.Elements[0].Kind != sramcapture.SequenceError || cfg.Sequence.Qualify || !cfg.Normal {
			t.Errorf("protocol %d (%s): sequence %+v", c.p.Proto, acq.HardwareSerialName(cfg), cfg.Sequence)
		}
	}
}
