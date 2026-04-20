package cache

import (
	"fmt"
	"math/rand"
	"sort"
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
		sort.Slice(temp, func(i, j int) bool {
			if temp[i].Host == temp[j].Host {
				return temp[i].Service < temp[j].Service
			}
			return temp[i].Host < temp[j].Host
		})
	}
}

func BenchmarkSortFrozenOld(b *testing.B) {
	entries := make([]FrozenEntry, 1000)
	for i := 0; i < 1000; i++ {
		entries[i] = FrozenEntry{
			Host:    fmt.Sprintf("host-%d", rand.Intn(100)),
			Service: fmt.Sprintf("service-%d", rand.Intn(100)),
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		temp := make([]FrozenEntry, len(entries))
		copy(temp, entries)
		b.StartTimer()
		sort.Slice(temp, func(i, j int) bool {
			if temp[i].Host != temp[j].Host {
				return temp[i].Host < temp[j].Host
			}
			return temp[i].Service < temp[j].Service
		})
	}
}

func BenchmarkSortFrozenNew(b *testing.B) {
	entries := make([]FrozenEntry, 1000)
	for i := 0; i < 1000; i++ {
		host := fmt.Sprintf("host-%d", rand.Intn(100))
		svc := fmt.Sprintf("service-%d", rand.Intn(100))
		entries[i] = FrozenEntry{
			Key:     ServiceKey(host, svc),
			Host:    host,
			Service: svc,
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		temp := make([]FrozenEntry, len(entries))
		copy(temp, entries)
		b.StartTimer()
		sort.Slice(temp, func(i, j int) bool {
			return temp[i].Key < temp[j].Key
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
		sort.Slice(temp, func(i, j int) bool {
			return temp[i].Key < temp[j].Key
		})
	}
}
