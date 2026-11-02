import path from "node:path";
import type { NextConfig } from "next";

const config: NextConfig = {
  // The SDK is installed from this checkout (nothing is published): node_modules/@payrudder/sdk is a link to ../../sdk/typescript, outside this folder, so the bundler must be
  // allowed to read up to the repository root, and Node loads the package at run time. Drop both lines when the SDK comes from a registry.
  turbopack: { root: path.join(__dirname, "../..") },
  serverExternalPackages: ["@payrudder/sdk"],
};

export default config;
