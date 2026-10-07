import { defineConfig } from 'vite'

export default defineConfig({
  root: './assets',
  build: {
    outDir: '../dist',
    emptyOutDir: true,
    rollupOptions: {
      input: {
        main: './assets/scripts/htmx.js'
      },
      output: {
        entryFileNames: 'js/[name].js',
        chunkFileNames: 'js/[name]-[hash].js',
        assetFileNames: ({ name }) => {
          if (name && name.endsWith('.css')) {
            return 'css/[name][extname]'
          }
          return 'assets/[name]-[hash][extname]'
        }
      }
    },
    minify: 'esbuild',
    sourcemap: true
  },
  server: {
    proxy: {
      // Proxy API calls to Go server during development
      // Auto-detect backend port via VITE_BACKEND_PORT env var, default 8080
      '/api': `http://localhost:${process.env.VITE_BACKEND_PORT || 8080}`,
      '/users': `http://localhost:${process.env.VITE_BACKEND_PORT || 8080}`,
      '/components': `http://localhost:${process.env.VITE_BACKEND_PORT || 8080}`
    }
  },
  css: {
    devSourcemap: true
  }
})
