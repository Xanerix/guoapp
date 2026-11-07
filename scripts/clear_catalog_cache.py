#!/usr/bin/env python3
import argparse
import os
import shutil
import subprocess
import sys
from pathlib import Path

from app_build import BuildVariant

APPS = {
    "zhen": BuildVariant(True),
    "hong": BuildVariant(False),
}


def running(executable: str) -> bool:
    output = subprocess.run(
        ["tasklist", "/FI", f"IMAGENAME eq {executable}", "/NH"],
        capture_output=True,
        text=True,
    ).stdout
    return executable.lower() in output.lower()


def remove(path: Path) -> None:
    if not path.exists():
        return
    if path.is_dir():
        shutil.rmtree(path)
    else:
        path.unlink()
    print(f"已删除 {path}")


def main() -> int:
    parser = argparse.ArgumentParser(description="清空 Windows 版剧库缓存，模拟首次加载")
    parser.add_argument("--app", choices=APPS, default="zhen")
    parser.add_argument("--covers", action="store_true", help="同时清空海报缓存")
    args = parser.parse_args()

    variant = APPS[args.app]
    if running(variant.slug + ".exe"):
        print(f"{variant.display_name} 正在运行，请先退出应用")
        return 1
    directory = Path(os.environ["APPDATA"]) / variant.bundle_identifier
    if not directory.is_dir():
        print(f"找不到数据目录 {directory}")
        return 1

    remove(directory / "catalogs.json")
    remove(directory / "sources.json")
    for temporary in directory.glob(".cache-write-*"):
        remove(temporary)
    if args.covers:
        remove(directory / "covers-v1")
    print("完成，追剧、收藏、设置和下载未改动")
    return 0


if __name__ == "__main__":
    sys.exit(main())
