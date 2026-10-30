// Remote images (RD14) are never fetched: the sanitizer turns each one into a placeholder
// (sanitize.ts, postprocess.ts) and the Content Security Policy would refuse the request anyway.
// This module only counts those placeholders so the status bar can say so once, instead of the
// reader having to find every chip. It reads the already sanitized preview and loads nothing.
//
// A "Load remote images" choice is deliberately not built yet. The CSP is fixed for the lifetime
// of a page and cannot be widened for one document, and the desktop shell has no HTTPS client
// (docs/roadmap.md RD14), so the only way to load an image today would be to allow every https
// origin all the time. That trade is the owner's to make; see readout/CLAUDE.md invariant 2.

export interface RemoteImageSummary {
  /** Placeholders in the preview, one per image. */
  count: number;
  /** Distinct hosts, in order of first appearance. */
  hosts: string[];
  /** Images whose address starts with http:, which Readout would refuse even with an opt-in. */
  insecure: number;
}

const MAX_HOSTS_LISTED = 8;

export function summarizeRemoteImages(root: ParentNode): RemoteImageSummary {
  const hosts: string[] = [];
  let count = 0;
  let insecure = 0;
  for (const chip of root.querySelectorAll<HTMLElement>('a.remote-image-placeholder')) {
    const url = chip.getAttribute('data-remote-url') ?? '';
    count++;
    if (/^http:/i.test(url.trim())) insecure++;
    let host = '';
    try {
      host = new URL(url.trim(), 'https://invalid.example').host;
    } catch {
      // An address that does not parse is counted but names no host.
    }
    if (host && !hosts.includes(host)) hosts.push(host);
  }
  return { count, hosts, insecure };
}

/** The status bar text and its tooltip, or null when the document has no remote images. */
export function describeRemoteImages(summary: RemoteImageSummary): { text: string; title: string } | null {
  if (summary.count === 0) return null;
  const noun = summary.count === 1 ? 'remote image' : 'remote images';
  const listed = summary.hosts.slice(0, MAX_HOSTS_LISTED).join(', ');
  const more = summary.hosts.length > MAX_HOSTS_LISTED ? ` and ${summary.hosts.length - MAX_HOSTS_LISTED} more` : '';
  const lines = [
    `This document has ${summary.count} ${noun}. Readout does not load them, so opening a file cannot tell a tracking server that you read it.`,
  ];
  if (listed) lines.push(`From: ${listed}${more}`);
  if (summary.insecure > 0) lines.push(`${summary.insecure} use plain http.`);
  lines.push('Each placeholder in the preview links to its image; clicking it opens the image in your browser.');
  return { text: `${summary.count} ${noun} not loaded`, title: lines.join('\n') };
}
