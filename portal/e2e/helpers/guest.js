import fs from 'node:fs';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

const REPO_ROOT = path.resolve(__dirname, '..', '..', '..');
const DEFAULT_KEY_PATH = path.join(REPO_ROOT, '.tmp', 'openwrt-vm', 'keys', 'id_ed25519');

export function buildGuestSSHArgs(command, options = {}) {
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

export function execGuest(command, options = {}) {
  if (!command || typeof command !== 'string') {
    return Promise.reject(new Error('execGuest requires non-empty command string'));
  }

  const sshBin = options.sshBin || process.env.SSH_BIN || 'ssh';
  const timeout = Number(options.timeout || 30000);
  const check = options.check !== false;
  const args = buildGuestSSHArgs(command, options);

  return new Promise((resolve, reject) => {
    execFile(
      sshBin,
      args,
      { timeout, maxBuffer: 10 * 1024 * 1024 },
      (error, stdout, stderr) => {
        const result = {
          command,
          stdout: (stdout || '').trimEnd(),
          stderr: (stderr || '').trimEnd(),
          exitCode: error && Number.isInteger(error.code) ? error.code : 0,
        };

        if (error) {
          if (!check) {
            resolve(result);
            return;
          }

          const message = result.stderr || result.stdout || error.message;
          const wrapped = new Error(`execGuest failed (${result.exitCode}): ${command}\n${message}`);
          wrapped.result = result;
          reject(wrapped);
          return;
        }

        resolve(result);
      },
    );
  });
}
