// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/superres"
)

// fpgaStackBody is the request: an engine request plus an optional "match",
// which builds the template qualifier from the latest frame around the
// trigger (ADR-STACKING-TEMPLATE-QUALIFIER).
type fpgaStackBody struct {
	engine.FPGAStackRequest
	Match *struct {
		Points    int     `json:"points"`
		Tolerance float64 `json:"tolerance"`
	} `json:"match,omitempty"`
}

// frameTemplate builds the template from the align channel of the latest frame.
// TRLC-LINKS: REQ-SDS-141
func (s *Server) frameTemplate(cfg engine.FPGAStackRequest, points int, tolerance float64) (superres.FPGATemplate, error) {
	var sig []float64
	var sampleS, edgeX float64
	s.sc.WithFrame(func(f *engine.Frame) {
		if f == nil || f.IsEnv || f.Valid < 8 {
			return
		}
		codes, fine := f.C1, f.Q1
		if cfg.Stack.Channel == 1 {
			codes, fine = f.C2, f.Q2
		}
		sig = make([]float64, f.Valid)
		for i := range sig {
			if len(fine) >= f.Valid {
				sig[i] = float64(fine[i]) / 256
			} else {
				sig[i] = float64(codes[i])
			}
		}
		sampleS, edgeX = f.SampleS, f.EdgeX
	})
	if sig == nil {
		return superres.FPGATemplate{}, fmt.Errorf("no frame to take a template from")
	}
	return superres.BuildFPGATemplate(sig, sampleS, edgeX, float64(cfg.Stack.Level), cfg.Stack.Falling, points, tolerance)
}

// fpgaStackSource is the engine's FPGA stacking session (ADR-STACKING-IMAGE-SPLIT).
type fpgaStackSource interface {
	FPGAStack(context.Context, engine.FPGAStackRequest) (engine.FPGAStackResult, error)
}

// POST /api/superres/fpga {"stack":{...},"records":N} switches to the stacking
// image, stacks N fresh records in the FPGA, restores the general image and
// returns the stack. Live acquisition pauses for the session.
// TRLC-LINKS: REQ-SDS-141
func (s *Server) registerFPGAStack(mux *http.ServeMux) {
	source, ok := s.sc.(fpgaStackSource)
	if !ok {
		return
	}
	mux.HandleFunc("POST /api/superres/fpga", func(w http.ResponseWriter, r *http.Request) {
		var body fpgaStackBody
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			http.Error(w, "expected one request", 400)
			return
		}
		req := body.FPGAStackRequest
		if m := body.Match; m != nil && m.Points > 0 {
			tpl, err := s.frameTemplate(req, m.Points, m.Tolerance)
			if err != nil {
				http.Error(w, "match: "+err.Error(), 409)
				return
			}
			req.Stack.Template = make([]int, len(tpl.Values))
			for i, v := range tpl.Values {
				req.Stack.Template[i] = int(v)
			}
			req.Stack.TemplateStride, req.Stack.TemplatePre, req.Stack.TemplateThreshold = tpl.Stride, tpl.Pre, tpl.Threshold
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
		defer cancel()
		res, err := source.FPGAStack(ctx, req)
		if err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		writeJSON(w, struct {
			engine.FPGAStackResult
			Template []int `json:"template,omitempty"`
			Stride   int   `json:"template_stride,omitempty"`
			Pre      int   `json:"template_pre,omitempty"`
			Limit    int   `json:"template_threshold,omitempty"`
		}{res, req.Stack.Template, req.Stack.TemplateStride, req.Stack.TemplatePre, req.Stack.TemplateThreshold})
	})
}
