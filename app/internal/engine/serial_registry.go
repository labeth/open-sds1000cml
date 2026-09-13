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
func serialDecoder(id int) (SerialDecoder, bool) {
	serialDecoders.RLock()
	defer serialDecoders.RUnlock()
	d, ok := serialDecoders.byID[id]
	return d, ok
}
func init() {
	byteMatch := func(s []decode.Span, p SerialParams) (bool, int) { return matchBytes(s, p.Bytes) }
	for _, d := range []SerialDecoder{
		{ID: serUART, Name: "UART", Decode: func(a, b []uint8, sampleS float64, p SerialParams) decode.Result {
			return decode.DecodeUART(a, sampleS, decode.UARTCfg{Baud: p.Baud, Bits: p.Bits, Parity: p.Parity, Threshold: p.Threshold, HaveThr: p.HaveThr})
		}, Match: byteMatch},
		{ID: serI2C, Name: "I²C", Decode: func(a, b []uint8, sampleS float64, p SerialParams) decode.Result {
			return decode.DecodeI2C(a, b, sampleS, decode.I2CCfg{Threshold: p.Threshold, HaveThr: p.HaveThr})
		}, Match: matchI2C},
		{ID: serSPI, Name: "SPI", Decode: func(a, b []uint8, sampleS float64, p SerialParams) decode.Result {
			return decode.DecodeSPI(a, b, sampleS, decode.SPICfg{CPOL: p.CPOL, CPHA: p.CPHA, MSB: p.MSB, Threshold: p.Threshold, HaveThr: p.HaveThr})
		}, Match: byteMatch},
		{ID: serManchester, Name: "Manchester", Decode: func(a, b []uint8, sampleS float64, p SerialParams) decode.Result {
			return decode.DecodeManchester(a, sampleS, decode.ManchesterCfg{Bitrate: p.Baud, IEEE: p.IEEE, MSB: p.MSB, Bits: p.Bits, Threshold: p.Threshold, HaveThr: p.HaveThr})
		}, Match: byteMatch},
		{ID: serSENT, Name: "SENT", Decode: func(a, b []uint8, sampleS float64, p SerialParams) decode.Result {
			return decode.DecodeSENT(a, sampleS, decode.SENTCfg{TickNs: p.TickNs, Nibbles: p.Nibbles, Threshold: p.Threshold, HaveThr: p.HaveThr})
		}, Match: byteMatch},
		{ID: serCAN, Name: "CAN FD", Decode: func(a, b []uint8, sampleS float64, p SerialParams) decode.Result {
			return decode.DecodeCANFD(a, sampleS, decode.CANFDCfg{NominalBaud: p.Baud, DataBaud: p.DataBaud, DominantLow: true, Threshold: p.Threshold, HaveThr: p.HaveThr})
		}, Match: byteMatch},
		{ID: serMIL1553, Name: "MIL-STD-1553", Decode: func(a, b []uint8, sampleS float64, p SerialParams) decode.Result {
			return decode.DecodeMIL1553(a, sampleS, decode.MIL1553Cfg{Bitrate: p.Baud, Threshold: p.Threshold, HaveThr: p.HaveThr})
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
	} {
		if err := RegisterSerialDecoder(d); err != nil {
			panic(err)
		}
	}
}
