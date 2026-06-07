package main

import (
	"fmt"
	"strconv"
	"testing"
	"time"
)

func BenchmarkSprintfInt(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = fmt.Sprintf("%d", i)
	}
}

func BenchmarkStrconvItoa(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = strconv.Itoa(i)
	}
}

func BenchmarkSprintfConcat(b *testing.B) {
	s := "test"
	for i := 0; i < b.N; i++ {
		_ = fmt.Sprintf("OK: %s", s)
	}
}

func BenchmarkStringConcat(b *testing.B) {
	s := "test"
	for i := 0; i < b.N; i++ {
		_ = "OK: " + s
	}
}

func BenchmarkSprintfComplex(b *testing.B) {
	reqID := "req123"
	svc := "my-service"
	now := time.Now().UnixNano()
	for i := 0; i < b.N; i++ {
		_ = fmt.Sprintf("%s-%s-%d", reqID, svc, now)
	}
}

func BenchmarkStringConcatComplex(b *testing.B) {
	reqID := "req123"
	svc := "my-service"
	now := time.Now().UnixNano()
	for i := 0; i < b.N; i++ {
		_ = reqID + "-" + svc + "-" + strconv.FormatInt(now, 10)
	}
}
