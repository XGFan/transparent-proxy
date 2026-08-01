#!/usr/bin/env node
/**
 * 校验构建产物符合面板契约：
 * - server/web/panel.js 存在，且是唯一的 JS
 * - 不产出独立 CSS（样式必须内联进 panel.js 才能进 shadow root）
 * - server/web/index.html 是 standalone 宿主页
 */
import { existsSync, readdirSync, readFileSync } from 'node:fs';
import path from 'node:path';

const webRoot = path.resolve(process.cwd(), '../server/web');
const fail = msg => { console.error(`构建产物校验失败: ${msg}`); process.exit(1); };

if (!existsSync(path.join(webRoot, 'panel.js'))) {
  fail('缺少 server/web/panel.js');
}
if (!existsSync(path.join(webRoot, 'index.html'))) {
  fail('缺少 server/web/index.html');
}

const files = readdirSync(webRoot, { recursive: true }).map(String);

const jsFiles = files.filter(f => f.endsWith('.js'));
if (jsFiles.length !== 1 || jsFiles[0] !== 'panel.js') {
  fail(`panel.js 必须是唯一 JS 产物，实际: ${jsFiles.join(', ')}`);
}

const cssFiles = files.filter(f => f.endsWith('.css'));
if (cssFiles.length > 0) {
  fail(`CSS 必须内联进 panel.js，实际产出: ${cssFiles.join(', ')}`);
}

const html = readFileSync(path.join(webRoot, 'index.html'), 'utf-8');
if (!html.includes('/panel.js') || !html.includes('<tp-panel')) {
  fail('index.html 未加载 /panel.js 或未挂载 <tp-panel>');
}

console.log('✓ 构建产物校验通过 (panel.js 单文件 + standalone 宿主页)');
