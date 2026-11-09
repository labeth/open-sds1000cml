// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/labeth/open-sds1000cml/app/internal/bus"
)

// The MAX V's CS3 registers the panel port writes: the lamp latch, the
// vertical offset DACs and the trigger comparator DAC quad.
const (
	cs3LEDLo     = 0x09
	cs3LEDHi     = 0x0a
	cs3LEDStrobe = 0x0b
	cs3OffC1Lo   = 0x10
	cs3OffC1Hi   = 0x30
	cs3OffC2Lo   = 0x11
	cs3OffC2Hi   = 0x31
	cs3LevelALo  = 0x14
	cs3LevelAHi  = 0x34
	cs3LevelBLo  = 0x15
	cs3LevelBHi  = 0x35
)

// panelPort stages the CS3 words other goroutines set (the lamp word, the
// offset DACs, the trigger level DAC) and writes them on the owner: at a
// commit point (Service) all of them, between the bursts of a long read the
// lamps only, so a key press lights its lamp at once while a deep record is
// read and nothing else moves mid-read.
type panelPort struct {
	bus bus.Bus

	mu       sync.Mutex
	ledWord  uint16
	ledDirty bool
	ledInit  bool
	ledAt    time.Time // when the pending lamp word was staged
	offCode  [2]uint16
	offDirty [2]bool
	lvlCode  uint16
	lvlDirty bool

	// scaleGen counts what moves a channel's scale: an offset staged and
	// written, a relay or gain write (Board.writeFrontEnd). A roll drops
	// the data captured across one.
	scaleGen atomic.Uint64

	// lampLag hears how long a lamp word waited for its flush.
	lampLag func(time.Duration)
	// writeErr hears a failed write.
	writeErr func(error)
}

// stageLamps stages the lamp latch word: compare-on-change, the first word
// always.
// TRLC-LINKS: REQ-SDS-001
func (p *panelPort) stageLamps(word uint16) {
	p.mu.Lock()
	if !p.ledInit || word != p.ledWord {
		if !p.ledDirty {
			p.ledAt = time.Now()
		}
		p.ledWord, p.ledDirty, p.ledInit = word, true, true
	}
	p.mu.Unlock()
}

// stageOffset stages channel ch's offset DAC code: last write wins, no
// compare (the producer suppresses redundant writes).
// TRLC-LINKS: REQ-SDS-001
func (p *panelPort) stageOffset(ch int, code uint16) {
	p.mu.Lock()
	p.offCode[ch&1], p.offDirty[ch&1] = code, true
	p.mu.Unlock()
	p.scaleGen.Add(1)
}

// stageLevel stages the trigger comparator DAC code.
// TRLC-LINKS: REQ-SDS-001
func (p *panelPort) stageLevel(code uint16) {
	p.mu.Lock()
	p.lvlCode, p.lvlDirty = code, true
	p.mu.Unlock()
}

// TRLC-LINKS: REQ-SDS-001
func (p *panelPort) w3(sel, val uint16) {
	if err := p.bus.Write(bus.PlaneCS3, sel, val); err != nil && p.writeErr != nil {
		p.writeErr(err)
	}
}

// flushLamps writes a staged lamp word: the latch strobe is one indivisible
// four-write burst, never interleaved with another CS3 write.
// TRLC-LINKS: REQ-SDS-001
func (p *panelPort) flushLamps() {
	p.mu.Lock()
	dirty, word, at := p.ledDirty, p.ledWord, p.ledAt
	p.ledDirty = false
	p.mu.Unlock()
	if !dirty {
		return
	}
	if p.lampLag != nil {
		p.lampLag(time.Since(at))
	}
	p.w3(cs3LEDStrobe, 0)
	p.w3(cs3LEDHi, word>>8)
	p.w3(cs3LEDLo, word&0xff)
	p.w3(cs3LEDStrobe, 1)
}

// flush writes everything staged, in order: the lamps, the offset DACs (low
// byte, then the self-latching high byte), then the trigger level quad (both
// lanes the same code, high bytes self-latch). It reports whether a level
// was written, and which.
// TRLC-LINKS: REQ-SDS-001
func (p *panelPort) flush() (level bool, code uint16) {
	p.mu.Lock()
	level, code = p.lvlDirty, p.lvlCode
	off, offDirty := p.offCode, p.offDirty
	p.lvlDirty = false
	p.offDirty = [2]bool{}
	p.mu.Unlock()
	p.flushLamps()
	if offDirty[0] || offDirty[1] {
		p.scaleGen.Add(1)
	}
	if offDirty[0] {
		p.w3(cs3OffC1Lo, off[0]&0xff)
		p.w3(cs3OffC1Hi, off[0]>>8)
	}
	if offDirty[1] {
		p.w3(cs3OffC2Lo, off[1]&0xff)
		p.w3(cs3OffC2Hi, off[1]>>8)
	}
	if level {
		lo, hi := code&0xff, code>>8
		p.w3(cs3LevelALo, lo)
		p.w3(cs3LevelAHi, hi)
		p.w3(cs3LevelBLo, lo)
		p.w3(cs3LevelBHi, hi)
	}
	return level, code
}
