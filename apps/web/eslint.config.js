import js from "@eslint/js";
import globals from "globals";
import tseslint from "typescript-eslint";
import reactHooks from "eslint-plugin-react-hooks";

export default tseslint.config(
  {
    // Build output and generated assets — never linted.
    ignores: ["dist/**", "dev-dist/**", "node_modules/**"],
  },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    // Project-wide rule tuning for this existing codebase: keep genuinely
    // useful rules as errors, demote stylistic/incremental ones to warnings
    // so `pnpm lint` stays green in CI while still surfacing them.
    rules: {
      // Best-effort `catch {}` is intentional throughout (parse/JSON guards).
      "no-empty": ["error", { allowEmptyCatch: true }],
      // `interface FooProps extends React.XHTMLAttributes<...> {}` is the
      // idiomatic named-props pattern; allow the single-extends form.
      "@typescript-eslint/no-empty-object-type": [
        "error",
        { allowInterfaces: "with-single-extends" },
      ],
      "@typescript-eslint/no-explicit-any": "warn",
      "@typescript-eslint/no-unused-vars": [
        "warn",
        { argsIgnorePattern: "^_", varsIgnorePattern: "^_" },
      ],
    },
  },
  {
    // Browser app sources.
    files: ["src/**/*.{ts,tsx}"],
    languageOptions: {
      ecmaVersion: 2022,
      sourceType: "module",
      globals: { ...globals.browser },
    },
    plugins: { "react-hooks": reactHooks },
    rules: {
      "react-hooks/rules-of-hooks": "error",
      "react-hooks/exhaustive-deps": "warn",
    },
  },
  {
    // Node-context tooling: config files and build/test scripts.
    files: ["*.{js,ts}", "scripts/**/*.{js,ts,mts}"],
    languageOptions: {
      globals: { ...globals.node },
    },
  },
  {
    // Dedicated Web Push service worker (static asset, runs in the SW global).
    files: ["public/push-sw.js"],
    languageOptions: {
      globals: { ...globals.serviceworker, ...globals.browser },
    },
  },
);
