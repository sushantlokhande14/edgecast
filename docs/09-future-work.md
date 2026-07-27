# 9. Future work

Roughly ordered by value per effort:

1. **Real moq-transport wire format.** Swap the JSON control plane for varint-coded moq-transport messages and interop-test against an existing implementation (e.g. moq-rs). The relay core (group buffers, fanout, pull-through) stays; only `internal/proto` changes.
2. **WebTransport ingest.** Add an HTTP/3 WebTransport listener beside raw QUIC so browser subscribers can join the MoQ tree; the transport abstraction already isolates the session type.
3. **Subscriber-driven ABR on MoQ.** Publish the ladder as parallel tracks and let subscribers switch tracks on measured throughput, mirroring how MoQ deployments are expected to do ABR; compare against the current publisher-side backpressure ladder.
4. **GCC/TWCC on the WebRTC path.** Pion interceptors support TWCC feedback; adding a bandwidth-estimating publisher would make the WebRTC comparison fair under rate caps.
5. **Correlated loss.** Replace iid loss with a Gilbert-Elliott two-state model in the emulator; burst loss is where stream-per-group isolation should shine and iid understates it.
6. **Kernel netem mode.** On hosts whose kernel ships `sch_netem`, offer a compose overlay applying tc in privileged sidecars, and validate the userspace emulator against it.
7. **LL-HLS variant.** Partial segments and blocking playlist reloads would show how much of HLS's latency gap is protocol vs configuration.
8. **Multi-track scale runs.** Hundreds of tracks with Zipf-distributed popularity to measure relay cache behavior and memory.
9. **CI.** A GitHub Actions job compiling the tree and running a 2-run micro-matrix in the runner to catch regressions in the measurement pipeline.
10. **Priority experiments.** Implement object priorities (drop deltas before keyframes) and measure quality-of-experience under the same profiles.

## Open issues

- **`edgecast_pub_bitrate_kbps` intermittently reads zero.** The gauge read zero for the publisher instance across an entire 80-minute matrix while the publisher was demonstrably at its top tier, then reported correctly afterwards. Root cause unknown. Likely candidates: the admin server serving `/metrics` before the publisher goroutine performs its first `Set`, combined with package-level registration exporting the same metric name from every role. Fix direction: register role-owned collectors inside the role constructor and set an initial value before the admin server starts.
- **Reconnect backoff is flat and unjittered.** The fault-injection experiment showed recovery taking longer than the outage itself, dominated by client backoff. Jittered exponential backoff plus a warm standby connection to a second relay would both shorten recovery and avoid synchronised reconnect storms.
- **Delivered fraction is a coarse quality proxy.** It closes the measurement blind spot described in `docs/06-findings.md` section 6.2 only partially; frame-level decodability accounting would close it properly.
