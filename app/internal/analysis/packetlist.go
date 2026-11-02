// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
package analysis

import "encoding/binary"

// maxKnown bounds the messages a PacketList remembers (a bus that counts or
// timestamps makes a new message every capture).
const maxKnown = 4096

// PacketList is the BUS workspace's packet list: the complete packets of a
// capture, held still while the same messages repeat (REQ-SDS-214). A running
// scope captures 20 times a second, and each capture holds the messages at
// other times, one more or one fewer of them, or another part of a repeating
// cycle; replacing the rows every time would make them jump.
//
// Update therefore remembers every message seen since Reset and replaces the
// rows only for a capture that holds a message never seen, or that shows more
// distinct messages than the rows do (so the list settles on the fullest view
// and then holds). The selection follows the message the user selected, by
// content, through replacements, and returns to it when a later capture lists
// it again. The zero value is an empty list.
// TRLC-LINKS: REQ-SDS-214
type PacketList struct {
	rows    []Packet
	keys    []string            // content key per row
	shown   int                 // distinct messages in the rows
	known   map[string]struct{} // every message seen since Reset
	sel     int
	selKey  string // the message the selection follows
	hasSel  bool
	selMemo selMemo
}

// Update offers the packets of a new capture (Packets' result; incomplete
// packets are left out) and reports whether it replaced the rows: for a
// message never seen, or a fuller view of the known ones. A capture without a
// complete packet leaves the list alone.
// TRLC-LINKS: REQ-SDS-214
func (l *PacketList) Update(ps []Packet) (changed bool) {
	keys, distinct := completeKeys(ps)
	if len(keys) == 0 {
		return false
	}
	if len(l.rows) > 0 && distinct <= l.shown {
		fresh := false
		for _, k := range keys {
			if _, ok := l.known[k]; !ok {
				fresh = true
				break
			}
		}
		if !fresh {
			return false
		}
	}
	l.set(ps, keys, distinct)
	return true
}

// Replace takes the complete packets of ps as the rows unconditionally, as
// when the scope stops and the list must show the capture on screen; the
// selection follows its message. It reports whether the rows changed.
// TRLC-LINKS: REQ-SDS-214
func (l *PacketList) Replace(ps []Packet) (changed bool) {
	keys, distinct := completeKeys(ps)
	if samePackets(l.rows, ps, keys) {
		return false
	}
	l.set(ps, keys, distinct)
	return true
}

// Reset empties the list and forgets the messages it has seen, as a decode
// or timebase change must.
// TRLC-LINKS: REQ-SDS-214
func (l *PacketList) Reset() { *l = PacketList{} }

// Rows are the listed packets in capture order. The slice is the list's own:
// it stays the same while the list holds still, and must not be modified.
// TRLC-LINKS: REQ-SDS-214
func (l *PacketList) Rows() []Packet { return l.rows }

// Select selects row i, clamped to the rows; the selection then follows that
// row's message.
// TRLC-LINKS: REQ-SDS-214
func (l *PacketList) Select(i int) {
	if len(l.rows) == 0 {
		return
	}
	l.sel = clampInt(i, 0, len(l.rows)-1)
	l.selKey, l.hasSel = l.keys[l.sel], true
}

// Selected is the selected row, -1 when the list is empty. A new list
// selects its first row.
// TRLC-LINKS: REQ-SDS-214
func (l *PacketList) Selected() int {
	if len(l.rows) == 0 {
		return -1
	}
	return clampInt(l.sel, 0, len(l.rows)-1)
}

// SelectedIn finds the selected row's message in the packets of a live
// capture: the complete packet with the same content whose place among the
// capture's complete packets is nearest the selected row (the list holds
// still while the capture moves on). It returns its index in ps, -1 when the
// capture does not hold the message.
// TRLC-LINKS: REQ-SDS-214
func (l *PacketList) SelectedIn(ps []Packet) int {
	s := l.Selected()
	if s < 0 {
		return -1
	}
	want := l.keys[s]
	// A capture's packets are replaced whole, never changed in place: the
	// answer holds for the same packets, row and message (the display asks
	// at every tick).
	var first *Packet
	if len(ps) > 0 {
		first = &ps[0]
	}
	if m := l.selMemo; m.ok && m.ps == first && m.n == len(ps) && m.sel == s && m.want == want {
		return m.res
	}
	best := nearestMessage(ps, want, s)
	l.selMemo = selMemo{ps: first, n: len(ps), sel: s, want: want, res: best, ok: true}
	return best
}

// selMemo is SelectedIn's last answer and what it was for.
type selMemo struct {
	ps     *Packet
	n, sel int
	want   string
	res    int
	ok     bool
}

// nearestMessage is the complete packet of ps with content key want whose
// place among the complete packets is nearest s, -1 none.
// TRLC-LINKS: REQ-SDS-214
func nearestMessage(ps []Packet, want string, s int) int {
	best, bestD, c := -1, 0, 0
	var buf [256]byte
	for i := range ps {
		p := &ps[i]
		if !p.Complete {
			continue
		}
		if string(appendContentKey(buf[:0], p)) == want {
			if d := abs(c - s); best < 0 || d < bestD {
				best, bestD = i, d
			}
		}
		c++
	}
	return best
}

// set installs the complete packets of ps (whose keys are given) as the rows,
// remembers their messages, and moves the selection onto the followed
// message (the copy nearest the old place), or keeps its place when the
// message is not listed.
// TRLC-LINKS: REQ-SDS-214
func (l *PacketList) set(ps []Packet, keys []string, distinct int) {
	rows := make([]Packet, 0, len(keys))
	for _, p := range ps {
		if p.Complete {
			p.Bytes = append([]byte(nil), p.Bytes...)
			p.ByteErr = append([]bool(nil), p.ByteErr...)
			rows = append(rows, p)
		}
	}
	old := l.sel
	l.rows, l.keys, l.shown = rows, keys, distinct
	if l.known == nil || len(l.known)+len(keys) > maxKnown {
		l.known = make(map[string]struct{}, len(keys))
	}
	for _, k := range keys {
		l.known[k] = struct{}{}
	}
	if len(rows) == 0 {
		return
	}
	if !l.hasSel {
		l.sel, l.selKey, l.hasSel = 0, keys[0], true
		return
	}
	best := -1
	for i, k := range keys {
		if k == l.selKey && (best < 0 || abs(i-old) < abs(best-old)) {
			best = i
		}
	}
	if best < 0 {
		best = old
	}
	l.sel = clampInt(best, 0, len(rows)-1)
}

// completeKeys are the content keys of the complete packets of ps, in order,
// and how many distinct messages they hold.
// TRLC-LINKS: REQ-SDS-214
func completeKeys(ps []Packet) ([]string, int) {
	keys := make([]string, 0, len(ps))
	seen := make(map[string]struct{}, len(ps))
	var buf []byte // one buffer for every key: a string each, no buffer each
	for i := range ps {
		if ps[i].Complete {
			buf = appendContentKey(buf[:0], &ps[i])
			k := string(buf)
			keys = append(keys, k)
			seen[k] = struct{}{}
		}
	}
	return keys, len(seen)
}

// samePackets reports whether rows equal the complete packets of ps (whose
// keys are given), positions included.
// TRLC-LINKS: REQ-SDS-214
func samePackets(rows, ps []Packet, keys []string) bool {
	if len(rows) != len(keys) {
		return false
	}
	i := 0
	var buf []byte
	for k := range ps {
		p := &ps[k]
		if !p.Complete {
			continue
		}
		if rows[i].I0 != p.I0 || rows[i].I1 != p.I1 {
			return false
		}
		// compared as bytes: no string a row
		if buf = appendContentKey(buf[:0], &rows[i]); string(buf) != keys[i] {
			return false
		}
		i++
	}
	return true
}

// appendContentKey appends p's contentKey to b: a caller that only compares
// keys builds them in a buffer of its own (the display looks for the
// selected message in every packet of the record at each tick).
// TRLC-LINKS: REQ-SDS-214
func appendContentKey(b []byte, p *Packet) []byte {
	var flags byte
	if p.HasAddr {
		flags |= 1
	}
	if p.Read {
		flags |= 2
	}
	b = append(b, flags)
	if p.HasAddr {
		b = binary.AppendVarint(b, int64(p.Addr))
	}
	b = binary.AppendUvarint(b, uint64(len(p.Err)))
	b = append(b, p.Err...)
	b = binary.AppendUvarint(b, uint64(len(p.Bytes)))
	b = append(b, p.Bytes...)
	var bits byte
	for i := range p.Bytes {
		if i < len(p.ByteErr) && p.ByteErr[i] {
			bits |= 1 << (i % 8)
		}
		if i%8 == 7 || i == len(p.Bytes)-1 {
			b = append(b, bits)
			bits = 0
		}
	}
	return b
}
