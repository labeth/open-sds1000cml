// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/measure"
	"github.com/labeth/open-dso-core/sample"
)

// The engine's hardware-free parts live in the core engine; these names
// keep the scope's packages naming them through this one.
const (
	AcqAverage      = core.AcqAverage
	AcqEres         = core.AcqEres
	AcqNormal       = core.AcqNormal
	AcqPeak         = core.AcqPeak
	AcqPrecision    = core.AcqPrecision
	BodeOff         = core.BodeOff
	BodeOn          = core.BodeOn
	ClientReplyMax  = core.ClientReplyMax
	CondGreater     = core.CondGreater
	CondInside      = core.CondInside
	CondLess        = core.CondLess
	MILCommand      = core.MILCommand
	MaskOff         = core.MaskOff
	MaskStopFail    = core.MaskStopFail
	MaskTest        = core.MaskTest
	SerialOff       = core.SerialOff
	SerialProtocols = core.SerialProtocols
	SerialTrigger   = core.SerialTrigger
	SoundLog2       = core.SoundLog2
	TrigEdge        = core.TrigEdge
	TrigPulse       = core.TrigPulse
	TrigSlope       = core.TrigSlope
	TrigVideo       = core.TrigVideo
	ViewScope       = core.ViewScope
	ViewWeb         = core.ViewWeb
	ZoneOff         = core.ZoneOff
	ZoneTrigger     = core.ZoneTrigger
)

type (
	// BodePoint is core.BodePoint.
	BodePoint = core.BodePoint
	// ClientChunk is core.ClientChunk.
	ClientChunk = core.ClientChunk
	// ClientStreamInfo is core.ClientStreamInfo.
	ClientStreamInfo = core.ClientStreamInfo
	// ClientWords is core.ClientWords.
	ClientWords = core.ClientWords
	// Clock is core.Clock.
	Clock = core.Clock
	// Mask is core.Mask.
	Mask = core.Mask
	// MaskFail is core.MaskFail.
	MaskFail = core.MaskFail
	// PatternElem is core.PatternElem.
	PatternElem = core.PatternElem
	// SerialDecoder is core.SerialDecoder.
	SerialDecoder = core.SerialDecoder
	// SerialParams is core.SerialParams.
	SerialParams = core.SerialParams
	// SoundBlock is core.SoundBlock.
	SoundBlock = core.SoundBlock
	// SoundInfo is core.SoundInfo.
	SoundInfo = core.SoundInfo
	// SoundMark is core.SoundMark.
	SoundMark = core.SoundMark
	// SoundMarkConfig is core.SoundMarkConfig.
	SoundMarkConfig = core.SoundMarkConfig
	// SoundScale is core.SoundScale.
	SoundScale = core.SoundScale
	// SoundTake is core.SoundTake.
	SoundTake = core.SoundTake
	// TrigQual is core.TrigQual.
	TrigQual = core.TrigQual
	// TrigType is core.TrigType.
	TrigType = core.TrigType
	// ViewSurface is core.ViewSurface.
	ViewSurface = core.ViewSurface
	// Zone is core.Zone.
	Zone = core.Zone
)

// AppendClientChunkHeads is core.AppendClientChunkHeads.
// TRLC-LINKS: REQ-SDS-021
func AppendClientChunkHeads(dst []byte, chunks []core.ClientChunk) []byte {
	return core.AppendClientChunkHeads(dst, chunks)
}

// BuildMaskFromEnvelope is core.BuildMaskFromEnvelope.
// TRLC-LINKS: REQ-SDS-021
func BuildMaskFromEnvelope(lo, hi []uint8, winCols, tolCols, tolCodes, ch int) *core.Mask {
	return core.BuildMaskFromEnvelope(lo, hi, winCols, tolCols, tolCodes, ch)
}

// EdgeRead is core.EdgeRead.
// TRLC-LINKS: REQ-SDS-021
func EdgeRead(f *sample.Frame) uint64 { return core.EdgeRead(f) }

// EdgeWindows is measure.EdgeWindows: channel ch's samples of frame f's
// edge windows.
// TRLC-LINKS: REQ-SDS-021
func EdgeWindows(f *sample.Frame, ch int) []measure.EdgeWindow { return measure.EdgeWindows(f, ch) }

// EresLenForBits is core.EresLenForBits.
// TRLC-LINKS: REQ-SDS-021
func EresLenForBits(bits float64) int { return core.EresLenForBits(bits) }

// ErrClientFramesBody is the core error value itself, so errors.Is matches either name.
var ErrClientFramesBody = core.ErrClientFramesBody

// MeasureRange is measure.Range: the stretch of the record measurements
// read.
// TRLC-LINKS: REQ-SDS-021
func MeasureRange(f *sample.Frame) (int, int) { return measure.Range(f) }

// SerialDecoderList is core.SerialDecoderList.
// TRLC-LINKS: REQ-SDS-021
func SerialDecoderList() []core.SerialDecoder { return core.SerialDecoderList() }

// SoundTakeTimes is core.SoundTakeTimes.
// TRLC-LINKS: REQ-SDS-021
func SoundTakeTimes(pre, post float64) (float64, float64) {
	return core.SoundTakeTimes(pre, post)
}

// ViewDetail is core.ViewDetail.
// TRLC-LINKS: REQ-SDS-021
func ViewDetail(f *sample.Frame, s core.ViewSurface) *sample.Detail { return core.ViewDetail(f, s) }
