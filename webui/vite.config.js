import { defineConfig } from 'vite';
import solid from 'vite-plugin-solid';
import devtools from 'solid-devtools/vite';

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [devtools(), solid()],
  base: '/ui/',
  server: {
    proxy: {
      // Proxy API requests to Go backend during dev
      '^/(_history|[0-9a-f-]{36})': {
        target: 'http://localhost:8214',
        changeOrigin: true,
      },
    },
  },
});
