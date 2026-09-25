// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"open-sds/app/internal/sramcapture"
	"strconv"
	"time"
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
	epoch, e := strconv.ParseUint(r.URL.Query().Get("epoch"), 10, 32)
	if e != nil {
		return sramcapture.RecordIdentity{}, fmt.Errorf("epoch is required")
	}
	record, e := strconv.ParseUint(r.URL.Query().Get("record"), 10, 32)
	if e != nil {
		return sramcapture.RecordIdentity{}, fmt.Errorf("record is required")
	}
	return sramcapture.RecordIdentity{Epoch: uint32(epoch), Record: uint32(record)}, nil
}

// TRLC-LINKS: REQ-SDS-013
func (s *Server) registerDecodedCapture(mux *http.ServeMux) {
	source, ok := s.sc.(decodedCaptureSource)
	if !ok {
		return
	}
	mux.HandleFunc("POST /api/decoded/start", func(w http.ResponseWriter, r *http.Request) {
		var cfg sramcapture.Config
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&cfg); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			http.Error(w, "expected one configuration", 400)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		id, err := source.BeginDecodedCapture(ctx, cfg)
		if err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		writeJSON(w, id)
	})
	mux.HandleFunc("GET /api/decoded/status", func(w http.ResponseWriter, r *http.Request) {
		id, err := decodedIdentity(r)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		m, err := source.DecodedCaptureStatus(ctx, id)
		if err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		writeJSON(w, m)
	})
	mux.HandleFunc("POST /api/decoded/end", func(w http.ResponseWriter, r *http.Request) {
		id, err := decodedIdentity(r)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := source.EndDecodedCapture(ctx, id); err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /api/decoded/record.bin", func(w http.ResponseWriter, r *http.Request) {
		id, err := decodedIdentity(r)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		offset, err := strconv.ParseUint(r.URL.Query().Get("offset"), 10, 32)
		if err != nil {
			http.Error(w, "offset is required", 400)
			return
		}
		count, err := strconv.ParseUint(r.URL.Query().Get("words"), 10, 32)
		if err != nil || offset+count > uint64(sramcapture.Words) {
			http.Error(w, "invalid word range", 400)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		data, err := source.RecallDecodedRecord(ctx, id, uint32(offset), uint32(count))
		if err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = w.Write(data)
	})
	mux.HandleFunc("GET /api/decoded/events.bin", func(w http.ResponseWriter, r *http.Request) {
		id, err := decodedIdentity(r)
		if err != nil {
			http.Error(w, err.Error(), 400)
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
			http.Error(w, err.Error(), 409)
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
				e := event.MarshalTo(data[i*sramcapture.DecodedEventBytes : (i+1)*sramcapture.DecodedEventBytes])
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
}
