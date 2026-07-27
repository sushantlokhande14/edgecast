# 5. Measured results

All numbers below are real output of `scenarios/paper.yaml`, committed under `results/paper/` together with per-session, per-second raw data for every run.

**Environment.** Intel Core Ultra 9 185H (16C/22T), 23 GB RAM, Windows 11 + Docker Desktop (WSL2), all 22 containers sharing one host. 8 profiles x 3 protocols x 3 repetitions = 72 runs, 10s warmup discarded, 50s measure window. Sessions per run: MoQ 100 (5 regions x 20), WebRTC 20, HLS 20. **Zero session errors in all 72 runs.**

## 5.1 The matrix

| Profile | Protocol | Sessions | Startup p50/p95 (ms) | E2E p50/p95 (ms) | Jitter p50 (ms) | Goodput (kbps) | Stalled % | Recovery p50 (ms) |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| baseline | moq | 300 | **239** / 423 | 44 / 46 | 1 | 4665 | 5.8 | |
| baseline | webrtc | 60 | 688 / 986 | **23** / 28 | 0 | 2333 | 5.7 | |
| baseline | hls | 60 | 1269 / 1904 | 729 / 2506 | 309 | 6000 | **0.0** | |
| home-wifi | moq | 300 | **471** / 669 | 4062 / 5960 | 125 | 1372 | 14.7 | |
| home-wifi | webrtc | 60 | 884 / 1251 | **31** / 54 | 2 | 2361 | 5.8 | |
| home-wifi | hls | 60 | 1723 / 2048 | 1031 / 2863 | 323 | 3598 | **0.0** | |
| mobile-4g | moq | 300 | **998** / 1336 | 7063 / 11469 | 403 | 551 | 49.4 | |
| mobile-4g | webrtc | 60 | 1194 / 1509 | **56** / 156 | 9 | 2494 | 5.8 | |
| mobile-4g | hls | 60 | 2651 / 3386 | 1334 / 3756 | 409 | 1200 | **0.0** | |
| mobile-3g | moq | 300 | 2098 / 2690 | 13314 / 22313 | 1001 | 245 | 94.6 | |
| mobile-3g | webrtc | 60 | **2005** / 2601 | **284** / 334 | 19 | 1520 | **5.9** | |
| mobile-3g | hls | 60 | 6320 / 7533 | 12557 / 17026 | 582 | 593 | 81.0 | |
| lossy | moq | 300 | **776** / 1034 | 5829 / 9125 | 268 | 714 | 29.9 | |
| lossy | webrtc | 60 | 1093 / 2000 | **44** / 117 | 6 | 2403 | 6.0 | |
| lossy | hls | 60 | 3152 / 4474 | 1577 / 3772 | 467 | 1200 | **0.0** | |
| satellite | moq | 300 | **1850** / 2173 | 10688 / 18568 | 754 | 326 | 84.3 | |
| satellite | webrtc | 60 | 3079 / 3416 | **176** / 528 | 11 | 2584 | **5.9** | |
| satellite | hls | 60 | 7278 / 9198 | 14512 / 18707 | 581 | 330 | 96.0 | |
| burst-loss | moq | 300 | **467** / 658 | 4208 / 6731 | 117 | 1216 | 16.4 | 4000 |
| burst-loss | webrtc | 60 | 956 / 1193 | **38** / 64 | 3 | 2364 | 6.1 | **0** |
| burst-loss | hls | 60 | 1914 / 2301 | 1390 / 5106 | 623 | 1994 | **2.7** | 4000 |
| degrading | moq | 300 | **291** / 481 | 59 / 10021 | 757 | 1063 | 55.3 | |
| degrading | webrtc | 60 | 812 / 1096 | **92** / 317 | 19 | 1977 | **6.1** | |
| degrading | hls | 60 | 1718 / 2099 | 2591 / 8044 | 648 | 2248 | 13.2 | |

Bold marks the best value per profile per metric. Full CSV: `results/paper/summary.csv`.

## 5.2 The most important caveat: where each protocol hides its loss

**The stalled-time column is not a like-for-like quality comparison, and reading it as one would be wrong.** Emulator and relay accounting for the same window explains why:

| Path | Packets dropped at the network bottleneck | Application-level drops | Visible in our stall metric? |
| --- | --- | --- | --- |
| MoQ | **0** queue drops, 114k iid-loss drops | 129,303 whole groups dropped by relays | **Yes**, directly |
| WebRTC | **9,686,288** queue drops + 267k iid-loss drops | none (SFU forwards blindly) | **Largely no** |
| HLS | n/a (TCP path) | 16,275 modelled retransmission pauses | Yes, as buffer underruns |

Nearly ten million packets bound for WebRTC subscribers were destroyed at the emulated bottleneck during this window, and zero were destroyed for MoQ subscribers. Yet WebRTC reports a flat ~5.8 percent stalled time in every profile, identical to its unimpaired baseline.

Both facts are true because **the two architectures put their loss in different places**:

- The relay path applies backpressure in the application: when a subscriber cannot keep up, the relay discards whole groups, the subscriber's arrival stream develops real gaps, and our playout model counts them as stalls. The degradation is explicit, typed, and counted.
- The WebRTC path has no application backpressure and no bandwidth estimation, so the SFU keeps sending 2500 kbps into whatever the link is. The excess is destroyed in the network. Packets keep arriving steadily, just fewer of them, so an arrival-gap-based stall model barely notices.

Because this testbed does not decode, it cannot see what that loss does to a picture. In a real client, roughly half the packets missing would mean severe artefacting or frozen frames, not smooth playback. **WebRTC's apparent resilience in the stall column is substantially an artefact of measuring arrival continuity rather than decodability.**

The honest cross-protocol quality proxy in this data is **delivered fraction of offered bitrate**, which has no such blind spot. On mobile-3g, WebRTC delivered 1520 of the 2500 kbps it offered (61 percent) while MoQ delivered 245 of 5000 (5 percent). WebRTC is genuinely more robust under these conditions, but the honest gap is on delivered fraction, and a large part of MoQ's share is explained by the offered-rate mismatch in section 6.3 rather than by anything about QUIC.

## 5.3 Other things needed to read the table honestly

- **Publisher rates differ by design.** MoQ ran its adaptive ladder (which climbed to and held 5000 kbps), WebRTC published a fixed 2500 kbps, and HLS offered a 600/1500/3000 ladder that clients selected from. This compares systems as configured, not transports at equal offered load.
- **The startup ordering has one exception.** MoQ was fastest to first media in seven of eight profiles. On mobile-3g, WebRTC edged it, 2005 ms against 2098 ms, a difference of about 4 percent that is within run-to-run spread. Everywhere else the ordering held with a clear margin.
- **Baseline stalls of about 5.8 percent are host saturation, not protocol behaviour.** One hundred MoQ sessions at roughly 5 Mbps is about 500 Mbps of fanout on a laptop that is also running the emulator, Prometheus, and Grafana. WebRTC shows the same ~5.8 percent floor at baseline, which is the signature of a shared-host artefact rather than anything protocol-specific.
- **Rate-capped profiles also load the emulator.** When a profile sets a rate cap, every packet goes through token-bucket scheduling instead of the clean-link fast path, so the MoQ path's absolute goodput under capped profiles is partly limited by emulator CPU. This is why home-wifi, a 50 Mbit cap that should comfortably carry 5 Mbps, still shows reduced MoQ goodput.
- **HLS goodput can exceed its playback bitrate** because clients download ahead to fill a ten-second buffer during the measure window.
- **HLS loss response is modelled, not simulated.** See the TCP limitation in `docs/02-architecture.md`. Treat high-loss HLS figures as directional.

## 5.4 Recovery after transient loss

The burst-loss profile applies two ten-second windows of 5 percent loss. Measured recovery to 90 percent of pre-impairment throughput: **WebRTC 0 s** (within the one-second resolution; it never dropped below the threshold because it simply lost packets rather than slowing down), **MoQ 4 s** (queues drain and the next group resynchronises), **HLS 4 s** (buffer refill at segment cadence).

## 5.5 Reproducing

```bash
docker compose run --rm -e MATRIX=/app/scenarios/paper.yaml -e OUT=/results/paper expctl
docker build -t edgecast-figures:local tools/figures
docker run --rm -v "$PWD/results:/results:ro" -v "$PWD/docs/images:/out" -e RESULTS_DIR=/results/paper edgecast-figures:local
```
