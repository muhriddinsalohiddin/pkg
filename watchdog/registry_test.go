package watchdog

import (
	"testing"
	"time"
)

// A nil registry is usable. Services wire the watchdog in at startup, and a
// nil check at every call site is the kind of thing that gets forgotten once.
func TestNilRegistryIsUsable(t *testing.T) {
	var r *Registry

	entry, done := r.Begin(Work{Path: "/x"})
	if entry != nil {
		t.Error("a nil registry handed back an entry")
	}

	done()
	entry.SetCaller("303303592")

	if got := r.Len(); got != 0 {
		t.Errorf("Len = %d on a nil registry", got)
	}

	if got := r.Oldest(10, time.Now()); got != nil {
		t.Errorf("Oldest = %v on a nil registry", got)
	}
}

// Work enters the registry and leaves it. The leaving matters most: an entry
// that is never removed makes every later report wrong.
func TestBeginAndEnd(t *testing.T) {
	r := NewRegistry()

	_, done := r.Begin(Work{ID: "req-1", Method: "unary", Path: "/Service/Method"})

	if got := r.Len(); got != 1 {
		t.Fatalf("Len = %d after Begin, want 1", got)
	}

	done()

	if got := r.Len(); got != 0 {
		t.Errorf("Len = %d after the end function, want 0", got)
	}
}

// The caller is usually unknown when the work starts and known a moment
// later, once the token is read.
func TestSetCallerAfterBegin(t *testing.T) {
	r := NewRegistry()

	entry, done := r.Begin(Work{ID: "req-1", Path: "/x"})
	defer done()

	entry.SetCaller("303303592")

	got := r.Oldest(1, time.Now())
	if len(got) != 1 {
		t.Fatalf("%d entries, want 1", len(got))
	}

	if got[0].Caller != "303303592" {
		t.Errorf("caller = %q, want %q", got[0].Caller, "303303592")
	}
}

// Skipped prefixes exist for the profiling endpoints: a request that asks for
// a 30-second CPU profile would otherwise top every report it appears in.
func TestSkipPrefixes(t *testing.T) {
	r := NewRegistry("/debug/pprof", "/health")

	for _, path := range []string{"/debug/pprof/heap", "/health"} {
		if _, done := r.Begin(Work{Path: path}); true {
			done()
		}
	}

	if got := r.Len(); got != 0 {
		t.Errorf("Len = %d; skipped paths were tracked", got)
	}

	_, done := r.Begin(Work{Path: "/v1/documents"})
	defer done()

	if got := r.Len(); got != 1 {
		t.Errorf("Len = %d; a normal path was not tracked", got)
	}
}

// Oldest is the part of a report people read, so the order has to be right.
func TestOldestIsLongestFirst(t *testing.T) {
	r := NewRegistry()

	for _, id := range []string{"slow", "middle", "fast"} {
		_, done := r.Begin(Work{ID: id, Path: "/" + id})
		defer done()

		time.Sleep(2 * time.Millisecond)
	}

	got := r.Oldest(3, time.Now())
	if len(got) != 3 {
		t.Fatalf("%d entries, want 3", len(got))
	}

	if got[0].ID != "slow" || got[2].ID != "fast" {
		t.Errorf("order is %s, %s, %s; want slow, middle, fast",
			got[0].ID, got[1].ID, got[2].ID)
	}
}

// n bounds the list; a thousand in flight should not make a thousand-line
// report.
func TestOldestRespectsN(t *testing.T) {
	r := NewRegistry()

	for i := range 50 {
		_, done := r.Begin(Work{ID: string(rune('a' + i%26)), Path: "/x"})
		defer done()
	}

	if got := len(r.Oldest(5, time.Now())); got != 5 {
		t.Errorf("Oldest(5) returned %d", got)
	}
}

// A long query string must not fill the report.
func TestDetailIsTrimmed(t *testing.T) {
	r := NewRegistry()

	long := ""
	for range 400 {
		long += "x"
	}

	_, done := r.Begin(Work{Path: "/x", Detail: long})
	defer done()

	got := r.Oldest(1, time.Now())
	if len(got) != 1 {
		t.Fatal("no entry")
	}

	if len(got[0].Detail) > maxDetailLen+4 {
		t.Errorf("detail is %d characters, want it trimmed near %d",
			len(got[0].Detail), maxDetailLen)
	}
}

// Background jobs outlive the request that started them, which is the whole
// reason they are registered separately.
func TestBeginJob(t *testing.T) {
	r := NewRegistry()

	done := r.BeginJob("sync-doc", "303303592", "req-1", "factura?ids=0")
	defer done()

	got := r.Oldest(1, time.Now())
	if len(got) != 1 {
		t.Fatal("no entry")
	}

	if got[0].Method != JobMethod {
		t.Errorf("method = %q, want %q", got[0].Method, JobMethod)
	}

	if got[0].Caller != "303303592" {
		t.Errorf("caller = %q", got[0].Caller)
	}
}
