import type { Plugin } from 'vite';
import { defineConfig } from 'vitest/config';

// The browser build carries its Content Security Policy in a meta tag. The desktop build does
// not: Tauri injects the policy from src-tauri/tauri.conf.json, which must also allow its IPC
// origin, and two policies would intersect. TAURI_ENV_PLATFORM is set by the Tauri CLI.
const WEB_CSP = [
  "default-src 'self'",
  "script-src 'self'",
  // Inline style attributes are part of the documents (colour spans) and of CodeMirror and
  // Mermaid; they pass through the sanitizer's property allow-list first.
  "style-src 'self' 'unsafe-inline'",
  "img-src 'self' data: blob:",
  "font-src 'self' data:",
  "connect-src 'self' blob: https://api.github.com",
  "object-src 'none'",
  "base-uri 'none'",
  "form-action 'none'",
  "frame-src 'none'",
].join('; ');

function webCsp(): Plugin {
  return {
    name: 'ztg-web-csp',
    apply: 'build',
    transformIndexHtml(html) {
      if (process.env.TAURI_ENV_PLATFORM) return html;
      return html.replace(
        '<meta charset="utf-8" />',
        `<meta charset="utf-8" />\n    <meta http-equiv="Content-Security-Policy" content="${WEB_CSP}" />`,
      );
    },
  };
}

export default defineConfig({
  plugins: [webCsp()],
  // Relative asset paths let the browser build be served from any sub-path (for example
  // ztg.sh/tools/markdown/).
  base: './',
  clearScreen: false,
  server: { port: 1420, strictPort: true },
  envPrefix: ['VITE_', 'TAURI_ENV_'],
  build: {
    target: 'es2022',
    sourcemap: false,
    chunkSizeWarningLimit: 4096,
  },
  test: {
    environment: 'jsdom',
    include: ['test/**/*.test.ts'],
  },
});
