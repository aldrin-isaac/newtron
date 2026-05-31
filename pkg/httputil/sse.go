package httputil

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// Eventable is the contract events must satisfy to flow through
// WriteSSEStream. Implementations expose the SSE `event:` token
// (Kind) and the value that will be JSON-encoded into the `data:`
// line (Body).
//
// Each server defines its own typed Event struct and attaches Kind /
// Body as one-liner methods. The broker stays parametric over the
// typed Event; the SSE writer reads through this interface so it
// stays parametric over the event taxonomy.
type Eventable interface {
	Kind() string
	Body() any
}

// SSEHeartbeat is the interval between SSE comment-only "heartbeat"
// lines. Keeps proxies and load balancers from idling out the long-
// lived connection. Set to 30s to match the previous newtrun-server
// and newtlab-server values exactly.
const SSEHeartbeat = 30 * time.Second

// WriteSSEStream sets up an SSE response for key and pumps events
// from broker until the client disconnects or the server cancels the
// request context. Errors during marshaling are logged but do not
// terminate the stream — best-effort delivery.
//
// The handler writes its own status code and headers (Content-Type:
// text/event-stream, Cache-Control: no-cache, Connection: keep-alive,
// HTTP 200). On entry, before any event arrives, it writes a single
// SSE comment line acknowledging the subscription so clients can
// distinguish "stream open, no events yet" from "still connecting".
func WriteSSEStream[E Eventable](w http.ResponseWriter, r *http.Request, broker *Broker[E], key string, logger *log.Logger) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		WriteError(w, http.StatusInternalServerError, fmt.Errorf("streaming unsupported"))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, ": subscribed to %s\n\n", key)
	flusher.Flush()

	events, unsub := broker.Subscribe(key)
	defer unsub()

	heartbeat := time.NewTicker(SSEHeartbeat)
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
			payload, err := json.Marshal(ev.Body())
			if err != nil {
				if logger != nil {
					logger.Printf("sse marshal: %v", err)
				}
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Kind(), payload)
			flusher.Flush()
		}
	}
}
