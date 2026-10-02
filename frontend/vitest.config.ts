import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./vitest.setup.ts"],
    include: ["components/**/*.test.{ts,tsx}", "lib/**/*.test.{ts,tsx}"],
    css: false,
    // Observed: the first full run reported 2 × 5000ms timeouts (PhonePreview,
    // TestOnPhone) — both trivial renders that passed in isolation and on
    // re-run. 18 files against 8 CPUs starves a worker past vitest's default
    // 5s before the assertion ever runs; the assertion itself is not slow.
    // Raising the budget covers the contention instead of hiding a slow test.
    testTimeout: 15000,
  },
  resolve: {
    alias: [{ find: "@", replacement: __dirname }],
  },
});
