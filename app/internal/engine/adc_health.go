// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

// The converter checks are the board's (acq.Board.StartConverters); the
// engine runs them at its start, after its image loads and while
// acquiring, with its image reload as the recovery, and shows what they
// found in its status.

// noteRecordCores hands a full-rate record's raw codes to the board's
// converter check.
// TRLC-LINKS: REQ-SDS-231
func (e *Engine) noteRecordCores(c1, c2 []uint8) { e.hw().NoteRecordCores(c1, c2) }

// startConverters is the converters' bring-up on a freshly configured
// fabric (the start and every image load alike).
// TRLC-LINKS: REQ-SDS-231
func (e *Engine) startConverters(why string) {
	e.hw().StartConverters(why, e.reloadImage)
	e.noteConverterHealth()
}

// ensureCores checks the converters after why and brings stuck cores back.
// TRLC-LINKS: REQ-SDS-231
func (e *Engine) ensureCores(why string) {
	e.hw().EnsureCores(why, e.reloadImage)
	e.noteConverterHealth()
}

// checkCoresDue runs the periodic check while acquiring.
// TRLC-LINKS: REQ-SDS-231
func (e *Engine) checkCoresDue() {
	if e.hw().CheckCoresDue(e.reloadImage) {
		e.noteConverterHealth()
	}
}

// noteConverterHealth shows the board's converter health in the status.
// TRLC-LINKS: REQ-SDS-231
func (e *Engine) noteConverterHealth() {
	h := e.hw().ConverterHealth()
	e.mu.Lock()
	e.stats.ADCFault, e.stats.ADCRecoveries, e.stats.ADCRecovery, e.stats.ADCCheckMs = h.Fault, h.Recoveries, h.Recovery, h.CheckMs
	e.mu.Unlock()
}
