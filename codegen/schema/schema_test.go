// ENGMODEL-OWNER-UNIT: FU-CODEGEN-SCHEMA
package schema

import (
	"strings"
	"testing"
)

// TRLC-LINKS: REQ-SDS-155
func minimal() Interface {
	return Interface{
		Name: "t", Version: 3, VersionMagic: 0x0001, FabricID: 0xBEEF, SelMask: 0x7f,
		Legacy:       Legacy{Version: 2, SelMask: 0x7c},
		Undecoded:    []uint8{0x21, 0x57},
		Geometry:     Geometry{RecDepth: 16, AddrW: 4, Margin: 2, RowCols: 4, Rows: 4, DlineRows: 2, AccBits: 10, StackNMax: 4},
		BuildIDLoReg: "BUILDID_LO", BuildIDHiReg: "BUILDID_HI", VersionReg: "VERSION", FabricIDReg: "FABRIC_ID",
		OpcodeReg: "OPCODE", Opcodes: []Opcode{{Name: "GO", Value: 1}},
		DiagIdxReg: "DIAG_IDX", DiagDataReg: "DIAG_DATA", DiagReserved: 0x10,
		Regs: []Register{
			{Name: "BURST_ALIAS", Sel: 0x00, Access: R, Pop: true, Source: SrcAlias, AliasOf: "BURST"},
			{Name: "BUILDID_LO", Sel: 0x10, Access: R, Source: SrcBuildIDLo},
			{Name: "BUILDID_HI", Sel: 0x14, Access: R, Source: SrcBuildIDHi},
			{Name: "VERSION", Sel: 0x18, Access: R, Source: SrcConst, Const: 0x0001},
			{Name: "FABRIC_ID", Sel: 0x1c, Access: R, Source: SrcConst, Const: 0xBEEF},
			{Name: "OPCODE", Sel: 0x20, Access: W, Strobe: true},
			{Name: "DIAG_IDX", Sel: 0x08, Access: RW},
			{Name: "DIAG_DATA", Sel: 0x0c, Access: RW},
			{Name: "BURST", Sel: 0x40, Access: R, Pop: true, Fields: []Field{{Name: "HI", Hi: 15, Lo: 8}, {Name: "LO", Hi: 7, Lo: 0}}},
			{Name: "RUN", Sel: 0x24, Access: RW, Fields: []Field{{Name: "MODE", Hi: 1, Lo: 0, Enum: []EnumValue{{Name: "AUTO", Value: 0}, {Name: "NORM", Value: 1}}}, {Name: "RUN", Hi: 2, Lo: 2}}},
			{Name: "NEW", Sel: 0x25, Since: 3, Access: RW, Fields: []Field{{Name: "X", Hi: 3, Lo: 0}}},
			{Name: "LATER", Sel: 0x26, Since: 3, Access: RW, Stub: true},
		},
		Diag: []DiagEntry{
			{Name: "A", Idx: 0, Count: 1, Access: RW, Fields: []Field{{Name: "X", Hi: 3, Lo: 0}}},
			{Name: "MAP", Idx: 4, Count: 8, Access: RW},
		},
	}
}

// TRLC-LINKS: REQ-SDS-155
func TestMinimalValidates(t *testing.T) {
	if errs := minimal().Validate(); len(errs) > 0 {
		t.Fatal(errs)
	}
}

// TRLC-LINKS: REQ-SDS-155
func TestFieldMask(t *testing.T) {
	cases := []struct {
		hi, lo uint
		want   uint16
	}{{0, 0, 0x0001}, {15, 15, 0x8000}, {15, 0, 0xffff}, {5, 4, 0x0030}, {14, 0, 0x7fff}}
	for _, c := range cases {
		if got := (Field{Hi: c.hi, Lo: c.lo}).Mask(); got != c.want {
			t.Errorf("[%d:%d] mask 0x%04x, want 0x%04x", c.hi, c.lo, got, c.want)
		}
	}
}

// Every rule the emitters rely on must be caught by Validate with a message
// naming the offender.
// TRLC-LINKS: REQ-SDS-155
func TestValidateRejects(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Interface)
		want string
	}{
		{"dup selector", func(i *Interface) { i.Regs[9].Sel = 0x40 }, "selector 0x40 already used"},
		{"sel bit 7", func(i *Interface) { i.Regs[9].Sel = 0xa4 }, "outside SelMask"},
		{"selmask bit 7", func(i *Interface) { i.SelMask = 0xff }, "decodes bit 7"},
		{"legacy reg moved to a new slot", func(i *Interface) { i.Regs[9].Sel = 0x27 }, "must keep a selector inside the legacy mask"},
		{"new reg in a legacy slot", func(i *Interface) { i.Regs[10].Sel = 0x28 }, "must take a slot outside the legacy mask"},
		{"since newer than schema", func(i *Interface) { i.Regs[10].Since = 4 }, "newer than the schema"},
		{"legacy newer than schema", func(i *Interface) { i.Legacy.Version = 4 }, "newer than the schema"},
		{"legacy mask not a subset", func(i *Interface) { i.SelMask = 0x7b }, "not a subset"},
		{"undecoded used", func(i *Interface) { i.Regs[10].Sel = 0x21 }, "must stay undecoded"},
		{"undecoded outside mask", func(i *Interface) { i.Undecoded = append(i.Undecoded, 0x80) }, "outside SelMask"},
		{"undecoded twice", func(i *Interface) { i.Undecoded = append(i.Undecoded, 0x21) }, "listed twice"},
		{"stub const", func(i *Interface) { i.Regs[3].Stub = true }, "only a plain wire-sourced register can be a stub"},
		{"stub strobe", func(i *Interface) { i.Regs[5].Stub = true }, "only a plain wire-sourced register can be a stub"},
		{"stub pop", func(i *Interface) { i.Regs[8].Stub = true }, "only a plain wire-sourced register can be a stub"},
		{"alias of stub", func(i *Interface) { i.Regs[8].Pop = false; i.Regs[0].Pop = false; i.Regs[8].Stub = true }, "only a plain wire-sourced register can be a stub|alias target BURST is a stub"},
		{"diag idx stub", func(i *Interface) { i.Regs[6].Stub = true }, "DIAG index register"},
		{"enum overflow", func(i *Interface) { i.Regs[9].Fields[0].Enum[1].Value = 4 }, "does not fit 2 bits"},
		{"enum dup value", func(i *Interface) { i.Regs[9].Fields[0].Enum[1].Value = 0 }, "duplicates"},
		{"enum dup name", func(i *Interface) { i.Regs[9].Fields[0].Enum[1].Name = "AUTO" }, "duplicates"},
		{"enum name", func(i *Interface) { i.Regs[9].Fields[0].Enum[1].Name = "norm" }, "not UPPER_SNAKE"},
		{"geometry rows", func(i *Interface) { i.Geometry.Rows = 3 }, "does not make depth"},
		{"geometry dline", func(i *Interface) { i.Geometry.DlineRows = 5 }, "outside 1..4"},
		{"geometry accbits", func(i *Interface) { i.Geometry.StackNMax = 5 }, "cannot hold"},
		{"dup name", func(i *Interface) { i.Regs[9].Name = "BURST" }, "duplicate name"},
		{"lowercase name", func(i *Interface) { i.Regs[9].Name = "run" }, "not UPPER_SNAKE"},
		{"no access", func(i *Interface) { i.Regs[9].Access = 0 }, "access not declared"},
		{"pop on W", func(i *Interface) { i.Regs[5].Pop = true }, "pop-on-read but not readable"},
		{"strobe on RW", func(i *Interface) { i.Regs[5].Access = RW }, "strobe must be write-only"},
		{"field overlap", func(i *Interface) { i.Regs[9].Fields[1].Lo = 1 }, "overlaps"},
		{"field range", func(i *Interface) { i.Regs[9].Fields[1].Hi = 16 }, "bad range"},
		{"field hi<lo", func(i *Interface) { i.Regs[9].Fields[0].Lo = 5 }, "bad range"},
		{"dup field", func(i *Interface) { i.Regs[9].Fields[1].Name = "MODE" }, "duplicate field"},
		{"alias missing", func(i *Interface) { i.Regs[0].AliasOf = "NOPE" }, "alias target NOPE does not exist"},
		{"alias pop mismatch", func(i *Interface) { i.Regs[0].Pop = false }, "pop-on-read differs"},
		{"alias with fields", func(i *Interface) { i.Regs[0].Fields = []Field{{Name: "X", Hi: 0, Lo: 0}} }, "no fields of its own"},
		{"alias of alias", func(i *Interface) {
			i.Regs = append(i.Regs, Register{Name: "B2", Sel: 0x44, Access: R, Pop: true, Source: SrcAlias, AliasOf: "BURST_ALIAS"})
		}, "alias of an alias"},
		{"const writable", func(i *Interface) { i.Regs[3].Access = RW }, "must be read-only"},
		{"wrong magic", func(i *Interface) { i.Regs[3].Const = 0x0002 }, "must read 0x0001"},
		{"const without source", func(i *Interface) { i.Regs[9].Const = 5 }, "Const set but Source is wire"},
		{"buildid missing", func(i *Interface) { i.BuildIDHiReg = "X" }, `build-ID high register "X" does not exist`},
		{"stray buildid", func(i *Interface) { i.Regs[9].Access = R; i.Regs[9].Source = SrcBuildIDLo }, "stray build-ID source"},
		{"opcode reg not strobe", func(i *Interface) { i.Regs[5].Strobe = false }, "must exist and be a strobe"},
		{"no opcodes", func(i *Interface) { i.Opcodes = nil }, "no opcodes"},
		{"dup opcode", func(i *Interface) { i.Opcodes = append(i.Opcodes, Opcode{Name: "HALT", Value: 1}) }, "duplicates"},
		{"diag idx not RW", func(i *Interface) { i.Regs[6].Access = R }, "DIAG index register"},
		{"diag overlap", func(i *Interface) { i.Diag[1].Idx = 0 }, "overlaps"},
		{"diag reserved", func(i *Interface) { i.Diag[1].Count = 20 }, "reserved range"},
		{"diag count 0", func(i *Interface) { i.Diag[1].Count = 0 }, "bad count"},
		{"diag dup name", func(i *Interface) { i.Diag[1].Name = "A" }, "duplicate name"},
		{"diag field", func(i *Interface) { i.Diag[0].Fields[0].Hi = 20 }, "bad range"},
		{"geometry", func(i *Interface) { i.Geometry.AddrW = 3 }, "inconsistent"},
	}
	for _, c := range cases {
		i := minimal()
		c.mut(&i)
		errs := i.Validate()
		if len(errs) == 0 {
			t.Errorf("%s: accepted", c.name)
			continue
		}
		found := false
		for _, w := range strings.Split(c.want, "|") {
			if strings.Contains(strings.Join(errs, "\n"), w) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: got %q, want a message containing %q", c.name, errs, c.want)
		}
	}
}

// TRLC-LINKS: REQ-SDS-155
func TestBuildIDStableAndSensitive(t *testing.T) {
	a, b := minimal(), minimal()
	if a.BuildID() != b.BuildID() {
		t.Fatal("build-ID is not deterministic")
	}
	base := a.BuildID()
	muts := map[string]func(*Interface){
		"selector":     func(i *Interface) { i.Regs[9].Sel = 0x28 },
		"access":       func(i *Interface) { i.Regs[9].Access = R },
		"field":        func(i *Interface) { i.Regs[9].Fields[0].Hi = 0 },
		"field name":   func(i *Interface) { i.Regs[9].Fields[0].Name = "MODEX" },
		"pop":          func(i *Interface) { i.Regs[8].Pop = false; i.Regs[0].Pop = false },
		"opcode":       func(i *Interface) { i.Opcodes[0].Value = 2 },
		"diag":         func(i *Interface) { i.Diag[1].Count = 4 },
		"diag field":   func(i *Interface) { i.Diag[0].Fields[0].Lo = 1 },
		"geometry":     func(i *Interface) { i.Geometry.Margin = 1 },
		"version":      func(i *Interface) { i.Version = 4 },
		"since":        func(i *Interface) { i.Regs[9].Since = 2 },
		"stub":         func(i *Interface) { i.Regs[10].Stub = true },
		"diag stub":    func(i *Interface) { i.Diag[1].Stub = true },
		"enum":         func(i *Interface) { i.Regs[9].Fields[0].Enum[1].Value = 2 },
		"undecoded":    func(i *Interface) { i.Undecoded = []uint8{0x21} },
		"legacy":       func(i *Interface) { i.Legacy = Legacy{} },
		"row geometry": func(i *Interface) { i.Geometry.DlineRows = 4 },
		"fabric":       func(i *Interface) { i.FabricID = 0x1234; i.Regs[4].Const = 0x1234 },
		"name":         func(i *Interface) { i.Name = "u" },
		"diag reserve": func(i *Interface) { i.DiagReserved = 0x20 },
		"source":       func(i *Interface) { i.SourceDigest = 1 },
	}
	seen := map[uint32]string{base: "base"}
	for n, m := range muts {
		i := minimal()
		m(&i)
		if errs := i.Validate(); len(errs) > 0 {
			t.Fatalf("%s: mutation invalid: %v", n, errs)
		}
		id := i.BuildID()
		if prev, dup := seen[id]; dup {
			t.Errorf("%s: build-ID 0x%08x collides with %s", n, id, prev)
		}
		seen[id] = n
	}
	// Desc is documentation: it must not move the build-ID.
	i := minimal()
	i.Regs[9].Desc = "changed"
	i.Regs[9].Fields[0].Enum[0].Desc = "changed"
	i.Diag[0].Desc = "changed"
	i.Opcodes[0].Desc = "changed"
	i.SelDesc = "changed"
	if i.BuildID() != base {
		t.Error("Desc edit moved the build-ID")
	}
}

// A schema without a legacy generation has no slot-shape rule and a v1
// interface with the v2 mask still validates (the v2 shape of 05-WORKPLAN §2).
// TRLC-LINKS: REQ-SDS-155
func TestNoLegacyRule(t *testing.T) {
	i := minimal()
	i.Legacy = Legacy{}
	i.Regs[9].Sel = 0x27 // fine without the rule
	if errs := i.Validate(); len(errs) > 0 {
		t.Fatal(errs)
	}
	i = minimal()
	i.Version, i.SelMask, i.Legacy, i.Undecoded = 2, 0x7c, Legacy{}, nil
	i.Regs = i.Regs[:10]
	if errs := i.Validate(); len(errs) > 0 {
		t.Fatal(errs)
	}
	if i.SelCount() != 32 || minimal().SelCount() != 128 {
		t.Error("SelCount")
	}
}

// TRLC-LINKS: REQ-SDS-155
func TestLookups(t *testing.T) {
	i := minimal()
	if d, ok := i.DiagEntry("MAP"); !ok || d.Last() != 11 {
		t.Errorf("DiagEntry: %+v %t", d, ok)
	}
	if _, ok := i.DiagEntry("NOPE"); ok {
		t.Error("DiagEntry found a missing entry")
	}
	if (Field{Hi: 3, Lo: 2}).Width() != 2 {
		t.Error("Width")
	}
}
