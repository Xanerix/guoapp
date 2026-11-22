"""Prepare a separate signed GuoApp bundle with an experimental iOS libmpv."""

import argparse
from pathlib import Path
import re
import subprocess


def run(*args):
    return subprocess.run(args, check=True, capture_output=True).stdout


def prepare_framework_binary(original: Path, library: Path, output: Path) -> None:
    if b'guoapp-ios-pixelbuffer' not in library.read_bytes():
        raise SystemExit('The replacement libmpv lacks the pixel-buffer backend')
    run('xcrun', 'lipo', str(library), '-verify_arch', 'arm64')
    original_dependencies = run('otool', '-L', str(original)).decode()
    framework_paths = re.findall(r'\s(@rpath/(\w+)\.framework/\w+)\s', original_dependencies)
    paths_by_name = {name.lower(): path for path, name in framework_paths}
    library_dependencies = run('otool', '-L', str(library)).decode().splitlines()[2:]
    changes = []
    for line in library_dependencies:
        dependency = line.strip().split(' (compatibility version', 1)[0]
        if dependency.startswith(('/System/Library/', '/usr/lib/')):
            continue
        dylib_name = re.fullmatch(r'lib(\w+)\.\d+\.dylib', Path(dependency).name)
        if dylib_name is None:
            raise SystemExit(f'Unexpected dependency: {dependency}')
        framework = paths_by_name.get(dylib_name[1].lower())
        if framework is None:
            raise SystemExit(f'No bundled framework for {dependency}')
        changes.extend(('-change', dependency, framework))

    output.write_bytes(library.read_bytes())
    output.chmod(original.stat().st_mode & 0o777)
    run('install_name_tool', '-id', '@rpath/Mpv.framework/Mpv', *changes, str(output))


def stage(app: Path, library: Path, output: Path, identity: str) -> None:
    if output.exists():
        raise SystemExit(f'Test bundle already exists: {output}')
    run('ditto', str(app), str(output))
    original = app / 'Frameworks/Mpv.framework/Mpv'
    replacement = output / 'Frameworks/Mpv.framework/Mpv'
    prepare_framework_binary(original, library, replacement)
    run('codesign', '--force', '--sign', identity, str(replacement.parent))
    run('codesign', '--force', '--sign', identity,
        '--preserve-metadata=entitlements', str(output))
    run('codesign', '--verify', '--deep', '--strict', str(output))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('app', type=Path)
    parser.add_argument('library', type=Path)
    parser.add_argument('output', type=Path)
    parser.add_argument('--identity', required=True)
    stage(**vars(parser.parse_args()))
