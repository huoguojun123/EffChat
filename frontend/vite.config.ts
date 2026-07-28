import path from "path"
import tailwindcss from "@tailwindcss/vite"
import react from "@vitejs/plugin-react"
import { defineConfig } from "vitest/config"
import { VitePWA } from "vite-plugin-pwa"

export default defineConfig({
  plugins: [
    react(),
    tailwindcss(),
    VitePWA({
      registerType: "prompt",
      includeAssets: ["favicon.svg", "icons.svg", "theme-init.js"],
      manifest: {
        name: "EffChat",
        short_name: "EffChat",
        description: "Personal AI chat workbench",
        theme_color: "#303236",
        background_color: "#303236",
        display: "standalone",
        start_url: "/",
        scope: "/",
        icons: [
          {
            src: "/pwa-192x192.png",
            sizes: "192x192",
            type: "image/png",
          },
          {
            src: "/pwa-512x512.png",
            sizes: "512x512",
            type: "image/png",
          },
          {
            src: "/pwa-512x512.png",
            sizes: "512x512",
            type: "image/png",
            purpose: "any maskable",
          },
        ],
      },
      workbox: {
        navigateFallback: "index.html",
        cleanupOutdatedCaches: true,
        maximumFileSizeToCacheInBytes: 4 * 1024 * 1024,
        globPatterns: [
          "index.html",
          "manifest.webmanifest",
          "favicon.svg",
          "icons.svg",
          "pwa-*.png",
          "assets/index-*.js",
          "assets/index-*.css",
          "assets/rolldown-runtime-*.js",
          "assets/motion-*.js",
        ],
      },
    }),
  ],
  resolve: {
    alias: [
      { find: "zustand/react", replacement: path.resolve(__dirname, "./node_modules/zustand/esm/react.mjs") },
      { find: "zustand/vanilla", replacement: path.resolve(__dirname, "./node_modules/zustand/esm/vanilla.mjs") },
      { find: "zustand", replacement: path.resolve(__dirname, "./node_modules/zustand/esm/index.mjs") },
      { find: "@", replacement: path.resolve(__dirname, "./src") },
    ],
  },
  optimizeDeps: {
    include: ["react-router/dom"],
  },
  server: {
    port: 5173,
    proxy: {
      "/api": {
        target: "http://localhost:8080",
        changeOrigin: true,
      },
    },
  },
  test: {
    environment: "node",
    server: {
      deps: {
        inline: ["zustand"],
      },
    },
    // e2e/ 由 Playwright 运行（npm run e2e），不走 vitest。
    exclude: ["**/node_modules/**", "**/dist/**", "e2e/**"],
    coverage: {
      provider: "v8",
    },
  },
})
