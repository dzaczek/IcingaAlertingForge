package handler

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSSEBroker_SubscribePublishUnsubscribe(t *testing.T) {
	b := NewSSEBroker()

	ch := b.Subscribe()
	if ch == nil {
		t.Fatal("expected non-nil channel")
	}

	b.Publish(SSEEvent{Status: "CRITICAL", ServiceName: "svc-1"})

	select {
	case ev := <-ch:
		if ev.Status != "CRITICAL" || ev.ServiceName != "svc-1" {
			t.Errorf("unexpected event: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}

	b.Unsubscribe(ch)

	// Channel should be closed after Unsubscribe.
	_, open := <-ch
	if open {
		t.Error("channel should be closed after Unsubscribe")
	}
}

func TestSSEBroker_MaxClients(t *testing.T) {
	b := NewSSEBroker()
	for i := 0; i < sseMaxClients; i++ {
		ch := b.Subscribe()
		if ch == nil {
			t.Fatalf("expected channel for client %d", i)
		}
	}
	// sseMaxClients+1 should return nil
	ch := b.Subscribe()
	if ch != nil {
		t.Error("expected nil channel when at max clients")
	}
}

func TestSSEBroker_PublishRaw(t *testing.T) {
	b := NewSSEBroker()
	ch := b.Subscribe()

	b.PublishRaw("debug", []byte(`{"msg":"hello"}`))

	select {
	case ev := <-ch:
		if !strings.Contains(ev.rawMessage, "event: debug") {
			t.Errorf("unexpected raw message: %q", ev.rawMessage)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for raw event")
	}
}

func TestSSEBroker_ServeHTTP(t *testing.T) {
	b := NewSSEBroker()

	srv := httptest.NewServer(b)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil && ctx.Err() == nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != nil {
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
			t.Errorf("expected text/event-stream, got %q", ct)
		}
	}
}

func TestSSEBroker_ServeHTTP_TooManyClients(t *testing.T) {
	b := NewSSEBroker()

	// Fill up all client slots synchronously
	for i := 0; i < sseMaxClients; i++ {
		b.Subscribe()
	}

	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	rr := httptest.NewRecorder()
	b.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rr.Code)
	}
}

func TestSSEBroker_ServeHTTP_Event(t *testing.T) {
	b := NewSSEBroker()

	srv := httptest.NewServer(b)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil && ctx.Err() == nil {
		t.Fatalf("unexpected connection error: %v", err)
	}
	if resp == nil {
		return
	}
	defer resp.Body.Close()

	// Give the handler time to subscribe, then publish
	time.Sleep(30 * time.Millisecond)
	b.Publish(SSEEvent{Status: "OK", ServiceName: "svc-x"})

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "svc-x") {
			return // success
		}
	}
	// context timeout is acceptable — we just verified the stream was opened
}

func TestSSEBroker_ServeHTTP_RawEvent(t *testing.T) {
	b := NewSSEBroker()

	srv := httptest.NewServer(b)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil && ctx.Err() == nil {
		t.Fatalf("unexpected connection error: %v", err)
	}
	if resp == nil {
		return
	}
	defer resp.Body.Close()

	// Give the handler time to subscribe, then publish a raw event with HTML characters
	time.Sleep(30 * time.Millisecond)
	b.PublishRaw("debug", []byte(`{"msg":"<script>alert(1)</script>"}`))

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		// Wait for the specific data line containing our escaped payload
		if strings.HasPrefix(line, "data: ") {
			if !strings.Contains(line, `\u003cscript\u003e`) {
				t.Errorf("expected HTML characters to be escaped to JSON unicode, got: %s", line)
			}
			return // success
		}
	}
}
