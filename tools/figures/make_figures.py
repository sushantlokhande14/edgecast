"""Build the report figures from committed experiment output.

Reads results/<matrix>/summary.csv plus results/<matrix>/raw/*.json and writes
PNGs to docs/images/. Runs in a python:3.12-slim container with matplotlib; no
host Python needed. See tools/figures/README.md.
"""

import csv
import glob
import json
import os
from collections import defaultdict

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
from matplotlib.ticker import FuncFormatter

RESULTS = os.environ.get("RESULTS_DIR", "/results/paper")
OUT = os.environ.get("OUT_DIR", "/out")

PROTO_ORDER = ["moq", "webrtc", "hls"]
PROTO_LABEL = {"moq": "MoQ (relay)", "webrtc": "WebRTC (SFU)", "hls": "HLS (HTTP)"}
PROTO_COLOR = {"moq": "#4C6EF5", "webrtc": "#12B886", "hls": "#F76707"}
PROFILE_ORDER = [
    "baseline",
    "home-wifi",
    "mobile-4g",
    "mobile-3g",
    "lossy",
    "satellite",
    "burst-loss",
    "degrading",
]

plt.rcParams.update(
    {
        "figure.dpi": 160,
        "savefig.dpi": 160,
        "font.size": 10,
        "axes.titlesize": 12,
        "axes.titleweight": "bold",
        "axes.grid": True,
        "grid.alpha": 0.25,
        "axes.axisbelow": True,
        "axes.spines.top": False,
        "axes.spines.right": False,
        "figure.autolayout": False,
    }
)


def load_rows(results_dir):
    path = os.path.join(results_dir, "summary.csv")
    with open(path, newline="") as fh:
        rows = list(csv.DictReader(fh))
    for r in rows:
        for k, v in list(r.items()):
            if k in ("profile", "protocol"):
                continue
            r[k] = float(v) if v not in ("", None) else float("nan")
    return rows


def cell(rows, profile, protocol, field):
    for r in rows:
        if r["profile"] == profile and r["protocol"] == protocol:
            return r[field]
    return float("nan")


def profiles_present(rows):
    have = {r["profile"] for r in rows}
    return [p for p in PROFILE_ORDER if p in have]


def grouped_bar(rows, field, title, ylabel, filename, logy=False, annotate="{:.0f}"):
    profs = profiles_present(rows)
    fig, ax = plt.subplots(figsize=(11, 4.6))
    n = len(PROTO_ORDER)
    width = 0.26
    xs = range(len(profs))
    for i, proto in enumerate(PROTO_ORDER):
        vals = [cell(rows, p, proto, field) for p in profs]
        offs = [x + (i - (n - 1) / 2) * width for x in xs]
        bars = ax.bar(
            offs, vals, width, label=PROTO_LABEL[proto], color=PROTO_COLOR[proto], edgecolor="white", linewidth=0.6
        )
        for b, v in zip(bars, vals):
            if v == v:  # not NaN
                ax.annotate(
                    annotate.format(v),
                    (b.get_x() + b.get_width() / 2, v),
                    textcoords="offset points",
                    xytext=(0, 2),
                    ha="center",
                    fontsize=7,
                    color="#333",
                )
    if logy:
        ax.set_yscale("log")
    ax.set_xticks(list(xs))
    ax.set_xticklabels(profs, rotation=0)
    ax.set_ylabel(ylabel)
    ax.set_title(title)
    ax.legend(frameon=False, ncol=3, loc="upper left")
    fig.tight_layout()
    fig.savefig(os.path.join(OUT, filename))
    plt.close(fig)
    print("wrote", filename)


def startup_p50_p95(rows, filename):
    """Startup delay with p50 bars and p95 whisker caps."""
    profs = profiles_present(rows)
    fig, ax = plt.subplots(figsize=(11, 4.6))
    width = 0.26
    xs = range(len(profs))
    for i, proto in enumerate(PROTO_ORDER):
        p50 = [cell(rows, p, proto, "startup_p50_ms") for p in profs]
        p95 = [cell(rows, p, proto, "startup_p95_ms") for p in profs]
        offs = [x + (i - 1) * width for x in xs]
        ax.bar(offs, p50, width, label=PROTO_LABEL[proto], color=PROTO_COLOR[proto], edgecolor="white", linewidth=0.6)
        for o, a, b in zip(offs, p50, p95):
            if a == a and b == b:
                ax.plot([o, o], [a, b], color="#222", linewidth=1.1, zorder=5)
                ax.plot([o - width * 0.28, o + width * 0.28], [b, b], color="#222", linewidth=1.1, zorder=5)
    ax.set_xticks(list(xs))
    ax.set_xticklabels(profs)
    ax.set_ylabel("startup delay (ms)")
    ax.set_title("Time to first media: bar = p50, whisker cap = p95 (lower is better)")
    ax.legend(frameon=False, ncol=3, loc="upper left")
    fig.tight_layout()
    fig.savefig(os.path.join(OUT, filename))
    plt.close(fig)
    print("wrote", filename)


def load_runs(results_dir, profile, protocol):
    out = []
    for path in sorted(glob.glob(os.path.join(results_dir, "raw", "*.json"))):
        with open(path) as fh:
            rr = json.load(fh)
        if rr.get("ProfileName", rr.get("profile")) == profile and rr.get("Protocol", rr.get("protocol")) == protocol:
            out.append(rr)
    return out


def timeline(results_dir, profile, title, filename, shade_from_timeline=True):
    """Mean per-session throughput second by second, all protocols overlaid."""
    fig, ax = plt.subplots(figsize=(11, 4.4))
    shaded = False
    for proto in PROTO_ORDER:
        runs = load_runs(results_dir, profile, proto)
        if not runs:
            continue
        # Align every run to its own start, average across runs and sessions.
        acc = defaultdict(list)
        for rr in runs:
            base = rr["started_unix_ms"] // 1000
            for s in rr["sessions"]:
                for sec in s.get("seconds") or []:
                    rel = sec["t"] - base
                    if 0 <= rel <= rr["warmup_s"] + rr["measure_s"]:
                        acc[rel].append(sec["bytes"] * 8 / 1000.0)
        if not acc:
            continue
        ts = sorted(acc)
        ys = [sum(acc[t]) / len(acc[t]) for t in ts]
        ax.plot(ts, ys, label=PROTO_LABEL[proto], color=PROTO_COLOR[proto], linewidth=1.9)
        if shade_from_timeline and not shaded:
            events = runs[0].get("profile_def", {}).get("timeline") or []
            open_at = None
            for ev in events:
                a = ev.get("apply", {})
                if (a.get("loss_pct") or 0) > 0 and open_at is None:
                    open_at = ev["at_seconds"]
                elif (a.get("loss_pct") or 0) == 0 and open_at is not None:
                    ax.axvspan(open_at, ev["at_seconds"], color="#E03131", alpha=0.10, lw=0)
                    open_at = None
            for ev in events:
                ax.axvline(ev["at_seconds"], color="#868E96", linestyle=":", linewidth=1)
            shaded = True
    ax.set_xlabel("seconds since session start (warmup included)")
    ax.set_ylabel("per-session throughput (kbps)")
    ax.set_title(title)
    ax.legend(frameon=False, ncol=3, loc="upper right")
    fig.tight_layout()
    fig.savefig(os.path.join(OUT, filename))
    plt.close(fig)
    print("wrote", filename)


def latency_vs_stall(rows, filename):
    """The trade-off plane: median latency against stalled time."""
    fig, ax = plt.subplots(figsize=(7.6, 5.2))
    for proto in PROTO_ORDER:
        xs, ys, labels = [], [], []
        for p in profiles_present(rows):
            x = cell(rows, p, proto, "e2e_p50_ms")
            y = cell(rows, p, proto, "stalled_pct")
            if x == x and y == y:
                xs.append(max(x, 1))
                ys.append(y)
                labels.append(p)
        ax.scatter(xs, ys, s=70, color=PROTO_COLOR[proto], label=PROTO_LABEL[proto], alpha=0.85, edgecolor="white")
        for x, y, lab in zip(xs, ys, labels):
            ax.annotate(lab, (x, y), textcoords="offset points", xytext=(5, 4), fontsize=7, color="#555")
    ax.set_xscale("log")
    ax.xaxis.set_major_formatter(FuncFormatter(lambda v, _: f"{v:,.0f}"))
    ax.set_xlabel("median end-to-end latency (ms, log scale)")
    ax.set_ylabel("stalled time (% of measure window)")
    ax.set_title("The trade-off plane: latency against stalls\n(bottom-left is the good corner)")
    ax.legend(frameon=False)
    fig.tight_layout()
    fig.savefig(os.path.join(OUT, filename))
    plt.close(fig)
    print("wrote", filename)


def ab_compare(rows_a, rows_b, label_a, label_b, profiles, filename):
    """Side-by-side stalled % for the ABR diagnosis experiment."""
    fig, axes = plt.subplots(1, 2, figsize=(11, 4.2))
    for ax, field, ylabel, title in (
        (axes[0], "stalled_pct", "stalled time (%)", "Stalled time"),
        (axes[1], "throughput_kbps", "goodput (kbps)", "Delivered goodput"),
    ):
        width = 0.36
        xs = range(len(profiles))
        va = [cell(rows_a, p, "moq", field) for p in profiles]
        vb = [cell(rows_b, p, "moq", field) for p in profiles]
        ax.bar([x - width / 2 for x in xs], va, width, label=label_a, color="#E03131", edgecolor="white")
        ax.bar([x + width / 2 for x in xs], vb, width, label=label_b, color="#2F9E44", edgecolor="white")
        for x, v in zip(xs, va):
            if v == v:
                ax.annotate(f"{v:.0f}", (x - width / 2, v), ha="center", xytext=(0, 2), textcoords="offset points", fontsize=8)
        for x, v in zip(xs, vb):
            if v == v:
                ax.annotate(f"{v:.0f}", (x + width / 2, v), ha="center", xytext=(0, 2), textcoords="offset points", fontsize=8)
        ax.set_xticks(list(xs))
        ax.set_xticklabels(profiles)
        ax.set_ylabel(ylabel)
        ax.set_title(title)
        ax.legend(frameon=False)
    fig.suptitle(
        "Diagnosis check: the MoQ collapse is publisher rate selection, not QUIC transport", fontweight="bold"
    )
    fig.tight_layout()
    fig.savefig(os.path.join(OUT, filename))
    plt.close(fig)
    print("wrote", filename)


def main():
    os.makedirs(OUT, exist_ok=True)
    rows = load_rows(RESULTS)

    startup_p50_p95(rows, "fig-startup.png")
    grouped_bar(
        rows,
        "stalled_pct",
        "Stalled time as a share of the measure window (lower is better)",
        "stalled (%)",
        "fig-stalls.png",
        annotate="{:.1f}",
    )
    grouped_bar(
        rows,
        "e2e_p50_ms",
        "Median end-to-end latency, publisher stamp to arrival (log scale, lower is better)",
        "latency (ms)",
        "fig-latency.png",
        logy=True,
    )
    grouped_bar(
        rows,
        "throughput_kbps",
        "Delivered goodput per session during the measure window",
        "kbps",
        "fig-throughput.png",
    )
    latency_vs_stall(rows, "fig-tradeoff.png")
    timeline(
        RESULTS,
        "burst-loss",
        "Recovery behavior: two 5% packet-loss bursts (shaded) and what happens after",
        "fig-burst-loss.png",
    )
    timeline(
        RESULTS,
        "degrading",
        "Progressive degradation: link steps down at 15s, 30s, and 45s (dotted)",
        "fig-degrading.png",
    )

    ab_dir = os.environ.get("AB_DIR")
    if ab_dir and os.path.exists(os.path.join(ab_dir, "summary.csv")):
        rows_ab = load_rows(ab_dir)
        profs = [p for p in ["mobile-4g", "mobile-3g", "lossy"] if p in {r["profile"] for r in rows_ab}]
        ab_compare(
            rows,
            rows_ab,
            "publisher at 5000 kbps (ABR blind to access link)",
            "publisher pinned to 1000 kbps",
            profs,
            "fig-abr-diagnosis.png",
        )


if __name__ == "__main__":
    main()
