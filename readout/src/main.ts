import 'katex/dist/katex.min.css';
import './styles/preview.css';
import './styles/print.css';
import './styles/app.css';
import { App } from './ui/app';
import type { Platform } from './platform/types';

// One build serves both targets: inside the desktop app Tauri injects its IPC bridge, and the
// desktop platform module (which imports the Tauri API) loads only then.
async function choosePlatform(): Promise<Platform> {
  if ('__TAURI_INTERNALS__' in window) return (await import('./platform/desktop')).desktopPlatform;
  return (await import('./platform/web')).webPlatform;
}

const host = document.getElementById('app')!;
void choosePlatform().then((platform) => new App(platform).start(host));
