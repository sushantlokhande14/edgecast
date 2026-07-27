# Results: matrix "paper"

Generated 2026-07-27T01:43:34Z. 72 runs, warmup 10s, measure 50s.

| Profile | Protocol | Sessions | Startup p50/p95 (ms) | E2E p50/p95 (ms) | Jitter p50 (ms) | Throughput (kbps) | Stall s/min | Stalled % | Recovery p50 (ms) | Errors |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| baseline | moq | 300 | 239 / 423 | 44 / 46 | 1 | 4665 | 4.80 | 5.76 |  | 0 |
| baseline | webrtc | 60 | 688 / 986 | 23 / 28 | 0 | 2333 | 4.80 | 5.72 |  | 0 |
| baseline | hls | 60 | 1269 / 1904 | 729 / 2506 | 309 | 6000 | 0.00 | 0.00 |  | 0 |
| home-wifi | moq | 300 | 471 / 669 | 4062 / 5960 | 125 | 1372 | 20.25 | 14.70 |  | 0 |
| home-wifi | webrtc | 60 | 884 / 1251 | 31 / 54 | 2 | 2361 | 4.80 | 5.84 |  | 0 |
| home-wifi | hls | 60 | 1723 / 2048 | 1031 / 2863 | 323 | 3598 | 0.00 | 0.00 |  | 0 |
| mobile-4g | moq | 300 | 998 / 1336 | 7063 / 11469 | 403 | 551 | 57.16 | 49.37 |  | 0 |
| mobile-4g | webrtc | 60 | 1194 / 1509 | 56 / 156 | 9 | 2494 | 4.80 | 5.78 |  | 0 |
| mobile-4g | hls | 60 | 2651 / 3386 | 1334 / 3756 | 409 | 1200 | 0.00 | 0.00 |  | 0 |
| mobile-3g | moq | 300 | 2098 / 2690 | 13314 / 22313 | 1001 | 245 | 60.00 | 94.57 |  | 0 |
| mobile-3g | webrtc | 60 | 2005 / 2601 | 284 / 334 | 19 | 1520 | 4.80 | 5.90 |  | 0 |
| mobile-3g | hls | 60 | 6320 / 7533 | 12557 / 17026 | 582 | 593 | 54.25 | 81.01 |  | 0 |
| lossy | moq | 300 | 776 / 1034 | 5829 / 9125 | 268 | 714 | 44.52 | 29.88 |  | 0 |
| lossy | webrtc | 60 | 1093 / 2000 | 44 / 117 | 6 | 2403 | 4.80 | 6.00 |  | 0 |
| lossy | hls | 60 | 3152 / 4474 | 1577 / 3772 | 467 | 1200 | 0.00 | 0.00 |  | 0 |
| satellite | moq | 300 | 1850 / 2173 | 10688 / 18568 | 754 | 326 | 59.98 | 84.30 |  | 0 |
| satellite | webrtc | 60 | 3079 / 3416 | 176 / 528 | 11 | 2584 | 4.80 | 5.89 |  | 0 |
| satellite | hls | 60 | 7278 / 9198 | 14512 / 18707 | 581 | 330 | 58.14 | 95.98 |  | 0 |
| burst-loss | moq | 300 | 467 / 658 | 4208 / 6731 | 117 | 1216 | 21.59 | 16.40 | 4000 | 0 |
| burst-loss | webrtc | 60 | 956 / 1193 | 38 / 64 | 3 | 2364 | 4.82 | 6.14 | 0 | 0 |
| burst-loss | hls | 60 | 1914 / 2301 | 1390 / 5106 | 623 | 1994 | 3.15 | 2.71 | 4000 | 0 |
| degrading | moq | 300 | 291 / 481 | 59 / 10021 | 757 | 1063 | 45.63 | 55.28 |  | 0 |
| degrading | webrtc | 60 | 812 / 1096 | 92 / 317 | 19 | 1977 | 4.80 | 6.12 |  | 0 |
| degrading | hls | 60 | 1718 / 2099 | 2591 / 8044 | 648 | 2248 | 7.76 | 13.15 |  | 0 |

Metric definitions: docs/03-experiment-design.md. Startup covers the full join (dial included). Throughput and stall figures cover the measure window only.
