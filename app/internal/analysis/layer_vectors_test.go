// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
package analysis

import (
	"fmt"
	"math/rand"
)

// layerVec is one record's packets read with one layer.
type layerVec struct {
	Name    string
	ID      string
	Proto   string
	Packets []Packet
	Args    LayerArgs
}

// mkSPI is an SPI transaction (one burst).
// TRLC-LINKS: REQ-SDS-242
func mkSPI(b ...byte) Packet { return Packet{Bytes: b, ByteErr: make([]bool, len(b)), Complete: true} }

// mkI2C is an I2C transfer to addr.
// TRLC-LINKS: REQ-SDS-243
func mkI2C(addr int, read bool, b ...byte) Packet {
	return Packet{Bytes: b, ByteErr: make([]bool, len(b)), Addr: addr, HasAddr: true, Read: read, Complete: true}
}

// mkUSB is a USB packet with its PID.
// TRLC-LINKS: REQ-SDS-246
func mkUSB(pid int, b ...byte) Packet {
	return Packet{Bytes: b, ByteErr: make([]bool, len(b)), Addr: pid, HasAddr: true, Complete: true}
}

// sbusBytes builds an S.BUS frame of 16 channel values and flags.
// TRLC-LINKS: REQ-SDS-244
func sbusBytes(ch [16]int, flags byte) []byte {
	b := make([]byte, 25)
	b[0] = 0x0f
	bit := 0
	for _, v := range ch {
		for j := 0; j < 11; j++ {
			if v>>j&1 != 0 {
				b[1+bit>>3] |= 1 << (bit & 7)
			}
			bit++
		}
	}
	b[23] = flags
	return b
}

// edidBlock is a valid 128-byte EDID base block for a 1920x1080 60 Hz
// display named "TESTMON".
// TRLC-LINKS: REQ-SDS-243
func edidBlock() []byte {
	m := make([]byte, 128)
	copy(m, edidHeader)
	m[8], m[9] = 0x10, 0xac // "DEL"
	m[10], m[11] = 0x34, 0x12
	m[18], m[19] = 1, 3
	// preferred timing: 148.5 MHz, 1920+280 x 1080+45
	pc := 14850
	m[54], m[55] = byte(pc), byte(pc>>8)
	m[56], m[57], m[58] = 1920&0xff, 280&0xff, byte((1920>>8)<<4|(280>>8))
	m[59], m[60], m[61] = 1080&0xff, 45, byte((1080>>8)<<4)
	d := 72
	m[d+3] = 0xfc
	copy(m[d+5:d+18], "TESTMON\n     ")
	sum := 0
	for _, v := range m[:127] {
		sum += int(v)
	}
	m[127] = byte((256 - sum%256) % 256)
	return m
}

// layerCurated are realistic records for every layer.
// TRLC-LINKS: REQ-SDS-241
func layerCurated() []layerVec {
	var vs []layerVec
	add := func(name, id, proto string, ps ...Packet) {
		vs = append(vs, layerVec{Name: name, ID: id, Proto: proto, Packets: ps, Args: LayerArgs{Addr: -1}})
	}
	add("flash session", "spiflash", "spi", mkSPI(0x9f, 0, 0, 0), mkSPI(0x06), mkSPI(0x02, 0, 1, 0, 1, 2, 3, 4),
		mkSPI(0x05, 0), mkSPI(0x03, 0, 0x10, 0, 0, 0, 0, 0), mkSPI(0x0b, 1, 2, 3, 0, 9, 9), mkSPI(0x20, 0, 0x10, 0),
		mkSPI(0xd8, 1, 0, 0), mkSPI(0x20, 0, 0x10, 0x10), mkSPI(0xab), mkSPI(0xab, 0, 0, 0, 0x14), mkSPI(0x77, 1),
		mkSPI(0x03, 0), mkSPI(0x06, 1), mkSPI(0x90, 0, 0, 0, 0xef, 0x13), mkSPI(0x01, 0x02), mkSPI(0xc7))
	add("sd card init", "sdcard", "spi", mkSPI(0xff, 0x40, 0, 0, 0, 0, 0x95, 0xff, 0xff), mkSPI(0x48, 0, 0, 1, 0xaa, 0x87),
		mkSPI(0x77, 0, 0, 0, 0, 0x65), mkSPI(0x69, 0x40, 0, 0, 0, 0x77), mkSPI(0x51, 0, 0, 0x10, 0, 0x01),
		mkSPI(0x58, 0, 0, 0, 8, 0x12), mkSPI(0xfe, 1, 2, 3, 4), mkSPI(0xfd), mkSPI(0x42, 1, 2), mkSPI(0x13))
	add("avr isp", "avrisp", "spi", mkSPI(0xac, 0x53, 0, 0), mkSPI(0x30, 0, 0, 0, 0x30, 0, 1, 0, 0x30, 0, 2, 0),
		mkSPI(0xac, 0x80, 0, 0), mkSPI(0x50, 0, 0, 0), mkSPI(0x58, 8, 0, 0), mkSPI(0x50, 8, 0, 0), mkSPI(0x58, 0, 0, 0),
		mkSPI(0xa0, 1, 0x23, 0), mkSPI(0x28, 0x01, 0x23, 0), mkSPI(0x40, 0, 5, 0xab), mkSPI(0x4c, 1, 2, 0), mkSPI(1, 2, 3, 4), mkSPI(1, 2))
	add("nrf24", "nrf24l01", "spi", mkSPI(0x20, 0x0e), mkSPI(0x2a, 0xe7, 0xe6, 0xe5, 0xe4, 0xe3), mkSPI(0x07, 0),
		mkSPI(0xa0, 'h', 'i', 0), mkSPI(0xb0, 1), mkSPI(0xa9, 'x'), mkSPI(0x61, 0, 0, 0), mkSPI(0x60, 0), mkSPI(0xe1),
		mkSPI(0xe2), mkSPI(0xe3), mkSPI(0xff), mkSPI(0x50, 0x73), mkSPI(0x50, 0x12), mkSPI(0x70))
	add("nrf905", "nrf905", "spi", mkSPI(0x00, 0x6c, 0x0c, 0x44, 0x20, 0x20), mkSPI(0x10, 0, 0), mkSPI(0x20, 1, 2),
		mkSPI(0x21, 0), mkSPI(0x22, 0xe7, 0xe7, 0xe7, 0xe7), mkSPI(0x23, 0), mkSPI(0x24, 0, 0), mkSPI(0x8f, 0x6c),
		mkSPI(0x80), mkSPI(0x0c, 1), mkSPI(0xee))
	add("cc1101", "cc1101", "spi", mkSPI(0x30), mkSPI(0x34), mkSPI(0x0d, 0x10), mkSPI(0x7e, 0xc0, 0xc8),
		mkSPI(0x80, 0), mkSPI(0xc0, 0, 0), mkSPI(0xf5, 0), mkSPI(0xb5), mkSPI(0x37), mkSPI(0x3f, 1, 2))
	add("rfm12", "rfm12", "spi", mkSPI(0x80, 0xd7), mkSPI(0x82, 0x39), mkSPI(0xa6, 0x40), mkSPI(0xc6, 0x23),
		mkSPI(0x94, 0xa2), mkSPI(0xc2, 0xac), mkSPI(0xca, 0x81), mkSPI(0xce, 0xd4), mkSPI(0xb0, 0), mkSPI(0xc4, 0x83),
		mkSPI(0x98, 0x50), mkSPI(0xcc, 0x77), mkSPI(0xb8, 0xaa), mkSPI(0xfe, 0), mkSPI(0xe0, 0), mkSPI(0xc8, 0),
		mkSPI(0xc0, 0xe0), mkSPI(0x00, 0), mkSPI(0x12, 0), mkSPI(0x80), mkSPI(0xc6, 0x80))
	add("mrf24j40", "mrf24j40", "spi", mkSPI(0x01, 0x01), mkSPI(0x02, 0), mkSPI(0x80, 0x10, 0x41), mkSPI(0xc0, 0x00, 0),
		mkSPI(0xe0, 0x10, 5, 0x03, 2), mkSPI(0x05), mkSPI(0x90, 0x10))
	add("enc28j60", "enc28j60", "spi", mkSPI(0x00, 0), mkSPI(0x5f, 0x02), mkSPI(0x14, 0), mkSPI(0x9f, 0x01),
		mkSPI(0x14, 0), mkSPI(0xbf, 0x03), mkSPI(0x14, 0), mkSPI(0x3a, 0, 0, 0), mkSPI(0x7a, 1, 2, 3), mkSPI(0xff),
		mkSPI(0x5f), mkSPI(0x3b), mkSPI(0xe5))
	add("st25", "st25r39xx", "spi", mkSPI(0x02, 0x80), mkSPI(0x42, 0), mkSPI(0xfb, 0x05, 1), mkSPI(0xfc, 0x41, 0),
		mkSPI(0x80, 1, 2, 3), mkSPI(0x9f, 0, 0), mkSPI(0xa0, 1), mkSPI(0xbf, 0), mkSPI(0xc1), mkSPI(0xc2, 0xdb),
		mkSPI(0xfa), mkSPI(0xe9))
	add("adxl345", "adxl345", "spi", mkSPI(0x80, 0), mkSPI(0x2d, 0x08), mkSPI(0x2c, 0x0a), mkSPI(0x31, 0x0b),
		mkSPI(0xf2, 0, 0, 0, 0, 0, 0), mkSPI(0x1e, 1, 2), mkSPI(0x5e, 1, 2, 3), mkSPI(0x2d, 0), mkSPI(0x05, 9))
	add("adns5020", "adns5020", "spi", mkSPI(0x02, 0, 0x03, 0, 0x04, 0), mkSPI(0x8d, 0x01), mkSPI(0x63, 0), mkSPI(0x70, 0), mkSPI(0x05))
	add("ade7758", "ade77xx", "spi", mkSPI(0x93, 0x04), mkSPI(0x0d, 0, 0, 0), mkSPI(0x9b, 0x12, 0x34), mkSPI(0x10, 0),
		mkSPI(0x50, 0), mkSPI(0x9b, 0x12), mkSPI(0x7f, 0))
	add("adf4351", "adf435x", "spi", mkSPI(0x00, 0x58, 0x00, 0x05), mkSPI(0x00, 0x00, 0x80, 0x29), mkSPI(0x00, 0x00, 0x4e, 0x42),
		mkSPI(0x00, 0x00, 0x04, 0xb3), mkSPI(0x00, 0x8c, 0x80, 0x3c), mkSPI(0x00, 0x58, 0x00, 0x05), mkSPI(1, 2, 3))
	add("ad5626", "ad5626", "spi", mkSPI(0x07, 0xff), mkSPI(0x0f, 0xff), mkSPI(0x00, 0x01), mkSPI(1))
	add("ad79x0", "ad79x0", "spi", mkSPI(0x07, 0xff), mkSPI(0x0f, 0xff), mkSPI(0), mkSPI(1, 2, 3), mkSPI(0x80, 0))
	add("ltc242x", "ltc242x", "spi", mkSPI(0x20, 0x00, 0x00), mkSPI(0x3f, 0xff, 0xff), mkSPI(0x50, 0, 0), mkSPI(0x9f, 0, 1), mkSPI(1))
	add("max7219", "max72xx", "spi", mkSPI(0x0c, 0x01), mkSPI(0x0a, 0x08), mkSPI(0x0b, 0x07), mkSPI(0x09, 0xff),
		mkSPI(0x0f, 0x00), mkSPI(0x01, 0x7e, 0x02, 0x30), mkSPI(0x00, 0), mkSPI(0x0e, 1), mkSPI(1))
	add("x2444", "x2444m", "spi", mkSPI(0x84), mkSPI(0x9b, 0x12, 0x34), mkSPI(0x8e, 0, 0), mkSPI(0x81), mkSPI(0x85),
		mkSPI(0x82), mkSPI(0x80), mkSPI(0x81, 1), mkSPI(0x00))
	add("rgb leds", "rgbled", "spi", mkSPI(0xff, 0, 0, 0, 0xff, 0, 0, 0, 0xff), mkSPI(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21), mkSPI(1, 2))
	add("nes", "nesgamepad", "spi", mkSPI(0xff), mkSPI(0x00), mkSPI(0x7f), mkSPI(0x6e, 0xfe))
	add("ssi32", "ssi32", "spi", mkSPI(0x80, 0, 0, 0), mkSPI(0x01, 0x02, 4, 0x55, 1, 2, 3, 4), mkSPI(0x01, 0, 9, 0, 1), mkSPI(1, 2))

	edid := edidBlock()
	add("edid in two reads", "edid", "i2c", mkI2C(0x50, false, 0), mkI2C(0x50, true, edid[:64]...),
		mkI2C(0x50, false, 64), mkI2C(0x50, true, edid[64:]...), mkI2C(0x50, true, 1, 2))
	bad := append([]byte(nil), edid...)
	bad[127]++
	add("edid one read, checksum bad", "edid", "i2c", mkI2C(0x50, true, bad...))
	add("eeprom 1-byte addresses", "eeprom24xx", "i2c", mkI2C(0x50, false, 0x10), mkI2C(0x50, true, 1, 2, 3),
		mkI2C(0x50, false, 0x20, 0xab), mkI2C(0x50, true, 9), mkI2C(0x51, false, 0x00, 1, 2, 3, 4, 5, 6, 7, 8, 9),
		Packet{Addr: 0x50, HasAddr: true, Err: FaultNAK, Complete: true}, mkI2C(0x60, false, 1))
	add("eeprom 2-byte addresses", "eeprom24xx", "i2c", mkI2C(0x57, true, 1), mkI2C(0x57, false, 0x01, 0x00),
		mkI2C(0x57, true, 1, 2), mkI2C(0x57, false, 0x02, 0x00, 7, 8))
	add("ds1307", "ds1307", "i2c", mkI2C(0x68, false, 0), mkI2C(0x68, true, 0x56, 0x34, 0x12, 0x07, 0x18, 0x10, 0x26),
		mkI2C(0x68, false, 0, 0x00, 0x45, 0x71, 0x02, 0x31, 0x12, 0x99), mkI2C(0x68, false, 7, 0x10),
		mkI2C(0x68, true, 0x10), mkI2C(0x68, false, 0x08, 1, 2, 3, 4, 5), mkI2C(0x68, false, 0, 0x99, 0x99, 0x99, 0x99, 0x99, 0x99, 0x99))
	add("rtc8564", "rtc8564", "i2c", mkI2C(0x51, false, 2), mkI2C(0x51, true, 0x56, 0x34, 0x12, 0x18, 0x00, 0x10, 0x26),
		mkI2C(0x51, false, 0, 0, 0), mkI2C(0x51, true, 0x12, 0x45))
	add("lm75", "lm75", "i2c", mkI2C(0x48, true, 0x19, 0x80), mkI2C(0x48, false, 1), mkI2C(0x48, true, 0x02),
		mkI2C(0x48, false, 3, 0x50, 0x00), mkI2C(0x49, true, 0xe7, 0x00), mkI2C(0x4f, false, 2, 0x4b))
	add("mlx90614", "mlx90614", "i2c", mkI2C(0x5a, false, 0x07), mkI2C(0x5a, true, 0xa8, 0x3a, 0x00),
		mkI2C(0x5a, false, 0x06), mkI2C(0x5a, true, 0x9b, 0x3a, 0x6f), mkI2C(0x5a, false, 0x24, 0xff, 0xff, 0x00), mkI2C(0x5a, true, 1))
	add("atsha204a", "atsha204a", "i2c", mkI2C(0x64, false), mkI2C(0x64, true, 4, 0x11, 0x33, 0x43),
		atshaCmd(0x02, 0x00, 0x0000, nil), mkI2C(0x64, true, 7, 1, 2, 3, 4, 0, 0), mkI2C(0x64, false, 1), mkI2C(0x64, false, 3, 7, 0x1b, 0, 0, 0, 1, 2))
	add("hdcp", "hdcp", "i2c", mkI2C(0x3a, false, 0x00), mkI2C(0x3a, true, 1, 2, 3, 4, 5), mkI2C(0x3a, false, 0x41),
		mkI2C(0x3a, true, 0x02, 0x10), mkI2C(0x3a, false, 0x50), mkI2C(0x3a, true, 4), mkI2C(0x3a, false, 0x70),
		mkI2C(0x3a, true, 0x10, 0x05), mkI2C(0x3a, false, 0x60, 2, 9, 9), mkI2C(0x3a, false, 0x80), mkI2C(0x3a, true, 3))
	add("ltc2607", "ltc26x7", "i2c", mkI2C(0x10, false, 0x30, 0x80, 0x00), mkI2C(0x10, false, 0x1f), mkI2C(0x10, false, 0x41), mkI2C(0x10, false, 0x22, 1, 2))
	add("tca6408a", "tca6408a", "i2c", mkI2C(0x20, false, 3, 0xf0), mkI2C(0x20, false, 1, 0x0f), mkI2C(0x20, false, 0), mkI2C(0x20, true, 0xaa), mkI2C(0x22, false, 1))
	add("pca9571", "pca9571", "i2c", mkI2C(0x25, false, 0x55), mkI2C(0x25, true, 0x55), mkI2C(0x26, false, 1))
	add("mcp23017", "mcp23017", "i2c", mkI2C(0x20, false, 0x00, 0x00, 0xff), mkI2C(0x20, false, 0x12), mkI2C(0x20, true, 0x55, 0xaa),
		mkI2C(0x27, false, 0x0a, 0x20), mkI2C(0x21, true, 1, 2, 3, 4, 5, 6))
	add("mcp23008", "mcp23008", "i2c", mkI2C(0x20, false, 0x00, 0x00), mkI2C(0x20, false, 0x09), mkI2C(0x20, true, 0x5a))
	add("mxc6225xu", "mxc6225xu", "i2c", mkI2C(0x15, false, 0), mkI2C(0x15, true, 0x10, 0xf0, 0x45), mkI2C(0x15, false, 3, 0x80))
	add("nunchuk", "nunchuk", "i2c", mkI2C(0x52, false, 0x40, 0x00), mkI2C(0x52, false, 0x00), mkI2C(0x52, true, 0x80, 0x7f, 0x80, 0x81, 0xb0, 0x5d),
		mkI2C(0x52, false, 0xf0, 0x55), mkI2C(0x52, false, 0xfb, 0x00), mkI2C(0x52, true, 1, 2, 3), mkI2C(0x52, true, 1, 2, 3, 4, 5, 0x03))
	xfp := make([]byte, 256)
	xfp[0], xfp[127], xfp[128] = 6, 1, 6
	xfp[96], xfp[97] = 0x23, 0x40
	xfp[100], xfp[101] = 0x0b, 0xb8
	xfp[102], xfp[103] = 0x13, 0x88
	xfp[104], xfp[105] = 0x0f, 0xa0
	copy(xfp[148:164], "ACME OPTICS     ")
	copy(xfp[168:184], "XFP-10G-SR      ")
	xfp[186], xfp[187] = 0x66, 0x58
	copy(xfp[196:212], "SN0001          ")
	add("xfp", "xfp", "i2c", mkI2C(0x50, false, 0), mkI2C(0x50, true, xfp...), mkI2C(0x50, false, 96),
		mkI2C(0x50, true, xfp[96:110]...), mkI2C(0x50, false, 127, 1))
	add("demux", "i2cdemux", "i2c", mkI2C(0x50, false, 1), mkI2C(0x68, true, 1, 2), mkI2C(0x50, true, 3), mkI2C(0x20, false))
	vs = append(vs, layerVec{Name: "filter 0x50 reads", ID: "i2cfilter", Proto: "i2c", Args: LayerArgs{Addr: 0x50, Dir: 1},
		Packets: []Packet{mkI2C(0x50, false, 1), mkI2C(0x68, true, 1, 2), mkI2C(0x50, true, 3), mkI2C(0x50, true)}})
	vs = append(vs, layerVec{Name: "filter writes any", ID: "i2cfilter", Proto: "i2c", Args: LayerArgs{Addr: -1, Dir: 2},
		Packets: []Packet{mkI2C(0x50, false, 1), mkI2C(0x68, true, 1, 2), mkI2C(0x20, false)}})

	var ch [16]int
	for k := range ch {
		ch[k] = 172 + 100*k
	}
	sb := sbusBytes(ch, 0x0c)
	sb2 := sbusBytes(ch, 0x03)
	sb2[24] = 0x14
	sb3 := sbusBytes(ch, 0)
	sb3[24] = 0x55
	perr := Packet{Bytes: sbusBytes(ch, 0), ByteErr: make([]bool, 25), Complete: true}
	perr.ByteErr[3] = true
	add("sbus", "sbus", "uart", Packet{Bytes: sb, Complete: true}, Packet{Bytes: sb2, Complete: true},
		Packet{Bytes: sb3, Complete: true}, perr, Packet{Bytes: []byte{0x0f, 1, 2}, Complete: true})
	add("itm", "arm_itm", "uart", mkSPI(0, 0, 0, 0, 0, 0x80, 0x01, 'h', 0x01, 'i', 0x01, '\n', 0x0b, 1, 2, 3, 4),
		mkSPI(0x09, 'a', 0x02, 0x34, 0x12, 0x0e, 0x0f, 0x10, 0x17, 0x78, 0x56, 0x34, 0x12, 0x05, 0x21),
		mkSPI(0x70, 0x10, 0xc0, 0x81, 0x01, 0x08, 0x84, 0x01, 0x8d, 0x22, 0x47, 1, 2, 3, 4, 0x4e, 1, 2, 0x55, 9, 9),
		mkSPI(0x94, 0x00, 0x00), mkSPI(0x03), mkSPI(0xc0, 0x80), mkSPI(0x00, 0x00, 0x01))
	tp := tpiuFrame([]tpiuItem{{id: 1}, {b: 0x01}, {b: 'O'}, {b: 0x01}, {b: 'K'}, {b: 0x02}, {b: 0x34}, {b: 0x12}, {id: 2}, {b: 0x08}, {b: 0x09}})
	add("tpiu", "arm_tpiu", "uart", Packet{Bytes: append([]byte{0xff, 0xff, 0xff, 0x7f}, tp...), Complete: true},
		Packet{Bytes: append(tp, 1, 2), Complete: true}, Packet{Bytes: []byte{1, 2, 3}, Complete: true})
	add("etm", "arm_etmv3", "uart", mkSPI(0, 0, 0, 0, 0x80, 0x08, 0x20, 0x01, 0x01, 0x00, 0x08, 0x84, 0x88),
		mkSPI(0x25, 0x83, 0x02, 0x0c, 0x76, 0x7e, 0x66, 0xc4, 0x8a, 0x86),
		mkSPI(0x81, 0x80, 0x80, 0x80, 0x10, 0xfd, 0x80, 0x01, 0x42, 0x05, 0x04, 0x83, 0x01),
		mkSPI(0x01, 0x08, 0x00), mkSPI(0x81, 0x81), mkSPI(0x50))
	add("amulet", "amulet", "uart", mkSPI(0xd0, '1', 'A'), mkSPI(0xd5, '0', '2', 'F', 'f'), mkSPI(0xd6, '0', '3', '1', '2', '3', '4'),
		mkSPI(0xd7, '0', '4', 'h', 'i', 0), mkSPI(0xa0, 0x02, '0', '1', 0x2d), mkSPI(0xf0, 0xf1),
		mkSPI(0xd9, '0', '0', '0', '1', '0', '0', '0', '2', '0', '0', '0', '3', '0', '0', '0', '4'),
		mkSPI(0xd0, '1'), mkSPI(0xd5, 'x', 'y', '1', '2'), mkSPI(0x41), mkSPI(0xd7, '0', '1', 'a'))
	add("pan1321", "pan1321", "uart", Packet{Bytes: []byte("AT+JSLN=05,hello\r\nAT+JSEC=1,1,2,04,1234\r\n"), Complete: true},
		Packet{Bytes: []byte("ROK\r\nOK\r\nAT+JAAC=1\r"), Complete: true}, Packet{Bytes: []byte("\nAT+JSDA=03,abc\r\nERR=-5\r\n"), Complete: true},
		Packet{Bytes: []byte("AT+JRES\r\nAT+JPRO=0\r\nAT+JSDA=09,xy\r\nhello\r\nAT+JAAC=7\r\n"), Complete: true})

	rom := []byte{0x28, 0x2c, 0x1b, 0x6a, 0x05, 0x00, 0x00, 0}
	rom[7] = byte(crc8Maxim(rom[:7]))
	rom42 := []byte{0x42, 1, 2, 3, 4, 5, 6, 0}
	rom42[7] = byte(crc8Maxim(rom42[:7]))
	sp := []byte{0x91, 0x01, 0x4b, 0x46, 0x7f, 0xff, 0x0f, 0x10, 0}
	sp[8] = byte(crc8Maxim(sp[:8]))
	ow := func(b ...byte) Packet { return Packet{Bytes: b, ByteErr: make([]bool, len(b)), Complete: true} }
	owSet := []Packet{ow(append(append([]byte{0x55}, rom...), 0x44)...), ow(append([]byte{0xcc, 0xbe}, sp...)...),
		ow(append([]byte{0x33}, rom42...)...), ow(0xf0, 1, 2, 3, 4, 5, 6, 7, 8), ow(0xcc, 0x4e, 0x4b, 0x46, 0x7f),
		ow(0xa5, 0x12), {Complete: true}, {Err: FaultCRC, Complete: true}, ow(0x55, 1, 2), ow(0x99, 1)}
	add("1-wire net", "onewire_net", "onewire", owSet...)
	add("ds28ea00", "ds28ea00", "onewire", append(owSet, ow(0xcc, 0xa5, 0x0f, 0xf0), ow(0xcc, 0x48), ow(0xcc, 0x13))...)
	add("ds2408", "ds2408", "onewire", ow(0xcc, 0xf0, 0x88, 0x00, 0xff, 0x00), ow(0xcc, 0xf5, 0xff, 0xfe), ow(0xcc, 0x5a, 0x0f, 0xf0, 0xaa, 0x0f),
		ow(0xcc, 0x5a, 0x0f, 0xf1), ow(0xcc, 0xcc, 0x8b, 0x00, 0x01), ow(0xcc, 0xc3, 0xaa), ow(0xcc, 0x3c), ow(0xcc, 0x77))
	ws := []byte{0x0f, 0x00, 0x00, 1, 2, 3, 4, 5, 6, 7, 8, 0, 0}
	c := crc16Maxim(ws[:11])
	ws[11], ws[12] = byte(c), byte(c>>8)
	rs := []byte{0xaa, 0x00, 0x00, 0x07, 1, 2, 3, 4, 5, 6, 7, 8, 0, 0}
	c = crc16Maxim(rs[:12])
	rs[12], rs[13] = byte(c^1), byte(c>>8)
	add("ds243x", "ds243x", "onewire", ow(append([]byte{0xcc}, ws...)...), ow(append([]byte{0xcc}, rs...)...),
		ow(0xcc, 0x55, 0, 0, 7), ow(0xcc, 0xf0, 0x20, 0, 1, 2, 3), ow(0xcc, 0x5a, 0, 0), ow(0xcc, 0x33, 0, 0), ow(0xcc, 0xa5, 0, 0), ow(0xcc, 0x01))

	md := func(st, op, port, dev, data int, bad bool) Packet {
		h := st<<12 | op<<10 | port<<5 | dev
		p := Packet{Bytes: []byte{byte(h >> 8), byte(h), byte(data >> 8), byte(data)}, ByteErr: []bool{false, false, bad, bad},
			Addr: port, HasAddr: true, Read: op&2 != 0, Complete: true}
		return p
	}
	add("cfp", "cfp", "mdio", md(0, 0, 0, 1, 0x8000, false), md(0, 3, 0, 1, 0x000e, false), md(0, 0, 0, 1, 0x8010, false),
		md(0, 2, 0, 1, 0x1234, false), md(0, 3, 0, 1, 0x5678, false), md(0, 1, 0, 1, 0xa000, false), md(0, 3, 2, 1, 0, false),
		md(0, 1, 3, 1, 1, false), md(1, 2, 1, 2, 0x796d, false), md(0, 0, 0, 1, 0xa010, false), md(0, 3, 0, 1, 0xffff, true))
	setup := []byte{0x80, 0x06, 0x00, 0x01, 0x00, 0x00, 0x12, 0x00}
	dd := append(append([]byte(nil), setup...), 0, 0)
	desc := []byte{18, 1, 0x00, 0x02, 0, 0, 0, 64, 0x34, 0x12, 0x78, 0x56, 0, 1, 1, 2, 3, 1, 0, 0}
	add("usb enumeration", "usb_request", "usbls", mkUSB(0x5, 0x2a, 0x00), mkUSB(0xd, 0x00, 0x10), mkUSB(0x3, dd...),
		mkUSB(0x2), mkUSB(0x9, 0x00, 0x10), mkUSB(0xb, desc...), mkUSB(0x2), mkUSB(0x1, 0x00, 0x10), mkUSB(0xb, 0, 0),
		mkUSB(0x2), mkUSB(0xd, 0x00, 0x10), mkUSB(0x3, 0x00, 0x05, 0x07, 0, 0, 0, 0, 0, 0, 0), mkUSB(0xa),
		mkUSB(0xd, 0x07, 0x10), mkUSB(0x3, 0x21, 0x09, 0x00, 0x02, 0, 0, 1, 0, 0, 0), mkUSB(0xe),
		mkUSB(0x9, 0x07, 0x11), mkUSB(0x3, 1, 2, 3, 0, 0), mkUSB(0x6), mkUSB(0xc), mkUSB(0x4),
		Packet{Bytes: []byte{1, 2, 3}, ByteErr: []bool{false, true, true}, Addr: 3, HasAddr: true, Err: FaultCRC},
		mkUSB(0x1, 1), mkUSB(0x5), mkUSB(0xb))
	return vs
}

// atshaCmd is an ATSHA204A command transfer with its count and CRC.
// TRLC-LINKS: REQ-SDS-243
func atshaCmd(op, p1 byte, p2 int, data []byte) Packet {
	c := []byte{0, op, p1, byte(p2), byte(p2 >> 8)}
	c = append(c, data...)
	c[0] = byte(len(c) + 2)
	crc := atshaCRC(c)
	c = append(c, byte(crc), byte(crc>>8))
	return mkI2C(0x64, false, append([]byte{3}, c...)...)
}

// tpiuItem is a stream change (id > 0) or a data byte for tpiuFrame.
type tpiuItem struct {
	id int
	b  byte
}

// tpiuFrame packs stream changes and data into 16-byte TPIU frames: an ID
// takes an even byte (its low bit 1, and the auxiliary bit 0: it takes
// effect at once), data fills the rest.
// TRLC-LINKS: REQ-SDS-244
func tpiuFrame(items []tpiuItem) []byte {
	var out []byte
	for len(items) > 0 {
		f := make([]byte, 16)
		var aux byte
		pos := 0
		for pos < 15 && len(items) > 0 {
			it := items[0]
			if pos%2 == 0 {
				if it.id > 0 {
					f[pos] = byte(it.id<<1 | 1)
				} else {
					f[pos] = it.b &^ 1
					aux |= (it.b & 1) << (pos / 2)
				}
			} else {
				if it.id > 0 {
					break // an ID takes an even byte: the frame's odd byte is padding
				}
				f[pos] = it.b
			}
			items = items[1:]
			pos++
		}
		for ; pos < 15; pos++ { // pad with a no-op stream change to stream 0x7f
			if pos%2 == 0 {
				f[pos] = 0x7f<<1 | 1
			}
		}
		f[15] = aux
		out = append(out, f...)
	}
	return out
}

// layerRandom are random records for every layer (the parity and break
// suites' second half): random lengths and bytes, flags and addresses.
// TRLC-LINKS: REQ-SDS-241
func layerRandom(seed int64) []layerVec {
	r := rand.New(rand.NewSource(seed))
	var vs []layerVec
	for _, l := range layerList[1:] {
		for rec := 0; rec < 4; rec++ {
			v := layerVec{Name: fmt.Sprintf("random %s %d", l.ID, rec), ID: l.ID, Proto: l.Bus, Args: LayerArgs{Addr: r.Intn(130) - 2, Dir: r.Intn(3)}}
			for k := 0; k < 1+r.Intn(12); k++ {
				n := r.Intn(30)
				if r.Intn(4) == 0 {
					n = r.Intn(300)
				}
				b := make([]byte, n)
				for i := range b {
					b[i] = byte(r.Intn(256))
					if r.Intn(5) == 0 {
						b[i] = []byte{0, 0xff, 0x0f, 0x80, 0x55, 0x03, 0x40, 0x08}[r.Intn(8)]
					}
				}
				p := Packet{Bytes: b, ByteErr: make([]bool, n), Complete: r.Intn(5) > 0}
				for i := range p.ByteErr {
					p.ByteErr[i] = r.Intn(40) == 0
				}
				switch l.Bus {
				case "i2c":
					p.HasAddr, p.Addr, p.Read = r.Intn(10) > 0, []int{0x50, 0x51, 0x68, 0x48, 0x5a, 0x64, 0x3a, 0x20, 0x25, 0x15, 0x52, 0x10, 0x57}[r.Intn(13)], r.Intn(2) == 0
					if r.Intn(8) == 0 {
						p.Err = FaultNAK
					}
				case "usbls":
					p.HasAddr, p.Addr = true, r.Intn(16)
					if r.Intn(4) == 0 {
						p.Bytes = p.Bytes[:min(len(p.Bytes), 2)]
						p.ByteErr = p.ByteErr[:len(p.Bytes)]
					}
					if r.Intn(10) == 0 {
						p.Err = FaultCRC
					}
				case "mdio":
					p.HasAddr, p.Addr = true, r.Intn(32)
					p.Bytes = append(p.Bytes, 0, 0, 0, 0)[:4]
					p.Bytes[0] &= 0x3f
					p.ByteErr = p.ByteErr[:0]
					p.ByteErr = append(p.ByteErr, false, false, r.Intn(10) == 0, false)
				case "onewire":
					if r.Intn(3) == 0 && len(b) > 0 {
						b[0] = []byte{0x55, 0xcc, 0x33, 0xf0, 0xa5, 0x69, 0x3c, 0xec}[r.Intn(8)]
					}
					if r.Intn(10) == 0 {
						p.Err = FaultCRC
					}
				case "uart":
					if r.Intn(3) == 0 && len(b) > 0 {
						b[0] = []byte{0x0f, 0x00, 0xd5, 0xa0, 0xff, 'A'}[r.Intn(6)]
					}
				}
				v.Packets = append(v.Packets, p)
			}
			vs = append(vs, v)
		}
	}
	return vs
}
