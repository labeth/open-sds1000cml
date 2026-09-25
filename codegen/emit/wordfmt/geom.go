// The constants the models refer to, with the values of the default schema.
// This file is NOT spliced into iface.go (there the same names are generated
// from the schema); emit.TestWordfmtConstantsMatchSchema fails when a value
// here drifts from codegen/ifacedef.
// ENGMODEL-OWNER-UNIT: FU-CODEGEN-EMIT
package wordfmt

const (
	RowCols   = 5
	Rows      = 4096
	RecDepth  = 20480
	DlineRows = 512
	AccBits   = 18
	StackNMax = 1024

	RunChmodeDual uint16 = 0
	RunChmodeCh1  uint16 = 1
	RunChmodeCh2  uint16 = 2

	IlCtrlTsrcAdc    uint16 = 0
	IlCtrlTsrcRamp   uint16 = 1
	IlCtrlTsrcColtag uint16 = 2
	IlCtrlTsrcGlitch uint16 = 3

	IlCtrlReduceModeRaw      uint16 = 0
	IlCtrlReduceModeDecim    uint16 = 1
	IlCtrlReduceModePeak     uint16 = 2
	IlCtrlReduceModeBoxcar8  uint16 = 3
	IlCtrlReduceModeBoxcar16 uint16 = 4
)
