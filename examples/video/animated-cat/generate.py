import math, os
from PIL import Image, ImageDraw

W, H = 320, 240
OUT = os.path.dirname(os.path.abspath(__file__))
FRAMES = os.path.join(OUT, "frames")
os.makedirs(FRAMES, exist_ok=True)

N = 96
FPS = 24

def draw_cat(draw, t):
    # background
    draw.rectangle((0, 0, W, H), fill=(232, 240, 248))
    # ground
    draw.rectangle((0, H-30, W, H), fill=(200, 220, 180))
    # tail wag behind body
    cx, cy = 110, 150
    phase = t * 2 * math.pi
    tail_pts = []
    for i in range(8):
        a = i / 7.0
        x = cx - 30 - a*40
        y = cy + 20 + math.sin(phase + a*3) * 18 * (1-a)
        tail_pts.append((x, y))
    for i in range(len(tail_pts)-1):
        draw.line((tail_pts[i], tail_pts[i+1]), fill=(230, 140, 70), width=12)
    # body
    draw.ellipse((70, 120, 170, 210), fill=(244, 162, 97))
    # head
    hx, hy = 150, 105
    draw.ellipse((hx-55, hy-50, hx+55, hy+50), fill=(244, 162, 97))
    # ears
    ear_wiggle = math.sin(phase*1.3) * 3
    for side, sx in ((-1, -1), (1, 1)):
        pts = [(hx+side*28, hy-40), (hx+side*48, hy-78+ear_wiggle*side), (hx+side*6, hy-55)]
        draw.polygon(pts, fill=(244, 162, 97))
        inner = [(hx+side*28, hy-42), (hx+side*42, hy-68+ear_wiggle*side), (hx+side*10, hy-52)]
        draw.polygon(inner, fill=(231, 111, 81))
    # eyes (blink)
    blink = 0.0
    # blink every ~1.8s for 0.15s
    bt = t * (N/FPS)  # seconds-ish using t in [0,1]
    # simpler: blink when fractional part near 0
    sec = t * 4.0
    frac = sec - math.floor(sec)
    if frac > 0.92:
        blink = (frac-0.92)/0.08
        if blink > 1: blink = 2-blink
    eye_h = 18 * (1 - 0.85*blink)
    for ex in (hx-20, hx+20):
        draw.ellipse((ex-12, hy-8-eye_h/2, ex+12, hy-8+eye_h/2), fill=(43, 43, 43))
        if blink < 0.5:
            draw.ellipse((ex-3, hy-12, ex+3, hy-6), fill=(255, 255, 255))
    # nose
    draw.polygon([(hx, hy+6), (hx-6, hy+14), (hx+6, hy+14)], fill=(231, 111, 81))
    # mouth
    mouth_open = 0.5 + 0.5*math.sin(phase*0.7)
    draw.arc((hx-16, hy+12, hx+16, hy+28), 0, 180, fill=(43,43,43), width=2)
    if mouth_open > 0.7:
        draw.ellipse((hx-6, hy+16, hx+6, hy+24), fill=(43,43,43))
    # whiskers
    for side in (-1, 1):
        for j, yy in enumerate((hy+2, hy+8)):
            wig = math.sin(phase*2 + j) * 2
            draw.line(((hx+side*18, hy+4+j*6), (hx+side*60, yy+wig*side)), fill=(43,43,43), width=2)
    # paws
    for px in (95, 135):
        draw.ellipse((px, 195, px+22, 215), fill=(244, 162, 97))
    # text
    draw.text((10, 10), "Wombat cat", fill=(60, 80, 120))

for i in range(N):
    t = i / (N-1)
    img = Image.new("RGB", (W, H), (232, 240, 248))
    draw = ImageDraw.Draw(img)
    draw_cat(draw, t)
    img.save(os.path.join(FRAMES, f"frame_{i:04d}.png"))

print("frames:", N)
