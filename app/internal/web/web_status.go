package web

import (
	"net/http"
	"open-sds/app/internal/analog"
	"open-sds/app/internal/buildinfo"
	"open-sds/app/internal/engine"
)

type statusReply struct {
	engine.Stats
	Tdivs       []float64 `json:"tdivs"`
	TrigVolts   float64   `json:"trig_volts"`
	TrigZero    float64   `json:"trig_zero"` // active source-detent trig cal: code = zero − cpv·V (BNC volts)
	TrigCpv     float64   `json:"trig_cpv"`  // DAC codes per input-volt at the source detent
	TrigCodeMin uint16    `json:"trig_code_min"`
	TrigCodeMax uint16    `json:"trig_code_max"`
	Vdivs       []float64 `json:"vdivs,omitempty"`
	Vdiv1       float64   `json:"vdiv1,omitempty"`
	Vdiv2       float64   `json:"vdiv2,omitempty"`
	Probe1      float64   `json:"probe1,omitempty"`
	Probe2      float64   `json:"probe2,omitempty"`
	Cpl1        int       `json:"cpl1"` // 0=DC 1=AC 2=GND
	Cpl2        int       `json:"cpl2"`
	Inv1        bool      `json:"inv1"` // display-level trace invert (SCPI Cn:INVS shadow — the truth)
	Inv2        bool      `json:"inv2"`
	Zoom1       int       `json:"zoom1,omitempty"`
	Zoom2       int       `json:"zoom2,omitempty"`
	VdivLive    bool      `json:"vdiv_live"` // false until the first emit
	Off1V       float64   `json:"off1_v"`
	Off2V       float64   `json:"off2_v"`
	CalSource   string    `json:"cal_source,omitempty"`
	DC1V        float64   `json:"dc1_v"` // calibrated DC diagnostic (GAIN/110)
	DC2V        float64   `json:"dc2_v"`
	Version     string    `json:"version"`

	// Device super-res (panel stack-and-crunch) live state; omitted when inactive.
	SRActive   bool    `json:"sr_active,omitempty"`
	SRReview   bool    `json:"sr_review,omitempty"`
	SRBits     float64 `json:"sr_bits,omitempty"`
	SRFrames   int     `json:"sr_frames,omitempty"`
	SRRejected int     `json:"sr_rejected,omitempty"`
	SRStatus   string  `json:"sr_status,omitempty"`

	// Realtime acquisition checker (instrumentation only): HALF-record diagnosis.
	HalfRate float64            `json:"half_rate"` // fraction HALF over the last ≤64 samples
	AcqLog   []engine.AcqSample `json:"acq_log"`   // last ≤24 frames, most-recent-last
	CmdLog   []engine.CmdNote   `json:"cmd_log"`   // last ≤16 web set-control calls
}

func (s *Server) hStatus(w http.ResponseWriter, r *http.Request) {
	st := s.sc.Snapshot()
	rep := statusReply{
		Stats:       st,
		Tdivs:       engine.SupportedTdivs(),
		TrigVolts:   engine.TrigLevelVolts(st.TrigCode),
		TrigCodeMin: engine.TrigCodeMin,
		TrigCodeMax: engine.TrigCodeMax,
		Version:     buildinfo.String(),
	}
	if sr, ok := s.panel.(superresReporter); ok {
		rep.SRActive, rep.SRReview, rep.SRBits, rep.SRFrames, rep.SRRejected, rep.SRStatus = sr.SuperresStatus()
	}
	if s.invSrc != nil {
		inv := s.invSrc()
		rep.Inv1, rep.Inv2 = inv[0], inv[1]
	}
	rep.AcqLog, rep.HalfRate = s.sc.AcqLog(24)
	rep.CmdLog = s.sc.CmdLog(16)
	if s.fe != nil {
		idx, emitted := s.fe.Snapshot()
		for _, d := range analog.Detents {
			rep.Vdivs = append(rep.Vdivs, d.VdivV)
		}
		rep.Vdiv1 = analog.Detents[idx[0]].VdivV
		rep.Vdiv2 = analog.Detents[idx[1]].VdivV
		rep.Zoom1 = analog.Detents[idx[0]].Zoom
		rep.Zoom2 = analog.Detents[idx[1]].Zoom
		rep.VdivLive = emitted
		rep.Probe1 = s.fe.ProbeFactor(0)
		rep.Probe2 = s.fe.ProbeFactor(1)
		rep.Cpl1 = s.fe.Coupling(0)
		rep.Cpl2 = s.fe.Coupling(1)
		// The trigger level is input-referred to its source channel through
		// that channel's per-detent cal (slope + zero) AND its probe. Also
		// publish the active {zero, cpv} so the browser converts drag→code with
		// the same slope the server uses (measured global fit, 911 codes/V).
		rep.TrigVolts = s.fe.TrigVolts(st.TrigCode, st.TrigSource)
		rep.TrigZero, rep.TrigCpv = s.fe.TrigCalActive(st.TrigSource)
	}
	offVolts := analog.OffsetVolts
	if s.fe != nil {
		offVolts = s.fe.OffsetVolts
		rep.CalSource = s.fe.CalSource()
		// Calibrated DC diagnostic over the latest frame's record means. The
		// GAIN/110 mapping is for the deep 50-codes/div scale; roll codes are
		// half-scale, so skip the diagnostic on a roll frame rather than
		// report double the true voltage.
		s.sc.WithFrame(func(f *engine.Frame) {
			if f == nil || f.Valid == 0 || f.IsEnv || f.RollCodes {
				return
			}
			mean := func(sig []uint8) float64 {
				sum := 0
				for _, v := range sig[:f.Valid] {
					sum += int(v)
				}
				return float64(sum) / float64(f.Valid)
			}
			rep.DC1V = s.fe.DCVolts(0, mean(f.C1))
			rep.DC2V = s.fe.DCVolts(1, mean(f.C2))
		})
	}
	if st.OffC1 != 0 {
		rep.Off1V = offVolts(0, st.OffC1)
	}
	if st.OffC2 != 0 {
		rep.Off2V = offVolts(1, st.OffC2)
	}
	if s.fe != nil { // report offsets at the probe tip, matching the frame's off_v
		rep.Off1V *= s.fe.ProbeFactor(0)
		rep.Off2V *= s.fe.ProbeFactor(1)
	}
	writeJSON(w, rep)
}
