#!/usr/bin/env python3
"""从统一设计变量生成正式三端使用的 CSS 与 Dart，--check 用于校验产物。"""

import argparse
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT / "design/tokens.json"
FIELD_NAMES = {
    "background": "bg", "surface": "surface", "surfaceSubtle": "surface-subtle",
    "text": "text", "textSecondary": "text-secondary", "muted": "muted",
    "border": "border", "accent": "accent", "accentContrast": "accent-contrast",
    "accentSoft": "accent-soft", "sidebar": "sidebar", "sidebarText": "sidebar-text",
    "sidebarActive": "sidebar-active", "success": "green", "successBg": "green-bg",
    "warning": "amber", "warningBg": "amber-bg", "danger": "red", "dangerBg": "red-bg",
    "heroBg": "hero-bg", "heroText": "hero-text", "chartSecondary": "chart-secondary",
}


def camel(value):
    first, *rest = value.split("-")
    return first + "".join(part.title() for part in rest)


def contrast(foreground, background):
    def luminance(color):
        channels = [int(color[index:index + 2], 16) / 255 for index in (1, 3, 5)]
        linear = [value / 12.92 if value <= .04045 else ((value + .055) / 1.055) ** 2.4 for value in channels]
        return sum(value * weight for value, weight in zip(linear, (.2126, .7152, .0722)))
    low, high = sorted((luminance(foreground), luminance(background)))
    return (high + .05) / (low + .05)


def validate(spec):
    pairs = [
        ("text", "surface"), ("text-secondary", "surface"), ("text-secondary", "bg"),
        ("muted", "surface-subtle"), ("accent", "accent-soft"),
        ("accent-contrast", "accent"), ("sidebar-text", "sidebar"),
        ("sidebar-strong", "sidebar-active"),
        *((name, name + "-bg") for name in ("green", "amber", "red", "blue", "violet")),
    ]
    for scheme, modes in spec["palettes"].items():
        for mode, colors in modes.items():
            for foreground, background in pairs:
                ratio = contrast(colors[foreground], colors[background])
                if ratio < 4.5:
                    raise ValueError(f"{scheme}/{mode} 的 {foreground}/{background} 对比度 {ratio:.2f}，需达到 4.5")


def render(spec):
    validate(spec)
    css = ["/* 由 scripts/generate-design-tokens.py 生成；请修改 design/tokens.json。 */"]
    dart = ["// 由 scripts/generate-design-tokens.py 生成；请修改 design/tokens.json。", "part of 'palette.dart';", ""]
    for group, class_name in (("metrics", "AppMetrics"), ("typography", "AppTypography")):
        dart.extend([f"abstract final class {class_name} {{"])
        for key, value in spec[group].items():
            dart.append(f"  static const double {camel(key)} = {value};")
        dart.extend(["}", ""])
    for scheme, modes in spec["palettes"].items():
        for mode, colors in modes.items():
            selector = ":root"
            if mode == "dark":
                selector += '[data-theme="dark"]'
            if scheme != "graphite":
                selector += f'[data-color-scheme="{scheme}"]'
            css.append(selector + " {")
            if scheme == "graphite" and mode == "light":
                css.extend([
                    '  font-family: Inter, "Noto Sans CJK SC", "PingFang SC", "Microsoft YaHei", sans-serif;',
                    "  font-synthesis: none;", "  text-rendering: optimizeLegibility;",
                    "  -webkit-font-smoothing: antialiased;",
                ])
                for group in ("metrics", "typography"):
                    css.extend(f"  --{key}: {value}px;" for key, value in spec[group].items())
            css.append(f"  color-scheme: {mode};")
            css.extend(f"  --{key}: {value};" for key, value in colors.items())
            if scheme == "graphite":
                css.extend([
                    "  --shadow: 0 8px 24px rgb(0 0 0 / " + (".22);" if mode == "dark" else ".06);"),
                    "  --shadow-menu: 0 12px 32px rgb(0 0 0 / " + (".4);" if mode == "dark" else ".12);"),
                ])
            css.extend(["}", ""])
            dart.append(f"const _{scheme}{mode.title()} = AppPalette(")
            for field, token in FIELD_NAMES.items():
                color = colors[token].removeprefix("#").upper()
                if len(color) != 6:
                    raise ValueError(f"Dart 色值必须为六位十六进制：{token}={color}")
                dart.append(f"  {field}: Color(0xFF{color}),")
            dart.extend(["  radius: AppMetrics.radius,", ");", ""])
    content = "\n".join(css)
    return {
        ROOT / "apps/admin/src/styles/tokens.css": content,
        ROOT / "services/tiku/internal/platform/panel/assets/tokens.css": content,
        ROOT / "apps/client_flutter/lib/design/palette_data.dart": "\n".join(dart),
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    outputs = render(json.loads(SOURCE.read_text(encoding="utf-8")))
    mismatches = []
    for path, content in outputs.items():
        if args.check:
            if not path.exists() or path.read_text(encoding="utf-8") != content:
                mismatches.append(str(path.relative_to(ROOT)))
        else:
            path.write_text(content, encoding="utf-8", newline="\n")
    if mismatches:
        raise SystemExit("设计变量产物需要重新生成：\n" + "\n".join(mismatches))
    print("设计变量产物一致" if args.check else "已生成三端设计变量")


if __name__ == "__main__":
    main()
