// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

// Front-panel button codes (spec 08 §6), sel-index<<8 | bit.
var (
	btnF1 = bcode(1, 5)
	btnF2 = bcode(1, 13)
	btnF3 = bcode(2, 5)
	btnF4 = bcode(2, 13)
	btnF5 = bcode(3, 5)

	btnTrigMenu     = bcode(0, 10)
	btnAcquire      = bcode(0, 11)
	btnDisplay      = bcode(1, 3)
	btnHorizMenu    = bcode(1, 12)
	btnMenuOnOff    = bcode(0, 13)
	btnMeasure      = bcode(1, 4)
	btnCursors      = bcode(0, 12)
	btnMath         = bcode(2, 12)
	btnDefaultSetup = bcode(2, 10)
	btnRef          = bcode(3, 12)
	btnSaveRecall   = bcode(1, 11)
	btnPrint        = bcode(3, 13)
	btnHelp         = bcode(3, 11)
	btnCh1          = bcode(2, 4)
	btnCh2          = bcode(3, 4)

	btnSet50        = bcode(2, 2)
	btnForce        = bcode(3, 2)
	btnCh1PosPush   = bcode(0, 1)
	btnCh2PosPush   = bcode(3, 1)
	btnHorizPosPush = bcode(3, 9)
	btnTdivPush     = bcode(2, 9)
)

// softkeys are F1..F5, top to bottom.
var softkeys = []int{btnF1, btnF2, btnF3, btnF4, btnF5}

// tabKeys map the dedicated keys to the names keyTab knows them by.
var tabKeys = map[int]string{
	btnTrigMenu: "trigmenu", btnAcquire: "acquire", btnDisplay: "display", btnHorizMenu: "horizmenu",
	btnMeasure: "measure", btnCursors: "cursors", btnMath: "math", btnRef: "ref", btnUtility: "utility",
	btnSaveRecall: "saverecall",
}
