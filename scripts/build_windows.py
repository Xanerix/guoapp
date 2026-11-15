import argparse
import os
import shutil
import subprocess
import sys
from pathlib import Path

from app_build import app

root = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser()
options = parser.parse_args()
env = os.environ.copy()
env.setdefault('GOPROXY', 'https://goproxy.cn,direct')
env.setdefault('GOSUMDB', 'off')
flutter = shutil.which('flutter')
if not flutter:
    raise SystemExit('请先将 Flutter SDK 的 bin 目录加入 PATH。')
subprocess.run([sys.executable, str(root / 'scripts' / 'build_native.py'), '--platform', 'windows'],
               cwd=root, env=env, check=True)
subprocess.run([flutter, 'pub', 'get', '--enforce-lockfile'], cwd=root, env=env, check=True)
subprocess.run([flutter, 'build', 'windows', '--release', '--no-pub', *app.flutter_arguments], cwd=root, env=env, check=True)
subprocess.run([sys.executable, str(root / 'scripts' / 'package_release.py'), '--platform', 'windows'],
               cwd=root, env=env, check=True)
