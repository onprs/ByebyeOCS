import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

// 开发时转发同源管理 API、健康检查与题库维护面板；正式静态包由部署入口提供同源接口。
const adminPort = Number(process.env.BYEBYEOCS_DEV_ADMIN_PORT) || 5187
const adminApiTarget = process.env.BYEBYEOCS_DEV_ADMIN_API_TARGET || 'http://127.0.0.1:8091'
const tikuTarget = process.env.BYEBYEOCS_DEV_TIKU_TARGET || 'http://127.0.0.1:8092'

export default defineConfig({
  plugins: [react()],
  server: {
    host: '127.0.0.1',
    port: adminPort,
    strictPort: true,
    proxy: {
      '/admin/api': {
        target: adminApiTarget,
        changeOrigin: false,
      },
      '/health': {
        target: adminApiTarget,
        changeOrigin: false,
      },
      '^/panel(?:/|$)': {
        target: tikuTarget,
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
