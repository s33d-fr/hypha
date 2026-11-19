#!/bin/bash
# Ramène des MP4 déjà assemblés à la plage vidéo « tv » et en tire la copie WebM (VP9).
# Sert quand les images du tournage ne sont plus là (assembler.py fait les deux à l'assemblage).
#   bash reencoder.sh [dossier]     (défaut : /tmp/hypha-video/sortie)
set -euo pipefail
cd "${1:-/tmp/hypha-video/sortie}"
for f in ./*.mp4; do
  b=${f%.mp4}
  nice -n 15 ffmpeg -y -loglevel error -i "$f" -vf "scale=out_range=tv,format=yuv420p" -c:v libx264 -preset slow \
    -crf 23 -tune stillimage -g 125 -color_range tv -movflags +faststart -an "$b.tv.mp4"
  mv -f "$b.tv.mp4" "$f"
  nice -n 15 ffmpeg -y -loglevel error -i "$f" -c:v libvpx-vp9 -b:v 0 -crf 36 -deadline good -cpu-used 4 -row-mt 1 \
    -threads 4 -an "$b.webm"
  echo "$b : $(du -k "$f" | cut -f1) Kio mp4, $(du -k "$b.webm" | cut -f1) Kio webm"
done
