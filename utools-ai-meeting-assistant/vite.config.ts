import { readFile, writeFile } from "node:fs/promises";
import { fileURLToPath, URL } from "node:url";

import vue from "@vitejs/plugin-vue";
import { defineConfig, type Plugin } from "vite";

const productionManifestPlugin = (): Plugin => ({
  name: "production-utools-manifest",
  apply: "build",
  async closeBundle() {
    const manifestPath = fileURLToPath(new URL("./dist/plugin.json", import.meta.url));
    const manifest: unknown = JSON.parse(await readFile(manifestPath, "utf8"));
    if (!manifest || typeof manifest !== "object" || Array.isArray(manifest)) {
      throw new Error("dist/plugin.json must contain a JSON object");
    }
    Reflect.deleteProperty(manifest, "development");
    await writeFile(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`, "utf8");
  },
});

export default defineConfig(({ mode }) => ({
  base: "./",
  publicDir: "plugin",
  plugins: [vue(), ...(mode === "production" ? [productionManifestPlugin()] : [])],
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  server: {
    host: "127.0.0.1",
    port: 5173,
    strictPort: true,
    proxy: {
      "/api": {
        target: "http://127.0.0.1:8000",
        changeOrigin: false,
        rewrite: (path) => path.replace(/^\/api/, ""),
      },
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
    sourcemap: mode !== "production",
  },
}));
