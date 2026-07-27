# 8. Prototype vs production: what transfers and what does not

This testbed is a controlled emulation on one host. That is a feature for comparability and a limitation for external validity. This section is explicit about both directions.

## 8.1 Where the prototype diverges from the real world

**Single host.** All 21 containers share CPU, memory bandwidth, and one kernel. Heavy load on one path can perturb another (we mitigate by running protocols' experiments sequentially in expctl, but Prometheus, relays, and generators still coexist). Real deployments fail differently: NICs, kernel schedulers, and cross-datacenter links each add their own behavior.

**Emulated links, not real ones.** The userspace emulator applies iid loss, uniform jitter, and a fixed-depth bottleneck queue. Real radio links produce correlated (bursty) loss, delay spikes from retransmission layers below IP, and bufferbloat that interacts with congestion control. The TCP path is further approximated: loss becomes modeled retransmission pauses rather than real cwnd dynamics, so HLS results under `lossy` profiles are indicative, not faithful.

**No real codecs.** Frames are sized byte blobs. Real encoders produce variable frame sizes, scene-change keyframes, and encode latency; real decoders conceal errors rather than just counting them. Startup in production includes decoder init and often a license/DRM round trip that dwarfs some transport differences measured here.

**Simplified MoQ.** JSON control plane, flat track names, one priority level, one cached group, no authentication, raw QUIC only (no WebTransport, so no browser subscribers). Real moq-transport relays negotiate versions, enforce namespaces and auth, and make caching/priority decisions this testbed sidesteps. The properties we rely on (stream-per-group isolation, relay fanout, late-join from cache) are the ones the IETF design also has, which is why the qualitative results should transfer.

**WebRTC without sender-side bandwidth estimation.** The publisher sends at a fixed bitrate; there is no GCC/TWCC-driven adaptation and no simulcast. Production WebRTC would degrade more gracefully under the rate-capped profiles and worse under none. NACK retransmission is active, which is the main loss-resilience mechanism at these RTTs.

**No CDN in front of HLS.** Clients hit the origin directly. Production HLS latency and startup depend heavily on CDN edge cache state; our numbers represent a cache-miss-ish worst case with a warm TCP path, which flatters neither side.

**Scale.** ~140 concurrent sessions, one track, five relays. Production questions this testbed cannot answer: relay memory at 10k tracks, fanout CPU at 100k subscribers, control-plane storms on mass reconnect, inter-relay routing.

**Clocks.** Publisher and subscribers share one clock, which makes end-to-end latency exactly measurable; production systems need NTP/PTP discipline or one-way-delay estimation, and their reported e2e numbers carry that error.

## 8.2 What we expect to transfer

- The **ordering** of startup delay (MoQ relay with group cache < WebRTC ICE/DTLS join < HLS multi-segment buffer) is structural: it follows from round trips and buffering rules, not from tuning.
- **Stream-per-group isolation**: loss hurting one group without stalling the next is a protocol property; kernel-level loss would show the same shape.
- **Relay economics**: publisher uplink paid once per edge, fanout at the edge, late joiners served from cache; these scale arguments are architecture, not emulation.
- **Directional sensitivity**: which metric each protocol sacrifices first under a given impairment (HLS holds throughput but grows latency; WebRTC holds latency but sheds throughput/quality; MoQ sits between depending on drop policy).

## 8.3 What not to quote as-is

Any absolute number: startup milliseconds, stall percentages, recovery times. They are functions of this emulator's parameters, this hardware, and synthetic media. The repo commits them as evidence the methodology works and to support relative claims, with the full raw data for anyone to re-derive.

## 8.4 What production hardening would involve

Auth on ANNOUNCE/SUBSCRIBE, real certificates, relay clustering with a control plane for track routing, cache hierarchies deeper than one group, congestion-control tuning (BBR vs CUBIC per path), observability with bounded label cardinality, chaos testing against correlated failures, and clients on real devices with real decoders. Each is future work the testbed's structure was chosen to make approachable.
