// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"errors"
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
	// Edges is Decode over digital lines' edges (decode.EdgeLine), for a
	// decoder that reads its lines only through their edges: the digital
	// stream decodes it in time that follows the bus's edges. Nil for the
	// decoders that read samples.
	Edges func(a, b *decode.EdgeLine, sampleS float64, p SerialParams) decode.Result `json:"-"`
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
		return errors.New("invalid serial decoder registration")
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
	d, ok := serialDecoders.byID[id]
	serialDecoders.RUnlock()
	return d, ok
}

// TRLC-LINKS: REQ-SDS-013
func init() {
	byteMatch := func(s []decode.Span, p SerialParams) (bool, int) { return matchBytes(s, p.Bytes) }
	for _, d := range serialDecoderTable() {
		if d.Match == nil {
			d.Match = byteMatch
		}
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

// serialDecoderTable is the built-in decoders; a nil Match is filled in
// by init.
// TRLC-LINKS: REQ-SDS-013
func serialDecoderTable() []SerialDecoder {
	return []SerialDecoder{
		{ID: serUART, Name: "UART", Decode: func(a, _ []uint8, sampleS float64, p SerialParams) decode.Result {
			return decode.DecodeUART(a, sampleS, decode.UARTCfg{Inverted: p.Inverted, Baud: p.Baud, Bits: p.Bits, Parity: p.Parity, Threshold: p.Threshold, HaveThr: p.HaveThr})
		}},
		{ID: serI2C, Name: "I²C", Decode: func(a, b []uint8, sampleS float64, p SerialParams) decode.Result {
			return decode.DecodeI2C(a, b, sampleS, decode.I2CCfg{Inverted: p.Inverted, Threshold: p.Threshold, HaveThr: p.HaveThr})
		}, Match: matchI2C},
		{ID: serSPI, Name: "SPI", Decode: func(a, b []uint8, sampleS float64, p SerialParams) decode.Result {
			cfg := decode.SPICfg{Inverted: p.Inverted, CPOL: p.CPOL, CPHA: p.CPHA, MSB: p.MSB, Threshold: p.Threshold, HaveThr: p.HaveThr, DataLag: decode.SPIDataLag(p.ChA, sampleS)}
			if sampleS > 0 {
				cfg.DataDelay = SPIDelayFPGA(p.SPIDelayNs, p.ChA) * 1e-9 / sampleS // as the FPGA's receiver reads it
			}
			return decode.DecodeSPI(a, b, sampleS, cfg)
		}},
		lineDecoder(serManchester, "Manchester", func(p SerialParams) decode.ManchesterCfg {
			return decode.ManchesterCfg{Bitrate: p.Baud, IEEE: p.IEEE, MSB: p.MSB, Bits: p.Bits, Threshold: p.Threshold, HaveThr: p.HaveThr}
		}, decode.DecodeManchester, nil),
		lineDecoder(serSENT, "SENT", func(p SerialParams) decode.SENTCfg {
			return decode.SENTCfg{Inverted: p.Inverted, TickNs: p.TickNs, Nibbles: p.Nibbles, Threshold: p.Threshold, HaveThr: p.HaveThr}
		}, decode.DecodeSENT, nil),
		lineDecoder(serCAN, "CAN FD", func(p SerialParams) decode.CANFDCfg {
			return decode.CANFDCfg{NominalBaud: p.Baud, DataBaud: p.DataBaud, DominantLow: true, Threshold: p.Threshold, HaveThr: p.HaveThr}
		}, decode.DecodeCANFD, nil),
		lineDecoder(serMIL1553, "MIL-STD-1553", func(p SerialParams) decode.MIL1553Cfg {
			return decode.MIL1553Cfg{Inverted: p.Inverted, Bitrate: p.Baud, Threshold: p.Threshold, HaveThr: p.HaveThr}
		}, decode.DecodeMIL1553, nil),
		lineDecoder(serARINC, "ARINC 429", func(p SerialParams) decode.ARINC429Cfg {
			return decode.ARINC429Cfg{Bitrate: p.Baud, Threshold: p.Threshold, HaveThr: p.HaveThr}
		}, decode.DecodeARINC429, nil),
		lineDecoder(serUSB, "USB LS", func(p SerialParams) decode.USBLSCfg {
			return decode.USBLSCfg{Bitrate: p.Baud, Threshold: p.Threshold, HaveThr: p.HaveThr}
		}, decode.DecodeUSBLS, nil),
		lineDecoder(serFlexRay, "FlexRay", func(p SerialParams) decode.FlexRayCfg {
			return decode.FlexRayCfg{Bitrate: p.Baud, Threshold: p.Threshold, HaveThr: p.HaveThr}
		}, decode.DecodeFlexRay, nil),
		lineDecoder(serLIN, "LIN", func(p SerialParams) decode.LINCfg {
			return decode.LINCfg{Baud: p.Baud, Threshold: p.Threshold, HaveThr: p.HaveThr}
		}, decode.DecodeLIN, decode.DecodeLINEdges),
		lineDecoder(serDMX, "DMX512", func(p SerialParams) decode.DMXCfg {
			return decode.DMXCfg{Baud: p.Baud, Threshold: p.Threshold, HaveThr: p.HaveThr}
		}, decode.DecodeDMX512, decode.DecodeDMX512Edges),
		lineDecoder(serMIDI, "MIDI", func(p SerialParams) decode.MIDICfg {
			return decode.MIDICfg{Baud: p.Baud, Threshold: p.Threshold, HaveThr: p.HaveThr}
		}, decode.DecodeMIDI, nil),
		lineDecoder(serModbus, "Modbus RTU", func(p SerialParams) decode.ModbusCfg {
			return decode.ModbusCfg{Baud: p.Baud, Parity: p.Parity, Threshold: p.Threshold, HaveThr: p.HaveThr}
		}, decode.DecodeModbus, decode.DecodeModbusEdges),
		lineDecoder(serOneWire, "1-Wire", func(p SerialParams) decode.OneWireCfg {
			return decode.OneWireCfg{Overdrive: p.Overdrive, Threshold: p.Threshold, HaveThr: p.HaveThr}
		}, decode.DecodeOneWire, decode.DecodeOneWireEdges),
		lineDecoder(serWS281x, "WS281x", func(p SerialParams) decode.WS281xCfg {
			return decode.WS281xCfg{Threshold: p.Threshold, HaveThr: p.HaveThr}
		}, decode.DecodeWS281x, decode.DecodeWS281xEdges),
		lineDecoder(serDHT, "DHT", func(p SerialParams) decode.DHTCfg {
			return decode.DHTCfg{Threshold: p.Threshold, HaveThr: p.HaveThr}
		}, decode.DecodeDHT, decode.DecodeDHTEdges),
		lineDecoder(serCEC, "HDMI-CEC", func(p SerialParams) decode.CECCfg {
			return decode.CECCfg{Threshold: p.Threshold, HaveThr: p.HaveThr}
		}, decode.DecodeCEC, decode.DecodeCECEdges),
		lineDecoder(serJ1850, "J1850 VPW", func(p SerialParams) decode.J1850Cfg {
			return decode.J1850Cfg{Threshold: p.Threshold, HaveThr: p.HaveThr}
		}, decode.DecodeJ1850, decode.DecodeJ1850Edges),
		lineDecoder(serIRNEC, "IR NEC", irCfg, decode.DecodeIRNEC, nil),
		lineDecoder(serIRSIRC, "IR Sony", irCfg, decode.DecodeIRSIRC, nil),
		lineDecoder(serIRRC5, "IR RC-5", irCfg, decode.DecodeIRRC5, nil),
		lineDecoder(serIRRC6, "IR RC-6", irCfg, decode.DecodeIRRC6, nil),
		lineDecoder(serDALI, "DALI", func(p SerialParams) decode.DALICfg {
			return decode.DALICfg{Threshold: p.Threshold, HaveThr: p.HaveThr}
		}, decode.DecodeDALI, decode.DecodeDALIEdges),
		lineDecoder(serUSBPD, "USB PD", func(p SerialParams) decode.USBPDCfg {
			return decode.USBPDCfg{Baud: p.Baud, Threshold: p.Threshold, HaveThr: p.HaveThr}
		}, decode.DecodeUSBPD, decode.DecodeUSBPDEdges),
		lineDecoder(serSPDIF, "S/PDIF", func(p SerialParams) decode.SPDIFCfg {
			return decode.SPDIFCfg{Threshold: p.Threshold, HaveThr: p.HaveThr}
		}, decode.DecodeSPDIF, decode.DecodeSPDIFEdges),
		clockedDecoder(serPS2, "PS/2", decode.DecodePS2, decode.DecodePS2Edges),
		clockedDecoder(serSWD, "SWD", decode.DecodeSWD, decode.DecodeSWDEdges),
		clockedDecoder(serMDIO, "MDIO", decode.DecodeMDIO, decode.DecodeMDIOEdges),
		clockedDecoder(serWiegand, "Wiegand", decode.DecodeWiegand, decode.DecodeWiegandEdges),
		clockedDecoder(serQuad, "Quadrature", decode.DecodeQuadrature, decode.DecodeQuadratureEdges),
		clockedDecoder(serStepper, "Stepper", decode.DecodeStepper, decode.DecodeStepperEdges),
		lineDecoder(serPWM, "PWM", lineCfgFixed, decode.DecodePWM, decode.DecodePWMEdges),
		lineDecoder(serRCEncode, "RC encode", lineCfgFixed, decode.DecodeRCEncode, decode.DecodeRCEncodeEdges),
		lineDecoder(serOOK, "OOK", lineCfgFixed, decode.DecodeOOK, decode.DecodeOOKEdges),
		lineDecoder(serMiller, "Miller", lineCfgRate, decode.DecodeMiller, decode.DecodeMillerEdges),
		lineDecoder(serMorse, "Morse", lineCfgFixed, decode.DecodeMorse, decode.DecodeMorseEdges),
		lineDecoder(serDCF77, "DCF77", lineCfgRate, decode.DecodeDCF77, decode.DecodeDCF77Edges),
		lineDecoder(serEM4100, "EM4100", lineCfgRate, decode.DecodeEM4100, nil),
		lineDecoder(serEM4305, "EM4305", lineCfgRate, decode.DecodeEM4305, nil),
		lineDecoder(serT55xx, "T55xx", lineCfgRate, decode.DecodeT55xx, nil),
		lineDecoder(serSWIM, "SWIM", lineCfgFixed, decode.DecodeSWIM, decode.DecodeSWIMEdges),
		lineDecoder(serSDQ, "SDQ", lineCfgRate, decode.DecodeSDQ, decode.DecodeSDQEdges),
		lineDecoder(serPJDL, "PJON", lineCfgFixed, decode.DecodePJDL, decode.DecodePJDLEdges),
		lineDecoder(serDSI, "DSI", lineCfgFixed, decode.DecodeDSI, decode.DecodeDSIEdges),
		lineDecoder(serQi, "Qi", lineCfgFixed, decode.DecodeQi, decode.DecodeQiEdges),
		clockedDecoder(serSDCard, "SD card", decode.DecodeSDCard, decode.DecodeSDCardEdges),
		clockedDecoder(serPDI, "PDI", decode.DecodePDI, decode.DecodePDIEdges),
		clockedDecoder(serCJTAG, "cJTAG", decode.DecodeCJTAG, decode.DecodeCJTAGEdges),
		clockedDecoder(serMaple, "Maple", decode.DecodeMaple, decode.DecodeMapleEdges),
		clockedDecoder(serCaliper, "Caliper", decode.DecodeCaliper, decode.DecodeCaliperEdges),
		lineDecoder(serIRMP, "IR multi", lineCfgFixed, decode.DecodeIRMP, nil),
	}
}

// lineDecoder is a single-line decoder: samples reads the first line's
// samples, edges (nil for none) its edges, each with the settings cfg makes
// of the request.
// TRLC-LINKS: REQ-SDS-013
func lineDecoder[C any](id int, name string, cfg func(SerialParams) C,
	samples func([]uint8, float64, C) decode.Result, edges func(*decode.EdgeLine, float64, C) decode.Result,
) SerialDecoder {
	d := SerialDecoder{ID: id, Name: name, Decode: func(a, _ []uint8, sampleS float64, p SerialParams) decode.Result {
		return samples(a, sampleS, cfg(p))
	}}
	if edges != nil {
		d.Edges = func(a, _ *decode.EdgeLine, sampleS float64, p SerialParams) decode.Result {
			return edges(a, sampleS, cfg(p))
		}
	}
	return d
}

// clockedDecoder is a clocked two-line decoder, from samples or edges.
// TRLC-LINKS: REQ-SDS-013
func clockedDecoder(id int, name string,
	samples func(a, b []uint8, sampleS float64, cfg decode.ClockedCfg) decode.Result,
	edges func(a, b *decode.EdgeLine, sampleS float64, cfg decode.ClockedCfg) decode.Result,
) SerialDecoder {
	return SerialDecoder{
		ID: id, Name: name,
		Decode: func(a, b []uint8, sampleS float64, p SerialParams) decode.Result {
			return samples(a, b, sampleS, clockedCfg(p))
		},
		Edges: func(a, b *decode.EdgeLine, sampleS float64, p SerialParams) decode.Result {
			return edges(a, b, sampleS, clockedCfg(p))
		},
	}
}

// irCfg is a request's threshold as the infrared decoders take it.
// TRLC-LINKS: REQ-SDS-013
func irCfg(p SerialParams) decode.IRCfg {
	return decode.IRCfg{Threshold: p.Threshold, HaveThr: p.HaveThr}
}

// lineCfgFixed is lineCfg for a decoder that measures its own rate.
// TRLC-LINKS: REQ-SDS-013
func lineCfgFixed(p SerialParams) decode.LineCfg { return lineCfg(p, false) }

// lineCfgRate is lineCfg for a decoder that takes the request's rate.
// TRLC-LINKS: REQ-SDS-013
func lineCfgRate(p SerialParams) decode.LineCfg { return lineCfg(p, true) }

// clockedCfg is a request's two thresholds as the clocked decoders take them.
// TRLC-LINKS: REQ-SDS-013
func clockedCfg(p SerialParams) decode.ClockedCfg {
	return decode.ClockedCfg{Threshold: p.Threshold, HaveThr: p.HaveThr, ThresholdB: p.ThresholdB, HaveThrB: p.HaveThrB}
}

// lineCfg is a request's settings as the single-line decoders beyond the
// first thirty take them: Bits their sub-protocol, Baud the rate they would
// otherwise measure where rate says the decoder takes one (DCF77's scale,
// a Miller, EM4100 or SDQ bit rate, a reader's field frequency).
// TRLC-LINKS: REQ-SDS-013
func lineCfg(p SerialParams, rate bool) decode.LineCfg {
	c := decode.LineCfg{Threshold: p.Threshold, HaveThr: p.HaveThr, Variant: p.Bits}
	if rate {
		c.Baud = p.Baud
	}
	return c
}

// clockedTwoLine reports the clocked two-line protocols beyond SPI and
// I2C: PS/2, SWD and MDIO (clock and data), Wiegand (D0 and D1), a
// quadrature encoder (A and B), a stepper (STEP and DIR), an SD card's CLK
// and CMD, PDI, cJTAG, Maple (SDCKA and SDCKB) and calipers.
// TRLC-LINKS: REQ-SDS-013
func clockedTwoLine(proto int) bool {
	switch proto {
	case serPS2, serSWD, serMDIO, serWiegand, serQuad, serStepper, serSDCard, serPDI, serCJTAG, serMaple, serCaliper:
		return true
	}
	return false
}
