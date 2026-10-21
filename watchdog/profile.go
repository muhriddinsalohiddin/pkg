package watchdog

import (
	"bytes"
	"context"
	"fmt"
	"runtime"
	"runtime/pprof"
	"time"
)

// Profile is one kind of evidence collected when a trigger fires. Pick the
// ones that answer the question you have: a heap profile says what is holding
// memory, a goroutine dump says what everything is waiting on, a CPU profile
// says where the time goes.
type Profile string

const (
	// Heap — live objects by allocation site. The usual first question.
	Heap Profile = "heap"
	// Goroutine — a stack for every goroutine. Answers "what are those
	// thousand goroutines doing", which a count alone never does.
	Goroutine Profile = "goroutine"
	// Allocs — all allocations since the process started, not just live
	// ones. Useful for churn that the garbage collector keeps up with.
	Allocs Profile = "allocs"
	// ThreadCreate — OS threads and where they came from.
	ThreadCreate Profile = "threadcreate"
	// Block — time spent blocked on synchronisation. Empty unless
	// Config.BlockProfileRate is set, because the accounting runs always,
	// not only when a trigger fires.
	Block Profile = "block"
	// Mutex — contended mutex holders. Empty unless
	// Config.MutexProfileFraction is set, for the same reason.
	Mutex Profile = "mutex"
	// CPU — a sampled CPU profile taken over Config.CPUProfile. Unlike the
	// others this one takes time to collect, so the watchdog is blind while
	// it runs.
	CPU Profile = "cpu"
)

// DefaultProfiles is what to collect when Config.Profiles is empty: the three
// that cost nothing to have switched on and answer most questions.
var DefaultProfiles = []Profile{Heap, Goroutine, CPU}

// lookupProfiles are the ones pprof.Lookup serves. CPU is not among them —
// it is recorded over a window rather than read at an instant.
var lookupProfiles = map[Profile]bool{
	Heap: true, Goroutine: true, Allocs: true,
	ThreadCreate: true, Block: true, Mutex: true,
}

// Valid reports whether p is a profile this package knows.
func (p Profile) Valid() bool { return p == CPU || lookupProfiles[p] }

// fileName is what the profile is saved as. The .pb.gz is not decoration:
// pprof writes a gzipped protobuf at debug 0, so `go tool pprof` opens these
// files exactly as it opens one fetched from /debug/pprof.
func (p Profile) fileName() string { return string(p) + ".pb.gz" }

// takeLookup collects one of the instant profiles.
func takeLookup(p Profile) ([]byte, error) {
	prof := pprof.Lookup(string(p))
	if prof == nil {
		return nil, fmt.Errorf("watchdog: no profile named %q", p)
	}

	var buf bytes.Buffer
	if err := prof.WriteTo(&buf, 0); err != nil {
		return nil, fmt.Errorf("watchdog: write %s profile: %w", p, err)
	}

	return buf.Bytes(), nil
}

// takeCPU records the CPU for d. It fails when a profile is already running,
// for example one requested through /debug/pprof.
func takeCPU(ctx context.Context, d time.Duration) ([]byte, error) {
	var buf bytes.Buffer
	if err := pprof.StartCPUProfile(&buf); err != nil {
		return nil, fmt.Errorf("watchdog: start cpu profile: %w", err)
	}

	select {
	case <-ctx.Done():
	case <-time.After(d):
	}

	pprof.StopCPUProfile()

	return buf.Bytes(), nil
}

// enableRates switches on the two profiles that are off by default.
//
// They are separate from asking for the profile because they are paid for
// differently: Heap and Goroutine cost only when collected, while Block and
// Mutex account on every blocking operation for as long as the process runs.
// Asking for those profiles without setting a rate yields an empty one, and
// the event says so.
func enableRates(cfg Config) {
	if cfg.BlockProfileRate > 0 {
		runtime.SetBlockProfileRate(cfg.BlockProfileRate)
	}

	if cfg.MutexProfileFraction > 0 {
		runtime.SetMutexProfileFraction(cfg.MutexProfileFraction)
	}
}

// rateMissing explains an empty Block or Mutex profile rather than leaving a
// zero-byte file for someone to puzzle over.
func rateMissing(p Profile, cfg Config) string {
	switch {
	case p == Block && cfg.BlockProfileRate <= 0:
		return "block profiling is off; set Config.BlockProfileRate"
	case p == Mutex && cfg.MutexProfileFraction <= 0:
		return "mutex profiling is off; set Config.MutexProfileFraction"
	default:
		return ""
	}
}
