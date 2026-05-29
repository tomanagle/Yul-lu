import path from "node:path";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// In dev (`bun run dev` → vite on :47824) the Go server runs on :47823 and
// vite proxies /api and /mcp to it. Port :47824 sits adjacent to the Go
// server so the two are obviously paired and won't collide with other Vite
// apps running on the default :5173. In production (`bun run build`) the
// Go binary embeds dist/ and serves everything itself, so this proxy is
// unused.
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  clearScreen: false,
  server: {
    port: 47824,
    strictPort: true,
    // Pop the dev dashboard in the default browser once vite is listening.
    // `make dev` (and a bare `bun run dev`) both rely on this — vite waits
    // until the server is actually ready, so there's no connect-refused race.
    open: true,
    proxy: {
      "/api": "http://localhost:47823",
      "/mcp": "http://localhost:47823",
    },
  },
});
