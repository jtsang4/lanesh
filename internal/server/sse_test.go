package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestKeepSSEAlive(t *testing.T) {
	const hold = 150 * time.Millisecond
	tests := []struct {
		name       string
		handler    http.HandlerFunc
		wantStatus int
		heartbeats bool
		wantBody   string // checked after removing heartbeats
	}{
		{
			name: "event stream",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, ": ok\n\n")
				http.NewResponseController(w).Flush()
				time.Sleep(hold)
			},
			wantStatus: http.StatusOK, heartbeats: true, wantBody: ": ok\n\n",
		},
		{
			name: "event stream with an implicit status",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
				_, _ = io.WriteString(w, "data: 1\n\n")
				http.NewResponseController(w).Flush()
				time.Sleep(hold)
			},
			wantStatus: http.StatusOK, heartbeats: true, wantBody: "data: 1\n\n",
		},
		{
			name: "event stream committed by a flush",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				http.NewResponseController(w).Flush()
				time.Sleep(hold)
			},
			wantStatus: http.StatusOK, heartbeats: true,
		},
		{
			name: "event stream not yet committed",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				time.Sleep(hold)
				_, _ = io.WriteString(w, "data: late\n\n")
			},
			wantStatus: http.StatusOK, wantBody: "data: late\n\n",
		},
		{
			name: "json",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				http.NewResponseController(w).Flush()
				time.Sleep(hold)
				_, _ = io.WriteString(w, `{"ok":true}`)
			},
			wantStatus: http.StatusOK, wantBody: `{"ok":true}`,
		},
		{
			name: "error status on an event stream",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusNotFound)
				http.NewResponseController(w).Flush()
				time.Sleep(hold)
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name: "accepted",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				time.Sleep(hold)
				w.WriteHeader(http.StatusAccepted)
			},
			wantStatus: http.StatusAccepted,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(keepSSEAlive(tt.handler, 10*time.Millisecond))
			defer ts.Close()
			resp, err := http.Get(ts.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}
			n := strings.Count(string(body), sseHeartbeat)
			if tt.heartbeats && n < 3 {
				t.Errorf("got %d heartbeats in %q, want several", n, body)
			}
			if !tt.heartbeats && n > 0 {
				t.Errorf("got %d heartbeats in %q, want none", n, body)
			}
			if got := strings.ReplaceAll(string(body), sseHeartbeat, ""); got != tt.wantBody {
				t.Errorf("body without heartbeats = %q, want %q", got, tt.wantBody)
			}
		})
	}
}

// Heartbeats and events written concurrently by other goroutines, as the MCP
// SDK does, must never interleave within an event.
func TestKeepSSEAliveDoesNotSplitEvents(t *testing.T) {
	const events = 200
	event := "event: message\ndata: " + strings.Repeat("x", 4096) + "\n\n"
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		var wg sync.WaitGroup
		for range 4 {
			wg.Go(func() {
				for range events / 4 {
					_, _ = io.WriteString(w, event)
					_ = http.NewResponseController(w).Flush()
					time.Sleep(time.Millisecond)
				}
			})
		}
		wg.Wait()
	})
	ts := httptest.NewServer(keepSSEAlive(handler, time.Millisecond))
	defer ts.Close()
	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	got, heartbeats := 0, 0
	for block := range strings.SplitSeq(strings.TrimSuffix(string(body), "\n\n"), "\n\n") {
		switch block + "\n\n" {
		case event:
			got++
		case sseHeartbeat:
			heartbeats++
		default:
			t.Fatalf("unexpected block %.80q", block)
		}
	}
	if got != events || heartbeats == 0 {
		t.Fatalf("got %d events and %d heartbeats, want %d events and some heartbeats", got, heartbeats, events)
	}
}
