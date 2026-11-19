#!/usr/bin/env python3
"""Assemble les images d'un tournage (tournage.mjs) en MP4 H.264 avec légendes incrustées.

    python3 assembler.py /tmp/hypha-video/vue-ensemble-fr /tmp/hypha-video/sortie/vue-ensemble-fr

Sorties : <nom>.mp4 (1280 × 720, 25 i/s, faststart), <nom>.jpg (affiche) et <nom>.vtt (légendes).
Les silences du screencast (page immobile) deviennent des images tenues : durée réelle conservée.
"""
import json
import os
import subprocess
import sys

FONT = "/usr/share/fonts/truetype/noto/NotoSans-Regular.ttf"
CAPTION_LAG = 1.0


def vtt_time(s):
    h, rest = divmod(s, 3600)
    m, sec = divmod(rest, 60)
    return f"{int(h):02d}:{int(m):02d}:{sec:06.3f}"


def remap(speeds):
    """Temps réel → temps de la vidéo, avec les passages accélérés ({t, factor}) de tournage.mjs."""
    marks = sorted(speeds, key=lambda s: s["t"])

    def f(t):
        out, prev, factor = 0.0, 0.0, 1.0
        for m in marks:
            if m["t"] >= t:
                break
            out += (m["t"] - prev) / factor
            prev, factor = m["t"], m["factor"]
        return out + (t - prev) / factor
    return f


def webm(src, dest):
    """Copie VP9 de la vidéo : les navigateurs sans décodeur H.264 (Chromium libre) la lisent à la place."""
    subprocess.run(["nice", "-n", "15", "ffmpeg", "-y", "-loglevel", "error", "-i", src, "-c:v", "libvpx-vp9", "-b:v", "0",
                    "-crf", "36", "-deadline", "good", "-cpu-used", "4", "-row-mt", "1", "-threads", "4", "-an", dest],
                   check=True)


def main(src, dest):
    data = json.load(open(os.path.join(src, "frames.json")))
    f = remap(data.get("speeds") or [])
    for fr in data["frames"]:
        fr["t"] = f(fr["t"])
    # Sous SwiftShader, l'image d'un écran arrive environ une seconde après le geste qui l'a produit :
    # les légendes (posées à l'heure du geste) sont décalées d'autant.
    for c in data["captions"]:
        c["start"], c["end"] = f(c["start"]) + CAPTION_LAG, f(c["start"]) + CAPTION_LAG + (c["end"] - c["start"])
    data["end"] = f(data["end"])
    frames, end = data["frames"], data["end"]
    if not frames:
        raise SystemExit("aucune image")
    lst = os.path.join(src, "liste.txt")
    with open(lst, "w") as f:
        for i, fr in enumerate(frames):
            nxt = frames[i + 1]["t"] if i + 1 < len(frames) else max(end, fr["t"] + 0.5)
            f.write(f"file '{fr['f']}'\nduration {max(0.04, nxt - fr['t']):.3f}\n")
        f.write(f"file '{frames[-1]['f']}'\n")
    # Images JPEG du screencast en plage complète : ramenées à la plage vidéo (tv), que tous les lecteurs lisent.
    filters = ["scale=1280:720:flags=lanczos:out_range=tv,fps=25,format=yuv420p"]
    for i, c in enumerate(data["captions"]):
        tf = os.path.join(src, f"legende{i}.txt")
        with open(tf, "w") as f:
            f.write(c["text"])
        filters.append(
            f"drawtext=fontfile={FONT}:textfile={tf}:fontsize=28:fontcolor=white:line_spacing=6"
            f":box=1:boxcolor=0x101010@0.82:boxborderw=16:x=(w-text_w)/2:y=h-text_h-46"
            f":enable='between(t,{c['start']:.2f},{c['end']:.2f})'")
    os.makedirs(os.path.dirname(dest), exist_ok=True)
    subprocess.run(["nice", "-n", "15", "ffmpeg", "-y", "-loglevel", "error", "-threads", "4", "-f", "concat", "-safe", "0",
                    "-i", lst, "-vf", ",".join(filters), "-c:v", "libx264", "-preset", "slow", "-crf", "27",
                    "-tune", "stillimage", "-g", "125", "-color_range", "tv", "-movflags", "+faststart", "-an",
                    dest + ".mp4"], check=True)
    webm(dest + ".mp4", dest + ".webm")
    poster = min(2.5, end / 2)
    subprocess.run(["ffmpeg", "-y", "-loglevel", "error", "-ss", f"{poster:.2f}", "-i", dest + ".mp4", "-frames:v", "1",
                    "-q:v", "4", dest + ".jpg"], check=True)
    with open(dest + ".vtt", "w") as f:
        f.write("WEBVTT\n\n")
        for i, c in enumerate(data["captions"], 1):
            f.write(f"{i}\n{vtt_time(c['start'])} --> {vtt_time(c['end'])}\n{c['text']}\n\n")
    size = os.path.getsize(dest + ".mp4")
    print(f"{os.path.basename(dest)} : {end:.1f} s, {size / 1e6:.2f} Mo")


if __name__ == "__main__":
    main(sys.argv[1], sys.argv[2])
