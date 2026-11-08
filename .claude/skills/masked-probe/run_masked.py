import os
import subprocess
import sys
import threading
from pathlib import Path

from mask_cjk import mask_text


def pump(source, target):
    for line in source:
        target.write(mask_text(line))
        target.flush()


def child_env():
    env = dict(os.environ, PYTHONIOENCODING="utf-8", PYTHONUNBUFFERED="1")
    paths = [str(Path(__file__).resolve().parent), env.get("PYTHONPATH", "")]
    env["PYTHONPATH"] = os.pathsep.join(path for path in paths if path)
    return env


def main():
    if len(sys.argv) < 2:
        print("usage: python .claude/skills/masked-probe/run_masked.py <script.py> [args]", file=sys.stderr)
        return 2
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
    sys.stderr.reconfigure(encoding="utf-8", errors="replace")
    process = subprocess.Popen(
        [sys.executable, *sys.argv[1:]],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        encoding="utf-8",
        errors="replace",
        env=child_env(),
    )
    pumps = [
        threading.Thread(target=pump, args=(process.stdout, sys.stdout)),
        threading.Thread(target=pump, args=(process.stderr, sys.stderr)),
    ]
    for thread in pumps:
        thread.start()
    for thread in pumps:
        thread.join()
    return process.wait()


if __name__ == "__main__":
    raise SystemExit(main())
