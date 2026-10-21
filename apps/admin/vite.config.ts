import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

// 开发时只转发同源管理 API 与健康检查；正式静态包由部署入口提供同源接口。
export default defineConfig({
  plugins: [react()],
  server: {
    host: '127.0.0.1',
    port: 5187,
    strictPort: true,
    proxy: {
      '/admin/api': {
        target: 'http://127.0.0.1:8091',
        changeOrigin: false,
      },
      '/health': {
        target: 'http://127.0.0.1:8091',
        changeOrigin: false,
      },
    },
  },
  build: {
    sourcemap: false,
    rollupOptions: {
      output: {
        manualChunks: {
          'react-vendor': ['react', 'react-dom', 'react-router-dom'],
          'ui-vendor': ['@radix-ui/react-dialog', 'lucide-react'],
        },
      },
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./tests/setup.ts'],
    include: ['tests/unit/**/*.test.{ts,tsx}'],
    restoreMocks: true,
  },
})
