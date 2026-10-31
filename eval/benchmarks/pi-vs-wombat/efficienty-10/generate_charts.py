#!/usr/bin/env python3
import json
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
RED = "#DC2626"

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
ordinary = [task for task in tasks if task != "research_bounded"]


def metric(agent, key, subset=None):
    subset = subset or tasks
    return sum(by_task[task][agent][key] for task in subset)


def cached(agent, subset=None):
    subset = subset or tasks
    return sum(by_task[task][agent].get("cache_read", 0) + by_task[task][agent].get("cache_write", 0) for task in subset)


def uncached(agent, subset=None):
    return metric(agent, "input_tokens", subset) - cached(agent, subset)


def passed(agent):
    return sum(1 for task in tasks if by_task[task][agent]["passed"])


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
ordinary_totals = {
    agent: {
        "input": metric(agent, "input_tokens", ordinary),
        "uncached": uncached(agent, ordinary),
        "output": metric(agent, "output_tokens", ordinary),
        "tools": metric(agent, "tool_calls", ordinary),
        "steps": metric(agent, "model_steps", ordinary),
        "time": metric(agent, "duration_s", ordinary),
    }
    for agent in AGENTS
}

labels = [f"{AGENTS[a]}\n{totals[a]['passed']}/10" for a in ("wombat", "pi")]
x = np.arange(2)

fig, axes = plt.subplots(2, 2, figsize=(12, 8.8))
fig.suptitle("efficienty Wombat vs Pi — 10-task harness eval", x=0.02, y=0.98, ha="left", fontsize=17, fontweight="bold")
fig.text(0.02, 0.935, "Model: deepseek-v4.1-flash · 4 simple + 6 complex tasks · same instructions, skill, MCP, Playwright", color=MUTED, fontsize=10)

ax = axes[0, 0]
w_cached, pi_cached = totals["wombat"]["cached"], totals["pi"]["cached"]
w_uncached, pi_uncached = totals["wombat"]["uncached"], totals["pi"]["uncached"]
ax.bar(x, [w_cached, pi_cached], width=0.55, color=[W_LIGHT, PI_LIGHT], edgecolor="white", label="Cached")
ax.bar(x, [w_uncached, pi_uncached], bottom=[w_cached, pi_cached], width=0.55, color=[W, PI], edgecolor="white", label="Uncached")
for i, agent in enumerate(("wombat", "pi")):
    total = totals[agent]["input"]
    ax.text(i, total * 1.015, fmt_int(total), ha="center", va="bottom", fontsize=9.5, fontweight="bold")
    ax.text(i, totals[agent]["cached"] / 2, fmt_int(totals[agent]["cached"]), ha="center", va="center", fontsize=8.5, color=INK)
    mid = totals[agent]["cached"] + totals[agent]["uncached"] / 2
    ax.text(i, mid, fmt_int(totals[agent]["uncached"]), ha="center", va="center", fontsize=8.5, color="white")
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

fig.text(0.02, 0.015, "Pi's total is dominated by the research task: 612k input, 36 tools, 300s timeout, no answer.md.", color=MUTED, fontsize=8.5)
fig.tight_layout(rect=[0, 0.03, 1, 0.90])
fig.savefig(ASSETS / "summary.svg", bbox_inches="tight")
plt.close(fig)


def per_task_chart(metric_key, title, filename, value_formatter, note, subset):
    order = sorted(subset, key=lambda task: max(by_task[task]["wombat"][metric_key], by_task[task]["pi"][metric_key]), reverse=True)
    fig, ax = plt.subplots(figsize=(10.8, 7.4))
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
    "Wall time: 9 ordinary tasks",
    "per-task-time.svg",
    lambda value: f"{value:.0f}s",
    "Research task shown separately",
    ordinary,
)
per_task_chart(
    "input_tokens",
    "Prompt tokens: 9 ordinary tasks",
    "per-task-input.svg",
    fmt_k,
    "Research task shown separately",
    ordinary,
)

fig, ax = plt.subplots(figsize=(12, 5.8))
w_cum = np.cumsum([by_task[task]["wombat"]["duration_s"] for task in tasks])
pi_cum = np.cumsum([by_task[task]["pi"]["duration_s"] for task in tasks])
ax.step(np.arange(len(tasks)), w_cum, where="mid", color=W, linewidth=2.5, marker="o", markersize=4, label="Wombat")
ax.step(np.arange(len(tasks)), pi_cum, where="mid", color=PI, linewidth=2.5, marker="o", markersize=4, label="Pi")
ax.text(len(tasks) - 0.85, w_cum[-1], f"{w_cum[-1]:.0f}s", color=W, fontsize=10, fontweight="bold", va="center")
ax.text(len(tasks) - 0.85, pi_cum[-1], f"{pi_cum[-1]:.0f}s", color=PI, fontsize=10, fontweight="bold", va="center")
ax.set_xticks(np.arange(len(tasks)), [task.replace("_", " ") for task in tasks], rotation=40, ha="right")
ax.set_xlim(-0.3, len(tasks) - 0.2)
ax.set_ylim(0, max(pi_cum[-1], w_cum[-1]) * 1.10)
ax.set_ylabel("cumulative seconds")
ax.set_title("Cumulative wall time across all 10 tasks", loc="left")
ax.legend(loc="upper left")
finish(ax)
fig.tight_layout()
fig.savefig(ASSETS / "cumulative-time.svg", bbox_inches="tight")
plt.close(fig)

research = by_task["research_bounded"]
fig, axes = plt.subplots(2, 2, figsize=(11, 7.4))
fig.suptitle("Research task: Wombat finishes, Pi times out", x=0.02, y=0.98, ha="left", fontsize=16, fontweight="bold")
fig.text(0.02, 0.935, "Same prompt: find 5 repos, remote/India-friendly, write answer.md. Pi made 35 bash calls and never wrote the file.", color=MUTED, fontsize=9.5)
panels = [
    ("Input tokens", "input_tokens", fmt_int),
    ("Output tokens", "output_tokens", fmt_int),
    ("Tool calls", "tool_calls", lambda value: f"{value:.0f}"),
    ("Wall time", "duration_s", lambda value: f"{value:.0f}s"),
]
for ax, (title, key, formatter) in zip(axes.flat, panels):
    values = [research[a][key] for a in ("wombat", "pi")]
    bars = ax.bar(np.arange(2), values, width=0.55, color=[W, PI])
    if key == "duration_s":
        bars[1].set_edgecolor(RED)
        bars[1].set_linewidth(2)
    for i, value in enumerate(values):
        label = formatter(value)
        if key == "duration_s" and i == 1:
            label = "300s (timeout)"
        ax.text(i, value * 1.015 if value else 1, label, ha="center", va="bottom", fontsize=9.5, fontweight="bold")
    ax.set_xticks(np.arange(2), ["Wombat\npass", "Pi\nfail"])
    ax.set_ylim(0, max(values) * 1.18)
    ax.set_title(title, loc="left")
    if key in ("input_tokens", "output_tokens"):
        comma_axis(ax)
    finish(ax)
fig.text(0.02, 0.015, "Wombat used 2 searches (third blocked by the budget) and wrote answer.md. Pi timed out at 300s.", color=MUTED, fontsize=8.5)
fig.tight_layout(rect=[0, 0.03, 1, 0.90])
fig.savefig(ASSETS / "research-focus.svg", bbox_inches="tight")
plt.close(fig)

print("| Metric | Wombat | Pi |")
print("|---|---:|---:|")
print(f"| Tasks passed | {totals['wombat']['passed']}/10 | {totals['pi']['passed']}/10 |")
for key, label in [
    ("input", "Input tokens"),
    ("uncached", "Uncached input"),
    ("output", "Output tokens"),
    ("tools", "Tool calls"),
    ("steps", "Model steps"),
    ("time", "Total wall time"),
]:
    w = totals["wombat"][key]
    p = totals["pi"][key]
    if key == "time":
        print(f"| {label} | {w:.1f}s | {p:.1f}s |")
    else:
        print(f"| {label} | {w:,} | {p:,} |")
print("")
print("| Task | In tokens | Out tokens | Tools | Steps | Time (s) | Pass |")
print("|---|---:|---:|---:|---:|---:|:---:|")
for task in tasks:
    w = by_task[task]["wombat"]
    p = by_task[task]["pi"]
    print(
        f"| {task} | {w['input_tokens']:,} / {p['input_tokens']:,} | "
        f"{w['output_tokens']:,} / {p['output_tokens']:,} | "
        f"{w['tool_calls']} / {p['tool_calls']} | "
        f"{w['model_steps']} / {p['model_steps']} | "
        f"{w['duration_s']:.1f} / {p['duration_s']:.1f} | "
        f"{'Y' if w['passed'] else 'N'} / {'Y' if p['passed'] else 'N'} |"
    )
print("")
print("ordinary", ordinary_totals)
