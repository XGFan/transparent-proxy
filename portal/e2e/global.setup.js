import fs from 'node:fs';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

const REPO_ROOT = path.resolve(__dirname, '..', '..');
const DEFAULT_KEY_PATH = path.join(REPO_ROOT, '.tmp', 'openwrt-vm', 'keys', 'id_ed25519');
const GUEST_FIXTURE_PATH = '/tmp/tp-chnroute-fixture.txt';

function execLocal(command, options = {}) {
  return new Promise((resolve, reject) => {
    execFile('sh', ['-c', command], { timeout: options.timeout || 30000 }, (error, stdout, stderr) => {
      if (error) {
        reject(new Error(`Command failed: ${command}\n${stderr || error.message}`));
        return;
      }
      resolve({ stdout: stdout.trim(), stderr: stderr.trim() });
    });
  });
}

function buildGuestSSHArgs(command, options = {}) {
  const host = options.host || process.env.QEMU_HOST || '127.0.0.1';
  const port = String(options.port || process.env.SSH_PORT || '2222');
  const user = options.user || process.env.OPENWRT_VM_SSH_USER || 'root';
  const keyPath = options.keyPath || process.env.OPENWRT_TEST_KEY_PATH || DEFAULT_KEY_PATH;

  if (!fs.existsSync(keyPath)) {
    throw new Error(`guest ssh key not found: ${keyPath}`);
  }

  return [
    '-o', 'StrictHostKeyChecking=no',
    '-o', 'UserKnownHostsFile=/dev/null',
    '-o', 'ConnectTimeout=5',
    '-o', 'BatchMode=yes',
    '-i', keyPath,
    '-p', port,
    `${user}@${host}`,
    command,
  ];
}

function execGuest(command, options = {}) {
  const sshBin = options.sshBin || process.env.SSH_BIN || 'ssh';
  const timeout = Number(options.timeout || 30000);
  const args = buildGuestSSHArgs(command, options);

  return new Promise((resolve, reject) => {
    execFile(sshBin, args, { timeout, maxBuffer: 10 * 1024 * 1024 }, (error, stdout, stderr) => {
      if (error) {
        const message = (stderr || stdout || error.message);
        reject(new Error(`execGuest failed: ${command}\n${message}`));
        return;
      }
      resolve({ stdout: stdout.trim(), stderr: stderr.trim() });
    });
  });
}

async function copyFixtureToGuest(fixturePath) {
  const scpBin = process.env.SCP_BIN || 'scp';
  const host = process.env.QEMU_HOST || '127.0.0.1';
  const port = String(process.env.SSH_PORT || '2222');
  const user = process.env.OPENWRT_VM_SSH_USER || 'root';
  const keyPath = process.env.OPENWRT_TEST_KEY_PATH || DEFAULT_KEY_PATH;

  if (!fs.existsSync(keyPath)) {
    throw new Error(`guest ssh key not found: ${keyPath}`);
  }

  const args = [
    '-O',
    '-o', 'StrictHostKeyChecking=no',
    '-o', 'UserKnownHostsFile=/dev/null',
    '-o', 'ConnectTimeout=5',
    '-o', 'BatchMode=yes',
    '-i', keyPath,
    '-P', port,
    fixturePath,
    `${user}@${host}:${GUEST_FIXTURE_PATH}`,
  ];

  return new Promise((resolve, reject) => {
    execFile(scpBin, args, { timeout: 30000 }, (error, stdout, stderr) => {
      if (error) {
        reject(new Error(`scp failed: ${stderr || error.message}`));
        return;
      }
      resolve({ stdout, stderr });
    });
  });
}

async function restartGuestServerWithFixture(fixturePath) {
  // Stop the init.d service and any running instances
  const stopCmd = `
/etc/init.d/transparent-proxy stop >/dev/null 2>&1 || true
killall server 2>/dev/null || true
sleep 1
killall -9 server 2>/dev/null || true
sleep 1
`;

  await execGuest(stopCmd, { timeout: 10000 });

  // Start with fixture env var
  const startCmd = `TP_CHNROUTE_FIXTURE_PATH='${fixturePath}' /etc/transparent-proxy/server -c /etc/transparent-proxy/config.yaml >/tmp/tp-server.log 2>&1 &`;
  await execGuest(startCmd, { timeout: 10000 });

  // Wait for API to be ready
  const host = process.env.QEMU_HOST || '127.0.0.1';
  const apiPort = process.env.API_PORT || '1444';
  const apiBaseUrl = `http://${host}:${apiPort}`;

  // Poll until /api/ip returns 200
  let ready = false;
  for (let i = 0; i < 30; i++) {
    try {
      const result = await execLocal(`curl -s -o /dev/null -w '%{http_code}' --connect-timeout 2 --max-time 5 '${apiBaseUrl}/api/ip'`, { timeout: 10000 });
      if (result.stdout === '200') {
        ready = true;
        break;
      }
    } catch (e) {
      // ignore
    }
    await new Promise(resolve => setTimeout(resolve, 1000));
  }

  if (!ready) {
    throw new Error('API server did not become ready within 30s');
  }
}

export default async () => {
  const artifactDir = process.env.TP_PLAYWRIGHT_ARTIFACT_DIR
    ? path.resolve(process.env.TP_PLAYWRIGHT_ARTIFACT_DIR)
    : path.resolve(__dirname, '..', 'test-results');

  fs.mkdirSync(artifactDir, { recursive: true });

  const payload = {
    startedAt: new Date().toISOString(),
    suite: process.env.TP_TEST_SUITE_TIER || '',
    apiBaseUrl: process.env.TP_API_BASE_URL || '',
    uiBaseUrl: process.env.TP_UI_BASE_URL || '',
    portalApiTarget: process.env.PORTAL_API_TARGET || '',
    chnrouteFixturePath: process.env.TP_CHNROUTE_FIXTURE_PATH || '',
  };

  fs.writeFileSync(
    path.join(artifactDir, 'harness-global-setup.json'),
    JSON.stringify(payload, null, 2),
    'utf8',
  );

  // Setup fixture for refresh-route tests if fixture path is provided
  const fixturePath = process.env.TP_CHNROUTE_FIXTURE_PATH;
  if (fixturePath && fs.existsSync(fixturePath)) {
    console.log(`[global.setup] Copying fixture to guest: ${fixturePath}`);
    await copyFixtureToGuest(fixturePath);
    console.log(`[global.setup] Restarting guest server with fixture env var`);
    await restartGuestServerWithFixture(GUEST_FIXTURE_PATH);
    console.log(`[global.setup] Guest server ready with fixture`);
  }
};
