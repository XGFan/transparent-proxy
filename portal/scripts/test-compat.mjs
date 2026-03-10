#!/usr/bin/env node
/**
 * 兼容性包装脚本：将 CRA 风格的 --watchAll=false 转换为 Vitest 的 --run
 * 
 * CRA: npm test -- --watchAll=false
 * Vitest: vitest --run
 */

import { spawn } from 'child_process';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);

// 解析参数
const args = process.argv.slice(2);
const hasWatchAllFalse = args.includes('--watchAll=false') || args.includes('--watchAll');

// 过滤掉 CRA 特有参数
const vitestArgs = args.filter(arg => 
  !arg.startsWith('--watchAll') && 
  !arg.startsWith('--watch') &&
  arg !== '--watchAll=false'
);

// 如果有 --watchAll=false 或在非交互模式下，使用 --run
const shouldRun = hasWatchAllFalse || !process.stdout.isTTY;

if (shouldRun && !vitestArgs.includes('--run')) {
  vitestArgs.unshift('--run');
}

// 启动 Vitest - 查找 portal 根目录的 node_modules
const vitest = spawn(
  'npx',
  ['vitest', ...vitestArgs],
  {
    cwd: join(__dirname, '..'),
    stdio: 'inherit',
    shell: process.platform === 'win32'
  }
);

vitest.on('exit', (code) => {
  process.exit(code ?? 1);
});
