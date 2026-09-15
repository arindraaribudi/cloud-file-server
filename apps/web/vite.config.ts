import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Proxy /api -> BFF during dev so the cookie + auth flow stays single-origin-feel.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 9001,
    proxy: {
      "/api": { target: "http://localhost:7000", changeOrigin: true },
    },
  },
});
