// Package expctl runs the experiment matrix: for every (profile, protocol,
// repetition) it applies impairment to the target containers over HTTP,
// restarts their sessions so startup is measured under the profile, plays any
// scenario timeline, collects per-session results, and aggregates summaries.
// It never touches Docker; the compose network's admin endpoints are its only
// control surface.
package expctl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/sushantlokhande14/edgecast/internal/loadgen"
)

type Impairment struct {
	DelayMs  float64 `yaml:"delay_ms" json:"delay_ms"`
	JitterMs float64 `yaml:"jitter_ms" json:"jitter_ms"`
	LossPct  float64 `yaml:"loss_pct" json:"loss_pct"`
	RateKbit float64 `yaml:"rate_kbit" json:"rate_kbit"`
}

type TimelineEvent struct {
	AtSeconds float64    `yaml:"at_seconds" json:"at_seconds"` // relative to session restart
	Apply     Impairment `yaml:"apply" json:"apply"`
}

type Profile struct {
	Name     string          `yaml:"name" json:"name"`
	Apply    Impairment      `yaml:"apply" json:"apply"`
	Timeline []TimelineEvent `yaml:"timeline" json:"timeline,omitempty"`
}

type ProtocolTargets struct {
	Name    string   `yaml:"name" json:"name"`
	Targets []string `yaml:"targets" json:"targets"` // host:adminPort of load generators
}

type Matrix struct {
	Name           string            `yaml:"name"`
	WarmupSeconds  int               `yaml:"warmup_seconds"`
	MeasureSeconds int               `yaml:"measure_seconds"`
	Repetitions    int               `yaml:"repetitions"`
	Protocols      []ProtocolTargets `yaml:"protocols"`
	Profiles       []Profile         `yaml:"profiles"`
}

type runRecord struct {
	Matrix        string           `json:"matrix"`
	RunID         string           `json:"run_id"`
	Protocol      string           `json:"protocol"`
	ProfileName   string           `json:"profile"`
	Profile       Profile          `json:"profile_def"`
	Repetition    int              `json:"repetition"`
	StartedUnixMs int64            `json:"started_unix_ms"`
	WarmupS       int              `json:"warmup_s"`
	MeasureS      int              `json:"measure_s"`
	Sessions      []loadgen.Result `json:"sessions"`
}

// Run executes the whole matrix and writes raw + aggregated results.
func Run(ctx context.Context, matrixPath, outDir string) error {
	raw, err := os.ReadFile(matrixPath)
	if err != nil {
		return fmt.Errorf("read matrix: %w", err)
	}
	var m Matrix
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("parse matrix: %w", err)
	}
	if m.Repetitions <= 0 {
		m.Repetitions = 1
	}
	if err := os.MkdirAll(filepath.Join(outDir, "raw"), 0o755); err != nil {
		return err
	}

	client := &http.Client{Timeout: 15 * time.Second}
	total := len(m.Profiles) * len(m.Protocols) * m.Repetitions
	log.Printf("expctl: matrix %q: %d profiles x %d protocols x %d reps = %d runs (~%s)",
		m.Name, len(m.Profiles), len(m.Protocols), m.Repetitions, total,
		time.Duration(total*(m.WarmupSeconds+m.MeasureSeconds+5))*time.Second)

	var runs []runRecord
	n := 0
	for _, prof := range m.Profiles {
		for _, pt := range m.Protocols {
			for rep := 1; rep <= m.Repetitions; rep++ {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				n++
				runID := fmt.Sprintf("%s.%s.%s.r%d.%d", m.Name, pt.Name, prof.Name, rep, time.Now().Unix())
				log.Printf("expctl: run %d/%d: protocol=%s profile=%s rep=%d", n, total, pt.Name, prof.Name, rep)
				rr, err := runOne(ctx, client, m, pt, prof, rep, runID)
				if err != nil {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					log.Printf("expctl: run %s failed: %v", runID, err)
					continue
				}
				if b, err := json.MarshalIndent(rr, "", " "); err == nil {
					_ = os.WriteFile(filepath.Join(outDir, "raw", runID+".json"), b, 0o644)
				}
				runs = append(runs, rr)
			}
		}
	}

	rows := aggregate(m, runs)
	if err := writeCSV(filepath.Join(outDir, "summary.csv"), rows); err != nil {
		return err
	}
	if err := writeMarkdown(filepath.Join(outDir, "summary.md"), m, runs, rows); err != nil {
		return err
	}
	log.Printf("expctl: done: %d/%d runs ok; summaries in %s", len(runs), total, outDir)
	return nil
}

func runOne(ctx context.Context, client *http.Client, m Matrix, pt ProtocolTargets, prof Profile, rep int, runID string) (runRecord, error) {
	for _, t := range pt.Targets {
		if err := post(client, t, "/netem/apply", prof.Apply); err != nil {
			return runRecord{}, fmt.Errorf("apply to %s: %w", t, err)
		}
	}
	started := time.Now()
	for _, t := range pt.Targets {
		if err := post(client, t, "/sessions/restart", map[string]any{"run_id": runID}); err != nil {
			return runRecord{}, fmt.Errorf("restart on %s: %w", t, err)
		}
	}

	totalDur := time.Duration(m.WarmupSeconds+m.MeasureSeconds) * time.Second
	events := append([]TimelineEvent(nil), prof.Timeline...)
	sort.Slice(events, func(i, j int) bool { return events[i].AtSeconds < events[j].AtSeconds })
	for _, ev := range events {
		due := started.Add(time.Duration(ev.AtSeconds * float64(time.Second)))
		if d := time.Until(due); d > 0 {
			select {
			case <-time.After(d):
			case <-ctx.Done():
				return runRecord{}, ctx.Err()
			}
		}
		for _, t := range pt.Targets {
			_ = post(client, t, "/netem/apply", ev.Apply)
		}
		log.Printf("expctl: %s: timeline event at %.0fs applied", runID, ev.AtSeconds)
	}
	if d := time.Until(started.Add(totalDur)); d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return runRecord{}, ctx.Err()
		}
	}

	rr := runRecord{
		Matrix: m.Name, RunID: runID, Protocol: pt.Name, ProfileName: prof.Name,
		Profile: prof, Repetition: rep, StartedUnixMs: started.UnixMilli(),
		WarmupS: m.WarmupSeconds, MeasureS: m.MeasureSeconds,
	}
	for _, t := range pt.Targets {
		var sessions []loadgen.Result
		if err := get(client, t, "/sessions/results?run_id="+runID, &sessions); err != nil {
			log.Printf("expctl: collect from %s: %v", t, err)
			continue
		}
		rr.Sessions = append(rr.Sessions, sessions...)
	}
	for _, t := range pt.Targets {
		_ = post(client, t, "/netem/clear", nil)
	}
	if len(rr.Sessions) == 0 {
		return runRecord{}, fmt.Errorf("no sessions collected")
	}
	return rr, nil
}

func post(client *http.Client, host, path string, body any) error {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return err
		}
	}
	resp, err := client.Post("http://"+host+path, "application/json", &buf)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s%s: HTTP %d", host, path, resp.StatusCode)
	}
	return nil
}

func get(client *http.Client, host, path string, out any) error {
	resp, err := client.Get("http://" + host + path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s%s: HTTP %d", host, path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
