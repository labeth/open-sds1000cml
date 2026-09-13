package web

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"open-sds/app/internal/sramcapture"
)

//go:embed sram.html
var sramPage string

// SRAMSource is an exclusive, serialized acquisition owner, separate from the
// normal default-fabric engine. Handlers never access device registers.
type SRAMSource interface {
	Status() (sramcapture.Metadata, error)
	Arm(context.Context, sramcapture.Config) error
	Force(context.Context) error
	Halt(context.Context) error
	Snapshot(context.Context) ([10]uint8, error)
	Recall(context.Context, uint32, uint32, io.Writer) (int64, error)
}

func SRAMHandler(source SRAMSource) http.Handler {
	mux := http.NewServeMux()
	fail := func(w http.ResponseWriter, code int, err error) {
		w.Header().Del("Content-Length")
		w.Header().Del("Content-Disposition")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
	}
	reply := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(v)
	}
	status := func(w http.ResponseWriter, r *http.Request) {
		m, e := source.Status()
		if e != nil {
			fail(w, 503, e)
			return
		}
		format := "uint8 CH1, CH2 pairs; two pairs per word"
		spw := m.SamplesPerWord
		if spw == 0 {
			spw = 2
		}
		if m.FractionBits == 8 {
			format = "uint16 little-endian Q8.8 CH1, CH2; one pair per word"
		}
		reply(w, map[string]any{"capture": m, "capacity_words": sramcapture.Words, "capacity_samples_per_channel": sramcapture.Words * uint32(spw), "sample_rate_hz": m.SampleRateHz, "channels": 2, "format": format})
	}
	mux.HandleFunc("GET /api/sram/status", status)
	mux.HandleFunc("POST /api/sram/arm", func(w http.ResponseWriter, r *http.Request) {
		var cfg sramcapture.Config
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if e := decoder.Decode(&cfg); e != nil {
			fail(w, 400, e)
			return
		}
		var extra any
		if e := decoder.Decode(&extra); e != io.EOF {
			fail(w, 400, fmt.Errorf("expected one configuration object"))
			return
		}
		if e := source.Arm(r.Context(), cfg); e != nil {
			fail(w, 409, e)
			return
		}
		status(w, r)
	})
	for path, action := range map[string]func(context.Context) error{"force": source.Force, "halt": source.Halt} {
		mux.HandleFunc("POST /api/sram/"+path, func(w http.ResponseWriter, r *http.Request) {
			if e := action(r.Context()); e != nil {
				fail(w, 409, e)
				return
			}
			status(w, r)
		})
	}
	mux.HandleFunc("GET /api/sram/snapshot", func(w http.ResponseWriter, r *http.Request) {
		v, e := source.Snapshot(r.Context())
		if e != nil {
			fail(w, 503, e)
			return
		}
		reply(w, map[string]any{"cores": v})
	})
	mux.HandleFunc("GET /api/sram/record.bin", func(w http.ResponseWriter, r *http.Request) {
		m, e := source.Status()
		if e != nil {
			fail(w, 503, e)
			return
		}
		if !m.Ready || !m.Frozen || m.Running {
			fail(w, 409, fmt.Errorf("no frozen record; capture or halt first"))
			return
		}
		parse := func(key string, fallback uint32) (uint32, error) {
			s := r.URL.Query().Get(key)
			if s == "" {
				return fallback, nil
			}
			v, e := strconv.ParseUint(s, 10, 32)
			return uint32(v), e
		}
		offset, e := parse("offset", 0)
		if e != nil || offset > m.Length {
			fail(w, 400, fmt.Errorf("invalid word offset"))
			return
		}
		count, e := parse("words", m.Length-offset)
		if e != nil || uint64(offset)+uint64(count) > uint64(m.Length) {
			fail(w, 400, fmt.Errorf("invalid word count"))
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.FormatUint(uint64(count)*4, 10))
		w.Header().Set("Content-Disposition", "attachment; filename=\"sram-record.bin\"")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-SRAM-Word-Offset", strconv.FormatUint(uint64(offset), 10))
		w.Header().Set("X-SRAM-Words", strconv.FormatUint(uint64(count), 10))
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		n, e := source.Recall(ctx, offset, count, deadlineWriter{w})
		if e != nil && n == 0 {
			fail(w, 409, e)
		}
		// Once binary bytes have been sent, leave a short response on failure;
		// never append JSON to a record. Content-Length makes truncation detectable.
	})
	page := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, sramPage)
	}
	mux.HandleFunc("GET /{$}", page)
	mux.HandleFunc("GET /sram", page)
	return mux
}

type deadlineWriter struct{ http.ResponseWriter }

func (w deadlineWriter) Write(p []byte) (int, error) {
	// Bound network backpressure per chunk so the bus owner and health poll
	// are not held for longer than the OTA supervisor's three-second window.
	_ = http.NewResponseController(w.ResponseWriter).SetWriteDeadline(time.Now().Add(1500 * time.Millisecond))
	return w.ResponseWriter.Write(p)
}
