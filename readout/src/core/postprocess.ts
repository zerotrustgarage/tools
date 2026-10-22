import katex from 'katex';
import { sanitizeSvg } from './sanitize';

// Turns the sanitized placeholders into rendered math, diagrams and local images. Each step
// works on text the sanitizer already accepted, and each produces markup from a trusted
// library configured not to trust its input (KaTeX trust:false, Mermaid securityLevel strict).

export interface PostprocessOptions {
  /** 'htmlAndMathml' for the preview; 'mathml' for export, which then needs no KaTeX fonts. */
  mathOutput: 'htmlAndMathml' | 'mathml';
  /** Resolves a path relative to the open document to an object URL, or null. */
  loadLocalImage?: (relative: string) => Promise<string | null>;
  dark: boolean;
}

const mermaidCache = new Map<string, string>();
let mermaidModule: Promise<typeof import('mermaid')['default']> | undefined;
let mermaidTheme: string | undefined;
let mermaidCounter = 0;

async function loadMermaid(dark: boolean) {
  // Mermaid is large, so it loads only when a document contains a diagram.
  mermaidModule ??= import('mermaid').then((m) => m.default);
  const mermaid = await mermaidModule;
  const theme = dark ? 'dark' : 'default';
  if (mermaidTheme !== theme) {
    mermaid.initialize({
      startOnLoad: false,
      securityLevel: 'strict',
      theme,
      htmlLabels: false,
      flowchart: { htmlLabels: false },
      // Keys a diagram's own %%{init}%% directive may not override.
      secure: ['secure', 'securityLevel', 'startOnLoad', 'maxTextSize', 'maxEdges', 'themeCSS', 'fontFamily', 'htmlLabels', 'flowchart'],
      maxTextSize: 100_000,
    });
    mermaidTheme = theme;
    mermaidCache.clear();
  }
  return mermaid;
}

function renderMath(root: ParentNode, output: PostprocessOptions['mathOutput']): void {
  for (const el of root.querySelectorAll<HTMLElement>('.math[data-tex]')) {
    const tex = el.dataset.tex ?? '';
    try {
      katex.render(tex, el, {
        displayMode: el.classList.contains('math-display'),
        throwOnError: false,
        trust: false,
        strict: 'ignore',
        output,
        maxSize: 50,
        maxExpand: 1000,
      });
      el.classList.add('math-rendered');
    } catch {
      el.classList.add('math-error');
    }
  }
}

async function renderDiagrams(root: ParentNode, dark: boolean): Promise<void> {
  const blocks = [...root.querySelectorAll<HTMLElement>('pre[data-mermaid]')];
  if (blocks.length === 0) return;
  const mermaid = await loadMermaid(dark);
  for (const block of blocks) {
    const source = block.textContent ?? '';
    const key = `${dark ? 'd' : 'l'}:${source}`;
    let svg = mermaidCache.get(key);
    if (svg === undefined) {
      try {
        const result = await mermaid.render(`ztg-mermaid-${++mermaidCounter}`, source);
        svg = sanitizeSvg(result.svg);
      } catch (error) {
        block.classList.add('mermaid-error');
        block.title = error instanceof Error ? error.message : 'Diagram could not be rendered';
        continue;
      }
      mermaidCache.set(key, svg);
    }
    const figure = document.createElement('figure');
    figure.className = 'mermaid-diagram';
    if (block.dataset.line) figure.dataset.line = block.dataset.line;
    figure.innerHTML = svg;
    block.replaceWith(figure);
  }
}

function describeRemoteImages(root: ParentNode): void {
  for (const img of root.querySelectorAll<HTMLImageElement>('img[data-remote-src]')) {
    const url = img.dataset.remoteSrc ?? '';
    const chip = document.createElement('a');
    chip.className = 'remote-image-placeholder';
    chip.href = url;
    chip.rel = 'noopener noreferrer';
    let host = url;
    try {
      host = new URL(url, 'https://invalid.example').host;
    } catch {
      // keep the raw text
    }
    chip.textContent = `Remote image not loaded${img.alt ? `: ${img.alt}` : ''} (${host})`;
    chip.title = `${url}\nRemote images are not fetched, so opening a file cannot notify a tracking server. Click to open it in your browser.`;
    img.replaceWith(chip);
  }
}

async function resolveLocalImages(root: ParentNode, load: PostprocessOptions['loadLocalImage']): Promise<void> {
  const images = [...root.querySelectorAll<HTMLImageElement>('img[data-local-src]')];
  await Promise.all(
    images.map(async (img) => {
      const url = load ? await load(img.dataset.localSrc ?? '').catch(() => null) : null;
      if (url) img.src = url;
      else img.classList.add('local-image-missing');
    }),
  );
}

/** Relative luminance of a CSS colour the browser can parse, or null. */
function luminance(color: string): number | null {
  const probe = document.createElement('span');
  probe.style.color = color;
  if (!probe.style.color) return null;
  document.body.append(probe);
  const rgb = getComputedStyle(probe).color.match(/[\d.]+/g)?.slice(0, 3).map(Number);
  probe.remove();
  if (!rgb || rgb.length < 3) return null;
  const [r, g, b] = rgb.map((v) => {
    const c = v / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  }) as [number, number, number];
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

/**
 * Authors set a highlight colour and assume dark text (or the reverse). When an element sets a
 * background but no text colour, pick a readable text colour for that background, so a pale
 * highlight stays legible in the dark theme. Display only: the source is never changed.
 */
function keepHighlightsReadable(root: ParentNode): void {
  for (const node of root.querySelectorAll<HTMLElement>('[style*="background"]')) {
    if (node.style.color) continue;
    const background = node.style.backgroundColor;
    const lum = background ? luminance(background) : null;
    if (lum === null) continue;
    node.style.color = lum > 0.4 ? '#1f2937' : '#f8fafc';
  }
}

export async function postprocess(root: ParentNode, options: PostprocessOptions): Promise<void> {
  renderMath(root, options.mathOutput);
  keepHighlightsReadable(root);
  describeRemoteImages(root);
  await Promise.all([renderDiagrams(root, options.dark), resolveLocalImages(root, options.loadLocalImage)]);
}
