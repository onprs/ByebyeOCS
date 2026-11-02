#!/usr/bin/env python3
"""并行 v3 打标。"""
import json, sys
from multiprocessing import Pool
from pathlib import Path

sys.path.insert(0, str(Path(".local/probe").resolve()))
import font_label3 as base


def work(path):
    try:
        return base.label_page(path)
    except Exception as error:
        return {"html": str(path), "error": str(error), "labels": {}}


def main():
    paths = sys.argv[2:]
    results = []
    with Pool(int(sys.argv[1])) as pool:
        for index, item in enumerate(pool.imap_unordered(work, paths, chunksize=1), 1):
            if item:
                results.append(item)
            if index % 20 == 0:
                print(f"已处理 {index}/{len(paths)}", flush=True)
    out = Path(".local/probe/secretfont-labels-v3.json")
    out.write_text(json.dumps(results, ensure_ascii=False), encoding="utf-8")
    print("已写入", out, "页面", len(results))


if __name__ == "__main__":
    main()
