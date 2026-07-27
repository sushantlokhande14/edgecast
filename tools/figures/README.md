# Figure generation

Builds every chart in `docs/images/` from committed experiment output. No host
Python required.

```bash
docker build -t edgecast-figures:local tools/figures
docker run --rm \
  -v "$PWD/results:/results:ro" -v "$PWD/docs/images:/out" \
  -e RESULTS_DIR=/results/paper -e AB_DIR=/results/abr-ab \
  edgecast-figures:local
```

`RESULTS_DIR` selects which matrix to plot; `AB_DIR` is optional and adds the
ABR diagnosis comparison figure.

Dashboard PNGs are produced separately by Grafana's own image renderer (a
`renderer` service in the compose file), which is more reliable than driving a
generic headless browser:

```bash
curl -o docs/images/dashboard-protocol-comparison.png \
  "http://localhost:3000/render/d/edgecast-compare/x?orgId=1&from=now-40m&to=now&kiosk&width=1500&height=1500&scale=2"
```
