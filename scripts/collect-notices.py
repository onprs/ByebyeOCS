#!/usr/bin/env python3
"""收集实际构建依赖已有的许可证和NOTICE，不推断项目本身的许可。"""

import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys

root = Path(__file__).resolve().parents[1]
destination = Path(sys.argv[1]).resolve()
architecture = sys.argv[2]
destination.mkdir(parents=True, exist_ok=True)
records = []


def collect(kind, name, version, directory, metadata_license=None):
    identity = re.sub(r"[^A-Za-z0-9._+-]", "_", name + "@" + version)
    output = destination / kind / identity
    notices = []
    if directory.is_dir():
        for entry in sorted(directory.iterdir()):
            if entry.is_file() and not entry.is_symlink() and entry.name.upper().startswith(("LICENSE", "LICENCE", "COPYING", "NOTICE")):
                output.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(entry, output / entry.name)
                notices.append(str((output / entry.name).relative_to(destination)))
    records.append({"kind": kind, "name": name, "version": version, "declared_license": metadata_license, "notice_files": notices})


def json_stream(content):
    decoder = json.JSONDecoder()
    content = content.lstrip()
    while content:
        value, end = decoder.raw_decode(content)
        yield value
        content = content[end:].lstrip()


environment = os.environ.copy()
environment.update({"CGO_ENABLED": "0", "GOOS": "linux", "GOARCH": architecture})
modules = {}
for module in ("server", "services/tiku"):
    result = subprocess.run(["go", "-C", str(root / module), "list", "-deps", "-json", "./cmd/..."], env=environment, check=True, capture_output=True, text=True)
    for package in json_stream(result.stdout):
        dependency = package.get("Module", {})
        if dependency.get("Main") or not dependency.get("Path"):
            continue
        resolved = dependency.get("Replace", dependency)
        key = (resolved["Path"], resolved.get("Version", "local"))
        if resolved.get("Dir"):
            modules[key] = Path(resolved["Dir"])
for (name, version), directory in sorted(modules.items()):
    collect("go", name, version, directory)

go_root = Path(subprocess.run(["go", "env", "GOROOT"], check=True, capture_output=True, text=True).stdout.strip())
go_version = subprocess.run(["go", "env", "GOVERSION"], check=True, capture_output=True, text=True).stdout.strip()
collect("go", "Go standard library", go_version, go_root)

admin = root / "apps/admin"
lock = json.loads((admin / "package-lock.json").read_text())
for relative, package in sorted(lock.get("packages", {}).items()):
    if not relative.startswith("node_modules/") or package.get("dev"):
        continue
    directory = admin / relative
    package_file = directory / "package.json"
    if not package_file.is_file():
        continue
    metadata = json.loads(package_file.read_text())
    collect("npm", metadata["name"], metadata.get("version", package.get("version", "unknown")), directory, metadata.get("license"))

(destination / "dependencies.json").write_text(json.dumps({"format": 1, "dependencies": records, "flutter_notices": "apps/client-web/assets/NOTICES"}, ensure_ascii=False, indent=2) + "\n")
print("已收集依赖声明：", len(records))
