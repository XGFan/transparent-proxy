/// <reference types="vitest" />
import { readFileSync } from 'fs';
import path from 'path';
import { defineConfig, Plugin } from 'vite';
import preact from '@preact/preset-vite';

const embeddedFrontendOutDir = path.resolve(__dirname, '../server/web');

/**
 * lib 模式不处理 index.html，这里把开发用的 index.html 复用为 standalone 宿主页：
 * 只把 dev 的模块入口换成构建产物 /panel.js，避免两份 HTML 漂移。
 */
function emitStandaloneHost(): Plugin {
  return {
    name: 'tp-emit-standalone-host',
    apply: 'build',
    generateBundle() {
      const html = readFileSync(path.resolve(__dirname, 'index.html'), 'utf-8')
        .replace('/src/panel.tsx', '/panel.js');
      this.emitFile({ type: 'asset', fileName: 'index.html', source: html });
    },
  };
}

export default defineConfig({
  plugins: [preact(), emitStandaloneHost()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  build: {
    outDir: embeddedFrontendOutDir,
    sourcemap: false,
    emptyOutDir: true,
    // 面板契约：单文件自包含 ES module，CSS 全部通过 ?inline 进 JS，不产出额外 chunk
    cssCodeSplit: false,
    lib: {
      entry: path.resolve(__dirname, 'src/panel.tsx'),
      formats: ['es'],
      fileName: () => 'panel.js',
    },
    rollupOptions: {
      output: { inlineDynamicImports: true },
    },
  },
  server: {
    port: 3000,
    // Proxy API requests to backend during development
    proxy: {
      '/api': {
        target: process.env.PORTAL_API_TARGET || 'http://localhost:1444',
        changeOrigin: true,
      },
    },
  },
  test: {
    globals: true,
    environment: 'jsdom',
    // 面板样式靠 ?inline 导入注入 shadow root，测试里必须真实处理 CSS 才断言得到内容
    css: true,
    setupFiles: ['src/test/setup.ts'],
    include: ['src/**/*.{test,spec}.{js,mjs,cjs,ts,tsx}'],
  },
});
