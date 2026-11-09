// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/webserver"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// Optional capability implemented by the normal SRAM acquisition owner.
// HTTP handlers never touch the bus, and network writes happen after owner work.
// TRLC-LINKS: REQ-SDS-013
type decodedBatchSource interface {
	ReadDecodedBatchInto(context.Context, sramcapture.RecordIdentity, []sramcapture.DecodedEvent) (int, error)
}

type decodedCaptureSource interface {
	BeginDecodedCapture(context.Context, sramcapture.Config) (sramcapture.RecordIdentity, error)
	ReadDecodedBatch(context.Context, sramcapture.RecordIdentity) ([]sramcapture.DecodedEvent, error)
	DecodedCaptureStatus(context.Context, sramcapture.RecordIdentity) (sramcapture.Metadata, error)
	RecallDecodedRecord(context.Context, sramcapture.RecordIdentity, uint32, uint32) ([]byte, error)
	EndDecodedCapture(context.Context, sramcapture.RecordIdentity) error
}

// TRLC-LINKS: REQ-SDS-013
func decodedIdentity(r *http.Request) (sramcapture.RecordIdentity, error) {
	q := r.URL.Query() // parsed once for both values
	epoch, e := strconv.ParseUint(q.Get("epoch"), 10, 32)
	if e != nil {
		return sramcapture.RecordIdentity{}, errors.New("epoch is required")
	}
	record, e := strconv.ParseUint(q.Get("record"), 10, 32)
	if e != nil {
		return sramcapture.RecordIdentity{}, errors.New("record is required")
	}
	return sramcapture.RecordIdentity{Epoch: uint32(epoch), Record: uint32(record)}, nil
}

// decodedSource is the decoded capture the routes drive: the board's own
// stream sessions where the board carries them (hal.Session: the decoded
// streams the core's cycle serves), else the engine's.
// TRLC-LINKS: REQ-SDS-013
func (s *Scope) decodedSource(board hal.Board) (decodedCaptureSource, bool) {
	if board != nil {
		if ss, ok := hal.SessionOf(board); ok {
			if src, ok := ss.(decodedCaptureSource); ok {
				return src, true
			}
		}
	}
	src, ok := s.sc.(decodedCaptureSource)
	return src, ok
}

// TRLC-LINKS: REQ-SDS-013
func (s *Scope) decodedRoutes(board hal.Board) []webserver.Route {
	var routes []webserver.Route
	add := func(pattern string, h http.HandlerFunc) {
		routes = append(routes, webserver.Route{Pattern: pattern, Feature: "decoded_capture", Handler: h})
	}
	source, ok := s.decodedSource(board)
	if !ok {
		return nil
	}
	add("POST /api/decoded/start", func(w http.ResponseWriter, r *http.Request) {
		var cfg sramcapture.Config
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&cfg); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			http.Error(w, "expected one configuration", http.StatusBadRequest)
			return
		}
		// Starting a packet-protocol stream may first load the packet image.
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		id, err := source.BeginDecodedCapture(ctx, cfg)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		webserver.WriteJSON(w, id)
	})
	add("GET /api/decoded/status", func(w http.ResponseWriter, r *http.Request) {
		id, err := decodedIdentity(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		m, err := source.DecodedCaptureStatus(ctx, id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		webserver.WriteJSON(w, m)
	})
	add("POST /api/decoded/end", func(w http.ResponseWriter, r *http.Request) {
		id, err := decodedIdentity(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := source.EndDecodedCapture(ctx, id); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	add("GET /api/decoded/record.bin", func(w http.ResponseWriter, r *http.Request) {
		id, err := decodedIdentity(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		offset, err := strconv.ParseUint(r.URL.Query().Get("offset"), 10, 32)
		if err != nil {
			http.Error(w, "offset is required", http.StatusBadRequest)
			return
		}
		count, err := strconv.ParseUint(r.URL.Query().Get("words"), 10, 32)
		if err != nil || offset+count > uint64(sramcapture.Words) {
			http.Error(w, "invalid word range", http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		data, err := source.RecallDecodedRecord(ctx, id, uint32(offset), uint32(count))
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = w.Write(data) //nolint:gosec // G705: raw record words served as application/octet-stream, never rendered
	})
	add("GET /api/decoded/events.bin", func(w http.ResponseWriter, r *http.Request) {
		id, err := decodedIdentity(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		readBatch := func() ([]sramcapture.DecodedEvent, error) { return source.ReadDecodedBatch(r.Context(), id) }
		if reusable, ok := source.(decodedBatchSource); ok {
			storage := make([]sramcapture.DecodedEvent, 4096)
			readBatch = func() ([]sramcapture.DecodedEvent, error) {
				n, err := reusable.ReadDecodedBatchInto(r.Context(), id, storage)
				return storage[:n], err
			}
		}
		batch, err := readBatch()
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Trailer", "X-Decoded-Error")
		w.Header().Set("X-Decoded-Record-Bytes", "32")
		controller := http.NewResponseController(w)
		var expected uint32
		data := make([]byte, 4096*sramcapture.DecodedEventBytes)
		for {
			_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
			size := len(batch) * sramcapture.DecodedEventBytes
			if cap(data) < size {
				data = make([]byte, size)
			} else {
				data = data[:size]
			}
			for i, event := range batch {
				if event.Epoch != id.Epoch || event.Sequence != expected {
					w.Header().Set("X-Decoded-Error", "event sequence discontinuity")
					return
				}
				e := sramcapture.MarshalEvent(event, data[i*sramcapture.DecodedEventBytes:(i+1)*sramcapture.DecodedEventBytes])
				if e != nil {
					w.Header().Set("X-Decoded-Error", e.Error())
					return
				}
				expected++
			}
			if len(data) != 0 {
				n, e := w.Write(data)
				if e != nil || n != len(data) {
					return
				}
			}
			if err := controller.Flush(); err != nil {
				return
			}
			// Drain backlog immediately; throttle only an empty producer.
			if len(batch) == 0 {
				timer := time.NewTimer(5 * time.Millisecond)
				select {
				case <-r.Context().Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
			batch, err = readBatch()
			if err != nil {
				w.Header().Set("X-Decoded-Error", err.Error())
				return
			}
		}
	})
	return routes
}
