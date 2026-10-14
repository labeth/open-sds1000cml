// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel"
	"open-sds/app/internal/panel/framecalc"
)

// decodePanel is the scope's decoder settings and its Auto, for the browser
// tests: the BUS sheet rows a page sets (/api/sheets/set), Auto detecting the
// bus once on the frame the fake serves and filling the rows in, and the
// status's panel Display reporting them, as the panel controller does.
type decodePanel struct {
	Panel // the keys, when the test's scope has a panel double of its own
	sc    Scope
	mu    sync.Mutex
	d     panel.Display
	seq   uint64
	sets  []string // the rows set, in order
}

// newDecodePanel is decode off, C1 then C2, 115200 Bd.
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-222
func newDecodePanel(sc Scope, keys Panel) *decodePanel {
	if keys == nil {
		keys = noKeys{}
	}
	return &decodePanel{Panel: keys, sc: sc, d: panel.Display{DecChB: 1, DecBaud: 115200}}
}

// noKeys is a panel without keys.
type noKeys struct{}

// TRLC-LINKS: REQ-SDS-214, REQ-SDS-222
func (noKeys) InjectButton(string) bool { return false }

// TRLC-LINKS: REQ-SDS-214, REQ-SDS-222
func (noKeys) InjectKnob(string, int, int) bool { return false }

// TRLC-LINKS: REQ-SDS-214, REQ-SDS-222
func (p *decodePanel) PanelStatus() panel.Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	return panel.Status{Seq: p.seq, SheetsSeq: p.seq, Display: p.d}
}

// TRLC-LINKS: REQ-SDS-214, REQ-SDS-222
func (p *decodePanel) WaitPanel(uint64, time.Duration) panel.Status { return p.PanelStatus() }

// TRLC-LINKS: REQ-SDS-214, REQ-SDS-222
func (p *decodePanel) Sheets() panel.SheetsModel { return panel.SheetsModel{} }

// TRLC-LINKS: REQ-SDS-214, REQ-SDS-222
func (p *decodePanel) SheetAction(row, _ string) panel.SetResult {
	return panel.SetResult{Row: row, Error: "no action"}
}

// TRLC-LINKS: REQ-SDS-214, REQ-SDS-222
func (p *decodePanel) TypeText(string) panel.SetResult { return panel.SetResult{Error: "no entry"} }

// Sets is the rows the page set, in order.
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-222
func (p *decodePanel) Sets() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.sets...)
}

// SheetSet sets a BUS row; Protocol = Auto (1) detects the bus and fills the
// rows in.
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-222
func (p *decodePanel) SheetSet(row string, value any, _ string) panel.SetResult {
	v, ok := value.(float64)
	if !ok {
		return panel.SetResult{Row: row, Error: "want a number"}
	}
	n := int(v)
	if row == "bus.protocol" && n == framecalc.ProtoAuto {
		p.detect()
		return panel.SetResult{OK: true, Row: row}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	d := &p.d
	switch row {
	case "bus.protocol":
		d.DecProto = n
		if n == framecalc.ProtoI2C || n == framecalc.ProtoSPI {
			d.DecChB = 1 - d.DecChA
		}
	case "bus.source", "bus.scl", "bus.clk":
		d.DecChA, d.DecChB = n&1, 1-n&1
	case "bus.sda", "bus.data":
		d.DecChB, d.DecChA = n&1, 1-n&1
	case "bus.baud", "bus.bit_rate":
		d.DecBaud = n
	case "bus.spi_mode":
		d.DecCPOL, d.DecCPHA = n&2 != 0, n&1 != 0
	case "bus.bit_order":
		d.DecLSB = n == 1
	default:
		return panel.SetResult{Row: row, Error: "no row " + row}
	}
	d.DecAuto = false
	p.sets = append(p.sets, fmt.Sprintf("%s=%d", row, n))
	p.seq++
	return panel.SetResult{OK: true, Row: row}
}

// detect is Auto: the bus on the frame served, filled in.
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-222
func (p *decodePanel) detect() {
	var res framecalc.DecodeParams
	var note string
	found := false
	p.sc.WithFrame(func(f *engine.Frame) {
		r := framecalc.Detect(f, "hex")
		proto := map[string]int{"uart": framecalc.ProtoUART, "i2c": framecalc.ProtoI2C, "spi": framecalc.ProtoSPI,
			"manchester": framecalc.ProtoManchester, "sent": framecalc.ProtoSENT, "canfd": framecalc.ProtoCAN,
			"mil1553": framecalc.ProtoMIL1553, "arinc429": framecalc.ProtoARINC429, "usbls": framecalc.ProtoUSB,
			"flexray": framecalc.ProtoFlexRay}[r.Proto]
		if !r.OK || proto == 0 {
			return
		}
		found = true
		res = framecalc.DecodeParams{Proto: proto, Baud: r.Baud, ChA: r.Roles.ChA & 1, ChB: 1 - r.Roles.ChA&1,
			CPOL: r.Roles.CPOL, CPHA: r.Roles.CPHA, LSB: !r.Roles.MSB}
		if proto == framecalc.ProtoI2C || proto == framecalc.ProtoSPI {
			res.ChB = r.Roles.ChB & 1
		}
		note = fmt.Sprintf("%s %d on C%d", strings.ToUpper(r.Proto), r.Baud, res.ChA+1)
	})
	p.mu.Lock()
	defer p.mu.Unlock()
	d := &p.d
	if found {
		d.DecProto, d.DecChA, d.DecChB, d.DecAuto = res.Proto, res.ChA, res.ChB, true
		if res.Baud > 0 {
			d.DecBaud = res.Baud
		}
		if res.Proto == framecalc.ProtoSPI {
			d.DecCPOL, d.DecCPHA, d.DecLSB = res.CPOL, res.CPHA, res.LSB
		}
		d.DecNote = note
	} else {
		d.DecNote = "no bus found on CH1 or CH2 - decode stays off"
	}
	p.sets = append(p.sets, "bus.protocol=1")
	p.seq++
}
