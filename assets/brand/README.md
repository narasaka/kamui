# Kamui brand assets

Exports of the Kamui icon: a three-armed vortex collapsing into a dark core.
The macOS app icon is `macos/Resources/AppIcon.icns`; the files here are for
the web, documentation, and press.

| File | Use |
| --- | --- |
| `icon.svg`, `icon-{16…1024}.png` | App icon on its tile, with transparent corners. |
| `mark.svg`, `mark-{512,1024}.png` | The vortex alone, in colour, on a transparent background. |
| `mark-black.svg`, `mark-black-*.png` | Single-colour mark for light backgrounds and print. |
| `mark-white.svg`, `mark-white-*.png` | Single-colour mark for dark backgrounds. |
| `favicon.svg` | Website icon. A heavier spiral that stays legible at 16 px and turns light in dark mode. |
| `favicon.ico` | 16, 32, and 48 px fallback for browsers without SVG favicons. |
| `apple-touch-icon.png` | 180 px opaque icon for iOS home screens. |

```html
<link rel="icon" href="/favicon.ico" sizes="48x48">
<link rel="icon" href="/favicon.svg" type="image/svg+xml">
<link rel="apple-touch-icon" href="/apple-touch-icon.png">
```

Leave clear space of at least a quarter of the mark's width around it, and do
not recolour the full-colour mark; use the black or white versions instead.
