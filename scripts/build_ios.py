import argparse
import hashlib
import os
import platform
import re
import shlex
import shutil
import subprocess
import sys
import time
import urllib.request
import zipfile
from pathlib import Path

from app_build import BuildVariant, add_variant_argument

root = Path(__file__).resolve().parents[1]

# CI 上从 GitHub Release 下载 libmpv 偶发拿到不完整响应（几十字节），
# media_kit 的 Makefile 因此校验失败、Pod 缺 Mpv.framework 后链接报错。
# 这里在 pod install 之前把归档准备好，并做重试与校验。
_MPV_ATTEMPTS = 5


def run(arguments, **kwargs):
    subprocess.run(arguments, cwd=kwargs.pop('cwd', root), check=True, **kwargs)


def _mpv_archive_spec(makefile):
    version = re.search(r'^MPV_XCFRAMEWORKS_VERSION=(\S+)', makefile, re.MULTILINE)
    checksum = re.search(r'^MPV_XCFRAMEWORKS_SHA256SUM=(\S+)', makefile, re.MULTILINE)
    url = re.search(r'curl\s+-L[^\n]*\n\s*(\S+)', makefile)
    if not version or not checksum or not url:
        raise SystemExit('无法解析 media_kit_libs_ios_video 的 libmpv 下载配置。')
    address = url.group(1).strip('`\'"').replace('${MPV_XCFRAMEWORKS_VERSION}', version.group(1))
    return version.group(1), checksum.group(1), address


def _download_archive(url, checksum, destination):
    for attempt in range(1, _MPV_ATTEMPTS + 1):
        temporary = destination.with_suffix('.tmp')
        try:
            request = urllib.request.Request(url, headers={'User-Agent': 'guoapp-build'})
            with urllib.request.urlopen(request, timeout=60) as response:
                body = response.read()
            digest = hashlib.sha256(body).hexdigest()
            if digest != checksum:
                raise ValueError(f'校验不匹配（收到 {len(body)} 字节）')
            temporary.write_bytes(body)
            temporary.replace(destination)
            return
        except Exception as error:  # noqa: BLE001 - 下载失败一律重试
            print(f'libmpv 下载第 {attempt} 次失败：{error}', flush=True)
            temporary.unlink(missing_ok=True)
            if attempt == _MPV_ATTEMPTS:
                raise SystemExit('libmpv 归档下载失败，请检查网络后重试。') from error
            time.sleep(attempt * 3)


def prepare_libmpv():
    if not (root / 'pubspec.lock').is_file():
        raise SystemExit('缺少 pubspec.lock。')
    hosted = Path(os.environ.get('PUB_CACHE') or Path.home() / '.pub-cache') / 'hosted'
    directories = list(hosted.glob('*/media_kit_libs_ios_video-*'))
    if not directories:
        raise SystemExit('未找到 media_kit_libs_ios_video 包，请先执行 flutter pub get。')
    lock = (root / 'pubspec.lock').read_text(encoding='utf-8')
    pinned = re.search(r'^  media_kit_libs_ios_video:\n(?:    .*\n)*?    version:\s*"?([^"\n]+)"?',
                       lock, re.MULTILINE)
    if pinned:
        matched = [path for path in directories
                   if path.name.endswith('-' + pinned.group(1))]
        directories = matched or directories
    package = directories[0]
    makefile = (package / 'ios' / 'Makefile').read_text(encoding='utf-8')
    version, checksum, url = _mpv_archive_spec(makefile)
    archive = package / 'ios' / '.cache' / 'xcframeworks' / (
        f'libmpv-xcframeworks-{version}-ios-universal.tar.gz')
    if archive.is_file() and hashlib.sha256(archive.read_bytes()).hexdigest() == checksum:
        return
    archive.parent.mkdir(parents=True, exist_ok=True)
    print(f'预下载 libmpv {version} 归档', flush=True)
    _download_archive(url, checksum, archive)


def validate_unsigned_application(application):
    executable = application / 'Runner'
    if not executable.is_file() or not os.access(executable, os.X_OK):
        raise SystemExit('iOS 包缺少可执行的 Runner。')
    run(['xcrun', 'lipo', str(executable), '-verify_arch', 'arm64'])
    if (application / 'embedded.mobileprovision').exists():
        raise SystemExit('未签名 iOS 包不应包含 provisioning profile。')
    signature = subprocess.run(['codesign', '-d', str(application)],
                               capture_output=True, text=True)
    if signature.returncode == 0 or 'not signed at all' not in signature.stderr:
        raise SystemExit('Runner.app 未通过无签名检查：' + signature.stderr)


def validate_package(package, prefix):
    with zipfile.ZipFile(package) as archive:
        for name in [prefix + '/Runner', prefix + '/Info.plist']:
            if name not in archive.namelist() or archive.getinfo(name).file_size == 0:
                raise SystemExit('iOS 压缩包缺少必要文件：' + name)
        corrupt = archive.testzip()
        if corrupt:
            raise SystemExit('iOS 压缩包损坏：' + corrupt)


def write_variant_config(variant):
    display = variant.display_name
    config = root / 'ios' / 'Flutter' / 'AppVariant.xcconfig'
    config.write_text(f'PRODUCT_BUNDLE_IDENTIFIER = {variant.bundle_identifier}\n'
                      f'APP_DISPLAY_NAME = {display}\n', encoding='utf-8')


def build_core(simulator=False, variant=BuildVariant()):
    if platform.system() != 'Darwin':
        raise SystemExit('iOS 构建需要 macOS 和完整 Xcode。')
    go = shutil.which('go')
    if not go:
        raise SystemExit('请安装 Go 1.24.1 或更新版本。')
    environment = os.environ.copy()
    environment.setdefault('GOPROXY', 'https://goproxy.cn,direct')
    environment.setdefault('GOSUMDB', 'off')
    environment['CGO_ENABLED'] = '1'
    environment['GOTOOLCHAIN'] = os.environ.get('GOTOOLCHAIN', 'auto')
    slices = [('iphoneos', 'arm64', 'arm64-apple-ios15.1')]
    if simulator:
        slices.extend([
            ('iphonesimulator', 'arm64', 'arm64-apple-ios15.1-simulator'),
            ('iphonesimulator', 'amd64', 'x86_64-apple-ios15.1-simulator'),
        ])
    libraries = []
    for sdk, architecture, triple in slices:
        sdk_path = subprocess.check_output(['xcrun', '--sdk', sdk, '--show-sdk-path'], text=True).strip()
        compiler = subprocess.check_output(['xcrun', '--sdk', sdk, '--find', 'clang'], text=True).strip()
        directory = root / 'native' / 'build' / 'ios' / f'{sdk}-{architecture}'
        headers = directory / 'Headers'
        headers.mkdir(parents=True, exist_ok=True)
        output = directory / 'libDuanjuCore.a'
        flags = shlex.join(['-isysroot', sdk_path, '-target', triple])
        build_env = environment | {
            'GOOS': 'ios', 'GOARCH': architecture, 'CC': compiler,
            'CGO_CFLAGS': flags, 'CGO_LDFLAGS': flags,
        }
        run([go, 'build', '-trimpath', '-buildmode=c-archive', '-ldflags=' + variant.linker_flags,
             '-o', str(output), './bridge'], cwd=root / 'native', env=build_env)
        shutil.copy2(output.with_suffix('.h'), headers / 'DuanjuCore.h')
        libraries.append((sdk, output, headers))
    arguments = ['xcodebuild', '-create-xcframework']
    device = libraries[0]
    arguments.extend(['-library', str(device[1]), '-headers', str(device[2])])
    if simulator:
        universal = root / 'native' / 'build' / 'ios' / 'simulator-universal'
        universal.mkdir(parents=True, exist_ok=True)
        archive = universal / 'libDuanjuCore.a'
        run(['xcrun', 'lipo', '-create', str(libraries[1][1]), str(libraries[2][1]), '-output', str(archive)])
        arguments.extend(['-library', str(archive), '-headers', str(libraries[1][2])])
    framework = root / 'ios' / 'DuanjuCore' / 'DuanjuCore.xcframework'
    if framework.exists():
        shutil.rmtree(framework)
    run(arguments + ['-output', str(framework)])


def main():
    parser = argparse.ArgumentParser(description='构建红果鉴 / 全站源版 iOS 核心和应用')
    parser.add_argument('--core-only', action='store_true')
    parser.add_argument('--simulator', action='store_true', help='额外生成模拟器核心；不启动模拟器')
    parser.add_argument('--export-options', type=Path, help='使用自己的 Xcode 签名配置导出 IPA')
    add_variant_argument(parser)
    options = parser.parse_args()
    variant = BuildVariant(options.all_sources)
    write_variant_config(variant)
    build_core(options.simulator, variant)
    if options.core_only:
        return
    flutter = shutil.which('flutter')
    if not flutter or not shutil.which('pod'):
        raise SystemExit('请安装 Flutter 和 CocoaPods。')
    run([flutter, 'pub', 'get', '--enforce-lockfile'])
    prepare_libmpv()
    run(['pod', 'install'], cwd=root / 'ios')
    output = root / 'dist' / 'ios'
    output.mkdir(parents=True, exist_ok=True)
    version = re.search(r'^version:\s*(\S+)', (root / 'pubspec.yaml').read_text(), re.MULTILINE).group(1)
    artifacts = []
    if options.export_options:
        config = options.export_options.expanduser().resolve()
        if not config.is_file():
            raise SystemExit('ExportOptions.plist 不存在。')
        run([flutter, 'build', 'ipa', '--release', '--no-pub', '--export-options-plist', str(config), *variant.flutter_arguments])
        for package in (root / 'build' / 'ios' / 'ipa').glob('*.ipa'):
            destination = output / f'{variant.slug}-{version}-ios.ipa'
            shutil.copy2(package, destination)
            artifacts.append(destination)
    else:
        run([flutter, 'build', 'ios', '--release', '--no-codesign', '--no-pub', *variant.flutter_arguments])
        application = root / 'build' / 'ios' / 'iphoneos' / 'Runner.app'
        validate_unsigned_application(application)
        symbols = subprocess.check_output(['xcrun', 'nm', '-gU', str(application / 'Runner')], text=True)
        for symbol in ['_DuanjuRequest', '_DuanjuFree']:
            if symbol not in symbols:
                raise SystemExit('iOS 包缺少 FFI 入口：' + symbol)
        payload = output / 'Payload'
        if payload.exists():
            shutil.rmtree(payload)
        payload.mkdir(parents=True)
        shutil.copytree(application, payload / application.name, symlinks=True)
        ipa = output / f'{variant.slug}-{version}-ios-unsigned.ipa'
        if ipa.exists():
            ipa.unlink()
        run(['ditto', '-c', '-k', '--sequesterRsrc', '--keepParent', str(payload), str(ipa)])
        validate_package(ipa, 'Payload/Runner.app')
        shutil.rmtree(payload)
        artifacts.append(ipa)
    if not artifacts:
        raise SystemExit('未生成 iOS 安装包。')
    for artifact in artifacts:
        print(artifact)


if __name__ == '__main__':
    main()
