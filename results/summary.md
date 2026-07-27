# Results: matrix "smoke"

Generated 2026-07-27T00:18:19Z. 24 runs, warmup 10s, measure 45s.

| Profile | Protocol | Sessions | Startup p50/p95 (ms) | E2E p50/p95 (ms) | Jitter p50 (ms) | Throughput (kbps) | Stall s/min | Stalled % | Recovery p50 (ms) | Errors |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| baseline | moq | 200 | 235 / 421 | 44 / 45 | 3 | 4736 | 4.00 | 4.47 |  | 0 |
| baseline | webrtc | 40 | 699 / 943 | 23 / 28 | 0 | 2410 | 2.67 | 2.95 |  | 0 |
| baseline | hls | 40 | 1218 / 1872 | 713 / 2628 | 317 | 6000 | 0.00 | 0.00 |  | 0 |
| mobile-4g | moq | 200 | 1007 / 1325 | 6935 / 11131 | 548 | 557 | 57.35 | 47.84 |  | 0 |
| mobile-4g | webrtc | 40 | 1182 / 1529 | 56 / 157 | 9 | 2594 | 2.67 | 2.94 |  | 0 |
| mobile-4g | hls | 40 | 2682 / 3248 | 1296 / 3357 | 397 | 1200 | 0.00 | 0.00 |  | 0 |
| mobile-3g | moq | 200 | 2034 / 2648 | 12555 / 23624 | 1255 | 245 | 60.00 | 93.13 |  | 0 |
| mobile-3g | webrtc | 40 | 1984 / 2294 | 283 / 341 | 18 | 1638 | 2.67 | 3.05 |  | 0 |
| mobile-3g | hls | 40 | 6377 / 8092 | 12716 / 17441 | 557 | 655 | 54.26 | 80.72 |  | 0 |
| burst-loss | moq | 200 | 463 / 668 | 4159 / 6300 | 143 | 1251 | 20.98 | 14.83 | 3000 | 0 |
| burst-loss | webrtc | 40 | 869 / 1221 | 38 / 64 | 3 | 2438 | 2.67 | 3.17 | 0 | 0 |
| burst-loss | hls | 40 | 1924 / 2292 | 1617 / 4429 | 652 | 2491 | 2.75 | 5.47 | 5000 | 0 |

Metric definitions: docs/03-experiment-design.md. Startup covers the full join (dial included). Throughput and stall figures cover the measure window only.
