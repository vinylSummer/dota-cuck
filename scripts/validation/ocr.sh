#!/usr/bin/env bash
# OCR a validation screenshot to plain text — replaces eyeballing shots with the vision model.
#
# tesseract + imagemagick live INSIDE the dota-steam image (host has neither), and the shots
# live on the shared /fard/steam volume the container mounts, so we just `docker exec` the
# pipeline in the running container. Light preprocessing (grayscale + 2x upscale + threshold)
# makes Dota's Panorama UI text far more legible to tesseract; --psm 11 reads sparse, scattered
# UI text (titles, button labels, modal copy) rather than assuming a uniform block.
#
# Usage:
#   scripts/validation/ocr.sh <shot-name>        # resolves $SHOTDIR/<shot-name>(.png)
#   scripts/validation/ocr.sh 00-after-launch
#   scripts/validation/ocr.sh /abs/path/on/shared/volume.png
#   PSM=6 scripts/validation/ocr.sh <shot>       # override page-segmentation mode
#   OCR_CROP=WxH+X+Y scripts/validation/ocr.sh <shot>   # OCR only a region (orig-image px)
#   OCR_TSV=1 scripts/validation/ocr.sh <shot>   # emit tesseract TSV word boxes instead of text
#
# OCR_TSV / OCR_CROP exist so gui_spectate.py's state machine can locate clickable word boxes.
# The pipeline upscales 200% before OCR, so TSV left/top/width/height are in the cropped, 2x
# image space: the caller maps a box back to a screen pixel as  region_x + left/2 (+ width/4 for
# the center), region_y + top/2 (+ height/4).  ocr.sh stays a thin host-side debug wrapper;
# gui_spectate.py does the same convert|tesseract in-process (it runs inside the container).
set -euo pipefail
cd "$(dirname "$0")/../.."
[ -f ~/.dota-validation.env ] && { set -a; . ~/.dota-validation.env; set +a; }

STEAMHOME=${STEAM_HOME_DIR:-/fard/steam/steamhome}
SHOTDIR="$STEAMHOME/v5-shots"
NAME=${NAME:-dota-v5}
PSM=${PSM:-11}
OCR_TSV=${OCR_TSV:-0}
OCR_CROP=${OCR_CROP:-}

arg=${1:?usage: ocr.sh <shot-name|abs-path>}

# Resolve the image path AS SEEN INSIDE THE CONTAINER (same /fard/steam mount on host + container).
case "$arg" in
    /*) img="$arg" ;;                                   # absolute path (shared volume)
    *)  img="$SHOTDIR/$arg"; [ -f "$img" ] || img="$SHOTDIR/$arg.png" ;;
esac

# Verify the file exists from the host's view (host sees the same /fard/steam path).
if [ ! -f "$img" ]; then
    echo "ocr.sh: no such screenshot: $img" >&2
    echo "  available in $SHOTDIR:" >&2
    ls -1 "$SHOTDIR" 2>/dev/null | sed 's/^/    /' >&2 || true
    exit 1
fi

# Crop (in original-image px) BEFORE the grayscale/upscale so a region-scoped OCR ignores the
# rest of the frame; +repage resets the virtual canvas so downstream geometry starts at 0,0.
crop_op=""
[ -n "$OCR_CROP" ] && crop_op="-crop $OCR_CROP +repage"
# TSV mode emits word boxes (+ confidence) for box-location; text mode emits plain text.
tess_out="stdout"; [ "$OCR_TSV" = 1 ] && tess_out="stdout tsv"

docker exec "$NAME" bash -lc "
    convert '$img' $crop_op -colorspace Gray -resize 200% -threshold 50% png:- \
        | tesseract stdin $tess_out --psm $PSM 2>/dev/null
"
