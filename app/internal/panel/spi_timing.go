// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"math"

	"open-sds/app/internal/decode"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/framecalc"
	"open-sds/app/internal/panel/wsview"
)

// SPI's data delay row: where the data line is read after the clock's
// sampling edge, from 8 ns before to 9 ns after (a flash read at 104 MHz
// settles 6 to 8 ns after the edge that launches it).
const (
	spiDelayMinNs = -8
	spiDelayMaxNs = 9
)

// spiDelayOf is the data delay in use: the eye's middle measured (Auto),
// none until a record has shown it, or the one set by hand; in sixteenths
// of a nanosecond, as the FPGA takes it.
// TRLC-LINKS: REQ-SDS-230
func spiDelayOf(auto bool, ns float64, eye decode.SPIEye) float64 {
	if auto {
		if eye.PeriodS > 0 {
			ns = eye.DelayS * 1e9
		} else {
			ns = 0
		}
	}
	return math.Round(clampF(ns, spiDelayMinNs, spiDelayMaxNs)*16) / 16
}

// spiDelayNs is the data delay in use (ns).
// TRLC-LINKS: REQ-SDS-230
func (s *sv) spiDelayNs() float64 { return spiDelayOf(s.decDelayAuto, s.decDelayNs, s.spiEye) }

// spiMarginNs is the timing margin at the delay in use as the FPGA takes it
// (its delay register clamped): the eye's half opening less the distance
// from its middle; ok false with no eye measured.
// TRLC-LINKS: REQ-SDS-230
func spiMarginNs(s *sv) (fpgaNs, marginNs float64, ok bool) {
	d := s.spiDelayNs()
	fpgaNs = engine.SPIDelayFPGA(d, s.decChA) // what the receiver on the samples reads at
	if s.spiEye.PeriodS <= 0 {
		return fpgaNs, 0, false
	}
	return fpgaNs, s.spiEye.MarginS*1e9 - math.Abs(fpgaNs-s.spiEye.DelayS*1e9), true
}

// spiDelayText is the data delay row's value: the delay, Auto's, and the
// margin left each side of it on the last record ("Auto +3.7 ns, margin
// +-2.1 ns"); the FPGA's own delay when its register cannot reach the one
// asked.
// TRLC-LINKS: REQ-SDS-230
func spiDelayText(s *sv) string {
	t := fmt.Sprintf("%+.1f ns", s.spiDelayNs())
	if s.decDelayAuto {
		t = "Auto " + t
		if s.spiEye.PeriodS <= 0 {
			return "Auto (no eye yet)"
		}
	}
	fpga, margin, ok := spiMarginNs(s)
	if math.Abs(fpga-s.spiDelayNs()) > 1.0/32 {
		t += fmt.Sprintf(" (FPGA %+.1f ns)", fpga)
	}
	if ok {
		t += fmt.Sprintf(", margin %+.1f ns", margin)
	}
	return t
}

// setSPIDelay sets the data delay by hand (ns).
// TRLC-LINKS: REQ-SDS-230
func (c *Controller) setSPIDelay(ns float64) error {
	c.mu.Lock()
	c.decDelayNs, c.decDelayAuto = clampF(ns, spiDelayMinNs, spiDelayMaxNs), false
	c.mu.Unlock()
	c.syncSerialBus()
	return nil
}

// setSPIDelayAuto takes the data delay from the eye measured on the records.
// TRLC-LINKS: REQ-SDS-230
func (c *Controller) setSPIDelayAuto() error {
	c.mu.Lock()
	c.decDelayAuto = true
	c.mu.Unlock()
	c.syncSerialBus()
	return nil
}

// setSPIClock sets the SPI clock rate the FPGA frames bytes by (0: measured).
// TRLC-LINKS: REQ-SDS-229
func (c *Controller) setSPIClock(hz int) error {
	se := c.serialTrig()
	if se == nil {
		return fmt.Errorf("no serial trigger in this engine")
	}
	p := se.SerialConfig()
	p.SPIClockHz = max(0, min(hz, 125_000_000))
	se.SetSerialParams(c.serialBus(p))
	return nil
}

// setDecFine asks for the FPGA's receivers on the samples at any rate.
// TRLC-LINKS: REQ-SDS-229
func (c *Controller) setDecFine(on bool) error {
	c.mu.Lock()
	c.decFine = on
	c.mu.Unlock()
	c.syncSerialBus()
	return nil
}

// noteSPIEye measures the data's eye on a full-rate SPI record (the
// converters' own 2 ns samples) and keeps it for the delay row; with the
// delay on Auto, a middle moved by more than a quarter nanosecond re-sends
// the bus to an armed trigger.
// TRLC-LINKS: REQ-SDS-230
func (c *Controller) noteSPIEye(s *sv, f *engine.Frame) {
	if s.decProto != framecalc.ProtoSPI || f == nil || f.SampleS <= 0 || f.SampleS > 2.05e-9 || f.PeakDetect || f.IsEnv {
		return
	}
	valid := framecalc.Valid(f)
	eye, ok := decode.MeasureSPIEye(framecalc.Channel(f, s.decChA, valid), framecalc.Channel(f, s.decChB, valid), f.SampleS,
		decode.SPICfg{CPOL: s.decCPOL, CPHA: s.decCPHA, MSB: !s.decLSB, DataLag: decode.SPIDataLag(s.decChA, f.SampleS)})
	if !ok {
		return
	}
	// the bursts' ends of the records since the bus changed decide whether
	// the middle lies after the edge or before it (a record shows a few)
	key := fmt.Sprint(s.decChA, s.decChB, s.decCPOL, s.decCPHA)
	c.mu.Lock()
	if key != c.spiEyeKey {
		c.spiEyeKey, c.spiEyeEnds, c.spiEyeLaunched = key, 0, 0
	}
	c.spiEyeEnds += eye.Ends
	c.spiEyeLaunched += eye.Launched
	eye = eye.Resolve(c.spiEyeEnds, c.spiEyeLaunched)
	moved := math.Abs(eye.DelayS-c.spiEye.DelayS) > 0.25e-9 || c.spiEye.PeriodS <= 0
	c.spiEye = eye
	auto := c.decDelayAuto
	c.mu.Unlock()
	if auto && moved {
		go c.syncSerialBus()
	}
}

// spiTimingRows are BUS's SPI timing rows: the clock rate the FPGA frames
// bytes by, and the data delay with the record's eye.
// TRLC-LINKS: REQ-SDS-230
func spiTimingRows(isSPI func(*sv) bool) []*row {
	clk := numRow("bus.spi_clock", "Clock rate", "Hz", func(s *sv) float64 {
		if !s.hasSerial {
			return 0
		}
		return float64(s.serial.SPIClockHz)
	}, constLim(0, 125e6), constStep(0), func(c *Controller, v float64) error { return c.setSPIClock(int(math.Round(v))) }, siFmt("Hz"))
	clk.help = "SPI clock rate: the FPGA's receiver ends a byte after an idle gap of 1.5 clock periods. Auto measures the clock on the captures."
	clk.rel, clk.why = func(s *sv) bool { return isSPI(s) && s.hasSerial }, "Protocol = SPI"
	clk.ladder = func(*sv) []float64 { return spiClockLadder }
	clk.stepN = func(c *Controller, n int) error {
		v := clk.val(c.snap())
		i := nearestLadder(spiClockLadder, math.Max(v, spiClockLadder[0]))
		if v <= 0 && n < 0 {
			return nil
		}
		return c.setSPIClock(int(spiClockLadder[clampInt(i+n, 0, len(spiClockLadder)-1)]))
	}
	clk.text = func(s *sv) string {
		if v := clk.val(s); v > 0 {
			return si(v, "Hz")
		}
		if s.spiEye.PeriodS > 0 {
			return "Auto (" + si(1/s.spiEye.PeriodS, "Hz") + ")"
		}
		return "Auto"
	}
	clk.presets = func(*sv) []preset { return []preset{{t: "Auto", v: 0}} }
	clk.def = func(c *Controller) error { return c.setSPIClock(0) }
	delay := numRow("bus.spi_delay", "Data delay", "s", func(s *sv) float64 { return s.spiDelayNs() * 1e-9 },
		constLim(spiDelayMinNs*1e-9, spiDelayMaxNs*1e-9), constStep(0.25e-9), func(c *Controller, v float64) error { return c.setSPIDelay(v * 1e9) }, siFmt("s"))
	delay.help = "Where the data line is read after the clock's sampling edge: a device's data settles late (a flash read at 104 MHz: 6 to 8 ns after the edge that launches it). Auto reads it at the middle of the data's eye on the record; the margin is the time left each side."
	delay.knob = "Auto follows the record; a value set here stays."
	delay.rel, delay.why = isSPI, "Protocol = SPI"
	delay.text = spiDelayText
	delay.presets = func(*sv) []preset {
		return []preset{{t: "Auto", run: (*Controller).setSPIDelayAuto}, {t: "At edge", v: 0}}
	}
	delay.def = func(c *Controller) error { return c.setSPIDelay(0) }
	return []*row{clk, delay}
}

// spiClockLadder is the clock rates the clock row steps through.
var spiClockLadder = []float64{100e3, 400e3, 1e6, 2e6, 5e6, 10e6, 20e6, 25e6, 33e6, 50e6, 66e6, 75e6, 80e6, 100e6, 104e6, 125e6}

// receiverRow is BUS's choice of FPGA receiver for SPI, UART and I2C.
// TRLC-LINKS: REQ-SDS-229
func receiverRow() *row {
	return &row{id: "bus.receiver", label: "Receiver", kind: wsview.Enum,
		help: "The FPGA receiver that triggers and streams SPI, UART and I2C. Auto: the general image's receiver on 125 MHz line levels where it reads the bus, else the one on the samples (the fast image: sub-nanosecond edges, SPI to 125 MHz, UART to 62.5 MBd, I2C high-speed mode). Samples: that one at any rate (an image load of a few seconds).",
		rel: func(s *sv) bool {
			return s.hasSerial && (s.decProto == framecalc.ProtoUART || s.decProto == framecalc.ProtoI2C || s.decProto == framecalc.ProtoSPI)
		},
		why:  "Protocol = UART, I2C or SPI",
		opts: staticOpts([]opt{{0, "Auto"}, {1, "Samples"}}),
		get:  func(s *sv) int { return b2ic(s.decFine) },
		set:  func(c *Controller, v int) error { return c.setDecFine(v == 1) },
		def:  func(c *Controller) error { return c.setDecFine(false) },
	}
}
