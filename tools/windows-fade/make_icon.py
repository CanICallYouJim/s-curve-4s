"""Render the application's native icon (Pillow is needed only to regenerate it)."""
from pathlib import Path
import math
from PIL import Image, ImageDraw

size = 1024
image = Image.new('RGBA', (size, size))
pixels = image.load()
for y in range(size):
    for x in range(size):
        t = (x + y) / (size * 2)
        pixels[x, y] = (int(17 + 8*t), int(36 + 21*t), int(51 + 24*t), 255)
mask = Image.new('L', (size, size))
ImageDraw.Draw(mask).rounded_rectangle((10, 10, size-10, size-10), radius=220, fill=255)
image.putalpha(mask)
draw = ImageDraw.Draw(image)
# An ascending half-cosine curve, matching the actual audio envelope.
points = []
for i in range(401):
    t = i/400
    points.append((160 + 660*t, 725 - 450*(.5-.5*math.cos(math.pi*t))))
upper, lower = [], []
for i, (x, y) in enumerate(points):
    slope = -450 * math.pi * math.sin(math.pi*i/400) / (2*660)
    length = math.sqrt(1+slope*slope)
    nx, ny = -slope*31/length, 31/length
    upper.append((x+nx,y+ny))
    lower.append((x-nx,y-ny))
draw.polygon(upper+list(reversed(lower)), fill='#74f4cf')
for x, y in (points[0], points[-1]):
    draw.ellipse((x-31, y-31, x+31, y+31), fill='#74f4cf')
# Small equalizer bars identify the audio domain even at desktop icon sizes.
for x, height in [(210, 80), (285, 150), (360, 110)]:
    draw.rounded_rectangle((x, 218, x+32, 218+height), radius=16, fill='#eefafa')
root = Path(__file__).resolve().parent
image.resize((256,256), Image.Resampling.LANCZOS).save(root/'icon.png')
image.save(root/'icon.ico', sizes=[(16,16),(24,24),(32,32),(48,48),(64,64),(128,128),(256,256)])
