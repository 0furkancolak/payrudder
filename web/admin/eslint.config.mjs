import nextCoreWebVitals from "eslint-config-next/core-web-vitals";
import nextTypescript from "eslint-config-next/typescript";

const config = [
  ...nextCoreWebVitals,
  ...nextTypescript,
  { ignores: [".next/**", "node_modules/**", "src/lib/schema.d.ts", "playwright-report/**", "test-results/**"] },
  {
    rules: {
      // The API client is typed from the OpenAPI file: `any` would defeat that.
      "@typescript-eslint/no-explicit-any": "error",
    },
  },
];

export default config;
