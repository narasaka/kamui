import { fileURLToPath } from "node:url";
import { unified } from "@astrojs/markdown-remark";
import tailwindcss from "@tailwindcss/vite";
import { defineConfig } from "astro/config";
import { remarkDocLinks } from "./src/lib/remark-doc-links";
import { remarkGithubAlerts } from "./src/lib/remark-github-alerts";

const repoRoot = fileURLToPath(new URL("..", import.meta.url));

// https://astro.build/config
export default defineConfig({
  site: "https://kamui.narasaka.dev",
  // Pages build to docs/cli.html and so on; vercel.json serves them at /docs/cli.
  trailingSlash: "never",
  // Compression drops the space between text and an inline tag on the next line.
  compressHTML: false,
  build: {
    format: "file",
    // The stylesheet is small; inlining it saves a render-blocking request.
    inlineStylesheets: "always",
  },
  markdown: {
    syntaxHighlight: false,
    // Render docs/*.md the way GitHub does: GFM on, no typographic quotes or dashes.
    processor: unified({
      smartypants: false,
      remarkPlugins: [[remarkDocLinks, { repoRoot }], remarkGithubAlerts],
    }),
  },
  vite: {
    plugins: [tailwindcss()],
  },
});
