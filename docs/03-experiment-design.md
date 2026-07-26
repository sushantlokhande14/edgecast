# 3. Experiment design

## 3.1 Metric definitions (identical across protocols)

| Metric | Definition |
| --- | --- |
| **Startup delay** | Session start (subscribe/offer/playlist request sent) to first media object rendered. For MoQ: first object of a decodable group. For WebRTC: first RTP packet after PeerConnection connects. For HLS: initial buffer (2 segments) filled. |
| **End-to-end latency** | Publisher stamp on a frame to its arrival at the subscriber. Valid because publisher and subscriber share one host clock. HLS latency includes segment accumulation by design. |
| **Throughput** | Media payload bytes received per second (1s buckets, recorded per session). |
| **Interarrival jitter** | RFC 3550 style: EWMA of the deviation between frame interarrival spacing and the nominal frame interval. |
| **Stall** | Simulated playout: a jitter buffer targets 150ms (MoQ/WebRTC) or uses the player buffer (HLS). A stall is playhead starvation longer than 100ms; we record count and total stalled time. |
| **Recovery time** | An impairment window ends at t_e. Recovery is the first time after t_e where the session's 3s rolling throughput is at least 90 percent of its pre-impairment baseline, minus t_e. Computed offline from per-second timelines. |

## 3.2 Network profiles

Applied with `tc netem` (delay/jitter/loss) plus `tbf` (rate). Access-side impairment targets subscriber containers; backbone impairment targets edge relays.

| Profile | Delay | Jitter | Loss | Rate cap | Models |
| --- | --- | --- | --- | --- | --- |
| `baseline` | 5ms | 0 | 0% | none | Wired LAN |
| `home-wifi` | 20ms | 5ms | 0.1% | 50 Mbit | Decent broadband |
| `mobile-4g` | 60ms | 15ms | 0.5% | 12 Mbit | Median LTE |
| `mobile-3g` | 150ms | 40ms | 1.5% | 2 Mbit | Congested / legacy mobile |
| `lossy` | 40ms | 10ms | 3% | 20 Mbit | Bad WiFi, interference |
| `satellite` | 300ms | 20ms | 0.5% | 15 Mbit | GEO satellite |
| `degrading` | ramp 20ms to 150ms | ramp | ramp 0 to 2% | ramp 20 to 1.5 Mbit | Driving away from a cell tower (timeline scenario) |
| `burst-loss` | 30ms | 5ms | 10s bursts of 5% | 20 Mbit | Transient interference; drives recovery-time measurement |

Region base delays (always on, models geography): us-west 2ms, us-east 35ms, eu-central 70ms, ap-south 110ms, ap-east 90ms.

## 3.3 Run protocol

Each run = (protocol, profile, repetition):

1. `expctl` clears all impairments, applies the profile to target containers.
2. Restarts sessions on the protocol's load generators with a fresh `run_id` (so startup delay is measured under the profile, not before it).
3. Warmup 10s (excluded), measure 45s.
4. Collects per-session JSON: startup ms, first-frame ms, stall count, stalled ms, per-second byte/stall timeline, jitter, e2e latency percentiles.
5. Timeline profiles (`degrading`, `burst-loss`) additionally step impairments on a schedule during the measure window.

The **smoke matrix** (3 protocols x 4 profiles x 2 reps, ~20 min wall clock) validates the pipeline and produces the committed results in this repo. The **full matrix** (3 protocols x 8 profiles x repetitions x per-region variants) is defined in `scenarios/full.yaml`; runtime scales linearly, and results land in `results/` the same way.

## 3.4 Fairness and validity controls

- One synthetic media generator shared by all three paths: 30 fps, 1s keyframe interval, same bitrate ladder, incompressible payloads.
- Same metric code computes startup/stall/jitter for all protocols (shared `internal/metrics` session recorder).
- Warmup windows excluded everywhere; sessions restarted per run so no protocol benefits from a warm connection.
- Repetitions with per-run seeds; results report medians and p95 across sessions x reps.
- Known threats to validity are tracked in [08-prototype-vs-production.md](08-prototype-vs-production.md): shared-host CPU contention, netem's deviation from real radio links, no real codec/decoder cost, loopback-scale RTTs on the unshaped path.
