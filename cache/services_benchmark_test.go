package cache

import (
	"cmp"
	"fmt"
	"math/rand"
	"slices"
	"testing"
)

func BenchmarkSortOld(b *testing.B) {
	entries := make([]CacheEntry, 1000)
	for i := 0; i < 1000; i++ {
		entries[i] = CacheEntry{
			Key:     ServiceKey(fmt.Sprintf("host-%d", rand.Intn(100)), fmt.Sprintf("service-%d", rand.Intn(100))),
			Host:    fmt.Sprintf("host-%d", rand.Intn(100)),
			Service: fmt.Sprintf("service-%d", rand.Intn(100)),
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		temp := make([]CacheEntry, len(entries))
		copy(temp, entries)
		b.StartTimer()
		slices.SortFunc(temp, func(a, b CacheEntry) int {
			if a.Host == b.Host {
				return cmp.Compare(a.Service, b.Service)
			}
			return cmp.Compare(a.Host, b.Host)
		})
	}
}

func BenchmarkSortNew(b *testing.B) {
	entries := make([]CacheEntry, 1000)
	for i := 0; i < 1000; i++ {
		host := fmt.Sprintf("host-%d", rand.Intn(100))
		svc := fmt.Sprintf("service-%d", rand.Intn(100))
		entries[i] = CacheEntry{
			Key:     ServiceKey(host, svc),
			Host:    host,
			Service: svc,
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		temp := make([]CacheEntry, len(entries))
		copy(temp, entries)
		b.StartTimer()
		slices.SortFunc(temp, func(a, b CacheEntry) int {
			return cmp.Compare(a.Key, b.Key)
		})
	}
}
