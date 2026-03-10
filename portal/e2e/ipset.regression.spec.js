// Regression E2E Suite - Failure/Recovery/Consistency tests
// Task 9: Regression tests for error paths and state consistency
// Uses real portal -> backend -> OpenWrt VM, NOT browser-layer mocks
import { test, expect } from '@playwright/test';
import { selectors } from './helpers/selectors.js';
import { execGuest } from './helpers/guest.js';

const TEST_SET = 'proxy_dst';
// Use IPs from different subnets to avoid nft range merging
const TEST_IP = '10.99.100.1';
const TEST_IP_2 = '10.99.200.1';
const TEST_IP_3 = '10.99.50.1';
const GUEST_TIMEOUT = 30000;

// Guest paths
const GUEST_FIXTURE_PATH = '/tmp/tp-chnroute-fixture.txt';
const GUEST_SERVER_LOG = '/tmp/tp-server.log';
const GUEST_SERVER_BIN = '/etc/transparent-proxy/server';
const GUEST_CONFIG = '/etc/transparent-proxy/config.yaml';

test.describe('Regression E2E Suite', { tag: '@regression' }, () => {
  async function guestExec(command, options = {}) {
    return execGuest(command, {
      timeout: options.timeout || GUEST_TIMEOUT,
      check: false,
    });
  }

  async function guestSetContainsIP(setName, ip) {
    const result = await guestExec(`nft list set inet fw4 ${setName} | grep -F -- '${ip}'`);
    return result.exitCode === 0 && result.stdout.includes(ip);
  }

  async function cleanupAllTestIPs() {
    await guestExec(`nft delete element inet fw4 ${TEST_SET} { ${TEST_IP} } >/dev/null 2>&1 || true`);
    await guestExec(`nft delete element inet fw4 ${TEST_SET} { ${TEST_IP_2} } >/dev/null 2>&1 || true`);
    await guestExec(`nft delete element inet fw4 ${TEST_SET} { ${TEST_IP_3} } >/dev/null 2>&1 || true`);
  }

  async function guestFileExists(filePath) {
    const result = await guestExec(`test -f '${filePath}'`);
    return result.exitCode === 0;
  }

  async function writeInvalidFixtureToGuest() {
    await guestExec(`cat > '${GUEST_FIXTURE_PATH}' << 'EOF'
# invalid fixture
apnic|CN|ipv4|1.0.9.0|not-a-number|20110414|allocated
EOF`);
  }

  async function writeValidFixtureToGuest() {
    await guestExec(`cat > '${GUEST_FIXTURE_PATH}' << 'EOF'
# valid fixture
apnic|CN|ipv4|1.0.1.0|256|20110414|allocated
apnic|CN|ipv4|1.0.2.0|512|20110414|allocated
EOF`);
  }

  async function restartGuestServerWithFixture() {
    await guestExec(`/etc/init.d/transparent-proxy stop >/dev/null 2>&1 || true`);
    await guestExec(`killall server 2>/dev/null || true`);
    await new Promise(resolve => setTimeout(resolve, 500));
    await guestExec(`killall -9 server 2>/dev/null || true`);
    await new Promise(resolve => setTimeout(resolve, 500));
    
    await guestExec(`TP_CHNROUTE_FIXTURE_PATH='${GUEST_FIXTURE_PATH}' ${GUEST_SERVER_BIN} -c ${GUEST_CONFIG} >${GUEST_SERVER_LOG} 2>&1 &`);
    
    const host = process.env.QEMU_HOST || '127.0.0.1';
    const apiPort = process.env.API_PORT || '1444';
    
    for (let i = 0; i < 30; i++) {
      const result = await guestExec(`wget -q -O /dev/null --timeout=2 'http://${host}:${apiPort}/api/ip' && echo 'ready'`);
      if (result.exitCode === 0 && result.stdout.includes('ready')) {
        return true;
      }
      await new Promise(resolve => setTimeout(resolve, 1000));
    }
    return false;
  }

  async function stopGuestServer() {
    await guestExec(`/etc/init.d/transparent-proxy stop >/dev/null 2>&1 || true`);
    await guestExec(`killall server 2>/dev/null || true`);
    await new Promise(resolve => setTimeout(resolve, 500));
    await guestExec(`killall -9 server 2>/dev/null || true`);
    await new Promise(resolve => setTimeout(resolve, 500));
  }

  async function isApiReady() {
    const host = process.env.QEMU_HOST || '127.0.0.1';
    const apiPort = process.env.API_PORT || '1444';
    const result = await guestExec(`wget -q -O /dev/null --timeout=2 'http://${host}:${apiPort}/api/ip'`);
    return result.exitCode === 0;
  }

  /** Wait for /api/status to complete (action-loading disappears) */
  async function waitForPageReady(page) {
    await page.locator('[data-testid="action-loading"]').waitFor({ state: 'hidden', timeout: 20000 });
  }

  /** Navigate to Rules page and wait for content to load */
  async function navigateToRules(page) {
    await page.click(selectors.navRules);
    await page.waitForSelector(selectors.rulesPage, { state: 'visible', timeout: 10000 });
  }

  /** Add an IP to a set by selecting the set and filling the IP */
  async function addIPToSet(page, setName, ip) {
    await page.locator(selectors.setSelect).selectOption(setName);
    const input = page.locator(selectors.ipInput);
    await input.fill(ip);
    await input.press('Enter');
  }

  /** Wait for operation feedback (success or error) on Rules page */
  async function waitForRulesFeedback(page, timeout = 15000) {
    await page.waitForSelector(selectors.operationFeedback, { state: 'visible', timeout });
  }

  /** Wait for error feedback on Rules page */
  async function waitForRulesError(page, timeout = 15000) {
    await page.waitForSelector(selectors.actionError, { state: 'visible', timeout });
  }

  /** Wait for error message on Status page (when server is down) */
  async function waitForStatusError(page, timeout = 15000) {
    await page.waitForSelector('.error-message', { state: 'visible', timeout });
  }

  test.beforeEach(async ({ page }) => {
    await cleanupAllTestIPs();
  });

  test.afterEach(async () => {
    await cleanupAllTestIPs();
  });

  // Test 3: add invalid IP shows error
  test('add invalid IP shows error feedback @regression', async ({ page }) => {
    await page.goto('/');
    await waitForPageReady(page);
    await navigateToRules(page);
    
    await addIPToSet(page, TEST_SET, 'invalid-ip-address');
    
    await waitForRulesError(page, 10000);
    
    const errorElement = page.locator(selectors.actionError);
    const errorText = await errorElement.textContent();
    expect(errorText.toLowerCase()).toMatch(/valid|invalid|error/);
  });

  // Test 4: added IP persists after page reload
  test('added IP persists after page reload @regression', async ({ page }) => {
    const existsBefore = await guestSetContainsIP(TEST_SET, TEST_IP);
    expect(existsBefore).toBe(false);
    
    await page.goto('/');
    await waitForPageReady(page);
    await navigateToRules(page);
    
    await addIPToSet(page, TEST_SET, TEST_IP);
    
    const removeButton = page.locator(selectors.setRemove(TEST_SET, TEST_IP));
    await expect(removeButton).toBeVisible({ timeout: 10000 });
    
    const existsAfterAdd = await guestSetContainsIP(TEST_SET, TEST_IP);
    expect(existsAfterAdd).toBe(true);
    
    await page.reload();
    await waitForPageReady(page);
    await navigateToRules(page);
    
    const removeButtonAfterReload = page.locator(selectors.setRemove(TEST_SET, TEST_IP));
    await expect(removeButtonAfterReload).toBeVisible({ timeout: 10000 });
  });

  // Test 5: removed IP does not reappear after page reload
  test('removed IP does not reappear after page reload @regression', async ({ page }) => {
    // Pre-add IP via guest command - MUST use execGuest with check: true for verification
    await execGuest(`nft add element inet fw4 ${TEST_SET} { ${TEST_IP} }`, { timeout: GUEST_TIMEOUT, check: true });
    
    // Verify IP is in set
    const existsInSet = await guestSetContainsIP(TEST_SET, TEST_IP);
    expect(existsInSet).toBe(true);
    
    await page.goto('/');
    await waitForPageReady(page);
    await navigateToRules(page);
    
    const removeButton = page.locator(selectors.setRemove(TEST_SET, TEST_IP));
    await expect(removeButton).toBeVisible({ timeout: 10000 });
    await removeButton.click();
    
    // Wait for button to disappear
    await expect(removeButton).not.toBeVisible({ timeout: 10000 });
    
    // Verify IP is gone from guest
    const existsAfterRemove = await guestSetContainsIP(TEST_SET, TEST_IP);
    expect(existsAfterRemove).toBe(false);
    
    await page.reload();
    await waitForPageReady(page);
    await navigateToRules(page);
    
    const removeButtonAfterReload = page.locator(selectors.setRemove(TEST_SET, TEST_IP));
    await expect(removeButtonAfterReload).not.toBeVisible({ timeout: 5000 });
  });

  // Test 6: error clears when subsequent action succeeds
  test('error clears when subsequent action succeeds @regression', async ({ page }) => {
    await page.goto('/');
    await waitForPageReady(page);
    await navigateToRules(page);
    
    await addIPToSet(page, TEST_SET, 'invalid-ip-address');
    
    const errorElement = page.locator(selectors.actionError);
    await expect(errorElement).toBeVisible({ timeout: 10000 });
    
    await addIPToSet(page, TEST_SET, TEST_IP);
    
    const removeButton = page.locator(selectors.setRemove(TEST_SET, TEST_IP));
    await expect(removeButton).toBeVisible({ timeout: 10000 });
    
    await expect(errorElement).not.toBeVisible({ timeout: 5000 });
  });

  // Test 7: multiple sequential add operations with non-consecutive IPs
  test('multiple sequential add operations handled correctly @regression', async ({ page }) => {
    await page.goto('/');
    await waitForPageReady(page);
    await navigateToRules(page);
    
    const testIPs = [TEST_IP, TEST_IP_2, TEST_IP_3];
    
    for (const ip of testIPs) {
      await addIPToSet(page, TEST_SET, ip);
      
      const removeButton = page.locator(selectors.setRemove(TEST_SET, ip));
      await expect(removeButton).toBeVisible({ timeout: 10000 });
    }
    
    for (const ip of testIPs) {
      const exists = await guestSetContainsIP(TEST_SET, ip);
      expect(exists).toBe(true);
    }
  });

  // Test 8: sync button shows success feedback
  test('sync button shows success feedback @regression', async ({ page }) => {
    await execGuest(`nft add element inet fw4 ${TEST_SET} { ${TEST_IP} }`, { timeout: GUEST_TIMEOUT, check: true });
    
    await page.goto('/');
    await waitForPageReady(page);
    await navigateToRules(page);
    
    const syncButton = page.locator(selectors.syncButton);
    await expect(syncButton).toBeEnabled();
    await syncButton.click();
    
    await waitForRulesFeedback(page, 15000);
    
    const feedback = page.locator(selectors.operationFeedback);
    await expect(feedback).toContainText('已同步');
    
    const fileExists = await guestFileExists(`/etc/nftables.d/${TEST_SET}.nft`);
    expect(fileExists).toBe(true);
  });

  // Test 9: initial load shows error when server is down
  test('initial load shows error when server is down @regression', async ({ page }) => {
    await stopGuestServer();
    
    const apiReady = await isApiReady();
    expect(apiReady).toBe(false);
    
    await page.goto('/');
    
    await waitForStatusError(page, 15000);
    
    const errorElement = page.locator('.error-message');
    const errorText = await errorElement.textContent();
    expect(errorText.toLowerCase()).toMatch(/fail|error|unavailable|network|获取.*失败/);
    
    await writeValidFixtureToGuest();
    await restartGuestServerWithFixture();
  });

  // Test 10: service recovery allows operations after failure
  test('service recovery allows operations after failure @regression', async ({ page }) => {
    await stopGuestServer();
    
    const apiReadyAfterStop = await isApiReady();
    expect(apiReadyAfterStop).toBe(false);
    
    await page.goto('/');
    
    await waitForStatusError(page, 15000);
    
    await writeValidFixtureToGuest();
    await restartGuestServerWithFixture();
    
    const apiReadyAfterRestore = await isApiReady();
    expect(apiReadyAfterRestore).toBe(true);
    
    await page.reload();
    await waitForPageReady(page);
    await navigateToRules(page);
    
    await page.waitForSelector(selectors.rulesPage, { state: 'visible', timeout: 10000 });
    
    await addIPToSet(page, TEST_SET, TEST_IP);
    
    const removeButton = page.locator(selectors.setRemove(TEST_SET, TEST_IP));
    await expect(removeButton).toBeVisible({ timeout: 10000 });
    
    const syncButton = page.locator(selectors.syncButton);
    await expect(syncButton).toBeEnabled();
    await syncButton.click();
    
    await waitForRulesFeedback(page, 15000);
    
    const feedback = page.locator(selectors.operationFeedback);
    await expect(feedback).toContainText('已同步');
  });
});
