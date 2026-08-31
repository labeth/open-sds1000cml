package schema_test

import (
	"strings"
	"testing"

	"open-sds/codegen/ifacedef"
	"open-sds/codegen/schema"
)

// The owned interface must validate clean.
func TestStandardValid(t *testing.T) {
	if errs := ifacedef.Standard().Validate(); len(errs) > 0 {
		for _, e := range errs {
			t.Errorf("Standard invalid: %v", e)
		}
	}
}

// The build-ID must be deterministic and non-zero.
func TestBuildIDDeterministic(t *testing.T) {
	a, b := ifacedef.Standard().BuildID(), ifacedef.Standard().BuildID()
	if a != b {
		t.Fatalf("BuildID not deterministic: %#x != %#x", a, b)
	}
	if a == 0 {
		t.Fatal("BuildID is zero")
	}
}

// base returns a MINIMAL valid interface, freshly constructed each call (no
// shared slice backing), so negative tests can break exactly one thing.
func base() schema.Interface {
	return schema.Interface{
		Name: "t", Version: "1",
		BuildIDLo: "BID_LO", BuildIDHi: "BID_HI",
		Stream:  schema.Stream{SampleLaneBits: 18, TransformStages: 1},
		Capture: schema.Capture{RecordDepth: 100, AddrBits: 7, Margin: 2, PreTrigProgrammable: true, TrigMark: true},
		// CS1 selectors are multiples of 4 below 0x80: the fabric forces selector
		// bits 0/1/7 to zero (A1 is a clock, A2 floats, A8 is unwired), so any
		// other value is undecodable. The fixture obeys the same rule the real
		// map does, or every negative test below would drown in that one error.
		Blocks: []schema.Block{{
			Name: "meta", Plane: schema.CS1, Base: 0x10, Span: 0x20,
			Regs: []schema.Register{
				{Name: "BID_LO", Sel: 0x10, Plane: schema.CS1, Access: schema.R, Sem: schema.SemNormal},
				{Name: "BID_HI", Sel: 0x14, Plane: schema.CS1, Access: schema.R, Sem: schema.SemNormal},
				{Name: "DATA", Sel: 0x18, Plane: schema.CS1, Access: schema.R, Sem: schema.SemAutoIncPort},
				{Name: "CNT", Sel: 0x1C, Plane: schema.CS1, Access: schema.R, Sem: schema.SemLevelStatus},
			},
		}},
		Channels: []schema.Channel{{
			Name: "c", RecordBits: 8,
			Fields: []schema.RecField{
				{Name: "v", Bits: 7},
				{Name: "overflow", Bits: 1, Overflow: true},
			},
			Ports: []schema.ChannelPort{
				{Role: schema.PortData, Reg: "DATA"},
				{Role: schema.PortCount, Reg: "CNT"},
			},
		}},
		Descriptor: schema.Descriptor{Name: "d", Fields: []schema.RecField{{Name: "x", Bits: 8}}},
	}
}

func TestBaseValid(t *testing.T) {
	if errs := base().Validate(); len(errs) > 0 {
		t.Fatalf("base() should be valid, got %v", errs)
	}
}

func mustErr(t *testing.T, i schema.Interface, want string) {
	t.Helper()
	for _, e := range i.Validate() {
		if strings.Contains(e.Error(), want) {
			return
		}
	}
	t.Fatalf("expected an error containing %q; got %v", want, i.Validate())
}

func TestC1StreamWidth(t *testing.T) {
	i := base()
	i.Stream.SampleLaneBits = 16
	mustErr(t, i, "C1")
}

func TestC1TransformStage(t *testing.T) {
	i := base()
	i.Stream.TransformStages = 0
	mustErr(t, i, "TransformStages")
}

func TestC2PreTrigger(t *testing.T) {
	i := base()
	i.Capture.PreTrigProgrammable = false
	mustErr(t, i, "C2")
}

func TestC2TrigMark(t *testing.T) {
	i := base()
	i.Capture.TrigMark = false
	mustErr(t, i, "TrigMark")
}

func TestC2RecordDepthZero(t *testing.T) {
	i := base()
	i.Capture.RecordDepth = 0
	mustErr(t, i, "RecordDepth")
}

func TestCaptureAddrBitsTooSmall(t *testing.T) {
	i := base()
	i.Capture.AddrBits = 6 // 2^6 = 64 < 100
	mustErr(t, i, "cannot address")
}

func TestCaptureMarginZero(t *testing.T) {
	i := base()
	i.Capture.Margin = 0
	mustErr(t, i, "Margin")
}

func TestC3ChannelOverflow(t *testing.T) {
	i := base()
	i.Channels[0].Fields = []schema.RecField{{Name: "v", Bits: 8}}
	mustErr(t, i, "C3")
}

func TestChannelWidthMismatch(t *testing.T) {
	i := base()
	i.Channels[0].RecordBits = 99
	mustErr(t, i, "fields sum")
}

func TestChannelPortRegMissing(t *testing.T) {
	i := base()
	i.Channels[0].Ports = append(i.Channels[0].Ports, schema.ChannelPort{Role: schema.PortData, Reg: "NOPE"})
	mustErr(t, i, "not found")
}

func TestChannelPortRoleMismatch(t *testing.T) {
	i := base()
	// DATA port must be an auto-inc register; point it at a plain register.
	i.Channels[0].Ports[0] = schema.ChannelPort{Role: schema.PortData, Reg: "BID_LO"}
	mustErr(t, i, "SemAutoIncPort")
}

func TestC4AccessSemantics(t *testing.T) {
	i := base()
	i.Blocks[0].Regs[0].Sem = 0
	mustErr(t, i, "C4")
}

func TestSelectorCollision(t *testing.T) {
	i := base()
	i.Blocks[0].Regs[1].Sel = 0x10 // same as Regs[0]
	mustErr(t, i, "collision")
}

func TestReservedContainment(t *testing.T) {
	i := base()
	i.Blocks[0].Regs[0].Sel = 0x40 // decodable, but outside meta [0x10,0x30)
	mustErr(t, i, "outside block")
}

func TestBlockOverlap(t *testing.T) {
	i := base()
	i.Blocks = append(i.Blocks, schema.Block{
		Name: "over", Plane: schema.CS1, Base: 0x20, Span: 0x08,
		Regs: []schema.Register{{Name: "X", Sel: 0x20, Plane: schema.CS1, Access: schema.R, Sem: schema.SemNormal}},
	})
	mustErr(t, i, "block overlap")
}

func TestFieldRange(t *testing.T) {
	i := base()
	i.Blocks[0].Regs[0].Fields = []schema.Field{{Name: "bad", Hi: 16, Lo: 0}}
	mustErr(t, i, "bad range")
}

func TestPlaneMismatch(t *testing.T) {
	i := base()
	i.Blocks[0].Regs[0].Plane = schema.CS3 // block is CS1
	mustErr(t, i, "!= block")
}

func TestEmptyDescriptor(t *testing.T) {
	i := base()
	i.Descriptor.Fields = nil
	mustErr(t, i, "no fields")
}

func TestDuplicateRegName(t *testing.T) {
	i := base()
	i.Blocks[0].Regs = append(i.Blocks[0].Regs,
		schema.Register{Name: "BID_LO", Sel: 0x20, Plane: schema.CS1, Access: schema.R, Sem: schema.SemNormal})
	mustErr(t, i, "duplicate register")
}

func TestExpectNonReadable(t *testing.T) {
	i := base()
	i.Blocks[0].Regs = append(i.Blocks[0].Regs,
		schema.Register{Name: "WO", Sel: 0x20, Plane: schema.CS1, Access: schema.W, Sem: schema.SemStrobe, Expect: p16(0x1)})
	mustErr(t, i, "not readable")
}

func TestBuildIDRegMissing(t *testing.T) {
	i := base()
	i.BuildIDLo = ""
	mustErr(t, i, "build-ID")
}

func TestBuildIDMovesOnChange(t *testing.T) {
	a := base()
	b := base()
	b.Blocks[0].Regs[0].Sel = 0x24 // still valid, but a different schema
	if a.BuildID() == b.BuildID() {
		t.Fatal("BuildID must change when the schema changes")
	}
}

func p16(v uint16) *uint16 { return &v }

// A register with an omitted/zero Access must fail — else it is silently
// mislabeled AccR in the bindings yet dropped from the read/write decode.
func TestAccessEnum(t *testing.T) {
	i := base()
	i.Blocks[0].Regs[3].Access = 0 // omit Access (CNT)
	mustErr(t, i, "invalid Access")
}

// A selector past the 8-bit space the RTL decodes must fail Validate — else it
// truncates on the fabric and silently aliases a low selector.
func TestSelectorExceeds8Bit(t *testing.T) {
	i := base()
	// widen a block into the >0xFF space and put a register there.
	i.Blocks = append(i.Blocks, schema.Block{
		Name: "hi", Plane: schema.CS1, Base: 0x100, Span: 0x20,
		Regs: []schema.Register{
			{Name: "HI_REG", Sel: 0x110, Plane: schema.CS1, Access: schema.RW, Sem: schema.SemNormal},
		},
	})
	mustErr(t, i, "8-bit selector space")
}

func TestDescriptorZeroWidthField(t *testing.T) {
	i := base()
	i.Descriptor.Fields = append(i.Descriptor.Fields, schema.RecField{Name: "pad", Bits: 0})
	mustErr(t, i, "zero width")
}

func TestDescriptorDuplicateField(t *testing.T) {
	i := base()
	i.Descriptor.Fields = append(i.Descriptor.Fields, schema.RecField{Name: "x", Bits: 4})
	mustErr(t, i, "duplicate field name")
}

// Opcodes: a valid strobe payload passes; bad targets/collisions fail.
func withOpcodeReg() schema.Interface {
	i := base()
	i.Blocks[0].Regs = append(i.Blocks[0].Regs, schema.Register{
		Name: "OPCODE", Sel: 0x20, Plane: schema.CS1, Access: schema.W, Sem: schema.SemStrobe,
	})
	return i
}

func TestOpcodeValid(t *testing.T) {
	i := withOpcodeReg()
	i.Opcodes = []schema.Opcode{{Name: "OP_GO", Reg: "OPCODE", Value: 0x0001}}
	if errs := i.Validate(); len(errs) > 0 {
		t.Fatalf("a valid opcode should pass, got %v", errs)
	}
	// and it must move the build-ID (folded into the hash).
	if withOpcodeReg().BuildID() == i.BuildID() {
		t.Fatal("adding an opcode must change the BuildID")
	}
}

func TestOpcodeUnknownReg(t *testing.T) {
	i := withOpcodeReg()
	i.Opcodes = []schema.Opcode{{Name: "OP_GO", Reg: "NOPE", Value: 0x0001}}
	mustErr(t, i, "unknown register")
}

func TestOpcodeNonStrobeReg(t *testing.T) {
	i := withOpcodeReg()
	i.Opcodes = []schema.Opcode{{Name: "OP_GO", Reg: "BID_LO", Value: 0x0001}} // read-only, not a strobe
	mustErr(t, i, "not a writable strobe")
}

func TestOpcodeValueCollision(t *testing.T) {
	i := withOpcodeReg()
	i.Opcodes = []schema.Opcode{
		{Name: "OP_GO", Reg: "OPCODE", Value: 0x0001},
		{Name: "OP_DUP", Reg: "OPCODE", Value: 0x0001},
	}
	mustErr(t, i, "value collision")
}

// ---- CS1 decodability + read aliases -------------------------------------

// A CS1 selector with bit 0, 1 or 7 set can never be decoded by the fabric: the
// RTL forces those bits low because A1 carries a clock, A2 floats high, and A8 is
// not wired. Such a register silently aliases onto sel&0x7c and answers with a
// neighbour's data — the exact failure that once collapsed a drained record to
// mem[0] replicated. Validate must reject it at schema-edit time.
func TestCS1SelectorMustBeDecodable(t *testing.T) {
	for _, sel := range []uint16{0x11, 0x12, 0x13, 0x82} {
		i := base()
		i.Blocks[0].Span = 0x80 // widen so containment is not what fails
		i.Blocks[0].Regs[1].Sel = sel
		mustErr(t, i, "not decodable on CS1")
	}
}

// The same rule must NOT apply to CS3: that plane is decoded by the MAX V, not by
// our fabric, and its vendor selectors are not multiples of 4.
func TestCS3SelectorsAreNotConstrained(t *testing.T) {
	i := base()
	i.Blocks = append(i.Blocks, schema.Block{
		Name: "vendor", Plane: schema.CS3, Base: 0x00, Span: 0x10,
		Regs: []schema.Register{
			{Name: "V7", Sel: 0x07, Plane: schema.CS3, Access: schema.W, Sem: schema.SemNormal},
			{Name: "V9", Sel: 0x09, Plane: schema.CS3, Access: schema.W, Sem: schema.SemNormal},
		},
	})
	if errs := i.Validate(); len(errs) > 0 {
		t.Fatalf("CS3 selectors must not be constrained by the CS1 decode rule, got %v", errs)
	}
}

// withAlias puts a read alias on DATA in unreserved CS1 space.
func withAlias(sel uint16) schema.Interface {
	i := base()
	i.Blocks[0].Regs[2].ReadAliases = []schema.Alias{{Sel: sel, Desc: "why"}}
	return i
}

// A well-formed alias in unreserved, decodable space must validate.
func TestReadAliasValid(t *testing.T) {
	if errs := withAlias(0x00).Validate(); len(errs) > 0 {
		t.Fatalf("a valid read alias should pass, got %v", errs)
	}
}

// An alias must never shadow a real register: the register would become
// unreachable-by-name and the mux would carry two cases for one selector.
func TestReadAliasShadowsRegister(t *testing.T) {
	mustErr(t, withAlias(0x10), "shadows register")
}

// Two registers must not claim the same alias selector.
func TestReadAliasDuplicate(t *testing.T) {
	i := withAlias(0x00)
	i.Blocks[0].Regs[3].ReadAliases = []schema.Alias{{Sel: 0x00, Desc: "dup"}}
	mustErr(t, i, "both alias")
}

// An alias inside a reserved block range is a landmine: the block promises that
// space to a future register, which would then collide with the alias.
func TestReadAliasInsideReservedBlock(t *testing.T) {
	mustErr(t, withAlias(0x28), "reserved block")
}

// An alias is a read doorway; a write-only register cannot have one.
func TestReadAliasOnNonReadable(t *testing.T) {
	i := base()
	i.Blocks[0].Regs = append(i.Blocks[0].Regs, schema.Register{
		Name: "WONLY", Sel: 0x20, Plane: schema.CS1, Access: schema.W, Sem: schema.SemStrobe,
		ReadAliases: []schema.Alias{{Sel: 0x00, Desc: "no"}},
	})
	mustErr(t, i, "is not readable")
}

// An alias must obey the same CS1 decodability rule as a register.
func TestReadAliasMustBeDecodable(t *testing.T) {
	mustErr(t, withAlias(0x04|0x01), "not decodable on CS1")
}

// Aliases are deliberately OUTSIDE the build-ID: an alias cannot change how any
// declared register behaves, so it cannot mispair a fabric with an app, and
// writing down an alias the deployed fabric already implements must not move the
// fingerprint the app checks against it.
func TestReadAliasDoesNotMoveBuildID(t *testing.T) {
	if base().BuildID() != withAlias(0x00).BuildID() {
		t.Fatal("adding a read alias must NOT change the BuildID")
	}
}
