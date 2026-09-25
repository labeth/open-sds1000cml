// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import (
	"encoding/binary"
	"fmt"
)

// EnableDecodedEvents begins a fresh stream when enabled, returning its epoch.
// Caller must configure the decoder before enabling: FPGA configuration is held
// stable during streaming. This does not arm or discard a retained SRAM record.
// TRLC-LINKS: REQ-SDS-013
func (c *Capture) EnableDecodedEvents(enabled bool) (uint32, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enableDecodedEvents(enabled)
}

// Called while capture.mu is held, including configure/enable/arm ordering.
// TRLC-LINKS: REQ-SDS-013
func (c *Capture) enableDecodedEvents(enabled bool) (uint32, error) {
	capability, err := c.read(63)
	if err != nil {
		return 0, err
	}
	if capability != 0x4501 {
		return 0, fmt.Errorf("sramcapture: fabric lacks decoded event transport")
	}
	if err := c.write(57, 0); err != nil {
		return 0, err
	}
	if err := c.write(60, 2); err != nil {
		return 0, err
	}
	if enabled {
		// Allocate the normal batch buffer before the producer starts.
		if cap(c.decodedScratch) < 512*DecodedEventBytes {
			c.decodedScratch = make([]byte, 512*DecodedEventBytes)
		}
		if err := c.write(57, 1); err != nil {
			return 0, err
		}
	}
	return c.read32(61)
}

// ReadDecodedEvent returns one complete event or available=false without waiting.
// All accesses share the capture lock. A leftover partial read is rewound before
// retrying. A failed final halfword read may already have consumed the event:
// callers must report that transport error, never treat it as an empty queue.
// Epoch is the value returned when this stream was enabled.
// TRLC-LINKS: REQ-SDS-013
func (c *Capture) ReadDecodedEvent(epoch uint32) (event DecodedEvent, available bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	active, err := c.read(57)
	if err != nil {
		return event, false, err
	}
	if active != 1 {
		return event, false, fmt.Errorf("sramcapture: decoded stream is disabled")
	}
	current, err := c.read32(61)
	if err != nil {
		return event, false, err
	}
	if current != epoch {
		return event, false, fmt.Errorf("sramcapture: decoded epoch changed: %d != %d", current, epoch)
	}
	status, err := c.read(58)
	if err != nil {
		return event, false, err
	}
	if status&6 != 0 {
		return event, false, fmt.Errorf("sramcapture: decoded transport error status %04x", status)
	}
	if status&1 == 0 {
		return event, false, nil
	}
	if status&0xf0 != 0 {
		if err := c.write(60, 1); err != nil {
			return event, false, err
		}
	}
	var data [DecodedEventBytes]byte
	if pop, ok := c.bus.(interface{ PopBytesChecked(uint16, []byte) error }); ok {
		// Availability guarantees exactly one complete event. Never retry a
		// failed DMA: its final pop may already have released that event.
		if err := pop.PopBytesChecked(59, data[:]); err != nil {
			return event, false, fmt.Errorf("sramcapture: event DMA (consumption uncertain): %w", err)
		}
		c.beats.Add(DecodedEventBytes / 2)
	} else {
		for i := 0; i < len(data); i += 2 {
			word, err := c.read(59)
			if err != nil {
				return event, false, fmt.Errorf("sramcapture: event halfword %d (consumption uncertain): %w", i/2, err)
			}
			binary.LittleEndian.PutUint16(data[i:], word)
		}
	}
	event, err = ParseDecodedEvent(data[:])
	if err != nil {
		return DecodedEvent{}, false, err
	}
	if event.Epoch != epoch {
		return DecodedEvent{}, false, fmt.Errorf("sramcapture: stale decoded event epoch %d", event.Epoch)
	}
	return event, true, nil
}

// ReadDecodedEvents reserves only complete host-FIFO records and drains them in
// one checked DMA. A failed batch is never retried because consumption is unknown.
// The acquisition owner serializes this operation with enable, rearm and recall.
// TRLC-LINKS: REQ-SDS-013
func (c *Capture) ReadDecodedEvents(epoch uint32, limit int) ([]DecodedEvent, error) {
	return c.readDecodedEvents(epoch, limit, nil)
}

// ReadDecodedEventsInto uses caller-owned event storage. The returned slice
// aliases dst; it remains valid until the caller reuses that storage.
// TRLC-LINKS: REQ-SDS-013
func (c *Capture) ReadDecodedEventsInto(epoch uint32, dst []DecodedEvent) ([]DecodedEvent, error) {
	return c.readDecodedEvents(epoch, len(dst), dst)
}

// TRLC-LINKS: REQ-SDS-013
func (c *Capture) readDecodedEvents(epoch uint32, limit int, dst []DecodedEvent) ([]DecodedEvent, error) {
	if limit < 1 || limit > 512 {
		return nil, fmt.Errorf("sramcapture: invalid event batch limit")
	}
	if isolated, ok := c.bus.(interface {
		ReadIsolatedDecodedBytes(uint32, []byte) (int, bool, error)
	}); ok {
		c.mu.Lock()
		if cap(c.decodedScratch) < limit*DecodedEventBytes {
			c.decodedScratch = make([]byte, limit*DecodedEventBytes)
		}
		data := c.decodedScratch[:limit*DecodedEventBytes]
		n, owned, err := isolated.ReadIsolatedDecodedBytes(epoch, data)
		if owned {
			defer c.mu.Unlock()
			if err != nil {
				return nil, err
			}
			if n < 0 || n > len(data) || n%DecodedEventBytes != 0 {
				return nil, fmt.Errorf("sramcapture: invalid isolated event batch")
			}
			c.beats.Add(uint64(n/2 + 1))
			events := dst
			if events == nil {
				events = make([]DecodedEvent, n/DecodedEventBytes)
			} else {
				events = events[:n/DecodedEventBytes]
			}
			for i := range events {
				event, err := ParseDecodedEvent(data[i*DecodedEventBytes : (i+1)*DecodedEventBytes])
				if err != nil {
					return nil, err
				}
				if event.Epoch != epoch {
					return nil, fmt.Errorf("sramcapture: isolated epoch mismatch")
				}
				events[i] = event
			}
			return events, nil
		}
		c.mu.Unlock()
	}
	c.mu.Lock()
	capability, err := c.read(74)
	if err != nil {
		c.mu.Unlock()
		return nil, err
	}
	if capability != 0x4503 {
		c.mu.Unlock()
		event, available, err := c.ReadDecodedEvent(epoch)
		if err != nil || !available {
			return nil, err
		}
		if dst == nil {
			return []DecodedEvent{event}, nil
		}
		dst[0] = event
		return dst[:1], nil
	}
	defer c.mu.Unlock()
	active, err := c.read(57)
	if err != nil {
		return nil, err
	}
	if active != 1 {
		return nil, fmt.Errorf("sramcapture: decoded stream is disabled")
	}
	current, err := c.read32(61)
	if err != nil {
		return nil, err
	}
	if current != epoch {
		return nil, fmt.Errorf("sramcapture: decoded epoch changed")
	}
	status, err := c.read(58)
	if err != nil {
		return nil, err
	}
	if status&6 != 0 {
		return nil, fmt.Errorf("sramcapture: decoded transport error status %04x", status)
	}
	if status&0xf0 != 0 {
		if err := c.write(60, 1); err != nil {
			return nil, err
		}
	}
	// Freeze the count in the host clock domain before the asynchronous GPMC
	// read. A live binary counter can glitch across a multi-bit transition.
	if err := c.write(76, 1); err != nil {
		return nil, err
	}
	count, err := c.read(75)
	if err != nil {
		return nil, err
	}
	if count > 512 {
		return nil, fmt.Errorf("sramcapture: invalid event FIFO count %d", count)
	}
	n := min(int(count), limit)
	if n == 0 {
		return nil, nil
	}
	if cap(c.decodedScratch) < limit*DecodedEventBytes {
		c.decodedScratch = make([]byte, limit*DecodedEventBytes)
	}
	data := c.decodedScratch[:n*DecodedEventBytes]
	if pop, ok := c.bus.(interface{ PopBytesChecked(uint16, []byte) error }); ok {
		if err := pop.PopBytesChecked(59, data); err != nil {
			return nil, fmt.Errorf("sramcapture: event batch DMA (consumption uncertain): %w", err)
		}
		c.beats.Add(uint64(len(data) / 2))
	} else {
		for i := 0; i < len(data); i += 2 {
			v, err := c.read(59)
			if err != nil {
				return nil, fmt.Errorf("sramcapture: event batch read (consumption uncertain): %w", err)
			}
			binary.LittleEndian.PutUint16(data[i:], v)
		}
	}
	events := dst
	if events == nil {
		events = make([]DecodedEvent, n)
	} else {
		events = events[:n]
	}
	for i := range events {
		event, err := ParseDecodedEvent(data[i*DecodedEventBytes : (i+1)*DecodedEventBytes])
		if err != nil {
			return nil, fmt.Errorf("sramcapture: batch event %d/%d header %x: %w", i, n, data[i*DecodedEventBytes:i*DecodedEventBytes+12], err)
		}
		if event.Epoch != epoch {
			return nil, fmt.Errorf("sramcapture: stale decoded batch epoch %d", event.Epoch)
		}
		events[i] = event
	}
	return events, nil
}
