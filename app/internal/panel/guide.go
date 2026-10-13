// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import "open-sds/app/internal/panel/wsview"

// The Help guide (the SYS tab, opened by the Help key on its Help row): the
// workspace you are in, the sheets, or the knobs and lamps. Its F-keys are
// these pages, so Help then F1 never runs an action.

type gl = wsview.Span

// TRLC-LINKS: REQ-SDS-212
func gT(t string) gl { return gl{Text: t, Col: wsview.Amber} }

// TRLC-LINKS: REQ-SDS-212
func gN(t string) gl { return gl{Text: t, Col: wsview.Text} }

// TRLC-LINKS: REQ-SDS-212
func gD(t string) gl { return gl{Text: t, Col: wsview.Dim} }

var wsGuide = [wsview.Count][]gl{
	{gT("SCOPE (F1): THE WHOLE TRACE AND THE EVERYDAY READOUTS"),
		gN("Adjust ......... the last value you set in a sheet (tag at the bottom),"),
		gD("                 else the active cursor"),
		gN("Adjust push .... opens the sheet at that row"),
		gN("Position < > ... trigger position (zoomed: zoom position); push = centre"),
		gN("F1 again ....... Y-T / X-Y"),
		gN("Menu On/Off .... sheet with TRIG CH1 CH2 TIME ACQ first")},
	{gT("BUS (F2): PACKETS (HEX DUMP) OR EYE DIAGRAM, TRIGGER ON BYTES"),
		gD("Entering with decode off finds the bus and decodes it at once;"),
		gD("decoding stays on when you leave (BUS sheet: Protocol Off ends it)."),
		gD("Packets: hex and string of each complete packet; the decode strip under"),
		gD("the trace uses the Strip format of the BUS sheet (default Hex)."),
		gN("F2 again ....... Packets / Eye diagram"),
		gN("PACKETS  Adjust: packet  push: pick bytes  Position: next match"),
		gN("         push: to trigger  S/DIV push: zoom onto the packet"),
		gN("EYE      Adjust: threshold  push: clear eye  Position: sample point"),
		gN("         push: both cursors back   S/DIV push (Zoom): fold the zoom window"),
		gD("The eye folds the data line at the recovered bit period, every capture."),
		gD("Zoom gives finer samples; < and > mark figures the sample interval limits."),
		gN("Menu On/Off .... sheet with BUS TRIG TIME first")},
	{gT("SPECTRUM (F3): FFT, SPECTROGRAM OR BODE, PEAKS, TIME TRACE"),
		gD("Entering turns the screen to the analysis view; leaving returns."),
		gN("Adjust ......... marker to the next / previous peak of the table"),
		gN("Adjust push .... delta marker on / off (the readout shows the difference)"),
		gN("Position < > ... move the marker bin by bin"),
		gN("Position push .. marker back to the largest peak"),
		gN("S/DIV .......... time/div sets the span and the resolution"),
		gN("F3 again ....... FFT / Spectrogram / Bode"),
		gN("Menu On/Off .... sheet with VIEW (FFT source, Bode channels) ACQ TIME first")},
	{gT("MEASURE (F4): STATISTICS OF YOUR MEASUREMENTS, CURSORS"),
		gD("Entering with nothing chosen measures Vpp and Freq of each channel."),
		gN("Adjust ......... cursor A"),
		gN("Adjust push .... cursors Off / Time / Volts"),
		gN("Position < > ... cursor B"),
		gN("Position push .. zero the statistics"),
		gN("F4 again ....... statistics / all values of both channels"),
		gN("Menu On/Off .... sheet with MEAS CURS ACQ first")},
	{gT("COMPARE (F5): A STACKED MASTER, REFERENCES, MASK TEST, MATH"),
		gN("F5 again ....... stack a master (again while stacking: cancel):"),
		gD("                 average N captures of the trigger source, store them"),
		gD("                 as REF A, build the mask from them, restore the"),
		gD("                 acquisition and test the live captures raw"),
		gD("                 Only when the source repeats (the caption says so);"),
		gD("                 else set a stable trigger first: Trig Menu holdoff"),
		gD("                 or another edge, or a BUS serial trigger"),
		gN("Adjust ......... choose a row: MASTER, REF A, REF B, MASK, MATH"),
		gN("Adjust push .... the row's action, written at its right end: depth N,"),
		gD("                 store or show/hide, build the mask then its test"),
		gD("                 (counting, stop on the first fail, off), next math"),
		gN("Position < > ... trigger position (zoomed: zoom position); push = centre"),
		gN("Menu On/Off .... sheet with MATH/REF CH1 CH2 first")},
}

var guideCommon = []gl{gT("ANY WORKSPACE"),
	gN("F1-F5 .......... switch workspace (the pictogram rail beside the keys)"),
	gD("                 the active key again: the next view (tool line, right)"),
	gN("Keys (Trig Menu, 1, 2, Acquire ...) open their sheet tab; again closes"),
	gN("V/DIV, Position, Level, S/DIV as printed; pushes Variable, Zero, Zoom")}

var guideSheets = []gl{gT("SHEET OPEN: F1-F5 BELONG TO THE SHEET"),
	gN("S/DIV knob ......... previous / next tab (clockwise = next)"),
	gN("Adjust turn ........ move the row cursor"),
	gN("F1..F5 ............. pick the choice shown beside the key"),
	gN("Adjust push ........ edit the row with Adjust, push again = done"),
	gN("Position < > ....... change the row directly; push = default"),
	gN("Menu On/Off ........ close: back to the workspace"),
	gN("Same key again ..... closes the sheet"),
	gT("THE WORKSPACE'S OWN TABS COME FIRST, THE REST AFTER THE BAR"),
	gN("Trig Menu TRIG   1 CH1   2 CH2   Hori Menu TIME   Acquire ACQ"),
	gN("Display VIEW   Measure MEAS   Cursors CURS   Math/Ref MATH/REF"),
	gN("Utility BUS   Save/Recall FILE   Help SYS (this guide)")}

var guideKnobs = []gl{gT("NO MENU OPEN: KNOBS AND KEYS AS PRINTED"),
	gN("V/DIV ....... volts/div       (clockwise toward mV, as the printed arc)"),
	gD("              push Variable: no fine V/div here (12 calibrated steps)"),
	gN("Position .... trace offset    push Zero: offset 0 V"),
	gN("S/DIV ....... time/div        push Zoom: zoom on/off"),
	gD("              (clockwise toward nS; zoomed: more zoom; BUS: on the packet)"),
	gN("Level ....... trigger level   push Zero: level 0 V"),
	gN("Adjust, Position < > ........ the workspace's tools (page 1)"),
	gN("Single = Normal + one capture.   Auto and Default keep the workspace."),
	gT("LAMPS (one meaning: the key's function is in use)"),
	gN("Measure: readouts on    Acquire: not Normal or super-res    1, 2: shown"),
	gN("Display: not Y-T or persistence    Utility: decoding"),
	gN("Math: a math trace    Ref: a reference shown    Cursors: on    Single: armed"),
	gN("Save/Recall: settings unchanged since the last save or recall"),
	gN("Run/Stop: green running, amber waiting for a trigger, red stopped")}

// guidePage is the Help page on show.
// TRLC-LINKS: REQ-SDS-212
func guidePage(s *sv) []wsview.Span {
	switch s.ui.guide {
	case 1:
		return guideSheets
	case 2:
		return guideKnobs
	}
	return append(append([]wsview.Span(nil), wsGuide[s.ui.ws]...), guideCommon...)
}
