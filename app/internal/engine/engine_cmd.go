package engine

// serviceCommands flushes staged panel/CS3 work at the frame boundary — the
// engine is armed+filling here, never inside a halt window. Snapshot+clear
// under the mutex; bus writes with it released (they sleep in the re-arm).
// Servicing order per spec 09 §4: matrix requests, LED latch, offset DACs,
// then the trigger level.
func (e *Engine) serviceCommands() {
	// Drain every pending matrix request with ONE snapshot (a CS1
	// config-plane read does not pop the sample FIFO — safe while filling;
	// and 0x69 is read exactly once per boundary).
	var matrixSnap [5]uint16
	matrixRead := false
drain:
	for {
		select {
		case r := <-e.matrixReq:
			if !matrixRead {
				for i, sel := range [5]uint16{0x64, 0x65, 0x66, 0x67, 0x69} {
					matrixSnap[i] = e.r(sel)
				}
				matrixRead = true
			}
			r <- matrixSnap
		default:
			break drain
		}
	}

	e.mu.Lock()
	trigDirty, code := e.trigDirty, e.trigCode
	offDirty, offCode := e.offDirty, e.offCode
	ledDirty, ledWord := e.ledDirty, e.ledWord
	e.trigDirty = false
	e.offDirty = [2]bool{}
	e.ledDirty = false
	e.mu.Unlock()

	// LED latch strobe (spec 08 §5): one indivisible 4-write burst, never
	// interleaved with any other CS3 write.
	if ledDirty {
		e.w3(0x0b, 0)
		e.w3(0x0a, ledWord>>8)
		e.w3(0x09, ledWord&0xff)
		e.w3(0x0b, 1)
	}

	// Vertical offset (spec 06 §5.3): low byte, then self-latching high
	// byte, then re-assert the CS1 run word to re-anchor the front-end
	// change on the once-armed engine.
	if offDirty[0] {
		e.w3(cs3OffC1Lo, offCode[0]&0xff)
		e.w3(cs3OffC1Hi, offCode[0]>>8)
	}
	if offDirty[1] {
		e.w3(cs3OffC2Lo, offCode[1]&0xff)
		e.w3(cs3OffC2Hi, offCode[1]>>8)
	}
	if offDirty[0] || offDirty[1] {
		e.w(selRunWord, e.runWord())
	}

	if !trigDirty {
		return
	}
	// The trigger-level safe recommit (spec 05 §1.3): level quad (both lanes
	// the same code, high bytes self-latch), comparator re-anchor preamble,
	// then a full re-arm. A bare level poke off this path wedges the display.
	lo, hi := code&0xff, code>>8
	e.w3(cs3LevelALo, lo)
	e.w3(cs3LevelAHi, hi)
	e.w3(cs3LevelBLo, lo)
	e.w3(cs3LevelBHi, hi)
	e.w(selPreamble, 0x0080)
	e.w(selPreamble, 0x0080)
	e.armEngine()
	e.logf("engine: trigger level recommitted, code=%#04x", code)
}

func (e *Engine) syncBandStatsLocked() {
	e.stats.TdivS = e.band.TdivS
	e.stats.DisplayedS = e.band.DisplayedSdivS()
	switch e.band.Kind() {
	case KindNativeFast:
		e.stats.BandKind = "native-fast"
	case KindDecimated:
		e.stats.BandKind = "decimated"
	case KindEnvelope:
		e.stats.BandKind = "envelope"
	case KindRoll:
		e.stats.BandKind = "roll"
	}
	if e.band.Kind() == KindRoll {
		e.stats.HaltMode = "latch-no-halt"
	} else {
		e.stats.HaltMode = "capture-halt"
	}
}
