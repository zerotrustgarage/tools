// Renders the desktop title bar (src/ui/titlebar.ts, the real component and the real stylesheet)
// with window controls that only record what was asked. The built browser bundle never draws it:
// the bar exists only when the platform supplies window controls, which is the Tauri shell.
// Served by uitest/run.mjs through a Vite dev server; not part of any build.
import '../../src/styles/app.css';
import { titleBar } from '../../src/ui/titlebar.ts';

const calls = (window.__calls = []);
document.documentElement.dataset.theme = matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
const controls = {
  async minimize() { calls.push('minimize'); },
  async toggleMaximize() { calls.push('toggleMaximize'); },
  async close() { calls.push('close'); },
  onMaximizedChange(handler) {
    window.__setMaximized = handler;
    handler(false);
  },
};
const root = document.createElement('div');
root.className = 'app has-titlebar';
root.append(titleBar(controls, 'ZTG Readout'));
document.getElementById('app').append(root);
window.__ready = true;
