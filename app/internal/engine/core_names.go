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
	BodePoint        = core.BodePoint
	ClientChunk      = core.ClientChunk
	ClientStreamInfo = core.ClientStreamInfo
	ClientWords      = core.ClientWords
	Clock            = core.Clock
	Mask             = core.Mask
	MaskFail         = core.MaskFail
	PatternElem      = core.PatternElem
	SerialDecoder    = core.SerialDecoder
	SerialParams     = core.SerialParams
	SoundBlock       = core.SoundBlock
	SoundInfo        = core.SoundInfo
	SoundMark        = core.SoundMark
	SoundMarkConfig  = core.SoundMarkConfig
	SoundScale       = core.SoundScale
	SoundTake        = core.SoundTake
	TrigQual         = core.TrigQual
	TrigType         = core.TrigType
	ViewSurface      = core.ViewSurface
	Zone             = core.Zone
)

// AppendClientChunkHeads is core.AppendClientChunkHeads.
func AppendClientChunkHeads(dst []byte, chunks []core.ClientChunk) []byte {
	return core.AppendClientChunkHeads(dst, chunks)
}

// BuildMaskFromEnvelope is core.BuildMaskFromEnvelope.
func BuildMaskFromEnvelope(lo []uint8, hi []uint8, winCols int, tolCols int, tolCodes int, ch int) *core.Mask {
	return core.BuildMaskFromEnvelope(lo, hi, winCols, tolCols, tolCodes, ch)
}

// EdgeRead is core.EdgeRead.
func EdgeRead(f *sample.Frame) uint64 { return core.EdgeRead(f) }

// EdgeWindows is core.EdgeWindows.
func EdgeWindows(f *sample.Frame, ch int) []measure.EdgeWindow { return core.EdgeWindows(f, ch) }

// EresLenForBits is core.EresLenForBits.
func EresLenForBits(bits float64) int { return core.EresLenForBits(bits) }

// ErrClientFramesBody is the core error value itself, so errors.Is matches either name.
var ErrClientFramesBody = core.ErrClientFramesBody

// MeasureRange is core.MeasureRange.
func MeasureRange(f *sample.Frame) (int, int) { return core.MeasureRange(f) }

// SerialDecoderList is core.SerialDecoderList.
func SerialDecoderList() []core.SerialDecoder { return core.SerialDecoderList() }

// SoundTakeTimes is core.SoundTakeTimes.
func SoundTakeTimes(pre float64, post float64) (float64, float64) {
	return core.SoundTakeTimes(pre, post)
}

// ViewDetail is core.ViewDetail.
func ViewDetail(f *sample.Frame, s core.ViewSurface) *sample.Detail { return core.ViewDetail(f, s) }
