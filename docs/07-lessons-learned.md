# 7. Engineering lessons learned

Things this project taught that were not obvious from reading protocol specs.

## 7.1 Test your emulation layer before building on it

The design assumed kernel `tc netem`; a 30-second feasibility probe (`tc qdisc add dev eth0 root netem ...` in a throwaway container) showed Docker Desktop's WSL2 kernel does not ship `sch_netem`, before any code depended on it. The userspace emulator that replaced it ended up strictly better for this project: portable to any Docker host, no privileged containers, and its behavior is plain Go that can be unit-tested and metric-instrumented. Lesson: validate platform assumptions with the cheapest possible probe, and treat a forced pivot as a design opportunity.

## 7.2 Backpressure is a measurement, not just a safety valve

Two places turn backpressure into signal:

- The MoQ publisher's ABR uses **time spent blocked in stream writes per group** as its congestion signal. When QUIC's congestion controller is starved, writes block, and the per-group write time climbs toward the group duration. No RTT probing, no bandwidth estimation, yet it steps down within two groups of a real bottleneck.
- The relay's per-subscriber queues drop **whole oldest groups** rather than arbitrary bytes. Every delivered group is decodable from its keyframe, and the drop counter itself becomes the "this subscriber cannot keep up" metric.

## 7.3 One recorder, or the comparison is fiction

Early sketches had each protocol path computing its own stalls. The definitions drifted immediately (is a 90ms gap a stall? does startup include DNS?). Moving all accounting into one `Recorder` with two explicit escape hatches (manual startup for buffered players, manual stalls for buffer-model paths) is what makes cross-protocol rows in the results table meaningful.

## 7.4 Go's ServeMux patterns bite when one mux serves two jobs

Sharing one admin mux between control endpoints and HLS content hit Go 1.22's pattern-conflict panic: `GET /{rendition}/{seg}` is "more general" than method-less `/netem/apply`. Namespacing content routes (`/hls/...`) fixed it. Lesson: wildcard routes and flat control routes need separate prefixes from day one.

## 7.5 Wrap-safe absolute timestamps make e2e latency trivial

Stamping RTP timestamps as `unix_ms * 90` (mod 2^32) let subscribers compute true end-to-end latency with two lines of uint32 arithmetic, no RTCP SR mapping. Only possible because the testbed shares one clock; the technique is worth knowing regardless.

## 7.6 Restart sessions per run, or startup numbers lie

The first expctl sketch applied impairment to already-running sessions and read startup from whenever the session had originally joined (a clean network). Restarting all sessions after applying each profile is what makes "startup under mobile-3g" a real measurement. Generally: any metric tied to a lifecycle event needs that event to happen inside the measurement window.

## 7.7 Fanout should copy pointers, not bytes

The relay encodes each object once and hands the same immutable byte slice to every subscriber's writer via a small condition-variable buffer. With 100 subscribers per region, encoding per subscriber would have multiplied CPU by two orders of magnitude. The pattern (single producer appending to an immutable log, many readers indexing into it) is the same one that makes real relays and brokers cheap.

## 7.8 Healthchecks plus reconnect loops beat startup ordering

21 containers with dependencies would be miserable with strict startup ordering. Every client retries with backoff and every server tolerates unknown tracks, so `docker compose up` converges from any order, and killing any container mid-run heals. Chaos tolerance fell out of ordinary reconnect hygiene.
