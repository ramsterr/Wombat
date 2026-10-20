const { chromium } = require('/tmp/shotenv/node_modules/playwright-core');
const fs = require('fs');
const path = require('path');
(async () => {
  const browser = await chromium.launch({
    executablePath: process.env.HOME + '/Library/Caches/ms-playwright/chromium-1243/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing',
    args: ['--force-color-profile=srgb', '--font-render-hinting=none']
  });
  const page = await browser.newPage({ viewport: { width: 1200, height: 1600 }, deviceScaleFactor: 2 });
  const dir = process.argv[2];
  const files = fs.readdirSync(dir).filter(f => /^poster-\d+\.html$/.test(f)).sort();
  for (const f of files) {
    await page.goto('file://' + path.join(dir, f), { waitUntil: 'networkidle' });
    await page.evaluate(() => document.fonts.ready);
    await page.waitForTimeout(120);
    const out = path.join(dir, '..', 'posters', f.replace('.html', '.png'));
    fs.mkdirSync(path.dirname(out), { recursive: true });
    await page.screenshot({ path: out, type: 'png' });
    console.log('rendered', out);
  }
  await browser.close();
})();
