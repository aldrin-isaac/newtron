package httputil

import (
	"bufio"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWriteSSEStreamWritesSubscribeCommentImmediately(t *testing.T) {
	broker := NewBroker[testEvent]()
	logger := log.New(&strings.Builder{}, "", 0)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteSSEStream(w, r, broker, "topo1", logger)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("client do: %v", err)
	}
	defer resp.Body.Close()

	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", resp.Header.Get("Content-Type"))
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Scan()
	line := scanner.Text()
	if !strings.HasPrefix(line, ": subscribed to topo1") {
		t.Errorf("first line = %q, want subscribe comment", line)
	}
}

func TestWriteSSEStreamForwardsPublishedEventToWire(t *testing.T) {
	broker := NewBroker[testEvent]()
	logger := log.New(&strings.Builder{}, "", 0)

	var wg sync.WaitGroup
	wg.Add(1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer wg.Done()
		WriteSSEStream(w, r, broker, "topo1", logger)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("client do: %v", err)
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	// Drain the initial subscribe comment line + its blank line.
	scanner.Scan()
	scanner.Scan()

	// Publish after the subscriber is registered (give Subscribe a
	// moment to race).
	time.Sleep(50 * time.Millisecond)
	broker.Publish("topo1", testEvent{Kind_: "phase", Value: 7})

	var eventLine, dataLine string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			break
		}
		if strings.HasPrefix(line, "event: ") {
			eventLine = line
		}
		if strings.HasPrefix(line, "data: ") {
			dataLine = line
		}
	}
	if eventLine != "event: phase" {
		t.Errorf("event line = %q, want 'event: phase'", eventLine)
	}
	if dataLine != "data: 7" {
		t.Errorf("data line = %q, want 'data: 7'", dataLine)
	}

	cancel()
	wg.Wait()
}
