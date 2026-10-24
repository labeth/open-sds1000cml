// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import "fmt"

// Symbol trigger bit sources (ADR-SYMBOL-TRIGGER, the qualify image's
// symbol_trigger.v).
const (
	SymbolClass   = 0 // bits are the runs' data codes (pulse-width codes)
	SymbolLevel   = 1 // the run line's level each unit (UART, Manchester)
	SymbolEdge    = 2 // an edge in each unit or not (biphase mark)
	SymbolClocked = 3 // line B at the run line's clock edges
)

// SymbolTableSize is the run table's entries: index {level, e[3:0], m[5:0]}
// of a run's prescaled tick count (decode.SymbolIndex).
const SymbolTableSize = 2048

// SymbolClockNs is the symbol trigger's clock (the 125 MHz receiver clock).
const SymbolClockNs = 8

// SymbolFireClocks is how many receiver clocks after the edge or unit
// sample that completes a match the block's match comes; the trigger follows
// it through a synchronizer to the acquisition clock.
const SymbolFireClocks = 7

// SymbolTriggerConfig is the qualify image's symbol trigger: two sliced
// lines (A on Channel at the trigger level, B on the other at SecondLevel),
// a run table, a bit source and a masked 64-bit window compared at bit K of
// a frame (and every Period bits after). The engine compiles each protocol
// into it (engine/serial_symbol_hardware.go).
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
type SymbolTriggerConfig struct {
	Enabled    bool   `json:"enabled"`
	Channel    uint8  `json:"channel"`
	InvA       bool   `json:"inv_a,omitempty"`
	InvB       bool   `json:"inv_b,omitempty"`
	AndAB      bool   `json:"and_ab,omitempty"`
	ClockFall  bool   `json:"clock_fall,omitempty"`
	TwoLines   bool   `json:"two_lines,omitempty"` // line B is used: register 59 holds its level
	Mode       uint8  `json:"mode"`
	LeadLen    uint8  `json:"lead_len"`
	Prescale   uint8  `json:"prescale"`
	UnitQ8     uint32 `json:"unit_q8"`
	FrameUnits uint8  `json:"frame_units,omitempty"`
	Period     uint8  `json:"period,omitempty"`
	Phase0     uint8  `json:"phase0,omitempty"`
	K          uint16 `json:"k"`
	Pattern    uint64 `json:"pattern"`
	Mask       uint64 `json:"mask"`
	// Table is the run table, {leader code[7:4], data code[3:0]} an entry.
	Table [SymbolTableSize]uint8 `json:"-"`
}

// Validate refuses a configuration the block cannot run (a unit under
// eight receiver clocks among them): the planner checks its plans with it,
// so a request the block refuses is unplannable rather than an arm that
// fails on every capture.
// TRLC-LINKS: REQ-SDS-013
func (p SymbolTriggerConfig) Validate() error { return p.validate() }

// TRLC-LINKS: REQ-SDS-013
func (p SymbolTriggerConfig) validate() error {
	if !p.Enabled {
		return nil
	}
	if p.Channel > 1 || p.Mode > SymbolClocked || p.LeadLen > 4 || p.Prescale > 7 || p.UnitQ8 < 2048 || p.UnitQ8 >= 1<<28 ||
		p.FrameUnits > 63 || p.Period > 63 || p.Phase0 > 63 || (p.Period != 0 && p.Phase0 >= p.Period) || p.K > 1023 {
		return fmt.Errorf("sramcapture: invalid hardware symbol trigger configuration")
	}
	return nil
}

// The symbol block's registers (qualify image): read 76 the block, 78 its
// matches, 98 the table flag; write 93..108.
const (
	symbolCapability = 76
	symbolMatches    = 78
	symbolLoaded     = 98
	symbolBlock      = 0x5359
)

// SupportsSymbol reports the symbol trigger block (the qualify image).
// TRLC-LINKS: REQ-SDS-013
func (c *Capture) SupportsSymbol() (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, err := c.read(symbolCapability)
	return v == symbolBlock && err == nil, err
}

// SymbolMatches reads the block's match count (modulo 2^16): every match,
// whether or not it triggered a record.
// TRLC-LINKS: REQ-SDS-013
func (c *Capture) SymbolMatches() (uint16, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.read(symbolMatches)
}

// configureSymbol writes the symbol block, or disables it; a fabric without
// it refuses an enabled one. The table is written only when the fabric's
// flag says it holds none (a load clears it) or another table is asked for.
// TRLC-LINKS: REQ-SDS-013
func (c *Capture) configureSymbol(revision uint16, p SymbolTriggerConfig) error {
	var capability uint16
	if revision >= 10 {
		v, err := c.read(symbolCapability)
		if err != nil {
			return err
		}
		capability = v
	}
	if capability != symbolBlock {
		if p.Enabled {
			return fmt.Errorf("sramcapture: fabric lacks the hardware symbol trigger")
		}
		return nil
	}
	if !p.Enabled {
		return c.write(93, 0)
	}
	if err := c.write(93, 0); err != nil {
		return err
	}
	loaded, err := c.read(symbolLoaded)
	if err != nil {
		return err
	}
	if loaded&1 == 0 || !c.symbolTableOK || c.symbolTable != p.Table {
		c.symbolTableOK = false
		// The table load in one batch (the worker splits it into packets).
		regs := make([][2]uint16, 0, len(p.Table)+3)
		regs = append(regs, [2]uint16{98, 0}, [2]uint16{99, 0})
		for _, v := range p.Table {
			regs = append(regs, [2]uint16{100, uint16(v)})
		}
		if err := c.writeMany(append(regs, [2]uint16{98, 1})...); err != nil {
			return err
		}
		c.symbolTable, c.symbolTableOK = p.Table, true
	}
	return c.writeMany(SymbolRegisters(p)...)
}

// SymbolRegisters is the block's register writes for p after its table, the
// control word (which enables it) last.
// TRLC-LINKS: REQ-SDS-013
func SymbolRegisters(p SymbolTriggerConfig) [][2]uint16 {
	control := 1 | uint16(p.Channel)<<1 | uint16(p.Mode)<<6 | uint16(p.LeadLen)<<8 | uint16(p.Prescale)<<11
	for _, f := range []struct {
		on  bool
		bit uint16
	}{{p.InvA, 4}, {p.InvB, 8}, {p.AndAB, 16}, {p.ClockFall, 32}} {
		if f.on {
			control |= f.bit
		}
	}
	return [][2]uint16{
		{94, uint16(p.UnitQ8)}, {95, uint16(p.UnitQ8 >> 16)},
		{96, uint16(p.Period)<<8 | uint16(p.FrameUnits)}, {97, uint16(p.Phase0)<<10 | p.K},
		{101, uint16(p.Pattern)}, {102, uint16(p.Pattern >> 16)}, {103, uint16(p.Pattern >> 32)}, {104, uint16(p.Pattern >> 48)},
		{105, uint16(p.Mask)}, {106, uint16(p.Mask >> 16)}, {107, uint16(p.Mask >> 32)}, {108, uint16(p.Mask >> 48)},
		{93, control},
	}
}
