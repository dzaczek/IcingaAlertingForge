package queue

import (
	"fmt"
	"testing"
	"time"
)

type dummySender struct{}

func (d *dummySender) SendCheckResult(host, service string, exitStatus int, message string) error {
	return nil
}

func BenchmarkQueueFlush(b *testing.B) {
	b.StopTimer()
	sender := &dummySender{}

	b.StartTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		q := New(Config{
			Enabled:       true,
			MaxSize:       20000,
			RetryBase:     1 * time.Second,
			RetryMax:      2 * time.Second,
			CheckInterval: 1 * time.Second,
		}, sender)

		// Enqueue 10000 items
		for j := 0; j < 10000; j++ {
			_ = q.Enqueue(Item{
				ID:         fmt.Sprintf("item-%d", j),
				Host:       "testhost",
				Service:    "testsvc",
				ExitStatus: 1,
				Message:    "test message",
			})
		}
		b.StartTimer()

		q.Flush()
	}
}
