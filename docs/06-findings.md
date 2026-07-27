# 6. Findings

Interpretation of [05-results.md](05-results.md) against the research questions from [01-objectives.md](01-objectives.md).

## 6.1 RQ1 (startup): the ordering is structural and it held everywhere

MoQ < WebRTC < HLS in every profile, roughly 1 : 3 : 5 at baseline (235 / 699 / 1218 ms p50).

- MoQ joins in about one handshake plus one control round trip, then the relay serves the **cached current group starting at its keyframe**. Late-join caching is the single highest-leverage design feature measured in this testbed.
- WebRTC pays ICE connectivity checks plus DTLS before the first packet; its startup roughly doubles the signaling-path RTT cost of MoQ's join.
- HLS pays playlist fetch + a 2-segment buffer rule; its startup is dominated by policy (segment duration x buffer depth), which is why it degrades worst as RTT grows (6.4s p50 on mobile-3g).

Under impairment the ordering persisted while all values shifted up by the added RTT and loss retries, which is what "structural" predicts.

## 6.2 RQ2 (resilience): each system sacrifices a different metric first

- **WebRTC** was the most resilient as configured: modest fixed bitrate (2500 kbps) plus NACK retransmission held ~3% stall time and sub-350ms e2e through every profile. Its trade: it never uses more than 2500 kbps when the network is good.
- **HLS** trades bitrate for smoothness: on mobile-4g it dropped renditions (1200 kbps average) and recorded **zero** stalls, at the cost of 1.3s median latency. It only broke (80% stalled) on mobile-3g, where the emulator's TCP loss model leaves less than the lowest rendition's bandwidth.
- **MoQ** delivered the best clean-network experience (fastest startup, 4736 kbps at 44ms e2e) and the worst impaired one, for a reason worth stating precisely (6.3).

## 6.3 RQ5 (adaptation): publisher-side ABR is blind exactly where viewers live

The matrix's most valuable negative result. The MoQ publisher's backpressure ABR watches congestion on the **publisher-to-relay uplink**, which experiments left clean; it therefore held the 5000 kbps tier through every access-side impairment (2 tier changes in 45 minutes, both during the initial climb). Downstream, relays did exactly what they were told: protect liveness by dropping oldest groups per slow subscriber, 51,388 groups over the matrix. The result was 48 to 93 percent stalled time for mobile-profile subscribers receiving a stream their access links could not carry.

The architectural conclusion: in a relay/fanout system, **rate adaptation must live at the subscriber edge**, because that is where heterogeneous last-mile capacity is visible. This is precisely why MoQ deployments model ABR as subscribers switching between parallel tracks rather than the publisher adapting one track for everyone (a publisher adapting down would punish every clean-network viewer for one bad link). The testbed measured the failure mode that motivates that design. Implementing subscriber-driven track switching is future work item 3.

## 6.4 RQ3 (recovery): retransmission recovers fastest, buffers slowest

After 10s bursts of 5% loss ended: WebRTC showed no measurable degradation window at all (NACK recovery at 30ms RTT absorbed the bursts), MoQ returned to 90% baseline in ~3s (queues drain, next keyframe group resyncs), HLS took ~5s (a buffer refill at 2s-segment cadence). The mechanism ordering (retransmit < resync < refill) should transfer to production even though the absolute values are emulator-specific.

## 6.5 RQ4 (relay behavior): the tree did its job

- Pull-through worked with zero configuration: edges subscribed upstream on first local demand, and the publisher's uplink carried the track once per edge rather than once per 20 viewers (5x fanout amplification at the origin, measured as ingest vs fanout bytes on the relay dashboard).
- Late-join caching held regional startup p50s within ~2x of the local region despite up to 110ms of emulated backbone RTT.
- Drop-oldest-group backpressure kept relays memory-stable under 93%-stalled subscribers; slow consumers never stalled ingest or fast consumers (per-subscriber queues isolated them).

## 6.6 What we would do differently

1. Subscriber-driven ABR on the MoQ path (the 6.3 fix) before any other feature.
2. Give WebRTC bandwidth estimation before quoting its rate-cap results widely; fixed-bitrate flatters it under caps it happens to fit and would break under caps it does not.
3. Calibrate the TCP loss model against real kernel behavior (or run the HLS path on a host with sch_netem) before leaning on mobile-3g HLS numbers.
4. Pin publisher CPU or lower the top ABR tier so baseline MoQ stalls read zero instead of reflecting host saturation.
