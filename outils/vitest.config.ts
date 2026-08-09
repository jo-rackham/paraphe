import { defineConfig } from "vitest/config";

// The root is the repository's: the shared core keeps its tests next to
// its code, and they must run with the tools'.
export default defineConfig({
  root: "..",
  test: {
    include: ["outils/**/*.test.ts", "noyau/**/*.test.ts"],
  },
});
