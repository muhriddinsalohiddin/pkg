package watchdog_test

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/muhriddinsalohiddin/pkg/watchdog"
)

// Watching a service that only cares about goroutine leaks: no heap
// threshold, no CPU profile, nothing paid for that nobody reads.
func ExampleNew_goroutinesOnly() {
	reg := watchdog.NewRegistry()

	wd := watchdog.New(watchdog.Config{
		Goroutines: 5000,
		Profiles:   []watchdog.Profile{watchdog.Goroutine},
		Cooldown:   10 * time.Minute,
		Dir:        "/tmp/watchdog",
	}, reg, func(ev watchdog.Event) {
		fmt.Println(ev.Report("my-service"))
	})

	fmt.Println(wd.Watching())
	// Output: true
}

// Memory and CPU together, with the three profiles that cost nothing to have
// available.
func ExampleNew_memoryAndCPU() {
	wd := watchdog.New(watchdog.Config{
		HeapBytes:  2500 << 20,
		CPUPercent: 100,
		CPUSamples: 2, // two samples in a row, not one busy tick
		Profiles:   []watchdog.Profile{watchdog.Heap, watchdog.Goroutine, watchdog.CPU},
		CPUProfile: 10 * time.Second,
		Cooldown:   5 * time.Minute,
	}, watchdog.NewRegistry(), nil)

	fmt.Println(wd.Watching())
	// Output: true
}

// Block and mutex profiling is off in Go until somebody switches it on, and
// it is charged on every blocking operation rather than only when a trigger
// fires. Ask for the rate as well as the profile, or the profile comes back
// empty with a note saying so.
func ExampleConfig_blockAndMutex() {
	cfg := watchdog.Config{
		Goroutines:           5000,
		Profiles:             []watchdog.Profile{watchdog.Goroutine, watchdog.Block, watchdog.Mutex},
		BlockProfileRate:     10000, // nanoseconds; higher samples less
		MutexProfileFraction: 100,   // one in a hundred contended locks
	}

	fmt.Println(len(cfg.Profiles))
	// Output: 3
}

// Registering work, for an HTTP service. The registry knows nothing about
// any framework: Begin takes a description and returns the function that
// ends it, so the adapter is a few lines wherever it is needed.
func ExampleRegistry_Begin_http() {
	reg := watchdog.NewRegistry("/debug/pprof")

	middleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			entry, done := reg.Begin(watchdog.Work{
				ID:       r.Header.Get("X-Request-Id"),
				Method:   r.Method,
				Path:     r.URL.Path,
				Detail:   r.URL.RawQuery,
				IP:       r.RemoteAddr,
				BodySize: int(r.ContentLength),
			})
			defer done()

			// Once authentication has resolved who this is:
			entry.SetCaller("303303592")

			next.ServeHTTP(w, r)
		})
	}

	_ = middleware

	fmt.Println(reg.Len())
	// Output: 0
}

// The same for gRPC. Written out here rather than shipped as code, so this
// package keeps to the standard library:
//
//	func WatchdogInterceptor(reg *watchdog.Registry) grpc.UnaryServerInterceptor {
//		return func(ctx context.Context, req any, info *grpc.UnaryServerInfo,
//			handler grpc.UnaryHandler) (any, error) {
//			_, done := reg.Begin(watchdog.Work{
//				ID:     requestIDFrom(ctx),
//				Method: "unary",
//				Path:   info.FullMethod,
//			})
//			defer done()
//
//			return handler(ctx, req)
//		}
//	}
//
// Background work started by a request outlives it, so register that too —
// otherwise a report taken while a job holds a gigabyte lists only whatever
// small requests happen to be in flight.
func ExampleRegistry_BeginJob() {
	reg := watchdog.NewRegistry()

	go func() {
		done := reg.BeginJob("sync-documents", "303303592", "req-1", "2026-01-01..2026-09-30")
		defer done()

		// ... the long job ...
		_ = context.Background()
	}()

	fmt.Println("started")
	// Output: started
}
