// Package framecalc holds the per-frame calculations that both the LCD
// renderer and the panel's workspace tools need: the protocol decode of a
// published frame and its magnitude spectrum. Each is cached by frame
// content, so the decode strip and the BUS packet list, or the FFT plot and
// the SPECTRUM peak table, share one computation per frame instead of
// repeating it per redraw (the ARM renders about 20 times a second).
// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package framecalc

import (
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"open-sds/app/internal/decode"
	"open-sds/app/internal/engine"
)

// Protocol numbers, shared by the panel's decode setting and the LCD.
// ProtoAuto is no decoder: it asks for the bus to be detected once and its
// protocol and settings filled in (a setup saved when Auto was a decoding
// mode holds it, and loads as that request).
const (
	ProtoOff = iota
	ProtoAuto
	ProtoUART
	ProtoI2C
	ProtoSPI
	ProtoManchester
	ProtoSENT
	ProtoCAN
	ProtoMIL1553
	ProtoARINC429
	ProtoUSB
	ProtoFlexRay
	ProtoLIN
	ProtoDMX
	ProtoMIDI
	ProtoModbus
	ProtoOneWire
	ProtoWS281x
	ProtoDHT
	ProtoCEC
	ProtoJ1850
	ProtoIRNEC
	ProtoIRSIRC
	ProtoIRRC5
	ProtoIRRC6
	ProtoDALI
	ProtoUSBPD
	ProtoSPDIF
	ProtoPS2
	ProtoSWD
	ProtoMDIO
	ProtoWiegand
	ProtoQuad
	ProtoStepper
	ProtoPWM
	ProtoRCEncode
	ProtoOOK
	ProtoMiller
	ProtoMorse
	ProtoDCF77
	ProtoEM4100
	ProtoEM4305
	ProtoT55xx
	ProtoSWIM
	ProtoSDQ
	ProtoPJDL
	ProtoDSI
	ProtoQi
	ProtoSDCard
	ProtoPDI
	ProtoCJTAG
	ProtoMaple
	ProtoCaliper
	ProtoIRMP
	NumProtos
)

// protoNumbers are the protocol numbers by a decode's Proto name.
var protoNumbers = map[string]int{"uart": ProtoUART, "i2c": ProtoI2C, "spi": ProtoSPI, "manchester": ProtoManchester,
	"sent": ProtoSENT, "canfd": ProtoCAN, "mil1553": ProtoMIL1553, "arinc429": ProtoARINC429, "usbls": ProtoUSB,
	"flexray": ProtoFlexRay, "lin": ProtoLIN, "dmx512": ProtoDMX, "midi": ProtoMIDI, "modbus": ProtoModbus,
	"onewire": ProtoOneWire, "ws281x": ProtoWS281x, "dht": ProtoDHT, "cec": ProtoCEC, "j1850": ProtoJ1850,
	"irnec": ProtoIRNEC, "irsirc": ProtoIRSIRC, "irrc5": ProtoIRRC5, "irrc6": ProtoIRRC6, "dali": ProtoDALI,
	"usbpd": ProtoUSBPD, "spdif": ProtoSPDIF, "ps2": ProtoPS2, "swd": ProtoSWD, "mdio": ProtoMDIO, "wiegand": ProtoWiegand,
	"quad": ProtoQuad, "stepper": ProtoStepper, "pwm": ProtoPWM, "rcencode": ProtoRCEncode, "ook": ProtoOOK,
	"miller": ProtoMiller, "morse": ProtoMorse, "dcf77": ProtoDCF77, "em4100": ProtoEM4100, "em4305": ProtoEM4305,
	"t55xx": ProtoT55xx, "swim": ProtoSWIM, "sdq": ProtoSDQ, "pjdl": ProtoPJDL, "dsi": ProtoDSI, "qi": ProtoQi,
	"sdcard": ProtoSDCard, "pdi": ProtoPDI, "cjtag": ProtoCJTAG, "maple": ProtoMaple, "caliper": ProtoCaliper,
	"irmp": ProtoIRMP}

// ProtoOf is the protocol number of a decode's Proto name (decode.Result's
// "uart", "lin"...), 0 for none.
// TRLC-LINKS: REQ-SDS-018
func ProtoOf(name string) int { return protoNumbers[name] }

// ResultName is the Proto name a decode of protocol p reports ("uart",
// "i2c"...), "" for none.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-241
func ResultName(p int) string {
	for name, n := range protoNumbers {
		if n == p {
			return name
		}
	}
	return ""
}

// TwoLine reports a protocol of two lines (ChA and ChB): I2C, SPI, the
// clocked PS/2, SWD, MDIO and Wiegand, an encoder's or a stepper's pair and
// the clocked SD card, PDI, cJTAG, Maple and caliper buses.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-258
func TwoLine(p int) bool {
	switch p {
	case ProtoI2C, ProtoSPI, ProtoPS2, ProtoSWD, ProtoMDIO, ProtoWiegand, ProtoQuad, ProtoStepper, ProtoSDCard,
		ProtoPDI, ProtoCJTAG, ProtoMaple, ProtoCaliper:
		return true
	}
	return false
}

// DecodeParams selects the decoder and its settings.
type DecodeParams struct {
	Proto      int // ProtoOff..ProtoIRMP
	Baud       int // UART baud, or the bit rate of the bit-rate protocols
	ChA, ChB   int // UART source / I2C SCL / SPI CLK, and I2C SDA / SPI DATA (0 = C1)
	CPOL, CPHA bool
	LSB        bool   // SPI least significant bit first
	Format     string // span text: "hex", "ascii" or "both"
	// DataDelayS is where SPI's data line is read after the clock's
	// sampling edge (0: at the edge, the channels' phase only).
	DataDelayS float64
	// Parity is Modbus RTU's character framing ("even", "odd", "none"; ""
	// the decoder finds it), and a UART's ("" none); Inverted, a UART line
	// idling low (S.BUS).
	Parity   string
	Inverted bool
}

// Formats are the span text formats by the decode setting's format index.
var Formats = [3]string{"hex", "ascii", "both"}

// Params are the decode settings as the panel holds them, made into
// DecodeParams. The panel's packet list and the LCD's strip both take them
// from here, so they ask Decode the same question: they share one decode of
// the record (the cache is keyed by the parameters), and the strip reads the
// bytes the list does (the strip without SPI's data delay read a late
// 104 MHz device one bit behind the list).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-214
func Params(proto, baud, chA, chB int, cpol, cpha, lsb bool, format int, spiDelayNs float64, parity string) DecodeParams {
	p := DecodeParams{Proto: proto, Baud: baud, ChA: chA, ChB: chB, CPOL: cpol, CPHA: cpha, LSB: lsb,
		Format: Formats[((format%3)+3)%3]}
	if proto == ProtoSPI {
		p.DataDelayS = spiDelayNs * 1e-9
	}
	if proto == ProtoModbus {
		p.Parity = parity
	}
	return p
}

type decodeKey struct {
	id, seq bool
	ident   uint64
	valid   int
	sampleS float64
	p       DecodeParams
}

// decodeCache holds the last few decodes: the LCD, the packet list and the
// remote can ask with different settings or frames in turn, and one entry
// would decode the held record again on every display tick.
var decodeCache struct {
	sync.Mutex
	e    [4]decodeEntry
	next int
}

// decodeEntry is one cached decode.
type decodeEntry struct {
	key decodeKey
	res decode.Result
	ok  bool
}

// Channel returns a frame channel's valid samples; C2 falls back to C1 when
// the frame has no second channel.
// TRLC-LINKS: REQ-SDS-018
func Channel(f *engine.Frame, ch, valid int) []uint8 {
	if ch == 1 && len(f.C2) >= valid {
		return f.C2[:valid]
	}
	return f.C1[:valid]
}

// Valid clamps a frame's valid-sample count into its C1 slice.
// TRLC-LINKS: REQ-SDS-018
func Valid(f *engine.Frame) int {
	return min(max(f.Valid, 1), len(f.C1))
}

// Decode decodes f's valid samples with p, reusing the last result for the
// same frame content and settings: a deep record's decode takes most of a
// second on the ARM, and the strip, the packet list and the eye all need it.
// A zero Seq and Content (a hand-built frame) is never cached.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-214
func Decode(f *engine.Frame, p DecodeParams) decode.Result {
	if f == nil || len(f.C1) == 0 || p.Proto == ProtoOff || p.Proto == ProtoAuto {
		return decode.Result{}
	}
	valid := Valid(f)
	key := decodeKey{valid: valid, sampleS: f.SampleS, p: p}
	switch {
	case f.Content != 0:
		key.id, key.ident = true, f.Content
	case f.Seq != 0:
		key.seq, key.ident = true, f.Seq
	}
	cacheable := key.id || key.seq
	ch := func(c int) []uint8 { return Channel(f, c, valid) }
	decodeCache.Lock()
	defer decodeCache.Unlock()
	if cacheable {
		for i := range decodeCache.e {
			if c := &decodeCache.e[i]; c.ok && c.key == key {
				return c.res
			}
		}
	}
	var res decode.Result
	switch p.Proto {
	case ProtoUART:
		cfg := decode.UARTCfg{Baud: p.Baud, Format: p.Format, Parity: p.Parity, Inverted: p.Inverted}
		if f.HasMatchEnd && f.EdgeX >= 0 && f.SampleS > 0 { // the hardware matcher's character fixes the framing
			cfg.Anchor, cfg.HasAnchor = f.EdgeX+f.MatchEndS/f.SampleS, true
		}
		res = decode.DecodeUART(ch(p.ChA), f.SampleS, cfg)
	case ProtoI2C:
		res = decode.DecodeI2C(ch(p.ChA), ch(p.ChB), f.SampleS, decode.I2CCfg{Format: p.Format})
	case ProtoSPI:
		cfg := decode.SPICfg{CPOL: p.CPOL, CPHA: p.CPHA, MSB: !p.LSB, Format: p.Format, DataLag: decode.SPIDataLag(p.ChA, f.SampleS)}
		if f.SampleS > 0 {
			cfg.DataDelay = p.DataDelayS / f.SampleS
		}
		res = decode.DecodeSPI(ch(p.ChA), ch(p.ChB), f.SampleS, cfg)
	case ProtoManchester:
		// MSB first, as Auto found it and the web decodes it (LSB first reads
		// 12 34 as 48 2C).
		res = decode.DecodeManchester(ch(p.ChA), f.SampleS, decode.ManchesterCfg{Bitrate: p.Baud, IEEE: true, MSB: true, Format: p.Format})
	case ProtoSENT:
		res = decode.DecodeSENT(ch(p.ChA), f.SampleS, decode.SENTCfg{})
	case ProtoCAN:
		res = decode.DecodeCANFD(ch(p.ChA), f.SampleS, decode.CANFDCfg{NominalBaud: p.Baud, DominantLow: true})
	case ProtoMIL1553:
		res = decode.DecodeMIL1553(ch(p.ChA), f.SampleS, decode.MIL1553Cfg{Bitrate: p.Baud})
	case ProtoARINC429:
		res = decode.DecodeARINC429(ch(p.ChA), f.SampleS, decode.ARINC429Cfg{Bitrate: p.Baud})
	case ProtoUSB:
		res = decode.DecodeUSBLS(ch(p.ChA), f.SampleS, decode.USBLSCfg{Bitrate: p.Baud})
	case ProtoFlexRay:
		res = decode.DecodeFlexRay(ch(p.ChA), f.SampleS, decode.FlexRayCfg{Bitrate: p.Baud})
	default:
		res = decodeMore(p, ch, f.SampleS)
	}
	if f.PeakDetect && f.Ordered && !EnvelopeResolves(res) {
		res = decode.Result{Proto: res.Proto, Error: decode.Names[res.Proto] + unresolvedNote}
	}
	if cacheable {
		decodeCache.e[decodeCache.next] = decodeEntry{key: key, res: res, ok: true}
		decodeCache.next = (decodeCache.next + 1) % len(decodeCache.e)
	}
	return res
}

// decodeMore decodes the protocols beyond the first ten with p's settings;
// ch gives a channel's samples.
// TRLC-LINKS: REQ-SDS-018
func decodeMore(p DecodeParams, ch func(int) []uint8, sampleS float64) decode.Result {
	a := ch(p.ChA)
	ir := decode.IRCfg{Format: p.Format}
	two := decode.ClockedCfg{Format: p.Format}
	switch p.Proto {
	case ProtoLIN:
		return decode.DecodeLIN(a, sampleS, decode.LINCfg{Baud: p.Baud, Format: p.Format})
	case ProtoDMX:
		return decode.DecodeDMX512(a, sampleS, decode.DMXCfg{Format: p.Format})
	case ProtoMIDI:
		return decode.DecodeMIDI(a, sampleS, decode.MIDICfg{Format: p.Format})
	case ProtoModbus:
		return decode.DecodeModbus(a, sampleS, decode.ModbusCfg{Baud: p.Baud, Parity: p.Parity, Format: p.Format})
	case ProtoOneWire:
		return decode.DecodeOneWire(a, sampleS, decode.OneWireCfg{Overdrive: p.Baud >= 100000, Format: p.Format})
	case ProtoWS281x:
		return decode.DecodeWS281x(a, sampleS, decode.WS281xCfg{Format: p.Format})
	case ProtoDHT:
		return decode.DecodeDHT(a, sampleS, decode.DHTCfg{Format: p.Format})
	case ProtoCEC:
		return decode.DecodeCEC(a, sampleS, decode.CECCfg{Format: p.Format})
	case ProtoJ1850:
		return decode.DecodeJ1850(a, sampleS, decode.J1850Cfg{Format: p.Format})
	case ProtoIRNEC:
		return decode.DecodeIRNEC(a, sampleS, ir)
	case ProtoIRSIRC:
		return decode.DecodeIRSIRC(a, sampleS, ir)
	case ProtoIRRC5:
		return decode.DecodeIRRC5(a, sampleS, ir)
	case ProtoIRRC6:
		return decode.DecodeIRRC6(a, sampleS, ir)
	case ProtoDALI:
		return decode.DecodeDALI(a, sampleS, decode.DALICfg{Format: p.Format})
	case ProtoUSBPD:
		return decode.DecodeUSBPD(a, sampleS, decode.USBPDCfg{Format: p.Format})
	case ProtoSPDIF:
		return decode.DecodeSPDIF(a, sampleS, decode.SPDIFCfg{Format: p.Format})
	case ProtoPS2:
		return decode.DecodePS2(a, ch(p.ChB), sampleS, two)
	case ProtoSWD:
		return decode.DecodeSWD(a, ch(p.ChB), sampleS, two)
	case ProtoMDIO:
		return decode.DecodeMDIO(a, ch(p.ChB), sampleS, two)
	case ProtoWiegand:
		return decode.DecodeWiegand(a, ch(p.ChB), sampleS, two)
	}
	return decodePhys(p, a, ch(p.ChB), sampleS)
}

// decodePhys decodes the sigrok protocols beyond the first thirty on line a
// (and b, the two-line ones'). Each finds its own timing and variant on the
// record (a tag's data rate, an RC code's trits or bytes, a PJON mode, the
// IR protocol), as Auto's detection does.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-258
func decodePhys(p DecodeParams, a, b []uint8, sampleS float64) decode.Result {
	two := decode.ClockedCfg{Format: p.Format}
	lc := decode.LineCfg{Format: p.Format}
	switch p.Proto {
	case ProtoQuad:
		return decode.DecodeQuadrature(a, b, sampleS, two)
	case ProtoStepper:
		return decode.DecodeStepper(a, b, sampleS, two)
	case ProtoSDCard:
		return decode.DecodeSDCard(a, b, sampleS, two)
	case ProtoPDI:
		return decode.DecodePDI(a, b, sampleS, two)
	case ProtoCJTAG:
		return decode.DecodeCJTAG(a, b, sampleS, two)
	case ProtoMaple:
		return decode.DecodeMaple(a, b, sampleS, two)
	case ProtoCaliper:
		return decode.DecodeCaliper(a, b, sampleS, two)
	}
	line := map[int]func([]uint8, float64, decode.LineCfg) decode.Result{
		ProtoPWM: decode.DecodePWM, ProtoRCEncode: decode.DecodeRCEncode, ProtoOOK: decode.DecodeOOK,
		ProtoMiller: decode.DecodeMiller, ProtoMorse: decode.DecodeMorse, ProtoDCF77: decode.DecodeDCF77,
		ProtoEM4100: decode.DecodeEM4100, ProtoEM4305: decode.DecodeEM4305, ProtoT55xx: decode.DecodeT55xx,
		ProtoSWIM: decode.DecodeSWIM, ProtoSDQ: decode.DecodeSDQ, ProtoPJDL: decode.DecodePJDL, ProtoDSI: decode.DecodeDSI,
		ProtoQi: decode.DecodeQi, ProtoIRMP: decode.DecodeIRMP}[p.Proto]
	if line == nil {
		return decode.Result{}
	}
	return line(a, sampleS, lc)
}

// Detect finds the bus on f's valid samples: the protocol, channel roles and
// settings that decode it best (decode.Autodetect), with the spans in the
// frame's sample indices. It is Auto's search on one frame, run when a
// detection is asked for, never per frame.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-214, REQ-SDS-222
func Detect(f *engine.Frame, format string) decode.Result {
	if f == nil || len(f.C1) == 0 {
		return decode.Result{}
	}
	valid := Valid(f)
	// Strided: a full Autodetect of a 500 k-sample screen took 14 s on the
	// scope's ARM.
	res, st := decode.AutodetectFast(Channel(f, 0, valid), Channel(f, 1, valid), f.SampleS, format)
	if k := int(st/f.SampleS + 0.5); k > 1 {
		for i := range res.Spans { // back to the frame's sample indices
			res.Spans[i].I0 *= k
			res.Spans[i].I1 *= k
		}
		res.SPB *= float64(k)
	}
	if f.PeakDetect && f.Ordered && res.Proto == "off" && res.Error == "no protocol matched" {
		// a bus the envelope cannot resolve is not found either (a 115200 Bd
		// UART at 500us/div matches no protocol there)
		res.Error = "no protocol resolved at this timebase - S/DIV faster, or SINGLE"
	}
	return res
}

// MinEnvelopeFeature is the shortest bus feature, in points, a live envelope
// decodes: well over its (min, max) bucket of two points (a CAN frame of
// ideal edges fails at 2.8 points a bit; a 115200 Bd UART at 500us/div's 3.6
// reads 0A as 02 and 20 as 30).
const MinEnvelopeFeature = 4.0

// EnvelopeResolves reports whether a decode of a live envelope can stand. The
// envelope holds a (min, max) pair per bucket; a line that changes within a
// bucket aliases there (a fast clock reads as a clock of two buckets, its
// data as starts and stops: I2C at 500us/div lists packets of no bytes), so a
// bus whose shortest feature is not well over a bucket is
// not decoded from it.
// TRLC-LINKS: REQ-SDS-214
func EnvelopeResolves(res decode.Result) bool {
	fs := decode.EnvelopeFeatureSamples(res)
	return fs == 0 || fs >= MinEnvelopeFeature
}

// unresolvedNote ends the error of a decode refused on a live envelope that
// does not resolve the bus (EnvelopeResolves, Unresolved).
const unresolvedNote = " not resolved at this timebase - S/DIV faster, or SINGLE"

// Unresolved reports a decode refused because the live envelope does not
// resolve the bus: sampled frames would decode it.
// TRLC-LINKS: REQ-SDS-214
func Unresolved(res decode.Result) bool {
	return !res.OK && strings.HasSuffix(res.Error, unresolvedNote)
}

// ---- spectrum ----

// MaxFFT caps the transform length so a frame's spectrum stays well under
// the render budget on the ARM.
const MaxFFT = 8192

// Spec is one channel's Hann-windowed magnitude spectrum: the record
// block-mean decimated to N (a power of two, at most MaxFFT) samples, mean
// removed, transformed. Mags is the half spectrum (bin 0 = DC) in code
// units; a sine of amplitude A codes reads A·N/4 at its bin.
type Spec struct {
	Mags   []float64
	N      int
	Stride int     // record samples per transform sample
	EffNyq float64 // Hz at bin N/2 (the decimated Nyquist)
	// SpanHz is what the spectrum covers: EffNyq, or a decimated capture's
	// filter bandwidth when lower (Mags then ends there: above it the bins
	// hold the filter's stopband, not the signal).
	SpanHz float64
	BinHz  float64
	Peak   float64 // the largest magnitude, DC excluded
	PeakK  int
}

// EnvPair is 2 for a peak-detect envelope, whose values come in (min, max)
// pairs per bucket, else 1.
// TRLC-LINKS: REQ-SDS-070
func EnvPair(f *engine.Frame) int {
	if f.PeakDetect {
		return 2
	}
	return 1
}

// PairStride is the block length that reduces a record to its spectrum
// samples. An envelope's blocks hold whole (min, max) pairs, so each spectrum
// sample is bucket midpoints: transforming the pairs themselves puts their
// alternation at the top of the axis as a rising false spectrum (a UART at
// 10 us/div).
// TRLC-LINKS: REQ-SDS-070
func PairStride(f *engine.Frame, stride int) int {
	stride = max(stride, 1)
	if p := EnvPair(f); stride%p != 0 {
		stride += p - stride%p
	}
	return stride
}

// BlockMeans reduces src to n samples, each the mean of stride consecutive
// samples: a boxcar low-pass ahead of the decimation. Taking every stride-th
// sample instead folded everything above the reduced Nyquist back into the
// plot (a 1 MHz square's harmonics and the 200 MHz interleave spur at stride 2).
// TRLC-LINKS: REQ-SDS-070
func BlockMeans(src []uint8, n, stride int) []float64 {
	stride = max(stride, 1)
	out := make([]float64, n)
	for i := range out {
		sum := 0
		for _, v := range src[i*stride : (i+1)*stride] {
			sum += int(v)
		}
		out[i] = float64(sum) / float64(stride)
	}
	return out
}

// FFT is an in-place iterative radix-2 Cooley-Tukey transform (len must be
// a power of two).
// TRLC-LINKS: REQ-SDS-070
func FFT(re, im []float64) {
	n := len(re)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}
	// Each level's twiddles follow one recurrence from 1 at every block's
	// start: made once a level, they are the very values each block made
	// for itself, and the butterflies no longer wait on the recurrence.
	twr, twi := make([]float64, n/2), make([]float64, n/2)
	for l := 2; l <= n; l <<= 1 {
		ang := -2 * math.Pi / float64(l)
		wr, wi := math.Cos(ang), math.Sin(ang)
		h := l / 2
		cr, ci := 1.0, 0.0
		for k := 0; k < h; k++ {
			twr[k], twi[k] = cr, ci
			cr, ci = cr*wr-ci*wi, cr*wi+ci*wr
		}
		wre, wim := twr[:h], twi[:h]
		for i := 0; i < n; i += l {
			ar, ai := re[i:i+h], im[i:i+h]
			br, bi := re[i+h:i+l], im[i+h:i+l]
			br, bi, ai = br[:len(ar)], bi[:len(ar)], ai[:len(ar)]
			wre, wim := wre[:len(ar)], wim[:len(ar)]
			for k := range ar {
				cr, ci := wre[k], wim[k]
				tr := br[k]*cr - bi[k]*ci
				ti := br[k]*ci + bi[k]*cr
				br[k] = ar[k] - tr
				bi[k] = ai[k] - ti
				ar[k] += tr
				ai[k] += ti
			}
		}
	}
}

// Mags Hann-windows samples (mean removed), transforms them and returns the
// half-spectrum magnitudes and the largest of them, DC excluded (Hann
// leakage at DC would otherwise become a false 0 dB anchor). The samples are
// real: their even and odd ones go through one complex transform of half the
// length, and the window and the unpacking twiddles are tabled per length -
// an 8192-point spectrum took 86 ms on the scope's ARM, two a SPECTRUM frame.
// TRLC-LINKS: REQ-SDS-070
func Mags(samples []float64) ([]float64, float64, int) {
	n := len(samples)
	half := n / 2
	tab := magsTables(n)
	var mean float64
	for _, v := range samples {
		mean += v
	}
	mean /= float64(n)
	zr, zi := make([]float64, half), make([]float64, half)
	for k := 0; k < half; k++ {
		zr[k] = (samples[2*k] - mean) * tab.win[2*k]
		zi[k] = (samples[2*k+1] - mean) * tab.win[2*k+1]
	}
	FFT(zr, zi)
	mags := make([]float64, half)
	peak, peakK := 1e-9, 1
	for k := 0; k < half; k++ {
		j := half - k // (half-k) mod half, without a division a bin
		if k == 0 {
			j = 0
		}
		er, ei := (zr[k]+zr[j])/2, (zi[k]-zi[j])/2 // the even samples' transform
		or, oi := (zi[k]+zi[j])/2, (zr[j]-zr[k])/2 // the odd samples'
		c, s := tab.cos[k], tab.sin[k]
		mags[k] = hypotFinite(er+c*or+s*oi, ei+c*oi-s*or)
		if k >= 1 && mags[k] > peak {
			peak, peakK = mags[k], k
		}
	}
	return mags, peak, peakK
}

// magsTable is Mags' Hann window and unpacking twiddles for one length.
type magsTable struct {
	win, cos, sin []float64
}

var magsCache struct {
	sync.Mutex
	byN map[int]*magsTable
}

// magsTables is the table for n samples, made once.
// TRLC-LINKS: REQ-SDS-070
func magsTables(n int) *magsTable {
	magsCache.Lock()
	defer magsCache.Unlock()
	if t := magsCache.byN[n]; t != nil {
		return t
	}
	t := &magsTable{win: make([]float64, n), cos: make([]float64, n/2), sin: make([]float64, n/2)}
	for i := range t.win {
		t.win[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1))
	}
	for k := range t.cos {
		t.cos[k], t.sin[k] = math.Cos(2*math.Pi*float64(k)/float64(n)), math.Sin(2*math.Pi*float64(k)/float64(n))
	}
	if magsCache.byN == nil {
		magsCache.byN = map[int]*magsTable{}
	}
	magsCache.byN[n] = t
	return t
}

type specKey struct {
	id, seq bool
	ident   uint64
	valid   int
	ch      int
	sampleS float64
	pd      bool
}

var specCache struct {
	sync.Mutex
	slot [2]struct {
		key  specKey
		spec Spec
		ok   bool
		at   time.Time // when it was taken (a held spectrum's age; zero unheld)
	}
}

// Spectrum returns channel ch's spectrum of frame f at sampleS seconds per
// sample, cached per channel by frame content. ok is false for a frame
// without per-sample data (an envelope) or too few samples.
// TRLC-LINKS: REQ-SDS-070, REQ-SDS-216
func Spectrum(f *engine.Frame, ch int, sampleS float64) (Spec, bool) {
	return spectrum(f, ch, sampleS, 0)
}

// specHold is how long SpectrumHeld keeps a spectrum (ns); 0: none.
var specHold atomic.Int64

// SetSpectrumHold sets how long SpectrumHeld keeps a channel's spectrum
// for the next frames of the same acquisition (0: a new one each frame).
// TRLC-LINKS: REQ-SDS-070, REQ-SDS-216
func SetSpectrumHold(d time.Duration) { specHold.Store(int64(max(d, 0))) }

// SpectrumHeld is Spectrum for the display: while the spectrum hold lasts
// it answers a later frame of the same acquisition (length, channel, rate)
// with the spectrum it took, not a new one. Two 8192-point spectra a
// picture cost the one core more than the capture loop's whole frame; the
// display holds them when it is what keeps the loop under its rate.
// TRLC-LINKS: REQ-SDS-070, REQ-SDS-216
func SpectrumHeld(f *engine.Frame, ch int, sampleS float64) (Spec, bool) {
	return spectrum(f, ch, sampleS, time.Duration(specHold.Load()))
}

// spectrum is Spectrum, a cached one of the same acquisition answering for
// hold after it was taken.
// TRLC-LINKS: REQ-SDS-070, REQ-SDS-216
func spectrum(f *engine.Frame, ch int, sampleS float64, hold time.Duration) (Spec, bool) {
	if f == nil || f.IsEnv || len(f.C1) == 0 {
		return Spec{}, false
	}
	ch &= 1
	valid := Valid(f)
	if ch == 1 && len(f.C2) < valid {
		return Spec{}, false
	}
	n := 1
	for n*2 <= valid/EnvPair(f) && n < MaxFFT {
		n <<= 1
	}
	if n < 16 {
		return Spec{}, false
	}
	key := specKey{valid: valid, ch: ch, sampleS: sampleS, pd: f.PeakDetect}
	switch {
	case f.Content != 0:
		key.id, key.ident = true, f.Content
	case f.Seq != 0:
		key.seq, key.ident = true, f.Seq
	}
	cacheable := key.id || key.seq
	specCache.Lock()
	defer specCache.Unlock()
	s := &specCache.slot[ch]
	if cacheable && s.ok && s.key == key {
		return s.spec, true
	}
	var now time.Time
	if hold > 0 {
		now = time.Now()
		k := s.key
		k.id, k.seq, k.ident = key.id, key.seq, key.ident
		if cacheable && s.ok && k == key && now.Sub(s.at) < hold {
			return s.spec, true
		}
	}
	stride := PairStride(f, valid/n)
	sp := specOf(BlockMeans(Channel(f, ch, valid), n, stride), stride, sampleS, f.BandwidthHz)
	s.key, s.spec, s.ok, s.at = key, sp, cacheable, now
	return sp, true
}

// specOf is the spectrum of samples, each the mean of stride record samples
// sampleS apart, cut at a decimated capture's filter bandwidth bw (0: none).
// TRLC-LINKS: REQ-SDS-070
func specOf(samples []float64, stride int, sampleS, bw float64) Spec {
	n := len(samples)
	mags, peak, peakK := Mags(samples)
	sp := Spec{Mags: mags, N: n, Stride: stride, Peak: peak, PeakK: peakK}
	if sampleS > 0 {
		sp.EffNyq = 0.5 / sampleS / float64(stride)
		sp.BinHz = sp.EffNyq / float64(n/2)
		sp.SpanHz = sp.EffNyq
		// A decimated capture's filter ends the spectrum at its bandwidth: past
		// it the bins hold nothing (a capture filtered at 4.2 MHz drawn to
		// 15.6 MHz shows its harmonics above 4.2 MHz gone)
		if bw > 0 && bw < sp.EffNyq {
			kb := min(len(mags), max(4, int(math.Ceil(bw/sp.BinHz))))
			sp.Mags, sp.SpanHz, sp.Peak, sp.PeakK = mags[:kb], float64(kb)*sp.BinHz, 1e-9, 1
			for k := 1; k < kb; k++ {
				if mags[k] > sp.Peak {
					sp.Peak, sp.PeakK = mags[k], k
				}
			}
		}
	}
	return sp
}

// RowFFT is the length of a spectrogram row's transform: a 2048-point
// spectrum has 1024 bins, more than the waterfall's columns.
const RowFFT = 2048

// SpectrumRows is channel ch's record of frame f as spectrogram rows, oldest
// first: the record in consecutive stretches, each transformed at RowFFT
// points with the stride and span of the record's own spectrum (Spectrum),
// so one capture paints several rows of the time it spans. A record whose
// spectrum is shorter than two rows' gives that spectrum alone.
// TRLC-LINKS: REQ-SDS-068, REQ-SDS-070
func SpectrumRows(f *engine.Frame, ch int, sampleS float64) []Spec {
	full, ok := Spectrum(f, ch, sampleS)
	if !ok {
		return nil
	}
	k := full.N / RowFFT
	if k < 2 {
		return []Spec{full}
	}
	src := Channel(f, ch&1, Valid(f))
	seg := RowFFT * full.Stride
	out := make([]Spec, 0, k)
	for i := 0; i < k; i++ {
		out = append(out, specOf(BlockMeans(src[i*seg:(i+1)*seg], RowFFT, full.Stride), full.Stride, sampleS, f.BandwidthHz))
	}
	return out
}
