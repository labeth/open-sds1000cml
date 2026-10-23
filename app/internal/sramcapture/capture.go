// Package sramcapture owns the full-depth acquisition probe's CS1 protocol.
// It does not load a fabric, open/close a device, or access CS3. The caller
// supplies the already inherited bus and must give this backend exclusive
// ownership while it is in use; its register ABI differs from default.v.
// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

const (
	Words             = uint32(1 << 19)
	Bytes             = Words * 4
	SamplesPerChannel = Words * 2
	FabricID          = uint16(0x5a52)
	// QualifiedMapID comes from lanecal-2026-09-11 and lanemap_seed.vh.
	QualifiedMapID = uint16(0xe192)
	addressMask    = Words - 1
)

// Bus is the subset of bus.Dev used here. RawWrite bypasses the OLD default
// fabric's selector schema, never the plane guard: all accesses here are CS1.
// TRLC-LINKS: REQ-SDS-082
type Bus interface {
	Read(plane uint8, selector uint16) (uint16, error)
	RawWrite(selector, value uint16) error
}

// TRLC-LINKS: REQ-SDS-033, REQ-SDS-082, REQ-SDS-083
type Capture struct {
	decodedScratch []byte
	recallScratch  []byte
	recallData     []byte
	recallHalves   []uint16
	isolatedData   []byte // a bus worker's whole read (recallIsolated)
	streamHalves   []uint16
	streamWords    uint16
	streamBytes    []byte
	mu             sync.Mutex
	bus            Bus
	// armRead holds the registers Arm reads in one batch (armSels); while
	// it runs, the constant ones answer its parts' reads, which would each
	// be a round trip.
	armRead  [len(armSels)]uint16
	armReady bool
	spanHook func(done, total int) // between recall bursts (SetSpanHook)
	// envelopeOn: an envelope recall may have left the reducer enabled.
	envelopeOn      bool
	envelopeRejects int
	envelopeLast    string
	// symbolTable is the symbol trigger's table last written (symbolTableOK).
	symbolTable   [SymbolTableSize]uint8
	symbolTableOK bool
	beats         atomic.Uint64
}

// TRLC-LINKS: REQ-SDS-032, REQ-SDS-040
type Source uint8

const (
	ADC Source = iota
	Counter
)

// Config counts 32-bit SRAM words. Each ADC word contains two consecutive
// sample pairs, byte order CH1[n], CH2[n], CH1[n+1], CH2[n+1].
// With DecimationLog2 nonzero, each word instead contains one little-endian
// uint16 Q8.8 CH1/CH2 pair. PreWords excludes
// the triggering word; PostWords includes it. Pair and TriggerChannel are
// zero based. Cross-core analog timing is not implied by this API.
// TRLC-LINKS: REQ-SDS-032, REQ-SDS-040
type Config struct {
	Qualifier         QualifierConfig         `json:"qualifier"`
	Zones             ZoneConfig              `json:"zones"`
	Symbol            SymbolTriggerConfig     `json:"symbol"`
	Sequence          SequenceTriggerConfig   `json:"sequence"`
	Packet            PacketTriggerConfig     `json:"packet"`
	Manchester        ManchesterTriggerConfig `json:"manchester"`
	USBLS             USBLSTriggerConfig      `json:"usbls"`
	MIL1553           MIL1553TriggerConfig    `json:"mil1553"`
	SENT              SENTTriggerConfig       `json:"sent"`
	DecodedEvents     bool                    `json:"decoded_events"`
	SPI               SPITriggerConfig        `json:"spi"`
	I2C               I2CTriggerConfig        `json:"i2c"`
	UART              UARTTriggerConfig       `json:"uart"`
	Source            Source                  `json:"source"`
	DecimationLog2    uint8                   `json:"decimation_log2"`
	Pair              uint8                   `json:"pair"`
	PreWords          uint32                  `json:"pre_words"`
	PostWords         uint32                  `json:"post_words"`
	Normal            bool                    `json:"normal"`
	Falling           bool                    `json:"falling"`
	TriggerChannel    uint8                   `json:"trigger_channel"`
	TriggerLevel      uint8                   `json:"trigger_level"`
	TriggerHysteresis uint8                   `json:"trigger_hysteresis,omitempty"`
	// SecondLevel slices a two-line receiver's second line (SPI's data,
	// I2C's SDA; register 59, its band TriggerHysteresis), so the channels
	// may sit at different offsets and V/div; 0 is TriggerLevel.
	SecondLevel uint8 `json:"second_level,omitempty"`
}

// UARTTriggerConfig describes the optional hardware matcher: the 125 MHz,
// 8N1 receiver on line levels (BitTicks), or the receiver on the samples
// (capability 0x5503, uart_sample.v; Period16 set), which takes 5 to 9 data
// bits, parity and a bit time in sixteenths of a nanosecond to 62.5 MBd.
// Pattern is packed chronologically, e.g. length 2 and pattern 0x4869.
// TRLC-LINKS: REQ-SDS-013
type UARTTriggerConfig struct {
	Enabled  bool   `json:"enabled"`
	Channel  uint8  `json:"channel"`
	Inverted bool   `json:"inverted"`
	BitTicks uint32 `json:"bit_ticks"`
	Pattern  uint32 `json:"pattern"`
	Length   uint8  `json:"length"`
	Period16 uint32 `json:"period16,omitempty"` // 256 (16 ns) .. 2^24-1
	Bits     uint8  `json:"bits,omitempty"`     // 5..9, 0 is 8
	Parity   uint8  `json:"parity,omitempty"`   // 0 none, 1 even, 2 odd
	Resync   bool   `json:"resync,omitempty"`   // every edge in a frame re-times the bits after it
}

// UART receiver capabilities (register 48).
const (
	uartLevels  = 0x5502
	uartSamples = 0x5503
)

// sampleUART reports a request for the receiver on the samples.
// TRLC-LINKS: REQ-SDS-229
func (u UARTTriggerConfig) sampleUART() bool { return u.Period16 != 0 }

// valid reports a request either receiver takes.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-229
func (u UARTTriggerConfig) valid() bool {
	if u.Channel > 1 || u.Length > 4 {
		return false
	}
	if u.sampleUART() {
		bits := u.Bits
		if bits == 0 {
			bits = 8
		}
		return u.Period16 >= 256 && u.Period16 <= 0xffffff && bits >= 5 && bits <= 9 && u.Parity <= 2
	}
	return u.BitTicks >= 4 && u.BitTicks <= 0xffffff && (u.Bits == 0 || u.Bits == 8) && u.Parity == 0
}

// Metadata counters are stable for recall only when Ready and Frozen are true.
// TriggerIndex is a WORD index, not a sample-half or interpolated timestamp.
// TRLC-LINKS: REQ-SDS-083
type Metadata struct {
	HasSampleTimeline bool    `json:"has_sample_timeline"`
	SampleFirst       uint64  `json:"sample_first"`
	SampleLast        uint64  `json:"sample_last"`
	RecordID          uint32  `json:"record_id"`
	EventEpoch        uint32  `json:"event_epoch"`
	HasRecordIdentity bool    `json:"has_record_identity"`
	Locked            bool    `json:"locked"`
	Ready             bool    `json:"ready"`
	Running           bool    `json:"running"`
	Frozen            bool    `json:"frozen"`
	Triggered         bool    `json:"triggered"`
	Prefetched        bool    `json:"prefetched"`
	Length            uint32  `json:"words"`
	Start             uint32  `json:"start"`
	TriggerIndex      uint32  `json:"trigger_index"`
	Position          uint32  `json:"position"`
	Origin            uint32  `json:"origin"`
	Revision          uint16  `json:"revision"`
	MapID             uint16  `json:"map_id"`
	SampleRateHz      float64 `json:"sample_rate_hz"`
	Decimation        uint32  `json:"decimation"`
	SampleBits        uint8   `json:"sample_bits"`
	FractionBits      uint8   `json:"fraction_bits"`
	SamplesPerWord    uint8   `json:"samples_per_word"`
	Interleaved       bool    `json:"interleaved"`
	DataFault         bool    `json:"data_fault"`
}

// TRLC-LINKS: REQ-SDS-032
func New(b Bus) (*Capture, error) {
	c := &Capture{bus: b}
	id, err := c.read(0)
	if err != nil {
		return nil, err
	}
	if id != FabricID {
		return nil, fmt.Errorf("sramcapture: fabric %04x, expected %04x (do not use the default fabric ABI)", id, FabricID)
	}
	rev, err := c.read(13)
	if err != nil {
		return nil, err
	}
	if rev < 4 {
		return nil, fmt.Errorf("sramcapture: revision %d lacks the corrected ADC map", rev)
	}
	m, err := c.read(14)
	if err != nil {
		return nil, err
	}
	if m != QualifiedMapID {
		return nil, fmt.Errorf("sramcapture: unqualified ADC map %04x, expected %04x", m, QualifiedMapID)
	}
	// A previous process may have left the envelope reducer on (a failed
	// reduction): the first recall, the startup counter check, must turn it off
	// or it reads reduced words ("startup counter word 0 is 1216", a crash loop
	// into the emergency binary).
	c.envelopeOn = rev == 10
	return c, nil
}

// TRLC-LINKS: REQ-SDS-082, REQ-SDS-025
func (c *Capture) read(s uint16) (uint16, error) {
	if c.armReady {
		for i, a := range armSels[1:] { // not the status, which moves
			if a == s {
				return c.armRead[1+i], nil
			}
		}
	}
	v, err := c.bus.Read(1, s)
	if err == nil {
		c.beats.Add(1)
	}
	return v, err
}

// Beats advances on successful bus activity, including a long record recall.
// A supervisor can observe progress without taking ownership from a transfer.
// TRLC-LINKS: REQ-SDS-025
func (c *Capture) Beats() uint64 { return c.beats.Load() }

// armSels are the registers Arm reads in one batch: the status, which it
// checks once, then the revision and the stream, record identity, packet
// and qualifier capabilities, which no write changes.
var armSels = [...]uint16{1, 13, 63, 73, 110, qualCapability}

// manyReader is a bus that reads several registers in one call (a bus
// worker: one round trip for them all).
type manyReader interface {
	ReadMany(plane uint8, sels, dst []uint16) error
}

// readMany reads the CS1 registers sels into dst in order.
// TRLC-LINKS: REQ-SDS-082
func (c *Capture) readMany(sels []uint16, dst []uint16) error {
	if m, ok := c.bus.(manyReader); ok {
		if err := m.ReadMany(1, sels, dst); err != nil {
			return err
		}
		c.beats.Add(uint64(len(sels)))
		return nil
	}
	for i, s := range sels {
		v, err := c.read(s)
		if err != nil {
			return err
		}
		dst[i] = v
	}
	return nil
}

// TRLC-LINKS: REQ-SDS-082
func (c *Capture) write(s, v uint16) error { return c.bus.RawWrite(s, v) }

// TRLC-LINKS: REQ-SDS-082
func (c *Capture) read32(s uint16) (uint32, error) {
	lo, e := c.read(s)
	if e != nil {
		return 0, e
	}
	hi, e := c.read(s + 1)
	return uint32(lo) | uint32(hi)<<16, e
}

// TRLC-LINKS: REQ-SDS-082
func (c *Capture) write32(s uint16, v uint32) error {
	if e := c.write(s, uint16(v)); e != nil {
		return e
	}
	return c.write(s+1, uint16(v>>16))
}

// TRLC-LINKS: REQ-SDS-040, REQ-SDS-083
func (c *Capture) Status() (Metadata, error) { c.mu.Lock(); defer c.mu.Unlock(); return c.status() }

// TRLC-LINKS: REQ-SDS-013
func (c *Capture) SupportsUART() (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	revision, err := c.read(13)
	if err != nil || revision < 10 {
		return false, err
	}
	capability, err := c.read(48)
	return (capability == uartLevels || capability == uartSamples) && err == nil, err
}

// SupportsSampleUART reports the UART receiver on the samples (uart_sample.v).
// TRLC-LINKS: REQ-SDS-229
func (c *Capture) SupportsSampleUART() (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	revision, err := c.read(13)
	if err != nil || revision < 10 {
		return false, err
	}
	capability, err := c.read(48)
	return capability == uartSamples && err == nil, err
}

// TRLC-LINKS: REQ-SDS-040, REQ-SDS-083
func (c *Capture) status() (m Metadata, err error) {
	// Read as few batches as the revision allows: each read through a bus
	// worker is a round trip, and this runs for every frame.
	var r [13]uint16
	if err = c.readMany([]uint16{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 13, 14}, r[:]); err != nil {
		return m, err
	}
	s := r[0]
	m.Ready = s&4 != 0
	m.Locked = s&64 != 0
	m.Running = s&8 != 0
	m.Frozen = s&16 != 0
	m.Triggered = s&32 != 0
	m.Prefetched = s&1024 != 0
	m.Length = uint32(r[1]) | uint32(r[2])<<16
	m.Start = uint32(r[3]) | uint32(r[4])<<16
	m.TriggerIndex = uint32(r[5]) | uint32(r[6])<<16
	m.Position = uint32(r[7]) | uint32(r[8])<<16
	m.Origin = uint32(r[9]) | uint32(r[10])<<16
	m.Revision, m.MapID = r[11], r[12]
	// From revision 10 the later reads are one batch too: the capability,
	// then rate, flags, decimation and format (revisions 6 and 9).
	var late [5]uint16
	haveLate := m.Revision >= 10
	if haveLate {
		if err = c.readMany([]uint16{73, 15, 19, 28, 29}, late[:]); err != nil {
			return m, err
		}
		if late[0] == 0x5201 {
			var id [4]uint16
			if err = c.readMany([]uint16{69, 70, 71, 72}, id[:]); err != nil {
				return m, err
			}
			m.RecordID = uint32(id[0]) | uint32(id[1])<<16
			m.EventEpoch = uint32(id[2]) | uint32(id[3])<<16
			m.HasRecordIdentity = true
		}
	}
	m.SampleRateHz = 100000000
	m.Decimation = 1
	m.SampleBits = 8
	m.SamplesPerWord = 2
	if m.Revision >= 6 {
		rate, e := late[1], error(nil)
		if !haveLate {
			rate, e = c.read(15)
		}
		if e != nil {
			return m, e
		}
		if rate != 500 {
			return m, fmt.Errorf("sramcapture: unknown interleave rate %d", rate)
		}
		flags := late[2]
		if !haveLate {
			flags, e = c.read(19)
		}
		if e != nil {
			return m, e
		}
		m.SampleRateHz = float64(rate) * 1000000
		m.Interleaved = true
		m.DataFault = flags&1 != 0
		m.Locked = m.Locked && flags&2 != 0
	}
	if m.Revision >= 9 {
		log, bits, e := late[3], late[4], error(nil)
		if !haveLate {
			log, e = c.read(28)
			if e == nil {
				bits, e = c.read(29)
			}
		}
		if e != nil {
			return m, e
		}
		if log != 0 && (log < 4 || log > 20) {
			return m, fmt.Errorf("sramcapture: invalid decimation %d", log)
		}
		m.Decimation = uint32(1) << log
		m.SampleRateHz /= float64(m.Decimation)
		if log != 0 {
			m.SampleBits = 16
			m.FractionBits = 8
			m.SamplesPerWord = 1
		}
		if bits != uint16(m.SampleBits) {
			return m, fmt.Errorf("sramcapture: format mismatch %d", bits)
		}
	}
	if m.Revision >= 10 && m.Ready && m.Frozen && m.HasRecordIdentity && m.Interleaved && m.SamplesPerWord == 2 && m.Length != 0 {
		err = c.sampleTimeline(&m)
	}
	return m, err
}

// sampleTimeline publishes only a frozen raw record's ADC ordinals. Precision
// records require their filter's sample mapping and cannot use this linear map.
// TRLC-LINKS: REQ-SDS-039, REQ-SDS-041, REQ-SDS-083
func (c *Capture) sampleTimeline(m *Metadata) error {
	capability, err := c.read(83)
	if err != nil || capability != 0x5401 {
		return err
	}
	valid, err := c.read(84)
	if err != nil || valid&1 == 0 {
		return err
	}
	var t [8]uint16
	if err := c.readMany([]uint16{85, 86, 87, 88, 89, 90, 91, 92}, t[:]); err != nil {
		return err
	}
	low, high := uint32(t[0])|uint32(t[1])<<16, uint32(t[2])|uint32(t[3])<<16
	id, epoch := uint32(t[4])|uint32(t[5])<<16, uint32(t[6])|uint32(t[7])<<16
	if id != m.RecordID || epoch != m.EventEpoch {
		return fmt.Errorf("sramcapture: sample timeline identity changed")
	}
	lastWord := uint64(high)<<32 | uint64(low)
	span := 2 * uint64(m.Length-1)
	if m.Length > Words || lastWord < span || lastWord&1 != 0 {
		return fmt.Errorf("sramcapture: invalid raw sample timeline")
	}
	m.SampleFirst, m.SampleLast = lastWord-span, lastWord+1
	m.HasSampleTimeline = true
	return nil
}

// TRLC-LINKS: REQ-SDS-082
func (c *Capture) command(ctx context.Context, op uint16) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	s, e := c.read(1)
	if e != nil {
		return e
	}
	ack := s & 512
	if e = c.write(1, op); e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	for {
		if e = ctx.Err(); e != nil {
			return fmt.Errorf("sramcapture: command %d: %w", op, e)
		}
		s, e = c.read(1)
		if e != nil {
			return e
		}
		if s&512 != ack {
			if s&384 != 0 {
				return fmt.Errorf("sramcapture: command %d rejected (status %04x)", op, s)
			}
			return nil
		}
	}
}

// TRLC-LINKS: REQ-SDS-082
func (c *Capture) wait(ctx context.Context, mask uint16) error {
	for {
		if e := ctx.Err(); e != nil {
			return e
		}
		s, e := c.read(1)
		if e != nil {
			return e
		}
		if s&mask == mask {
			return nil
		}
		timer := time.NewTimer(100 * time.Microsecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// TRLC-LINKS: REQ-SDS-082
func (c *Capture) ready(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return c.wait(ctx, 4)
}

// TRLC-LINKS: REQ-SDS-032, REQ-SDS-040, REQ-SDS-082
func (c *Capture) Arm(ctx context.Context, cfg Config) error {
	if err := cfg.Packet.validate(); err != nil {
		return err
	}
	if err := cfg.Sequence.validate(); err != nil {
		return err
	}
	// A sequence matches the events of one enabled hardware decoder.
	if cfg.Sequence.Length != 0 && !(cfg.Packet.Enabled || cfg.Manchester.Enabled || cfg.USBLS.Enabled || cfg.MIL1553.Enabled ||
		cfg.SENT.Enabled || cfg.SPI.Enabled || cfg.I2C.Enabled || cfg.UART.Enabled) {
		return fmt.Errorf("sramcapture: a sequence trigger needs a configured hardware decoder")
	}
	if err := cfg.Manchester.validate(); err != nil {
		return err
	}
	if err := cfg.Qualifier.validate(); err != nil {
		return err
	}
	if err := cfg.Zones.validate(); err != nil {
		return err
	}
	if err := cfg.Symbol.validate(); err != nil {
		return err
	}
	// The symbol trigger fires on its own matches in place of the trigger edge.
	if cfg.Symbol.Enabled && (cfg.Qualifier.Enabled || cfg.Zones.Enabled() || cfg.Sequence.Length != 0 || cfg.Packet.Enabled || cfg.Manchester.Enabled ||
		cfg.USBLS.Enabled || cfg.MIL1553.Enabled || cfg.SENT.Enabled || cfg.SPI.Enabled || cfg.I2C.Enabled || cfg.UART.Enabled || cfg.Source != ADC || !cfg.Normal) {
		return fmt.Errorf("sramcapture: a symbol trigger requires exclusive ADC normal triggering")
	}
	if cfg.Zones.Enabled() && (cfg.Source != ADC || !cfg.Normal) {
		return fmt.Errorf("sramcapture: zones qualify a normal ADC trigger")
	}
	// A qualifier fires on its own event in place of the trigger edge.
	if cfg.Qualifier.Enabled && (cfg.Packet.Enabled || cfg.Manchester.Enabled || cfg.USBLS.Enabled || cfg.MIL1553.Enabled || cfg.SENT.Enabled ||
		cfg.SPI.Enabled || cfg.I2C.Enabled || cfg.UART.Enabled || cfg.Source != ADC || !cfg.Normal) {
		return fmt.Errorf("sramcapture: a qualifier requires exclusive ADC normal triggering")
	}
	// The packet image carries only these two blocks; each needs the whole receiver.
	general := cfg.USBLS.Enabled || cfg.MIL1553.Enabled || cfg.SENT.Enabled || cfg.SPI.Enabled || cfg.I2C.Enabled || cfg.UART.Enabled
	if (cfg.Packet.Enabled || cfg.Manchester.Enabled) && ((cfg.Packet.Enabled && cfg.Manchester.Enabled) || general || cfg.Source != ADC || !cfg.Normal) {
		return fmt.Errorf("sramcapture: packet and Manchester triggers require exclusive ADC normal triggering")
	}
	if err := cfg.USBLS.validate(); err != nil {
		return err
	}
	if cfg.USBLS.Enabled && (cfg.UART.Enabled || cfg.I2C.Enabled || cfg.SPI.Enabled || cfg.SENT.Enabled || cfg.MIL1553.Enabled || cfg.Source != ADC || !cfg.Normal) {
		return fmt.Errorf("sramcapture: USB requires exclusive ADC normal triggering")
	}
	if err := cfg.MIL1553.validate(); err != nil {
		return err
	}
	if cfg.MIL1553.Enabled && (cfg.UART.Enabled || cfg.I2C.Enabled || cfg.SPI.Enabled || cfg.SENT.Enabled || cfg.Source != ADC || !cfg.Normal) {
		return fmt.Errorf("sramcapture: MIL-STD-1553 requires exclusive ADC normal triggering")
	}
	if err := cfg.SENT.validate(); err != nil {
		return err
	}
	if cfg.SENT.Enabled && (cfg.UART.Enabled || cfg.I2C.Enabled || cfg.SPI.Enabled || cfg.Source != ADC || !cfg.Normal) {
		return fmt.Errorf("sramcapture: SENT requires exclusive ADC normal triggering")
	}
	if cfg.DecodedEvents && !(cfg.Packet.Enabled || cfg.Manchester.Enabled || cfg.USBLS.Enabled || cfg.UART.Enabled || cfg.I2C.Enabled || cfg.SPI.Enabled || cfg.SENT.Enabled || cfg.MIL1553.Enabled) {
		return fmt.Errorf("sramcapture: decoded streaming requires a configured hardware decoder")
	}
	if cfg.SPI.Enabled && (cfg.UART.Enabled || cfg.I2C.Enabled || cfg.Source != ADC || !cfg.Normal || cfg.SPI.ClockChannel > 1 || cfg.SPI.Length > 4 || cfg.SPI.GapTicks < 3 || cfg.SPI.GapTicks > 0xffffff) {
		return fmt.Errorf("sramcapture: invalid hardware SPI configuration")
	}
	if cfg.I2C.Enabled && (cfg.UART.Enabled || cfg.Source != ADC || !cfg.Normal || cfg.I2C.ClockChannel > 1 || cfg.I2C.Address < -1 || cfg.I2C.Address > 127 || cfg.I2C.Direction > 2 || cfg.I2C.Length > 4) {
		return fmt.Errorf("sramcapture: invalid hardware I2C configuration")
	}
	if cfg.UART.Enabled && (cfg.Source != ADC || !cfg.Normal || !cfg.UART.valid()) {
		return fmt.Errorf("sramcapture: invalid hardware UART configuration")
	}
	if (cfg.DecimationLog2 != 0 && (cfg.DecimationLog2 < 4 || cfg.DecimationLog2 > 20)) || cfg.Source > Counter || cfg.Pair > 4 || cfg.TriggerChannel > 1 || cfg.PostWords == 0 || uint64(cfg.PreWords)+uint64(cfg.PostWords) > uint64(Words) {
		return fmt.Errorf("sramcapture: invalid source, channel, or record geometry")
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.readMany(armSels[:], c.armRead[:]); e != nil {
		return e
	}
	c.armReady = true
	defer func() { c.armReady = false }()
	s := c.armRead[0]
	if s&4 == 0 || s&8 != 0 {
		return fmt.Errorf("sramcapture: halt the current acquisition before rearming")
	}
	revision, e := c.read(13)
	if e != nil {
		return e
	}
	if revision >= 6 && cfg.Pair != 0 {
		return fmt.Errorf("sramcapture: interleaved image uses all five pairs; pair must be zero")
	}
	if revision < 9 && cfg.DecimationLog2 != 0 {
		return fmt.Errorf("sramcapture: fabric lacks precision decimation")
	}
	if cfg.DecimationLog2 != 0 {
		if raw, e := c.rawOnly(); e != nil || raw {
			return errors.Join(e, fmt.Errorf("sramcapture: loaded image records raw samples only"))
		}
	}
	// Unknown registers read zero on the owned fabric. Discover the optional
	streamCapability := uint16(0)
	if revision >= 10 {
		streamCapability, e = c.read(63)
		if e != nil {
			return e
		}
	}
	if cfg.DecodedEvents && streamCapability != 0x4501 {
		return fmt.Errorf("sramcapture: fabric lacks decoded event transport")
	}
	// Each arm is a new capture configuration. Stop the previous stream before
	// changing receiver registers; its epoch is invalidated on the next enable.
	if streamCapability == 0x4501 {
		if e = c.write(57, 0); e != nil {
			return e
		}
	}
	if err := c.configureQualifier(revision, cfg.Qualifier); err != nil {
		return err
	}
	if err := c.configureZones(revision, cfg.Zones); err != nil {
		return err
	}
	if err := c.configureSymbol(revision, cfg.Symbol); err != nil {
		return err
	}
	if err := c.configureSequence(cfg.Sequence); err != nil {
		return err
	}
	if err := c.configurePacket(cfg.Packet); err != nil {
		return err
	}
	if err := c.configureManchester(cfg.Manchester); err != nil {
		return err
	}
	if err := c.configureSENT(revision, cfg.SENT); err != nil {
		return err
	}
	if err := c.configureUSBLS(revision, cfg.USBLS); err != nil {
		return err
	}
	if err := c.configureMIL1553(revision, cfg.MIL1553); err != nil {
		return err
	}
	if err := c.configureSPI(revision, cfg.SPI); err != nil {
		return err
	}
	if err := c.configureI2C(revision, cfg.I2C); err != nil {
		return err
	}
	// Unknown registers read zero on the owned fabric. Discover the optional
	// block before writing it, and explicitly disable it when returning to edges.
	if revision >= 10 {
		capability, err := c.read(48)
		if err != nil {
			return err
		}
		if capability == uartLevels || capability == uartSamples {
			u := cfg.UART
			if u.Enabled && u.sampleUART() != (capability == uartSamples) {
				return fmt.Errorf("sramcapture: the loaded image's UART receiver takes the other configuration")
			}
			control := uint16(0)
			if u.Enabled {
				control = 1 | uint16(u.Channel)<<1 | uint16(u.Length)<<3
				if u.Inverted {
					control |= 4
				}
				ticks := u.BitTicks
				if u.sampleUART() {
					bits := u.Bits
					if bits == 0 {
						bits = 8
					}
					control |= uint16(bits)<<6 | uint16(u.Parity)<<10
					if u.Resync {
						control |= 1 << 12
					}
					ticks = u.Period16
				}
				for _, r := range []struct{ sel, value uint16 }{
					{49, uint16(ticks)}, {50, uint16(ticks >> 16)},
					{51, uint16(u.Pattern)}, {52, uint16(u.Pattern >> 16)},
				} {
					if err := c.write(r.sel, r.value); err != nil {
						return err
					}
				}
			}
			if err := c.write(48, control); err != nil {
				return err
			}
		} else if cfg.UART.Enabled {
			return fmt.Errorf("sramcapture: fabric lacks hardware UART trigger")
		}
	} else if cfg.UART.Enabled {
		return fmt.Errorf("sramcapture: fabric lacks hardware UART trigger")
	}
	if revision >= 9 {
		if e = c.write(18, uint16(cfg.DecimationLog2)); e != nil {
			return e
		}
	}
	word := uint16(cfg.Pair) << 1
	if cfg.Source == ADC {
		word |= 1
	}
	if cfg.Normal {
		word |= 16
	}
	if cfg.Falling {
		word |= 32
	}
	if cfg.TriggerChannel == 1 {
		word |= 64
	}
	if e = c.write32(2, cfg.PreWords); e != nil {
		return e
	}
	if e = c.write32(4, cfg.PostWords); e != nil {
		return e
	}
	if e = c.write(6, word); e != nil {
		return e
	}
	if e = c.write(7, uint16(cfg.TriggerLevel)|uint16(cfg.TriggerHysteresis)<<8); e != nil {
		return e
	}
	if cfg.SPI.Enabled || cfg.I2C.Enabled || (cfg.Symbol.Enabled && cfg.Symbol.TwoLines) {
		second := cfg.SecondLevel
		if second == 0 {
			second = cfg.TriggerLevel
		}
		if e = c.write(regSecondLevel, uint16(second)); e != nil {
			return e
		}
	}
	if cfg.DecodedEvents {
		if _, e = c.enableDecodedEvents(true); e != nil {
			return e
		}
	}
	e = c.command(ctx, 1)
	if e != nil && cfg.DecodedEvents {
		_ = c.write(57, 0)
	}
	return e
}

// TRLC-LINKS: REQ-SDS-082
func (c *Capture) Force(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.command(ctx, 4)
}

// TRLC-LINKS: REQ-SDS-082
func (c *Capture) Halt(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.command(ctx, 5); e != nil {
		return e
	}
	return c.ready(ctx)
}

// TRLC-LINKS: REQ-SDS-082
func (c *Capture) WaitFrozen(ctx context.Context) error {
	// Do not hold ownership while waiting for an external trigger: another
	// caller must remain able to Force or Halt this acquisition.
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		c.mu.Lock()
		s, err := c.read(1)
		c.mu.Unlock()
		if err != nil {
			return err
		}
		if s&20 == 20 {
			return nil
		}
		timer := time.NewTimer(100 * time.Microsecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// TRLC-LINKS: REQ-SDS-084
func (c *Capture) Snapshot(ctx context.Context) (out [10]uint8, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err = c.command(ctx, 6); err != nil {
		return out, err
	}
	for {
		flags, e := c.read(1)
		if e != nil {
			return out, e
		}
		if flags&2048 == 0 {
			break
		}
		if e = ctx.Err(); e != nil {
			return out, e
		}
	}
	for i := 0; i < 5; i++ {
		v, e := c.read(uint16(20 + i))
		if e != nil {
			return out, e
		}
		out[2*i] = uint8(v)
		out[2*i+1] = uint8(v >> 8)
	}
	return out, nil
}

// RestartEncode stops the ten converter cores' encode clocks (register 15,
// one enable a core) and starts them again: a converter whose encode came up
// without converting starts afresh. Between acquisitions only: the cores
// hold their last codes while stopped.
// TRLC-LINKS: REQ-SDS-231
func (c *Capture) RestartEncode() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.write(15, 0); err != nil {
		return err
	}
	time.Sleep(time.Millisecond)
	return c.write(15, 0x3ff)
}

// Recall streams an immutable frozen window. It never rearms or writes SRAM.
// A failed/partial host write leaves the record intact for another recall.
// Zero count means an empty window, not full depth. Byte layout is documented
// on Config; the caller can use 2*i and 2*i+1 to split channels.
// TRLC-LINKS: REQ-SDS-033
func (c *Capture) Recall(ctx context.Context, offset, count uint32, dst io.Writer) (written int64, err error) {
	return c.recall(ctx, offset, count, dst, false)
}

// RecallForward stages a frozen window in two forward passes. Fresh reads retain
// their warm-up prefix; a second pass fills the gaps left between bulk reads.
// No data reaches dst until all SRAM reads have succeeded.
// TRLC-LINKS: REQ-SDS-033
func (c *Capture) RecallForward(ctx context.Context, offset, count uint32, dst io.Writer) (int64, error) {
	return c.recall(ctx, offset, count, dst, true)
}

// RecordRead is one window of a record read by RecallReadsOf: Count buckets
// of Bucket words from word Offset reduced as RecallEnvelope does, or Count
// words raw (Bucket 0) as RecallForward does on a revision 10 fabric and
// Recall otherwise, written to Dst.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-033
type RecordRead struct {
	Offset, Bucket, Count uint32
	Dst                   io.Writer
}

// size is the bytes the read gives from a record m describes.
// TRLC-LINKS: REQ-SDS-033
func (r RecordRead) size(m Metadata) int {
	if r.Bucket != 0 && m.FractionBits == 8 {
		return int(r.Count) * 8 // Q8.8 buckets: the minima word, then the maxima
	}
	return int(r.Count) * 4
}

// RecordReadsMax bounds the reads RecallReadsOf makes in one round trip.
const RecordReadsMax = 4

// RecallReadsOf makes reads, in order, of the record m describes (read by
// Status, or an earlier recall of it, while frozen): a frame's screen
// and the stretches its views show magnified. A bus worker makes them all in
// one round trip (isolatedRecall); else a check that the same record is still
// the frozen one replaces reading its whole status again.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-033
func (c *Capture) RecallReadsOf(ctx context.Context, m Metadata, reads ...RecordRead) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if owned, err := c.recallIsolated(m, reads); owned {
		return err
	}
	m, err := c.sameFrozen(m)
	if err != nil {
		return err
	}
	for _, r := range reads {
		if err := c.recallRead(ctx, m, r); err != nil {
			return err
		}
	}
	return nil
}

// RecallRecord makes reads, in order, of the frozen record id names, as
// RecallReadsOf does here; an error when that record is no longer the frozen
// one. A bus worker serves isolatedRecall with it.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-033
func (c *Capture) RecallRecord(ctx context.Context, id RecordIdentity, reads []RecordRead) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	m, err := c.status()
	if err != nil {
		return err
	}
	if !m.HasRecordIdentity || m.RecordID != id.Record || m.EventEpoch != id.Epoch {
		return fmt.Errorf("sramcapture: retained record identity changed")
	}
	for _, r := range reads {
		if err := c.recallRead(ctx, m, r); err != nil {
			return err
		}
	}
	return nil
}

// sameFrozen checks that the record m describes is still the frozen one -
// its flags and its identity, five register reads - and returns m; a record
// without an identity is checked against a full status read.
// Called with c.mu held.
// TRLC-LINKS: REQ-SDS-033, REQ-SDS-083
func (c *Capture) sameFrozen(m Metadata) (Metadata, error) {
	if !m.HasRecordIdentity {
		now, err := c.status()
		if err != nil {
			return now, err
		}
		if now.Length != m.Length || now.Start != m.Start || now.Origin != m.Origin || now.TriggerIndex != m.TriggerIndex || now.Revision != m.Revision || now.Decimation != m.Decimation {
			return now, fmt.Errorf("sramcapture: retained record changed")
		}
		return now, nil
	}
	s, err := c.read(1)
	if err != nil {
		return m, err
	}
	if s&4 == 0 || s&16 == 0 || s&8 != 0 {
		return m, fmt.Errorf("sramcapture: record is not frozen")
	}
	record, err := c.read32(69)
	if err != nil {
		return m, err
	}
	epoch, err := c.read32(71)
	if err != nil {
		return m, err
	}
	if record != m.RecordID || epoch != m.EventEpoch {
		return m, fmt.Errorf("sramcapture: retained record identity changed")
	}
	return m, nil
}

// recallRead makes read r of the record m, read just now. Called with c.mu
// held.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-033
func (c *Capture) recallRead(ctx context.Context, m Metadata, r RecordRead) error {
	if r.Bucket != 0 {
		return c.recallEnvelope(ctx, m, r.Offset, r.Bucket, r.Count, r.Dst)
	}
	_, err := c.recallFrozen(ctx, m, r.Offset, r.Count, r.Dst, m.Revision == 10)
	return err
}

// isolatedRecall is a bus whose registers a worker process owns: it makes
// whole reads of a frozen record there (Capture.RecallRecord) in one round
// trip, where a read made here takes one per register access - a few dozen,
// each a pair of process switches on the scope's single core - and every
// round trip waits for whatever else holds the CPU (a display render) to let
// the caller run again: a live frame's reads cost it several ms of CPU and
// tens of ms. windows holds each read's (offset, bucket, count). owned is
// false when no worker owns the bus or dst exceeds what one round trip
// carries: the reads are then made here.
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-033
type isolatedRecall interface {
	RecallIsolated(epoch, record uint32, windows []uint32, dst []byte) (owned bool, err error)
}

// recallIsolated has the bus worker make reads of the record m describes
// (see isolatedRecall); false when they are to be made here, also for a
// record without the identity to check it by. Called with c.mu held.
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-033
func (c *Capture) recallIsolated(m Metadata, reads []RecordRead) (bool, error) {
	r, ok := c.bus.(isolatedRecall)
	if !ok || !m.HasRecordIdentity || len(reads) == 0 || len(reads) > RecordReadsMax {
		return false, nil
	}
	size := 0
	var windows [3 * RecordReadsMax]uint32
	for i, rd := range reads {
		size += rd.size(m)
		windows[3*i], windows[3*i+1], windows[3*i+2] = rd.Offset, rd.Bucket, rd.Count
	}
	if cap(c.isolatedData) < size {
		c.isolatedData = make([]byte, size)
	}
	data := c.isolatedData[:size]
	owned, err := r.RecallIsolated(m.EventEpoch, m.RecordID, windows[:3*len(reads)], data)
	if !owned || err != nil {
		return owned, err
	}
	for _, rd := range reads {
		k := rd.size(m)
		n, err := rd.Dst.Write(data[:k])
		if err == nil && n != k {
			err = io.ErrShortWrite
		}
		if err != nil {
			return true, err
		}
		data = data[k:]
	}
	return true, nil
}

// TRLC-LINKS: REQ-SDS-033
func (c *Capture) recall(ctx context.Context, offset, count uint32, dst io.Writer, forward bool) (written int64, err error) {
	return c.recallChecked(ctx, offset, count, dst, forward, nil)
}

// TRLC-LINKS: REQ-SDS-033, REQ-SDS-013
func (c *Capture) recallChecked(ctx context.Context, offset, count uint32, dst io.Writer, forward bool, expected *RecordIdentity) (written int64, err error) {
	if err = ctx.Err(); err != nil {
		return 0, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	m, err := c.status()
	if err != nil {
		return 0, err
	}
	if expected != nil && (!m.HasRecordIdentity || m.RecordID != expected.Record || m.EventEpoch != expected.Epoch) {
		return 0, fmt.Errorf("sramcapture: retained record identity changed")
	}
	return c.recallFrozen(ctx, m, offset, count, dst, forward)
}

// recallFrozen recalls the window of the record m, read just now. Called
// with c.mu held.
// TRLC-LINKS: REQ-SDS-033
func (c *Capture) recallFrozen(ctx context.Context, m Metadata, offset, count uint32, dst io.Writer, forward bool) (written int64, err error) {
	if m.DataFault {
		return 0, fmt.Errorf("sramcapture: interleave FIFO fault; record has missing samples")
	}
	if !m.Ready || !m.Frozen || m.Running {
		return 0, fmt.Errorf("sramcapture: record is not frozen")
	}
	if c.envelopeOn {
		if err = c.write(44, 0); err != nil {
			return 0, err
		}
		c.envelopeOn = false
	}
	if m.Length > Words || uint64(offset)+uint64(count) > uint64(m.Length) {
		return 0, fmt.Errorf("sramcapture: recall window exceeds record")
	}
	// Revision 7 qualifies fresh bursts with a discarded SRAM warm-up prefix.
	// These reads wrap physically but never add samples to the returned window.
	prefix := uint32(0)
	if m.Revision >= 7 {
		prefix = 16
	}
	bufferWords := uint32(512)
	physicalBufferWords := bufferWords
	if m.Revision >= 8 {
		size, e := c.read(26)
		if e != nil {
			return written, e
		}
		bufferWords = uint32(size)
		physicalBufferWords = bufferWords
		if bufferWords != 4096 && !(m.Revision == 11 && bufferWords == 8192) {
			return written, fmt.Errorf("sramcapture: unknown read buffer %d", bufferWords)
		}
		// Keep frozen recall transfers within the existing 8192-halfword DMA limit.
		bufferWords = min(bufferWords, 4096)
	}
	if forward && m.Revision != 10 {
		return 0, fmt.Errorf("sramcapture: forward recall requires revision 10")
	}
	if forward {
		if cap(c.recallScratch) < int(count*4) {
			c.recallScratch = make([]byte, count*4)
		}
		c.recallScratch = c.recallScratch[:count*4]
	}
	spans := recallSpans(count, bufferWords-prefix, 0)
	if forward {
		spans = recallSpans(count, bufferWords-prefix, prefix+2)
	}
	if cap(c.recallData) < int(bufferWords*4) {
		c.recallData = make([]byte, bufferWords*4)
		c.recallHalves = make([]uint16, bufferWords*2)
	}
	data := c.recallData[:bufferWords*4]
	halves := c.recallHalves[:bufferWords*2]
	for i, span := range spans {
		if err = ctx.Err(); err != nil {
			return written, err
		}
		if i > 0 {
			c.yieldBetweenSpans(i, len(spans))
		}
		n := span.count
		target := (m.Origin + m.Start + offset + span.offset - prefix) & addressMask
		position, e := c.read32(8)
		if e != nil {
			return written, e
		}
		flags, e := c.read(1)
		if e != nil {
			return written, e
		}
		op := uint16(2)
		if prefix == 0 && flags&1024 != 0 && target == (position-1)&addressMask {
			op = 7
		} else {
			skip := (target - position) & addressMask
			if skip != 0 {
				if e = c.write32(8, skip); e != nil {
					return written, e
				}
				if e = c.command(ctx, 3); e != nil {
					return written, e
				}
				if e = c.ready(ctx); e != nil {
					return written, e
				}
			}
		}
		if e = c.write32(8, n+prefix); e != nil {
			return written, e
		}
		if e = c.command(ctx, op); e != nil {
			return written, e
		}
		if e = c.ready(ctx); e != nil {
			return written, e
		}
		got, e := c.read(12)
		if e != nil {
			return written, e
		}
		if uint32(got) != n+prefix {
			return written, fmt.Errorf("sramcapture: short read buffer: got %d want %d", got, n+prefix)
		}
		if m.Revision >= 8 {
			if forward || m.Revision >= 10 {
				// Prime the non-burst RAM address to the same first word before
				// switching the host read mux to the DMA burst selector.
				if e = c.write(16, uint16(prefix)); e != nil {
					return written, e
				}
				if _, e = c.read(17); e != nil {
					return written, e
				}
			}
			if e = c.write(17, uint16(prefix*2)); e != nil {
				return written, e
			}
			if pop, ok := c.bus.(interface{ PopWordsChecked(uint16, []uint16) error }); ok {
				if e = pop.PopWordsChecked(25, halves[:n*2]); e != nil {
					return written, e
				}
			} else {
				for i := uint32(0); i < n*2; i++ {
					v, err := c.read(25)
					if err != nil {
						return written, err
					}
					halves[i] = v
				}
			}
			index, err := c.read(27)
			if err != nil {
				return written, err
			}
			if uint32(index) != ((n+prefix)*2)%(physicalBufferWords*2) {
				return written, fmt.Errorf("sramcapture: burst pointer %d after %d words", index, n)
			}
			for i := uint32(0); i < n*2; i++ {
				binary.LittleEndian.PutUint16(data[2*i:], halves[i])
			}
		} else {
			for i := uint32(0); i < n; i++ {
				if e = c.write(16, uint16(i+prefix)); e != nil {
					return written, e
				}
				v, e := c.read32(17)
				if e != nil {
					return written, e
				}
				binary.LittleEndian.PutUint32(data[4*i:], v)
			}
		}
		if forward {
			copy(c.recallScratch[span.offset*4:], data[:4*n])
			continue
		}
		nn, e := dst.Write(data[:4*n])
		written += int64(nn)
		if e != nil {
			return written, e
		}
		if nn != int(4*n) {
			return written, io.ErrShortWrite
		}
	}
	if forward && count != 0 {
		nn, e := dst.Write(c.recallScratch)
		if e == nil && nn != len(c.recallScratch) {
			e = io.ErrShortWrite
		}
		return int64(nn), e
	}
	return written, nil
}

// yieldBetweenSpans lets a waiting front-panel scan read run between recall
// bursts. A deep recall holds the capture for seconds; the panel samples key
// state (presses are not latched), so a blocked scan delays every press and
// drops a short tap outright. Each span re-primes its own position and read
// mux, and a scan read touches only its select/data registers, so nothing a
// burst relies on changes across the gap. Called with c.mu held.
// TRLC-LINKS: REQ-SDS-033, REQ-SDS-022
func (c *Capture) yieldBetweenSpans(done, total int) {
	if c.spanHook != nil {
		c.spanHook(done, total)
	}
	c.mu.Unlock()
	runtime.Gosched()
	c.mu.Lock()
}

// SetSpanHook installs fn to run between recall bursts on the recalling
// goroutine, which owns the bus there — e.g. to latch a staged panel lamp
// mid-recall — with the bursts done of total (recall progress). Set it before
// the first recall.
// TRLC-LINKS: REQ-SDS-033, REQ-SDS-135
func (c *Capture) SetSpanHook(fn func(done, total int)) {
	c.mu.Lock()
	c.spanHook = fn
	c.mu.Unlock()
}

// Locked runs fn holding the capture's bus lock, serialized with recalls'
// bursts and panel scan reads.
// TRLC-LINKS: REQ-SDS-135
func (c *Capture) Locked(fn func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fn()
}

// TRLC-LINKS: REQ-SDS-033
type recallSpan struct{ offset, count uint32 }

// gap accounts for the discarded prefix, fresh-read flush clock, and any
// explicit advance between bulk bursts.
// TRLC-LINKS: REQ-SDS-033
func recallSpans(count, payload, gap uint32) []recallSpan {
	var bulk, holes []recallSpan
	for at := uint32(0); at < count; {
		n := count - at
		if n > payload {
			n = payload
		}
		bulk = append(bulk, recallSpan{at, n})
		at += n
		n = count - at
		if n > gap {
			n = gap
		}
		if n != 0 {
			holes = append(holes, recallSpan{at, n})
			at += n
		}
	}
	return append(bulk, holes...)
}
