package metrics

import (
	"testing"
)

func TestPerKeyCollector(t *testing.T) {
	pk := NewPerKeyCollector()

	pk.Record("source1", false)
	pk.Record("source1", true)
	pk.Record("source2", false)

	stats := pk.GetStats()

	if len(stats) != 2 {
		t.Errorf("Expected 2 sources, got %d", len(stats))
	}

	s1 := stats["source1"]
	if s1.Requests.Load() != 2 {
		t.Errorf("Source1: expected 2 requests, got %d", s1.Requests.Load())
	}
	if s1.Errors.Load() != 1 {
		t.Errorf("Source1: expected 1 error, got %d", s1.Errors.Load())
	}
	if s1.LastSeen.Load() == 0 {
		t.Error("Source1: LastSeen not updated")
	}

	s2 := stats["source2"]
	if s2.Requests.Load() != 1 {
		t.Errorf("Source2: expected 1 request, got %d", s2.Requests.Load())
	}
	if s2.Errors.Load() != 0 {
		t.Errorf("Source2: expected 0 errors, got %d", s2.Errors.Load())
	}

	// Test empty source
	pk.Record("", false)
	if len(pk.GetStats()) != 2 {
		t.Error("Empty source should not be recorded")
	}
}

func TestPerKeyCollector_Concurrency(t *testing.T) {
	pk := NewPerKeyCollector()
	const iterations = 1000
	const goroutines = 10

	done := make(chan bool)
	for i := 0; i < goroutines; i++ {
		go func() {
			for j := 0; j < iterations; j++ {
				pk.Record("concurrent_source", j%2 == 0)
			}
			done <- true
		}()
	}

	for i := 0; i < goroutines; i++ {
		<-done
	}

	stats := pk.GetStats()
	s := stats["concurrent_source"]
	expectedReqs := int64(iterations * goroutines)
	if s.Requests.Load() != expectedReqs {
		t.Errorf("Expected %d requests, got %d", expectedReqs, s.Requests.Load())
	}
	expectedErrs := int64(iterations * goroutines / 2)
	if s.Errors.Load() != expectedErrs {
		t.Errorf("Expected %d errors, got %d", expectedErrs, s.Errors.Load())
	}
}
