#!/usr/bin/env python3
import json
import statistics
from pathlib import Path

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
import numpy as np
from matplotlib.ticker import FuncFormatter

HERE = Path(__file__).resolve().parent
ASSETS = HERE / "assets"
ASSETS.mkdir(parents=True, exist_ok=True)

OC = "#2563EB"
OC_LIGHT = "#93C5FD"
W = "#D97706"
W_LIGHT = "#FCD34D"
INK = "#111827"
MUTED = "#6B7280"
GRID = "#E5E7EB"

AGENTS = {"opencode": "OpenCode", "wombat": "Wombat"}
COLORS = {"opencode": OC, "wombat": W}

plt.rcParams.update(
    {
        "font.family": "DejaVu Sans",
        "font.size": 10,
        "axes.edgecolor": "#D1D5DB",
        "axes.labelcolor": MUTED,
        "axes.titlecolor": INK,
        "axes.titlesize": 12,
        "axes.titleweight": "bold",
        "axes.labelsize": 10,
        "xtick.color": MUTED,
        "ytick.color": MUTED,
        "text.color": INK,
        "axes.grid": True,
        "grid.color": GRID,
        "grid.linewidth": 0.8,
        "figure.facecolor": "white",
        "axes.facecolor": "white",
        "savefig.facecolor": "white",
        "legend.frameon": False,
    }
)

rows = json.loads((HERE / "results.json").read_text())
tasks = list(dict.fromkeys(r["task"] for r in rows))
by_task = {task: {r["agent"]: r for r in rows if r["task"] == task} for task in tasks}


def metric(agent, key):
    return sum(r[key] for r in rows if r["agent"] == agent)


def cached(agent):
    return sum(r.get("cache_read", 0) + r.get("cache_write", 0) for r in rows if r["agent"] == agent)


def uncached(agent):
    return metric(agent, "input_tokens") - cached(agent)


def passed(agent):
    return sum(1 for r in rows if r["agent"] == agent and r["passed"])


def fmt_int(value):
    return f"{value:,.0f}"


def fmt_k(value):
    if value >= 1000:
        return f"{value / 1000:.0f}k"
    return f"{value:.0f}"


def comma_axis(axis):
    axis.yaxis.set_major_formatter(FuncFormatter(lambda value, _: f"{value:,.0f}"))


def finish(axis):
    axis.spines["top"].set_visible(False)
    axis.spines["right"].set_visible(False)


totals = {
    agent: {
        "input": metric(agent, "input_tokens"),
        "cached": cached(agent),
        "uncached": uncached(agent),
        "output": metric(agent, "output_tokens"),
        "tools": metric(agent, "tool_calls"),
        "steps": metric(agent, "model_steps"),
        "time": metric(agent, "duration_s"),
        "passed": passed(agent),
    }
    for agent in AGENTS
}

labels = [f"{AGENTS[a]}\n{totals[a]['passed']}/15" for a in ("opencode", "wombat")]
x = np.arange(2)

fig, axes = plt.subplots(2, 2, figsize=(12, 8.6))
fig.suptitle("Wombat vs OpenCode — 15-task agent benchmark", x=0.02, y=0.98, ha="left", fontsize=17, fontweight="bold")
fig.text(0.02, 0.935, "Model: opencode-go/beam-free · Same skill content, MCP server, and isolated workspaces", color=MUTED, fontsize=10)

ax = axes[0, 0]
oc_cached, w_cached = totals["opencode"]["cached"], totals["wombat"]["cached"]
oc_uncached, w_uncached = totals["opencode"]["uncached"], totals["wombat"]["uncached"]
ax.bar(x, [oc_cached, w_cached], width=0.55, color=[OC_LIGHT, W_LIGHT], edgecolor="white", label="Cached")
ax.bar(x, [oc_uncached, w_uncached], bottom=[oc_cached, w_cached], width=0.55, color=[OC, W], edgecolor="white", label="Uncached")
for i, agent in enumerate(("opencode", "wombat")):
    total = totals[agent]["input"]
    ax.text(i, total * 1.015, fmt_int(total), ha="center", va="bottom", fontsize=9.5, fontweight="bold")
    ax.text(i, totals[agent]["cached"] / 2, fmt_int(totals[agent]["cached"]), ha="center", va="center", fontsize=8.5, color=INK)
    uncached_mid = totals[agent]["cached"] + totals[agent]["uncached"] / 2
    ax.text(i, uncached_mid, fmt_int(totals[agent]["uncached"]), ha="center", va="center", fontsize=8.5, color="white")
ax.set_xticks(x, labels)
ax.set_ylim(0, max(t["input"] for t in totals.values()) * 1.16)
ax.set_title("Prompt tokens (cached vs uncached)", loc="left")
ax.text(0.02, 0.98, "Light = cached · dark = uncached", transform=ax.transAxes, va="top", fontsize=8.5, color=MUTED)
comma_axis(ax)
finish(ax)

ax = axes[0, 1]
values = [totals[a]["output"] for a in ("opencode", "wombat")]
ax.bar(x, values, width=0.55, color=[OC, W])
for i, value in enumerate(values):
    ax.text(i, value * 1.015, fmt_int(value), ha="center", va="bottom", fontsize=10, fontweight="bold")
ax.set_xticks(x, labels)
ax.set_ylim(0, max(values) * 1.16)
ax.set_title("Output tokens", loc="left")
comma_axis(ax)
finish(ax)

ax = axes[1, 0]
values = [totals[a]["tools"] for a in ("opencode", "wombat")]
ax.bar(x, values, width=0.55, color=[OC, W])
for i, value in enumerate(values):
    ax.text(i, value + 2, f"{value}", ha="center", va="bottom", fontsize=10, fontweight="bold")
ax.set_xticks(x, labels)
ax.set_ylim(0, max(values) * 1.16)
ax.set_title("Tool calls", loc="left")
finish(ax)

ax = axes[1, 1]
values = [totals[a]["time"] for a in ("opencode", "wombat")]
ax.bar(x, values, width=0.55, color=[OC, W])
for i, value in enumerate(values):
    ax.text(i, value + 8, f"{value:.0f}s", ha="center", va="bottom", fontsize=10, fontweight="bold")
ax.set_xticks(x, labels)
ax.set_ylim(0, max(values) * 1.16)
ax.set_title("Total wall time", loc="left")
ax.set_ylabel("seconds")
finish(ax)

fig.text(
    0.02,
    0.015,
    "Uncached input = prompt tokens − cache reads. Time is wall-clock agent time, excluding checks.",
    color=MUTED,
    fontsize=8.5,
)
fig.tight_layout(rect=[0, 0.03, 1, 0.90])
fig.savefig(ASSETS / "summary.svg", bbox_inches="tight")
plt.close(fig)

fig, ax = plt.subplots(figsize=(9, 5.2))
relative = [
    ("Uncached input", totals["opencode"]["uncached"] / totals["wombat"]["uncached"] * 100),
    ("Total input", totals["opencode"]["input"] / totals["wombat"]["input"] * 100),
    ("Model steps", totals["opencode"]["steps"] / totals["wombat"]["steps"] * 100),
    ("Tool calls", totals["opencode"]["tools"] / totals["wombat"]["tools"] * 100),
    ("Output tokens", totals["opencode"]["output"] / totals["wombat"]["output"] * 100),
    ("Wall time", totals["opencode"]["time"] / totals["wombat"]["time"] * 100),
]
names = [name for name, _ in relative]
vals = [value for _, value in relative]
y = np.arange(len(relative))
bars = ax.barh(y, vals, height=0.55, color=OC)
ax.axvline(100, color=W, linewidth=2, linestyle="--")
for i, value in enumerate(vals):
    ax.text(value + 5, i, f"{value:.0f}%", va="center", fontsize=10, fontweight="bold")
ax.set_yticks(y, names)
ax.invert_yaxis()
ax.set_xlim(0, max(vals) * 1.18)
ax.set_xlabel("OpenCode / Wombat (Wombat = 100%)")
ax.set_title("OpenCode usage relative to Wombat", loc="left")
ax.legend(handles=[plt.Line2D([0], [0], color=W, linestyle="--", linewidth=2, label="Wombat = 100%")], loc="upper right", fontsize=9)
ax.grid(axis="y", visible=False)
finish(ax)
fig.tight_layout()
fig.savefig(ASSETS / "relative.svg", bbox_inches="tight")
plt.close(fig)


def per_task_chart(metric_key, title, filename, value_formatter, note):
    order = sorted(tasks, key=lambda task: by_task[task]["opencode"][metric_key], reverse=True)
    fig, ax = plt.subplots(figsize=(11, 8.6))
    y = np.arange(len(order))
    height = 0.38
    oc_values = [by_task[task]["opencode"][metric_key] for task in order]
    w_values = [by_task[task]["wombat"][metric_key] for task in order]
    ax.barh(y - height / 2, oc_values, height=height, color=OC, label="OpenCode")
    ax.barh(y + height / 2, w_values, height=height, color=W, label="Wombat")
    largest = max(oc_values + w_values)
    for i, (oc_value, w_value) in enumerate(zip(oc_values, w_values)):
        ax.text(oc_value + largest * 0.012, i - height / 2, value_formatter(oc_value), va="center", fontsize=8.5, color=INK)
        ax.text(w_value + largest * 0.012, i + height / 2, value_formatter(w_value), va="center", fontsize=8.5, color=INK)
    ax.set_yticks(y, [task.replace("_", " ") for task in order])
    ax.invert_yaxis()
    ax.set_xlim(0, largest * 1.17)
    ax.set_title(title, loc="left")
    ax.text(0.99, 1.01, note, transform=ax.transAxes, ha="right", va="bottom", fontsize=9, color=MUTED)
    ax.legend(loc="lower right")
    ax.grid(axis="y", visible=False)
    finish(ax)
    fig.tight_layout()
    fig.savefig(ASSETS / filename, bbox_inches="tight")
    plt.close(fig)


per_task_chart(
    "duration_s",
    "Wall time by task",
    "per-task-time.svg",
    lambda value: f"{value:.0f}s",
    "Total: OpenCode 666s · Wombat 177s (3.75×)",
)
per_task_chart(
    "tool_calls",
    "Tool calls by task",
    "per-task-tools.svg",
    lambda value: f"{value:.0f}",
    "Total: OpenCode 135 · Wombat 78 (1.73×)",
)
per_task_chart(
    "input_tokens",
    "Prompt tokens by task (cache included)",
    "per-task-input.svg",
    fmt_k,
    "Total: OpenCode 990k · Wombat 755k (1.31×)",
)

order = tasks
x = np.arange(len(order))
oc_cum = np.cumsum([by_task[task]["opencode"]["duration_s"] for task in order])
w_cum = np.cumsum([by_task[task]["wombat"]["duration_s"] for task in order])
fig, ax = plt.subplots(figsize=(12, 5.6))
ax.fill_between(x, w_cum, oc_cum, color=OC, alpha=0.10, linewidth=0)
ax.step(x, oc_cum, where="mid", color=OC, linewidth=2.5, marker="o", markersize=4, label="OpenCode")
ax.step(x, w_cum, where="mid", color=W, linewidth=2.5, marker="o", markersize=4, label="Wombat")
ax.text(x[-1] + 0.15, oc_cum[-1], f"{oc_cum[-1]:.0f}s", color=OC, fontsize=10, fontweight="bold", va="center")
ax.text(x[-1] + 0.15, w_cum[-1], f"{w_cum[-1]:.0f}s", color=W, fontsize=10, fontweight="bold", va="center")
ax.set_xticks(x, [task.replace("_", " ") for task in order], rotation=40, ha="right")
ax.set_xlim(-0.3, len(order) - 0.4)
ax.set_ylim(0, oc_cum[-1] * 1.10)
ax.set_ylabel("cumulative seconds")
ax.set_title("Cumulative wall time across the suite", loc="left")
ax.legend(loc="upper left")
finish(ax)
fig.tight_layout()
fig.savefig(ASSETS / "cumulative-time.svg", bbox_inches="tight")
plt.close(fig)

fig, ax = plt.subplots(figsize=(9.5, 6.4))
annotate = {
    "opencode": ["refactor_inventory", "data_sales", "snake_game", "website"],
    "wombat": [],
}
offsets = {
    ("opencode", "refactor_inventory"): (6, 4),
    ("opencode", "data_sales"): (6, 4),
    ("opencode", "snake_game"): (-10, 10),
    ("opencode", "website"): (6, 4),
}
for agent in ("opencode", "wombat"):
    xs = [by_task[task][agent]["tool_calls"] for task in tasks]
    ys = [by_task[task][agent]["duration_s"] for task in tasks]
    ax.scatter(xs, ys, s=72, color=COLORS[agent], alpha=0.85, edgecolor="white", linewidth=0.8, label=AGENTS[agent], zorder=3)
    for task in annotate[agent]:
        row = by_task[task][agent]
        ax.annotate(
            task.replace("_", " "),
            (row["tool_calls"], row["duration_s"]),
            xytext=offsets.get((agent, task), (5, 5)),
            textcoords="offset points",
            fontsize=8,
            color=COLORS[agent],
        )
ax.set_xlabel("tool calls")
ax.set_ylabel("wall time (seconds)")
ax.set_xlim(0, max(by_task[task][agent]["tool_calls"] for task in tasks for agent in AGENTS) + 1.5)
ax.set_ylim(0, max(by_task[task][agent]["duration_s"] for task in tasks for agent in AGENTS) * 1.12)
ax.set_title("Wall time vs tool calls", loc="left")
ax.text(
    0.99,
    1.01,
    "Each point is one task. Upper-right = more work.",
    transform=ax.transAxes,
    ha="right",
    va="bottom",
    fontsize=9,
    color=MUTED,
)
ax.legend(loc="upper left")
finish(ax)
fig.tight_layout()
fig.savefig(ASSETS / "time-vs-tools.svg", bbox_inches="tight")
plt.close(fig)

oc = totals["opencode"]
w = totals["wombat"]
oc_median = statistics.median([r["duration_s"] for r in rows if r["agent"] == "opencode"])
w_median = statistics.median([r["duration_s"] for r in rows if r["agent"] == "wombat"])

summary_rows = [
    ("Tasks passed", f"{oc['passed']}/15", f"{w['passed']}/15", "—"),
    ("Input tokens", fmt_int(oc["input"]), fmt_int(w["input"]), f"{oc['input'] / w['input']:.2f}×"),
    ("Uncached input", fmt_int(oc["uncached"]), fmt_int(w["uncached"]), f"{oc['uncached'] / w['uncached']:.2f}×"),
    ("Output tokens", fmt_int(oc["output"]), fmt_int(w["output"]), f"{oc['output'] / w['output']:.2f}×"),
    ("Tool calls", f"{oc['tools']}", f"{w['tools']}", f"{oc['tools'] / w['tools']:.2f}×"),
    ("Model steps", f"{oc['steps']}", f"{w['steps']}", f"{oc['steps'] / w['steps']:.2f}×"),
    ("Total wall time", f"{oc['time']:.1f}s", f"{w['time']:.1f}s", f"{oc['time'] / w['time']:.2f}×"),
    ("Mean time / task", f"{oc['time'] / 15:.1f}s", f"{w['time'] / 15:.1f}s", f"{oc['time'] / w['time']:.2f}×"),
    ("Median time / task", f"{oc_median:.1f}s", f"{w_median:.1f}s", f"{oc_median / w_median:.2f}×"),
]
print("| Metric | OpenCode | Wombat | OC / W |")
print("|---|---:|---:|---:|")
for row in summary_rows:
    print(f"| {row[0]} | {row[1]} | {row[2]} | {row[3]} |")

print("")
print("| Task | In tokens | Out tokens | Tools | Agents | Time (s) | Pass |")
print("|---|---:|---:|---:|---:|---:|:---:|")
for task in tasks:
    oc = by_task[task]["opencode"]
    w = by_task[task]["wombat"]
    print(
        f"| {task} | {oc['input_tokens']:,} / {w['input_tokens']:,} | "
        f"{oc['output_tokens']:,} / {w['output_tokens']:,} | "
        f"{oc['tool_calls']} / {w['tool_calls']} | "
        f"{oc['agent_runs']} / {w['agent_runs']} | "
        f"{oc['duration_s']:.1f} / {w['duration_s']:.1f} | "
        f"{'Y' if oc['passed'] else 'N'} / {'Y' if w['passed'] else 'N'} |"
    )
