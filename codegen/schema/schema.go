// Package schema is the single source of truth for the owned SDS1000CML
// acquisition FPGA <-> app interface. A validated schema.Interface is fed to the
// text/template emitters (Verilog regs.vh + regmux.vh, the app's Go bindings, and
// the register-map doc) so the fabric and the firmware can never drift: editing
// the interface is a schema edit, and both sides regenerate in lockstep.
//
// Validate() enforces the four load-bearing contracts that must be frozen before
// the fabric ships — the whole "no dead-end / results not raw" promise rests on
// these:
//
//	C1  stream sample lane >= 18 bits AND >= 1 reserved bypassable transform stage
//	C2  programmable pre-trigger capture AND a trig_mark; RecordDepth > 0
//	C3  every result/event channel carries an overflow / lost-count field, and its
//	    fields sum to RecordBits
//	C4  every register declares explicit access-semantics (never defaulted)
//
// plus structural safety: no selector collisions within a plane, every register
// inside its block's reserved range, no block overlaps, well-formed bit fields,
// readable build-ID registers, capture geometry wide enough for the record, and
// well-formed result-channel ports.
package schema

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
)

// Plane is a GPMC chip-select register window.
type Plane uint8

const (
	CS1 Plane = 1 // acquisition / read plane
	CS3 Plane = 3 // config / control plane
)

func (p Plane) String() string {
	switch p {
	case CS1:
		return "CS1"
	case CS3:
		return "CS3"
	}
	return fmt.Sprintf("CS?(%d)", uint8(p))
}

// Access is the read/write capability of a register.
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
	return "?"
}

// Sem is the behavioral access-semantics of a register (C4). These are the
// hazards that dominated the acquisition RE and MUST be encoded on both sides,
// never left to hand-written logic. A register may combine several.
type Sem uint16

const (
	SemNormal        Sem = 1 << iota // plain register, no side effect
	SemStrobe                        // write triggers an action; value may be ignored
	SemAutoIncPort                   // each READ pops/advances an internal pointer
	SemReadAfterHalt                 // only valid after capture-halt
	SemLevelStatus                   // live level; re-read, not edge/sticky
	SemWaitGuarded                   // read holds the bus WAIT line until ready (hang risk)
)

var semTable = []struct {
	bit  Sem
	name string
}{
	{SemNormal, "normal"},
	{SemStrobe, "strobe"},
	{SemAutoIncPort, "auto-inc-port"},
	{SemReadAfterHalt, "read-after-halt"},
	{SemLevelStatus, "level-status"},
	{SemWaitGuarded, "wait-guarded"},
}

// List returns the individual semantics set, in canonical order.
func (s Sem) List() []string {
	var out []string
	for _, e := range semTable {
		if s&e.bit != 0 {
			out = append(out, e.name)
		}
	}
	return out
}

func (s Sem) String() string {
	if l := s.List(); len(l) > 0 {
		return strings.Join(l, "|")
	}
	return "unset"
}

// Field is a named bit range within a 16-bit register word.
type Field struct {
	Name string
	Hi   uint // inclusive, 0..15
	Lo   uint // inclusive, <= Hi
	Desc string
}

// Register is one 16-bit addressable word in a plane.
type Register struct {
	Name   string
	Sel    uint16 // selector (NOT the pre-shifted byte address)
	Plane  Plane
	Access Access
	Sem    Sem
	Fields []Field
	Expect *uint16 // for identity/magic regs: the value a correct fabric returns
	Desc   string
}

// Block is a reserved selector range for one feature-class. Registers grow
// inside their block so the generated map never renumbers existing features. A
// block may declare no registers yet: it then only reserves its range (v1/v2/v3
// features claim their space now, before any selector is assigned).
type Block struct {
	Name  string
	Plane Plane
	Base  uint16 // first selector
	Span  uint16 // reserved count; block owns [Base, Base+Span)
	Regs  []Register
	Desc  string
}

// RecField is one field of a result/event channel record (bit width, in order).
type RecField struct {
	Name     string
	Bits     uint
	Overflow bool // marks the mandatory overflow / lost-count field (C3)
	Desc     string
}

// PortRole is the fixed accessor kind a result-channel exposes.
type PortRole uint8

const (
	PortData  PortRole = iota + 1 // auto-inc read port (pops one word/read)
	PortCount                     // level-status count + overflow port
	PortReset                     // optional strobe that clears the channel FIFO
)

func (r PortRole) String() string {
	switch r {
	case PortData:
		return "data"
	case PortCount:
		return "count"
	case PortReset:
		return "reset"
	}
	return "?"
}

// ChannelPort binds one of a channel's fixed accessor roles to the register that
// implements it. The uniform triad (DATA auto-inc, COUNT level-status, optional
// RESET strobe) lets the emitter generate one reusable result_fifo instance per
// channel, so v1/v2 taps (measure, trig_event, decode) are instances of the same
// pattern rather than hand-written FIFOs.
type ChannelPort struct {
	Role PortRole
	Reg  string // the Register.Name that implements this port
}

// Channel is a result/event channel drained/polled by the app (NOT raw samples).
type Channel struct {
	Name       string
	RecordBits uint          // must equal the sum of Fields' Bits
	Fields     []RecField    // LSB-first packing, in order
	Ports      []ChannelPort // optional; a wired channel declares its accessor triad
	Desc       string
}

// Descriptor is the DMA burst-drain descriptor layout the app programs.
type Descriptor struct {
	Name   string
	Fields []RecField
	Desc   string
}

// Stream is the canonical sample-stream contract (C1).
type Stream struct {
	SampleLaneBits  uint // >= 18 (headroom for v3 dither/ERES/DDC/math)
	TransformStages uint // >= 1 reserved bypassable in-line transform slots
	Desc            string
}

// Capture is the capture-buffer contract (C2). AddrBits and Margin are the single
// source of geometry truth for the RTL, too: the emitter derives REC_DEPTH,
// ADDR_W, and PRETRIG_MAX = RecordDepth-Margin into regs.vh so the circular writer
// and the app agree by construction (no Verilog module parameter to drift).
type Capture struct {
	RecordDepth         uint
	AddrBits            uint // physical record address width; 2^AddrBits >= RecordDepth
	Margin              uint // registered-write pipeline tail; PRETRIG_MAX = RecordDepth-Margin
	PreTrigProgrammable bool // must be true — v2 protocol trigger anchors retroactively
	TrigMark            bool // must be true
	Desc                string
}

// PretrigMax is the largest programmable pre+post window that can never overwrite
// a still-needed pre-trigger cell in the circular buffer (see DESIGN §4.4).
func (c Capture) PretrigMax() uint { return c.RecordDepth - c.Margin }

// Interface is the whole FPGA<->app contract: the single source of truth.
type Interface struct {
	Name       string
	Version    string
	BuildIDLo  string // Register.Name carrying the low 16 bits of the schema hash
	BuildIDHi  string // Register.Name carrying the high 16 bits
	Stream     Stream
	Capture    Capture
	Blocks     []Block
	Channels   []Channel
	Descriptor Descriptor
}

// AllRegs returns every register across all blocks, in declaration order.
func (i Interface) AllRegs() []Register {
	var rs []Register
	for _, b := range i.Blocks {
		rs = append(rs, b.Regs...)
	}
	return rs
}

func (i Interface) findReg(name string) (Register, bool) {
	for _, r := range i.AllRegs() {
		if r.Name == name {
			return r, true
		}
	}
	return Register{}, false
}

// Validate enforces the four contracts + structural safety. It returns ALL
// problems (not just the first) so a schema edit that breaks something fails
// loudly and completely.
func (i Interface) Validate() []error {
	var errs []error
	add := func(f string, a ...any) { errs = append(errs, fmt.Errorf(f, a...)) }

	// C1 — stream contract.
	if i.Stream.SampleLaneBits < 18 {
		add("C1: stream SampleLaneBits=%d, want >=18 (headroom for v3 transforms)", i.Stream.SampleLaneBits)
	}
	if i.Stream.TransformStages < 1 {
		add("C1: TransformStages=%d, want >=1 reserved in-line transform slot", i.Stream.TransformStages)
	}
	// C2 — capture contract.
	if !i.Capture.PreTrigProgrammable {
		add("C2: capture PreTrigProgrammable must be true (v2 protocol trigger anchors retroactively)")
	}
	if !i.Capture.TrigMark {
		add("C2: capture TrigMark must be true")
	}
	if i.Capture.RecordDepth == 0 {
		add("C2: capture RecordDepth must be > 0")
	}
	// Capture geometry (single source of truth for the RTL): address width wide
	// enough for the record, and a non-degenerate registered-write margin.
	if i.Capture.RecordDepth > 0 {
		if i.Capture.AddrBits == 0 || uint64(1)<<i.Capture.AddrBits < uint64(i.Capture.RecordDepth) {
			add("capture AddrBits=%d cannot address RecordDepth=%d (need 2^AddrBits >= RecordDepth)", i.Capture.AddrBits, i.Capture.RecordDepth)
		}
		if i.Capture.Margin < 1 {
			add("capture Margin=%d must be >=1 (registered-write pipeline tail; DESIGN §4.4 uses 2)", i.Capture.Margin)
		}
		if i.Capture.Margin >= i.Capture.RecordDepth {
			add("capture Margin=%d must be < RecordDepth=%d", i.Capture.Margin, i.Capture.RecordDepth)
		}
	}

	// C3 — every channel has an overflow field and a consistent record width.
	chanByName := map[string]Channel{}
	for _, c := range i.Channels {
		chanByName[c.Name] = c
		var sum uint
		over := 0
		names := map[string]bool{}
		for _, f := range c.Fields {
			sum += f.Bits
			if f.Overflow {
				over++
			}
			if names[f.Name] {
				add("channel %q: duplicate field %q", c.Name, f.Name)
			}
			names[f.Name] = true
			if f.Bits == 0 {
				add("channel %q field %q: zero width", c.Name, f.Name)
			}
		}
		if over == 0 {
			add("C3: channel %q has no overflow/lost-count field", c.Name)
		}
		if sum != c.RecordBits {
			add("channel %q: fields sum to %d bits, RecordBits=%d", c.Name, sum, c.RecordBits)
		}
	}
	// Descriptor sanity.
	if len(i.Descriptor.Fields) == 0 {
		add("descriptor %q: no fields", i.Descriptor.Name)
	}

	// C4 + structural: registers.
	type key struct {
		p Plane
		s uint16
	}
	seen := map[key]string{}
	blockOf := map[string]Block{}
	for _, b := range i.Blocks {
		for _, r := range b.Regs {
			blockOf[r.Name] = b
		}
	}
	regNames := map[string]bool{}
	for _, r := range i.AllRegs() {
		if regNames[r.Name] {
			add("duplicate register name %q", r.Name)
		}
		regNames[r.Name] = true
		// C4 — explicit semantics.
		if r.Sem == 0 {
			add("C4: register %q has no access-semantics (set SemNormal explicitly if plain)", r.Name)
		}
		// selector collision within a plane.
		k := key{r.Plane, r.Sel}
		if prev, ok := seen[k]; ok {
			add("selector collision: %s and %s both at %s sel %#04x", prev, r.Name, r.Plane, r.Sel)
		}
		seen[k] = r.Name
		// containment in the owning block's reserved range + plane match.
		b := blockOf[r.Name]
		if r.Plane != b.Plane {
			add("register %q plane %s != block %q plane %s", r.Name, r.Plane, b.Name, b.Plane)
		}
		if r.Sel < b.Base || r.Sel >= b.Base+b.Span {
			add("register %q sel %#04x outside block %q range [%#04x,%#04x)", r.Name, r.Sel, b.Name, b.Base, b.Base+b.Span)
		}
		// Expect only on readable regs.
		if r.Expect != nil && !r.Access.CanRead() {
			add("register %q has Expect but is not readable", r.Name)
		}
		// fields well-formed + non-overlapping within the word.
		var used uint16
		for _, f := range r.Fields {
			if f.Hi > 15 || f.Lo > f.Hi {
				add("register %q field %q: bad range [%d:%d]", r.Name, f.Name, f.Hi, f.Lo)
				continue
			}
			mask := fieldMask(f.Hi, f.Lo)
			if used&mask != 0 {
				add("register %q field %q: overlaps another field", r.Name, f.Name)
			}
			used |= mask
		}
	}

	// block overlaps within a plane.
	byPlane := map[Plane][]Block{}
	for _, b := range i.Blocks {
		if b.Span == 0 {
			add("block %q: zero span", b.Name)
		}
		byPlane[b.Plane] = append(byPlane[b.Plane], b)
	}
	for _, bs := range byPlane {
		sort.Slice(bs, func(a, c int) bool { return bs[a].Base < bs[c].Base })
		for n := 1; n < len(bs); n++ {
			if bs[n].Base < bs[n-1].Base+bs[n-1].Span {
				add("block overlap on %s: %q [%#04x,%#04x) and %q base %#04x",
					bs[n].Plane, bs[n-1].Name, bs[n-1].Base, bs[n-1].Base+bs[n-1].Span, bs[n].Name, bs[n].Base)
			}
		}
	}

	// result-channel ports: every declared port must bind a register whose
	// access-semantics match its role, so the generated port decode and the
	// reusable result_fifo instance are entirely schema-derived (never drift).
	for _, c := range i.Channels {
		for _, p := range c.Ports {
			r, ok := i.findReg(p.Reg)
			if !ok {
				add("channel %q port %s: register %q not found", c.Name, p.Role, p.Reg)
				continue
			}
			switch p.Role {
			case PortData:
				if r.Sem&SemAutoIncPort == 0 {
					add("channel %q DATA port %q must be SemAutoIncPort", c.Name, p.Reg)
				}
			case PortCount:
				if r.Sem&SemLevelStatus == 0 {
					add("channel %q COUNT port %q must be SemLevelStatus", c.Name, p.Reg)
				}
			case PortReset:
				if !r.Access.CanWrite() || r.Sem&SemStrobe == 0 {
					add("channel %q RESET port %q must be a write strobe", c.Name, p.Reg)
				}
			default:
				add("channel %q: unknown port role %d on %q", c.Name, p.Role, p.Reg)
			}
		}
	}

	// build-ID registers present + readable.
	for _, nm := range []string{i.BuildIDLo, i.BuildIDHi} {
		if nm == "" {
			add("build-ID register name unset")
			continue
		}
		r, ok := i.findReg(nm)
		if !ok {
			add("build-ID register %q not found", nm)
		} else if !r.Access.CanRead() {
			add("build-ID register %q must be readable", nm)
		}
	}

	return errs
}

func fieldMask(hi, lo uint) uint16 {
	var m uint16
	for b := lo; b <= hi; b++ {
		m |= 1 << b
	}
	return m
}

// Mask returns the 16-bit mask of a field.
func (f Field) Mask() uint16 { return fieldMask(f.Hi, f.Lo) }

// canonical writes a deterministic, order-stable dump of the interface. It is the
// basis of the build-ID hash: any semantically meaningful change moves the hash,
// so a mismatched fabric/app pair is caught by the build-ID handshake.
func (i Interface) canonical() string {
	var b strings.Builder
	fmt.Fprintf(&b, "iface %s v%s\n", i.Name, i.Version)
	fmt.Fprintf(&b, "stream lane=%d stages=%d\n", i.Stream.SampleLaneBits, i.Stream.TransformStages)
	fmt.Fprintf(&b, "capture depth=%d addr=%d margin=%d pretrig=%v mark=%v\n",
		i.Capture.RecordDepth, i.Capture.AddrBits, i.Capture.Margin, i.Capture.PreTrigProgrammable, i.Capture.TrigMark)
	for _, bl := range i.Blocks {
		fmt.Fprintf(&b, "block %s %s %#x+%#x\n", bl.Name, bl.Plane, bl.Base, bl.Span)
		for _, r := range bl.Regs {
			exp := "-"
			if r.Expect != nil {
				exp = fmt.Sprintf("%#04x", *r.Expect)
			}
			fmt.Fprintf(&b, " reg %s %s %#04x %s sem=%#x exp=%s\n", r.Name, r.Plane, r.Sel, r.Access, uint16(r.Sem), exp)
			for _, f := range r.Fields {
				fmt.Fprintf(&b, "  field %s [%d:%d]\n", f.Name, f.Hi, f.Lo)
			}
		}
	}
	for _, c := range i.Channels {
		fmt.Fprintf(&b, "chan %s %d\n", c.Name, c.RecordBits)
		for _, f := range c.Fields {
			fmt.Fprintf(&b, "  rf %s %d over=%v\n", f.Name, f.Bits, f.Overflow)
		}
		for _, p := range c.Ports {
			fmt.Fprintf(&b, "  port %s %s\n", p.Role, p.Reg)
		}
	}
	fmt.Fprintf(&b, "desc %s\n", i.Descriptor.Name)
	for _, f := range i.Descriptor.Fields {
		fmt.Fprintf(&b, "  df %s %d\n", f.Name, f.Bits)
	}
	return b.String()
}

// BuildID is a 32-bit fingerprint of the schema (truncated SHA-256 of the
// canonical dump). Both fabric and app compile this in; a mismatch means a
// mispaired build. Deterministic across machines.
func (i Interface) BuildID() uint32 {
	sum := sha256.Sum256([]byte(i.canonical()))
	return binary.BigEndian.Uint32(sum[:4])
}
