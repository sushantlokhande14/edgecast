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

## 7.8 A metric can be correct and still describe nothing

The most valuable lesson of the project came from a number that looked good. WebRTC reported a flat ~5.8% stalled time in every profile, including profiles where the emulator was destroying roughly ten million of its packets. Both facts were correct. The stall model measures gaps in arrival, and a stream that loses half its packets uniformly still arrives continuously, just thinner.

The deeper point is architectural. The relay path applies backpressure **in the application**, so its degradation is explicit: whole groups dropped, counted, and visible as playout gaps. WebRTC has no application backpressure here, so its excess is destroyed **in the network**, where a transport-level metric cannot see it and only a decoder would. Two systems can be equally degraded and only one of them will say so.

Practical rule: for every metric, ask what failure mode would leave it unchanged. If the answer is "a serious one", the metric needs a companion. Here the companion is delivered fraction of offered bitrate, which has no such blind spot, and the loss accounting exported by the emulator itself.

## 7.9 Package-level metric registration is global

Publisher metrics are declared with `promauto` at package scope. Because every role is compiled into one binary, all 18 containers registered and exported the publisher bitrate gauge, 17 of them reporting a constant zero. The dashboard panel showed 18 overlapping flat lines, and a naive `min_over_time` query returned zero, which would have supported a completely wrong conclusion about whether adaptation was working.

Single-binary multi-role designs trade deployment simplicity for this hazard. Register role-specific collectors in the role's constructor, and treat any panel showing more series than there are logical producers as a defect.

## 7.10 Prefer the product's own tool over a general-purpose one

Capturing dashboard images by driving a generic headless Chromium produced Grafana's "failed to load application files" page every time, with no console errors and no failed requests to explain it. Grafana ships an image renderer service designed for exactly this; adding it to the compose file turned dashboard capture into one HTTP request. The detour also made the repository better, since snapshots are now a documented command rather than a manual screenshot.

## 7.11 Healthchecks plus reconnect loops beat startup ordering

21 containers with dependencies would be miserable with strict startup ordering. Every client retries with backoff and every server tolerates unknown tracks, so `docker compose up` converges from any order, and killing any container mid-run heals. Chaos tolerance fell out of ordinary reconnect hygiene.
