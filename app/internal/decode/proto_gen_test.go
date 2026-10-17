// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

// Waveform generators for the protocols beyond the first ten, shared by
// their round-trip, break, parity, oracle and detection tests. Each writes
// logic levels on a timeline (oracle_test.go) at a sample rate, so
// non-integer samples per bit accumulate like a real capture.

// tlUART appends one UART character (start, 8 data bits LSB first, parity
// "even"/"odd"/"none", stops stop bits) at baud.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
func tlUART(w *timeline, v int, baud float64, parity string, stops int) {
	bit := 1 / baud
	w.add(0, bit)
	ones := 0
	for i := 0; i < 8; i++ {
		b := byte(v >> i & 1)
		ones += int(b)
		w.add(b, bit)
	}
	switch parity {
	case "even":
		w.add(byte(ones&1), bit)
	case "odd":
		w.add(byte(1-ones&1), bit)
	}
	w.add(1, float64(stops)*bit)
}

// linFrame appends a LIN frame: a break of breakBits, a one-bit delimiter,
// the sync, the protected identifier of id and, unless data is nil, the
// response with its checksum (enhanced unless classic or a diagnostic ID).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
func linFrame(w *timeline, baud float64, breakBits float64, id int, data []int, classic bool) {
	bit := 1 / baud
	w.add(0, breakBits*bit)
	w.add(1, bit)
	tlUART(w, 0x55, baud, "none", 1)
	pid := linPIDParity(id)
	tlUART(w, pid, baud, "none", 1)
	if data == nil {
		w.add(1, 30*bit)
		return
	}
	w.add(1, 2*bit) // response space
	for _, d := range data {
		tlUART(w, d, baud, "none", 1)
	}
	enh := !classic && id != 0x3c && id != 0x3d
	tlUART(w, linChecksum(pid, data, enh), baud, "none", 1)
	w.add(1, 30*bit)
}

// dmxPacket appends a DMX512 packet: a break of breakUs, a mark after break
// of mabUs, the start code and the slots (8N2 at 250 kbit/s), then 40 us of
// mark (under a break's 88 us: sigrok's decoder takes a longer mark for a
// break of the other polarity).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
func dmxPacket(w *timeline, breakUs, mabUs float64, start int, slots []int) {
	dmxPacketGap(w, breakUs, mabUs, 0, start, slots)
}

// dmxPacketGap is dmxPacket with mbsUs of mark between the slots.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
func dmxPacketGap(w *timeline, breakUs, mabUs, mbsUs float64, start int, slots []int) {
	w.add(0, breakUs*1e-6)
	w.add(1, mabUs*1e-6)
	tlUART(w, start, 250000, "none", 2)
	for _, s := range slots {
		w.add(1, mbsUs*1e-6)
		tlUART(w, s, 250000, "none", 2)
	}
	w.add(1, 40e-6)
}

// modbusFrame appends an RTU frame with its CRC, then 4 characters of idle.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
func modbusFrame(w *timeline, baud float64, parity string, stops int, b []int) {
	crc := modbusCRC(b)
	for _, v := range append(append([]int{}, b...), crc&0xff, crc>>8) {
		tlUART(w, v, baud, parity, stops)
	}
	w.add(1, 44/baud)
}

// idle appends a high line for d seconds.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
func (w *timeline) idle(d float64) { w.add(1, d) }

// codes is the timeline as two-level ADC codes.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
func (w *timeline) codes() []uint8 { return bitsToCodes(w.bits) }
