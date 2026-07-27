"""Summarises the relay-failure fault injection and plots it.

Reads results/faultinj/{treatment,control,meta}.json and writes
docs/images/fig-relay-failure.png plus a short text summary on stdout.
"""

import json
import os
from collections import defaultdict

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt

IN = os.environ.get("FAULT_DIR", "/results/faultinj")
OUT = os.environ.get("OUT_DIR", "/out")


def series(path, killed, lo=-30, hi=60):
    """Mean per-session throughput on a continuous axis.

    A session that receives nothing in a given second emits no bucket at all,
    so averaging only over buckets that exist makes an outage invisible: the
    surviving sessions hold the mean up. Missing seconds must be counted as
    zero, over a fixed session count and a fixed time axis.
    """
    with open(path) as fh:
        sessions = json.load(fh)
    n = max(len(sessions), 1)
    total = defaultdict(float)
    stalls = 0.0
    for s in sessions:
        stalls += s.get("stalled_ms") or 0
        for sec in s.get("seconds") or []:
            rel = sec["t"] - killed
            if lo <= rel <= hi:
                total[rel] += sec["bytes"] * 8 / 1000.0
    ts = list(range(lo, hi + 1))
    return ts, [total.get(t, 0.0) / n for t in ts], stalls / n, len(sessions)


def main():
    with open(os.path.join(IN, "meta.json")) as fh:
        meta = json.load(fh)
    killed = meta["killed_unix"]
    down_for = meta["restarted_unix"] - killed

    t_ts, t_ys, t_stall, t_n = series(os.path.join(IN, "treatment-eu-central.json"), killed)
    c_ts, c_ys, c_stall, c_n = series(os.path.join(IN, "control-us-east.json"), killed)

    # Recovery: first second after restart at which the treatment region is
    # back to 90% of its own pre-kill baseline. The baseline window is the
    # settled seconds just before the kill, not all of t<0, which would
    # include the ramp before sessions were established and understate it.
    base = [y for t, y in zip(t_ts, t_ys) if -12 <= t <= -2]
    baseline = sum(base) / len(base) if base else 0
    recovery = None
    for t, y in zip(t_ts, t_ys):
        if t >= down_for and baseline and y >= 0.9 * baseline:
            recovery = t - down_for
            break

    fig, ax = plt.subplots(figsize=(11, 4.4))
    ax.axvspan(0, down_for, color="#E03131", alpha=0.12, lw=0)
    ax.axvline(0, color="#E03131", lw=1.4)
    ax.axvline(down_for, color="#2F9E44", lw=1.4)
    ax.plot(t_ts, t_ys, color="#4C6EF5", lw=2, label=f"eu-central (behind the killed relay, n={t_n})")
    ax.plot(c_ts, c_ys, color="#868E96", lw=1.8, ls="--", label=f"us-east control (n={c_n})")
    ax.annotate("relay killed", (0, ax.get_ylim()[1] * 0.94), color="#E03131", fontsize=8.5, ha="left", rotation=90)
    ax.annotate("relay restarted", (down_for, ax.get_ylim()[1] * 0.94), color="#2F9E44", fontsize=8.5, ha="left", rotation=90)
    ax.set_xlabel("seconds relative to the kill")
    ax.set_ylabel("per-session throughput (kbps)")
    ax.set_title("Fault injection: an edge relay is killed and restarted while 20 sessions are watching")
    ax.legend(frameon=False, loc="lower right")
    ax.grid(alpha=0.25)
    fig.tight_layout()
    fig.savefig(os.path.join(OUT, "fig-relay-failure.png"), dpi=160)

    print(f"baseline_kbps={baseline:.0f}")
    print(f"down_seconds={down_for}")
    print(f"recovery_seconds_after_restart={recovery}")
    print(f"treatment_stalled_ms_per_session={t_stall:.0f}")
    print(f"control_stalled_ms_per_session={c_stall:.0f}")


if __name__ == "__main__":
    main()
