# 1. Objectives and research questions

## 1.1 Motivation

Three delivery models dominate realtime and near-realtime media today, and they make different trade-offs:

- **WebRTC** delivers sub-second latency over SRTP/UDP with sender-side congestion control, but its peer-centric design makes relays (SFUs) stateful and hard to cache, and every hop terminates the protocol.
- **HTTP adaptive streaming (HLS/DASH)** rides on plain HTTP and CDNs, scales to millions of viewers, but pays for it with multi-second startup and glass-to-glass latency because the unit of delivery is a segment.
- **Media over QUIC (MoQ)** is the IETF's in-progress attempt to combine both: publish/subscribe over QUIC streams, relays that fan out objects without terminating media sessions, per-group stream isolation so one lost group does not head-of-line-block the next, and priorities that let the network drop the right thing under congestion.

Most published comparisons of these stacks are not apples-to-apples: different codecs, different test networks, different definitions of "startup" and "stall". The goal of this testbed is a controlled comparison where everything except the delivery protocol is held constant.

## 1.2 Research questions

- **RQ1 (startup):** How does time-to-first-media differ between MoQ-style relay delivery, WebRTC via an SFU, and HLS-style segment fetch, at baseline and under impaired networks?
- **RQ2 (resilience):** How do stall frequency and stalled time respond to packet loss, latency, jitter, and bandwidth caps for each protocol?
- **RQ3 (recovery):** After a transient impairment ends, how quickly does each protocol return to target throughput?
- **RQ4 (relay behavior):** In a multi-region relay tree, what does a pull-through MoQ-style relay buy (fanout efficiency, late-join speed) and what does it cost (added latency per hop, buffer memory)?
- **RQ5 (adaptation):** How much does simple backpressure-driven bitrate adaptation at the publisher reduce stalls compared to a fixed bitrate, under degrading networks?

## 1.3 Success criteria for the testbed itself

1. **Reproducible:** one `docker compose up` brings up the full topology; one `expctl` command reruns the entire matrix; results land as versioned CSV/JSON.
2. **Fair:** identical synthetic media source (same bitrate ladder, frame cadence, keyframe interval) and identical metric definitions across protocols.
3. **Observable:** every component exports Prometheus metrics; Grafana dashboards show live per-protocol behavior and the network conditions in force at any moment.
4. **Controlled:** network impairment is applied per-container with `tc netem`, driven by scenario files, not by hand.

## 1.4 Non-goals

- No real codecs. Frames are synthetic byte payloads with realistic sizes and cadence. This isolates transport behavior from encoder behavior (and keeps the testbed CPU-light enough to run 5 regions on a laptop).
- Not a full IETF moq-transport implementation. The relay implements the architectural ideas (tracks, groups, objects, stream-per-group, relay fanout, late-join from group cache) with a simplified wire format. Differences are documented in [02-architecture.md](02-architecture.md).
- No internet-scale numbers. This is a single-host emulation; scale limits and how results would translate to production are analyzed in [08-prototype-vs-production.md](08-prototype-vs-production.md).
