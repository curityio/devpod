import { fixupConfigRules, fixupPluginRules } from "@eslint/compat"
import react from "eslint-plugin-react"
import typescriptEslint from "@typescript-eslint/eslint-plugin"
import tanstackQuery from "@tanstack/eslint-plugin-query"
import globals from "globals"
import tsParser from "@typescript-eslint/parser"
import path from "node:path"
import { fileURLToPath } from "node:url"
import js from "@eslint/js"
import { FlatCompat } from "@eslint/eslintrc"
import reactRefresh from "eslint-plugin-react-refresh"

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)
const compat = new FlatCompat({
  baseDirectory: __dirname,
  recommendedConfig: js.configs.recommended,
  allConfig: js.configs.all,
})

export default [
  {
    // eslint.config.js is JavaScript, which the TypeScript project (parserOptions.project) doesn't
    // include, so the TypeScript parser can't lint it.
    ignores: ["dist/**/*", "src-tauri/**/*", "public/**/*", "src/gen/**/*", "eslint.config.js"],
  },
  ...fixupConfigRules(
    compat.extends(
      "eslint:recommended",
      "plugin:react/recommended",
      "plugin:@typescript-eslint/eslint-recommended",
      "plugin:react-hooks/recommended",
      "prettier",
      "plugin:@tanstack/eslint-plugin-query/recommended"
    )
  ),
  reactRefresh.configs.recommended,
  {
    plugins: {
      react: fixupPluginRules(react),
      "@typescript-eslint": fixupPluginRules(typescriptEslint),
      "@tanstack/query": fixupPluginRules(tanstackQuery),
    },

    languageOptions: {
      globals: {
        ...globals.browser,
        ...globals.node,
        Atomics: "readonly",
        SharedArrayBuffer: "readonly",
      },

      parser: tsParser,
      ecmaVersion: 5,

      parserOptions: {
        project: true,
        tsConfigRootDir: __dirname,
      },
    },

    settings: {
      react: {
        version: "detect",
      },
    },
    rules: {
      "react/react-in-jsx-scope": "off",
      "no-unused-vars": "off",
      "@typescript-eslint/no-unused-vars": ["error"],

      "padding-line-between-statements": [
        "warn",
        {
          blankLine: "always",
          prev: "*",
          next: "return",
        },
      ],

      "no-warning-comments": [
        "error",
        {
          terms: ["fixme"],
          location: "start",
        },
      ],

      "@typescript-eslint/no-unnecessary-condition": [
        "warn",
        {
          allowConstantLoopConditions: true,
        },
      ],

      "@typescript-eslint/naming-convention": [
        "error",
        {
          selector: ["typeParameter", "typeAlias"],
          format: ["PascalCase"],
          prefix: ["T"],
        },
        {
          selector: ["interface"],
          format: ["PascalCase"],
          prefix: ["I"],
        },
        {
          selector: ["enum"],
          format: ["PascalCase"],
          prefix: ["E"],
        },
      ],
    },
  },
  {
    // The entry points and the router module export no components, so when they change Vite reloads
    // the page instead of refreshing components in place, which is fine for them.
    files: ["src/main.tsx", "src/routes.tsx", "update-window/src/main.tsx"],
    rules: {
      "react-refresh/only-export-components": "off",
    },
  },
]
