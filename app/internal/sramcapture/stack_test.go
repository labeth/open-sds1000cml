// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE

package sramcapture

import (
	"context"
	"fmt"
	"testing"

	"github.com/labeth/open-sds1000cml/app/internal/superres"
)

// stackPortBus emulates stack_engine_port.v over a frozen revision-8 record.
// Each scan adds hitsPerScan hits of value (global bin + 1) to every tile bin.
type stackPortBus struct {
	regs                map[uint16]uint16
	grant               bool
	opError             bool
	mailbox             [32]uint16
	mailIndex           int
	tile                [32][2][superres.FPGAStackWords]uint16
	hits, crossings     uint32
	hitsPerScan         uint32
	scans, uploads      int
	failScan            bool
	template            []uint16
	templateIndex       int
	failOnScan          int
	grantAtFailure      bool
	scannedWithoutGrant bool
}

// TRLC-LINKS: REQ-SDS-141
func newStackPortBus() *stackPortBus {
	return &stackPortBus{regs: map[uint16]uint16{}, hitsPerScan: 3}
}

// TRLC-LINKS: REQ-SDS-141
func (f *stackPortBus) Read(plane uint8, s uint16) (uint16, error) {
	if plane != 1 {
		return 0, fmt.Errorf("plane %d", plane)
	}
	switch s {
	case 0:
		return FabricID, nil
	case 1:
		return 4 | 16 | 64, nil
	case 2:
		return 5000, nil
	case 13:
		return 8, nil
	case 14:
		return QualifiedMapID, nil
	case 15:
		return 500, nil
	case 19:
		return 2, nil
	case selStackGrant:
		v := uint16(0)
		if f.grant {
			v = 3
		}
		return v, nil
	case selStackMail:
		v := f.mailbox[f.mailIndex%32]
		f.mailIndex++
		return v, nil
	case selStackOp:
		v := uint16(stackInitialized)
		if f.opError {
			v |= stackError
		}
		if f.grant {
			v |= stackOwned
		}
		return v, nil
	case selStackLevel:
		return StackCapabilityID, nil
	case selStackFlags:
		return 32, nil
	case selStackTemplateCap:
		return 1024, nil
	case selStackRejected:
		return 5, nil
	case selStackRejected + 1:
		return 0, nil
	case selStackFactorCap:
		return 1, nil
	case selStackPre:
		return uint16(f.hits), nil
	case selStackPre + 1:
		return uint16(f.hits >> 16), nil
	case selStackSep:
		return uint16(f.crossings), nil
	case selStackSep + 1:
		return uint16(f.crossings >> 16), nil
	}
	return 0, nil
}

// TRLC-LINKS: REQ-SDS-141
func (f *stackPortBus) RawWrite(s, v uint16) error {
	switch s {
	case selStackGrant:
		f.grant = v&1 != 0
	case selStackMailIndex:
		f.mailIndex = int(v)
	case selStackTemplateIndex:
		f.templateIndex, f.template = int(v), nil
	case selStackTemplateByte:
		f.template = append(f.template, v)
		f.templateIndex++
	case selStackMail:
		f.mailbox[f.mailIndex%32] = v
		f.mailIndex++
	case selStackOp:
		f.opError = false
		tile := f.regs[selStackTile]
		bin, ch := int(tile>>1), int(tile&1)
		switch v {
		case stackOpReset:
			f.tile = [32][2][superres.FPGAStackWords]uint16{}
		case stackOpTileWrite:
			copy(f.tile[bin][ch][:], f.mailbox[:superres.FPGAStackWords])
			f.uploads++
		case stackOpTileRead:
			copy(f.mailbox[:], f.tile[bin][ch][:])
		case stackOpScan:
			f.scans++
			if !f.grant {
				f.scannedWithoutGrant = true
			}
			if f.failScan || f.scans == f.failOnScan {
				f.opError, f.grantAtFailure = true, f.grant
				return nil
			}
			initial := uint32(f.regs[selStackInitial]) | uint32(f.regs[selStackInitial+1])<<16
			first := int(f.regs[selStackFirstBin]) | int(f.regs[selStackFirstBin+1])<<16
			for b := range int(f.regs[selStackBins]) {
				for c := range 2 {
					m, err := superres.DecodeFPGAMoments(f.tile[b][c][:])
					if err != nil {
						return err
					}
					value := uint64(first+b+1) << 24
					for h := range f.hitsPerScan {
						m.Sum[0] += value
						m.SumSquares[0] += value * value >> 24 // bounded test payload
						m.Count++
						if (initial+h)%2 == 0 {
							m.SumOdd[0] += value
							m.CountOdd++
						}
					}
					if f.tile[b][c], err = superres.EncodeFPGAMoments(m); err != nil {
						return err
					}
				}
			}
			f.hits, f.crossings = initial+f.hitsPerScan, f.hitsPerScan+1
		default:
			f.opError = true
		}
	default:
		f.regs[s] = v
	}
	return nil
}

// TRLC-LINKS: REQ-SDS-141
func TestStackTemplateQualifier(t *testing.T) {
	bus := newStackPortBus()
	c, err := New(bus)
	if err != nil {
		t.Fatal(err)
	}
	cfg := StackConfig{
		Level: 128, Bins: 32, Factor: 4, ChannelMask: 3,
		Template: []int{40, 80, 200, 200}, TemplateStride: 3, TemplatePre: 6, TemplateThreshold: 90,
	}
	if _, err := c.StackBegin(cfg); err != nil {
		t.Fatal(err)
	}
	if len(bus.template) != 4 || bus.template[2] != 200 || bus.regs[selStackQualify] != 1 || bus.regs[selStackQualifyCount] != 4 ||
		bus.regs[selStackQualifyStride] != 3 || bus.regs[selStackQualifyPre] != 6 || bus.regs[selStackQualifyThresh] != 90 {
		t.Fatalf("qualifier upload: template %v regs %v", bus.template, bus.regs)
	}
	if _, _, rejected, err := c.StackScan(context.Background(), 0, 32, 0); err != nil || rejected != 5 {
		t.Fatalf("scan rejected %d, %v", rejected, err)
	}
	for _, bad := range []StackConfig{
		{Bins: 8, Factor: 1, ChannelMask: 3, Template: []int{1, 2}, TemplateStride: 0},
		{Bins: 8, Factor: 1, ChannelMask: 3, Template: []int{1, 300}, TemplateStride: 1},
		{Bins: 8, Factor: 1, ChannelMask: 3, Template: make([]int, 1025), TemplateStride: 1},
	} {
		if _, err := c.StackBegin(bad); err == nil {
			t.Fatalf("accepted %+v", bad.Template[:2])
		}
	}
	cfg.Template = nil
	if _, err := c.StackBegin(cfg); err != nil || bus.regs[selStackQualify] != 0 {
		t.Fatalf("template-less config left the qualifier on: %v", err)
	}
}
