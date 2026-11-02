// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

// sampleToY: higher code = higher on screen; clamp to panel (spec 07 §3.4).
// 25 codes/div render scale (spec 10 §7.1): 8 divisions = 200 codes centred on
// code 128, so the ADC's 256 codes span 10.24 div and the trace clips at the
// graticule edge beyond ±4 div. This is the full-screen plot's mapping.
// TRLC-LINKS: REQ-SDS-021
func sampleToY(v float64) int { return fullPlot.sy(v) }

// drawTrace maps the record window onto the full-screen plot (spec 07 §3.5).
// TRLC-LINKS: REQ-SDS-021
func drawTrace(sf Surface, sig []uint8, win int, xc float64, interp bool, col uint16, posFrac float64, zoom ...int) {
	drawTraceID(sf, fullPlot, traceID{}, sig, win, xc, interp, col, posFrac, zoom...)
}
