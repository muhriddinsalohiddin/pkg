// Package watchdog watches a Go process from inside it and, when something
// spikes, writes down what was happening.
//
// The problem it solves: a service goes to a gigabyte of heap for forty
// seconds and comes back. By the time anyone reaches for /debug/pprof the
// moment has passed, and all that is left is a graph with a bump in it. A
// watchdog samples cheaply, and when a threshold is crossed it captures the
// profiles and the list of what was in flight — at the moment it matters.
//
// Pick what to watch and what to collect:
//
//	wd := watchdog.New(watchdog.Config{
//		HeapBytes:  2500 << 20,                 // 0 to ignore heap
//		Goroutines: 5000,                       // 0 to ignore goroutines
//		CPUPercent: 100, CPUSamples: 2,         // 0 to ignore CPU
//		Profiles:   []watchdog.Profile{watchdog.Heap, watchdog.Goroutine},
//		Cooldown:   5 * time.Minute,
//		Dir:        "/tmp/watchdog",
//	}, reg, notify)
//	go wd.Run(ctx)
//
// Cost, measured: one sample is a handful of runtime metrics and a getrusage,
// under a microsecond, once per Interval. Registering a request is about
// 150 ns. What costs is the capture, and Cooldown bounds how often that can
// happen.
//
// The files are ordinary pprof profiles — `go tool pprof` reads them exactly
// as it reads one fetched from /debug/pprof, because they are written by the
// same code.
package watchdog

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime/metrics"
	"sort"
	"syscall"
	"time"
)

// Config says what to watch, what to collect and where to put it.
//
// Every threshold is off at zero. A watchdog with no threshold set never
// fires, which is the way to switch it off without removing it from the
// process.
type Config struct {
	// Interval between samples. Default 5s.
	Interval time.Duration

	// --- triggers, each off at zero ---

	// HeapBytes fires when live heap objects reach it.
	HeapBytes uint64
	// RSSBytes fires on memory the runtime has mapped and not returned.
	// Heap is usually the better signal; this one also catches memory held
	// outside the Go heap.
	RSSBytes uint64
	// Goroutines fires on the goroutine count. Good for leaks, which show
	// up here long before they show up as memory.
	Goroutines uint64
	// CPUPercent fires on process CPU as a percent of one core — 100 is one
	// core saturated, 250 is two and a half.
	CPUPercent float64
	// CPUSamples is how many samples in a row CPU must stay high. Default 1.
	// Raise it so a single busy tick does not count as a spike.
	CPUSamples int

	// --- what to collect ---

	// Profiles to capture. Empty means DefaultProfiles.
	Profiles []Profile
	// CPUProfile is how long to record when CPU is among Profiles.
	// The watchdog takes no samples while it runs. Default 10s.
	CPUProfile time.Duration
	// BlockProfileRate and MutexProfileFraction switch on the two profiles
	// that Go keeps off by default. They are charged on every blocking
	// operation for as long as the process runs, not only when a trigger
	// fires, so they are asked for separately from the profile itself.
	BlockProfileRate     int
	MutexProfileFraction int

	// --- the report ---

	// Cooldown is the least time between two events. Without it a service
	// that stays over the threshold reports on every tick.
	Cooldown time.Duration
	// TopN is how many in-flight entries an event lists. Default 10.
	TopN int
	// Dir is where profiles are written. Empty keeps none, and the event
	// still carries them in memory.
	Dir string
	// Keep is how many files to leave in Dir. Default 60.
	Keep int
}

// Event is one detected spike.
type Event struct {
	Time       time.Time     `json:"time"`
	Reasons    []string      `json:"reasons"`
	HeapBytes  uint64        `json:"heap_bytes"`
	RSSBytes   uint64        `json:"rss_bytes"`
	CPUPercent float64       `json:"cpu_percent"`
	Goroutines uint64        `json:"goroutines"`
	InFlight   int           `json:"in_flight"`
	Entries    []Snapshot    `json:"entries"`
	Files      []string      `json:"files,omitempty"`
	Took       time.Duration `json:"took"`

	// Profiles holds what was collected, keyed by kind. Not serialised:
	// these are megabytes.
	Profiles map[Profile][]byte `json:"-"`
	// ProfileErrs says why a profile is missing — a CPU profile already
	// running elsewhere, or a block profile nobody switched on.
	ProfileErrs map[Profile]string `json:"profile_errors,omitempty"`
}

// Notifier receives every event. It runs on the watchdog's goroutine, so it
// should not block for long.
type Notifier func(Event)

type sample struct {
	heap, rss, goroutines uint64
	cpuPercent            float64
}

// Watchdog samples the process and reports spikes.
type Watchdog struct {
	cfg      Config
	reg      *Registry
	notify   Notifier
	profiles []Profile

	// OnPanic receives a panic recovered from one sampling round; the
	// watchdog then carries on with the next. Call it from nowhere else.
	OnPanic func(recovered any)

	read func() (heap, rss, goroutines uint64) // replaced in tests
	cpu  func() time.Duration
	now  func() time.Time

	lastCPU  time.Duration
	lastAt   time.Time
	cpuHigh  int
	lastFire time.Time
}

// New builds a watchdog over reg. reg and notify may both be nil.
//
// Profiles named in cfg that this package does not know are dropped, so a
// typo costs that one profile rather than the whole watchdog.
func New(cfg Config, reg *Registry, notify Notifier) *Watchdog {
	if cfg.Interval <= 0 {
		cfg.Interval = 5 * time.Second
	}

	if cfg.CPUSamples <= 0 {
		cfg.CPUSamples = 1
	}

	if cfg.TopN <= 0 {
		cfg.TopN = 10
	}

	if cfg.Keep <= 0 {
		cfg.Keep = 60
	}

	if cfg.CPUProfile <= 0 {
		cfg.CPUProfile = 10 * time.Second
	}

	if notify == nil {
		notify = func(Event) {}
	}

	chosen := cfg.Profiles
	if len(chosen) == 0 {
		chosen = DefaultProfiles
	}

	keep := make([]Profile, 0, len(chosen))

	for _, p := range chosen {
		if p.Valid() {
			keep = append(keep, p)
		}
	}

	enableRates(cfg)

	return &Watchdog{
		cfg: cfg, reg: reg, notify: notify, profiles: keep,
		read: readMetrics, cpu: processCPU, now: time.Now,
	}
}

// Watching reports whether any trigger is set. A watchdog that watches
// nothing is worth saying so about at startup.
func (w *Watchdog) Watching() bool {
	return w.cfg.HeapBytes > 0 || w.cfg.RSSBytes > 0 ||
		w.cfg.Goroutines > 0 || w.cfg.CPUPercent > 0
}

// Run samples until ctx is done.
func (w *Watchdog) Run(ctx context.Context) {
	w.lastCPU, w.lastAt = w.cpu(), w.now()

	t := time.NewTicker(w.cfg.Interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.round(ctx)
		}
	}
}

// round is one sample-and-report pass, isolated so a panic ends only the pass.
func (w *Watchdog) round(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil && w.OnPanic != nil {
			w.OnPanic(r)
		}
	}()

	if ev, ok := w.check(ctx); ok {
		w.notify(ev)
	}
}

// check takes one sample and, when it is a spike past the cooldown, builds
// the event.
func (w *Watchdog) check(ctx context.Context) (Event, bool) {
	s := w.sample()

	if w.cfg.CPUPercent > 0 && s.cpuPercent >= w.cfg.CPUPercent {
		w.cpuHigh++
	} else {
		w.cpuHigh = 0
	}

	var reasons []string

	if w.cfg.HeapBytes > 0 && s.heap >= w.cfg.HeapBytes {
		reasons = append(reasons, fmt.Sprintf("heap %s >= %s",
			FormatBytes(s.heap), FormatBytes(w.cfg.HeapBytes)))
	}

	if w.cfg.RSSBytes > 0 && s.rss >= w.cfg.RSSBytes {
		reasons = append(reasons, fmt.Sprintf("rss %s >= %s",
			FormatBytes(s.rss), FormatBytes(w.cfg.RSSBytes)))
	}

	if w.cfg.Goroutines > 0 && s.goroutines >= w.cfg.Goroutines {
		reasons = append(reasons, fmt.Sprintf("goroutines %d >= %d",
			s.goroutines, w.cfg.Goroutines))
	}

	cpuSpike := w.cfg.CPUPercent > 0 && w.cpuHigh >= w.cfg.CPUSamples
	if cpuSpike {
		reasons = append(reasons, fmt.Sprintf("CPU %.0f%% >= %.0f%% (%s)",
			s.cpuPercent, w.cfg.CPUPercent, time.Duration(w.cfg.CPUSamples)*w.cfg.Interval))
	}

	if len(reasons) == 0 || (!w.lastFire.IsZero() && w.now().Sub(w.lastFire) < w.cfg.Cooldown) {
		return Event{}, false
	}

	w.lastFire = w.now()

	return w.capture(ctx, s, reasons), true
}

func (w *Watchdog) sample() sample {
	heap, rss, goroutines := w.read()

	cpu, at := w.cpu(), w.now()

	var pct float64
	if wall := at.Sub(w.lastAt); wall > 0 {
		pct = float64(cpu-w.lastCPU) / float64(wall) * 100
	}

	w.lastCPU, w.lastAt = cpu, at

	return sample{heap: heap, rss: rss, goroutines: goroutines, cpuPercent: pct}
}

// capture collects the evidence. The in-flight list comes first: it is what
// the spike is made of, and those entries may finish while profiles are taken.
func (w *Watchdog) capture(ctx context.Context, s sample, reasons []string) Event {
	start := w.now()

	ev := Event{
		Time:        start,
		Reasons:     reasons,
		HeapBytes:   s.heap,
		RSSBytes:    s.rss,
		CPUPercent:  s.cpuPercent,
		Goroutines:  s.goroutines,
		InFlight:    w.reg.Len(),
		Entries:     w.reg.Oldest(w.cfg.TopN, start),
		Profiles:    make(map[Profile][]byte, len(w.profiles)),
		ProfileErrs: make(map[Profile]string),
	}

	for _, p := range w.profiles {
		if note := rateMissing(p, w.cfg); note != "" {
			ev.ProfileErrs[p] = note

			continue
		}

		var (
			data []byte
			err  error
		)

		if p == CPU {
			data, err = takeCPU(ctx, w.cfg.CPUProfile)
			// The profile blocked the loop; that time is not a sample.
			w.lastCPU, w.lastAt = w.cpu(), w.now()
		} else {
			data, err = takeLookup(p)
		}

		if err != nil {
			ev.ProfileErrs[p] = err.Error()
		}

		if len(data) > 0 {
			ev.Profiles[p] = data
		}
	}

	ev.Files = w.save(ev)
	ev.Took = w.now().Sub(start)

	return ev
}

// save writes the profiles and the text report, and returns the paths.
func (w *Watchdog) save(ev Event) []string {
	if w.cfg.Dir == "" {
		return nil
	}

	if err := os.MkdirAll(w.cfg.Dir, 0o750); err != nil {
		return nil
	}

	var (
		files []string
		stamp = ev.Time.Format("20060102_150405")
	)

	write := func(name string, data []byte) {
		if len(data) == 0 {
			return
		}

		path := filepath.Join(w.cfg.Dir, stamp+"_"+name)
		if err := os.WriteFile(path, data, 0o600); err == nil {
			files = append(files, path)
		}
	}

	write("report.txt", []byte(ev.Report("")))

	// Sorted so the file list does not shuffle between events for no reason.
	kinds := make([]Profile, 0, len(ev.Profiles))
	for p := range ev.Profiles {
		kinds = append(kinds, p)
	}

	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })

	for _, p := range kinds {
		write(p.fileName(), ev.Profiles[p])
	}

	w.prune()

	return files
}

// prune keeps the newest cfg.Keep files in Dir.
func (w *Watchdog) prune() {
	entries, err := os.ReadDir(w.cfg.Dir)
	if err != nil || len(entries) <= w.cfg.Keep {
		return
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}

	if len(names) <= w.cfg.Keep {
		return
	}

	// The names start with a timestamp, so lexical order is chronological.
	sort.Strings(names)

	for _, name := range names[:len(names)-w.cfg.Keep] {
		_ = os.Remove(filepath.Join(w.cfg.Dir, name))
	}
}

var metricNames = []string{
	"/memory/classes/heap/objects:bytes", // live objects plus garbage not yet swept
	"/memory/classes/total:bytes",
	"/memory/classes/heap/released:bytes",
	"/sched/goroutines:goroutines",
}

// readMetrics reads the heap and an RSS estimate (memory mapped by the runtime
// minus what it has handed back to the OS). It does not stop the world.
func readMetrics() (heap, rss, goroutines uint64) {
	samples := make([]metrics.Sample, len(metricNames))
	for i, name := range metricNames {
		samples[i].Name = name
	}

	metrics.Read(samples)

	value := func(i int) uint64 {
		if samples[i].Value.Kind() != metrics.KindUint64 {
			return 0
		}

		return samples[i].Value.Uint64()
	}

	total, released := value(1), value(2)
	if released < total {
		rss = total - released
	}

	return value(0), rss, value(3)
}

// processCPU is the user plus system CPU time used by the process so far.
func processCPU() time.Duration {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}

	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}
