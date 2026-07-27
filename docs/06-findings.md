# 6. Findings

Interpretation of [05-results.md](05-results.md) against the research questions in [01-objectives.md](01-objectives.md). Evidence: 72 matrix runs, a 9-run controlled A/B, and one fault-injection experiment, all committed under `results/`.

## 6.1 RQ1 (startup): the ordering is structural, with one honest exception

MoQ was fastest to first media in **seven of eight profiles**, at roughly 1 : 3 : 5 against WebRTC and HLS on a clean network (239 / 688 / 1269 ms).

The exception is mobile-3g, where WebRTC edged MoQ (2005 ms against 2098 ms, about 4 percent, within run-to-run spread). That is a tie, not an inversion, and it happens in the profile where MoQ was carrying the worst offered-rate mismatch (section 6.3).

The mechanism behind MoQ's advantage is the relay's one-group cache: a joining subscriber is served the group currently being filled, starting at its keyframe, so playback begins without waiting for the next keyframe. WebRTC pays ICE connectivity checks plus DTLS before its first packet. HLS pays a playlist fetch plus a two-segment buffer rule, which is policy rather than transport, and is why it degrades worst as round-trip time grows (6.3 s on mobile-3g, 7.3 s on satellite).

## 6.2 RQ2 (resilience): the stall column is not what it appears

The headline reading, that WebRTC holds ~5.8 percent stalled time everywhere while MoQ degrades badly, is **only partly a statement about the protocols**. It is substantially a statement about where each architecture puts its loss, and therefore about what our metrics can see. Section 5.2 has the accounting: nearly ten million packets destroyed at the bottleneck for WebRTC, zero for MoQ, and a stall metric that barely moved for WebRTC.

The honest version of RQ2:

- **HLS** trades bitrate for smoothness. On mobile-4g it dropped renditions to 1200 kbps and recorded exactly **zero** stalls, paying 1.3 s of latency for it. It only broke on mobile-3g and satellite, where even its lowest rendition does not fit.
- **WebRTC** trades quality for continuity. Without bandwidth estimation it keeps sending 2500 kbps into whatever the link is; the excess is destroyed in the network. Arrival stays continuous, so stalls stay low, but on mobile-3g only 61 percent of the offered bitrate arrives. In a real client with a decoder, that is visible artefacting, not smooth video.
- **MoQ** trades delivered data for liveness, and does so *explicitly*. The relay drops whole groups per slow subscriber (129,303 of them in this window), which keeps everything delivered decodable and keeps the degradation measurable.

That last property is underrated. Of the three, only the relay path makes its own degradation legible to an operator without a decoder in the loop.

## 6.3 RQ5 (adaptation): the central result, now experimentally validated

The paper matrix showed MoQ subscribers stalling 49 to 95 percent on capped profiles while the publisher held its top ladder tier. The publisher's adaptation signal is backpressure on **its own uplink to the origin**, which experiments never impaired, so it correctly concluded it had headroom and was blind to the subscriber access links.

**The controlled A/B** (`scenarios/abr-ab.yaml`, 9 runs, 300 sessions per profile) changed exactly one variable: the publisher was pinned to 1000 kbps instead of running its ladder to 5000. Nothing about the transport, the relay, the emulator, or the profiles changed.

| Profile | Stalled % at 5000 kbps | Stalled % at 1000 kbps | Goodput 5000 | Goodput 1000 | Startup p50 5000 | Startup p50 1000 |
| --- | --- | --- | --- | --- | --- | --- |
| mobile-4g | 49.4 | **8.3** | 551 kbps | 511 kbps | 998 ms | **355 ms** |
| mobile-3g | 94.6 | **26.3** | 245 kbps | 207 kbps | 2098 ms | **620 ms** |
| lossy | 29.9 | **6.7** | 714 kbps | 680 kbps | 776 ms | **338 ms** |

Stalled time fell by **72 to 83 percent** (average 78), startup improved by 56 to 70 percent, and median end-to-end latency fell from 5.8-13.3 s to 2.8-4.8 s.

**Goodput was essentially unchanged.** Offering five times the bitrate delivered no additional bits at all; it only produced queueing, group drops, stalls, and latency. That is the cleanest possible statement of the result:

> In a fanout topology, rate adaptation driven by publisher-side signals cannot see the last mile, and offering more than the last mile can carry costs quality without buying any.

This is precisely why the IETF Media over QUIC design expresses adaptation as **subscribers switching between parallel tracks** rather than a publisher adapting one track for everyone: a publisher adapting downward would also punish every well-connected viewer. Implementing subscriber-driven track switching is future-work item 3.

*Caveat, stated plainly:* the pinned arm also sends five times fewer packets, which reduces load on the userspace emulator, so the two arms are not perfectly isolated. The direction and magnitude are not explained by that alone, since goodput would have risen if emulator capacity were the binding constraint, and it did not.

## 6.4 RQ3 (recovery): mechanism ordering, and one uncomfortable number

After ten-second bursts of 5 percent loss ended: **WebRTC 0 s** (it never fell below the recovery threshold, because it sheds packets rather than slowing down), **MoQ 4 s** (queues drain, next group resynchronises), **HLS 4 s** (buffer refill at segment cadence).

The fault-injection experiment is less flattering and more useful. An edge relay was killed with SIGKILL while 20 sessions watched, held down 16 s, then restarted:

- The region behind it went **completely dark**, 0 kbps, for the entire outage. There is no failover: a subscriber pinned to one edge has no second path.
- It recovered to 90 percent of pre-failure throughput **29 seconds after the relay came back**, nearly twice the outage itself.
- Sessions behind the failure accumulated 62.2 s of stall each against 23.0 s for the unaffected control region.

The recovery lag is not the relay's restart time; it is client reconnect backoff (a flat two seconds, unjittered) plus cold-start pull-through re-establishment. Two concrete fixes follow directly: jittered exponential backoff to avoid synchronised reconnect storms, and subscribers holding a warm connection to a second relay so failover does not require a fresh dial.

## 6.5 RQ4 (relay behaviour): the tree earned its keep

- **Fanout amplification** of roughly twentyfold per relay, about 5 Mbps of ingest against about 100 Mbps of fanout, and roughly a hundredfold across the system: one publisher stream served 100 subscriber sessions.
- **Pull-through worked with zero configuration.** Edges subscribed upstream on first local demand.
- **Late-join caching** kept regional startup within about 2x of the local region despite up to 110 ms of emulated backbone round trip.
- **Backpressure isolated slow consumers.** Relays stayed memory-stable while subscribers were 95 percent stalled, and fast consumers were unaffected by slow ones.

## 6.6 A bug worth more than the experiment that found it

The first A/B run produced end-to-end latencies of 76, 269, and 458 **seconds**, with p50 and p95 nearly identical. Impossible numbers are a gift: they are never subtle.

The cause was that restarting the publisher reset group sequence numbers to zero, and the relay's per-subscriber "only forward groups newer than the last one I sent" filter then discarded the entire new stream, because long-lived downstream subscriptions still held the previous high sequence number. Directly attached us-west subscribers received 110 MB; all four edge regions received an identical 2.4 MB, the stale cached group, and then nothing. In production this is a publisher reconnect silently blackholing every remote region until connections happen to drop.

The fix gives each track a generation that increments when a sequence number moves backwards, and scopes the subscriber filter to a generation; the bump propagates down the tree because each relay republishes locally. Verified by reproducing the swap with all five regions staying live. The A/B was then rerun from scratch on the fixed build, and only those rerun numbers appear in 6.3.

Two lessons. First, an experiment that fails loudly is worth more than one that passes quietly, and a sanity check on the *plausibility* of a metric (not just its value) is what caught this. Second, monotonic counters that survive a peer restart need an epoch, or the first restart is an outage.

## 6.7 What we would do differently

1. **Subscriber-driven track switching** on the MoQ path, which is the fix for 6.3 and the highest-value change available.
2. **Give WebRTC bandwidth estimation** before quoting its resilience widely; fixed-rate flatters it under caps it happens to fit.
3. **Add a decodability metric.** Delivered fraction of offered bitrate is a start, but frame-level accounting would end the blind spot in 6.2 properly.
4. **Jittered backoff and warm standby connections**, from the fault-injection result in 6.4.
5. **Correlated (bursty) loss** rather than independent loss, which currently understates the value of per-group stream isolation.
6. **Pin publisher CPU or lower the top tier** so baseline stalls read zero instead of reflecting host saturation.
