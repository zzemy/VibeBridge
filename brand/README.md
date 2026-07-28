# VibeBridge Brand Assets

Two approved logo variants (flat, muted; bridge + terminal-prompt mark):

- `dark/` — 深色版：深石板底 (43,60,74) + 米白线条。用于深色场景、Windows 托盘、深色终端 UI。
- `light/` — 浅色版：米白底 (254,250,235) + 石板蓝线条。用于浅色场景、README、文档、商店 listing。

Each variant contains:

- `icon-16/24/32/48/64/128/192/256/512/1024.png` — 通用 PNG 各尺寸
- `icon.ico` — Windows 多尺寸 ICO（16/24/32/48/256），托盘与安装器用
- `maskable-512.png` — PWA maskable 图标（80% 安全区）

Plus:

- `readme-banner.png` — 1600x800 README 头图

## 用途对照

| 场景 | 文件 |
|---|---|
| Windows 托盘 / 安装器 | `dark/icon.ico`（托盘建议 dark 版） |
| PWA manifest icons | `light/icon-192.png`, `light/icon-512.png`, `light/maskable-512.png` |
| iOS App Store master | `icon-1024.png`（按场景选 dark/light） |
| Android adaptive icon | background 用底色，foreground 可从 1024 裁 |
| README 头图 | `readme-banner.png` |

生成日期：2026-07-29，源稿为 Seedream 生成 + PIL 裁切缩放。
