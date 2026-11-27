"""Build and prepare the VideoToolbox frame backend for the iOS device slice."""

import hashlib
import json
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import sys
from urllib.parse import unquote, urlparse

from stage_test_app import prepare_framework_binary

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]
UPSTREAM = '4286f5557bdccc0747030e3c376ce5cd160a96a0'
TARGET = 'build/intermediate/mpv_ios-arm64-video'


def run(*args, **kwargs):
    subprocess.run(args, check=True, **kwargs)


def edit(source, filename, before, after):
    path = source / filename
    text = path.read_text()
    if text.count(before) != 1:
        raise RuntimeError(f'Unexpected upstream source in {filename}')
    path.write_text(text.replace(before, after, 1))


def build() -> Path:
    work = ROOT / 'native/build/ios/mpv'
    result = work / 'Mpv'
    work.mkdir(parents=True, exist_ok=True)
    packages = json.loads((ROOT / '.dart_tool/package_config.json').read_text())
    package = next(p for p in packages['packages'] if p['name'] == 'media_kit_libs_ios_video')
    uri = urlparse(package['rootUri'])
    package_root = Path(unquote(uri.path)) if uri.scheme == 'file' else ROOT / '.dart_tool' / unquote(uri.path)
    original = package_root / 'ios/Frameworks/Mpv.xcframework/ios-arm64/Mpv.framework/Mpv'
    digest = hashlib.sha256()
    for path in (Path(__file__), HERE / 'apply.py', HERE / 'pixelbuffer.inc',
                 HERE / 'stage_test_app.py', original):
        digest.update(path.read_bytes())
    digest.update(subprocess.check_output(['xcodebuild', '-version']))
    key = digest.hexdigest()
    stamp = work / 'stamp'
    if result.is_file() and stamp.is_file() and stamp.read_text() == key:
        return result

    tools = {name: shutil.which(name) for name in ('meson', 'ninja', 'cmake', 'pkgconf')}
    missing = [name for name, path in tools.items() if path is None]
    if missing:
        raise SystemExit('缺少 iOS libmpv 构建工具：' + ', '.join(missing))

    source = work / 'source'
    if not source.exists():
        run('git', 'clone', '--depth', '1', '--branch', 'v0.6.0',
            'https://github.com/media-kit/libmpv-darwin-build.git', str(source))
    revision = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=source, text=True).strip()
    if revision != UPSTREAM:
        raise SystemExit(f'iOS libmpv 构建器版本不符：{revision}')
    run('git', 'restore', '--', 'Makefile', 'cross-files', 'scripts', cwd=source)

    edit(source, 'Makefile', 'BINARIES="meson ninja cmake nasm"',
         'BINARIES="meson ninja cmake"')
    edit(source, 'cross-files/ios-arm64.ini', "pkgconfig = ['pkg-config']",
         "pkgconfig = ['pkg-config']\ncmake = ['cmake']")
    sdk_path = subprocess.check_output(
        ['xcrun', '--sdk', 'iphoneos', '--show-sdk-path'], text=True).strip()
    cross_file = source / 'cross-files/ios-arm64.ini'
    cross_file.write_text(cross_file.read_text().replace(
        '/Applications/Xcode.app/Contents/Developer/Platforms/iPhoneOS.platform/Developer/SDKs/iPhoneOS.sdk',
        sdk_path))
    edit(source, 'scripts/mpv/build.sh', 'patches/mpv-fix-missing-objc.patch',
         'patches/mpv-fix-missing-objc.patch\n' +
         shlex.join([sys.executable, str(HERE / 'apply.py')]) + ' "$SRC_DIR"')
    edit(source, 'scripts/libxml2/meson.build', 'configure_options : [',
         "configure_options : [\n        '--without-lzma',")
    edit(source, 'scripts/mbedtls/build.sh',
         "find . -type f -name '*.dylib' -exec sh -c 'mv {} dist/lib' \\;",
         "find build_cross/subprojects/mbedtls -maxdepth 1 -name '*.dylib' -exec cp -P {} dist/lib \\;\n"
         "for lib in mbedtls mbedcrypto mbedx509; do\n"
         "    if [ ! -e \"dist/lib/lib${lib}.dylib\" ]; then\n"
         "        ver=$(cd dist/lib && ls lib${lib}.*.dylib | head -n 1)\n"
         "        ln -s \"$ver\" \"dist/lib/lib${lib}.dylib\"\n"
         "    fi\n"
         "done")
    edit(source, 'scripts/uchardet/meson.build', 'options.add_cmake_defines({',
         "options.add_cmake_defines({\n    'CMAKE_POLICY_VERSION_MINIMUM': '3.5',\n    'CMAKE_BUILD_TYPE': 'Release',")
    edit(source, 'scripts/uchardet/build.sh', 'cmake ./subprojects/uchardet',
         'export CMAKE_POLICY_VERSION_MINIMUM=3.5\ncmake ./subprojects/uchardet')
    edit(source, 'scripts/uchardet/build.sh',
         "cp 'build_cross/subprojects/uchardet/liblibuchardet.dylib' \"$DYLIB_FILE\"",
         "CROSS_DYLIB=$(find build_cross/subprojects/uchardet -maxdepth 1 -type f -name 'liblibuchardet*.dylib' | head -n 1)\n"
         "cp \"$CROSS_DYLIB\" \"$DYLIB_FILE\"")
    edit(source, 'scripts/harfbuzz/build.sh', 'cd ${SRC_DIR}',
         'cd ${SRC_DIR}\nsed -i "" "s/sincosf (/__sincosf (/g" src/OT/glyf/VarCompositeGlyph.hh')
    edit(source, 'scripts/freetype/build.sh', '-Dpng=enabled', '-Dpng=disabled')

    # Use the host executable with isolated target package metadata.
    wrapper = source / 'guoapp-pkgconfig/bin/pkg-config'
    wrapper.parent.mkdir(parents=True, exist_ok=True)
    wrapper.write_text('#!/bin/sh\nexport PKG_CONFIG_LIBDIR=/nonexistent\nexec '
                       + shlex.quote(tools['pkgconf']) + ' "$@"\n')
    wrapper.chmod(0o755)
    build_links = source / 'build/intermediate/links'
    if build_links.exists():
        shutil.rmtree(build_links)
    intermediate = source / TARGET
    if intermediate.exists():
        shutil.rmtree(intermediate)
    make = ('make', f'PROJECT_DIR={source}', 'PKGCONFIG_DIR=guoapp-pkgconfig')
    run(*make, 'build/intermediate/links', cwd=source)
    run(*make, TARGET, cwd=source)
    temporary = result.with_suffix('.tmp')
    prepare_framework_binary(original, intermediate / 'lib/libmpv.2.dylib', temporary)
    os.replace(temporary, result)
    stamp.write_text(key)
    return result


if __name__ == '__main__':
    print(build())
