# 5. Measured results (smoke matrix)

All numbers below are real output of `docker compose run --rm expctl` on this repo at the results commit, not projections. Raw per-session JSON (including per-second timelines) for every run is committed under `results/raw/`.

**Environment:** Intel Core Ultra 9 185H (16C/22T), 23 GB RAM, Windows 11 + Docker Desktop (WSL2), all 21 containers on one host. 24 runs: 4 profiles x 3 protocols x 2 repetitions, 10s warmup + 45s measure. Sessions per run: MoQ 200 (5 regions x 20 x 2 reps), WebRTC 40, HLS 40.

## 5.1 The matrix

| Profile | Protocol | Sessions | Startup p50/p95 (ms) | E2E p50/p95 (ms) | Jitter p50 (ms) | Throughput (kbps) | Stall s/min | Stalled % | Recovery p50 (ms) |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| baseline | moq | 200 | 235 / 421 | 44 / 45 | 3 | 4736 | 4.00 | 4.47 | |
| baseline | webrtc | 40 | 699 / 943 | 23 / 28 | 0 | 2410 | 2.67 | 2.95 | |
| baseline | hls | 40 | 1218 / 1872 | 713 / 2628 | 317 | 6000 | 0.00 | 0.00 | |
| mobile-4g | moq | 200 | 1007 / 1325 | 6935 / 11131 | 548 | 557 | 57.35 | 47.84 | |
| mobile-4g | webrtc | 40 | 1182 / 1529 | 56 / 157 | 9 | 2594 | 2.67 | 2.94 | |
| mobile-4g | hls | 40 | 2682 / 3248 | 1296 / 3357 | 397 | 1200 | 0.00 | 0.00 | |
| mobile-3g | moq | 200 | 2034 / 2648 | 12555 / 23624 | 1255 | 245 | 60.00 | 93.13 | |
| mobile-3g | webrtc | 40 | 1984 / 2294 | 283 / 341 | 18 | 1638 | 2.67 | 3.05 | |
| mobile-3g | hls | 40 | 6377 / 8092 | 12716 / 17441 | 557 | 655 | 54.26 | 80.72 | |
| burst-loss | moq | 200 | 463 / 668 | 4159 / 6300 | 143 | 1251 | 20.98 | 14.83 | 3000 |
| burst-loss | webrtc | 40 | 869 / 1221 | 38 / 64 | 3 | 2438 | 2.67 | 3.17 | 0 |
| burst-loss | hls | 40 | 1924 / 2292 | 1617 / 4429 | 652 | 2491 | 2.75 | 5.47 | 5000 |

Zero session errors in all 24 runs. Metric definitions: [03-experiment-design.md](03-experiment-design.md).

## 5.2 Context needed to read the table honestly

- **Publisher bitrates differ by design.** The MoQ publisher runs backpressure ABR (climbed 1000 to 5000 kbps on the clean uplink and stayed there: 2 tier changes over the whole matrix). WebRTC publishes fixed 2500 kbps; HLS offers a 600/1500/3000 ladder that clients pick from. Columns compare *systems as configured*, not raw transport efficiency at equal bitrate.
- **The MoQ collapse under mobile profiles is an ABR architecture result, not a QUIC result.** The publisher's ladder reacts to *its own* uplink congestion, which stayed clean; subscribers behind 12 Mbit/0.5% and 2 Mbit/1.5% access links were force-fed a 5000 kbps stream. Loss-based congestion control cannot carry 5 Mbps through those loss/RTT combinations, so relays dropped 51,388 whole groups (the intended backpressure behavior) and subscriber goodput/stalls cratered. Section 6 discusses why this is the most useful finding in the matrix.
- **Baseline MoQ stalls (~4.5%) are host saturation, not protocol behavior:** 100 sessions x ~5 Mbps means ~500 Mbps of aggregate fanout on one laptop; CPU contention shows up as occasional late frames.
- **HLS baseline "6000 kbps" exceeds the top rendition (3000)** because sessions download faster than realtime while filling their 10s buffer during the 45s window; it is a download rate, not a playback bitrate.
- **HLS loss figures are model-dependent.** TCP loss is emulated as retransmission-like pauses (docs/02, section 2.6), which is harsher than real cwnd dynamics at 1.5% loss; treat mobile-3g HLS stalls as directionally correct, not calibrated.
- **Recovery**: after 5%-loss bursts end, WebRTC is back above 90% of baseline within the 1s resolution (NACK retransmission absorbed the bursts entirely at 30ms RTT), MoQ needs ~3s (drop-oldest queues refill), HLS ~5s (buffer refill at segment cadence).

## 5.3 Reproducing and extending

```bash
docker compose run --rm expctl                                    # this table
docker compose run --rm -e MATRIX=/app/scenarios/full.yaml expctl # 120-run version
```

Per-run raw JSON in `results/raw/` includes every session's per-second byte/stall timeline, which is what the recovery column is derived from offline.
