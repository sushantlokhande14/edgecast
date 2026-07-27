// Screenshots the provisioned Grafana dashboards in kiosk mode.
// Runs in a Playwright container attached to the compose network; see
// tools/shots/README.md for the exact command.
const { chromium } = require('playwright');

const SHOTS = [
  ['edgecast-compare/edgecast-protocol-comparison', 'dashboard-protocol-comparison.png'],
  ['edgecast-relay/edgecast-moq-relay-deep-dive', 'dashboard-moq-relay.png'],
  ['edgecast-netem/edgecast-experiments-and-network-conditions', 'dashboard-experiments-network.png'],
];

(async () => {
  const browser = await chromium.launch({ args: ['--no-sandbox'] });
  const page = await browser.newPage({ viewport: { width: 1600, height: 1000 } });
  for (const [path, file] of SHOTS) {
    const url = `http://grafana:3000/d/${path}?orgId=1&from=now-40m&to=now&kiosk`;
    console.log('shooting', url);
    // domcontentloaded, not networkidle: Grafana keeps live queries open.
    await page.goto(url, { waitUntil: 'domcontentloaded', timeout: 60000 });
    await page.waitForTimeout(20000); // let every panel finish its query and animation
    const body = await page.evaluate(() => document.body.innerText.slice(0, 200));
    console.log('  body starts:', JSON.stringify(body.slice(0, 90)));
    await page.screenshot({ path: `/out/${file}`, fullPage: true });
    console.log('  wrote', file);
  }
  await browser.close();
})().catch((e) => {
  console.error('ERR', e.message);
  process.exit(1);
});
