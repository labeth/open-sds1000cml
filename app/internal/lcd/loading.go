// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import "strconv"

// DrawLoading displays an explicit acquisition interruption. Transfer progress
// is supplied by the loader; this never advances a simulated progress bar.
// TRLC-LINKS: REQ-SDS-021
func DrawLoading(sf *MemSurface, stage string, sent, total int) {
	sf.Fill(0x0841)
	title := "Loading acquisition"
	footer := "Controls return when loading finishes"
	if stage == "Firmware loading failed" || stage == "Unable to read firmware" {
		title = "Acquisition unavailable"
		footer = "Firmware loading stopped"
	}
	DrawText(sf, (W-TextWidth(title, 3))/2, 165, title, 0xffff, 3)
	DrawText(sf, (W-TextWidth(stage, 2))/2, 220, stage, 0xbdf7, 2)
	if total > 0 {
		if sent < 0 {
			sent = 0
		}
		if sent > total {
			sent = total
		}
		done := 520 * sent / total
		sf.fillSpan(140, 270, 140+done, 286, 0x07ff)
		sf.fillSpan(140+done, 270, 660, 286, 0x3186)
		label := strconv.Itoa(100*sent/total) + "% transferred"
		DrawText(sf, (W-TextWidth(label, 2))/2, 308, label, 0xffff, 2)
	}
	DrawText(sf, (W-TextWidth(footer, 2))/2, 390, footer, 0x94b2, 2)
}
