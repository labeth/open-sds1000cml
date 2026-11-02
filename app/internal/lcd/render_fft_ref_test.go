// ENGMODEL-OWNER-UNIT: FU-APP-LCD

package lcd

// fftCore Hann-windows `samples` (DC included; mean-subtracted here), FFTs it,
// draws the magnitude over the visible band [kLo,kHi] magnified across the
// screen, marks peaks, and returns the refined peak frequency. Shared by the
// live FFT (uint8) and the super-res FFT (float fine grid — the float input is
// what lets the crunched sub-LSB bits lower the spectrum noise floor).
// TRLC-LINKS: REQ-SDS-021, REQ-SDS-070
func fftCore(sf Surface, samples []float64, col uint16, effNyq float64, kLo, kHi int) float64 {
	return fftCoreP(sf, fullPlot, samples, col, effNyq, kLo, kHi)
}
