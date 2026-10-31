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

W = "#D97706"
W_LIGHT = "#FCD34D"
PI = "#7C3AED"
PI_LIGHT = "#C4B5FD"
INK = "#111827"
MUTED = "#6B7280"
GRID = "#E5E7EB"

AGENTS = {"wombat": "Wombat", "pi": "Pi"}
COLORS = {"wombat": W, "pi": PI}

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

labels = [f"{AGENTS[a]}\n{totals[a]['passed']}/18" for a in ("wombat", "pi")]
x = np.arange(2)

fig, axes = plt.subplots(2, 2, figsize=(12, 8.6))
fig.suptitle("Pi vs Wombat — 18-task harness benchmark", x=0.02, y=0.98, ha="left", fontsize=17, fontweight="bold")
fig.text(0.02, 0.935, "Model: opencode-go/beam-free · Same instructions, skill, MCP, and Playwright", color=MUTED, fontsize=10)

ax = axes[0, 0]
w_cached, pi_cached = totals["wombat"]["cached"], totals["pi"]["cached"]
w_uncached, pi_uncached = totals["wombat"]["uncached"], totals["pi"]["uncached"]
ax.bar(x, [w_cached, pi_cached], width=0.55, color=[W_LIGHT, PI_LIGHT], edgecolor="white", label="Cached")
ax.bar(x, [w_uncached, pi_uncached], bottom=[w_cached, pi_cached], width=0.55, color=[W, PI], edgecolor="white", label="Uncached")
for i, agent in enumerate(("wombat", "pi")):
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
values = [totals[a]["output"] for a in ("wombat", "pi")]
ax.bar(x, values, width=0.55, color=[W, PI])
for i, value in enumerate(values):
    ax.text(i, value * 1.015, fmt_int(value), ha="center", va="bottom", fontsize=10, fontweight="bold")
ax.set_xticks(x, labels)
ax.set_ylim(0, max(values) * 1.16)
ax.set_title("Output tokens", loc="left")
comma_axis(ax)
finish(ax)

ax = axes[1, 0]
values = [totals[a]["tools"] for a in ("wombat", "pi")]
ax.bar(x, values, width=0.55, color=[W, PI])
for i, value in enumerate(values):
    ax.text(i, value + 2, f"{value}", ha="center", va="bottom", fontsize=10, fontweight="bold")
ax.set_xticks(x, labels)
ax.set_ylim(0, max(values) * 1.16)
ax.set_title("Tool calls", loc="left")
finish(ax)

ax = axes[1, 1]
values = [totals[a]["time"] for a in ("wombat", "pi")]
ax.bar(x, values, width=0.55, color=[W, PI])
for i, value in enumerate(values):
    ax.text(i, value + 8, f"{value:.0f}s", ha="center", va="bottom", fontsize=10, fontweight="bold")
ax.set_xticks(x, labels)
ax.set_ylim(0, max(values) * 1.16)
ax.set_title("Total wall time", loc="left")
ax.set_ylabel("seconds")
finish(ax)

fig.text(0.02, 0.015, "Uncached input = prompt tokens − cache reads. Time is wall-clock agent time, excluding checks.", color=MUTED, fontsize=8.5)
fig.tight_layout(rect=[0, 0.03, 1, 0.90])
fig.savefig(ASSETS / "summary.svg", bbox_inches="tight")
plt.close(fig)

fig, ax = plt.subplots(figsize=(9, 5.2))
relative = [
    ("Uncached input", totals["wombat"]["uncached"] / totals["pi"]["uncached"] * 100),
    ("Total input", totals["wombat"]["input"] / totals["pi"]["input"] * 100),
    ("Model steps", totals["wombat"]["steps"] / totals["pi"]["steps"] * 100),
    ("Tool calls", totals["wombat"]["tools"] / totals["pi"]["tools"] * 100),
    ("Output tokens", totals["wombat"]["output"] / totals["pi"]["output"] * 100),
    ("Wall time", totals["wombat"]["time"] / totals["pi"]["time"] * 100),
]
names = [name for name, _ in relative]
vals = [value for _, value in relative]
y = np.arange(len(relative))
ax.barh(y, vals, height=0.55, color=W)
ax.axvline(100, color=PI, linewidth=2, linestyle="--")
for i, value in enumerate(vals):
    ax.text(value + 5, i, f"{value:.0f}%", va="center", fontsize=10, fontweight="bold")
ax.set_yticks(y, names)
ax.invert_yaxis()
ax.set_xlim(0, max(vals) * 1.18)
ax.set_xlabel("Wombat / Pi (Pi = 100%)")
ax.set_title("Wombat usage relative to Pi", loc="left")
ax.legend(handles=[plt.Line2D([0], [0], color=PI, linestyle="--", linewidth=2, label="Pi = 100%")], loc="upper right", fontsize=9)
ax.grid(axis="y", visible=False)
finish(ax)
fig.tight_layout()
fig.savefig(ASSETS / "relative.svg", bbox_inches="tight")
plt.close(fig)


def per_task_chart(metric_key, title, filename, value_formatter, note):
    order = sorted(tasks, key=lambda task: max(by_task[task]["wombat"][metric_key], by_task[task]["pi"][metric_key]), reverse=True)
    fig, ax = plt.subplots(figsize=(11, 9.2))
    y = np.arange(len(order))
    height = 0.38
    w_values = [by_task[task]["wombat"][metric_key] for task in order]
    pi_values = [by_task[task]["pi"][metric_key] for task in order]
    ax.barh(y - height / 2, w_values, height=height, color=W, label="Wombat")
    ax.barh(y + height / 2, pi_values, height=height, color=PI, label="Pi")
    largest = max(w_values + pi_values)
    for i, (w_value, pi_value) in enumerate(zip(w_values, pi_values)):
        ax.text(w_value + largest * 0.012, i - height / 2, value_formatter(w_value), va="center", fontsize=8.5, color=INK)
        ax.text(pi_value + largest * 0.012, i + height / 2, value_formatter(pi_value), va="center", fontsize=8.5, color=INK)
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
    "Total: Wombat 386s · Pi 270s (1.43×); medians 8.5s vs 8.0s",
)
per_task_chart(
    "tool_calls",
    "Tool calls by task",
    "per-task-tools.svg",
    lambda value: f"{value:.0f}",
    "Total: Wombat 80 · Pi 76 (1.05×)",
)
per_task_chart(
    "input_tokens",
    "Prompt tokens by task (cache included)",
    "per-task-input.svg",
    fmt_k,
    "Total: Wombat 874k · Pi 416k (2.10×)",
)

order = tasks
x = np.arange(len(order))
w_cum = np.cumsum([by_task[task]["wombat"]["duration_s"] for task in order])
pi_cum = np.cumsum([by_task[task]["pi"]["duration_s"] for task in order])
fig, ax = plt.subplots(figsize=(12, 5.8))
ax.fill_between(x, pi_cum, w_cum, color=W, alpha=0.10, linewidth=0)
ax.step(x, w_cum, where="mid", color=W, linewidth=2.5, marker="o", markersize=4, label="Wombat")
ax.step(x, pi_cum, where="mid", color=PI, linewidth=2.5, marker="o", markersize=4, label="Pi")
ax.text(x[-1] + 0.15, w_cum[-1], f"{w_cum[-1]:.0f}s", color=W, fontsize=10, fontweight="bold", va="center")
ax.text(x[-1] + 0.15, pi_cum[-1], f"{pi_cum[-1]:.0f}s", color=PI, fontsize=10, fontweight="bold", va="center")
ax.set_xticks(x, [task.replace("_", " ") for task in order], rotation=40, ha="right")
ax.set_xlim(-0.3, len(order) - 0.4)
ax.set_ylim(0, max(w_cum[-1], pi_cum[-1]) * 1.10)
ax.set_ylabel("cumulative seconds")
ax.set_title("Cumulative wall time across the suite", loc="left")
ax.legend(loc="upper left")
finish(ax)
fig.tight_layout()
fig.savefig(ASSETS / "cumulative-time.svg", bbox_inches="tight")
plt.close(fig)

fig, ax = plt.subplots(figsize=(9.8, 6.6))
annotate = {
    "wombat": ["skill_adherence"],
    "pi": ["browser_playwright"],
}
offsets = {
    ("wombat", "skill_adherence"): (8, -14),
    ("pi", "browser_playwright"): (-10, 12),
}
for agent in ("wombat", "pi"):
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
ax.text(0.99, 1.01, "Each point is one task. Upper-right = more work.", transform=ax.transAxes, ha="right", va="bottom", fontsize=9, color=MUTED)
ax.legend(loc="upper left")
finish(ax)
fig.tight_layout()
fig.savefig(ASSETS / "time-vs-tools.svg", bbox_inches="tight")
plt.close(fig)

w = totals["wombat"]
pi = totals["pi"]
w_median = statistics.median([r["duration_s"] for r in rows if r["agent"] == "wombat"])
pi_median = statistics.median([r["duration_s"] for r in rows if r["agent"] == "pi"])

summary_rows = [
    ("Tasks passed", f"{w['passed']}/18", f"{pi['passed']}/18", "—"),
    ("Input tokens", fmt_int(w["input"]), fmt_int(pi["input"]), f"{w['input'] / pi['input']:.2f}×"),
    ("Uncached input", fmt_int(w["uncached"]), fmt_int(pi["uncached"]), f"{w['uncached'] / pi['uncached']:.2f}×"),
    ("Output tokens", fmt_int(w["output"]), fmt_int(pi["output"]), f"{w['output'] / pi['output']:.2f}×"),
    ("Tool calls", f"{w['tools']}", f"{pi['tools']}", f"{w['tools'] / pi['tools']:.2f}×"),
    ("Model steps", f"{w['steps']}", f"{pi['steps']}", f"{w['steps'] / pi['steps']:.2f}×"),
    ("Total wall time", f"{w['time']:.1f}s", f"{pi['time']:.1f}s", f"{w['time'] / pi['time']:.2f}×"),
    ("Mean time / task", f"{w['time'] / 18:.1f}s", f"{pi['time'] / 18:.1f}s", f"{w['time'] / pi['time']:.2f}×"),
    ("Median time / task", f"{w_median:.1f}s", f"{pi_median:.1f}s", f"{w_median / pi_median:.2f}×"),
]
print("| Metric | Wombat | Pi | W / Pi |")
print("|---|---:|---:|---:|")
for row in summary_rows:
    print(f"| {row[0]} | {row[1]} | {row[2]} | {row[3]} |")

print("")
print("| Task | In tokens | Out tokens | Tools | Agents | Time (s) | Pass |")
print("|---|---:|---:|---:|---:|---:|:---:|")
for task in tasks:
    w_row = by_task[task]["wombat"]
    pi_row = by_task[task]["pi"]
    print(
        f"| {task} | {w_row['input_tokens']:,} / {pi_row['input_tokens']:,} | "
        f"{w_row['output_tokens']:,} / {pi_row['output_tokens']:,} | "
        f"{w_row['tool_calls']} / {pi_row['tool_calls']} | "
        f"{w_row['agent_runs']} / {pi_row['agent_runs']} | "
        f"{w_row['duration_s']:.1f} / {pi_row['duration_s']:.1f} | "
        f"{'Y' if w_row['passed'] else 'N'} / {'Y' if pi_row['passed'] else 'N'} |"
    )
