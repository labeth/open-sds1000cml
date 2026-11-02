// ENGMODEL-OWNER-UNIT: FU-APP-LCD

package lcd

// pushMags scrolls the waterfall down one row and paints the half spectrum
// mags (peak its largest) as the top row.
// TRLC-LINKS: REQ-SDS-068
func (sg *Spectrogram) pushMags(mags []float64, peak float64) {
	sg.scroll(1)
	sg.paintRow(sgY0, mags, peak)
	if sg.rows < sgRows {
		sg.rows++
	}
}

// Rows reports how many waterfall rows have been painted (debug/status).
// TRLC-LINKS: REQ-SDS-068
func (sg *Spectrogram) Rows() int { return sg.rows }

// drawSpectrogram blits the waterfall image and draws the frequency axis + a dB
// colour key. `sg` is the accumulated image passed via the HUD.
// TRLC-LINKS: REQ-SDS-021, REQ-SDS-068
func drawSpectrogram(sf Surface, p plotArea, sg *Spectrogram, hideCaptions bool) {
	drawSpectrogramMark(sf, p, sg, hideCaptions, nil)
}
