// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
package analysis

// layerList is the BUS sheet's Layer list: none, then each bus's layers,
// in the web page's order too (layers.js LAYERS; the settings keep a
// layer's ID). Detection tries a bus's layers in this order, the ones with
// the stronger proof first: a clock, a sensor or a Nunchuk at 0x51..0x57
// before the EEPROMs that answer there.
var layerList = []Layer{
	{ID: "", Name: "None"},
	// SPI (the data line the scope holds)
	{ID: "spiflash", Name: "SPI flash", Bus: "spi", Line: "MOSI"},
	{ID: "sdcard", Name: "SD card", Bus: "spi", Line: "MOSI"},
	{ID: "avrisp", Name: "AVR ISP", Bus: "spi", Line: "MOSI"},
	{ID: "nrf24l01", Name: "nRF24L01", Bus: "spi", Line: "MOSI"},
	{ID: "nrf905", Name: "nRF905", Bus: "spi", Line: "MOSI"},
	{ID: "cc1101", Name: "CC1101", Bus: "spi", Line: "MOSI"},
	{ID: "rfm12", Name: "RFM12", Bus: "spi", Line: "MOSI"},
	{ID: "mrf24j40", Name: "MRF24J40", Bus: "spi", Line: "MOSI"},
	{ID: "enc28j60", Name: "ENC28J60", Bus: "spi", Line: "MOSI"},
	{ID: "st25r39xx", Name: "ST25R39xx", Bus: "spi", Line: "MOSI"},
	{ID: "adxl345", Name: "ADXL345", Bus: "spi", Line: "MOSI"},
	{ID: "adns5020", Name: "ADNS-5020", Bus: "spi", Line: "MOSI"},
	{ID: "ade77xx", Name: "ADE7758", Bus: "spi", Line: "MOSI"},
	{ID: "adf435x", Name: "ADF4350/1", Bus: "spi", Line: "MOSI"},
	{ID: "ad5626", Name: "AD5626 DAC", Bus: "spi", Line: "MOSI"},
	{ID: "ad79x0", Name: "AD7910/20 ADC", Bus: "spi", Line: "MISO"},
	{ID: "ltc242x", Name: "LTC2421/2 ADC", Bus: "spi", Line: "MISO"},
	{ID: "max72xx", Name: "MAX7219/21", Bus: "spi", Line: "MOSI"},
	{ID: "x2444m", Name: "X2444M/P", Bus: "spi", Line: "MOSI"},
	{ID: "rgbled", Name: "RGB LEDs", Bus: "spi", Line: "MOSI"},
	{ID: "nesgamepad", Name: "NES gamepad", Bus: "spi", Line: "MISO"},
	{ID: "ssi32", Name: "SSI32", Bus: "spi", Line: "MOSI"},
	// I2C
	{ID: "edid", Name: "EDID", Bus: "i2c"},
	{ID: "xfp", Name: "XFP module", Bus: "i2c"},
	{ID: "ds1307", Name: "DS1307 RTC", Bus: "i2c"},
	{ID: "rtc8564", Name: "RTC-8564", Bus: "i2c"},
	{ID: "atsha204a", Name: "ATSHA204A", Bus: "i2c"},
	{ID: "mlx90614", Name: "MLX90614", Bus: "i2c"},
	{ID: "nunchuk", Name: "Wii Nunchuk", Bus: "i2c"},
	{ID: "eeprom24xx", Name: "24xx EEPROM", Bus: "i2c"},
	{ID: "hdcp", Name: "HDCP", Bus: "i2c"},
	{ID: "lm75", Name: "LM75", Bus: "i2c"},
	{ID: "ltc26x7", Name: "LTC2607/17/27", Bus: "i2c"},
	{ID: "tca6408a", Name: "TCA6408A", Bus: "i2c"},
	{ID: "pca9571", Name: "PCA9571", Bus: "i2c"},
	{ID: "mcp23008", Name: "MCP23008", Bus: "i2c"},
	{ID: "mcp23017", Name: "MCP23017", Bus: "i2c"},
	{ID: "mxc6225xu", Name: "MXC6225XU", Bus: "i2c"},
	{ID: "i2cdemux", Name: "By address", Bus: "i2c"},
	{ID: "i2cfilter", Name: "Address filter", Bus: "i2c"},
	// UART
	{ID: "sbus", Name: "S.BUS", Bus: "uart"},
	{ID: "arm_itm", Name: "ARM ITM", Bus: "uart"},
	{ID: "arm_tpiu", Name: "ARM TPIU", Bus: "uart"},
	{ID: "arm_etmv3", Name: "ARM ETMv3", Bus: "uart"},
	{ID: "pan1321", Name: "PAN1321", Bus: "uart"},
	{ID: "amulet", Name: "Amulet ASCII", Bus: "uart"},
	// 1-Wire
	{ID: "ds28ea00", Name: "DS28EA00/DS18B20", Bus: "onewire"},
	{ID: "ds2408", Name: "DS2408", Bus: "onewire"},
	{ID: "ds243x", Name: "DS243x", Bus: "onewire"},
	{ID: "onewire_net", Name: "1-Wire ROM", Bus: "onewire"},
	// MDIO
	{ID: "cfp", Name: "CFP module", Bus: "mdio"},
	// USB
	{ID: "usb_request", Name: "USB requests", Bus: "usbls"},
}

// layerImpls are the layers' notes, evidence and matches.
var layerImpls = map[string]layerFns{
	"spiflash":   {notes: func(ps []Packet) { eachNote(ps, flashNote) }, detect: flashDetect, match: flashMatch},
	"sdcard":     {notes: sdNotes, detect: sdDetect, match: sdMatch},
	"avrisp":     {notes: func(ps []Packet) { eachNote(ps, avrNote) }, detect: avrDetect, match: spiFirst(4)},
	"nrf24l01":   {notes: func(ps []Packet) { eachNote(ps, nrf24Note) }, match: spiFirst(1)},
	"nrf905":     {notes: func(ps []Packet) { eachNote(ps, nrf905Note) }, match: spiFirst(1)},
	"cc1101":     {notes: func(ps []Packet) { eachNote(ps, cc1101Note) }, match: spiFirst(1)},
	"rfm12":      {notes: func(ps []Packet) { eachNote(ps, rfm12Note) }, match: spiFirst(2)},
	"mrf24j40":   {notes: func(ps []Packet) { eachNote(ps, mrf24Note) }, match: spiFirst(2)},
	"enc28j60":   {notes: encNotes, match: spiFirst(1)},
	"st25r39xx":  {notes: st25Notes, match: spiFirst(1)},
	"adxl345":    {notes: func(ps []Packet) { eachNote(ps, adxl345Note) }, match: spiFirst(1)},
	"adns5020":   {notes: func(ps []Packet) { eachNote(ps, adnsNote) }, match: spiFirst(1)},
	"ade77xx":    {notes: func(ps []Packet) { eachNote(ps, ade7758Note) }, match: spiFirst(1)},
	"adf435x":    {notes: func(ps []Packet) { eachNote(ps, adf435xNote) }, match: spiFirst(4)},
	"ad5626":     {notes: func(ps []Packet) { eachNote(ps, ad5626Note) }, match: spiFirst(2)},
	"ad79x0":     {notes: func(ps []Packet) { eachNote(ps, ad79x0Note) }, match: spiFirst(2)},
	"ltc242x":    {notes: func(ps []Packet) { eachNote(ps, ltc242xNote) }, match: spiFirst(1)},
	"max72xx":    {notes: func(ps []Packet) { eachNote(ps, max72xxNote) }, match: spiFirst(2)},
	"x2444m":     {notes: func(ps []Packet) { eachNote(ps, x2444Note) }, match: spiFirst(1)},
	"rgbled":     {notes: func(ps []Packet) { eachNote(ps, rgbNote) }, match: spiFirst(3)},
	"nesgamepad": {notes: func(ps []Packet) { eachNote(ps, nesNote) }, match: spiFirst(1)},
	"ssi32":      {notes: func(ps []Packet) { eachNote(ps, ssi32Note) }, match: spiFirst(1)},

	"edid":       {notes: edidNotes, detect: edidDetect, match: i2cAddrMatch},
	"xfp":        {notes: xfpNotes, detect: xfpDetect, match: i2cAddrMatch},
	"eeprom24xx": {notes: eepromNotes, detect: eepromDetect, match: eepromMatch},
	"ds1307": {notes: func(ps []Packet) { rtcNotes(ps, ds1307Spec, 0x68) },
		detect: func(ps []Packet) string { return rtcDetect(ps, ds1307Spec, 0x68) }, match: i2cFirst(1)},
	"rtc8564": {notes: func(ps []Packet) { rtcNotes(ps, rtc8564Spec, 0x51) },
		detect: func(ps []Packet) string { return rtcDetect(ps, rtc8564Spec, 0x51) }, match: i2cFirst(1)},
	"atsha204a": {notes: atshaNotes, detect: atshaDetect, match: i2cFirst(3)},
	"mlx90614":  {notes: mlxNotes, detect: mlxDetect, match: i2cFirst(1)},
	"nunchuk":   {notes: nunchukNotes, detect: nunchukDetect, match: i2cAddrMatch},
	"hdcp":      {notes: hdcpNotes, detect: hdcpDetect, match: i2cFirst(1)},
	"lm75":      {notes: lm75Notes, match: i2cFirst(1)},
	"ltc26x7":   {notes: ltcNotes, match: i2cFirst(1)},
	"tca6408a":  {notes: tcaNotes, match: i2cFirst(1)},
	"pca9571":   {notes: pcaNotes, match: i2cAddrMatch},
	"mcp23008": {notes: func(ps []Packet) {
		regNotes(ps, 0x20, 0x27, "MCP23008", func(int) map[int]string { return mcp23008Regs })
	}, match: i2cFirst(1)},
	"mcp23017": {notes: func(ps []Packet) {
		regNotes(ps, 0x20, 0x27, "MCP23017", func(int) map[int]string { return mcp23017Regs })
	}, match: i2cFirst(1)},
	"mxc6225xu": {notes: mxcNotes, match: i2cFirst(1)},
	"i2cdemux":  {notes: demuxNotes, match: i2cAddrMatch},
	"i2cfilter": {match: i2cAddrMatch}, // the packet list itself is filtered (FilterI2C)

	"sbus":      {notes: sbusNotes, detect: sbusDetect, match: sbusMatch},
	"arm_itm":   {notes: itmNotes, detect: itmDetect, match: itmMatch},
	"arm_tpiu":  {notes: tpiuNotes, detect: tpiuDetect},
	"arm_etmv3": {notes: etmNotes},
	"pan1321":   {notes: panNotes, detect: panDetect},
	"amulet":    {notes: func(ps []Packet) { eachNote(ps, amuletNote) }, match: uartFirst(1)},

	"ds28ea00": {notes: owDevNotes(ds28ea00Fn), match: owMatch,
		detect: func(ps []Packet) string { return owFamilyDetect(ps, "DS28EA00, DS18B20", 0x42, 0x28) }},
	"ds2408": {notes: owDevNotes(ds2408Fn), match: owMatch,
		detect: func(ps []Packet) string { return owFamilyDetect(ps, "DS2408", 0x29) }},
	"ds243x": {notes: owDevNotes(ds243xFn), match: owMatch,
		detect: func(ps []Packet) string { return owFamilyDetect(ps, "DS243x", 0x2d, 0x23, 0x33) }},
	"onewire_net": {notes: owNetNotes, detect: owNetDetect, match: owMatch},

	"cfp": {notes: cfpNotes, detect: cfpDetect},

	"usb_request": {notes: usbNotes, detect: usbDetect, match: usbMatch},
}

// spiFirst matches an SPI transaction by its first n bytes after the burst's
// start: the command (and the register or address) that names it.
// TRLC-LINKS: REQ-SDS-248
func spiFirst(n int) func(p Packet) ([]string, int) {
	return func(p Packet) ([]string, int) {
		if len(p.Bytes) == 0 {
			return nil, -1
		}
		// "SOF" and the bytes in one list (sofToks builds the bytes' list and
		// copies it again).
		b := p.Bytes[:min(n, len(p.Bytes))]
		toks := make([]string, 1, 1+len(b))
		toks[0] = "SOF"
		for _, v := range b {
			toks = append(toks, hx(int(v), 2))
		}
		return toks, -1
	}
}

// i2cFirst matches an I2C transfer by its device address and first n bytes
// (a register pointer, a command).
// TRLC-LINKS: REQ-SDS-248
func i2cFirst(n int) func(p Packet) ([]string, int) {
	return func(p Packet) ([]string, int) {
		if !p.HasAddr {
			return nil, -1
		}
		if p.Read || len(p.Bytes) == 0 {
			return nil, p.Addr
		}
		return hexToks(p.Bytes[:min(n, len(p.Bytes))]), p.Addr
	}
}

// i2cAddrMatch matches an I2C transfer by its device address alone.
// TRLC-LINKS: REQ-SDS-248
func i2cAddrMatch(p Packet) ([]string, int) {
	if !p.HasAddr {
		return nil, -1
	}
	return nil, p.Addr
}

// uartFirst matches a UART burst by its first n bytes.
// TRLC-LINKS: REQ-SDS-248
func uartFirst(n int) func(p Packet) ([]string, int) {
	return func(p Packet) ([]string, int) {
		if len(p.Bytes) == 0 {
			return nil, -1
		}
		return hexToks(p.Bytes[:min(n, len(p.Bytes))]), -1
	}
}
