package netem

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/sushantlokhande14/edgecast/internal/admin"
)

var (
	gaugeRTT = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "edgecast_netem_rtt_ms", Help: "Emulated added round-trip time (base + scenario)",
	})
	gaugeJitter = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "edgecast_netem_jitter_ms", Help: "Emulated jitter",
	})
	gaugeLoss = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "edgecast_netem_loss_pct", Help: "Emulated downlink loss percent",
	})
	gaugeRate = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "edgecast_netem_rate_kbit", Help: "Emulated downlink rate cap (0 = unlimited)",
	})
)

// Scenario is what expctl applies: DelayMs is the ADDED ROUND-TRIP time of
// the link (split across both directions); jitter, loss, and the rate cap
// apply to the downlink (media) direction, like shaping the access link.
type Scenario struct {
	DelayMs  float64 `json:"delay_ms"`
	JitterMs float64 `json:"jitter_ms"`
	LossPct  float64 `json:"loss_pct"`
	RateKbit float64 `json:"rate_kbit"`
}

// State is the per-process link state: a static base RTT (region geography,
// from REGION_RTT_MS) plus the dynamic scenario applied by expctl.
type State struct {
	baseRTTms float64
	mu        sync.Mutex
	dyn       Scenario
	down      atomic.Pointer[Profile]
	up        atomic.Pointer[Profile]
}

func NewState(baseRTTms float64) *State {
	st := &State{baseRTTms: baseRTTms}
	st.recompute()
	return st
}

func (st *State) Apply(s Scenario) {
	st.mu.Lock()
	st.dyn = s
	st.recompute()
	st.mu.Unlock()
	log.Printf("netem: applied rtt+=%.0fms jitter=%.0fms loss=%.1f%% rate=%.0fkbit (base rtt %.0fms)",
		s.DelayMs, s.JitterMs, s.LossPct, s.RateKbit, st.baseRTTms)
}

func (st *State) Clear() { st.Apply(Scenario{}) }

// recompute derives per-direction profiles; callers hold st.mu or are init.
func (st *State) recompute() {
	rtt := st.baseRTTms + st.dyn.DelayMs
	down := Profile{DelayMs: rtt / 2, JitterMs: st.dyn.JitterMs, LossPct: st.dyn.LossPct, RateKbit: st.dyn.RateKbit}
	up := Profile{DelayMs: rtt / 2}
	st.down.Store(&down)
	st.up.Store(&up)
	gaugeRTT.Set(rtt)
	gaugeJitter.Set(st.dyn.JitterMs)
	gaugeLoss.Set(st.dyn.LossPct)
	gaugeRate.Set(st.dyn.RateKbit)
}

func (st *State) Down() Profile { return *st.down.Load() }
func (st *State) Up() Profile   { return *st.up.Load() }

// RegisterHandlers exposes the expctl-facing control surface.
func (st *State) RegisterHandlers(a *admin.Server) {
	a.Handle("/netem/apply", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var s Scenario
		if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		st.Apply(s)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	a.Handle("/netem/clear", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		st.Clear()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	a.Handle("/netem/state", func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		resp := map[string]any{"base_rtt_ms": st.baseRTTms, "scenario": st.dyn}
		st.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
}
