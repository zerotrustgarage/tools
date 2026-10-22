import previewCss from '../styles/preview.css?raw';
import { postprocess } from './postprocess';
import { renderMarkdown } from './render';

// Self-contained HTML export: one file with its styles inline, math as MathML (rendered natively
// by current browsers, so no KaTeX fonts are needed), diagrams as inline SVG and local images
// embedded as data URLs. It carries no script, and its own CSP says so.

export interface ExportOptions {
  title: string;
  loadLocalImage?: (relative: string) => Promise<string | null>;
}

const escapeText = (s: string) => s.replace(/[&<>"]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' })[c]!);

async function toDataUrl(url: string): Promise<string | null> {
  try {
    const blob = await (await fetch(url)).blob();
    return await new Promise((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve(String(reader.result));
      reader.onerror = () => reject(reader.error);
      reader.readAsDataURL(blob);
    });
  } catch {
    return null;
  } finally {
    if (url.startsWith('blob:')) URL.revokeObjectURL(url);
  }
}

export async function exportHtml(source: string, options: ExportOptions): Promise<string> {
  const container = document.createElement('div');
  container.innerHTML = renderMarkdown(source);
  await postprocess(container, {
    mathOutput: 'mathml',
    dark: false,
    loadLocalImage: async (relative) => {
      const url = await options.loadLocalImage?.(relative);
      return url ? toDataUrl(url) : null;
    },
  });
  // Line markers only serve the editor's scroll sync.
  for (const node of container.querySelectorAll('[data-line]')) node.removeAttribute('data-line');
  return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; img-src data:">
<meta name="generator" content="ZTG Readout">
<title>${escapeText(options.title)}</title>
<style>
${previewCss}
body { margin: 0; background: var(--md-bg); }
.markdown-body { max-width: 900px; margin: 0 auto; padding: 40px 24px 64px; }
</style>
</head>
<body data-theme="light">
<article class="markdown-body">
${container.innerHTML}
</article>
</body>
</html>
`;
}
