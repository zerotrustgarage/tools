import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { showUpdateDialog } from '../../scripts/updates/client.js';

// jsdom has the dialog element, but does not implement native window modality.
beforeEach(() => Object.defineProperties(HTMLDialogElement.prototype, {
  showModal: { configurable: true, writable: true, value() { this.open = true; } },
  close: { configurable: true, writable: true, value() { this.dispatchEvent(new Event('close')); } },
}));
afterEach(() => {
  document.body.replaceChildren(); vi.unstubAllGlobals(); vi.restoreAllMocks();
  Reflect.deleteProperty(HTMLDialogElement.prototype, 'showModal');
  Reflect.deleteProperty(HTMLDialogElement.prototype, 'close');
});

describe('update dialog', () => {
  it('renders a checked release as text and opens only its constructed GitHub URL', async () => {
    vi.spyOn(HTMLDialogElement.prototype, 'showModal').mockImplementation(function (this: HTMLDialogElement) { this.open = true; });
    vi.spyOn(HTMLDialogElement.prototype, 'close').mockImplementation(function (this: HTMLDialogElement) { this.dispatchEvent(new Event('close')); });
    const openExternal = vi.fn();
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify([
      { tag_name: 'readout-v0.2.0', published_at: '2026-10-22T12:00:00Z', draft: false, prerelease: false,
        html_url: 'javascript:alert(1)', body: '<img src=x onerror=alert(1)>', assets: [
          { name: 'SHA256SUMS.txt', state: 'uploaded', size: 100 },
          { name: 'ZTG-Readout-0.2.0-Setup.exe', state: 'uploaded', size: 100 },
        ] },
    ]))));
    showUpdateDialog({ tool: 'readout', current: '0.1.0', openExternal });
    await vi.waitFor(() => expect(document.querySelector('[role="status"]')?.textContent).toBe('Update available: 0.2.0.'));
    expect(document.querySelector('img')).toBeNull();
    const button = [...document.querySelectorAll('button')].find(b => b.textContent === 'View release and downloads')!;
    button.click();
    expect(openExternal).toHaveBeenCalledWith('https://github.com/zerotrustgarage/tools/releases/tag/readout-v0.2.0');
    [...document.querySelectorAll('button')].find(b => b.textContent === 'Close')!.click();
    expect(document.querySelector('dialog')).toBeNull();
  });

  it('shows offline failure and allows retry without claiming the app is current', async () => {
    vi.spyOn(HTMLDialogElement.prototype, 'showModal').mockImplementation(() => {});
    const request = vi.fn().mockRejectedValueOnce(new TypeError('offline')).mockResolvedValueOnce(new Response('[]'));
    vi.stubGlobal('fetch', request);
    showUpdateDialog({ tool: 'readout', current: '0.1.0', openExternal: vi.fn() });
    await vi.waitFor(() => expect(document.querySelector('[role="status"]')?.textContent).toContain('Check your connection'));
    [...document.querySelectorAll('button')].find(b => b.textContent === 'Check again')!.click();
    await vi.waitFor(() => expect(document.querySelector('[role="status"]')?.textContent).toContain('No supported Windows release'));
    expect(request).toHaveBeenCalledTimes(2);
  });
});
