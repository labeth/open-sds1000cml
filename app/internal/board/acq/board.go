// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

// Package acq is the SDS1000CML+'s acquisition board: the SRAM capture
// fabric, its FPGA images, the MAX V's panel and DAC words, behind the HAL
// (open-dso-core/hal) the engine's owner loop drives.
//
// Setters stage; Acquirer.Arm commits the staged capture (plan and trigger
// in one sramcapture.Config, the fabric's single arm write). Every method
// that touches the bus runs on the owner goroutine; the lamp word, offset
// and level DACs that other goroutines set are staged and written at the
// owner's commit points (Platform.Service) and, the lamps only, between the
// bursts of a long read.
package acq

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/bus"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// Board is the SDS1000CML+ behind the HAL.
type Board struct {
	mem   Memory // nil without the SRAM fabric (the register-path tests)
	panel panelPort

	profile   hal.Profile
	frontEnd  hal.FrontEnd
	levelGrid func(src int) hal.Grid
	display   hal.Display
	input     hal.Input
	storage   hal.Storage
	handover  hal.Handover

	images      ImageSwitcher
	imageLoaded string // the image in use: "" the general one

	// acquisition state, owner only
	plan     hal.Plan
	planned  planPart // the configured plan's fields; the trigger's are trig.staged
	state    hal.AcqState
	rec      sramcapture.Metadata // the halted record Readout reads
	reading  bool                 // inside a read: Service flushes the lamps only
	yield    func()               // the current read's yield
	progress func(done, total int)
	trig     trigState
	scratch  readScratch
	stream   streamState

	diagMu sync.Mutex
	diag   boardDiag
}

// boardDiag is the board's counters for the status "diag" object.
type boardDiag struct {
	Arms     uint64 `json:"arms"`
	Forces   uint64 `json:"forces"`
	Halts    uint64 `json:"halts"`
	Reads    uint64 `json:"reads"`
	ReadErrs uint64 `json:"read_errors"`
	Image    string `json:"image"`
}

// Config is what the board is built from.
type Config struct {
	Memory Memory  // the SRAM fabric; nil: none
	Panel  bus.Bus // the CS3 plane: lamps, offset and level DACs
	// LampLag hears how long a lamp word waited for its flush; WriteErr a
	// failed panel write.
	LampLag  func(time.Duration)
	WriteErr func(error)
}

// New is the board over cfg's fabric and panel plane.
// TRLC-LINKS: REQ-SDS-001
func New(cfg Config) *Board {
	b := &Board{mem: cfg.Memory}
	b.panel.bus, b.panel.lampLag, b.panel.writeErr = cfg.Panel, cfg.LampLag, cfg.WriteErr
	return b
}

var _ hal.Board = (*Board)(nil)

// SetReporting attaches what the owner hears of the panel plane: how long
// a lamp word waited for its flush, and a failed write.
// TRLC-LINKS: REQ-SDS-001
func (b *Board) SetReporting(lampLag func(time.Duration), writeErr func(error)) {
	b.panel.lampLag, b.panel.writeErr = lampLag, writeErr
}

// HasMemory reports whether the SRAM fabric is there.
// TRLC-LINKS: REQ-SDS-001
func (b *Board) HasMemory() bool { return b.mem != nil }

// Memory is the fabric's port, for the paths the HAL does not cover yet
// (decoded capture, the stacking scan, the panel chain, the drum streams).
// TRLC-LINKS: REQ-SDS-001
func (b *Board) Memory() Memory { return b.mem }

// SetProfile attaches the scope's profile. It, SetFrontEnd, SetLevelGrid,
// SetDevices and SetHandover attach the parts the composition builds outside
// the acquisition.
// TRLC-LINKS: REQ-SDS-001
func (b *Board) SetProfile(p hal.Profile) { b.profile = p }

// SetFrontEnd attaches the analog front end.
// TRLC-LINKS: REQ-SDS-001
func (b *Board) SetFrontEnd(fe hal.FrontEnd) { b.frontEnd = fe }

// SetLevelGrid attaches the trigger level grid of each source.
// TRLC-LINKS: REQ-SDS-001
func (b *Board) SetLevelGrid(g func(src int) hal.Grid) { b.levelGrid = g }

// SetHandover attaches what the supervisor gave this process.
// TRLC-LINKS: REQ-SDS-001
func (b *Board) SetHandover(h hal.Handover) { b.handover = h }

// SetDevices attaches the display, the input and the storage.
// TRLC-LINKS: REQ-SDS-001
func (b *Board) SetDevices(d hal.Display, in hal.Input, st hal.Storage) {
	b.display, b.input, b.storage = d, in, st
}

// Profile is the unit's capabilities.
// TRLC-LINKS: REQ-SDS-001
func (b *Board) Profile() hal.Profile { return b.profile }

// Platform is bring-up, the hand-over and the owner's housekeeping.
// TRLC-LINKS: REQ-SDS-001
func (b *Board) Platform() hal.Platform { return (*platformFacet)(b) }

// Planner is the SRAM capture plan arithmetic.
// TRLC-LINKS: REQ-SDS-001
func (b *Board) Planner() hal.Planner { return (*plannerFacet)(b) }

// Acquirer arms and serves captures.
// TRLC-LINKS: REQ-SDS-001
func (b *Board) Acquirer() hal.Acquirer { return (*acquirerFacet)(b) }

// Readout reads windows of the halted record.
// TRLC-LINKS: REQ-SDS-001
func (b *Board) Readout() hal.Readout { return (*readoutFacet)(b) }

// Trigger stages the fabric's trigger.
// TRLC-LINKS: REQ-SDS-001
func (b *Board) Trigger() hal.Trigger { return (*triggerFacet)(b) }

// FrontEnd is the analog front end the composition attached.
// TRLC-LINKS: REQ-SDS-001
func (b *Board) FrontEnd() hal.FrontEnd { return b.frontEnd }

// Display is the display the composition attached.
// TRLC-LINKS: REQ-SDS-001
func (b *Board) Display() hal.Display { return b.display }

// Input is the panel input the composition attached.
// TRLC-LINKS: REQ-SDS-001
func (b *Board) Input() hal.Input { return b.input }

// Storage is the storage the composition attached.
// TRLC-LINKS: REQ-SDS-001
func (b *Board) Storage() hal.Storage { return b.storage }

// Streamer is the fabric's stream port, when the image has one.
// TRLC-LINKS: REQ-SDS-001
func (b *Board) Streamer() (hal.Streamer, bool) {
	if b.mem == nil {
		return nil, false
	}
	return (*streamerFacet)(b), true
}

// Images is the FPGA image set, when the composition attached a loader.
// TRLC-LINKS: REQ-SDS-001
func (b *Board) Images() (hal.Images, bool) {
	if b.images == nil {
		return nil, false
	}
	return (*imagesFacet)(b), true
}

// Stacker is the stacking image's accumulator, when it is fitted.
// TRLC-LINKS: REQ-SDS-001
func (b *Board) Stacker() (hal.Stacker, bool) {
	if b.mem == nil || b.images == nil || !b.images.HasStack() {
		return nil, false
	}
	return (*stackerFacet)(b), true
}

// Segments reports no segmented memory: the SDS1000CML+ has none, nor a
// counter, a logic probe or a generator.
// TRLC-LINKS: REQ-SDS-001
func (b *Board) Segments() (hal.Segments, bool) { return nil, false }

// Counter reports no hardware frequency counter.
// TRLC-LINKS: REQ-SDS-001
func (b *Board) Counter() (hal.Counter, bool) { return nil, false }

// LogicProbe reports no logic probe.
// TRLC-LINKS: REQ-SDS-001
func (b *Board) LogicProbe() (hal.LogicProbe, bool) { return nil, false }

// Generator reports no waveform generator.
// TRLC-LINKS: REQ-SDS-001
func (b *Board) Generator() (hal.Generator, bool) { return nil, false }

// Extensions are served by the composition's web adapter until the web
// server moves to the core.
// TRLC-LINKS: REQ-SDS-001
func (b *Board) Extensions() []hal.Extension { return nil }

// TRLC-LINKS: REQ-SDS-001
func (b *Board) count(f func(*boardDiag)) {
	b.diagMu.Lock()
	f(&b.diag)
	b.diagMu.Unlock()
}

// TRLC-LINKS: REQ-SDS-001
func (b *Board) diagJSON() json.RawMessage {
	b.diagMu.Lock()
	d := b.diag
	b.diagMu.Unlock()
	d.Image = imageName(b.imageLoaded)
	out, _ := json.Marshal(d)
	return out
}

// recordID is the record's identity as the frames carry it.
// TRLC-LINKS: REQ-SDS-001
func recordID(m sramcapture.Metadata) sample.RecordID {
	return sample.RecordID{Epoch: m.EventEpoch, Seq: m.RecordID}
}
