/// <reference types="vitest" />
import { defineConfig } from 'vite';
import preact from '@preact/preset-vite';
import path from 'path';

const embeddedFrontendOutDir = path.resolve(__dirname, '../server/web');

export default defineConfig({
  base: './',
  plugins: [preact()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  build: {
    outDir: embeddedFrontendOutDir,
    sourcemap: false,
    emptyOutDir: true,
    assetsDir: 'assets',
    rollupOptions: {
      output: {
        entryFileNames: 'assets/[name]-[hash].js',
        chunkFileNames: 'assets/[name]-[hash].js',
        assetFileNames: 'assets/[name]-[hash].[ext]',
      },
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
    include: ['src/**/*.{test,spec}.{js,mjs,cjs,ts,tsx}'],
  },
});
