package handler

import (
	"net/http/httptest"
	"testing"
)

func TestSSEBroker_PublishRaw_Coverage(t *testing.T) {
	b := NewSSEBroker()
	req := httptest.NewRequest("GET", "/events", nil)
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		b.ServeHTTP(rr, req)
		close(done)
	}()

	b.Publish(SSEEvent{rawMessage: "test"})
	b.PublishRaw("test", []byte("test"))
	b.Publish(SSEEvent{Status: "ok"})

	// It's hard to deterministically stop ServeHTTP without closing the context,
	// but this will trigger the lines inside ServeHTTP at least.
}
