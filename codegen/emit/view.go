package emit

import (
	"fmt"
	"strings"

	"open-sds/codegen/schema"
)

// The view types are a flattened, precomputed projection of a schema.Interface:
// masks, LSBs, hex strings, camel-case identifiers, per-record bit offsets, Go
// types, and the read-mux read expressions are all computed HERE so the
// text/template sources stay logic-light — ranges and field lookups only, no
// arithmetic. Everything iterates slices (never maps) so rendering is
// deterministic and the drift gate is byte-stable.

// View is the whole interface, flattened for the templates.
type View struct {
	Banner     string
	Name       string
	Version    string
	BuildID    uint32
	BuildIDHex string // 8 lowercase hex digits
	LoHex      string // low 16 bits, 4 hex digits
	HiHex      string // high 16 bits, 4 hex digits
	CS1        uint8
	CS3        uint8
	Sems       []nameVal // Sem constant names + values
	Stream     streamView
	Capture    captureView
	Blocks     []blockView
	Regs       []regView // every register, flat, in declaration order
	WriteRegs  []regView // writable subset (for the write-strobe decode)
	ReadRegs   []regView // readable subset (for the read-data mux)
	AutoInc    []regView // auto-inc ports (plane-qualified)
	Channels   []channelView
	ChanPorts  []chanPortsView
	Descriptor descriptorView
	Verify     verifyView
	Opcodes    []opcodeView
}

type opcodeView struct {
	Name   string // e.g. OP_GO — emitted verbatim as both a Verilog macro and a Go const
	ValHex string // 4 hex digits
	Reg    string
	Desc   string
}

type nameVal struct {
	Name string
	Val  uint16
}

type streamView struct {
	LaneBits uint
	Stages   uint
	Desc     string
}

type captureView struct {
	Depth      uint
	AddrBits   uint
	Margin     uint
	PretrigMax uint
	Desc       string
}

type blockView struct {
	Name    string
	Plane   string // "CS1"/"CS3"
	Base    uint16
	Span    uint16
	BaseHex string
	LastHex string // Base+Span-1
	Desc    string
	Regs    []regView
}

type regView struct {
	Name        string
	Camel       string // e.g. StatusA
	Sel         uint16
	SelHex      string // 2 hex digits
	SelMacro    string // `SEL_<NAME>
	Plane       string // "CS1"/"CS3"
	PlaneConst  string // "CS1"/"CS3" (Go const)
	PlaneMacro  string // `PLANE_CS1 / `PLANE_CS3
	Access      string // "R"/"W"/"RW"
	AccessConst string // "AccR"/"AccW"/"AccRW"
	CanRead     bool
	CanWrite    bool
	Sem         uint16
	SemList     string // "auto-inc-port|read-after-halt" (Verilog-comment form)
	SemDoc      string // "auto-inc-port, read-after-halt" (Markdown-table-safe form)
	Fields      []fieldView
	HasExpect   bool
	ExpectHex   string
	Desc        string
	Note        string // doc note (Desc, prefixed with the Expect value if any)
	FieldSum    string // doc field summary, e.g. "MODE[1:0] RUN[2:2]"
	// read-data mux (regmux.vh): how this register's read value is produced.
	//   "buildid_lo"/"buildid_hi" — driven from the schema build-ID macro
	//   "const"                   — driven from a fixed Expect literal
	//   "behavior"                — driven by a hand-RTL rdata_<REG> wire
	ReadKind string
	ReadExpr string
}

type fieldView struct {
	RegName string
	Name    string
	Camel   string
	Hi      uint
	Lo      uint
	Lsb     uint
	Width   uint
	MaskHex string
	GoType  string
	IsBool  bool
	Desc    string
}

type channelView struct {
	Name       string
	Type       string // Go record type, e.g. EnvelopeRecord
	RecordBits uint
	Words      uint
	Fields     []recFieldView
	Desc       string
}

type recFieldView struct {
	Name     string
	Camel    string
	Offset   uint
	Width    uint
	Hi       uint // within the record word-stream
	Lo       uint
	GoType   string
	IsBool   bool
	Overflow bool
	Desc     string
	Note     string
}

type chanPortsView struct {
	Name     string
	DataReg  string
	CountReg string
	ResetReg string
	HasReset bool
	Words    uint
}

type descriptorView struct {
	Name   string
	Type   string // Go struct type, e.g. BurstDrainDescriptor
	Fields []recFieldView
	Words  uint
	Bits   uint
}

type verifyView struct {
	HasExpect    bool
	Expect       uint16
	VersionReg   string
	VersionPlane string
	LoReg        string
	LoPlane      string
	HiReg        string
	HiPlane      string
}

// newView flattens a validated interface into the template view.
func newView(i schema.Interface) View {
	id := i.BuildID()
	v := View{
		Banner:     genBanner,
		Name:       i.Name,
		Version:    i.Version,
		BuildID:    id,
		BuildIDHex: fmt.Sprintf("%08x", id),
		LoHex:      fmt.Sprintf("%04x", id&0xFFFF),
		HiHex:      fmt.Sprintf("%04x", id>>16),
		CS1:        uint8(schema.CS1),
		CS3:        uint8(schema.CS3),
		Sems: []nameVal{
			{"SemNormal", uint16(schema.SemNormal)},
			{"SemStrobe", uint16(schema.SemStrobe)},
			{"SemAutoIncPort", uint16(schema.SemAutoIncPort)},
			{"SemReadAfterHalt", uint16(schema.SemReadAfterHalt)},
			{"SemLevelStatus", uint16(schema.SemLevelStatus)},
			{"SemWaitGuarded", uint16(schema.SemWaitGuarded)},
		},
		Stream: streamView{i.Stream.SampleLaneBits, i.Stream.TransformStages, i.Stream.Desc},
		Capture: captureView{
			Depth: i.Capture.RecordDepth, AddrBits: i.Capture.AddrBits,
			Margin: i.Capture.Margin, PretrigMax: i.Capture.PretrigMax(), Desc: i.Capture.Desc,
		},
	}

	for _, b := range i.Blocks {
		bv := blockView{
			Name: b.Name, Plane: b.Plane.String(), Base: b.Base, Span: b.Span,
			BaseHex: fmt.Sprintf("%02x", b.Base),
			LastHex: fmt.Sprintf("%02x", b.Base+b.Span-1),
			Desc:    b.Desc,
		}
		for _, r := range b.Regs {
			rv := regViewOf(i, r)
			bv.Regs = append(bv.Regs, rv)
			v.Regs = append(v.Regs, rv)
			if rv.CanWrite {
				v.WriteRegs = append(v.WriteRegs, rv)
			}
			if rv.CanRead {
				v.ReadRegs = append(v.ReadRegs, rv)
			}
			if r.Sem&schema.SemAutoIncPort != 0 {
				v.AutoInc = append(v.AutoInc, rv)
			}
		}
		v.Blocks = append(v.Blocks, bv)
	}

	for _, c := range i.Channels {
		cv := channelView{
			Name: c.Name, Type: camel(c.Name) + "Record",
			RecordBits: c.RecordBits, Words: words(c.RecordBits), Desc: c.Desc,
		}
		var off uint
		for _, f := range c.Fields {
			cv.Fields = append(cv.Fields, recFieldView{
				Name: f.Name, Camel: camel(f.Name), Offset: off, Width: f.Bits,
				Hi: off + f.Bits - 1, Lo: off, GoType: goType(f.Bits, f.Overflow),
				IsBool: f.Overflow || f.Bits == 1, Overflow: f.Overflow, Desc: f.Desc,
				Note: recNote(f),
			})
			off += f.Bits
		}
		v.Channels = append(v.Channels, cv)

		if len(c.Ports) > 0 {
			cp := chanPortsView{Name: c.Name, Words: words(c.RecordBits)}
			for _, p := range c.Ports {
				switch p.Role {
				case schema.PortData:
					cp.DataReg = p.Reg
				case schema.PortCount:
					cp.CountReg = p.Reg
				case schema.PortReset:
					cp.ResetReg = p.Reg
					cp.HasReset = true
				}
			}
			v.ChanPorts = append(v.ChanPorts, cp)
		}
	}

	// Descriptor: treat like a record for the codec.
	dv := descriptorView{Name: i.Descriptor.Name, Type: camel(i.Descriptor.Name) + "Descriptor"}
	var doff uint
	for _, f := range i.Descriptor.Fields {
		dv.Fields = append(dv.Fields, recFieldView{
			Name: f.Name, Camel: camel(f.Name), Offset: doff, Width: f.Bits,
			Hi: doff + f.Bits - 1, Lo: doff, GoType: goType(f.Bits, false),
			IsBool: f.Bits == 1, Desc: f.Desc, Note: f.Desc,
		})
		doff += f.Bits
	}
	dv.Bits = doff
	dv.Words = words(doff)
	v.Descriptor = dv

	for _, o := range i.Opcodes {
		v.Opcodes = append(v.Opcodes, opcodeView{
			Name: o.Name, ValHex: fmt.Sprintf("%04x", o.Value), Reg: o.Reg, Desc: o.Desc,
		})
	}

	// Build-ID / version handshake registers.
	v.Verify = verifyView{LoReg: i.BuildIDLo, HiReg: i.BuildIDHi}
	for _, r := range i.AllRegs() {
		if r.Name == i.BuildIDLo {
			v.Verify.LoPlane = r.Plane.String()
		}
		if r.Name == i.BuildIDHi {
			v.Verify.HiPlane = r.Plane.String()
		}
		if r.Expect != nil && !v.Verify.HasExpect {
			v.Verify.HasExpect = true
			v.Verify.Expect = *r.Expect
			v.Verify.VersionReg = r.Name
			v.Verify.VersionPlane = r.Plane.String()
		}
	}

	return v
}

func regViewOf(i schema.Interface, r schema.Register) regView {
	rv := regView{
		Name: r.Name, Camel: camel(r.Name), Sel: r.Sel,
		SelHex: fmt.Sprintf("%02x", r.Sel), SelMacro: "`SEL_" + r.Name,
		Plane: r.Plane.String(), PlaneConst: r.Plane.String(), PlaneMacro: planeMacro(r.Plane),
		Access: r.Access.String(), AccessConst: accessConst(r.Access),
		CanRead: r.Access.CanRead(), CanWrite: r.Access.CanWrite(),
		Sem: uint16(r.Sem), SemList: r.Sem.String(), SemDoc: strings.Join(r.Sem.List(), ", "), Desc: r.Desc,
	}
	var summ []string
	for _, f := range r.Fields {
		rv.Fields = append(rv.Fields, fieldView{
			RegName: r.Name, Name: f.Name, Camel: camel(f.Name),
			Hi: f.Hi, Lo: f.Lo, Lsb: f.Lo, Width: f.Hi - f.Lo + 1,
			MaskHex: fmt.Sprintf("%04x", f.Mask()),
			GoType:  goType(f.Hi-f.Lo+1, false), IsBool: f.Hi == f.Lo,
			Desc: f.Desc,
		})
		summ = append(summ, fmt.Sprintf("%s[%d:%d]", f.Name, f.Hi, f.Lo))
	}
	rv.FieldSum = strings.Join(summ, " ")
	rv.Note = r.Desc
	if r.Expect != nil {
		rv.HasExpect = true
		rv.ExpectHex = fmt.Sprintf("%04x", *r.Expect)
		rv.Note = fmt.Sprintf("reads `0x%04x`; %s", *r.Expect, r.Desc)
	}
	// read-data mux source (regmux.vh): identity/build-ID and Expect registers are
	// driven from the schema so the fabric cannot report an identity the schema
	// did not define; every other readable register is driven by named hand RTL.
	switch {
	case r.Name == i.BuildIDLo:
		rv.ReadKind, rv.ReadExpr = "buildid_lo", "`IFACE_BUILD_ID_LO"
	case r.Name == i.BuildIDHi:
		rv.ReadKind, rv.ReadExpr = "buildid_hi", "`IFACE_BUILD_ID_HI"
	case r.Expect != nil && r.Access == schema.R:
		rv.ReadKind, rv.ReadExpr = "const", fmt.Sprintf("16'h%04x", *r.Expect)
	case r.Access.CanRead():
		rv.ReadKind, rv.ReadExpr = "behavior", "rdata_"+r.Name
	}
	return rv
}

// words is the number of 16-bit words needed to hold n bits.
func words(bits uint) uint { return (bits + 15) / 16 }

// goType picks the smallest unsigned Go type that holds a bit-field. Overflow
// and single-bit fields become bool (the natural API for a status/lost-count).
func goType(width uint, overflow bool) string {
	if overflow || width == 1 {
		return "bool"
	}
	switch {
	case width <= 8:
		return "uint8"
	case width <= 16:
		return "uint16"
	case width <= 32:
		return "uint32"
	default:
		return "uint64"
	}
}

func recNote(f schema.RecField) string {
	if f.Overflow {
		if f.Desc == "" {
			return "**overflow/lost-count**"
		}
		return "**overflow/lost-count** — " + f.Desc
	}
	return f.Desc
}

func planeMacro(p schema.Plane) string {
	if p == schema.CS3 {
		return "`PLANE_CS3"
	}
	return "`PLANE_CS1"
}

func accessConst(a schema.Access) string {
	switch a {
	case schema.W:
		return "AccW"
	case schema.RW:
		return "AccRW"
	default:
		return "AccR"
	}
}

// camel turns an UPPER_SNAKE (or lower_snake) register/field name into an
// exported Go identifier: STATUS_A -> StatusA, BURST_REMAIN -> BurstRemain,
// trig_event -> TrigEvent, start_idx -> StartIdx.
func camel(name string) string {
	var b strings.Builder
	for _, part := range strings.Split(name, "_") {
		if part == "" {
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]))
		if len(part) > 1 {
			b.WriteString(strings.ToLower(part[1:]))
		}
	}
	return b.String()
}
