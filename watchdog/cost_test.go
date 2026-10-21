package watchdog

import (
	"testing"
	"time"
)

// What the watchdog costs when nothing is wrong.
//
// Worth measuring rather than assuming, because the answer decides whether a
// service can afford to leave it switched on: a detector that is itself
// expensive is not worth having.
//
// Two different frequencies. The sample runs on a ticker, once per Interval.
// The registry runs once per request, so it is the one that scales with
// traffic.

func BenchmarkSample(b *testing.B) {
	w := New(Config{Interval: time.Second}, NewRegistry(), nil)

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		_ = w.sample()
	}
}

func BenchmarkReadMetrics(b *testing.B) {
	b.ReportAllocs()

	for range b.N {
		_, _, _ = readMetrics()
	}
}

func BenchmarkProcessCPU(b *testing.B) {
	b.ReportAllocs()

	for range b.N {
		_ = processCPU()
	}
}

// Per request: enter the registry and leave it.
func BenchmarkRegistryBegin(b *testing.B) {
	r := NewRegistry()

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		_, done := r.Begin(Work{ID: "req", Method: "unary", Path: "/Service/Method"})
		done()
	}
}

// With work already in flight, since entering takes the registry lock.
func BenchmarkRegistryBeginUnderLoad(b *testing.B) {
	r := NewRegistry()

	for i := range 64 {
		_, _ = r.Begin(Work{ID: string(rune('a' + i%26)), Path: "/x"})
	}

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		_, done := r.Begin(Work{ID: "req", Method: "unary", Path: "/Service/Method"})
		done()
	}
}

// Contended, which is what a real server does to it.
func BenchmarkRegistryBeginParallel(b *testing.B) {
	r := NewRegistry()

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, done := r.Begin(Work{ID: "req", Method: "unary", Path: "/Service/Method"})
			done()
		}
	})
}

// Oldest runs only when an event fires, but it sorts, so it is worth knowing
// it stays cheap with a full registry.
func BenchmarkRegistryOldest(b *testing.B) {
	r := NewRegistry()

	for i := range 1000 {
		_, _ = r.Begin(Work{ID: string(rune(i)), Path: "/x"})
	}

	now := time.Now()

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		_ = r.Oldest(10, now)
	}
}
