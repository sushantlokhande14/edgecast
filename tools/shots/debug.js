const { chromium } = require('playwright');
(async () => {
  const browser = await chromium.launch({ args: ['--no-sandbox'] });
  const page = await browser.newPage({ viewport: { width: 1400, height: 900 } });
  page.on('console', (m) => console.log('CONSOLE', m.type(), m.text().slice(0, 300)));
  page.on('requestfailed', (r) => console.log('FAILED', r.url().slice(0, 160), r.failure() && r.failure().errorText));
  page.on('response', (r) => { if (r.status() >= 400) console.log('HTTP', r.status(), r.url().slice(0, 160)); });
  await page.goto('http://grafana:3000/d/edgecast-compare/edgecast-protocol-comparison?orgId=1&kiosk', { waitUntil: 'domcontentloaded', timeout: 60000 });
  await page.waitForTimeout(8000);
  const html = await page.content();
  console.log('HTML_LEN', html.length);
  console.log('HAS_BOOTDATA', html.includes('grafanaBootData'));
  const scripts = await page.$$eval('script[src]', (els) => els.map((e) => e.getAttribute('src')));
  console.log('SCRIPTS', JSON.stringify(scripts).slice(0, 500));
  await browser.close();
})().catch((e) => { console.error('ERR', e.message); process.exit(1); });
