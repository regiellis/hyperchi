import { defineConfig } from 'vite'
import { fileURLToPath } from 'url'

export default defineConfig({
  resolve: {
    alias: {
      'animejs': fileURLToPath(new URL('./node_modules/animejs/dist/bundles/anime.esm.js', import.meta.url))
    }
  },

  // Assets only, no proxy: the browser loads pages from the Go backend on
  // :8080 and JS and CSS, with HMR, from this server.
  server: {
    host: '0.0.0.0',
    port: 3000,
    cors: true,
    // The base URL written into asset URLs, so pages from :8080 load them here.
    origin: 'http://localhost:3000',
  },

  build: {
    outDir: 'dist',
    assetsDir: 'assets',
    rollupOptions: {
      input: {
        main: './src/main.js',
        style: './src/style.css'
      },
      output: {
        entryFileNames: 'assets/[name].js',
        chunkFileNames: 'assets/[name].js',
        assetFileNames: 'assets/[name].[ext]'
      },
      onwarn(warning, warn) {
        if (warning.code === 'EVAL' && warning.id && warning.id.includes('htmx')) {
          return
        }
        warn(warning)
      }
    }
  },

  css: {
    postcss: './postcss.config.js',
  },

  publicDir: 'public',
  base: '/',
})
