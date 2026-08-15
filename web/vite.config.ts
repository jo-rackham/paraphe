import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// Published on GitHub Pages under /<repo>/; PARAPHE_BASE_PATH allows serving
// elsewhere (a domain root, a subpath of another site).
const target = process.env.PARAPHE_API ?? "http://127.0.0.1:8047";
export default defineConfig({
  base: process.env.PARAPHE_BASE_PATH ?? "/paraphe/",
  define: {
    // The instance the published browser version may pre-fill a campaign
    // from, baked HERE and not read from the URL: a link is free to name a
    // campaign, never a host. Empty = the ?org= parameter does nothing.
    "import.meta.env.PARAPHE_INSTANCE_DOMAIN": JSON.stringify(
      process.env.PARAPHE_BASE_DOMAIN ?? "",
    ),
  },
  plugins: [react()],
  build: {
    outDir: "dist",
    // no network request at runtime: everything is inlined or local
    assetsInlineLimit: 4096,
  },
  server: {
    // GUIDE.md lives at the repository root: the same source for both
    // modes and the mass mailing, no copy under web/
    fs: { allow: [".."] },
    // in development the Go API runs alongside (`task api`); in production
    // it serves the front end, so same origin either way
    proxy: {
      "/api": { target, changeOrigin: false },
      "/health": { target, changeOrigin: false },
    },
  },
  test: {
    // jsdom and not node: mode detection reads a tag from the document,
    // and local tracking lives in IndexedDB. Without a DOM neither was
    // testable — and that is precisely where the work-loss defects were.
    environment: "jsdom",
    // .tsx too: a component test at the natural name (Browser.test.tsx)
    // was silently never collected, and the suite stayed green with a
    // deliberately false assertion in it
    include: ["src/**/*.test.{ts,tsx}"],
    setupFiles: ["src/testing/setup.ts"],
  },
});
