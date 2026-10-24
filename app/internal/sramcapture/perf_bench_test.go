// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import (
	"context"
	"encoding/binary"
	"testing"
)

// counterBytes is a startup counter record of n words.
// TRLC-LINKS: REQ-SDS-034
func counterBytes(n int) []byte {
	b := make([]byte, 4*n)
	for i := 0; i < n; i++ {
		binary.LittleEndian.PutUint32(b[4*i:], uint32(i))
	}
	return b
}

// TRLC-LINKS: REQ-SDS-034
func BenchmarkCounterVerify(b *testing.B) {
	rec := counterBytes(int(Words))
	for b.Loop() {
		v := &counterVerifier{}
		for off := 0; off < len(rec); off += 16384 { // as the recall writes it, in bursts
			if _, err := v.Write(rec[off:min(off+16384, len(rec))]); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// A wrong word is found at its index, wherever it sits.
// TRLC-LINKS: REQ-SDS-034
func TestCounterVerifyFindsTheWord(t *testing.T) {
	for _, bad := range []int{0, 1, 2, 3, 4, 7, 1000, 4095} {
		rec := counterBytes(4096)
		rec[4*bad] ^= 1
		v := &counterVerifier{}
		n, err := v.Write(rec)
		if err == nil || n != 4*bad {
			t.Fatalf("bad word %d: n %d err %v", bad, n, err)
		}
	}
	v := &counterVerifier{}
	if _, err := v.Write(counterBytes(5)); err != nil || v.next != 5 {
		t.Fatalf("odd count: %v next %d", err, v.next)
	}
}

// TRLC-LINKS: REQ-SDS-013
func BenchmarkDecodedEventCodec(b *testing.B) {
	e := DecodedEvent{Epoch: 7, Sequence: 99, Sample: 123456789, Kind: EventData, Protocol: 9, Value: 0x41, Valid: true}
	var buf [DecodedEventBytes]byte
	for b.Loop() {
		if err := e.MarshalTo(buf[:]); err != nil {
			b.Fatal(err)
		}
		if _, err := ParseDecodedEvent(buf[:]); err != nil {
			b.Fatal(err)
		}
	}
}

// tripBus answers every capability as present and counts the bus calls a
// configuration takes (each a round trip through the bus worker).
type tripBus struct{ trips int }

// TRLC-LINKS: REQ-SDS-082
func (b *tripBus) Read(_ uint8, s uint16) (uint16, error) {
	b.trips++
	return map[uint16]uint16{13: 10, 53: i2cLevels, 64: spiLevels, 77: 0x5e01, 93: 0x4d01, 97: 0x5501, 102: 0x4d01,
		109: 0x5001, 110: 0xffff, 73: 0x5201, 118: 0x5351, qualCapability: 0x5154, qualCapability + 1: 0xf,
		symbolCapability: symbolBlock, symbolLoaded: 0}[s], nil
}

// TRLC-LINKS: REQ-SDS-082
func (b *tripBus) RawWrite(uint16, uint16) error { b.trips++; return nil }

// batchBus is tripBus that also makes a list of writes in one call.
type batchBus struct{ tripBus }

// TRLC-LINKS: REQ-SDS-082
func (b *batchBus) RawWriteMany([][2]uint16) error { b.trips++; return nil }

// configureAll enables every decoder trigger on c, one after another, as
// Arm does with each when a bus is set.
// TRLC-LINKS: REQ-SDS-013
func configureAll(c *Capture) error {
	var seq SequenceTriggerConfig
	seq.Length = 4
	steps := []error{
		c.configureI2C(10, I2CTriggerConfig{Enabled: true, Address: 0x50, Length: 2}),
		c.configureSPI(10, SPITriggerConfig{Enabled: true, Length: 2, GapTicks: 100}),
		c.configureSENT(10, SENTTriggerConfig{Enabled: true, TickTicks: 300, Nibbles: 6}),
		c.configureMIL1553(10, MIL1553TriggerConfig{Enabled: true, BitTicks: 250}),
		c.configureUSBLS(10, USBLSTriggerConfig{Enabled: true, BitTicksQ8: 40000, Length: 1}),
		c.configurePacket(PacketTriggerConfig{Enabled: true, Protocol: 1, Ticks: 100, Length: 1}),
		c.configureManchester(ManchesterTriggerConfig{Enabled: true, BitTicks: 100, Bits: 8, Length: 1}),
		c.configureSequence(seq),
		c.configureQualifier(10, QualifierConfig{Enabled: true, MinTicks: 10, MaxTicks: 20, Prescale: 1}),
		c.configureZones(10, ZoneConfig{Wait: 4, Bucket: 1, Zones: [4]ZoneWindow{{Enabled: true, Lo: 10, Hi: 200, Start: -5, End: 5}}}),
		c.configureSymbol(10, SymbolTriggerConfig{Enabled: true, Mode: 1, LeadLen: 4, Prescale: 1, UnitQ8: 256}),
	}
	for _, e := range steps {
		if e != nil {
			return e
		}
	}
	return nil
}

// TRLC-LINKS: REQ-SDS-013
func benchConfigure(b *testing.B, bus interface {
	Bus
	count() int
}) {
	c := &Capture{bus: bus}
	for b.Loop() {
		if err := configureAll(c); err != nil {
			b.Fatal(err)
		}
		c.symbolTableOK = false // reload the table every time, as a new trigger does
	}
	b.ReportMetric(float64(bus.count())/float64(b.N), "trips/op")
}

// TRLC-LINKS: REQ-SDS-082
func (b *tripBus) count() int { return b.trips }

// The configuration as one write a call (the bus without batches).
// TRLC-LINKS: REQ-SDS-013
func BenchmarkConfigureWriteEach(b *testing.B) { benchConfigure(b, &tripBus{}) }

// The configuration with the bus worker's batches.
// TRLC-LINKS: REQ-SDS-013
func BenchmarkConfigureBatched(b *testing.B) { benchConfigure(b, &batchBus{}) }

// armTrips wraps the capture tests' fake bus with a batched read and counts
// the round trips an Arm takes through it.
type armTrips struct {
	*fakeBus
	trips int
}

// TRLC-LINKS: REQ-SDS-082
func (a *armTrips) Read(p uint8, s uint16) (uint16, error) { a.trips++; return a.fakeBus.Read(p, s) }

// TRLC-LINKS: REQ-SDS-082
func (a *armTrips) RawWrite(s, v uint16) error { a.trips++; return a.fakeBus.RawWrite(s, v) }

// TRLC-LINKS: REQ-SDS-082
func (a *armTrips) ReadMany(p uint8, sels, dst []uint16) error {
	a.trips++
	for i, s := range sels {
		v, err := a.fakeBus.Read(p, s)
		if err != nil {
			return err
		}
		dst[i] = v
	}
	return nil
}

// An Arm on a revision-10 fabric through a bus worker reads its status,
// revision and every capability in one round trip.
// TRLC-LINKS: REQ-SDS-082
func TestArmRoundTrips(t *testing.T) {
	f := frozenBus()
	f.revision = 10
	f.flags = 4 // ready, idle
	a := &armTrips{fakeBus: f}
	c := &Capture{bus: a}
	if err := c.Arm(context.Background(), Config{PreWords: Words / 2, PostWords: Words / 2, Normal: true}); err != nil {
		t.Skip("the fake does not arm:", err)
	}
	if a.trips > 13 { // the status, revision and capabilities in one batch
		t.Fatalf("an Arm took %d round trips, want at most 13", a.trips)
	}
}

// One event drained to a writer that ends the drain.
// TRLC-LINKS: REQ-SDS-013
func BenchmarkDrainDecodedEvent(b *testing.B) {
	e := DecodedEvent{Epoch: 7, Kind: EventLoss, Count: 19, Sample: 100}
	raw, _ := e.MarshalBinary()
	for b.Loop() {
		c := &Capture{bus: &eventBus{enabled: true, epoch: 7, data: raw, fail: -1}}
		ctx, cancel := context.WithCancel(context.Background())
		c.DrainDecodedEvents(ctx, 7, eventWriter(func(p []byte) (int, error) { cancel(); return len(p), nil }))
		cancel()
	}
}
