// ENGMODEL-OWNER-UNIT: FU-APP-ACQSRAM
package main

import (
	"open-sds/app/internal/bus"
	"time"
)

// Profile the transport separately from SRAM command waits and the sink.
// This wrapper is used only by the explicit profiling command.
// TRLC-LINKS: REQ-SDS-170
type profileBus struct {
	dev                            *bus.Dev
	Reads, Writes, Pops, Halfwords uint64
	SkippedWords                   uint64
	ReadNS, WriteNS, PopNS         int64
	count                          uint32
}

// TRLC-LINKS: REQ-SDS-170
func (p *profileBus) Read(plane uint8, selector uint16) (uint16, error) {
	t := time.Now()
	v, err := p.dev.Read(plane, selector)
	p.ReadNS += time.Since(t).Nanoseconds()
	p.Reads++
	return v, err
}
// TRLC-LINKS: REQ-SDS-170
func (p *profileBus) RawWrite(selector, value uint16) error {
	t := time.Now()
	err := p.dev.RawWrite(selector, value)
	p.WriteNS += time.Since(t).Nanoseconds()
	p.Writes++
	if err == nil {
		switch selector {
		case 8:
			p.count = p.count&0xffff0000 | uint32(value)
		case 9:
			p.count = p.count&0xffff | uint32(value)<<16
		case 1:
			if value == 3 {
				p.SkippedWords += uint64(p.count)
			}
		}
	}
	return err
}
// TRLC-LINKS: REQ-SDS-170
func (p *profileBus) PopWordsChecked(selector uint16, dst []uint16) error {
	t := time.Now()
	err := p.dev.PopWordsChecked(selector, dst)
	p.PopNS += time.Since(t).Nanoseconds()
	p.Pops++
	p.Halfwords += uint64(len(dst))
	return err
}

// TRLC-LINKS: REQ-SDS-170
func (p *profileBus) PopBytesChecked(selector uint16, dst []byte) error {
	t := time.Now()
	err := p.dev.PopBytesChecked(selector, dst)
	p.PopNS += time.Since(t).Nanoseconds()
	p.Pops++
	p.Halfwords += uint64(len(dst) / 2)
	return err
}
