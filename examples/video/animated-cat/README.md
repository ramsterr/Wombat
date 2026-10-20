# Animated Cat (Pillow + ffmpeg)

A simple 4-second looping cat animation generated with free tools: Python + Pillow draws frames, ffmpeg encodes MP4. No diffusion models, no heavy deps.

- `generate.py` — renders 96 frames (24 fps) of a cat with wagging tail, blinking eyes, mouth animation, whiskers.
- `animated-cat.mp4` — output video.

Run:
```sh
python3 generate.py
ffmpeg -y -framerate 24 -i frames/frame_%04d.png -c:v libx264 -pix_fmt yuv420p animated-cat.mp4
```

## Provenance graph (OTel-style)

`otel_graph.png` — trace of tools/methods/sites/files used to produce the video:

- User prompt → Wombat agent → Bash tool → filesystem
- Python 3.13 → Pillow 12.3.0 (frame rendering) → ffmpeg 8.1/libx264 (encode)
- Reference: examples/design/cat/cat.svg
- Output: examples/video/animated-cat/animated-cat.mp4
- Source repo: https://github.com/ramsterr/Wombat
