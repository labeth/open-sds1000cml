// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"math"
	"strings"
	"time"

	"open-sds/app/internal/analysis"
	"open-sds/app/internal/decode"
	"open-sds/app/internal/panel/framecalc"
	"open-sds/app/internal/panel/wsview"
)

// The bus's layer (REQ-SDS-241): the application protocol or chip the BUS
// sheet's Layer row names reads the decoded packets and notes each one. AUTO
// in BUS and the protocol row's Auto choose a layer only on its evidence
// (REQ-SDS-247); the Match bytes row's From layer sets the hardware trigger's
// match to the selected packet's transaction (REQ-SDS-248).

// layerIndex is a layer's place in analysis.Layers (0: none, or unknown).
// TRLC-LINKS: REQ-SDS-241
func layerIndex(id string) int {
	for i, l := range analysis.Layers() {
		if l.ID == id {
			return i
		}
	}
	return 0
}

// layerArgs are the layer's settings.
// TRLC-LINKS: REQ-SDS-241
func layerArgs(s *sv) analysis.LayerArgs {
	return analysis.LayerArgs{Addr: s.decLayerAddr, Dir: s.decLayerDir}
}

// dropLayerLocked forgets a layer the protocol in use does not carry
// (c.mu held).
// TRLC-LINKS: REQ-SDS-241
func (c *Controller) dropLayerLocked() {
	if l, ok := analysis.LayerByID(c.decLayer); !ok || l.Bus != framecalc.ResultName(c.decProto) {
		c.decLayer = ""
	}
}

// setDecLayer chooses the bus's layer. S.BUS fixes the UART's framing: 100
// kbaud, even parity, the line idling low.
// TRLC-LINKS: REQ-SDS-241
func (c *Controller) setDecLayer(id string) error {
	l, ok := analysis.LayerByID(id)
	if !ok {
		return fmt.Errorf("no layer %q", id)
	}
	c.mu.Lock()
	if id != "" && l.Bus != framecalc.ResultName(c.decProto) {
		c.mu.Unlock()
		return fmt.Errorf("%s is a layer of %s: set Protocol first", l.Name, strings.ToUpper(l.Bus))
	}
	c.decLayer = id
	framing := id == "sbus"
	if framing {
		c.decBaud, c.decParity, c.decInv = 100000, "even", true
	}
	c.mu.Unlock()
	c.resetBusList()
	if framing {
		c.syncSerialBus()
	}
	return nil
}

// resetBusList starts the packet list over: its rows hold the old notes.
// TRLC-LINKS: REQ-SDS-241
func (c *Controller) resetBusList() {
	c.amu.Lock()
	c.ana.list.Reset()
	c.ana.listKey = ""
	c.amu.Unlock()
}

// layerPackets are a capture's packets as the layer reads them: noted, and
// for the I2C filter, its transfers only.
// TRLC-LINKS: REQ-SDS-241
func layerPackets(s *sv, proto string, ps []Packet) []Packet {
	if s.decLayer == "" {
		return ps
	}
	return analysis.ApplyLayer(s.decLayer, proto, ps, layerArgs(s))
}

// layerRows are the BUS sheet's Layer row and the I2C filter's address and
// direction.
// TRLC-LINKS: REQ-SDS-241
func layerRows() []*row {
	decOn := func(s *sv) bool {
		return s.decProto != framecalc.ProtoOff && len(analysis.LayersOn(framecalc.ResultName(s.decProto))) > 1
	}
	layer := &row{id: "bus.layer", label: "Layer", kind: wsview.Enum, hdr: "LAYER",
		help: "The chip or protocol the bus carries: the packet list, the strip and the web table name each transaction.",
		knob: "AUTO in BUS chooses one when the packets prove it (an SPI flash's commands, EEPROM reads, an EDID header).",
		rel:  decOn, why: "a Protocol with layers (SPI, I2C, UART, 1-Wire, MDIO, USB)",
		opts: func(s *sv) []opt {
			var o []opt
			ls := analysis.LayersOn(framecalc.ResultName(s.decProto))
			if len(ls) < 2 { // a bus without layers: what the others carry (choosing one asks for its protocol)
				ls = analysis.Layers()
			}
			for _, l := range ls {
				name := l.Name
				if l.Line != "" {
					name += " (" + l.Line + ")"
				}
				o = append(o, opt{layerIndex(l.ID), name})
			}
			return o
		},
		get: func(s *sv) int { return layerIndex(s.decLayer) },
		set: func(c *Controller, v int) error {
			ls := analysis.Layers()
			if v < 0 || v >= len(ls) {
				return fmt.Errorf("no layer %d", v)
			}
			return c.setDecLayer(ls[v].ID)
		},
		def: func(c *Controller) error { return c.setDecLayer("") },
	}
	isFilter := func(s *sv) bool { return s.decLayer == "i2cfilter" && s.decProto == framecalc.ProtoI2C }
	addr := numRow("bus.layer_address", "Filter address", "", func(s *sv) float64 { return float64(s.decLayerAddr) },
		constLim(-1, 127), constStep(1), func(c *Controller, v float64) error {
			c.mu.Lock()
			c.decLayerAddr = clampInt(int(math.Round(v)), -1, 127)
			c.mu.Unlock()
			c.resetBusList()
			return nil
		}, func(v float64) string {
			if v == -1 {
				return "Any"
			}
			return hexText(v)
		})
	addr.help = "The I2C address the Address filter layer lists (Any: every address)."
	addr.rel, addr.why = isFilter, "Layer = Address filter"
	addr.presets = func(*sv) []preset { return []preset{{t: "Any", v: -1}} }
	addr.def = func(c *Controller) error { return addr.setN(c, -1) }
	dir := &row{id: "bus.layer_direction", label: "Filter direction", kind: wsview.Enum,
		help: "The transfers the Address filter layer lists: reads, writes or both.",
		rel:  isFilter, why: "Layer = Address filter",
		opts: staticOpts([]opt{{0, "Both"}, {1, "Read"}, {2, "Write"}}),
		get:  func(s *sv) int { return s.decLayerDir },
		set: func(c *Controller, v int) error {
			c.mu.Lock()
			c.decLayerDir = clampInt(v, 0, 2)
			c.mu.Unlock()
			c.resetBusList()
			return nil
		},
	}
	dir.def = func(c *Controller) error { return dir.set(c, 0) }
	return []*row{layer, addr, dir}
}

// uartLineRow is the UART's idle level: Normal (high) or Inverted (low, as
// S.BUS sends it).
// TRLC-LINKS: REQ-SDS-244
func uartLineRow() *row {
	r := &row{id: "bus.uart_line", label: "UART line", kind: wsview.Enum,
		help: "Normal: the line idles high; Inverted: it idles low (S.BUS). Auto and the S.BUS layer set it.",
		rel:  func(s *sv) bool { return s.decProto == framecalc.ProtoUART }, why: "Protocol = UART",
		opts: staticOpts([]opt{{0, "Normal"}, {1, "Inverted"}}),
		get:  func(s *sv) int { return b2ic(s.decInv) },
		set: func(c *Controller, v int) error {
			c.mu.Lock()
			c.decInv = v == 1
			c.auto = autoWatch{} // set by hand
			c.mu.Unlock()
			c.syncSerialBus()
			return nil
		},
	}
	r.def = func(c *Controller) error { return r.set(c, 0) }
	return r
}

// selectedPacket is the BUS list's selected packet (false: none).
// TRLC-LINKS: REQ-SDS-248
func (c *Controller) selectedPacket() (Packet, bool) {
	c.amu.Lock()
	defer c.amu.Unlock()
	rows, partial := busRows(&c.ana)
	sel := busSelected(&c.ana, rows, partial)
	if sel < 0 || sel >= len(rows) {
		return Packet{}, false
	}
	return rows[sel], true
}

// matchFromLayer sets the serial trigger's match to the selected packet's
// transaction as its layer names it: the bus's bytes (an SPI flash READ's
// command and address after the burst's start; an EEPROM's word address)
// and, on I2C, the device address. The FPGA's matcher then decides the
// trigger.
// TRLC-LINKS: REQ-SDS-248
func (c *Controller) matchFromLayer() error {
	s := c.snap()
	if s.decLayer == "" {
		return fmt.Errorf("no layer: set the BUS sheet's Layer first")
	}
	p, ok := c.selectedPacket()
	if !ok {
		return fmt.Errorf("no packet selected in BUS")
	}
	toks, addr, ok := analysis.LayerMatch(s.decLayer, p)
	if !ok {
		return fmt.Errorf("this layer offers no match for the packet")
	}
	if err := c.setMatchTokens(toks); err != nil {
		return err
	}
	if s.decProto == framecalc.ProtoI2C {
		if err := c.setI2CAddr(addr); err != nil {
			return err
		}
	}
	what := strings.Join(toks, " ")
	if s.decProto == framecalc.ProtoI2C && addr >= 0 {
		what = strings.TrimSpace(fmt.Sprintf("address %s %s", hexText(float64(addr)), what))
	}
	c.toast("Match from layer: "+what, 1800*time.Millisecond)
	return nil
}

// detectLayer names the layer a detection's packets prove, after Auto filled
// in the bus; the layer chosen by hand stays when nothing proves another.
// It returns what it found for the note ("" none).
// TRLC-LINKS: REQ-SDS-247
func (c *Controller) detectLayer(res decode.Result, valid int) string {
	ps := anaPackets(res, valid)
	id, why := analysis.DetectLayer(res.Proto, ps)
	if id == "" {
		c.mu.Lock()
		c.dropLayerLocked()
		c.mu.Unlock()
		return ""
	}
	if err := c.setDecLayer(id); err != nil {
		return ""
	}
	l, _ := analysis.LayerByID(id)
	return l.Name + " (" + why + ")"
}
