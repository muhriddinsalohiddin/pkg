package watchdog

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Report renders the event as plain text, for a log line, a file or a chat
// message. who names the process — a service and replica, say — because a
// report that does not say where it came from is half a report.
//
// Callers that want another shape or another language should read the Event
// instead; everything in here comes from its exported fields.
func (ev Event) Report(who string) string {
	var b strings.Builder

	if who = strings.TrimSpace(who); who != "" {
		fmt.Fprintf(&b, "watchdog %s\n", who)
	} else {
		b.WriteString("watchdog\n")
	}

	fmt.Fprintf(&b, "%s\n", ev.Time.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "reason: %s\n", strings.Join(ev.Reasons, "; "))
	fmt.Fprintf(&b, "heap %s | rss~ %s | cpu %.0f%% | goroutines %d | in flight %d\n",
		FormatBytes(ev.HeapBytes), FormatBytes(ev.RSSBytes),
		ev.CPUPercent, ev.Goroutines, ev.InFlight)

	if len(ev.Entries) == 0 {
		b.WriteString("nothing in flight\n")
	} else {
		b.WriteString("longest running:\n")

		for i, e := range ev.Entries {
			fmt.Fprintf(&b, "%d) %s %s %s", i+1,
				e.Age.Round(100*time.Millisecond), e.Method, e.Path)

			if e.Detail != "" {
				fmt.Fprintf(&b, " %s", e.Detail)
			}

			if e.Caller != "" {
				fmt.Fprintf(&b, " caller=%s", e.Caller)
			}

			if e.IP != "" {
				fmt.Fprintf(&b, " ip=%s", e.IP)
			}

			if e.BodySize > 0 {
				fmt.Fprintf(&b, " body=%s", FormatBytes(uint64(e.BodySize)))
			}

			if e.ID != "" {
				fmt.Fprintf(&b, " id=%s", e.ID)
			}

			b.WriteString("\n")
		}
	}

	// Missing profiles are reported, not silently absent: an empty block
	// profile usually means nobody switched block profiling on, and that is
	// worth saying once rather than leaving a zero-byte file behind.
	for _, p := range sortedKeys(ev.ProfileErrs) {
		fmt.Fprintf(&b, "no %s profile: %s\n", p, ev.ProfileErrs[p])
	}

	for _, f := range ev.Files {
		fmt.Fprintf(&b, "file: %s\n", f)
	}

	return b.String()
}

func sortedKeys(m map[Profile]string) []Profile {
	out := make([]Profile, 0, len(m))
	for p := range m {
		out = append(out, p)
	}

	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })

	return out
}

// FormatBytes renders a byte count the way a person reads it.
func FormatBytes(n uint64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KiB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
