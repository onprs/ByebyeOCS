"""将实际浏览器截图排成三方向对照，不改变界面内容。需要 Pillow。"""
from pathlib import Path
from PIL import Image, ImageDraw, ImageFont

BASE = Path(__file__).resolve().parent.parent / "screenshots"
FONT = "/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc"
DIRECTIONS = [("graphite", "01  石墨", "#4969ce"), ("grove", "02  青禾", "#2d7159"), ("indigo", "03  靛蓝", "#6655ca")]


def font(size):
    try:
        return ImageFont.truetype(FONT, size, index=2)
    except OSError:
        return ImageFont.load_default()


def make(topic, title, mobile=False):
    width = 390 if mobile else 500
    pad, gap, header, label, footer = 34, 25, 105, 49, 45
    cards = []
    for key, name, color in DIRECTIONS:
        suffix = topic if mobile else f"{topic}-desktop"
        image = Image.open(BASE / f"{key}-{suffix}.png").convert("RGB")
        if not mobile:
            image = image.resize((width, round(image.height * width / image.width)), Image.Resampling.LANCZOS)
        cards.append((name, color, image))
    card_height = max(image.height for _, _, image in cards)
    sheet = Image.new("RGB", (pad * 2 + width * 3 + gap * 2, header + label + card_height + footer), "#f1f4f0")
    draw = ImageDraw.Draw(sheet)
    draw.text((pad, 23), title, fill="#2d3b31", font=font(24))
    draw.text((pad, 63), "ByebyeOCS  /  默认石墨，三种可选外观方案", fill="#879589", font=font(12))
    for index, (name, color, image) in enumerate(cards):
        x = pad + index * (width + gap)
        draw.rounded_rectangle((x, header, x + width, header + label + card_height), radius=10, fill="white", outline="#dde5dc", width=1)
        draw.ellipse((x + 17, header + 19, x + 24, header + 26), fill=color)
        draw.text((x + 35, header + 12), name, fill="#4b5d4f", font=font(16))
        sheet.paste(image, (x, header + label))
    draw.text((pad, header + label + card_height + 17), "交互原型截图 · 合成数据 · 默认石墨 · 用量单价待公布" + (" · 手机为 React 视觉原型" if mobile else ""), fill="#8d9a8e", font=font(11))
    output = BASE / f"comparison-{topic}.png"
    sheet.save(output, optimize=True)
    print(output.name)


for topic, title in [("admin-dashboard", "管理员 · 运行总览"), ("admin-questions", "管理员 · 题库与模型"), ("client-home", "用户端 · 课程与学习首页"), ("client-plans", "用户端 · 安排与实时进展"), ("client-wallet", "用户端 · 钱包与充值")]:
    make(topic, title)
for topic, title in [("mobile-home", "手机 · 学习首页"), ("mobile-plans", "手机 · 安排与实时进展"), ("mobile-wallet", "手机 · 钱包与充值")]:
    make(topic, title, mobile=True)
