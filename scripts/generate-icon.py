#!/usr/bin/env python3
"""Deterministic Windows ICO rendering of assets/netgo.svg (no downloaded art)."""
from pathlib import Path
from PIL import Image, ImageDraw

root = Path(__file__).resolve().parents[1]
scale = 4
image = Image.new('RGBA', (256*scale, 256*scale))
draw = ImageDraw.Draw(image)
def points(values): return [(x*scale,y*scale) for x,y in values]
def line(values, color, width):
    draw.line(points(values),fill=color,width=width*scale,joint='curve')
    for x,y in values:
        r=width/2; draw.ellipse(((x-r)*scale,(y-r)*scale,(x+r)*scale,(y+r)*scale),fill=color)
draw.rounded_rectangle((8*scale,8*scale,248*scale,248*scale),radius=56*scale,fill='#10384b')
line([(66,180),(66,76),(160,180),(160,76)],'#f5fcff',22)
line([(160,76),(200,76)],'#36d9c5',17)
line([(182,58),(200,76),(182,94)],'#36d9c5',17)
draw.ellipse((52*scale,166*scale,80*scale,194*scale),fill='#36d9c5')
image=image.resize((256,256),Image.Resampling.LANCZOS)
image.save(root/'assets/netgo.ico',sizes=[(s,s) for s in (16,20,24,32,40,48,64,128,256)])
print('Created assets/netgo.ico: 9 sizes, transparent background')
