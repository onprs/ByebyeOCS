#!/usr/bin/env python3
"""使用 libpq 官方解析器把 PostgreSQL URL/keyword DSN 转换为私有连接文件。

DSN 只从私有文件读取，不进入 argv；service file 不包含密码，密码写入 0600 的
PGPASSFILE。解析失败只返回固定安全文案，不转发可能包含 DSN 片段的 libpq 原文。
"""
import argparse
import ctypes
import ctypes.util
import os


class ConninfoOption(ctypes.Structure):
    _fields_ = [
        ("keyword", ctypes.c_char_p),
        ("envvar", ctypes.c_char_p),
        ("compiled", ctypes.c_char_p),
        ("val", ctypes.c_char_p),
        ("label", ctypes.c_char_p),
        ("dispchar", ctypes.c_char_p),
        ("dispsize", ctypes.c_int),
    ]


def load_libpq():
    candidates = []
    override = os.environ.get("BYEBYEOCS_LIBPQ")
    if override:
        candidates.append(override)
    found = ctypes.util.find_library("pq")
    if found:
        candidates.append(found)
    candidates.extend(["libpq.so.5", "libpq.so"])
    for candidate in candidates:
        try:
            lib = ctypes.CDLL(candidate)
            lib.PQconninfoParse.argtypes = [ctypes.c_char_p, ctypes.POINTER(ctypes.c_char_p)]
            lib.PQconninfoParse.restype = ctypes.POINTER(ConninfoOption)
            lib.PQconninfoFree.argtypes = [ctypes.POINTER(ConninfoOption)]
            lib.PQconninfoFree.restype = None
            lib.PQfreemem.argtypes = [ctypes.c_void_p]
            lib.PQfreemem.restype = None
            return lib
        except OSError:
            continue
    raise RuntimeError("无法加载 libpq；请安装 PostgreSQL 客户端库或设置 BYEBYEOCS_LIBPQ")


def parse_dsn(lib, dsn):
    error = ctypes.c_char_p()
    options = lib.PQconninfoParse(dsn.encode("utf-8"), ctypes.byref(error))
    if not options:
        if error.value:
            lib.PQfreemem(ctypes.cast(error, ctypes.c_void_p))
        # 不读取、不拼接 libpq 原始错误，错误文本可能包含 DSN 片段。
        raise RuntimeError("libpq 无法解析数据库 DSN")
    params = {}
    try:
        index = 0
        while True:
            option = options[index]
            if not option.keyword:
                break
            key = option.keyword.decode("ascii")
            if option.val is not None:
                params[key] = option.val.decode("utf-8", "surrogateescape")
            index += 1
    finally:
        lib.PQconninfoFree(options)
    return params


def write_private(path, content):
    directory = os.path.dirname(path)
    os.makedirs(directory, mode=0o700, exist_ok=True)
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    try:
        with os.fdopen(fd, "w", encoding="utf-8", errors="surrogateescape") as handle:
            handle.write(content)
    except Exception:
        try:
            os.unlink(path)
        finally:
            raise


def pgpass_escape(value):
    return value.replace("\\", "\\\\").replace(":", "\\:")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--dsn-file", required=True)
    parser.add_argument("--service-file", required=True)
    parser.add_argument("--service-name", required=True)
    parser.add_argument("--pgpass-file", required=True)
    args = parser.parse_args()
    if not os.path.isabs(args.dsn_file):
        raise SystemExit("DSN 文件必须是绝对路径")
    try:
        with open(args.dsn_file, "r", encoding="utf-8", errors="surrogateescape") as handle:
            dsn = handle.read().strip()
    except OSError:
        raise SystemExit("无法读取数据库 DSN 文件")
    if not dsn:
        raise SystemExit("数据库 DSN 为空")
    try:
        lib = load_libpq()
        params = parse_dsn(lib, dsn)
    except Exception:
        raise SystemExit("数据库 DSN 解析失败")

    dbname = params.get("dbname", "")
    user = params.get("user", "")
    if not dbname or not user:
        raise SystemExit("数据库 DSN 必须包含 dbname 和 user")
    password = params.get("password", "")
    for key, value in params.items():
        if "\n" in value or "\r" in value:
            raise SystemExit(f"数据库 DSN 参数 {key} 包含换行，拒绝生成连接文件")
    if any(params.get(key) for key in ("passfile", "service", "servicefile")):
        raise SystemExit("数据库 DSN 使用不支持的 passfile/service 参数；请使用明确的 host/port/dbname/user")
    excluded = {"password", "passfile", "service", "servicefile"}
    lines = [f"[{args.service_name}]\n"]
    for key in sorted(params):
        if key in excluded:
            continue
        value = params[key]
        lines.append(f"{key}={value}\n")
    if "application_name" not in params:
        lines.append("application_name=byebyeocs-backup\n")
    if "connect_timeout" not in params:
        lines.append("connect_timeout=10\n")
    write_private(args.service_file, "".join(lines))

    hosts = [value.strip() for value in params.get("host", "*").split(",")]
    if any(not value for value in hosts):
        raise SystemExit("数据库 DSN host 列表包含空项")
    raw_ports = params.get("port", "")
    ports = [] if raw_ports == "" else [value.strip() for value in raw_ports.split(",")]
    if any(not value for value in ports):
        raise SystemExit("数据库 DSN port 列表包含空项")
    if len(ports) == 0:
        ports = ["*"]
    if len(ports) == 1:
        ports = ports * len(hosts)
    elif len(ports) != len(hosts):
        raise SystemExit("数据库 DSN 的 host/port 数量不一致；拒绝猜测密码匹配")
    pgpass_lines = []
    for host, port in zip(hosts, ports):
        # libpq 对 Unix socket 的 pgpass 主机匹配使用 localhost。
        pgpass_host = "localhost" if host.startswith("/") else host
        pgpass_lines.append(":".join(pgpass_escape(value) for value in (pgpass_host, port, dbname, user, password)) + "\n")
    write_private(args.pgpass_file, "".join(pgpass_lines))


if __name__ == "__main__":
    main()
