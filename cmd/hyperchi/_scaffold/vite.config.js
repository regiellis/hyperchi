import { defineConfig } from 'vite'

// Vite serves JS/CSS to the Go backend (no proxy). Hit the Go server at :8080;
// it loads modules from this dev server when development=true.
export default defineConfig({
  server: {
    host: '0.0.0.0',
    port: 3000,
    cors: true,
    origin: 'http://localhost:3000',
  },
  build: {
    outDir: 'dist',
    assetsDir: 'assets',
    rollupOptions: {
      input: {
        main: './src/main.js',
        style: './src/style.css',
      },
      output: {
        entryFileNames: 'assets/[name].js',
        chunkFileNames: 'assets/[name].js',
        assetFileNames: 'assets/[name].[ext]',
      },
      onwarn(warning, warn) {
        if (warning.code === 'EVAL' && warning.id && warning.id.includes('htmx')) {
          return
        }
        warn(warning)
      },
    },
  },
  css: { postcss: './postcss.config.js' },
  base: '/',
})
