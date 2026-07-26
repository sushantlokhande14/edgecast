package expctl

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/sushantlokhande14/edgecast/internal/loadgen"
)

// Row is one aggregated (profile, protocol) cell of the matrix. Startup and
// error counts cover whole sessions; throughput and stall figures cover only
// the measure window (warmup excluded).
type Row struct {
	Profile, Protocol string
	Runs, Sessions    int
	Errors            int
	StartupP50Ms      float64
	StartupP95Ms      float64
	E2eP50Ms          float64
	E2eP95Ms          float64
	JitterP50Ms       float64
	ThroughputKbps    float64
	StallSecPerMin    float64 // seconds containing starvation, per session-minute
	StalledPct        float64 // starved time as % of the measure window
	RecoveryP50Ms     float64 // NaN when profile has no impairment windows
}

func aggregate(m Matrix, runs []runRecord) []Row {
	type key struct{ profile, protocol string }
	type acc struct {
		runs, sessions, errors                   int
		startups, jitters, e2e50s, e2e95s, recov []float64
		winBytes, winStallMs, winStallSecs       float64
		winSeconds                               float64
	}
	accs := map[key]*acc{}
	var order []key

	for _, rr := range runs {
		k := key{rr.ProfileName, rr.Protocol}
		a, ok := accs[k]
		if !ok {
			a = &acc{}
			accs[k] = a
			order = append(order, k)
		}
		a.runs++
		w0 := rr.StartedUnixMs/1000 + int64(rr.WarmupS)
		w1 := w0 + int64(rr.MeasureS)
		windows := impairmentWindows(rr)
		for _, s := range rr.Sessions {
			a.sessions++
			a.errors += s.Errors
			if s.StartupMs >= 0 {
				a.startups = append(a.startups, s.StartupMs)
			}
			if s.JitterMs > 0 {
				a.jitters = append(a.jitters, s.JitterMs)
			}
			if s.E2eP50Ms > 0 {
				a.e2e50s = append(a.e2e50s, s.E2eP50Ms)
				a.e2e95s = append(a.e2e95s, s.E2eP95Ms)
			}
			for _, sec := range s.Seconds {
				if sec.Unix >= w0 && sec.Unix < w1 {
					a.winBytes += float64(sec.Bytes)
					a.winStallMs += sec.StallMs
					if sec.StallMs > 0 {
						a.winStallSecs++
					}
					a.winSeconds++
				}
			}
			a.recov = append(a.recov, recoveryTimes(s, windows)...)
		}
	}

	rows := make([]Row, 0, len(order))
	for _, k := range order {
		a := accs[k]
		r := Row{
			Profile: k.profile, Protocol: k.protocol,
			Runs: a.runs, Sessions: a.sessions, Errors: a.errors,
			StartupP50Ms: percentile(a.startups, 50),
			StartupP95Ms: percentile(a.startups, 95),
			E2eP50Ms:     percentile(a.e2e50s, 50),
			E2eP95Ms:     percentile(a.e2e95s, 50),
			JitterP50Ms:  percentile(a.jitters, 50),
			RecoveryP50Ms: percentile(a.recov, 50),
		}
		if a.winSeconds > 0 {
			r.ThroughputKbps = a.winBytes * 8 / 1000 / a.winSeconds
			r.StallSecPerMin = a.winStallSecs / (a.winSeconds / 60)
			r.StalledPct = a.winStallMs / (a.winSeconds * 1000) * 100
		}
		rows = append(rows, r)
	}
	return rows
}

// impairmentWindows derives [start, end) unix-second pairs from timeline
// events: loss turning on opens a window, loss returning to zero closes it.
func impairmentWindows(rr runRecord) [][2]int64 {
	var out [][2]int64
	base := rr.StartedUnixMs / 1000
	var openAt int64 = -1
	for _, ev := range rr.Profile.Timeline {
		at := base + int64(ev.AtSeconds)
		if ev.Apply.LossPct > 0 && openAt < 0 {
			openAt = at
		} else if ev.Apply.LossPct == 0 && openAt >= 0 {
			out = append(out, [2]int64{openAt, at})
			openAt = -1
		}
	}
	return out
}

// recoveryTimes: per impairment window, time from impairment end until the
// session's 3s rolling throughput is back to 90% of its pre-window baseline.
func recoveryTimes(s loadgen.Result, windows [][2]int64) []float64 {
	if len(windows) == 0 {
		return nil
	}
	bySec := map[int64]float64{}
	for _, sec := range s.Seconds {
		bySec[sec.Unix] = float64(sec.Bytes)
	}
	rolling3 := func(t int64) float64 {
		return (bySec[t] + bySec[t-1] + bySec[t-2]) / 3
	}
	var out []float64
	for _, w := range windows {
		var base float64
		var n int
		for t := w[0] - 5; t < w[0]; t++ {
			if b, ok := bySec[t]; ok {
				base += b
				n++
			}
		}
		if n == 0 || base == 0 {
			continue
		}
		base /= float64(n)
		for t := w[1]; t < w[1]+30; t++ {
			if rolling3(t) >= 0.9*base {
				out = append(out, float64(t-w[1])*1000)
				break
			}
		}
	}
	return out
}

func percentile(xs []float64, p int) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	idx := (len(s) - 1) * p / 100
	return s[idx]
}

func fmtMs(v float64) string {
	if math.IsNaN(v) {
		return ""
	}
	return fmt.Sprintf("%.0f", v)
}

func writeCSV(path string, rows []Row) error {
	var b strings.Builder
	b.WriteString("profile,protocol,runs,sessions,errors,startup_p50_ms,startup_p95_ms,e2e_p50_ms,e2e_p95_ms,jitter_p50_ms,throughput_kbps,stall_sec_per_min,stalled_pct,recovery_p50_ms\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "%s,%s,%d,%d,%d,%s,%s,%s,%s,%s,%.0f,%.2f,%.2f,%s\n",
			r.Profile, r.Protocol, r.Runs, r.Sessions, r.Errors,
			fmtMs(r.StartupP50Ms), fmtMs(r.StartupP95Ms), fmtMs(r.E2eP50Ms), fmtMs(r.E2eP95Ms), fmtMs(r.JitterP50Ms),
			r.ThroughputKbps, r.StallSecPerMin, r.StalledPct, fmtMs(r.RecoveryP50Ms))
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func writeMarkdown(path string, m Matrix, runs []runRecord, rows []Row) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Results: matrix %q\n\nGenerated %s. %d runs, warmup %ds, measure %ds.\n\n",
		m.Name, time.Now().Format(time.RFC3339), len(runs), m.WarmupSeconds, m.MeasureSeconds)
	b.WriteString("| Profile | Protocol | Sessions | Startup p50/p95 (ms) | E2E p50/p95 (ms) | Jitter p50 (ms) | Throughput (kbps) | Stall s/min | Stalled % | Recovery p50 (ms) | Errors |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "| %s | %s | %d | %s / %s | %s / %s | %s | %.0f | %.2f | %.2f | %s | %d |\n",
			r.Profile, r.Protocol, r.Sessions,
			fmtMs(r.StartupP50Ms), fmtMs(r.StartupP95Ms), fmtMs(r.E2eP50Ms), fmtMs(r.E2eP95Ms), fmtMs(r.JitterP50Ms),
			r.ThroughputKbps, r.StallSecPerMin, r.StalledPct, fmtMs(r.RecoveryP50Ms), r.Errors)
	}
	b.WriteString("\nMetric definitions: docs/03-experiment-design.md. Startup covers the full join (dial included). Throughput and stall figures cover the measure window only.\n")
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
