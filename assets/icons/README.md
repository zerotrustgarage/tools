# ZTG application icons

Original vector glyphs for Scanner (radar), Copy (duplicate pages), TFTP (transfer arrows) and
Readout (open book). Each carries the green cursor from the official compact ZTG wordmark in
`zerotrustgarage/ztg`, `public/brand/zero-trust-garage-ztg-white-green.svg`. The main cursor keeps
the wordmark's 28:146 aspect ratio. Small variants widen it to survive Windows display scaling.
The glyphs use `dim2` (`#64748b`) and `acc` (`#00FF41`) from the canonical `design-tokens.json`.
There is no background tile, black border, baked-in shadow or font dependency.

- `icon.svg`: application master; `small.svg`: simplified glyph for 16 to 32 pixel use.
- `icon.png`: transparent 512 pixel image.
- `icon.ico`: 16, 20, 24, 32, 40, 48, 64, 128 and 256 pixel Windows images for Start, taskbar,
  Explorer, shortcuts and the installer. Small frames use the simplified drawing.
- `tray.png`, `tray.ico`: transparent small assets for a notification-area implementation.
  The ICO includes 16, 20, 24, 32, 40 and 48 pixel frames for display scaling.

Scanner's EXEs and MSI share `scanner/installer/icon.ico`; Readout's Tauri EXE, NSIS installer,
MSI, file associations and shortcuts use `readout/src-tauri/icons/`. Both browser favicons are
updated too. Copy is at RC0 in PR #6, and TFTP has a CLI only. Their desktop, installer and
notification-area consumers still belong to future slices. None of these applications currently
implements a notification-area menu; providing tray assets does not introduce background behavior.

Edit the SVG masters here, then export with the same optional Playwright version as Scanner UI
checks (`playwright@1.56.1`). No new build or shipping dependency is added:

```sh
NODE_PATH=$(npm root --global) node scripts/icons/build.mjs
# To use an installed Chromium instead of Playwright's browser:
NODE_PATH=$(npm root --global) CHROMIUM=/usr/bin/chromium node scripts/icons/build.mjs
node scripts/ci/check-icons.mjs
```

The exporter refreshes every committed consumer. Normal app and release builds use those
committed images and need no browser. CI checks alpha transparency, PNG and ICO dimensions,
palette values, distinct glyphs and identical consumer copies. Open [the preview](../../docs/icons.html)
to inspect every icon on light and dark backgrounds at Windows sizes.
