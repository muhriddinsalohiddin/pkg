package watchdog

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixed replaces the runtime readings so a test can say what the process
// looks like.
func fixed(w *Watchdog, heap, rss, goroutines uint64, cpu time.Duration) {
	w.read = func() (uint64, uint64, uint64) { return heap, rss, goroutines }

	var at time.Time

	w.now = func() time.Time { at = at.Add(time.Second); return at }
	w.cpu = func() time.Duration { return cpu }
}

// Each trigger is independent, and each is off at zero. A watchdog with one
// threshold set must not fire on another measure being high.
func TestTriggersAreIndependent(t *testing.T) {
	const (
		gib  = 1 << 30
		many = 10_000
	)

	tests := []struct {
		name string
		cfg  Config
		heap uint64
		rss  uint64
		gor  uint64
		want string // substring of the reason, empty means no event
	}{
		{name: "heap over", cfg: Config{HeapBytes: gib}, heap: 2 * gib, want: "heap"},
		{name: "heap under", cfg: Config{HeapBytes: gib}, heap: gib / 2},
		{name: "heap off, heap huge", cfg: Config{Goroutines: many}, heap: 100 * gib},
		{name: "rss over", cfg: Config{RSSBytes: gib}, rss: 2 * gib, want: "rss"},
		{name: "rss off, rss huge", cfg: Config{HeapBytes: gib}, rss: 100 * gib},
		{name: "goroutines over", cfg: Config{Goroutines: many}, gor: many, want: "goroutines"},
		{name: "goroutines under", cfg: Config{Goroutines: many}, gor: many - 1},
		{name: "goroutines off, count huge", cfg: Config{HeapBytes: gib}, gor: 1_000_000},
		{name: "nothing set", cfg: Config{}, heap: 100 * gib, gor: 1_000_000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.cfg.Profiles = []Profile{}
			tt.cfg.Dir = ""

			w := New(tt.cfg, NewRegistry(), nil)
			w.profiles = nil // no profiles: this test is about triggers

			fixed(w, tt.heap, tt.rss, tt.gor, 0)

			ev, ok := w.check(context.Background())

			if tt.want == "" {
				if ok {
					t.Fatalf("fired with reasons %v, want no event", ev.Reasons)
				}

				return
			}

			if !ok {
				t.Fatal("no event")
			}

			if !strings.Contains(strings.Join(ev.Reasons, ";"), tt.want) {
				t.Errorf("reasons %v, want one mentioning %q", ev.Reasons, tt.want)
			}
		})
	}
}

// Watching says whether anything at all is being watched, so a service can
// log that its watchdog is inert rather than silently never firing.
func TestWatching(t *testing.T) {
	if New(Config{}, nil, nil).Watching() {
		t.Error("a config with no threshold reports that it is watching")
	}

	for _, cfg := range []Config{
		{HeapBytes: 1}, {RSSBytes: 1}, {Goroutines: 1}, {CPUPercent: 1},
	} {
		if !New(cfg, nil, nil).Watching() {
			t.Errorf("%+v reports that it is watching nothing", cfg)
		}
	}
}

// Only the profiles asked for are collected. This is the point of making them
// configurable: a service that only wants to know about goroutine leaks should
// not pay for a CPU profile it never reads.
func TestOnlyRequestedProfilesAreCollected(t *testing.T) {
	tests := []struct {
		name string
		ask  []Profile
		want []Profile
	}{
		{name: "goroutine only", ask: []Profile{Goroutine}, want: []Profile{Goroutine}},
		{name: "heap only", ask: []Profile{Heap}, want: []Profile{Heap}},
		{name: "two", ask: []Profile{Heap, Allocs}, want: []Profile{Heap, Allocs}},
		{name: "unknown dropped", ask: []Profile{Heap, "nonsense"}, want: []Profile{Heap}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := New(Config{HeapBytes: 1, Profiles: tt.ask}, NewRegistry(), nil)
			fixed(w, 1<<20, 0, 0, 0)

			ev, ok := w.check(context.Background())
			if !ok {
				t.Fatal("no event")
			}

			if len(ev.Profiles) != len(tt.want) {
				t.Fatalf("collected %d profiles (%v), want %d (%v)",
					len(ev.Profiles), keysOf(ev.Profiles), len(tt.want), tt.want)
			}

			for _, p := range tt.want {
				if len(ev.Profiles[p]) == 0 {
					t.Errorf("%s profile is missing or empty", p)
				}
			}
		})
	}
}

func keysOf(m map[Profile][]byte) []Profile {
	out := make([]Profile, 0, len(m))
	for p := range m {
		out = append(out, p)
	}

	return out
}

// Every collected profile is a gzipped protobuf, which is what makes the
// files readable by `go tool pprof` without any conversion.
func TestProfilesArePprofFiles(t *testing.T) {
	w := New(Config{
		HeapBytes: 1,
		Profiles:  []Profile{Heap, Goroutine, Allocs, ThreadCreate},
	}, NewRegistry(), nil)
	fixed(w, 1<<20, 0, 0, 0)

	ev, ok := w.check(context.Background())
	if !ok {
		t.Fatal("no event")
	}

	for p, data := range ev.Profiles {
		if len(data) < 2 || data[0] != 0x1f || data[1] != 0x8b {
			t.Errorf("%s profile is not gzip: % x", p, data[:min(2, len(data))])
		}
	}

	if bytes.Equal(ev.Profiles[Heap], ev.Profiles[Goroutine]) {
		t.Error("the heap and goroutine profiles are identical")
	}
}

// Block and mutex profiling is off in Go by default. Asking for the profile
// without switching on the accounting gives an empty one, and the event has
// to say why rather than leave an empty file behind.
func TestBlockAndMutexSayWhyTheyAreEmpty(t *testing.T) {
	w := New(Config{HeapBytes: 1, Profiles: []Profile{Block, Mutex}}, NewRegistry(), nil)
	fixed(w, 1<<20, 0, 0, 0)

	ev, ok := w.check(context.Background())
	if !ok {
		t.Fatal("no event")
	}

	for _, p := range []Profile{Block, Mutex} {
		note, ok := ev.ProfileErrs[p]
		if !ok {
			t.Errorf("%s profile collected with no rate set and no explanation", p)
			continue
		}

		if !strings.Contains(note, "set Config.") {
			t.Errorf("%s note %q does not say what to set", p, note)
		}
	}

	if !strings.Contains(ev.Report("svc"), "no block profile") {
		t.Error("the report does not mention the missing block profile")
	}
}

// CPU must hold for CPUSamples in a row, so one busy tick is not a spike.
func TestCPUNeedsSustainedLoad(t *testing.T) {
	w := New(Config{CPUPercent: 100, CPUSamples: 2, Profiles: []Profile{}}, NewRegistry(), nil)
	w.profiles = nil

	var now time.Time

	cpu := time.Duration(0)

	w.read = func() (uint64, uint64, uint64) { return 0, 0, 0 }
	w.now = func() time.Time { now = now.Add(time.Second); return now }
	w.cpu = func() time.Duration { cpu += 2 * time.Second; return cpu } // 200%

	if _, ok := w.check(context.Background()); ok {
		t.Fatal("fired on the first high sample; CPUSamples is 2")
	}

	if _, ok := w.check(context.Background()); !ok {
		t.Fatal("did not fire on the second high sample")
	}
}

// Cooldown keeps a service that stays over the threshold from reporting on
// every tick.
func TestCooldown(t *testing.T) {
	w := New(Config{HeapBytes: 1, Cooldown: time.Hour, Profiles: []Profile{}}, NewRegistry(), nil)
	w.profiles = nil
	fixed(w, 1<<20, 0, 0, 0)

	if _, ok := w.check(context.Background()); !ok {
		t.Fatal("no first event")
	}

	if _, ok := w.check(context.Background()); ok {
		t.Error("fired again inside the cooldown")
	}
}

// The files are written with the profile's own name, so a directory listing
// says what each one is.
func TestSaveWritesNamedFiles(t *testing.T) {
	dir := t.TempDir()

	w := New(Config{
		HeapBytes: 1,
		Profiles:  []Profile{Heap, Goroutine},
		Dir:       dir,
	}, NewRegistry(), nil)
	fixed(w, 1<<20, 0, 0, 0)

	ev, ok := w.check(context.Background())
	if !ok {
		t.Fatal("no event")
	}

	want := map[string]bool{"report.txt": false, "heap.pb.gz": false, "goroutine.pb.gz": false}

	for _, f := range ev.Files {
		for suffix := range want {
			if strings.HasSuffix(filepath.Base(f), suffix) {
				want[suffix] = true
			}
		}
	}

	for suffix, found := range want {
		if !found {
			t.Errorf("no file ending in %q; got %v", suffix, ev.Files)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}

	if len(entries) != len(want) {
		t.Errorf("%d files in the directory, want %d", len(entries), len(want))
	}
}

// Keep bounds what accumulates in Dir; a watchdog that fills the disk is a
// worse problem than the one it reports.
func TestPruneKeepsTheNewest(t *testing.T) {
	dir := t.TempDir()

	for _, name := range []string{"20260101_000001_heap.pb.gz", "20260101_000002_heap.pb.gz", "20260101_000003_heap.pb.gz"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	w := New(Config{Dir: dir, Keep: 2}, nil, nil)
	w.prune()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("%d files left, want 2", len(entries))
	}

	if entries[0].Name() != "20260101_000002_heap.pb.gz" {
		t.Errorf("oldest kept is %s; prune removed the wrong end", entries[0].Name())
	}
}

// A panic in one round ends that round, not the watchdog.
func TestRoundRecoversFromPanic(t *testing.T) {
	w := New(Config{HeapBytes: 1, Profiles: []Profile{}}, NewRegistry(), nil)
	w.profiles = nil
	fixed(w, 1<<20, 0, 0, 0)

	var got any

	w.OnPanic = func(r any) { got = r }
	w.notify = func(Event) { panic("boom") }

	w.round(context.Background())

	if got != "boom" {
		t.Errorf("OnPanic received %v, want \"boom\"", got)
	}
}
