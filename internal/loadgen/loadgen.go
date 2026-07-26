// Package loadgen is the shared measurement core for all subscriber-side load
// generators. One Recorder per simulated viewer session computes the metric
// set defined in docs/03-experiment-design.md; one Manager per process owns N
// concurrent sessions and exposes restart/results endpoints for expctl.
//
// Using the same recorder for MoQ, WebRTC, and HLS is what makes the
// cross-protocol numbers comparable.
package loadgen

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/sushantlokhande14/edgecast/internal/admin"
)

const (
	jitterBufMicros      = 150_000 // simulated playout buffer for frame paths
	stallThresholdMicros = 100_000 // starvation below this is absorbed as jitter
)

var (
	labels      = []string{"protocol", "region"}
	startupHist = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "edgecast_sub_startup_seconds",
		Help:    "Session start to first media rendered",
		Buckets: []float64{0.05, 0.1, 0.15, 0.2, 0.3, 0.5, 0.75, 1, 1.5, 2, 3, 5, 8, 13},
	}, labels)
	e2eHist = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "edgecast_sub_e2e_seconds",
		Help:    "Publisher stamp to arrival",
		Buckets: []float64{0.02, 0.05, 0.1, 0.15, 0.2, 0.3, 0.5, 0.75, 1, 1.5, 2, 3, 5, 10, 20},
	}, labels)
	bytesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "edgecast_sub_bytes_total",
		Help: "Media payload bytes received",
	}, labels)
	framesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "edgecast_sub_frames_total",
		Help: "Media units (frames, packets, segments) received",
	}, labels)
	stallsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "edgecast_sub_stalls_total",
		Help: "Playout starvation events",
	}, labels)
	stalledSeconds = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "edgecast_sub_stalled_seconds_total",
		Help: "Total time the simulated playhead was starved",
	}, labels)
	sessionsActive = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "edgecast_sub_sessions_active",
		Help: "Configured concurrent sessions",
	}, labels)
	sessionErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "edgecast_sub_session_errors_total",
		Help: "Session attempts that ended in error (sessions auto-reconnect)",
	}, labels)
)

type SecondSample struct {
	Unix    int64   `json:"t"`
	Bytes   int64   `json:"bytes"`
	Frames  int     `json:"frames"`
	StallMs float64 `json:"stall_ms"`
}

// Result is the JSON snapshot expctl collects per session per run.
type Result struct {
	ID          string         `json:"id"`
	RunID       string         `json:"run_id"`
	Protocol    string         `json:"protocol"`
	Region      string         `json:"region"`
	StartUnixMs int64          `json:"start_unix_ms"`
	StartupMs   float64        `json:"startup_ms"`
	StallCount  int            `json:"stall_count"`
	StalledMs   float64        `json:"stalled_ms"`
	JitterMs    float64        `json:"jitter_ms"`
	E2eP50Ms    float64        `json:"e2e_p50_ms"`
	E2eP95Ms    float64        `json:"e2e_p95_ms"`
	Bytes       int64          `json:"bytes"`
	Errors      int            `json:"errors"`
	LastError   string         `json:"last_error,omitempty"`
	Seconds     []SecondSample `json:"seconds"`
}

// Recorder scores one viewer session. Safe for concurrent use (MoQ groups
// arrive on parallel streams).
type Recorder struct {
	// ManualStartup: session code calls MarkStartup itself (HLS buffers
	// before playback starts). AutoStall=false likewise hands stall
	// accounting to the session's own buffer model.
	ManualStartup bool
	AutoStall     bool

	mu        sync.Mutex
	res       Result
	started   time.Time
	startupSet bool
	prevArr   int64 // unix micros of previous media arrival
	prevPTS   uint64
	maxPTS    uint64
	jitterMic float64
	e2e       []float64 // ms samples, capped reservoir
	seconds   map[int64]*SecondSample

	obsStartup prometheus.Observer
	obsE2e     prometheus.Observer
	ctrBytes   prometheus.Counter
	ctrFrames  prometheus.Counter
	ctrStalls  prometheus.Counter
	ctrStallSec prometheus.Counter
	ctrErrors  prometheus.Counter
}

func newRecorder(id, runID, protocol, region string, start time.Time) *Recorder {
	return &Recorder{
		AutoStall: true,
		res: Result{
			ID: id, RunID: runID, Protocol: protocol, Region: region,
			StartUnixMs: start.UnixMilli(), StartupMs: -1,
		},
		started:     start,
		seconds:     map[int64]*SecondSample{},
		obsStartup:  startupHist.WithLabelValues(protocol, region),
		obsE2e:      e2eHist.WithLabelValues(protocol, region),
		ctrBytes:    bytesTotal.WithLabelValues(protocol, region),
		ctrFrames:   framesTotal.WithLabelValues(protocol, region),
		ctrStalls:   stallsTotal.WithLabelValues(protocol, region),
		ctrStallSec: stalledSeconds.WithLabelValues(protocol, region),
		ctrErrors:   sessionErrors.WithLabelValues(protocol, region),
	}
}

func (r *Recorder) second(sec int64) *SecondSample {
	s, ok := r.seconds[sec]
	if !ok {
		s = &SecondSample{Unix: sec}
		r.seconds[sec] = s
	}
	return s
}

// Media records one received media unit. ptsMicros is the publisher-side
// stamp (valid cross-clock because everything shares one host).
func (r *Recorder) Media(now time.Time, ptsMicros uint64, nbytes int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	nowMic := now.UnixMicro()

	r.res.Bytes += int64(nbytes)
	ss := r.second(now.Unix())
	ss.Bytes += int64(nbytes)
	ss.Frames++
	r.ctrBytes.Add(float64(nbytes))
	r.ctrFrames.Inc()

	if e2e := float64(nowMic-int64(ptsMicros)) / 1000.0; e2e >= 0 {
		r.obsE2e.Observe(e2e / 1000.0)
		if len(r.e2e) < 8192 {
			r.e2e = append(r.e2e, e2e)
		} else {
			r.e2e[rand.Intn(len(r.e2e))] = e2e
		}
	}

	if r.prevArr == 0 {
		if !r.ManualStartup {
			r.markStartupLocked(now)
		}
		r.prevArr, r.prevPTS, r.maxPTS = nowMic, ptsMicros, ptsMicros
		return
	}

	// Stall model: the playout buffer holds jitterBuf worth of media; arrival
	// silence beyond that starves the playhead until this unit arrived.
	if r.AutoStall {
		if gap := nowMic - r.prevArr; gap > jitterBufMicros+stallThresholdMicros {
			r.stallLocked(now, float64(gap-jitterBufMicros)/1000.0)
		}
	}

	if ptsMicros > r.maxPTS {
		// RFC 3550 interarrival jitter over in-order units.
		d := float64(nowMic-r.prevArr) - float64(ptsMicros-r.prevPTS)
		if d < 0 {
			d = -d
		}
		r.jitterMic += (d - r.jitterMic) / 16
		r.prevPTS = ptsMicros
		r.maxPTS = ptsMicros
	}
	r.prevArr = nowMic
}

func (r *Recorder) markStartupLocked(now time.Time) {
	if r.startupSet {
		return
	}
	r.startupSet = true
	d := now.Sub(r.started)
	r.res.StartupMs = float64(d.Microseconds()) / 1000.0
	r.obsStartup.Observe(d.Seconds())
}

// MarkStartup is for ManualStartup sessions (HLS: initial buffer filled).
func (r *Recorder) MarkStartup(now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.markStartupLocked(now)
}

func (r *Recorder) stallLocked(now time.Time, ms float64) {
	r.res.StallCount++
	r.res.StalledMs += ms
	r.second(now.Unix()).StallMs += ms
	r.ctrStalls.Inc()
	r.ctrStallSec.Add(ms / 1000.0)
}

// AddStall is for sessions running their own buffer model (HLS underruns).
func (r *Recorder) AddStall(now time.Time, ms float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stallLocked(now, ms)
}

func (r *Recorder) sessionError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.res.Errors++
	r.res.LastError = err.Error()
	r.ctrErrors.Inc()
}

// Snapshot returns the current Result with derived percentiles.
func (r *Recorder) Snapshot() Result {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.res
	out.JitterMs = r.jitterMic / 1000.0
	if n := len(r.e2e); n > 0 {
		s := make([]float64, n)
		copy(s, r.e2e)
		sort.Float64s(s)
		out.E2eP50Ms = s[n/2]
		out.E2eP95Ms = s[(n*95)/100]
	}
	out.Seconds = make([]SecondSample, 0, len(r.seconds))
	for _, ss := range r.seconds {
		out.Seconds = append(out.Seconds, *ss)
	}
	sort.Slice(out.Seconds, func(i, j int) bool { return out.Seconds[i].Unix < out.Seconds[j].Unix })
	return out
}

// SessionFunc runs one viewer session until ctx ends or the transport fails.
type SessionFunc func(ctx context.Context, rec *Recorder) error

// Manager owns the concurrent sessions of one load generator process.
type Manager struct {
	protocol, region string
	defaultCount     int
	run              SessionFunc
	configure        func(rec *Recorder)

	mu      sync.Mutex
	baseCtx context.Context
	cancel  context.CancelFunc
	byRun   map[string][]*Recorder
	runs    []string // insertion order, for pruning
	runSeq  int
}

func NewManager(protocol, region string, defaultCount int, run SessionFunc) *Manager {
	return &Manager{protocol: protocol, region: region, defaultCount: defaultCount, run: run, byRun: map[string][]*Recorder{}}
}

// Configure sets a hook applied to every new Recorder (e.g. HLS switching to
// manual startup/stall accounting).
func (m *Manager) Configure(f func(rec *Recorder)) { m.configure = f }

// Start launches the initial session set under runID "boot".
func (m *Manager) Start(ctx context.Context) {
	m.mu.Lock()
	m.baseCtx = ctx
	m.mu.Unlock()
	m.Restart("boot", m.defaultCount)
}

// Restart tears down all sessions and starts count fresh ones under runID,
// so startup delay is measured under whatever network profile is in force.
func (m *Manager) Restart(runID string, count int) {
	if count <= 0 {
		count = m.defaultCount
	}
	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
	}
	ctx, cancel := context.WithCancel(m.baseCtx)
	m.cancel = cancel
	m.runSeq++
	recs := make([]*Recorder, count)
	now := time.Now()
	for i := range recs {
		rec := newRecorder(fmt.Sprintf("%s-%s-r%d-s%d", m.protocol, m.region, m.runSeq, i), runID, m.protocol, m.region, now)
		if m.configure != nil {
			m.configure(rec)
		}
		recs[i] = rec
	}
	m.byRun[runID] = recs
	m.runs = append(m.runs, runID)
	for len(m.runs) > 4 {
		delete(m.byRun, m.runs[0])
		m.runs = m.runs[1:]
	}
	m.mu.Unlock()

	sessionsActive.WithLabelValues(m.protocol, m.region).Set(float64(count))
	log.Printf("%s/%s: starting %d sessions for run %q", m.protocol, m.region, count, runID)
	for _, rec := range recs {
		go m.session(ctx, rec)
	}
}

func (m *Manager) session(ctx context.Context, rec *Recorder) {
	// Small stagger so N sessions don't dial in the same millisecond.
	select {
	case <-time.After(time.Duration(rand.Intn(400)) * time.Millisecond):
	case <-ctx.Done():
		return
	}
	for ctx.Err() == nil {
		err := m.run(ctx, rec)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			rec.sessionError(err)
		}
		// Viewer with auto-reconnect: the downtime shows up as stalls.
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
		}
	}
}

func (m *Manager) results(runID string) []Result {
	m.mu.Lock()
	recs := m.byRun[runID]
	m.mu.Unlock()
	out := make([]Result, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.Snapshot())
	}
	return out
}

// RegisterHandlers exposes the expctl control surface.
func (m *Manager) RegisterHandlers(a *admin.Server) {
	a.Handle("/sessions/restart", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			RunID string `json:"run_id"`
			Count int    `json:"count"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RunID == "" {
			http.Error(w, "body must be {run_id, count?}", http.StatusBadRequest)
			return
		}
		m.Restart(req.RunID, req.Count)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "run_id": req.RunID})
	})
	a.Handle("/sessions/results", func(w http.ResponseWriter, r *http.Request) {
		runID := r.URL.Query().Get("run_id")
		if runID == "" {
			http.Error(w, "run_id required", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(m.results(runID))
	})
}
