package watchdog

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// maxDetailLen keeps one long query string or job description from filling a
// report.
const maxDetailLen = 160

// JobMethod marks an entry that is not a request but a background job: the
// request that started it has long since answered.
const JobMethod = "job"

// Entry is one unit of work in flight — a request being served, or a job
// running behind one. Everything except the caller is fixed at registration;
// the caller is filled in later, once authentication has resolved it.
type Entry struct {
	ID       string // request id, for finding the logs
	Method   string // HTTP method, gRPC "unary", or JobMethod
	Path     string // route, gRPC full method, or job name
	Detail   string // query string, job description; trimmed
	IP       string
	BodySize int
	Start    time.Time

	mu     sync.Mutex
	caller string
}

// SetCaller records who is behind the work — a TIN, a login — once it is
// known. Safe to call from a different goroutine than the one that started it,
// and safe to not call at all.
func (e *Entry) SetCaller(caller string) {
	if e == nil || caller == "" {
		return
	}

	e.mu.Lock()
	e.caller = strings.Clone(caller)
	e.mu.Unlock()
}

// Snapshot is a copy of an Entry taken at one moment.
type Snapshot struct {
	ID       string        `json:"req_id"`
	Method   string        `json:"method"`
	Path     string        `json:"path"`
	Detail   string        `json:"detail,omitempty"`
	IP       string        `json:"ip,omitempty"`
	Caller   string        `json:"caller,omitempty"`
	BodySize int           `json:"body_size"`
	Age      time.Duration `json:"age"`
}

// Registry tracks what the process is working on right now.
//
// It has no idea what framework you use. Begin returns the entry and the
// function that ends it, and an HTTP middleware or a gRPC interceptor is a
// few lines on top — see the package example. That is deliberate: a monitoring
// package that drags in a web framework is one more reason not to adopt it.
type Registry struct {
	mu      sync.Mutex
	entries map[*Entry]struct{}
	skip    []string
}

// NewRegistry returns an empty registry. Work whose path starts with one of
// skipPrefixes is not tracked — point it at the profiling endpoints, whose
// own long-running requests would otherwise top every report.
func NewRegistry(skipPrefixes ...string) *Registry {
	return &Registry{entries: make(map[*Entry]struct{}), skip: skipPrefixes}
}

// Begin registers work and returns it with the function that ends it. A nil
// registry is usable and does nothing, so a caller need not check.
//
//	entry, done := reg.Begin(watchdog.Work{Method: "unary", Path: info.FullMethod})
//	defer done()
func (r *Registry) Begin(w Work) (*Entry, func()) {
	if r == nil || r.skipped(w.Path) {
		return nil, func() {}
	}

	e := &Entry{
		ID:       strings.Clone(w.ID),
		Method:   strings.Clone(w.Method),
		Path:     strings.Clone(w.Path),
		Detail:   trim(w.Detail),
		IP:       strings.Clone(w.IP),
		BodySize: w.BodySize,
		caller:   strings.Clone(w.Caller),
		Start:    time.Now(),
	}

	r.mu.Lock()
	r.entries[e] = struct{}{}
	r.mu.Unlock()

	return e, func() {
		r.mu.Lock()
		delete(r.entries, e)
		r.mu.Unlock()
	}
}

// BeginJob registers a background job. Work handed to a goroutine outlives the
// request that started it, so without this a report taken while a job holds a
// gigabyte lists only the small requests that happen to be in flight and says
// nothing about the job.
func (r *Registry) BeginJob(name, caller, reqID, detail string) func() {
	_, done := r.Begin(Work{
		ID: reqID, Method: JobMethod, Path: name, Caller: caller, Detail: detail,
	})

	return done
}

// Work describes one unit of work to Begin.
type Work struct {
	ID       string
	Method   string
	Path     string
	Detail   string
	IP       string
	Caller   string
	BodySize int
}

func trim(s string) string {
	if len(s) > maxDetailLen {
		return strings.Clone(s[:maxDetailLen]) + "…"
	}

	return strings.Clone(s)
}

// Len is the number of units of work in flight.
func (r *Registry) Len() int {
	if r == nil {
		return 0
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.entries)
}

// Oldest returns up to n entries, longest-running first. This is the part of a
// report people actually read: what was running when it went wrong.
func (r *Registry) Oldest(n int, now time.Time) []Snapshot {
	if r == nil {
		return nil
	}

	r.mu.Lock()
	out := make([]Snapshot, 0, len(r.entries))

	for e := range r.entries {
		e.mu.Lock()
		caller := e.caller
		e.mu.Unlock()

		out = append(out, Snapshot{
			ID: e.ID, Method: e.Method, Path: e.Path, Detail: e.Detail,
			IP: e.IP, Caller: caller, BodySize: e.BodySize, Age: now.Sub(e.Start),
		})
	}
	r.mu.Unlock()

	sort.Slice(out, func(i, j int) bool { return out[i].Age > out[j].Age })

	if n > 0 && len(out) > n {
		out = out[:n]
	}

	return out
}

func (r *Registry) skipped(path string) bool {
	for _, p := range r.skip {
		if strings.HasPrefix(path, p) {
			return true
		}
	}

	return false
}
