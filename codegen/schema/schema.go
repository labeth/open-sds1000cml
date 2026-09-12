// Package schema is the type system for the acq2 FPGA<->app register interface.
// One schema.Interface (codegen/ifacedef) is the single source of truth; the
// emitters in codegen/emit render it into the fabric's Verilog header + decode
// include, the app's Go bindings and the register-map reference, so a selector
// or a field can never drift between the RTL and the app.
//
// The idea (schema -> regs.vh/regmux.vh/iface.go/REGISTER-MAP.md, build-ID
// folded from the schema) comes from owned-fpga codegen (commits 570af15,
// 8938920, 2ee23a0); this is a smaller re-implementation for the one-plane
// default image of docs/acq2/05-WORKPLAN.md §2 (32 selectors, schema v2) and
// docs/acq2/06-TIERS.md §2 (128 selectors, schema v3).
package schema

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
)

// Access is the read/write capability of a register or a DIAG window entry.
type Access uint8

const (
	R  Access = 1
	W  Access = 2
	RW Access = 3
)

func (a Access) CanRead() bool  { return a&R != 0 }
func (a Access) CanWrite() bool { return a&W != 0 }
func (a Access) String() string {
	switch a {
	case R:
		return "R"
	case W:
		return "W"
	case RW:
		return "RW"
	}
	return fmt.Sprintf("Access(%d)", uint8(a))
}

// Source says where the generated read mux takes a readable register's value.
type Source uint8

const (
	SrcWire      Source = iota // rdata_<NAME> wire driven by hand RTL (default)
	SrcConst                   // the fixed Const value (identity registers)
	SrcBuildIDLo               // `IFACE_BUILD_ID_LO
	SrcBuildIDHi               // `IFACE_BUILD_ID_HI
	SrcAlias                   // another register's rdata (AliasOf); pops it too
)

func (s Source) String() string {
	switch s {
	case SrcWire:
		return "wire"
	case SrcConst:
		return "const"
	case SrcBuildIDLo:
		return "buildid_lo"
	case SrcBuildIDHi:
		return "buildid_hi"
	case SrcAlias:
		return "alias"
	}
	return fmt.Sprintf("Source(%d)", uint8(s))
}

// EnumValue is one named value of a field (a mode code shared by the RTL and
// the app: `<REG>_<FIELD>_<NAME> / iface.<Reg><Field><Name>).
type EnumValue struct {
	Name  string
	Value uint16
	Desc  string
}

// Field is a named bit range [Hi:Lo] of a 16-bit word.
type Field struct {
	Name   string
	Hi, Lo uint
	Enum   []EnumValue // optional named values (must fit the field)
	Desc   string
}

// Mask returns the field's 16-bit mask.
func (f Field) Mask() uint16 {
	return uint16(((1 << (f.Hi - f.Lo + 1)) - 1) << f.Lo)
}

// Width returns the field width in bits.
func (f Field) Width() uint { return f.Hi - f.Lo + 1 }

// Register is one selector of the CS1 map.
type Register struct {
	Name string
	Sel  uint8 // selector (word index at the CS1 base); bits outside Interface.SelMask are not allowed
	// Since is the schema version that introduced the register (0 = the first
	// version). With Interface.Legacy set, a register from the legacy
	// generation must keep a selector inside Legacy.SelMask and a newer one
	// must take a slot outside it (06-TIERS §2: "every v2 register keeps its
	// selector; new registers take odd slots").
	Since   uint16
	Access  Access
	Pop     bool   // pop-on-read port: every read advances the source (pop_<NAME>)
	Strobe  bool   // write strobe: no storage, the write word is decoded (opcodes)
	Source  Source // read-mux source (readable registers only)
	Const   uint16 // fixed read value when Source == SrcConst
	AliasOf string // register name when Source == SrcAlias
	// Stub marks a register the current fabric increment does not implement:
	// the generated decode reads 0 and emits no write strobe (the register is
	// write-ignored), the app tables carry Stub so tooling skips the readback
	// check. Structural (it moves the build-ID) because it changes what the
	// fabric does; flip it to false in the increment that implements the
	// register. Wire-sourced registers only.
	Stub   bool
	Fields []Field
	Desc   string
}

// Opcode is one accepted payload of the strobe register named by Interface.OpcodeReg.
type Opcode struct {
	Name  string // emitted verbatim as `OP_<Name> / iface.Op<Name>
	Value uint16
	Desc  string
}

// DiagEntry is one index (or a run of Count consecutive indices) of the
// DIAG_IDX -> DIAG_DATA window.
type DiagEntry struct {
	Name   string
	Idx    uint8
	Count  uint // 1 for a single window, N for an array (LANEMAP)
	Access Access
	// Stub: the current fabric increment does not implement the entry (reads 0,
	// writes ignored). The DIAG data mux is hand RTL, so this is a contract
	// mark for the fabric and the app tables, not generated logic.
	Stub   bool
	Fields []Field
	Desc   string
}

// Last returns the last index covered by the entry.
func (d DiagEntry) Last() uint8 { return d.Idx + uint8(d.Count) - 1 }

// Geometry is the record geometry shared by the fabric and the app.
type Geometry struct {
	RecDepth uint // record words (dual-channel samples) = RowCols * Rows
	AddrW    uint // address width; 2^AddrW >= RecDepth
	Margin   uint // writer pipeline tail: PRETRIG_MAX = RecDepth - Margin
	// v3 row geometry (06-TIERS §1.1, §1.3, §1.4): the record is Rows rows of
	// RowCols 16-bit words, the reader emits row-major / column-minor; the
	// stack delay line holds DlineRows rows; an accumulator cell is AccBits
	// wide, so StackNMax records of 8-bit samples never overflow it.
	RowCols   uint
	Rows      uint
	DlineRows uint
	AccBits   uint
	StackNMax uint
}

// PretrigMax is the largest programmable pre-trigger depth.
func (g Geometry) PretrigMax() uint { return g.RecDepth - g.Margin }

// Legacy describes the selector space of the previous fabric generation so
// the validator can keep the compatibility promise of 06-TIERS §2.
type Legacy struct {
	Version uint16 // last schema version of that generation (0 = no legacy rule)
	SelMask uint8  // the selector bits it decoded
}

// Interface is the whole contract of one fabric image.
type Interface struct {
	Name         string
	Version      uint16
	VersionMagic uint16 // value the VERSION register must return
	// SourceDigest is a content digest of the fabric's RTL sources, set by
	// ifacegen (-rtl) before rendering. It folds into the build-ID so an
	// RTL-only change (a new LANEMAP seed, a pipeline fix) yields a new
	// build-ID and the app reloads the fabric at boot instead of accepting the
	// stale one that still answers the old ID. 0 = schema-only identity.
	SourceDigest uint32
	FabricID     uint16 // value the FABRIC_ID register must return
	SelMask      uint8  // selector bits the fabric decodes (v2: 0x7c = A3..A7; v3: 0x7f = A1..A7)
	SelDesc      string // documentation of the selector space (not hashed)
	Legacy       Legacy
	// Undecoded lists selectors inside SelMask that no register may occupy:
	// the fabric reads 0 there and ignores writes (v3: the vendor arm/halt
	// words 0x21 / 0x57 so experiment E1 can be replayed).
	Undecoded    []uint8
	Geometry     Geometry
	Regs         []Register
	BuildIDLoReg string // register carrying the build-ID low word
	BuildIDHiReg string
	VersionReg   string
	FabricIDReg  string
	OpcodeReg    string
	Opcodes      []Opcode
	DiagIdxReg   string
	DiagDataReg  string
	Diag         []DiagEntry
	DiagReserved uint8 // first reserved DIAG index
}

// Reg looks a register up by name.
func (i Interface) Reg(name string) (Register, bool) {
	for _, r := range i.Regs {
		if r.Name == name {
			return r, true
		}
	}
	return Register{}, false
}

// DiagEntry looks a DIAG window entry up by name.
func (i Interface) DiagEntry(name string) (DiagEntry, bool) {
	for _, d := range i.Diag {
		if d.Name == name {
			return d, true
		}
	}
	return DiagEntry{}, false
}

// Aliases returns the registers whose Source is SrcAlias with AliasOf == name.
func (i Interface) Aliases(name string) []Register {
	var out []Register
	for _, r := range i.Regs {
		if r.Source == SrcAlias && r.AliasOf == name {
			out = append(out, r)
		}
	}
	return out
}

// SelCount is the number of distinct selectors the mask decodes (32 for 0x7c,
// 128 for 0x7f).
func (i Interface) SelCount() int {
	n := 1
	for m := i.SelMask; m != 0; m &= m - 1 {
		n <<= 1
	}
	return n
}

// Canonical is the deterministic text the build-ID hashes: every structural
// attribute of the interface, in declaration order. Desc strings are NOT part of
// it — documentation edits must not force a fabric rebuild — everything else is.
func (i Interface) Canonical() string {
	var b strings.Builder
	fmt.Fprintf(&b, "iface %s v%d magic=%04x fabric=%04x selmask=%02x source=%08x\n", i.Name, i.Version, i.VersionMagic, i.FabricID, i.SelMask, i.SourceDigest)
	fmt.Fprintf(&b, "legacy v%d selmask=%02x undecoded=%v\n", i.Legacy.Version, i.Legacy.SelMask, i.Undecoded)
	g := i.Geometry
	fmt.Fprintf(&b, "geom depth=%d addrw=%d margin=%d rowcols=%d rows=%d dline=%d accbits=%d stacknmax=%d\n",
		g.RecDepth, g.AddrW, g.Margin, g.RowCols, g.Rows, g.DlineRows, g.AccBits, g.StackNMax)
	fmt.Fprintf(&b, "identity lo=%s hi=%s ver=%s fab=%s opcode=%s diagidx=%s diagdata=%s diagrsvd=%02x\n",
		i.BuildIDLoReg, i.BuildIDHiReg, i.VersionReg, i.FabricIDReg, i.OpcodeReg, i.DiagIdxReg, i.DiagDataReg, i.DiagReserved)
	fields := func(fs []Field) {
		for _, f := range fs {
			fmt.Fprintf(&b, "  field %s [%d:%d]", f.Name, f.Hi, f.Lo)
			for _, e := range f.Enum {
				fmt.Fprintf(&b, " %s=%d", e.Name, e.Value)
			}
			b.WriteString("\n")
		}
	}
	for _, r := range i.Regs {
		fmt.Fprintf(&b, "reg %s sel=%02x since=%d acc=%d pop=%t strobe=%t src=%d const=%04x alias=%s stub=%t\n",
			r.Name, r.Sel, r.Since, r.Access, r.Pop, r.Strobe, r.Source, r.Const, r.AliasOf, r.Stub)
		fields(r.Fields)
	}
	for _, o := range i.Opcodes {
		fmt.Fprintf(&b, "op %s=%04x\n", o.Name, o.Value)
	}
	for _, d := range i.Diag {
		fmt.Fprintf(&b, "diag %s idx=%02x count=%d acc=%d stub=%t\n", d.Name, d.Idx, d.Count, d.Access, d.Stub)
		fields(d.Fields)
	}
	return b.String()
}

// BuildID fingerprints the interface AND (through SourceDigest) the RTL it was
// generated for: FNV-1a 64-bit over Canonical(), folded to 32 bits as
// (hi32 XOR lo32). It is read back through the BUILDID_LO/HI registers; the
// app refuses (reloads) a fabric whose build-ID differs.
func (i Interface) BuildID() uint32 {
	h := fnv.New64a()
	h.Write([]byte(i.Canonical()))
	s := h.Sum64()
	return uint32(s>>32) ^ uint32(s)
}

var identRe = func(s string) bool {
	if s == "" {
		return false
	}
	for k, c := range s {
		switch {
		case c >= 'A' && c <= 'Z', c == '_':
		case c >= '0' && c <= '9':
			if k == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func checkFields(prefix string, fields []Field, errs *[]string) {
	seen := map[string]bool{}
	var used uint16
	for _, f := range fields {
		if !identRe(f.Name) {
			*errs = append(*errs, fmt.Sprintf("%s: field name %q is not UPPER_SNAKE", prefix, f.Name))
		}
		if seen[f.Name] {
			*errs = append(*errs, fmt.Sprintf("%s: duplicate field %s", prefix, f.Name))
		}
		seen[f.Name] = true
		if f.Hi > 15 || f.Lo > f.Hi {
			*errs = append(*errs, fmt.Sprintf("%s.%s: bad range [%d:%d]", prefix, f.Name, f.Hi, f.Lo))
			continue
		}
		if used&f.Mask() != 0 {
			*errs = append(*errs, fmt.Sprintf("%s.%s: overlaps an earlier field", prefix, f.Name))
		}
		used |= f.Mask()
		en, ev := map[string]bool{}, map[uint16]bool{}
		for _, e := range f.Enum {
			if !identRe(e.Name) {
				*errs = append(*errs, fmt.Sprintf("%s.%s: enum name %q is not UPPER_SNAKE", prefix, f.Name, e.Name))
			}
			if en[e.Name] || ev[e.Value] {
				*errs = append(*errs, fmt.Sprintf("%s.%s: enum %s=%d duplicates an earlier one", prefix, f.Name, e.Name, e.Value))
			}
			en[e.Name], ev[e.Value] = true, true
			if uint(e.Value) >= 1<<f.Width() {
				*errs = append(*errs, fmt.Sprintf("%s.%s: enum %s=%d does not fit %d bits", prefix, f.Name, e.Name, e.Value, f.Width()))
			}
		}
	}
}

// Validate returns every structural problem it finds (nil when the interface is
// sound). Everything the emitters rely on is checked here, at schema-edit time.
func (i Interface) Validate() []string {
	var errs []string
	if i.Name == "" {
		errs = append(errs, "interface has no name")
	}
	if i.SelMask == 0 {
		errs = append(errs, "SelMask is zero")
	}
	if i.SelMask&0x80 != 0 {
		errs = append(errs, fmt.Sprintf("SelMask 0x%02x decodes bit 7 (no GPMC A8 reaches the fabric)", i.SelMask))
	}
	if i.Legacy.Version != 0 {
		if i.Legacy.Version > i.Version {
			errs = append(errs, fmt.Sprintf("legacy version %d is newer than the schema (v%d)", i.Legacy.Version, i.Version))
		}
		if i.Legacy.SelMask&^i.SelMask != 0 {
			errs = append(errs, fmt.Sprintf("legacy SelMask 0x%02x is not a subset of SelMask 0x%02x", i.Legacy.SelMask, i.SelMask))
		}
	}
	g := i.Geometry
	if g.RecDepth == 0 || g.AddrW == 0 || (1<<g.AddrW) < g.RecDepth || g.Margin >= g.RecDepth {
		errs = append(errs, fmt.Sprintf("geometry depth=%d addrw=%d margin=%d is inconsistent", g.RecDepth, g.AddrW, g.Margin))
	}
	if g.RowCols == 0 || g.Rows == 0 || g.RowCols*g.Rows != g.RecDepth {
		errs = append(errs, fmt.Sprintf("geometry rowcols=%d rows=%d does not make depth=%d", g.RowCols, g.Rows, g.RecDepth))
	}
	if g.DlineRows == 0 || g.DlineRows > g.Rows {
		errs = append(errs, fmt.Sprintf("geometry dline=%d rows is outside 1..%d", g.DlineRows, g.Rows))
	}
	if g.AccBits == 0 || g.AccBits > 32 || g.StackNMax == 0 || uint64(255)*uint64(g.StackNMax) >= uint64(1)<<g.AccBits {
		errs = append(errs, fmt.Sprintf("geometry accbits=%d cannot hold stacknmax=%d records of 8-bit samples", g.AccBits, g.StackNMax))
	}
	undec := map[uint8]bool{}
	for _, s := range i.Undecoded {
		if s&^i.SelMask != 0 {
			errs = append(errs, fmt.Sprintf("undecoded selector 0x%02x has bits outside SelMask 0x%02x", s, i.SelMask))
		}
		if undec[s] {
			errs = append(errs, fmt.Sprintf("undecoded selector 0x%02x listed twice", s))
		}
		undec[s] = true
	}
	names := map[string]Register{}
	sels := map[uint8]string{}
	for _, r := range i.Regs {
		p := "register " + r.Name
		if !identRe(r.Name) {
			errs = append(errs, fmt.Sprintf("%s: name is not UPPER_SNAKE", p))
		}
		if _, dup := names[r.Name]; dup {
			errs = append(errs, fmt.Sprintf("%s: duplicate name", p))
		}
		names[r.Name] = r
		if r.Sel&^i.SelMask != 0 {
			errs = append(errs, fmt.Sprintf("%s: selector 0x%02x has bits outside SelMask 0x%02x", p, r.Sel, i.SelMask))
		}
		if other, dup := sels[r.Sel]; dup {
			errs = append(errs, fmt.Sprintf("%s: selector 0x%02x already used by %s", p, r.Sel, other))
		}
		sels[r.Sel] = r.Name
		if undec[r.Sel] {
			errs = append(errs, fmt.Sprintf("%s: selector 0x%02x must stay undecoded", p, r.Sel))
		}
		if r.Since > i.Version {
			errs = append(errs, fmt.Sprintf("%s: Since v%d is newer than the schema (v%d)", p, r.Since, i.Version))
		}
		if i.Legacy.Version != 0 {
			outside := r.Sel&^i.Legacy.SelMask != 0
			if r.Since <= i.Legacy.Version && outside {
				errs = append(errs, fmt.Sprintf("%s: a v%d register must keep a selector inside the legacy mask 0x%02x, has 0x%02x", p, r.Since, i.Legacy.SelMask, r.Sel))
			}
			if r.Since > i.Legacy.Version && !outside {
				errs = append(errs, fmt.Sprintf("%s: a v%d register must take a slot outside the legacy mask 0x%02x, has 0x%02x", p, r.Since, i.Legacy.SelMask, r.Sel))
			}
		}
		if r.Access < R || r.Access > RW {
			errs = append(errs, fmt.Sprintf("%s: access not declared", p))
		}
		if r.Pop && !r.Access.CanRead() {
			errs = append(errs, fmt.Sprintf("%s: pop-on-read but not readable", p))
		}
		if r.Strobe && (r.Access != W) {
			errs = append(errs, fmt.Sprintf("%s: a strobe must be write-only", p))
		}
		if r.Source != SrcWire && !r.Access.CanRead() {
			errs = append(errs, fmt.Sprintf("%s: read source %s on a write-only register", p, r.Source))
		}
		if r.Source != SrcConst && r.Const != 0 {
			errs = append(errs, fmt.Sprintf("%s: Const set but Source is %s", p, r.Source))
		}
		if (r.Source == SrcAlias) != (r.AliasOf != "") {
			errs = append(errs, fmt.Sprintf("%s: AliasOf must be set exactly when Source is alias", p))
		}
		if r.Source != SrcWire && r.Access.CanWrite() {
			errs = append(errs, fmt.Sprintf("%s: a %s-sourced register cannot be writable", p, r.Source))
		}
		if r.Source == SrcAlias && len(r.Fields) > 0 {
			errs = append(errs, fmt.Sprintf("%s: an alias carries no fields of its own", p))
		}
		if r.Stub && (r.Source != SrcWire || r.Strobe || r.Pop) {
			errs = append(errs, fmt.Sprintf("%s: only a plain wire-sourced register can be a stub", p))
		}
		checkFields(p, r.Fields, &errs)
	}
	for _, r := range i.Regs {
		if r.Source != SrcAlias {
			continue
		}
		t, ok := names[r.AliasOf]
		switch {
		case !ok:
			errs = append(errs, fmt.Sprintf("register %s: alias target %s does not exist", r.Name, r.AliasOf))
		case !t.Access.CanRead():
			errs = append(errs, fmt.Sprintf("register %s: alias target %s is not readable", r.Name, r.AliasOf))
		case t.Source == SrcAlias:
			errs = append(errs, fmt.Sprintf("register %s: alias of an alias (%s)", r.Name, r.AliasOf))
		case t.Pop != r.Pop:
			errs = append(errs, fmt.Sprintf("register %s: pop-on-read differs from alias target %s", r.Name, r.AliasOf))
		case t.Stub:
			errs = append(errs, fmt.Sprintf("register %s: alias target %s is a stub", r.Name, r.AliasOf))
		}
	}
	// identity registers
	want := func(role, name string, src Source, cst uint16) {
		r, ok := names[name]
		switch {
		case !ok:
			errs = append(errs, fmt.Sprintf("%s register %q does not exist", role, name))
		case r.Access != R:
			errs = append(errs, fmt.Sprintf("%s register %s must be read-only", role, name))
		case r.Source != src:
			errs = append(errs, fmt.Sprintf("%s register %s must have source %s", role, name, src))
		case src == SrcConst && r.Const != cst:
			errs = append(errs, fmt.Sprintf("%s register %s must read 0x%04x", role, name, cst))
		}
	}
	want("build-ID low", i.BuildIDLoReg, SrcBuildIDLo, 0)
	want("build-ID high", i.BuildIDHiReg, SrcBuildIDHi, 0)
	want("version", i.VersionReg, SrcConst, i.VersionMagic)
	want("fabric-ID", i.FabricIDReg, SrcConst, i.FabricID)
	for _, r := range i.Regs {
		if r.Source == SrcBuildIDLo && r.Name != i.BuildIDLoReg || r.Source == SrcBuildIDHi && r.Name != i.BuildIDHiReg {
			errs = append(errs, fmt.Sprintf("register %s: stray build-ID source", r.Name))
		}
	}
	// opcodes
	if r, ok := names[i.OpcodeReg]; !ok || !r.Strobe {
		errs = append(errs, fmt.Sprintf("opcode register %q must exist and be a strobe", i.OpcodeReg))
	}
	if len(i.Opcodes) == 0 {
		errs = append(errs, "no opcodes declared")
	}
	opn, opv := map[string]bool{}, map[uint16]bool{}
	for _, o := range i.Opcodes {
		if !identRe(o.Name) {
			errs = append(errs, fmt.Sprintf("opcode %q: name is not UPPER_SNAKE", o.Name))
		}
		if opn[o.Name] || opv[o.Value] {
			errs = append(errs, fmt.Sprintf("opcode %s=0x%04x duplicates an earlier one", o.Name, o.Value))
		}
		opn[o.Name], opv[o.Value] = true, true
	}
	// DIAG window
	if r, ok := names[i.DiagIdxReg]; !ok || r.Access != RW || r.Stub {
		errs = append(errs, fmt.Sprintf("DIAG index register %q must exist and be RW", i.DiagIdxReg))
	}
	if r, ok := names[i.DiagDataReg]; !ok || r.Access != RW || r.Stub {
		errs = append(errs, fmt.Sprintf("DIAG data register %q must exist and be RW", i.DiagDataReg))
	}
	dn := map[string]bool{}
	type span struct {
		lo, hi uint
		name   string
	}
	var spans []span
	for _, d := range i.Diag {
		p := "diag " + d.Name
		if !identRe(d.Name) {
			errs = append(errs, fmt.Sprintf("%s: name is not UPPER_SNAKE", p))
		}
		if dn[d.Name] {
			errs = append(errs, fmt.Sprintf("%s: duplicate name", p))
		}
		dn[d.Name] = true
		if d.Count == 0 || uint(d.Idx)+d.Count > 256 {
			errs = append(errs, fmt.Sprintf("%s: bad count %d at 0x%02x", p, d.Count, d.Idx))
			continue
		}
		if d.Access < R || d.Access > RW {
			errs = append(errs, fmt.Sprintf("%s: access not declared", p))
		}
		if uint(d.Last()) >= uint(i.DiagReserved) && i.DiagReserved != 0 {
			errs = append(errs, fmt.Sprintf("%s: reaches into the reserved range (>= 0x%02x)", p, i.DiagReserved))
		}
		spans = append(spans, span{uint(d.Idx), uint(d.Last()), d.Name})
		checkFields(p, d.Fields, &errs)
	}
	sort.Slice(spans, func(a, b int) bool { return spans[a].lo < spans[b].lo })
	for k := 1; k < len(spans); k++ {
		if spans[k].lo <= spans[k-1].hi {
			errs = append(errs, fmt.Sprintf("diag %s overlaps %s", spans[k].name, spans[k-1].name))
		}
	}
	return errs
}
