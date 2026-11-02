// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS

package analysis

// Len is the number of rows.
// TRLC-LINKS: REQ-SDS-214
func (l *PacketList) Len() int { return len(l.rows) }

// FindMatch finds the next row holding the byte sequence seq: searching from
// row from+dir onward in direction dir (+1 down, -1 up), or from row from
// itself downward when dir is 0. Bytes the decoder flagged never match. It
// returns the row, or -1 (no wrap-around: the caller says "no more").
// TRLC-LINKS: REQ-SDS-214
func (l *PacketList) FindMatch(seq []byte, from, dir int) int {
	if len(seq) == 0 || len(l.rows) == 0 {
		return -1
	}
	step, i := 1, from
	switch {
	case dir < 0:
		step, i = -1, from-1
	case dir > 0:
		i = from + 1
	}
	if step > 0 {
		i = max(i, 0)
	} else {
		i = min(i, len(l.rows)-1)
	}
	for ; i >= 0 && i < len(l.rows); i += step {
		if MatchIndex(l.rows[i], seq) >= 0 {
			return i
		}
	}
	return -1
}

// MatchIndex is the offset in p.Bytes where seq first occurs with none of its
// bytes flagged bad, or -1.
// TRLC-LINKS: REQ-SDS-214
func MatchIndex(p Packet, seq []byte) int {
	if len(seq) == 0 || len(seq) > len(p.Bytes) {
		return -1
	}
outer:
	for i := 0; i+len(seq) <= len(p.Bytes); i++ {
		for j, b := range seq {
			if k := i + j; p.Bytes[k] != b || (k < len(p.ByteErr) && p.ByteErr[k]) {
				continue outer
			}
		}
		return i
	}
	return -1
}

// contentKey identifies a message by what it says: its address, direction,
// fault, bytes and flagged bytes, not where it sits in the record.
// TRLC-LINKS: REQ-SDS-214
func contentKey(p Packet) string {
	return string(appendContentKey(make([]byte, 0, 16+len(p.Err)+len(p.Bytes)+len(p.Bytes)/8), &p))
}
