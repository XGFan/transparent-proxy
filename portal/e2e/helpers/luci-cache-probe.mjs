import crypto from 'node:crypto';
import fs from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';

import { chromium } from 'playwright';

function fail(message) {
  throw new Error(message);
}

function parseArgs(argv) {
  const options = {
    baseUrl: '',
    routePath: '/cgi-bin/luci/admin/services/transparent-proxy',
    profileDir: '',
    outputJson: '',
    screenshot: '',
    label: '',
  };

  for (let index = 0; index < argv.length; index += 1) {
    const token = argv[index];
    switch (token) {
      case '--base-url':
        options.baseUrl = argv[index + 1] || '';
        index += 1;
        break;
      case '--route-path':
        options.routePath = argv[index + 1] || '';
        index += 1;
        break;
      case '--profile-dir':
        options.profileDir = argv[index + 1] || '';
        index += 1;
        break;
      case '--output-json':
        options.outputJson = argv[index + 1] || '';
        index += 1;
        break;
      case '--screenshot':
        options.screenshot = argv[index + 1] || '';
        index += 1;
        break;
      case '--label':
        options.label = argv[index + 1] || '';
        index += 1;
        break;
      default:
        fail(`unknown argument: ${token}`);
    }
  }

  if (!options.baseUrl) {
    fail('missing --base-url');
  }
  if (!options.profileDir) {
    fail('missing --profile-dir');
  }
  if (!options.outputJson) {
    fail('missing --output-json');
  }

  return options;
}

function sha256(input) {
  return crypto.createHash('sha256').update(input).digest('hex');
}

function normalizeBaseUrl(input) {
  return input.endsWith('/') ? input.slice(0, -1) : input;
}

async function ensureParentDir(filePath) {
  await fs.mkdir(path.dirname(filePath), { recursive: true });
}

async function ensureRouteReady(page, routeUrl) {
  await page.goto(routeUrl, { waitUntil: 'domcontentloaded' });

  const usernameInput = page.locator('input[name="luci_username"]');
  if (await usernameInput.count() > 0) {
    await usernameInput.fill('root');
    await page.locator('input[name="luci_password"]').fill('');
    await Promise.all([
      page.waitForLoadState('domcontentloaded'),
      page.locator('form').first().evaluate(form => form.submit()),
    ]);
    await page.goto(routeUrl, { waitUntil: 'domcontentloaded' });
  }

  await page.waitForLoadState('networkidle').catch(() => {});
}

async function main() {
  const options = parseArgs(process.argv.slice(2));
  const baseUrl = normalizeBaseUrl(options.baseUrl);
  const routeUrl = `${baseUrl}${options.routePath}`;
  const jsUrl = `${baseUrl}/luci-static/resources/view/transparent-proxy/index.js`;

  await ensureParentDir(options.outputJson);
  if (options.screenshot) {
    await ensureParentDir(options.screenshot);
  }
  await fs.mkdir(options.profileDir, { recursive: true });

  const context = await chromium.launchPersistentContext(options.profileDir, {
    headless: true,
    viewport: { width: 1440, height: 960 },
  });

  const page = context.pages()[0] || await context.newPage();
  const pageErrors = [];
  const requestFailures = [];
  const consoleMessages = [];
  const networkEvents = [];

  page.on('pageerror', error => {
    pageErrors.push(error.message);
  });

  page.on('requestfailed', request => {
    requestFailures.push({
      url: request.url(),
      method: request.method(),
      failure: request.failure()?.errorText || 'unknown',
    });
  });

  page.on('console', message => {
    if (['error', 'warning'].includes(message.type())) {
      consoleMessages.push({
        type: message.type(),
        text: message.text(),
      });
    }
  });

  page.on('response', response => {
    const url = response.url();
    if (url.includes('/cgi-bin/luci/admin/services/transparent-proxy') || url.includes('/luci-static/resources/view/transparent-proxy/index.js')) {
      networkEvents.push({
        url,
        status: response.status(),
      });
    }
  });

  try {
    await ensureRouteReady(page, routeUrl);
    await page.waitForTimeout(1000);

    const directRouteResponse = await page.evaluate(async url => {
      const response = await fetch(url, { credentials: 'same-origin' });
      return {
        status: response.status,
        text: await response.text(),
      };
    }, routeUrl);
    const directRouteText = directRouteResponse.text;
    if (directRouteResponse.status !== 200) {
      fail(`unexpected direct route status: ${directRouteResponse.status}`);
    }

    const directJsResponse = await page.evaluate(async url => {
      const response = await fetch(url, { credentials: 'same-origin' });
      return {
        status: response.status,
        text: await response.text(),
      };
    }, jsUrl);
    const directJsText = directJsResponse.text;
    if (directJsResponse.status !== 200) {
      fail(`unexpected luci JS status: ${directJsResponse.status}`);
    }

    const fallbackLink = page.locator('[data-testid="tp-fallback-link"]');
    await fallbackLink.waitFor({ state: 'visible', timeout: 15000 });

    const noticeLocator = page.getByText('当前镜像未启用同源承载，已降级为独立管理页。');
    const sameOriginSupported = await page.evaluate(() => window.__TP_LUCI_SAME_ORIGIN_SUPPORTED__ ?? null);
    const fallbackHref = await fallbackLink.getAttribute('href');
    const fallbackText = (await fallbackLink.textContent())?.trim() || '';
    const menuLinkCount = await page.locator(`a[href$="${options.routePath}"]`).count();
    const menuLinkVisible = menuLinkCount > 0
      ? await page.locator(`a[href$="${options.routePath}"]`).first().isVisible().catch(() => false)
      : false;

    if (sameOriginSupported !== 0) {
      fail(`unexpected same-origin flag: ${sameOriginSupported}`);
    }
    if (!fallbackHref || !fallbackHref.endsWith(':1444/')) {
      fail(`unexpected fallback href: ${fallbackHref || '<empty>'}`);
    }
    if (!(await noticeLocator.isVisible())) {
      fail('fallback notice is not visible');
    }
    if (!directRouteText.includes('data-page="admin-services-transparent-proxy"')) {
      fail('route HTML missing data-page marker');
    }
    if (!directRouteText.includes('transparent-proxy/index')) {
      fail('route HTML missing view path marker');
    }
    if (!directRouteText.includes('fallbackNotice')) {
      fail('route HTML missing fallbackNotice');
    }
    if (!directJsText.includes('tp-fallback-link')) {
      fail('LuCI JS missing tp-fallback-link');
    }
    if (!directJsText.includes('window.__TP_LUCI_SAME_ORIGIN_SUPPORTED__=0')) {
      fail('LuCI JS missing fallback-only marker');
    }
    if (pageErrors.length > 0) {
      fail(`pageerror detected: ${pageErrors.join(' | ')}`);
    }
    if (requestFailures.length > 0) {
      fail(`requestfailed detected: ${JSON.stringify(requestFailures)}`);
    }

    if (options.screenshot) {
      await page.screenshot({ path: options.screenshot, fullPage: true });
    }

    const result = {
      label: options.label,
      baseUrl,
      routeUrl,
      jsUrl,
      loginStatus: 'page-form-or-direct-route',
      routeStatus: directRouteResponse.status,
      routeSha256: sha256(directRouteText),
      jsStatus: directJsResponse.status,
      jsSha256: sha256(directJsText),
      sameOriginSupported,
      fallbackHref,
      fallbackText,
      menuLinkCount,
      menuLinkVisible,
      pageErrors,
      requestFailures,
      consoleMessages,
      networkEvents,
      screenshot: options.screenshot || '',
    };

    await fs.writeFile(options.outputJson, `${JSON.stringify(result, null, 2)}\n`, 'utf8');
  } finally {
    await context.close();
  }
}

main().catch(async error => {
  process.stderr.write(`[luci-cache-probe][ERROR] ${error instanceof Error ? error.stack || error.message : String(error)}\n`);
  process.exitCode = 1;
});
