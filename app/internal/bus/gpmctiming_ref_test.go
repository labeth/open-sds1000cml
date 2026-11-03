// ENGMODEL-OWNER-UNIT: FU-APP-BUS

package bus

// WithOEOn is a copy of t with the output-enable assertion time set to v (masked to the field).
// TRLC-LINKS: REQ-SDS-131
func (t CS1Timing) WithOEOn(v uint32) CS1Timing { setBits(&t.Config4, 0, 4, v); return t }
