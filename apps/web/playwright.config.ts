import { defineConfig } from "@playwright/test";

// Assumes the stack is already up (docker compose up, or vite dev + ftp-server).
// Set PW_BASE_URL to point at a different host/port.
export default defineConfig({
  testDir: "./e2e",
  fullyParallel: true,
  retries: 0,
  reporter: "list",
  use: {
    baseURL: process.env.PW_BASE_URL ?? "http://localhost:9001",
    trace: "on-first-retry",
  },
});
