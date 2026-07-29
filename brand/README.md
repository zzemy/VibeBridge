# VibeBridge — Brand Guidelines

This page is the single source of truth for VibeBridge visual identity. If you
are producing UI, marketing material, screenshots, or any other surface that
will be seen by users, follow the tokens and rules below. When in doubt, match
what the [brand film](../blob/main/.github/brand-film.mp4) does.

## The mark

The mark is a flat, muted arch bridge with a terminal prompt at its base — a
literal picture of "your local terminal, reachable from across the room". It is
deliberately not a polished product shot; the project is technical and
early-stage, and the brand should look the part.

- **Two approved variants** — `brand/light/` (cream background, slate strokes)
  and `brand/dark/` (slate background, cream strokes). Always use the variant
  that matches the surface.
- **Never** recolour the strokes, add gradients, drop shadows, bevels, or
  rotate the mark. Flat muted only.
- **Minimum size** 16px square (favicon). Below that, drop to a single-colour
  monogram. Do not render the full mark under 24px on screen.

## Colour

All colours are tokenised. Do not introduce one-off hex values in product
surfaces — reference the same five tokens everywhere.

| Token        | Hex       | Role                                                    |
| ---          | ---       | ---                                                     |
| `cream`      | `#FEFAEB` | Primary surface, light backgrounds, document chrome     |
| `cream-deep` | `#F3ECD8` | Subtle surface lift, scene backgrounds in the film      |
| `ink`        | `#2B3C4A` | Primary text, logo strokes on light surfaces            |
| `ink-soft`   | `#6B7A8A` | Secondary text, captions, supporting copy               |
| `ink-faint`  | `#B9C1C9` | Tertiary — dotted patterns, hairline rules, dividers    |
| `accent`     | `#C2643A` | The single warm accent. Use sparingly, max once per view (CTA, play button, key data point) |

The accent is the only non-cool colour in the system. It exists so the eye has
exactly one place to land on. Adding a second accent breaks the system.

## Typography

Three families, three roles, no overlap.

| Family            | Role     | Weights used | Notes                                      |
| ---               | ---      | ---          | ---                                        |
| **Sora**          | Display  | 500 / 600 / 700 | Headlines, the wordmark, film titles       |
| **Inter**         | Body     | 400 / 500 / 600 | UI, body copy, captions                    |
| **JetBrains Mono**| Mono     | 400 / 500 / 600 | Code, file paths, terminal text, version   |

All three are loaded from `miaoda.feishu.cn/fonts` in the brand film. For
offline use, vendor the WOFF2 files alongside the document.

Tracking: display titles sit at `letter-spacing: -0.032em`. Body text stays at
the family default. Do not set monospace tighter than `-0.02em`.

## Voice

VibeBridge is a developer tool for people who care how their CLI behaves. The
voice mirrors that:

- **Reserved, not cold.** No marketing adjectives in product copy.
- **Direct.** Short sentences. Verbs over nouns. Show, don't sell.
- **Honest about state.** "Early-stage software for trusted private networks"
  is the kind of line we keep. Hype is not.
- **Concrete numbers, not feelings.** Bytes, seconds, version strings.

Bad: *"Revolutionise your AI workflow from anywhere."*
Good: *"Control local AI CLI sessions from your phone, over a trusted LAN."*

## Logo usage

**Clear space.** Keep at least the height of the bridge arch in empty space on
all four sides of the mark. No text, no other marks, no border decorations
inside that zone.

**Backgrounds.** Light surfaces → `brand/light/`. Dark surfaces → `brand/dark/`.
Never place the mark on a busy photograph. Never place the light mark on cream
or the dark mark on slate — the strokes vanish.

**Don't.**

- Don't outline, glow, emboss, or animate the mark's strokes.
- Don't substitute a different bridge, terminal cursor, or monogram.
- Don't write "VibeBridge" in a different typeface next to the mark.
- Don't place the mark inside a coloured chip or pill — its background is the
  surface it sits on.

## Asset inventory

Light and dark icon sets in 16–1024, ICO, and maskable, plus a README banner
and the brand film poster:

| Asset                                     | Where to use it                                |
| ---                                       | ---                                            |
| `brand/light/icon-{16..1024}.png`         | Light-surface icons (README, docs, store)      |
| `brand/light/icon.ico`                    | Windows installers, light-surface tray         |
| `brand/light/maskable-512.png`            | PWA manifest (light)                           |
| `brand/dark/icon-{16..1024}.png`          | Dark-surface icons (Windows tray, dark UI)     |
| `brand/dark/icon.ico`                     | Windows tray, installers on dark chrome        |
| `brand/dark/maskable-512.png`             | PWA manifest (dark)                            |
| `brand/banner.jpg`                        | README header (1600×800)                       |
| `.github/brand-film-poster.png`           | Static fallback thumbnail (1280×720)            |
| `.github/brand-film.mp4`                  | Master brand film — 42 seconds, 1920×1080, H.264 |
| `.github/brand-film.gif`                  | Inline preview that autoplays in GitHub README (720×405, 12fps, ~3.4 MB) |

Each icon set was generated from a single source mark via Seedream and resized
with PIL, with the cream / slate backgrounds re-applied per variant. See the
`Seedream + PIL` notes at the bottom of this document for the workflow.

## Banner & film

- **`brand/banner.jpg`** — 1600×800, cream background, arch mark left,
  wordmark right. Lives in the README hero.
- **`.github/brand-film-poster.png`** — 1280×720, the brand film frozen on a
  representative frame with a play affordance. Kept as a static fallback for
  surfaces that cannot render the animated GIF (RSS, terminal previews, image
  crawlers).
- **`.github/brand-film.gif`** — 720×405, 12fps, 42-second loop, palette
  optimised, ~3.4 MB. The inline preview the README embeds with an 
  tag; GitHub does not honour  in repo markdown, but it does autoplay
  animated GIFs, so this is what users actually see.
- **`.github/brand-film.mp4`** — the master brand film, 42 seconds, 1920×1080,
  30fps, H.264, ~1.7 MB. Linked from the README as a download for anyone who
  wants the full-resolution master with audio support if it is later added.

If you re-render the film, keep the same palette and typography tokens. Do not
add a soundtrack unless it is committed under a permissive licence and noted in
the provenance section.

## For contributors: regenerating assets

The icon set and the film both originate from a single source mark plus a
single design system. The reproducibility loop is:

1. Update the mark source (Seedream batch with the same prompt) only if the
   design system changes.
2. Re-apply the colour tokens per variant (light / dark) and export at every
   required size.
3. Re-render the brand film from the React scene graph in
   `vibeforge-brand-video-wahkrkg/index.html`; seek per-frame and stitch with
   ffmpeg (`libx264 -preset veryfast -crf 20 -pix_fmt yuv420p -movflags +faststart`).

Provenance — icon set generated 2026-07-29 from Seedream source + PIL resize;
brand film rendered 2026-07-29 from the React engine; no third-party assets
are embedded beyond the licensed typefaces.
