# 4. Setup and operations

## 4.1 Prerequisites

- Docker with Compose v2 (Docker Desktop on Windows/macOS, or any Linux engine). No kernel modules, no NET_ADMIN: network impairment is userspace (see [02-architecture.md](02-architecture.md) section 2.6).
- Roughly 4 CPU cores and 4 GB free for Docker while the full topology runs.
- No local Go toolchain needed; everything compiles inside the image build.

## 4.2 Quickstart

```bash
docker compose up -d --build
```

That brings up 21 containers: the 5-region MoQ tree with its publisher and 100 subscriber sessions, the WebRTC SFU with its publisher and 20 viewers, the HLS origin with 20 players, Prometheus, and Grafana.

- Grafana: http://localhost:3000 (anonymous admin, provisioned dashboards under the EdgeCast folder)
- Prometheus: http://localhost:9090

Within ~30 seconds the Protocol Comparison dashboard should show all three protocols moving bytes and the startup histograms filling in.

## 4.3 Running experiments

```bash
docker compose run --rm expctl
```

runs the smoke matrix (`scenarios/smoke.yaml`, ~25 minutes). For the full matrix:

```bash
docker compose run --rm -e MATRIX=/app/scenarios/full.yaml expctl
```

Outputs land in `results/`:

- `results/raw/<run-id>.json`: per-run metadata plus every session's metrics and per-second timeline
- `results/summary.csv` and `results/summary.md`: the aggregated matrix

Scenario files are mounted read-write-free from `./scenarios`, so editing profiles or adding a matrix needs no rebuild.

## 4.4 Poking the emulator by hand

Every container's admin port exposes the control surface. From the host, through any container:

```bash
docker exec edgecast-moq-sub-eu-central-1 curl -s -X POST -H "Content-Type: application/json" -d "{\"delay_ms\":150,\"jitter_ms\":30,\"loss_pct\":2,\"rate_kbit\":2000}" http://localhost:8080/netem/apply
```

Watch the eu-central line collapse on the Experiments dashboard, then:

```bash
docker exec edgecast-moq-sub-eu-central-1 curl -s -X POST http://localhost:8080/netem/clear
```

`POST /sessions/restart` with `{"run_id":"manual-1"}` restarts a load generator's sessions; `GET /sessions/results?run_id=manual-1` returns their JSON.

## 4.5 Producing figures and dashboard snapshots

Charts are built from committed experiment output, so any figure can be regenerated from a fresh clone without rerunning experiments:

```bash
docker build -t edgecast-figures:local tools/figures
docker run --rm -v "$PWD/results:/results:ro" -v "$PWD/docs/images:/out" -e RESULTS_DIR=/results/paper edgecast-figures:local
```

Dashboards are snapshotted server-side by the `renderer` service (Grafana's own image renderer), which is more reliable than driving a generic headless browser:

```bash
curl -o docs/images/dashboard-protocol-comparison.png "http://localhost:3000/render/d/edgecast-compare/x?orgId=1&from=now-40m&to=now&kiosk&width=1500&height=1500&scale=2"
```

Architecture diagrams live as Mermaid sources in `tools/diagrams` and render to PNG with `tools/diagrams/render.sh` (see the header comment for the exact container invocation).

## 4.6 Fault injection

Beyond the impairment profiles, `tools/faultinj/relay-failure.sh` kills an edge relay with SIGKILL mid-session and restarts it, holding an unaffected region as a control, then writes both regions' per-session timelines for offline analysis:

```bash
bash tools/faultinj/relay-failure.sh
```

This exercises the reconnect paths that let the topology converge from any state, and produces a measured recovery time rather than an assertion that reconnect works.

## 4.7 Knobs

| Env var | Where | Meaning |
| --- | --- | --- |
| `SESSIONS` | load generators | concurrent sessions per container |
| `BITRATE_KBPS` | publishers | starting (MoQ) or fixed (WebRTC) bitrate |
| `ABR` | moq-pub | enable backpressure-driven ladder adaptation |
| `REGION_RTT_MS` | any | static base RTT of the container's emulated link |
| `FPS`, `GROUP_FRAMES` | publishers | media cadence and group (GOP) size |
| `MATRIX`, `OUT` | expctl | scenario file and output directory |

Available matrices in `scenarios/`: `smoke.yaml` (4 profiles, 2 reps, ~25 min), `paper.yaml` (8 profiles, 3 reps, ~80 min), `full.yaml` (8 profiles, 5 reps, ~2.5 h), and `abr-ab.yaml` (the adaptation diagnosis check, MoQ only).

Scaling up is mostly `SESSIONS`; 100 MoQ + 20 WebRTC + 20 HLS sessions is comfortable on a laptop, and the compose file is the place to add regions.

## 4.6 Known noise

- quic-go logs `failed to sufficiently increase receive buffer size` in containers; UDP buffer sysctls are host-global and cannot be raised per-container. Harmless at testbed rates.
- The first seconds after `up` show reconnect churn while relays pull tracks through and the SFU waits for its publisher; every role retries with backoff.
