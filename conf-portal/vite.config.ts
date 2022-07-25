import {defineConfig} from 'vite'
import react from '@vitejs/plugin-react'
import monacoEditorPlugin from 'vite-plugin-monaco-editor';

// https://vitejs.dev/config/
export default defineConfig({
    plugins: [
      react(),
      // @ts-ignore
      monacoEditorPlugin.default({})
    ],
    server: {
      proxy: {
        '/api': {
          target: 'http://localhost:1323',
          changeOrigin: true,
        }
      }
    }
  }
)
