// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
package analysis

// Packets: the decoded spans of one record grouped into the messages of the
// BUS workspace's packet list (REQ-SDS-214). Byte-stream buses (UART, SPI)
// group bytes that follow each other closely: an idle gap of 1.5 byte times
// ends a packet, and so does a UART break. I2C groups START to STOP (or a
// repeated START). The framed protocols take their own frames: a CAN frame
// from SOF to ACK, a SENT frame from its SYNC, a MIL-STD-1553 message from its
// command word, an ARINC 429 word, and a Manchester, FlexRay or USB frame.
//
// A packet is complete when the record holds all of it: a start marker or
// enough idle line before it, and an end marker, another packet or enough idle
// line after it. A packet cut by the record's edges is still returned, marked
// incomplete.

import (
	"math"
	"sort"

	"open-sds/app/internal/decode"
)

// Packet faults (Packet.Err).
const (
	FaultNAK    = "NAK"     // I2C: the address was not acknowledged; CAN: no node acknowledged
	FaultNoStop = "no STOP" // I2C: the record ended inside the transaction
	FaultCRC    = "CRC"     // CAN, FlexRay or USB check sequence mismatch (CAN: or a stuffing violation)
	FaultPID    = "PID"     // USB: the PID check nibble does not match
	FaultStuff  = "stuff"   // USB: a bit-stuffing violation
	FaultCoding = "coding"  // Manchester: a cell without its mid-cell transition
	FaultParity = "parity"  // ARINC 429 or MIL-STD-1553 word parity
)

// Packet is one message of a decoded bus.
//
// Bytes holds the payload: UART and SPI bytes, I2C data bytes (the address is
// in Addr), CAN data, SENT nibbles (one per byte, CRC nibble last), the
// FlexRay frame's bytes (header, payload and CRC), the USB bytes after the PID
// (CRC included), MIL-STD-1553 words and the ARINC 429 data field. Words
// wider than a byte are split most significant byte first: 16-bit 1553 words
// and wide UART or Manchester words into two bytes, the 19-bit ARINC data
// field into three. ByteErr flags the bytes the decoder marked bad (framing,
// parity, CRC); Err names a fault of the packet as a whole (the Fault
// constants).
//
// Addr is the I2C 7-bit address, the CAN identifier, the FlexRay frame ID,
// the USB PID, the ARINC 429 label, or the RT address of a 1553 command or
// status word. Read is an I2C read, a CAN remote frame or a 1553 transmit
// command.
// TRLC-LINKS: REQ-SDS-214
type Packet struct {
	I0, I1   int // first and last sample of the packet
	Bytes    []byte
	ByteErr  []bool
	Addr     int
	HasAddr  bool
	Read     bool
	Complete bool // not cut by the record's edges
	Err      string
}

// Packets groups the spans of r into packets in record order. recordLen is
// the number of samples the decode ran on; with 0 the record is taken to end
// at the last span, so its last packet counts as cut.
// TRLC-LINKS: REQ-SDS-214
func Packets(r decode.Result, recordLen int) []Packet {
	if len(r.Spans) == 0 {
		return nil
	}
	if recordLen <= 0 {
		for _, sp := range r.Spans {
			recordLen = max(recordLen, sp.I0+1, sp.I1+1)
		}
	}
	recordLen = max(recordLen, 1)
	g := grouper{n: recordLen, bit: r.SPB, width: wordWidth(r)}
	if !(g.bit > 0) || !finite(g.bit) {
		g.bit = 0
	}
	switch r.Proto {
	case "i2c":
		g.i2c(r.Spans)
	case "canfd":
		g.can(r.Spans)
	case "manchester", "flexray", "usbls", "mil1553", "arinc429", "sent":
		g.framed(r.Proto, r.Spans)
	default: // uart, spi, and any byte stream to come
		g.gaps(r.Proto == "uart", r.Spans)
	}
	return g.out
}

// wordWidth is the number of bytes a decoded word takes in Packet.Bytes.
// TRLC-LINKS: REQ-SDS-214
func wordWidth(r decode.Result) int {
	switch r.Proto {
	case "mil1553":
		return 2
	case "arinc429":
		return 3
	case "i2c", "spi", "canfd", "usbls", "flexray", "sent":
		return 1
	}
	top := 0 // UART and Manchester words take 1..16 bits
	for _, sp := range r.Spans {
		if isWord(sp.Kind) {
			top = max(top, sp.Val)
		}
	}
	switch {
	case top > 0xffff:
		return 3
	case top > 0xff:
		return 2
	}
	return 1
}

// isWord reports whether a span of a byte-stream decode carries a data word.
// TRLC-LINKS: REQ-SDS-214
func isWord(kind string) bool {
	return kind == "data" || kind == "frame-error" || kind == "parity-error"
}

// grouper builds the packets of one decode.
// TRLC-LINKS: REQ-SDS-214
type grouper struct {
	n     int     // record length (samples)
	bit   float64 // samples per bit (0 when unknown)
	width int     // bytes per word
	out   []Packet
	cur   *Packet // the open packet, last in out
	start bool    // the open packet's start is not cut
}

// open starts a packet at sample i0; startOK says its start is not cut.
// TRLC-LINKS: REQ-SDS-214
func (g *grouper) open(i0 int, startOK bool) {
	g.out = append(g.out, Packet{I0: i0, I1: i0})
	g.cur, g.start = &g.out[len(g.out)-1], startOK
}

// close ends the open packet; endOK says its end is not cut.
// TRLC-LINKS: REQ-SDS-214
func (g *grouper) close(endOK bool) {
	if g.cur == nil {
		return
	}
	p := g.cur
	p.I0, p.I1 = clampInt(p.I0, 0, g.n-1), clampInt(p.I1, 0, g.n-1)
	p.Complete = g.start && endOK
	g.cur = nil
}

// span widens the open packet to cover sp.
// TRLC-LINKS: REQ-SDS-214
func (g *grouper) span(sp decode.Span) {
	g.cur.I0 = min(g.cur.I0, sp.I0, sp.I1)
	g.cur.I1 = max(g.cur.I1, sp.I0, sp.I1)
}

// word appends a decoded word to the open packet, most significant byte
// first, and widens the packet to its span.
// TRLC-LINKS: REQ-SDS-214
func (g *grouper) word(sp decode.Span, bad bool) {
	g.span(sp)
	for k := g.width - 1; k >= 0; k-- {
		g.cur.Bytes = append(g.cur.Bytes, byte(sp.Val>>(8*k)))
		g.cur.ByteErr = append(g.cur.ByteErr, bad)
	}
}

// markLast flags the bytes of the open packet's last word.
// TRLC-LINKS: REQ-SDS-214
func (g *grouper) markLast() {
	for k := len(g.cur.ByteErr) - 1; k >= 0 && k >= len(g.cur.ByteErr)-g.width; k-- {
		g.cur.ByteErr[k] = true
	}
}

// room reports whether the record runs at least d samples past sample i.
// TRLC-LINKS: REQ-SDS-214
func (g *grouper) room(i int, d float64) bool { return float64(g.n-1-i) >= d }

// gaps groups a byte stream (UART, SPI) by idle gaps: a packet ends where the
// line idles for 1.5 byte times (the median byte span plus a bit), at a UART
// break (a framing error on a zero byte: the line held low), and where the
// decoder could not complete a frame ("gap", cut by the record's edges or
// before the decode synced), which also leaves the packets touching it cut.
// TRLC-LINKS: REQ-SDS-214
func (g *grouper) gaps(uart bool, spans []decode.Span) {
	var ws []int
	for _, sp := range spans {
		if isWord(sp.Kind) {
			ws = append(ws, abs(sp.I1-sp.I0))
		}
	}
	if len(ws) == 0 {
		return
	}
	sort.Ints(ws)
	med := float64(ws[len(ws)/2])
	bit := g.bit
	if bit == 0 {
		bit = med / 9 // a start bit and 8 data bits
	}
	idle := math.Max(1, 1.5*(med+bit))
	const (
		evNone = iota
		evWord
		evBreak
		evCut
	)
	prev, prevEnd := evNone, 0 // what came before, and where it ended
	for _, sp := range spans {
		switch {
		case sp.Kind == "gap":
			if g.cur != nil {
				g.close(float64(sp.I0-g.cur.I1) > idle)
			}
			prev, prevEnd = evCut, max(prevEnd, sp.I1)
		case uart && sp.Kind == "frame-error" && sp.Val == 0:
			g.close(true)
			prev, prevEnd = evBreak, max(prevEnd, sp.I1)
		case isWord(sp.Kind):
			if g.cur == nil || float64(sp.I0-g.cur.I1) > idle {
				g.close(true)
				startOK := false
				switch prev {
				case evNone:
					startOK = float64(sp.I0) >= idle
				case evWord, evBreak:
					startOK = true
				case evCut:
					startOK = float64(sp.I0-prevEnd) > idle
				}
				g.open(sp.I0, startOK)
			}
			g.word(sp, sp.Kind != "data")
			prev, prevEnd = evWord, max(prevEnd, sp.I1)
		}
	}
	if g.cur != nil {
		g.close(g.room(g.cur.I1, idle))
	}
}

// i2c groups START to STOP; a repeated START ends one packet and opens the
// next, each with its own address.
// TRLC-LINKS: REQ-SDS-214
func (g *grouper) i2c(spans []decode.Span) {
	addrPhase := false // the last byte seen was the address
	for _, sp := range spans {
		switch sp.Kind {
		case "start":
			g.close(true) // a repeated START
			g.open(sp.I0, true)
			addrPhase = false
			continue
		case "stop":
			if g.cur != nil {
				g.span(sp)
				g.close(true)
			}
			continue
		case "frame-error": // "(no STOP)": the record ended inside the transaction
			if g.cur != nil {
				g.cur.Err = FaultNoStop
				g.close(false)
			}
			continue
		}
		if g.cur == nil {
			g.open(sp.I0, false) // bytes without a START in the record
		}
		g.span(sp)
		switch sp.Kind {
		case "addr":
			g.cur.Addr, g.cur.HasAddr = sp.Val&0x7f, true
			addrPhase = true
		case "rw":
			g.cur.Read = sp.Text == "R"
		case "data":
			g.word(sp, false)
			addrPhase = false
		case "nak":
			switch {
			case addrPhase:
				g.cur.Err = FaultNAK // no device answered
			case !g.cur.Read && len(g.cur.Bytes) > 0:
				g.markLast() // a write byte refused (a read's last NAK is the master's)
			}
		}
	}
	if g.cur != nil {
		g.close(false)
	}
}

// can takes each frame from its SOF. A classic frame is complete with its ACK
// slot; an FD frame, decoded through its data field only, when the record
// holds room for its CRC and ACK after it.
// TRLC-LINKS: REQ-SDS-214
func (g *grouper) can(spans []decode.Span) {
	acked, fd := false, false
	end := func() {
		if g.cur != nil {
			g.close(acked || (fd && g.room(g.cur.I1, 32*g.bit)))
		}
	}
	for _, sp := range spans {
		if sp.Kind == "sof" {
			end()
			g.open(sp.I0, true)
			acked, fd = false, false
		}
		if g.cur == nil {
			g.open(sp.I0, false)
		}
		g.span(sp)
		switch sp.Kind {
		case "id":
			g.cur.Addr, g.cur.HasAddr = sp.Val, true
		case "rtr":
			g.cur.Read = sp.Val == 1
		case "fd":
			fd = true
		case "data":
			g.word(sp, false)
		case "frame-error": // the CRC field: a mismatch or a stuffing violation
			g.cur.Err = FaultCRC
		case "ack":
			acked = true
		case "nak":
			acked = true
			if g.cur.Err == "" {
				g.cur.Err = FaultNAK
			}
		}
	}
	end()
}

// framed takes the frames of the self-framing protocols, each from its start
// marker: the room a frame needs around it (in bits) says when the record's
// edges may have cut it.
// TRLC-LINKS: REQ-SDS-214
func (g *grouper) framed(proto string, spans []decode.Span) {
	var head, tail float64 // bits of record needed before and after a frame
	switch proto {
	case "manchester":
		head, tail = 2.5, 2.5 // the decoder's own inter-frame idle
	case "flexray":
		tail = 10 // a byte: the frame may go on
	case "usbls":
		tail = 3 // the end of packet
	case "mil1553":
		tail = 20 // a word: the message may go on
	case "sent":
		tail = 28 // the longest nibble pulse: the frame may go on
	}
	head *= g.bit
	tail *= g.bit
	end := func(next bool) { // next: another frame follows in the record
		if g.cur != nil {
			g.close(next || g.room(g.cur.I1, tail))
		}
	}
	for _, sp := range spans {
		begins := false
		switch {
		case sp.Kind == "gap":
			end(true)
			continue
		case proto == "arinc429":
			begins = sp.Kind == "addr" // each word opens with its label
		case proto == "mil1553":
			begins = sp.Kind == "start" && (sp.Text != "dsync" || g.cur == nil)
		default:
			begins = sp.Kind == "start" || sp.Kind == "sync"
		}
		if begins {
			end(true)
			// a data word with no command word before it: the message was cut
			g.open(sp.I0, float64(sp.I0) >= head && !(proto == "mil1553" && sp.Text == "dsync"))
		}
		if g.cur == nil {
			g.open(sp.I0, false)
		}
		g.span(sp)
		switch sp.Kind {
		case "data":
			if proto == "mil1553" && len(g.cur.Bytes) == 0 && g.start {
				// the command (or status) word: RT address and T/R bit
				g.cur.Addr, g.cur.HasAddr = (sp.Val>>11)&0x1f, true
				g.cur.Read = (sp.Val>>10)&1 == 1
			}
			g.word(sp, false)
		case "crc": // SENT's CRC nibble
			g.word(sp, false)
		case "crc-error": // USB data CRC mismatch
			g.word(sp, true)
			g.cur.Err = FaultCRC
		case "addr":
			g.cur.Addr, g.cur.HasAddr = sp.Val, true
		case "frame-error":
			g.frameError(proto, sp)
		}
	}
	end(false)
}

// frameError records a framed protocol's error span on the open packet.
// TRLC-LINKS: REQ-SDS-214
func (g *grouper) frameError(proto string, sp decode.Span) {
	switch proto {
	case "sent": // an out-of-range nibble, or a CRC nibble that does not check
		g.word(sp, true)
	case "manchester":
		g.cur.Err = FaultCoding
	case "flexray": // the header span, flagged for a header or frame CRC error
		g.cur.Addr, g.cur.HasAddr, g.cur.Err = sp.Val, true, FaultCRC
	case "usbls":
		if sp.Text == "STUFF!" {
			g.cur.Err = FaultStuff
		} else {
			g.cur.Addr, g.cur.HasAddr, g.cur.Err = sp.Val, true, FaultPID
		}
	case "mil1553":
		g.markLast()
		g.cur.Err = FaultParity
	case "arinc429":
		g.cur.Err = FaultParity
	}
}

// abs is |v|.
// TRLC-LINKS: REQ-SDS-214
func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
