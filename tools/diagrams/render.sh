#!/bin/sh
# Renders every .mmd in this directory to docs/images/diagram-<name>.png.
# Run inside a Playwright image (which ships a working Chromium):
#   docker run --rm -v "$PWD/tools/diagrams:/data" -v "$PWD/docs/images:/out" \
#     -e PUPPETEER_EXECUTABLE_PATH=/ms-playwright/chromium-1148/chrome-linux/chrome \
#     mcr.microsoft.com/playwright:v1.49.0-noble sh /data/render.sh
set -e
npm i -g -s @mermaid-js/mermaid-cli@11.4.2 >/dev/null 2>&1
for f in topology moq-join experiment-loop emulator; do
  mmdc -p /data/puppeteer.json -i "/data/$f.mmd" -o "/out/diagram-$f.png" -b white -w 2200 -s 2
  echo "rendered $f"
done
