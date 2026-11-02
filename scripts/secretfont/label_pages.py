#!/usr/bin/env python3
"""字形标签 v3：栅格粗筛 + 轮廓倒角距离精排。"""
import base64, json, re, struct, sys, time
from pathlib import Path
from PIL import Image, ImageChops, ImageDraw, ImageFont, ImageStat

ROOT = Path(__file__).resolve().parents[2]
PROBE = ROOT / ".local" / "probe"
REF = "/usr/share/fonts/opentype/noto/NotoSansCJK-DemiLight.ttc"
REF_INDEX = 2
EM = 200
GRID = 32
SHORTLIST = 40

_page_fonts = {}
_reference = None


def cmap_of(data):
    count = struct.unpack_from(">H", data, 4)[0]
    tables = {}
    for index in range(count):
        offset = 12 + index * 16
        tag = data[offset:offset + 4].decode("latin1")
        _, start, length = struct.unpack_from(">III", data, offset + 4)
        tables[tag] = (start, length)
    start, _ = tables["cmap"]
    total = struct.unpack_from(">H", data, start + 2)[0]
    sub = None
    for index in range(total):
        platform, encoding, offset = struct.unpack_from(">HHI", data, start + 4 + index * 8)
        if platform == 3 and encoding == 1 and struct.unpack_from(">H", data, start + offset)[0] == 4:
            sub = start + offset
    segx2 = struct.unpack_from(">H", data, sub + 6)[0]
    segments = segx2 // 2
    ends = struct.unpack_from(">%dH" % segments, data, sub + 14)
    starts = struct.unpack_from(">%dH" % segments, data, sub + 16 + segx2)
    deltas = struct.unpack_from(">%dh" % segments, data, sub + 16 + 2 * segx2)
    ranges = struct.unpack_from(">%dH" % segments, data, sub + 16 + 3 * segx2)
    mapping = {}
    for index in range(segments):
        if starts[index] == 0xFFFF:
            continue
        for code in range(starts[index], ends[index] + 1):
            if ranges[index] == 0:
                gid = (code + deltas[index]) & 0xFFFF
            else:
                gid = struct.unpack_from(">H", data, sub + 16 + 3 * segx2 + index * 2 + ranges[index] + (code - starts[index]) * 2)[0]
            if gid:
                mapping[chr(code)] = gid
    return mapping


def render_em(font, char):
    canvas = Image.new("L", (EM, EM), 0)
    ImageDraw.Draw(canvas).text((EM // 2, int(EM * 0.8)), char, font=font, fill=255, anchor="ms")
    return canvas.resize((GRID, GRID), Image.LANCZOS)


def candidates():
    items = [chr(code) for code in range(0x20, 0x7F)]
    for high in range(0xB0, 0xF8):
        for low in range(0xA1, 0xFF):
            try:
                items.append(bytes([high, low]).decode("gb2312"))
            except Exception:
                pass
    return list(dict.fromkeys(items))


def reference():
    global _reference
    if _reference is None:
        from fontTools.ttLib import TTFont
        from fontTools.pens.recordingPen import RecordingPen
        font = TTFont(REF, fontNumber=REF_INDEX, lazy=True)
        glyphset = font.getGlyphSet()
        cmap = font.getBestCmap()
        image_font = ImageFont.truetype(REF, EM, index=REF_INDEX)
        entries = []
        for char in candidates():
            name = cmap.get(ord(char))
            if not name:
                continue
            pen = RecordingPen()
            glyphset[name].draw(pen)
            outline = flatten_recording(pen.value)
            if len(outline) < 3:
                continue
            image = render_em(image_font, char)
            entries.append((char, outline, image))
        _reference = entries
    return _reference


def flatten_recording(value, steps=6):
    points = []
    current = None
    for op, args in value:
        if op == "moveTo":
            current = args[0]
            points.append(current)
        elif op == "lineTo":
            points.append(args[0])
            current = args[0]
        elif op == "qCurveTo":
            start = current
            for control in args[:-1]:
                for step in range(1, steps):
                    t = step / steps
                    points.append((
                        (1 - t) ** 2 * start[0] + 2 * (1 - t) * t * control[0] + t * t * args[-1][0],
                        (1 - t) ** 2 * start[1] + 2 * (1 - t) * t * control[1] + t * t * args[-1][1],
                    ))
            points.append(args[-1])
            current = args[-1]
        elif op == "curveTo":
            points.append(args[-1])
            current = args[-1]
    return points


def page_outline(data, gid, depth=0):
    if depth > 5:
        return []
    count = struct.unpack_from(">H", data, 4)[0]
    tables = {}
    for index in range(count):
        offset = 12 + index * 16
        tag = data[offset:offset + 4].decode("latin1")
        _, start, length = struct.unpack_from(">III", data, offset + 4)
        tables[tag] = (start, length)
    head = tables["head"][0]
    fmt = struct.unpack_from(">h", data, head + 50)[0]
    total = struct.unpack_from(">H", data, tables["maxp"][0] + 4)[0]
    locs = struct.unpack_from(">%d%s" % (total + 1, "I" if fmt else "H"), data, tables["loca"][0])
    glyf = tables["glyf"][0]
    start = glyf + locs[gid] * (1 if fmt else 2)
    end = glyf + locs[gid + 1] * (1 if fmt else 2)
    if end <= start:
        return []
    d = data[start:end]
    ncont = struct.unpack_from(">h", d, 0)[0]
    if ncont < 0:
        return composite_outline(data, d, depth)
    if ncont == 0:
        return []
    ends = struct.unpack_from(">%dh" % ncont, d, 10)
    npts = ends[-1] + 1
    pos = 10 + 2 * ncont
    ilen = struct.unpack_from(">H", d, pos)[0]
    pos += 2 + ilen
    flags = []
    while len(flags) < npts:
        f = d[pos]
        pos += 1
        flags.append(f)
        if f & 8:
            rep = d[pos]
            pos += 1
            flags.extend([f] * rep)
    xs, value = [], 0
    for f in flags:
        if f & 2:
            delta = d[pos]
            pos += 1
            value += delta if f & 16 else -delta
        elif not f & 16:
            value += struct.unpack_from(">h", d, pos)[0]
            pos += 2
        xs.append(value)
    ys, value = [], 0
    for f in flags:
        if f & 4:
            delta = d[pos]
            pos += 1
            value += delta if f & 32 else -delta
        elif not f & 32:
            value += struct.unpack_from(">h", d, pos)[0]
            pos += 2
        ys.append(value)
    points = []
    idx = 0
    for contour_end in ends:
        contour = [(xs[i], ys[i], flags[i] & 1) for i in range(idx, contour_end + 1)]
        idx = contour_end + 1
        for i, (x0, y0, on0) in enumerate(contour):
            x1, y1, on1 = contour[(i + 1) % len(contour)]
            points.append((x0, y0))
            if not on0:
                mx, my = (x0 + x1) / 2, (y0 + y1) / 2
                points.append((mx, my))
    return points


def composite_outline(data, d, depth):
    """复合字形：按组件偏移/缩放递归展开。"""
    position = 10
    points = []
    while position + 4 <= len(d):
        flags = struct.unpack_from(">H", d, position)[0]
        component = struct.unpack_from(">H", d, position + 2)[0]
        position += 4
        if flags & 0x0001:
            dx, dy = struct.unpack_from(">hh", d, position)
            position += 4
        else:
            dx, dy = struct.unpack_from(">bb", d, position)
            position += 2
        scale = (0x4000, 0, 0, 0x4000)
        if flags & 0x0008:
            value = struct.unpack_from(">h", d, position)[0]
            position += 2
            scale = (value, 0, 0, value)
        elif flags & 0x0040:
            sx, sy = struct.unpack_from(">hh", d, position)
            position += 4
            scale = (sx, 0, 0, sy)
        elif flags & 0x0080:
            a, b, c, e = struct.unpack_from(">hhhh", d, position)
            position += 8
            scale = (a, b, c, e)
        for x, y in page_outline(data, component, depth + 1):
            nx = (x * scale[0] + y * scale[2]) / 0x4000 + dx
            ny = (x * scale[1] + y * scale[3]) / 0x4000 + dy
            points.append((nx, ny))
        if not flags & 0x0020:
            break
    return points


def chamfer(a, b, limit=180):
    sa = a[:: max(1, len(a) // limit)]
    sb = b[:: max(1, len(b) // limit)]
    total = 0.0
    for x, y in sa:
        total += min((x - u) ** 2 + (y - v) ** 2 for u, v in sb) ** 0.5
    for x, y in sb:
        total += min((x - u) ** 2 + (y - v) ** 2 for u, v in sa) ** 0.5
    return total / (len(sa) + len(sb))


def label_page(path):
    html = Path(path).read_text(encoding="utf-8")
    match = re.search(r"base64,(.*?)'\)", html, re.S)
    if not match:
        return None
    data = base64.b64decode(match.group(1))
    font_path = PROBE / (Path(path).stem + ".ttf")
    font_path.write_bytes(data)
    mapping = cmap_of(data)
    image_font = ImageFont.truetype(str(font_path), EM)
    refs = reference()
    labels = {}
    for char, gid in mapping.items():
        target = render_em(image_font, char)
        ink = sum(target.getdata())
        if ink == 0:
            continue
        shortlist = []
        for ref_char, ref_outline, ref_image in refs:
            diff = ImageStat.Stat(ImageChops.difference(target, ref_image)).sum[0]
            shortlist.append((diff, ref_char, ref_outline))
        shortlist.sort(key=lambda item: item[0])
        outline = page_outline(data, gid)
        if len(outline) < 3:
            continue
        scored = []
        for _, ref_char, ref_outline in shortlist[:SHORTLIST]:
            scored.append((chamfer(outline, ref_outline), ref_char))
        scored.sort()
        best = scored[0]
        second = scored[1] if len(scored) > 1 else (best[0] * 3, "")
        labels[char] = {"label": best[1], "score": round(best[0], 2), "margin": round(second[0] - best[0], 2)}
    return {"html": str(Path(path).resolve()), "font": str(font_path), "labels": labels}


def main():
    outputs = []
    for path in sys.argv[1:]:
        started = time.time()
        result = label_page(path)
        if result is None:
            continue
        outputs.append(result)
        print(f"{Path(path).name}: {len(result['labels'])} 字形 {time.time()-started:.1f}s", flush=True)
    out = PROBE / "secretfont-labels-v3.json"
    out.write_text(json.dumps(outputs, ensure_ascii=False), encoding="utf-8")
    print("已写入", out)


if __name__ == "__main__":
    main()
