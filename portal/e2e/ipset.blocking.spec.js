// Blocking E2E Suite - Real portal -> backend -> OpenWrt VM smoke tests
import { test, expect } from '@playwright/test';
import { selectors } from './helpers/selectors.js';
import { execGuest } from './helpers/guest.js';

const TEST_SET = 'proxy_dst';
const TEST_IP = '198.18.0.123';
const GUEST_TIMEOUT = 30000;

test.describe('Blocking E2E Suite', { tag: '@blocking' }, () => {
  async function guestSetContainsIP(setName, ip) {
    const result = await execGuest(`nft list set inet fw4 ${setName} | grep -F -- '${ip}'`, {
      timeout: GUEST_TIMEOUT,
      check: false,
    });
    return result.exitCode === 0 && result.stdout.includes(ip);
  }

  async function ensureTestIPAbsent() {
    await execGuest(`nft delete element inet fw4 ${TEST_SET} { ${TEST_IP} } >/dev/null 2>&1 || true`, {
      timeout: GUEST_TIMEOUT,
      check: false,
    });
  }

  async function guestFileExists(filePath) {
    const result = await execGuest(`test -f '${filePath}'`, {
      timeout: GUEST_TIMEOUT,
      check: false,
    });
    return result.exitCode === 0;
  }

  async function guestFileContains(filePath, content) {
    const result = await execGuest(`grep -F -- '${content}' '${filePath}'`, {
      timeout: GUEST_TIMEOUT,
      check: false,
    });
    return result.exitCode === 0;
  }

  async function waitForStatusPageReady(page) {
    await page.locator('.status-page').waitFor({ state: 'visible', timeout: 20000 });
    await page.locator('.loading-indicator').waitFor({ state: 'hidden', timeout: 15000 }).catch(() => {});
  }

  async function waitForRulesPageReady(page) {
    await page.locator('.rules-page').waitFor({ state: 'visible', timeout: 20000 });
    await page.locator('.loading-indicator').waitFor({ state: 'hidden', timeout: 15000 }).catch(() => {});
  }

  async function navigateToRulesPage(page) {
    await page.click('button:has-text("规则管理")');
    await waitForRulesPageReady(page);
  }

  test.beforeEach(async ({ page }) => {
    await ensureTestIPAbsent();
    await page.goto('/');
    await waitForStatusPageReady(page);
  });

  test('initial load shows status and health', async ({ page }) => {
    const statusElement = page.locator(selectors.healthStatus);
    await expect(statusElement).toBeVisible({ timeout: 15000 });

    const statusText = await statusElement.textContent();
    expect(['up', 'down', 'unknown', 'ok', 'healthy', 'error']).toContain(statusText.trim().toLowerCase());

    const rulesCount = page.locator(selectors.rulesCount);
    await expect(rulesCount).toBeVisible();
    const countText = await rulesCount.textContent();
    expect(parseInt(countText, 10)).toBeGreaterThanOrEqual(0);
  });

  test('add IP to proxy_dst shows remove button and guest side effect', async ({ page }) => {
    const existsBefore = await guestSetContainsIP(TEST_SET, TEST_IP);
    expect(existsBefore).toBe(false);

    await navigateToRulesPage(page);

    const setSelect = page.locator(selectors.setSelect);
    await setSelect.selectOption(TEST_SET);

    const ipInput = page.locator(selectors.ipInput);
    await ipInput.fill(TEST_IP);

    const addBtn = page.locator(selectors.addBtn);
    await addBtn.click();

    const operationFeedback = page.locator('.operation-feedback.success');
    await expect(operationFeedback).toBeVisible({ timeout: 10000 });

    const existsAfter = await guestSetContainsIP(TEST_SET, TEST_IP);
    expect(existsAfter).toBe(true);

    await ensureTestIPAbsent();
  });

  test('remove IP from proxy_dst removes from UI and guest', async ({ page }) => {
    await execGuest(`nft add element inet fw4 ${TEST_SET} { ${TEST_IP} }`, {
      timeout: GUEST_TIMEOUT,
    });

    const existsBefore = await guestSetContainsIP(TEST_SET, TEST_IP);
    expect(existsBefore).toBe(true);

    await navigateToRulesPage(page);

    const removeButton = page.locator(selectors.removeBtn(TEST_SET, TEST_IP));
    await expect(removeButton).toBeVisible({ timeout: 10000 });
    await removeButton.click();

    await expect(removeButton).not.toBeVisible({ timeout: 10000 });

    const existsAfter = await guestSetContainsIP(TEST_SET, TEST_IP);
    expect(existsAfter).toBe(false);
  });

  test('sync button shows success feedback and creates guest file', async ({ page }) => {
    await execGuest(`nft add element inet fw4 ${TEST_SET} { ${TEST_IP} }`, {
      timeout: GUEST_TIMEOUT,
    });

    await navigateToRulesPage(page);

    const syncButton = page.locator(selectors.syncButton);
    await expect(syncButton).toBeEnabled();
    await syncButton.click();

    const feedback = page.locator('.operation-feedback.success');
    await expect(feedback).toBeVisible({ timeout: 15000 });

    const fileExists = await guestFileExists(`/etc/nftables.d/${TEST_SET}.nft`);
    expect(fileExists).toBe(true);

    await ensureTestIPAbsent();
  });

  test('sync button is disabled during action', async ({ page }) => {
    await navigateToRulesPage(page);

    const syncButton = page.locator(selectors.syncButton);
    await expect(syncButton).toBeEnabled();

    await syncButton.click();
    await expect(syncButton).toBeDisabled({ timeout: 1000 });
    await expect(syncButton).toBeEnabled({ timeout: 20000 });
  });

  test('page shows all expected set cards', async ({ page }) => {
    const expectedSets = ['direct_src', 'direct_dst', 'proxy_src', 'proxy_dst'];

    for (const setName of expectedSets) {
      const setCard = page.locator(selectors.setCard(setName));
      await expect(setCard).toBeVisible({ timeout: 5000 });
    }
  });
});
