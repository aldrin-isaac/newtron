package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// handleEvents subscribes the caller to the per-topology event stream.
// SSE format: `event: <type>\ndata: <json>\n\n` for each event;
// `: <comment>\n\n` for keepalive comment lines that don't trigger
// EventSource onmessage in browsers.
//
// The stream stays open until the client disconnects or the server
// shuts down. A 30-second heartbeat comment keeps proxies and load
// balancers from idling out the connection.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("topology name required"))
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("streaming unsupported"))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, ": subscribed to %s\n\n", name)
	flusher.Flush()

	events, unsub := s.broker.Subscribe(name)
	defer unsub()

	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()
		case ev, ok := <-events:
			if !ok {
				return
			}
			payload, err := json.Marshal(ev.Payload)
			if err != nil {
				s.logger.Printf("sse marshal: %v", err)
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, payload)
			flusher.Flush()
		}
	}
}
