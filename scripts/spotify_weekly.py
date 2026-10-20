#!/usr/bin/env python3
"""
Spotify Weekly Top Artists — data pipeline + report + SVG chart.

Source of truth (no API key required):
  * kworb.net  -> Spotify GLOBAL weekly chart (top tracks + artists + Spotify IDs + stream counts)
  * MusicBrainz -> artist genre/tag data (no API key, polite User-Agent)

Outputs (into ./spotify-weekly):
  * data.json          raw fetched + parsed dataset
  * README.md          top-artists table with direct Spotify song links + genre
  * chart.svg          timeline (week ending) vs global streams, one line per top track,
                       colored by genre, with genre legend

Run:  python3 scripts/spotify_weekly.py
"""
import json
import re
import sys
import ssl
import subprocess
import urllib.request
import urllib.parse
import urllib.error
import datetime
import html
import os
import time

HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.abspath(os.path.join(HERE, "..", "spotify-weekly"))
os.makedirs(OUT, exist_ok=True)
CACHE = os.path.join(OUT, "genre_cache.json")


def load_cache():
    try:
        return json.load(open(CACHE))
    except Exception:
        return {}


def save_cache(cache):
    try:
        json.dump(cache, open(CACHE, "w"), indent=2)
    except Exception:
        pass

UA = "wombat-research/1.0 (contact: ramster@example.com)"

# ---- genre palette -------------------------------------------------------
GENRE_COLORS = {
    "reggaeton": "#39FF14",
    "latin": "#FFD500",
    "pop": "#FF4FD8",
    "hip hop": "#00E0FF",
    "rap": "#7C5CFF",
    "k-pop": "#FF7A00",
    "rock": "#FF3B3B",
    "r&b": "#C77DFF",
    "edm": "#00FFC8",
    "afrobeats": "#FF9E00",
    "country": "#B5651D",
    "indie": "#9ACD32",
    "other": "#9AA0A6",
}

def genre_color(g):
    g = (g or "").lower()
    if not g:
        return GENRE_COLORS["other"]
    for key, c in GENRE_COLORS.items():
        if key in g:
            return c
    return GENRE_COLORS["other"]

# ---- helpers -------------------------------------------------------------
def fetch(url):
    # Use curl via subprocess: it uses the system CA bundle even when Python's
    # own cert store is missing (common on some macOS installs).
    try:
        out = subprocess.run(
            ["curl", "-4", "-sL", "-A", UA, "--max-time", "30", url],
            capture_output=True, timeout=60,
        )
        if out.returncode == 0 and out.stdout:
            return out.stdout.decode("utf-8", "ignore")
    except Exception:
        pass
    # Fallback to urllib (will work where CA certs are present).
    req = urllib.request.Request(url, headers={"User-Agent": UA})
    ctx = ssl.create_default_context()
    ctx.check_hostname = False
    ctx.verify_mode = ssl.CERT_NONE
    with urllib.request.urlopen(req, timeout=30, context=ctx) as r:
        return r.read().decode("utf-8", "ignore")

def mb_lookup(artist_name):
    """Return (genre_string, tag_list) for an artist via MusicBrainz.

    MusicBrainz is rate-limited (~1 req/s), so we throttle and retry once.
    """
    q = urllib.parse.urlencode({"query": f'artist:"{artist_name}"', "fmt": "json"})
    for attempt in range(3):
        try:
            raw = fetch("https://musicbrainz.org/ws/2/artist/?" + q)
            d = json.loads(raw)
            break
        except Exception:
            time.sleep(1.5 * (attempt + 1))
            d = {}
    if not d:
        return "", []
    for a in d.get("artists", []):
        if a.get("name", "").lower() == artist_name.lower():
            genres = [g["name"] for g in a.get("genres", [])]
            tags = [t["name"] for t in a.get("tags", [])]
            if genres or tags:
                return (genres[0] if genres else tags[0]), (genres + tags)
    if d.get("artists"):
        a = d["artists"][0]
        genres = [g["name"] for g in a.get("genres", [])]
        tags = [t["name"] for t in a.get("tags", [])]
        return (genres[0] if genres else (tags[0] if tags else "")), (genres + tags)
    return "", []

def classify(genre_str, all_tags):
    """Map raw MusicBrainz tag to one of our canonical genre buckets."""
    blob = " ".join([genre_str] + all_tags).lower()
    order = ["k-pop", "reggaeton", "afrobeats", "latin", "country", "indie",
             "r&b", "rnb", "rock", "edm", "hip hop", "rap", "pop"]
    for key in order:
        if key in blob:
            return "r&b" if key == "rnb" else key
    return "other"

# ---- 1. fetch weekly chart ----------------------------------------------
def fetch_weekly():
    url = "https://kworb.net/spotify/country/global_weekly.html"
    h = fetch(url)
    m = re.search(r"Weekly Chart - Global - (\d{4}/\d{2}/\d{2})", h)
    week_ending = m.group(1) if m else "unknown"
    table = re.search(r"<table[^>]*>.*?</table>", h, re.S).group(0)
    rows = re.findall(r"<tr[^>]*>(.*?)</tr>", table, re.S)
    tracks = []
    for r in rows[1:]:
        cells = re.findall(r"<t[hd][^>]*>(.*?)</t[hd]>", r, re.S)
        if len(cells) < 9:
            continue
        pos = cells[0].strip()
        at = cells[2]
        streams = int(cells[6].replace(",", ""))
        # artist + track + links
        am = re.search(r'href="\.\./artist/([0-9A-Za-z]+)\.html">([^<]+)</a>\s*-\s*<a href="\.\./track/([0-9A-Za-z]+)\.html">([^<]+)</a>', at)
        if not am:
            continue
        artist_id, artist, track_id, track = am.groups()
        tracks.append({
            "pos": int(pos),
            "artist": html.unescape(artist).strip(),
            "artist_spotify_id": artist_id,
            "track": html.unescape(track).strip(),
            "track_spotify_id": track_id,
            "streams": streams,
        })
    return week_ending, tracks

# ---- 2. fetch 8-week timeline for top tracks -----------------------------
def fetch_timeline(track_id):
    """Return list of (date_str, global_streams) from the WEEKLY chart-history block.

    The weekly block is the first <table> on the track page. Its first column is
    the week-ending date (YYYY/MM/DD) and the second column is the Global position
    with the global stream count in parentheses:  `1 (35,671,213)`.
    """
    try:
        h = fetch(f"https://kworb.net/spotify/track/{track_id}.html")
    except Exception:
        return []
    tables = re.findall(r"<table[^>]*>.*?</table>", h, re.S)
    if not tables:
        return []
    week = tables[0]
    pts = []
    for row in re.findall(r"<tr[^>]*>(.*?)</tr>", week, re.S):
        cells = re.findall(r"<t[dh][^>]*>(.*?)</t[dh]>", row, re.S)
        if len(cells) < 2:
            continue
        d = re.sub(r"<[^>]+>", "", cells[0]).strip()
        g = re.sub(r"<[^>]+>", "", cells[1]).strip()
        if not re.match(r"\d{4}/\d{2}/\d{2}$", d):
            continue
        m = re.search(r"\(([\d,]+)\)", g)
        if not m:
            continue
        pts.append((d, int(m.group(1).replace(",", ""))))
    seen = {}
    for d, s in pts:
        seen[d] = s
    return sorted(seen.items())

# ---- 3. assemble ---------------------------------------------------------
def main():
    week_ending, tracks = fetch_weekly()
    print(f"Weekly chart (global) ending {week_ending}: {len(tracks)} tracks")

    # take top N by streams for the timeline chart
    top = sorted(tracks, key=lambda t: t["streams"], reverse=True)[:8]

    # enrich ALL artists with genre (the task is specifically about measuring
    # genre). Use a persistent cache so re-runs are instant and we only hit
    # MusicBrainz for new artists, throttled to respect their ~1 req/s limit.
    cache = load_cache()
    seen_artists = {}
    looked_up = 0
    for t in tracks:
        a = t["artist"]
        if a in cache:
            seen_artists[a] = tuple(cache[a])
            continue
        g, tags = mb_lookup(a)
        seen = (classify(g, tags), g, tags)
        cache[a] = list(seen)
        seen_artists[a] = seen
        looked_up += 1
        print(f"  genre {a}: {seen}")
        time.sleep(1.0)  # be polite to MusicBrainz
    save_cache(cache)
    print(f"Genre lookups this run: {looked_up} (cached: {len(cache) - looked_up})")

    # attach genre + spotify links to every track
    for t in tracks:
        g, raw, tags = seen_artists.get(t["artist"], ("other", "", []))
        t["genre"], t["genre_raw"], t["tags"] = g, raw, tags
        t["spotify_track_url"] = f"https://open.spotify.com/track/{t['track_spotify_id']}"
        t["spotify_artist_url"] = f"https://open.spotify.com/artist/{t['artist_spotify_id']}"

    # timeline for top tracks
    for t in top:
        t["timeline"] = fetch_timeline(t["track_spotify_id"])
        print(f"  timeline {t['track']}: {len(t['timeline'])} weeks")

    data = {
        "week_ending": week_ending,
        "generated": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "tracks": tracks,
        "top_for_chart": [t["track_spotify_id"] for t in top],
    }
    with open(os.path.join(OUT, "data.json"), "w") as f:
        json.dump(data, f, indent=2)

    write_report(tracks, week_ending)
    write_svg(top, week_ending)
    print("Wrote report + chart to", OUT)

# ---- 4. markdown report --------------------------------------------------
def write_report(tracks, week_ending):
    lines = []
    lines.append(f"# Spotify — Top Artists This Week (Global)")
    lines.append("")
    lines.append(f"**Weekly chart ending:** {week_ending}  ")
    lines.append(f"**Generated:** {datetime.date.today().isoformat()}  ")
    lines.append(f"**Source:** [kworb.net Spotify Global Weekly](https://kworb.net/spotify/country/global_weekly.html) · genre via [MusicBrainz](https://musicbrainz.org)")
    lines.append("")
    lines.append("| # | Artist | Top Song (this week) | Genre | Weekly Streams | Spotify song |")
    lines.append("|---|--------|----------------------|-------|---------------:|--------------|")
    for t in tracks:
        link = f"[▶ open]({t['spotify_track_url']})"
        lines.append(
            f"| {t['pos']} | [{t['artist']}]({t['spotify_artist_url']}) "
            f"| {t['track']} | {t['genre']} | {t['streams']:,} | {link} |"
        )
    lines.append("")
    lines.append("> Weekly streams = Spotify global streams for that track in the chart week. "
                 "Genre is derived from MusicBrainz artist tags and bucketed into the chart's genre palette.")
    lines.append("")
    lines.append("See [`chart.svg`](chart.svg) for the **timeline (week-ending) vs global weekly streams** chart, "
                 "one line per top track, colored by genre.")
    with open(os.path.join(OUT, "README.md"), "w") as f:
        f.write("\n".join(lines) + "\n")

# ---- 5. SVG chart --------------------------------------------------------
def write_svg(top, week_ending):
    W, H = 1200, 720
    ml, mr, mt, mb = 70, 220, 60, 60
    plot_w = W - ml - mr
    plot_h = H - mt - mb

    # x axis = weeks present across all timelines
    all_dates = sorted({d for t in top for d, _ in t["timeline"]})
    if not all_dates:
        all_dates = [week_ending]
    # y max
    ymax = max((s for t in top for _, s in t["timeline"]), default=1)
    ymax = max(ymax, 1)

    def x(i):
        if len(all_dates) == 1:
            return ml + plot_w / 2
        return ml + (i / (len(all_dates) - 1)) * plot_w

    def y(v):
        return mt + plot_h - (v / ymax) * plot_h

    parts = []
    parts.append(f'<svg xmlns="http://www.w3.org/2000/svg" width="{W}" height="{H}" '
                 f'font-family="Helvetica, Arial, sans-serif">')
    parts.append(f'<rect width="{W}" height="{H}" fill="#0d1117"/>')
    parts.append(f'<text x="{ml}" y="34" fill="#f0f6fc" font-size="22" font-weight="bold">'
                 f'Spotify Global — Top Tracks: Timeline vs Weekly Streams</text>')
    parts.append(f'<text x="{ml}" y="54" fill="#8b949e" font-size="13">'
                 f'Week ending {week_ending} · one line per top track · colored by genre</text>')

    # grid + y labels
    for g in range(0, 6):
        val = ymax * g / 5
        yy = y(val)
        parts.append(f'<line x1="{ml}" y1="{yy:.1f}" x2="{ml+plot_w}" y2="{yy:.1f}" stroke="#21262d"/>')
        parts.append(f'<text x="{ml-10}" y="{yy+4:.1f}" fill="#8b949e" font-size="11" text-anchor="end">'
                     f'{val/1e6:.1f}M</text>')
    # x labels (every other to avoid crowding)
    step = max(1, (len(all_dates) + 1) // 8)
    for i, d in enumerate(all_dates):
        if i % step == 0:
            parts.append(f'<text x="{x(i):.1f}" y="{mt+plot_h+22}" fill="#8b949e" font-size="11" '
                         f'text-anchor="middle">{d[5:]}</text>')
    parts.append(f'<text x="{ml+plot_w/2:.0f}" y="{H-12}" fill="#8b949e" font-size="12" '
                 f'text-anchor="middle">week ending (MM/DD)</text>')

    # lines
    for t in top:
        color = genre_color(t["genre"])
        pts = [(all_dates.index(d), s) for d, s in t["timeline"] if d in all_dates]
        if not pts:
            continue
        d_str = " ".join(f"{x(i):.1f},{y(s):.1f}" for i, s in pts)
        parts.append(f'<polyline fill="none" stroke="{color}" stroke-width="2.5" '
                     f'stroke-linejoin="round" points="{d_str}"/>')
        # end dot + label anchor
        li, ls = pts[-1]
        lx, ly = x(li), y(ls)
        parts.append(f'<circle cx="{lx:.1f}" cy="{ly:.1f}" r="3.5" fill="{color}"/>')
        t["_lx"] = lx
        t["_ly"] = ly
        t["_color"] = color

    # legend (right side), grouped by genre
    genres_order = []
    for t in top:
        if t["genre"] not in genres_order:
            genres_order.append(t["genre"])
    ly0 = mt + 10
    parts.append(f'<text x="{ml+plot_w+20}" y="{ly0-14}" fill="#f0f6fc" font-size="13" '
                 f'font-weight="bold">Legend (genre)</text>')
    for i, g in enumerate(genres_order):
        yy = ly0 + i * 26
        parts.append(f'<rect x="{ml+plot_w+20}" y="{yy-12}" width="14" height="14" rx="3" '
                     f'fill="{genre_color(g)}"/>')
        parts.append(f'<text x="{ml+plot_w+40}" y="{yy-1}" fill="#c9d1d9" font-size="13">{g}</text>')

    # track labels at line ends (right of plot)
    for t in top:
        if "_lx" not in t:
            continue
        lx, ly = t["_lx"], t["_ly"]
        parts.append(f'<text x="{lx+8:.1f}" y="{ly+4:.1f}" fill="{t["_color"]}" font-size="11">'
                     f'{html.escape(t["artist"])} – {html.escape(t["track"])[:22]}</text>')

    parts.append('</svg>')
    with open(os.path.join(OUT, "chart.svg"), "w") as f:
        f.write("\n".join(parts) + "\n")

if __name__ == "__main__":
    main()
