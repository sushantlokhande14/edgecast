# EdgeCast

A local, reproducible testbed for evaluating realtime media delivery protocols: **Media over QUIC (MoQ-style relay fanout)**, **WebRTC (SFU)**, and **HTTP segment streaming (HLS-style)**, under controlled network impairment (latency, jitter, packet loss, bandwidth caps).

> Status: project charter. Implementation lands in staged commits; see the roadmap below.

## Why this project exists

Realtime media delivery is in the middle of a protocol shift. WebRTC has owned sub-second latency for a decade, HTTP adaptive streaming owns scale, and Media over QUIC (MoQ) is the IETF's attempt to get both from one relay-friendly protocol. Public comparisons are mostly vendor blog posts measured on unlike setups. This testbed puts all three delivery models on the same host, driven by the same synthetic media source, measured by the same definitions, under the same emulated network conditions, so the numbers are actually comparable.

## What gets measured

For every protocol, per session: startup delay, end-to-end latency, throughput, interarrival jitter, stall count and stalled time, and post-impairment recovery time. Definitions are pinned in [docs/03-experiment-design.md](docs/03-experiment-design.md).

## Planned architecture (summary)

- A single Go binary (`edgecast`) with one subcommand per role: MoQ relay, MoQ publisher, MoQ subscriber load generator, WebRTC SFU, WebRTC publisher/subscriber, HLS origin, HLS client load generator, experiment controller.
- 5 simulated regions: an origin relay plus edge relays in a pull-through tree, each region's access link shaped independently with `tc netem`.
- Prometheus scrapes every component; Grafana ships with provisioned dashboards.
- `expctl` runs a scenario matrix (protocol x network profile x repetitions), restarts sessions per run, collects per-session JSON results, and emits CSV plus a markdown summary.

Full detail with diagrams: [docs/02-architecture.md](docs/02-architecture.md)

## Roadmap (staged commits)

- [x] Stage 1: charter, objectives, architecture, experiment design
- [ ] Stage 2: synthetic media core, MoQ wire protocol, relay + publisher + subscriber over QUIC
- [ ] Stage 3: WebRTC SFU path, HLS path
- [ ] Stage 4: netem agent, scenario profiles, experiment controller
- [ ] Stage 5: Prometheus + Grafana observability stack, full compose topology
- [ ] Stage 6: measured results from the smoke matrix
- [ ] Stage 7: findings, limitations, prototype vs production analysis

## Docs

| Doc | Contents |
| --- | --- |
| [01-objectives.md](docs/01-objectives.md) | Research questions and success criteria |
| [02-architecture.md](docs/02-architecture.md) | Components, topology, data flow, diagrams |
| [03-experiment-design.md](docs/03-experiment-design.md) | Metric definitions, network profiles, run protocol |

## License

MIT
