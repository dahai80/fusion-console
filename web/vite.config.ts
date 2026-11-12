import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import path from "node:path";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    outDir: path.resolve(__dirname, "../internal/server/webdist"),
    emptyOutDir: true,
  },
  server: {
    port: 11481,
    proxy: { "/api": "http://127.0.0.1:11480" },
  },
});
