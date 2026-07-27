# Results: matrix "abr-ab"

Generated 2026-07-27T02:09:24Z. 9 runs, warmup 10s, measure 50s.

| Profile | Protocol | Sessions | Startup p50/p95 (ms) | E2E p50/p95 (ms) | Jitter p50 (ms) | Throughput (kbps) | Stall s/min | Stalled % | Recovery p50 (ms) | Errors |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| mobile-4g | moq | 300 | 355 / 538 | 3148 / 5007 | 112 | 511 | 16.10 | 8.25 |  | 0 |
| mobile-3g | moq | 300 | 620 / 860 | 4808 / 7312 | 205 | 207 | 42.98 | 26.33 |  | 0 |
| lossy | moq | 300 | 338 / 521 | 2840 / 4679 | 57 | 680 | 8.88 | 6.70 |  | 0 |

Metric definitions: docs/03-experiment-design.md. Startup covers the full join (dial included). Throughput and stall figures cover the measure window only.
