// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"fmt"
	"open-sds/app/internal/decode"
	"sort"
	"sync"
)

// SerialDecoder receives chronological samples with their acquisition interval.
// It must not retain or mutate the sample slices. Registration is independent
// of acquisition, so additional decoders need no changes to the capture loop.
// TRLC-LINKS: REQ-SDS-013
type SerialDecoder struct {
	ID     int                                                               `json:"id"`
	Name   string                                                            `json:"name"`
	Decode func(a, b []uint8, sampleS float64, p SerialParams) decode.Result `json:"-"`
	Match  func([]decode.Span, SerialParams) (bool, int)                     `json:"-"`
}

var serialDecoders = struct {
	sync.RWMutex
	byID map[int]SerialDecoder
}{byID: map[int]SerialDecoder{}}

// RegisterSerialDecoder refuses replacement: an existing saved protocol ID
// must never silently acquire a different meaning.
// TRLC-LINKS: REQ-SDS-013
func RegisterSerialDecoder(d SerialDecoder) error {
	if d.ID <= 0 || d.Name == "" || d.Decode == nil || d.Match == nil {
		return fmt.Errorf("invalid serial decoder registration")
	}
	serialDecoders.Lock()
	defer serialDecoders.Unlock()
	if _, exists := serialDecoders.byID[d.ID]; exists {
		return fmt.Errorf("serial decoder %d already registered", d.ID)
	}
	serialDecoders.byID[d.ID] = d
	return nil
}

// SerialDecoderList is a sorted snapshot suitable for capability discovery.
// TRLC-LINKS: REQ-SDS-013
func SerialDecoderList() []SerialDecoder {
	serialDecoders.RLock()
	defer serialDecoders.RUnlock()
	result := make([]SerialDecoder, 0, len(serialDecoders.byID))
	for _, d := range serialDecoders.byID {
		result = append(result, d)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

// TRLC-LINKS: REQ-SDS-013
func serialDecoder(id int) (SerialDecoder, bool) {
	serialDecoders.RLock()
	defer serialDecoders.RUnlock()
	d, ok := serialDecoders.byID[id]
	return d, ok
}

// TRLC-LINKS: REQ-SDS-013
func init() {
	byteMatch := func(s []decode.Span, p SerialParams) (bool, int) { return matchBytes(s, p.Bytes) }
	for _, d := range []SerialDecoder{
		{ID: serUART, Name: "UART", Decode: func(a, b []uint8, sampleS float64, p SerialParams) decode.Result {
			return decode.DecodeUART(a, sampleS, decode.UARTCfg{Inverted: p.Inverted, Baud: p.Baud, Bits: p.Bits, Parity: p.Parity, Threshold: p.Threshold, HaveThr: p.HaveThr})
		}, Match: byteMatch},
		{ID: serI2C, Name: "I²C", Decode: func(a, b []uint8, sampleS float64, p SerialParams) decode.Result {
			return decode.DecodeI2C(a, b, sampleS, decode.I2CCfg{Inverted: p.Inverted, Threshold: p.Threshold, HaveThr: p.HaveThr})
		}, Match: matchI2C},
		{ID: serSPI, Name: "SPI", Decode: func(a, b []uint8, sampleS float64, p SerialParams) decode.Result {
			cfg := decode.SPICfg{Inverted: p.Inverted, CPOL: p.CPOL, CPHA: p.CPHA, MSB: p.MSB, Threshold: p.Threshold, HaveThr: p.HaveThr, DataLag: decode.SPIDataLag(p.ChA, sampleS)}
			if sampleS > 0 {
				cfg.DataDelay = SPIDelayFPGA(p.SPIDelayNs, p.ChA) * 1e-9 / sampleS // as the FPGA's receiver reads it
			}
			return decode.DecodeSPI(a, b, sampleS, cfg)
		}, Match: byteMatch},
		{ID: serManchester, Name: "Manchester", Decode: func(a, b []uint8, sampleS float64, p SerialParams) decode.Result {
			return decode.DecodeManchester(a, sampleS, decode.ManchesterCfg{Bitrate: p.Baud, IEEE: p.IEEE, MSB: p.MSB, Bits: p.Bits, Threshold: p.Threshold, HaveThr: p.HaveThr})
		}, Match: byteMatch},
		{ID: serSENT, Name: "SENT", Decode: func(a, b []uint8, sampleS float64, p SerialParams) decode.Result {
			return decode.DecodeSENT(a, sampleS, decode.SENTCfg{Inverted: p.Inverted, TickNs: p.TickNs, Nibbles: p.Nibbles, Threshold: p.Threshold, HaveThr: p.HaveThr})
		}, Match: byteMatch},
		{ID: serCAN, Name: "CAN FD", Decode: func(a, b []uint8, sampleS float64, p SerialParams) decode.Result {
			return decode.DecodeCANFD(a, sampleS, decode.CANFDCfg{NominalBaud: p.Baud, DataBaud: p.DataBaud, DominantLow: true, Threshold: p.Threshold, HaveThr: p.HaveThr})
		}, Match: byteMatch},
		{ID: serMIL1553, Name: "MIL-STD-1553", Decode: func(a, b []uint8, sampleS float64, p SerialParams) decode.Result {
			return decode.DecodeMIL1553(a, sampleS, decode.MIL1553Cfg{Inverted: p.Inverted, Bitrate: p.Baud, Threshold: p.Threshold, HaveThr: p.HaveThr})
		}, Match: byteMatch},
		{ID: serARINC, Name: "ARINC 429", Decode: func(a, b []uint8, sampleS float64, p SerialParams) decode.Result {
			return decode.DecodeARINC429(a, sampleS, decode.ARINC429Cfg{Bitrate: p.Baud, Threshold: p.Threshold, HaveThr: p.HaveThr})
		}, Match: byteMatch},
		{ID: serUSB, Name: "USB LS", Decode: func(a, b []uint8, sampleS float64, p SerialParams) decode.Result {
			return decode.DecodeUSBLS(a, sampleS, decode.USBLSCfg{Bitrate: p.Baud, Threshold: p.Threshold, HaveThr: p.HaveThr})
		}, Match: byteMatch},
		{ID: serFlexRay, Name: "FlexRay", Decode: func(a, b []uint8, sampleS float64, p SerialParams) decode.Result {
			return decode.DecodeFlexRay(a, sampleS, decode.FlexRayCfg{Bitrate: p.Baud, Threshold: p.Threshold, HaveThr: p.HaveThr})
		}, Match: byteMatch},
		{ID: serLIN, Name: "LIN", Decode: func(a, b []uint8, sampleS float64, p SerialParams) decode.Result {
			return decode.DecodeLIN(a, sampleS, decode.LINCfg{Baud: p.Baud, Threshold: p.Threshold, HaveThr: p.HaveThr})
		}, Match: byteMatch},
		{ID: serDMX, Name: "DMX512", Decode: func(a, b []uint8, sampleS float64, p SerialParams) decode.Result {
			return decode.DecodeDMX512(a, sampleS, decode.DMXCfg{Baud: p.Baud, Threshold: p.Threshold, HaveThr: p.HaveThr})
		}, Match: byteMatch},
		{ID: serMIDI, Name: "MIDI", Decode: func(a, b []uint8, sampleS float64, p SerialParams) decode.Result {
			return decode.DecodeMIDI(a, sampleS, decode.MIDICfg{Baud: p.Baud, Threshold: p.Threshold, HaveThr: p.HaveThr})
		}, Match: byteMatch},
		{ID: serModbus, Name: "Modbus RTU", Decode: func(a, b []uint8, sampleS float64, p SerialParams) decode.Result {
			return decode.DecodeModbus(a, sampleS, decode.ModbusCfg{Baud: p.Baud, Parity: p.Parity, Threshold: p.Threshold, HaveThr: p.HaveThr})
		}, Match: byteMatch},
		{ID: serOneWire, Name: "1-Wire", Decode: func(a, b []uint8, sampleS float64, p SerialParams) decode.Result {
			return decode.DecodeOneWire(a, sampleS, decode.OneWireCfg{Overdrive: p.Overdrive, Threshold: p.Threshold, HaveThr: p.HaveThr})
		}, Match: byteMatch},
		{ID: serWS281x, Name: "WS281x", Decode: func(a, b []uint8, sampleS float64, p SerialParams) decode.Result {
			return decode.DecodeWS281x(a, sampleS, decode.WS281xCfg{Threshold: p.Threshold, HaveThr: p.HaveThr})
		}, Match: byteMatch},
		{ID: serDHT, Name: "DHT", Decode: func(a, b []uint8, sampleS float64, p SerialParams) decode.Result {
			return decode.DecodeDHT(a, sampleS, decode.DHTCfg{Threshold: p.Threshold, HaveThr: p.HaveThr})
		}, Match: byteMatch},
		{ID: serCEC, Name: "HDMI-CEC", Decode: func(a, b []uint8, sampleS float64, p SerialParams) decode.Result {
			return decode.DecodeCEC(a, sampleS, decode.CECCfg{Threshold: p.Threshold, HaveThr: p.HaveThr})
		}, Match: byteMatch},
		{ID: serJ1850, Name: "J1850 VPW", Decode: func(a, b []uint8, sampleS float64, p SerialParams) decode.Result {
			return decode.DecodeJ1850(a, sampleS, decode.J1850Cfg{Threshold: p.Threshold, HaveThr: p.HaveThr})
		}, Match: byteMatch},
		{ID: serIRNEC, Name: "IR NEC", Decode: func(a, b []uint8, sampleS float64, p SerialParams) decode.Result {
			return decode.DecodeIRNEC(a, sampleS, decode.IRCfg{Threshold: p.Threshold, HaveThr: p.HaveThr})
		}, Match: byteMatch},
		{ID: serIRSIRC, Name: "IR Sony", Decode: func(a, b []uint8, sampleS float64, p SerialParams) decode.Result {
			return decode.DecodeIRSIRC(a, sampleS, decode.IRCfg{Threshold: p.Threshold, HaveThr: p.HaveThr})
		}, Match: byteMatch},
		{ID: serIRRC5, Name: "IR RC-5", Decode: func(a, b []uint8, sampleS float64, p SerialParams) decode.Result {
			return decode.DecodeIRRC5(a, sampleS, decode.IRCfg{Threshold: p.Threshold, HaveThr: p.HaveThr})
		}, Match: byteMatch},
		{ID: serIRRC6, Name: "IR RC-6", Decode: func(a, b []uint8, sampleS float64, p SerialParams) decode.Result {
			return decode.DecodeIRRC6(a, sampleS, decode.IRCfg{Threshold: p.Threshold, HaveThr: p.HaveThr})
		}, Match: byteMatch},
		{ID: serDALI, Name: "DALI", Decode: func(a, b []uint8, sampleS float64, p SerialParams) decode.Result {
			return decode.DecodeDALI(a, sampleS, decode.DALICfg{Threshold: p.Threshold, HaveThr: p.HaveThr})
		}, Match: byteMatch},
		{ID: serUSBPD, Name: "USB PD", Decode: func(a, b []uint8, sampleS float64, p SerialParams) decode.Result {
			return decode.DecodeUSBPD(a, sampleS, decode.USBPDCfg{Baud: p.Baud, Threshold: p.Threshold, HaveThr: p.HaveThr})
		}, Match: byteMatch},
		{ID: serSPDIF, Name: "S/PDIF", Decode: func(a, b []uint8, sampleS float64, p SerialParams) decode.Result {
			return decode.DecodeSPDIF(a, sampleS, decode.SPDIFCfg{Threshold: p.Threshold, HaveThr: p.HaveThr})
		}, Match: byteMatch},
	} {
		switch {
		case d.ID == serSENT, d.ID == serCAN, d.ID == serMIL1553, d.ID == serARINC, d.ID == serUSB, d.ID == serFlexRay,
			d.ID >= serLIN && d.ID != serMIDI:
			d.Match = func(s []decode.Span, p SerialParams) (bool, int) {
				return matchPackets(s, p.Bytes)
			}
		}
		if err := RegisterSerialDecoder(d); err != nil {
			panic(err)
		}
	}
}

// clockedTwoLine reports the clocked two-line protocols beyond SPI and
// I2C: PS/2, SWD and MDIO (clock and data), Wiegand (D0 and D1).
// TRLC-LINKS: REQ-SDS-013
func clockedTwoLine(proto int) bool {
	return proto == serPS2 || proto == serSWD || proto == serMDIO || proto == serWiegand
}
