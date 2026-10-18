// Package wsview is the render snapshot of the task-workspace UI
// (ADR-TASK-WORKSPACES): everything the panel controller shows on the LCD,
// as plain data the renderer draws. It holds no logic, so the panel (which
// builds it) and the LCD (which draws it) share one definition.
// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package wsview

// WS identifies a workspace; F1..F5 select them in this order.
type WS int

// The five workspaces (REQ-SDS-211).
const (
	Scope WS = iota
	Bus
	Spectrum
	Measure
	Compare
)

// Count is the number of workspaces.
const Count = 5

// Names are the workspaces' full names, Tags their 4-letter rail tags.
var (
	Names = [Count]string{"SCOPE", "BUS", "SPECTRUM", "MEASURE", "COMPARE"}
	Tags  = [Count]string{"SCOP", "BUS", "SPEC", "MEAS", "COMP"}
	IDs   = [Count]string{"scope", "bus", "spectrum", "measure", "compare"}
)

// Col is a semantic colour; the LCD maps it to its generated palette.
type Col uint8

// Semantic colours.
const (
	Text   Col = iota // body text
	Dim               // secondary text
	Amber             // the trigger/accent colour: active, selected, "what F-key again does"
	Stop              // errors, failures
	Run               // running / OK
	Warn              // warnings
	C1                // channel 1
	C2                // channel 2
	Math              // math trace
	SR                // super-res
	Mask              // mask overlay
	Screen            // the screen colour (dark drawing on an amber fill)
	Cursor            // cursors
)

// Span is a run of text in one colour.
type Span struct {
	Text string
	Col  Col
}

// Seg is one tool-line entry: a knob or push and its job here.
type Seg struct{ Key, Job string }

// Layout tells the renderer how the screen is divided.
type Layout struct {
	MenuW            int  // right column: 30 = the pictogram rail, 116 = a menu's F-key column
	PlotTop          int  // first plot row (0 = full height)
	HideTraces       bool // the plot shows no live traces (sheet, eye diagram)
	HideBottom       bool // no bottom Vpp/f readouts
	HideMeas         bool // no MEAS boxes (the MEASURE pane shows them)
	HideViewCaptions bool // the analysis views' captions are in the pane
	Sheet            bool // a full-screen settings sheet is open
}

// Key is one F-key label: a choice in a menu (sheet, picker, editor).
type Key struct {
	Label, Value string
	Sel, Dim     bool
}

// Keys is the F-key text column of an open menu.
type Keys struct {
	Title string
	Top   int // first row of the column (30 under the sheet's tab strip, 12 for the picker)
	Items [5]Key
}

// Note is the short label beside the rail after a workspace key.
type Note struct {
	WS         WS
	Head, Text string
}

// Pane is a workspace's task pane frame: the header line.
type Pane struct {
	Title   string
	Caption string
	CapCol  Col
	Right   string
	Tool    []Seg  // the tool line
	Again   string // its amber right end: "F2 again: Eye diagram"
}

// View is one complete UI snapshot.
type View struct {
	WS       WS
	Layout   Layout
	Rail     bool  // draw the pictogram rail (workspace mode)
	RailNote *Note // nil when none is showing
	Notice   string
	Keys     *Keys // menu mode: the text column of choices
	Sheet    *Sheet
	Picker   *Picker // the BUS workspace byte picker (pane form)
	Scope    *ScopePane
	Bus      *BusPane
	Eye      *EyePane
	Spec     *SpecPane
	Meas     *MeasPane
	Cmp      *CmpPane
}

// ScopePane is SCOPE's chrome: its name in the top bar and the Adjust tag.
type ScopePane struct {
	AdjustTag string // "Adjust: Holdoff 2.00 ms"; "" when Adjust has no target
	Hot       bool   // just turned: draw it amber
}

// PacketMark is a decoded packet of the frame on screen, for the strip.
type PacketMark struct {
	I0, I1 int // sample span in the frame
	Sel    bool
	Match  bool
	Text   string // the bus layer's note ("" none)
	Err    bool   // the layer found the transaction broken
}

// BusLine is one line of the packet list (a hex dump).
type BusLine struct {
	Num   int      // list number; 0 = a continuation line ("+")
	Sel   bool     // the selected packet
	Hex   []string // byte cells, "ER" for a framing error
	Err   []bool
	Str   string // the same bytes as text ('.' for non-printables)
	More  bool   // the packet is cut after this line (".." and ">")
	Bytes int    // byte count (first line only)
	Match string // the match bytes (amber), or Cut (dim)
	Cut   string
	Addr  string // the packet's address or ID ("24 W"; first line only)
	Fault string // a fault of the packet as a whole ("CRC", "NAK"; red, every line)
	// Note is the bus layer's reading of the packet: with a layer each packet
	// takes one line, its note in place of the hex dump (the selected one's
	// bytes follow on continuation lines); NoteErr draws it as a fault.
	Note    string
	NoteErr bool
}

// BusPane is the BUS workspace's packet list.
type BusPane struct {
	Pane
	Off      bool   // no bus decoded: Msg explains
	Msg      []Span // the explanation lines (big first line)
	Error    string // a decode error (shown in red)
	Lines    []BusLine
	Up, Dn   bool // more lines above / below
	Marks    []PacketMark
	SelNum   int // the list number of the selected packet (strip label)
	Empty    string
	Snapshot bool   // the list is the whole capture kept for a zoom
	Layer    string // the bus layer's name: the lines are its notes ("" none)
}

// EyePane is the BUS eye diagram: figures in the pane, the eye in the plot.
type EyePane struct {
	Pane
	Why       string // no eye yet: why (drawn in the plot)
	Figures   [3][4][2]string
	Cursor    string // "SAMPLE PT ... THRESHOLD ..." (amber)
	Warn      string // sample-limited warning (warn colour)
	Note      string // "< >: bounds ..." (dim)
	Grid      []uint32
	Cols      int // grid columns (2 UI)
	Rows      int // grid rows (the 8 divisions, top first)
	Max       uint32
	Ch        int    // the data line's channel (0 = C1)
	Box       [4]int // the eye opening in grid cells: c0, r0, c1, r1
	HasBox    bool
	SampleCol int // the sample-point cursor's grid column
	ThreshRow int // the threshold cursor's grid row
	TimeLabel string
}

// SpecRow is one peak-table row (FFT) or sweep point (Bode).
type SpecRow struct {
	Num                    int
	Freq, Level, Rel, Harm string
	Sel                    bool
}

// SpecPane is SPECTRUM's peak table, readout and marker.
type SpecPane struct {
	Pane
	Bode      bool
	Head      [4]string
	Rows      []SpecRow
	Empty     string
	Readout   []Span
	Marker    string // the MKR line (amber)
	TimeLabel string
	Ch        int // the FFT source (0 = C1)
	// The marker in the plot: frequency (Hz), level relative to the top peak
	// (dB, <= 0), and a dim delta reference.
	HasMark  bool
	MarkHz   float64
	MarkRel  float64
	MarkText string
	HasRef   bool
	RefHz    float64
	BodeIdx  int // Bode: the marked point (-1 none)
	BodeRef  int // Bode: the delta reference point (-1 none)
}

// StatRow is one MEASURE statistics row.
type StatRow struct {
	Name                         string
	Col                          Col
	Now, Mean, Min, Max, Sdev, N string
}

// MeasPane is MEASURE's statistics (or all values) and the cursor block.
type MeasPane struct {
	Pane
	All      bool
	Empty    bool
	Stats    []StatRow
	More     string
	AllRows  [18][3]string // param, C1, C2 (two columns)
	AllN     int
	AllSplit int // the first column's rows (0: nine)
	Cursor   []Span
	MaskLine Span
}

// CmpRow is one COMPARE row.
type CmpRow struct {
	Name       string
	Info       string
	InfoCol    Col
	Extra      string
	ExtraCol   Col
	Sel        bool
	Action     string
	Bar        bool // a stacking progress bar instead of Extra
	BarN       int
	BarOf      int
	BarNote    string
	BarNoteCol Col
}

// CmpPane is COMPARE's rows.
type CmpPane struct {
	Pane
	Rows [5]CmpRow
}

// PickTok is one byte of the byte picker.
type PickTok struct {
	Text  string // "55" or "ERR"
	Start bool   // a message starts here (drawn with a bar before it)
}

// Picker is the BUS byte picker: the bytes decoded on screen, the first byte
// to trigger on highlighted.
type Picker struct {
	Caption string
	Toks    []PickTok
	Cur     int // the first byte
	Lines   []Span
	Now     string
}

// RowKind is a sheet row's kind.
type RowKind uint8

// Sheet row kinds.
const (
	Enum   RowKind = iota // options written out, F-keys pick
	Number                // a value with a gauge: Down, Up, presets
	Action                // labelled actions run only from their F-key
	Bytes                 // the serial match (text entry)
	Slot                  // a setup memory slot
	Header                // a section header (no other fields)
)

// Opt is one option of an enum row.
type Opt struct {
	Text  string
	Sel   bool
	Quiet bool // selected but "off" or not in use: drawn grey, not amber
}

// Act is one action button of an action row.
type Act struct {
	Text  string
	Dim   bool
	Flash bool
}

// Row is one sheet row as drawn.
type Row struct {
	Kind   RowKind
	Header string // Header rows
	Label  string
	Rel    bool // in use (else drawn dim)
	Focus  bool
	Edit   bool
	// Enum
	Opts []Opt
	Meas [2]string // MEAS rows: the live C1 / C2 values ("" = channel off)
	// Number
	Value string
	Gauge float64 // 0..1
	// Action
	Acts   []Act
	Status string
	// Bytes / Slot
	Armed bool
	Used  bool
}

// Tab is one tab of the sheet's tab strip.
type Tab struct {
	Name   string
	Own    bool // one of the workspace's own tabs (drawn before the bar)
	Active bool
}

// Side is the panel right of the rows: the highlighted row explained.
type Side struct {
	Title string // sub-mode title (match editor, picker); "" for a row
	Label string
	Value string
	Big   bool // the value fits at scale 2
	Lines []Span
	Hints []Span
}

// Sub is a sub-mode box under the Match bytes row: the sheet's byte picker.
type Sub struct {
	Title string
	Toks  []PickTok
	Cur   int
	Lines []Span
}

// Sheet is a full-screen settings sheet.
type Sheet struct {
	WSName    string
	Tabs      []Tab
	Rows      []Row
	Status    []Span // live status under the rows
	Guide     []Span // the Help page (SYS tab)
	Side      Side
	Hint      Span
	Sub       *Sub
	ThumbView int // the mini view: 0 Y-T, 1 X-Y, 2 FFT, 3 Bode, 4 spectrogram
	FFTCh     int
}
