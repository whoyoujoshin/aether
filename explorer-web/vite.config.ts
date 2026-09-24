import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Dev-mode proxy so the frontend can call plain "/api/..." paths
// without CORS gymnastics, while the Go backend (cmd/explorer) runs
// separately on :8081. In production, cmd/explorer's --static flag
// serves this app's build output directly from the same origin, so
// the proxy is dev-only.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/api": {
        target: "http://localhost:8081",
        changeOrigin: true,
      },
    },
  },
});
