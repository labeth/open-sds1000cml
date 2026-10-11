// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
package analysis

import (
	"fmt"
	"strings"
	"testing"

	"open-sds/app/internal/decode"
)

// captures decodes windows of a long UART stream at moving offsets, as a
// free-running trigger sees it: each capture holds the same messages at
// other times, with one more or one fewer of them, and fragments at its edges.
// TRLC-LINKS: REQ-SDS-214
func captures(t *testing.T, msgs [][]byte, repeat, n, count int) [][]Packet {
	t.Helper()
	const spb = 8
	var all [][]byte
	for i := 0; i < repeat; i++ {
		all = append(all, msgs...)
	}
	w := uartMsgs(all, spb, 40, 25, 40)
	ct := 1.0 / (spb * 115200.0)
	var out [][]Packet
	for c := 0; c < count; c++ {
		off := (c * 7919 * spb) % (len(w) - n)
		r := decode.DecodeUART(w[off:off+n], ct, decode.UARTCfg{Baud: 115200})
		out = append(out, Packets(r, n))
	}
	return out
}

// rowText is the rows as text, the selected one marked.
// TRLC-LINKS: REQ-SDS-214
func rowText(l *PacketList) string {
	var b strings.Builder
	for i, p := range l.Rows() {
		if i == l.Selected() {
			b.WriteString(">")
		}
		b.WriteString(string(p.Bytes) + "|")
	}
	return b.String()
}

// TestPacketListHoldsStill: the same messages shifted in time leave the list
// alone; a new message replaces it; the selection follows its message.
// TRLC-LINKS: REQ-SDS-214
func TestPacketListHoldsStill(t *testing.T) {
	msgs := [][]byte{[]byte("temp=21.5C"), []byte("hum=40%"), []byte("OK")}
	var l PacketList
	if l.Selected() != -1 || l.Len() != 0 || l.Rows() != nil {
		t.Fatalf("zero list: selected %d, %d rows", l.Selected(), l.Len())
	}
	// captures long enough to hold every message: one change, then still
	caps := captures(t, msgs, 40, 4000, 40)
	changes := 0
	var first []Packet
	for i, ps := range caps {
		if l.Update(ps) {
			changes++
			if i > 0 {
				t.Errorf("capture %d replaced the rows: %s", i, rowText(&l))
			}
			first = l.Rows()
		}
		if &l.Rows()[0] != &first[0] {
			t.Fatalf("capture %d: rows not the same slice while held", i)
		}
	}
	if changes != 1 {
		t.Fatalf("%d changes over %d captures of repeating messages", changes, len(caps))
	}
	hum := func() bool { // the selection is on "hum" whenever "hum" is listed
		listed := false
		for _, p := range l.Rows() {
			listed = listed || string(p.Bytes) == "hum=40%"
		}
		s := l.Selected()
		return !listed || (s >= 0 && string(l.Rows()[s].Bytes) == "hum=40%")
	}
	for i, p := range l.Rows() {
		if string(p.Bytes) == "hum=40%" {
			l.Select(i)
			break
		}
	}
	// A new message on the bus, in captures too short to hold the whole cycle:
	// the new message replaces the rows, a fuller view of the cycle may replace
	// them a few times more, then the list holds.
	alarm := [][]byte{[]byte("temp=21.5C"), []byte("ALARM"), []byte("hum=40%"), []byte("OK")}
	news := captures(t, alarm, 40, 2600, 60)
	changes = 0
	for i, ps := range news {
		if l.Update(ps) {
			changes++
			if i >= 30 {
				t.Errorf("capture %d still replaced the rows: %s", i, rowText(&l))
			}
		}
		if !hum() {
			t.Errorf("capture %d: the selection left its message: %s", i, rowText(&l))
		}
	}
	if changes == 0 || changes > len(alarm) {
		t.Errorf("%d replacements for one new message", changes)
	}
	t.Logf("a new message: %d replacements over %d captures, then held", changes, len(news))
	seen := false
	for _, p := range l.Rows() {
		seen = seen || string(p.Bytes) == "ALARM"
	}
	if !seen {
		t.Errorf("the new message is not listed: %s", rowText(&l))
	}
	// the old stream's captures: all known, no fuller: held
	for i, ps := range caps[:10] {
		if l.Update(ps) && len(l.Rows()) < 4 {
			t.Errorf("old capture %d replaced the rows with a thinner view: %s", i, rowText(&l))
		}
	}
	// only fragments: held
	var frag []Packet
	for _, p := range news[1] {
		p.Complete = false
		frag = append(frag, p)
	}
	if l.Update(frag) || l.Update(nil) {
		t.Error("a capture without a complete packet changed the list")
	}
	// Stop: Replace shows the capture on screen, selection still following
	if !l.Replace(caps[5]) {
		t.Error("Replace with another capture reported no change")
	}
	if l.Replace(caps[5]) {
		t.Error("Replace with the same capture reported a change")
	}
	if !hum() {
		t.Errorf("selection after Replace: %s", rowText(&l))
	}
	l.Reset()
	if l.Len() != 0 || l.Selected() != -1 {
		t.Errorf("after Reset: %d rows, selected %d", l.Len(), l.Selected())
	}
}

// TestPacketListSelectAndFind: Select clamps, FindMatch steps match to match
// in both directions without wrapping, flagged bytes never match, and
// SelectedIn finds the selected message in a live capture.
// TRLC-LINKS: REQ-SDS-214
func TestPacketListSelectAndFind(t *testing.T) {
	mk := func(i0 int, s string, bad int) Packet {
		p := Packet{I0: i0, I1: i0 + 10*len(s), Bytes: []byte(s), ByteErr: make([]bool, len(s)), Complete: true}
		if bad >= 0 {
			p.ByteErr[bad] = true
		}
		return p
	}
	ps := []Packet{mk(0, "Hi U\x55\xaa", -1), mk(100, "status", -1), mk(200, "x\x55\xaay", -1),
		mk(300, "status", -1), mk(400, "z\x55\xaa", 1), mk(500, "\x55\xaa!", -1)}
	var l PacketList
	if !l.Update(ps) || l.Selected() != 0 {
		t.Fatalf("first rows: selected %d", l.Selected())
	}
	l.Select(99)
	if l.Selected() != 5 {
		t.Errorf("Select(99) = %d, want the last row", l.Selected())
	}
	l.Select(-4)
	if l.Selected() != 0 {
		t.Errorf("Select(-4) = %d, want the first row", l.Selected())
	}
	m := []byte{0x55, 0xAA}
	steps := []struct{ from, dir, want int }{
		{-1, 1, 0}, {0, 1, 2}, {2, 1, 5}, {5, 1, -1}, // row 4's 55 is flagged: no match
		{6, -1, 5}, {5, -1, 2}, {0, -1, -1}, {3, 0, 5}, {2, 0, 2}, {-9, 1, 0}, {99, -1, 5},
	}
	for _, s := range steps {
		if got := l.FindMatch(m, s.from, s.dir); got != s.want {
			t.Errorf("FindMatch(55 AA, %d, %d) = %d, want %d", s.from, s.dir, got, s.want)
		}
	}
	if l.FindMatch(nil, 0, 1) != -1 || l.FindMatch([]byte("not there"), -1, 1) != -1 {
		t.Error("an empty or absent pattern matched")
	}
	if MatchIndex(ps[2], m) != 1 || MatchIndex(ps[4], m) != -1 || MatchIndex(ps[0], []byte("Hi U\x55\xaa!")) != -1 {
		t.Error("MatchIndex")
	}
	// the live capture holds "status" twice at other places; the selected row
	// (row 3, the second "status") maps to the live packet nearest its place
	l.Select(3)
	live := []Packet{mk(5, "x\x55\xaay", -1), mk(105, "status", -1), {I0: 150, Bytes: []byte("status")},
		mk(205, "Hi U\x55\xaa", -1), mk(305, "\x55\xaa!", -1), mk(405, "status", -1)}
	if got := l.SelectedIn(live); got != 5 {
		t.Errorf("SelectedIn = %d, want 5 (the incomplete copy does not count)", got)
	}
	l.Select(0)
	if got := l.SelectedIn(live); got != 3 {
		t.Errorf("SelectedIn = %d, want 3", got)
	}
	if got := l.SelectedIn(nil); got != -1 {
		t.Errorf("SelectedIn(nil) = %d", got)
	}
	var empty PacketList
	if empty.SelectedIn(live) != -1 || empty.FindMatch(m, -1, 1) != -1 {
		t.Error("an empty list found something")
	}
	empty.Select(3)
	if empty.Selected() != -1 {
		t.Error("an empty list selected a row")
	}
}

// TestPacketListContent: the content key tells messages apart by address,
// direction, fault and flagged bytes, not by position.
// TRLC-LINKS: REQ-SDS-214
func TestPacketListContent(t *testing.T) {
	base := Packet{I0: 10, I1: 90, Bytes: []byte{1, 2}, ByteErr: []bool{false, false}, Addr: 0x24, HasAddr: true,
		Complete: true}
	moved := base
	moved.I0, moved.I1 = 500, 580
	if contentKey(base) != contentKey(moved) {
		t.Error("a moved message reads as another")
	}
	for name, p := range map[string]Packet{
		"address":   func() Packet { q := base; q.Addr = 0x25; return q }(),
		"direction": func() Packet { q := base; q.Read = true; return q }(),
		"no addr":   func() Packet { q := base; q.HasAddr = false; return q }(),
		"fault":     func() Packet { q := base; q.Err = FaultNAK; return q }(),
		"flag":      func() Packet { q := base; q.ByteErr = []bool{false, true}; return q }(),
		"bytes": func() Packet {
			q := base
			q.Bytes, q.ByteErr = []byte{1, 2, 0}, []bool{false, false, false}
			return q
		}(),
	} {
		if contentKey(p) == contentKey(base) {
			t.Errorf("%s: the same content key", name)
		}
	}
	// 16 flags: the error bitmap spans two bytes
	long := Packet{Bytes: make([]byte, 16), ByteErr: make([]bool, 16)}
	k0 := contentKey(long)
	long.ByteErr[15] = true
	if contentKey(long) == k0 {
		t.Error("a flag on byte 16 is not in the key")
	}
}

// BenchmarkPacketListUpdate: a held list offered a capture of 200 messages.
// TRLC-LINKS: REQ-SDS-214
func BenchmarkPacketListUpdate(b *testing.B) {
	var ps []Packet
	for i := 0; i < 200; i++ {
		s := fmt.Sprintf("message %d", i%20)
		ps = append(ps, Packet{I0: i * 100, I1: i*100 + 90, Bytes: []byte(s), ByteErr: make([]bool, len(s)), Complete: true})
	}
	var l PacketList
	l.Update(ps)
	b.ReportAllocs()
	for b.Loop() {
		l.Update(ps)
	}
}
