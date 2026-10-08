// Package decodedlines builds the decoded-stream hexdump transcript from
// decoded events (ADR-PANEL-DECODED-STREAM, ADR-STREAM-LINES-IN-WORKER). The
// isolated acquisition worker builds it from the events it drains and the GUI
// process appends the finished lines, so both use this one implementation.
// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package decodedlines

import (
	"encoding/binary"
	"fmt"
	"strings"

	"open-sds/app/internal/sramcapture"
)

// Line is one hexdump line: up to sixteen units of one frame.
type Line struct {
	Sample uint64 // first unit's sample ordinal
	Vals   [16]uint32
	N      uint8
	Errs   uint16 // slot is a decode error, not a unit
	Naks   uint16 // I2C: the byte was not acknowledged
	Note   string // I2C address, USB PID, loss
}

// Counters are the transcript's running totals.
type Counters struct {
	Events, Units, Lost, Errors uint64
}

// Transcript is the line history, oldest dropped first, with absolute line
// indices that survive the ring's wrap.
type Transcript struct {
	proto              int
	perLine, hexDigits int
	ascii              bool
	capacity           int
	lines              []Line // ring once full
	head               int    // ring start
	dropped            int    // lines overwritten: the first absolute index held
	cur                Line
	first              uint64
	haveFirst          bool
	Counters
	trig, recFirst, recLast uint64
	marked                  bool
	triggerHint             bool // a trigger or retained-record event arrived
}

// New makes a transcript for a serial-registry protocol holding capacity
// lines.
// TRLC-LINKS: REQ-SDS-018
func New(proto, capacity int) *Transcript {
	t := &Transcript{capacity: max(capacity, 1)}
	t.lines = make([]Line, 0, t.capacity) // allocated once: growth would copy megabytes mid-stream
	t.Reset(proto)
	return t
}

// Reset empties the transcript for a new stream of proto, keeping its
// storage: the worker reuses one transcript and allocates nothing while
// streaming.
// TRLC-LINKS: REQ-SDS-018
func (t *Transcript) Reset(proto int) {
	lines, capacity := t.lines[:0], t.capacity
	*t = Transcript{proto: proto, perLine: 16, hexDigits: 2, ascii: true, capacity: capacity, lines: lines}
	switch proto {
	case 5: // SENT nibbles
		t.hexDigits, t.ascii = 1, false
	case 7: // MIL-1553 words
		t.perLine, t.hexDigits, t.ascii = 8, 4, false
	case 8: // ARINC 429 words
		t.perLine, t.hexDigits, t.ascii = 4, 8, false
	}
}

// Proto is the transcript's protocol.
// TRLC-LINKS: REQ-SDS-018
func (t *Transcript) Proto() int { return t.proto }

// Count is the lines held; Base the absolute index of the oldest; Next the
// index the next line will get.
// TRLC-LINKS: REQ-SDS-018
func (t *Transcript) Count() int { return len(t.lines) }

// Base is the absolute index of the oldest line held.
// TRLC-LINKS: REQ-SDS-018
func (t *Transcript) Base() int { return t.dropped }

// Next is the absolute index the next completed line will get.
// TRLC-LINKS: REQ-SDS-018
func (t *Transcript) Next() int { return t.dropped + len(t.lines) }

// At is the line at position i from the oldest held (0 <= i < Count).
// TRLC-LINKS: REQ-SDS-018
func (t *Transcript) At(i int) *Line { return &t.lines[(t.head+i)%len(t.lines)] }

// TakeTriggerHint reports and clears a trigger or retained-record event.
// TRLC-LINKS: REQ-SDS-018
func (t *Transcript) TakeTriggerHint() bool {
	h := t.triggerHint
	t.triggerHint = false
	return h
}

// Add folds decoded events into lines.
// TRLC-LINKS: REQ-SDS-018
func (t *Transcript) Add(events []sramcapture.DecodedEvent) {
	for _, e := range events {
		t.Events++
		if e.Kind != sramcapture.EventLoss && !t.haveFirst {
			t.first, t.haveFirst = e.Sample, true
		}
		switch e.Kind {
		case sramcapture.EventStart:
			t.Flush()
			if name, ok := usbPIDs[e.Value&0xff]; t.proto == 9 && ok { // USB: the packet's PID
				t.cur.Sample, t.cur.Note = e.Sample, name
			}
		case sramcapture.EventData:
			t.Units++
			// I2C DATA metadata rides in Count: bit 0 address byte, bit 1 NACK.
			if t.proto == 2 && e.Count&1 != 0 { // address: a new line, noted
				t.Flush()
				rw := "W"
				if e.Value&1 != 0 {
					rw = "R"
				}
				t.cur.Sample, t.cur.Note = e.Sample, fmt.Sprintf("@%02X%s", (e.Value>>1)&0x7f, rw)
				if e.Count&2 != 0 {
					t.cur.Note += "!"
				}
				continue
			}
			if t.proto == 2 && e.Count&2 != 0 {
				t.cur.Naks |= 1 << t.cur.N
			}
			t.slot(e.Sample, e.Value, false)
		case sramcapture.EventEnd:
			t.Flush()
		case sramcapture.EventError:
			t.Errors++
			if t.cur.N == 0 && t.cur.Note != "" { // an error closing a unit-less packet marks its note
				t.cur.Note += "!"
				t.Flush()
				continue
			}
			t.slot(e.Sample, 0, true)
		case sramcapture.EventTrigger, sramcapture.EventRetained:
			t.triggerHint = true
		case sramcapture.EventLoss:
			t.Lost += uint64(e.Count)
			t.Flush()
			t.cur.Sample, t.cur.Note = e.Sample, fmt.Sprintf("LOST %d", e.Count)
			t.Flush()
		}
	}
}

// TRLC-LINKS: REQ-SDS-018
func (t *Transcript) slot(sample uint64, v uint32, isErr bool) {
	if t.cur.N == 0 && t.cur.Note == "" {
		t.cur.Sample = sample
	}
	if isErr {
		t.cur.Errs |= 1 << t.cur.N
	}
	t.cur.Vals[t.cur.N] = v
	t.cur.N++
	if int(t.cur.N) >= t.perLine {
		t.Flush()
	}
}

// Flush completes the line being built.
// TRLC-LINKS: REQ-SDS-018
func (t *Transcript) Flush() {
	if t.cur.N == 0 && t.cur.Note == "" {
		return
	}
	t.push(t.cur)
	t.cur = Line{}
}

// TRLC-LINKS: REQ-SDS-018
func (t *Transcript) push(l Line) {
	if len(t.lines) < t.capacity {
		t.lines = append(t.lines, l)
	} else {
		t.lines[t.head] = l
		t.head = (t.head + 1) % len(t.lines)
		t.dropped++
	}
}

// Append adds a finished line built elsewhere (the worker) with its absolute
// index; a jump past Next drops the gap as missed lines (the caller counts
// them) and keeps indices aligned with the builder.
// TRLC-LINKS: REQ-SDS-018
func (t *Transcript) Append(index int, l Line) {
	if index < t.Next() {
		return // already held
	}
	if len(t.lines) == 0 || index-t.Next() >= t.capacity { // start (again) at index
		t.lines, t.head, t.dropped = t.lines[:0], 0, index
	}
	for t.Next() < index { // keep absolute indices: mark the missed lines
		t.push(Line{Sample: l.Sample, Note: "GAP"})
	}
	if !t.haveFirst {
		t.first, t.haveFirst = l.Sample, true
	}
	t.push(l)
}

// SetCounters replaces the totals with a builder's.
// TRLC-LINKS: REQ-SDS-018
func (t *Transcript) SetCounters(c Counters) { t.Counters = c }

// Mark records the trigger and the retained record's span (sample ordinals).
// TRLC-LINKS: REQ-SDS-018
func (t *Transcript) Mark(trig, first, last uint64) {
	t.trig, t.recFirst, t.recLast, t.marked = trig, first, last, true
}

// TriggerLine is the held position of the last line starting at or before
// the trigger, or -1.
// TRLC-LINKS: REQ-SDS-018
func (t *Transcript) TriggerLine() int {
	if !t.marked || len(t.lines) == 0 || t.At(0).Sample > t.trig {
		return -1
	}
	lo, hi := 0, len(t.lines)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if t.At(mid).Sample <= t.trig {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo
}

// Header names the columns, matching Format's layout.
// TRLC-LINKS: REQ-SDS-018
func (t *Transcript) Header() string {
	w := t.perLine*(t.hexDigits+1) - 1
	h := fmt.Sprintf("  %11s %-6s %-*s", "t ms", "", w, "hex")
	if t.ascii {
		h += "  text"
	}
	return h
}

// Format renders held line i: markers, time, note, hex units, text. Times
// count from the trigger once there is one, else from the first event.
// TRLC-LINKS: REQ-SDS-018
func (t *Transcript) Format(i int) string {
	l := t.At(i)
	g1, g2 := byte(' '), byte(' ')
	ms := float64(l.Sample-t.first) * 2e-6
	if t.marked {
		if i == t.TriggerLine() {
			g1 = '>'
		}
		if l.Sample >= t.recFirst && l.Sample < t.recLast {
			g2 = '|'
		}
		ms = (float64(l.Sample) - float64(t.trig)) * 2e-6
	}
	var hex, text strings.Builder
	for k := 0; k < int(l.N); k++ {
		if k > 0 {
			hex.WriteByte(' ')
		}
		if l.Errs&(1<<k) != 0 {
			hex.WriteString(strings.Repeat("!", t.hexDigits))
			text.WriteByte('!')
			continue
		}
		fmt.Fprintf(&hex, "%0*X", t.hexDigits, l.Vals[k])
		if l.Naks&(1<<k) != 0 {
			text.WriteByte('~')
		} else if b := l.Vals[k]; b >= 0x20 && b < 0x7f {
			text.WriteByte(byte(b))
		} else {
			text.WriteByte('.')
		}
	}
	w := t.perLine*(t.hexDigits+1) - 1
	s := fmt.Sprintf("%c%c%+11.4f %-6s %-*s", g1, g2, ms, l.Note, w, hex.String())
	if t.ascii {
		s += "  " + text.String()
	}
	return s
}

// usbPIDs names USB packet identifiers for the note column.
var usbPIDs = map[uint32]string{0xE1: "OUT", 0x69: "IN", 0xA5: "SOF", 0x2D: "SETUP", 0xC3: "DATA0", 0x4B: "DATA1",
	0x87: "DATA2", 0x0F: "MDATA", 0xD2: "ACK", 0x5A: "NAK", 0x1E: "STALL", 0x96: "NYET", 0x3C: "PRE"}

// Wire format of a line batch (worker to GUI process), little-endian:
//
//	header: base u32, next u32, events u64, units u64, lost u64, errors u64,
//	        flags u8 (bit 0 trigger hint), count u16
//	line:   index u32, sample u64, n u8 (bit 7: values are one byte each),
//	        errs u16, naks u16, note-len u8, note bytes, n values (u8 or u32)
//
// One-byte values make a 16-byte UART line 34 bytes instead of 82, so one
// worker packet carries about 1900 lines.
const batchHeader = 4 + 4 + 8*4 + 1 + 2

// MaxLineBytes bounds one encoded line.
const MaxLineBytes = 4 + 8 + 1 + 2 + 2 + 1 + 255 + 16*4

// Batch is a decoded line batch.
type Batch struct {
	Base, Next  int
	Counters    Counters
	TriggerHint bool
	Indices     []int
	Lines       []Line
}

// EncodeLines writes held lines from absolute index from into dst until it is
// full, and reports the bytes used; the trigger hint is taken.
// TRLC-LINKS: REQ-SDS-018
func (t *Transcript) EncodeLines(from int, dst []byte) int {
	if len(dst) < batchHeader {
		return 0
	}
	from = min(max(from, t.Base()), t.Next())
	off, count := batchHeader, 0
	for i := from; i < t.Next(); i++ {
		l := t.At(i - t.dropped)
		note := l.Note
		if len(note) > 255 {
			note = note[:255]
		}
		width := 1
		for k := 0; k < int(l.N); k++ {
			if l.Vals[k] > 0xff {
				width = 4
				break
			}
		}
		need := 4 + 8 + 1 + 2 + 2 + 1 + len(note) + int(l.N)*width
		if off+need > len(dst) || count == 0xffff {
			break
		}
		binary.LittleEndian.PutUint32(dst[off:], uint32(i))
		binary.LittleEndian.PutUint64(dst[off+4:], l.Sample)
		dst[off+12] = l.N
		if width == 1 {
			dst[off+12] |= 0x80
		}
		binary.LittleEndian.PutUint16(dst[off+13:], l.Errs)
		binary.LittleEndian.PutUint16(dst[off+15:], l.Naks)
		dst[off+17] = byte(len(note))
		off += 18 + copy(dst[off+18:], note)
		for k := 0; k < int(l.N); k++ {
			if width == 1 {
				dst[off] = byte(l.Vals[k])
				off++
			} else {
				binary.LittleEndian.PutUint32(dst[off:], l.Vals[k])
				off += 4
			}
		}
		count++
	}
	binary.LittleEndian.PutUint32(dst[0:], uint32(t.Base()))
	binary.LittleEndian.PutUint32(dst[4:], uint32(t.Next()))
	binary.LittleEndian.PutUint64(dst[8:], t.Events)
	binary.LittleEndian.PutUint64(dst[16:], t.Units)
	binary.LittleEndian.PutUint64(dst[24:], t.Lost)
	binary.LittleEndian.PutUint64(dst[32:], t.Errors)
	if t.TakeTriggerHint() {
		dst[40] = 1
	} else {
		dst[40] = 0
	}
	binary.LittleEndian.PutUint16(dst[41:], uint16(count))
	return off
}

// DecodeLines parses an EncodeLines batch.
// TRLC-LINKS: REQ-SDS-018
func DecodeLines(b []byte) (Batch, error) {
	var bt Batch
	if len(b) < batchHeader {
		return bt, fmt.Errorf("decodedlines: short batch")
	}
	bt.Base = int(binary.LittleEndian.Uint32(b[0:]))
	bt.Next = int(binary.LittleEndian.Uint32(b[4:]))
	bt.Counters = Counters{binary.LittleEndian.Uint64(b[8:]), binary.LittleEndian.Uint64(b[16:]),
		binary.LittleEndian.Uint64(b[24:]), binary.LittleEndian.Uint64(b[32:])}
	bt.TriggerHint = b[40]&1 != 0
	count := int(binary.LittleEndian.Uint16(b[41:]))
	bt.Indices, bt.Lines = make([]int, 0, count), make([]Line, 0, count)
	off := batchHeader
	for c := 0; c < count; c++ {
		if off+18 > len(b) {
			return bt, fmt.Errorf("decodedlines: truncated line %d", c)
		}
		var l Line
		idx := int(binary.LittleEndian.Uint32(b[off:]))
		l.Sample = binary.LittleEndian.Uint64(b[off+4:])
		l.N = b[off+12] & 0x7f
		width := 4
		if b[off+12]&0x80 != 0 {
			width = 1
		}
		l.Errs = binary.LittleEndian.Uint16(b[off+13:])
		l.Naks = binary.LittleEndian.Uint16(b[off+15:])
		nl := int(b[off+17])
		off += 18
		if l.N > 16 || off+nl+int(l.N)*width > len(b) {
			return bt, fmt.Errorf("decodedlines: bad line %d", c)
		}
		if nl > 0 {
			l.Note = string(b[off : off+nl])
		}
		off += nl
		for k := 0; k < int(l.N); k++ {
			if width == 1 {
				l.Vals[k] = uint32(b[off])
				off++
			} else {
				l.Vals[k] = binary.LittleEndian.Uint32(b[off:])
				off += 4
			}
		}
		bt.Indices = append(bt.Indices, idx)
		bt.Lines = append(bt.Lines, l)
	}
	return bt, nil
}
