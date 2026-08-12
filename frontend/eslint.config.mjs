import { defineConfig, globalIgnores } from "eslint/config";
import nextVitals from "eslint-config-next/core-web-vitals";
import nextTs from "eslint-config-next/typescript";

const eslintConfig = defineConfig([
  ...nextVitals,
  ...nextTs,
  // Override default ignores of eslint-config-next.
  globalIgnores([
    // Default ignores of eslint-config-next:
    ".next/**",
    "out/**",
    "build/**",
    "next-env.d.ts",
    // Yarn's generated runtime. It carries its own eslint-disable directives,
    // which get reported as unused, and it is not ours to fix.
    ".pnp.cjs",
    ".pnp.loader.mjs",
    ".yarn/**",
  ]),
]);

export default eslintConfig;
