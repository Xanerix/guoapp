import argparse
import os
import platform
import plistlib
import re
import shutil
import subprocess
import sys
import zipfile
from pathlib import Path

from app_build import app

root = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser()
parser.add_argument('--platform', choices=['android', 'windows', 'macos', 'linux'], required=True)
parser.add_argument('--abi', action='append', choices=['arm64-v8a', 'armeabi-v7a', 'x86_64'])
parser.add_argument('--identity', default='',
                    help='macOS 签名身份；默认 ad-hoc 签名（-），有 Developer ID 时传证书名')
options = parser.parse_args()
match = re.search(r'^version:\s*([\w.+-]+)\s*$', (root / 'pubspec.yaml').read_text(encoding='utf-8'), re.MULTILINE)
if not match:
    raise SystemExit('pubspec.yaml 缺少合法版本号。')
version = match.group(1)
output = root / 'dist' / options.platform
output.mkdir(parents=True, exist_ok=True)
artifacts = []


def run(arguments, cwd=None):
    subprocess.run([str(item) for item in arguments], cwd=cwd, check=True)


def macho_architectures(path):
    result = subprocess.run(['lipo', '-info', str(path)], capture_output=True, text=True)
    if result.returncode != 0:
        raise SystemExit(f'不是 Mach-O 文件：{path}')
    return result.stdout.strip()


def verify_macos_application(application):
    info = plistlib.loads((application / 'Contents' / 'Info.plist').read_bytes())
    executable = application / 'Contents' / 'MacOS' / info['CFBundleExecutable']
    if not executable.is_file() or not os.access(executable, os.X_OK):
        raise SystemExit('macOS 包缺少可执行文件。')
    architectures = macho_architectures(executable)
    if 'arm64' not in architectures or 'x86_64' not in architectures:
        raise SystemExit('macOS 包应同时包含 arm64 和 x86_64：' + architectures)
    symbols = subprocess.run(['nm', '-gU', str(executable)], capture_output=True, text=True).stdout
    for symbol in ['_DuanjuRequest', '_DuanjuFree']:
        if symbol not in symbols:
            raise SystemExit('macOS 包缺少 FFI 入口：' + symbol)
    frameworks = application / 'Contents' / 'Frameworks'
    for required in ['App.framework', 'FlutterMacOS.framework', 'Mpv.framework', 'ffmpegkit.framework']:
        if not (frameworks / required).is_dir():
            raise SystemExit('macOS 包缺少框架：' + required)


def sign_macos_application(application, identity):
    flags = ['codesign', '--force', '--sign', identity or '-']
    if identity and identity != '-':
        flags += ['--options', 'runtime', '--timestamp']
    for framework in sorted((application / 'Contents' / 'Frameworks').glob('*')):
        run(flags + [framework])
    entitlements = root / 'macos' / 'Runner' / 'Release.entitlements'
    run(flags + ['--entitlements', entitlements, application])


def draw_dmg_background(path, display):
    try:
        from PIL import Image, ImageDraw, ImageFont
    except ImportError:
        return None
    width, height = 620, 380
    image = Image.new('RGB', (width, height), '#F5F9FC')
    draw = ImageDraw.Draw(image)
    draw.rectangle((0, 0, width, 96), fill='#E8F1FA')
    arrow = '#B9CBDD'
    draw.line((300, 178, 400, 178), fill=arrow, width=6)
    draw.line((384, 166, 402, 178), fill=arrow, width=6)
    draw.line((384, 190, 402, 178), fill=arrow, width=6)
    font_path = None
    for candidate in ('/System/Library/Fonts/PingFang.ttc',
                      '/System/Library/Fonts/Hiragino Sans GB.ttc',
                      '/System/Library/Fonts/STHeiti Medium.ttc'):
        if Path(candidate).exists():
            font_path = candidate
            break
    title = ImageFont.truetype(font_path, 26) if font_path else ImageFont.load_default()
    hint = ImageFont.truetype(font_path, 15) if font_path else ImageFont.load_default()
    draw.text((width / 2, 48), display, font=title, fill='#3B4552', anchor='mm')
    draw.text((width / 2, 292), f'把「{display}」拖到右侧 Applications 完成安装',
              font=hint, fill='#7A8796', anchor='mm')
    image.save(path)
    return path


def build_macos_dmg(display, staging, staged, target):
    background = draw_dmg_background(staging.parent / 'dmg-background.png', display)
    installed = subprocess.run([sys.executable, '-c', 'import dmgbuild'],
                               capture_output=True).returncode == 0
    if installed:
        settings = staging.parent / 'dmg-settings.py'
        layout = [
            f'volume_name = {display!r}',
            "format = 'UDZO'",
            f'files = [{staged.name!r}]',
            "symlinks = {'Applications': '/Applications'}",
            "default_view = 'icon-view'",
            'icon_size = 128',
            'text_size = 13',
            'window_rect = ((240, 140), (620, 380))',
            f'icon_locations = {{{staged.name!r}: (155, 175), "Applications": (465, 175)}}',
            'show_status_bar = False',
            'show_tab_view = False',
            'show_toolbar = False',
            'show_pathbar = False',
            'show_sidebar = False',
            'arrange_by = None',
        ]
        if background is not None:
            layout.append(f'background = {str(background)!r}')
        settings.write_text('\n'.join(layout) + '\n')
        run([sys.executable, '-m', 'dmgbuild', '-s', settings, display, target], cwd=staging)
    else:
        print('未安装 dmgbuild，退回普通 dmg（没有拖拽安装的窗口布局）；'
              '需要时执行 pip install dmgbuild。', flush=True)
        os.symlink('/Applications', staging / 'Applications')
        run(['hdiutil', 'create', '-volname', display, '-srcfolder', staging, '-ov',
             '-fs', 'HFS+', '-format', 'UDZO', '-imagekey', 'zlib-level=9', target])


def package_macos(application):
    display = app.display_name
    staging = root / 'build' / 'macos-dmg'
    if staging.exists():
        shutil.rmtree(staging)
    staging.mkdir(parents=True)
    staged = staging / f'{display}.app'
    shutil.copytree(application, staged, symlinks=True)
    plist_path = staged / 'Contents' / 'Info.plist'
    info = plistlib.loads(plist_path.read_bytes())
    info['CFBundleDisplayName'] = display
    info['CFBundleName'] = display
    info['CFBundleIdentifier'] = app.bundle_identifier
    plist_path.write_bytes(plistlib.dumps(info))
    sign_macos_application(staged, options.identity)
    verify_macos_application(staged)
    target = output / f'{app.slug}-{version}-macos-universal.dmg'
    if target.exists():
        target.unlink()
    build_macos_dmg(display, staging, staged, target)
    mount = root / 'build' / 'macos-dmg-mount'
    if mount.exists():
        shutil.rmtree(mount)
    mount.mkdir(parents=True)
    try:
        run(['hdiutil', 'attach', target, '-nobrowse', '-readonly', '-mountpoint', mount])
        if not (mount / f'{display}.app').is_dir():
            raise SystemExit('dmg 里缺少应用：' + display)
        if not (mount / 'Applications').is_symlink():
            raise SystemExit('dmg 里缺少 Applications 快捷方式。')
        if not (mount / '.DS_Store').is_file():
            print('警告：dmg 里没有 .DS_Store，双击后不会按预期摆放图标。', flush=True)
    finally:
        subprocess.run(['hdiutil', 'detach', str(mount)], capture_output=True)
    return target


def windows_product_name(path):
    import ctypes
    version = ctypes.windll.version
    size = version.GetFileVersionInfoSizeW(str(path), None)
    if not size:
        return ''
    data = ctypes.create_string_buffer(size)
    version.GetFileVersionInfoW(str(path), 0, size, data)
    value = ctypes.c_wchar_p()
    length = ctypes.c_uint()
    version.VerQueryValueW(data, r'\StringFileInfo\040904e4\ProductName', ctypes.byref(value), ctypes.byref(length))
    return value.value or ''


def find_inno_compiler():
    candidates = [shutil.which('iscc')]
    for variable, folder in (('LOCALAPPDATA', 'Programs'), ('ProgramFiles(x86)', ''), ('ProgramFiles', '')):
        if os.environ.get(variable):
            candidates.append(str(Path(os.environ[variable]) / folder / 'Inno Setup 6' / 'ISCC.exe'))
    for candidate in candidates:
        if candidate and Path(candidate).is_file():
            return candidate
    raise SystemExit('找不到 Inno Setup 6 的 ISCC.exe，请先安装：winget install JRSoftware.InnoSetup')


def package_windows_installer(bundle, bundle_executable):
    base_name = f'{app.slug}-{version}-windows-x64-setup'
    run([find_inno_compiler(), '/Q',
         f'/DAppId={app.bundle_identifier}',
         f'/DAppName={app.display_name}',
         f'/DAppVersion={version}',
         f'/DNumericVersion={version.replace("+", ".")}',
         f'/DSlug={app.slug}',
         f'/DBundleExecutable={bundle_executable}',
         f'/DSourceDir={bundle}',
         f'/DOutputDir={output}',
         f'/DOutputBaseName={base_name}',
         root / 'windows' / 'installer' / 'setup.iss'])
    target = output / f'{base_name}.exe'
    if not target.is_file():
        raise SystemExit('Windows 安装包没有生成：' + str(target))
    return target


LINUX_SYSTEM_LIB_EXACT = {
    'linux-vdso.so.1',
    'ld-linux-x86-64.so.2',
    'ld-linux-aarch64.so.1',
    'libc.so.6',
    'libm.so.6',
    'libdl.so.2',
    'librt.so.1',
    'libpthread.so.0',
    'libresolv.so.2',
    'libutil.so.1',
    'libanl.so.1',
    'libnsl.so.1',
    'libnss_compat.so.2',
    'libnss_dns.so.2',
    'libnss_files.so.2',
    'libgcc_s.so.1',
    'libstdc++.so.6',
    'libz.so.1',
    'libexpat.so.1',
    'libuuid.so.1',
    'libcom_err.so.2',
    'libgpg-error.so.0',
    'libkeyutils.so.1',
    'libcap.so.2',
    'libmount.so.1',
    'libblkid.so.1',
    'libudev.so.1',
    'libsystemd.so.0',
    'libdbus-1.so.3',
    'libfontconfig.so.1',
    'libfreetype.so.6',
    'libharfbuzz.so.0',
    'libfribidi.so.0',
    'libthai.so.0',
    'libgmp.so.10',
    'libGL.so.1',
    'libGLX.so.0',
    'libGLdispatch.so.0',
    'libEGL.so.1',
    'libGLESv2.so.2',
    'libOpenGL.so.0',
    'libvulkan.so.1',
    'libdrm.so.2',
    'libgbm.so.1',
    'libva.so.2',
    'libva-drm.so.2',
    'libva-wayland.so.2',
    'libva-x11.so.2',
    'libvdpau.so.1',
    'libasound.so.2',
    'libpulse.so.0',
    'libpulse-simple.so.0',
    'libpipewire-0.3.so.0',
    'libjack.so.0',
    'libOpenCL.so.1',
    'libapparmor.so.1',
    'libacl.so.1',
    'libX11.so.6',
    'libX11-xcb.so.1',
    'libxcb.so.1',
    'libXext.so.6',
    'libXfixes.so.3',
    'libXrender.so.1',
    'libXi.so.6',
    'libXrandr.so.2',
    'libXcursor.so.1',
    'libXcomposite.so.1',
    'libXdamage.so.1',
    'libXinerama.so.1',
    'libXss.so.1',
    'libXv.so.1',
    'libXtst.so.6',
    'libICE.so.6',
    'libSM.so.6',
    'libxkbcommon.so.0',
    'libwayland-client.so.0',
    'libwayland-cursor.so.0',
    'libwayland-egl.so.1',
    'libwayland-server.so.0',
    'libgtk-3.so.0',
    'libgdk-3.so.0',
    'libgdk_pixbuf-2.0.so.0',
    'libcairo.so.2',
    'libcairo-gobject.so.2',
    'libpango-1.0.so.0',
    'libpangocairo-1.0.so.0',
    'libpangoft2-1.0.so.0',
    'libatk-1.0.so.0',
    'libatk-bridge-2.0.so.0',
    'libatspi.so.0',
    'libepoxy.so.0',
    'libglib-2.0.so.0',
    'libgobject-2.0.so.0',
    'libgio-2.0.so.0',
    'libgmodule-2.0.so.0',
    'libgthread-2.0.so.0',
    'libpcre.so.3',
    'libpcre2-8.so.0',
    'libffi.so.8',
    'libffi.so.7',
    'libffi.so.6',
    'libselinux.so.1',
    'libssl.so.3',
    'libcrypto.so.3',
    'libgnutls.so.30',
    'libp11-kit.so.0',
    'libkrb5.so.3',
    'libk5crypto.so.3',
    'libgssapi_krb5.so.2',
    'libkrb5support.so.0',
}

LINUX_SYSTEM_LIB_PREFIXES = (
    'libxcb-',
    'libdrm_',
    'libnvidia-',
    'libcuda',
    'libEGL_',
    'libGLX_',
    'libmesa',
    'libgallium',
    'libpulsecommon-',
)


def is_linux_system_library(name):
    return name in LINUX_SYSTEM_LIB_EXACT or name.startswith(LINUX_SYSTEM_LIB_PREFIXES)


def linux_direct_needed(binary):
    result = subprocess.run(['readelf', '-d', str(binary)], capture_output=True, text=True)
    if result.returncode != 0:
        raise SystemExit(f'readelf 检查失败：{binary}\n{result.stderr}')
    return re.findall(r'\(NEEDED\)\s+Shared library:\s+\[([^\]]+)\]', result.stdout)


def linux_ldd_dependencies(binary, lib_dir):
    env = os.environ.copy()
    existing = env.get('LD_LIBRARY_PATH', '')
    env['LD_LIBRARY_PATH'] = f'{lib_dir}:{existing}' if existing else str(lib_dir)
    result = subprocess.run(['ldd', str(binary)], capture_output=True, text=True, env=env)
    if result.returncode != 0:
        raise SystemExit(f'ldd 检查失败：{binary}\n{result.stderr}')
    resolved = {}
    missing = []
    for line in result.stdout.splitlines():
        text = line.strip()
        if '=> not found' in text:
            missing.append(text.split()[0])
            continue
        match = re.match(r'^(\S+)\s+=>\s+(\S+)\s+\(', text)
        if match:
            resolved[match.group(1)] = Path(match.group(2))
    return resolved, missing


def bundle_linux_dependencies(app_bin, lib_dir):
    lib_dir_resolved = lib_dir.resolve()
    queue = [app_bin, *sorted(lib_dir.glob('*.so*'))]
    scanned = set()
    missing_all = {}
    while queue:
        current = queue.pop(0)
        if not current.is_file():
            continue
        key = current.resolve()
        if key in scanned:
            continue
        scanned.add(key)
        direct_needed = set(linux_direct_needed(current))
        resolved, missing = linux_ldd_dependencies(current, lib_dir)
        for name in missing:
            if name in direct_needed and not is_linux_system_library(name):
                missing_all.setdefault(name, []).append(current.name)
        for soname in direct_needed:
            if is_linux_system_library(soname):
                continue
            source = resolved.get(soname)
            if source is None:
                continue
            target = lib_dir / soname
            if target.exists():
                queue.append(target)
                continue
            try:
                source_resolved = source.resolve()
            except OSError:
                continue
            if source_resolved.parent == lib_dir_resolved:
                continue
            if source_resolved.is_file():
                shutil.copy2(source_resolved, target)
                queue.append(target)
    if missing_all:
        details = '; '.join(f'{lib} (由 {", ".join(sorted(set(owners)))} 需要)'
                            for lib, owners in sorted(missing_all.items()))
        raise SystemExit('Linux 打包缺少动态库依赖：' + details)
    patchelf = shutil.which('patchelf')
    if patchelf:
        for library in sorted(lib_dir.glob('*.so*')):
            if library.is_file() and not library.is_symlink():
                subprocess.run([patchelf, '--set-rpath', '$ORIGIN', str(library)],
                               capture_output=True)


def find_appimagetool():
    for name in ('appimagetool', 'appimagetool-x86_64.AppImage'):
        found = shutil.which(name)
        if found:
            return found
    configured = os.environ.get('APPIMAGETOOL', '').strip()
    if configured and Path(configured).is_file():
        return configured
    raise SystemExit('找不到 appimagetool，请先安装并将其加入 PATH，或设置 APPIMAGETOOL 环境变量。')


def package_linux(bundle):
    arch = platform.machine().lower()
    if arch == 'amd64':
        arch = 'x86_64'
    required = [app.slug, 'lib/libduanju_core.so', 'lib/libflutter_linux_gtk.so',
                'lib/libapp.so', 'lib/libffmpegkit.so', 'data/icudtl.dat', 'data/app_icon.png']
    missing = [name for name in required if not (bundle / name).is_file()]
    if missing:
        raise SystemExit('Linux 构建产物缺少文件：' + ', '.join(missing))
    staging = root / 'build' / 'linux-appdir'
    if staging.exists():
        shutil.rmtree(staging)
    app_dir = staging / f'{app.slug}.AppDir'
    usr_bin = app_dir / 'usr' / 'bin'
    shutil.copytree(bundle, usr_bin, symlinks=True)
    bundle_linux_dependencies(usr_bin / app.slug, usr_bin / 'lib')
    mpv_soname = next(
        (p.name for p in sorted((usr_bin / 'lib').glob('libmpv.so.*')) if p.is_file()),
        None,
    )
    if mpv_soname:
        mpv_link = usr_bin / 'lib' / 'libmpv.so'
        if not mpv_link.exists():
            os.symlink(mpv_soname, mpv_link)
    icon_source = usr_bin / 'data' / 'app_icon.png'
    icon_dir = app_dir / 'usr' / 'share' / 'icons' / 'hicolor' / '512x512' / 'apps'
    icon_dir.mkdir(parents=True, exist_ok=True)
    shutil.copy2(icon_source, icon_dir / f'{app.slug}.png')
    shutil.copy2(icon_source, app_dir / f'{app.slug}.png')
    shutil.copy2(icon_source, app_dir / '.DirIcon')
    desktop_content = '\n'.join([
        '[Desktop Entry]',
        'Type=Application',
        f'Name={app.display_name}',
        f'Exec={app.slug}',
        f'Icon={app.slug}',
        'Categories=AudioVideo;Video;Player;',
        'Terminal=false',
        f'StartupWMClass={app.bundle_identifier}',
    ]) + '\n'
    applications_dir = app_dir / 'usr' / 'share' / 'applications'
    applications_dir.mkdir(parents=True, exist_ok=True)
    (applications_dir / f'{app.slug}.desktop').write_text(desktop_content, encoding='utf-8')
    (app_dir / f'{app.slug}.desktop').write_text(desktop_content, encoding='utf-8')
    apprun = app_dir / 'AppRun'
    apprun.write_text('\n'.join([
        '#!/bin/sh',
        'HERE="$(dirname "$(readlink -f "$0")")"',
        'export LD_LIBRARY_PATH="$HERE/usr/bin/lib${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"',
        'export LIBMPV_LIBRARY_PATH="$HERE/usr/bin/lib/libmpv.so"',
        f'exec "$HERE/usr/bin/{app.slug}" "$@"',
    ]) + '\n', encoding='utf-8')
    apprun.chmod(0o755)
    target = output / f'{app.slug}-{version}-linux-{arch}.AppImage'
    if target.exists():
        target.unlink()
    tool = find_appimagetool()
    env = os.environ.copy()
    env['ARCH'] = arch
    env['APPIMAGE_EXTRACT_AND_RUN'] = '1'
    subprocess.run([tool, str(app_dir), str(target)], env=env, check=True)
    target.chmod(0o755)
    return target


if options.platform == 'android':
    for abi in options.abi or ['arm64-v8a', 'armeabi-v7a', 'x86_64']:
        source = root / 'build' / 'app' / 'outputs' / 'flutter-apk' / f'app-{abi}-release.apk'
        if not source.is_file():
            raise SystemExit('缺少 APK：' + str(source))
        with zipfile.ZipFile(source) as archive:
            names = set(archive.namelist())
            required = [f'lib/{abi}/{library}' for library in
                        ['libduanju_core.so', 'libflutter.so', 'libapp.so', 'libmpv.so', 'libffmpegkit.so']]
            missing = set(required) - names
            if missing:
                raise SystemExit('APK 缺少原生库：' + ', '.join(sorted(missing)))
        target = output / f'{app.slug}-{version}-{abi}.apk'
        shutil.copy2(source, target)
        artifacts.append(target)
elif options.platform == 'windows':
    bundle = root / 'build' / 'windows' / 'x64' / 'runner' / 'Release'
    bundle_executable = 'guoapp.exe'
    required = [bundle_executable, 'duanju_core.dll', 'flutter_windows.dll', 'libffmpegkit.dll',
                'libmpv-2.dll', 'vulkan-1.dll', 'msvcp140.dll', 'vcruntime140.dll',
                'data/icudtl.dat', 'data/app.so']
    missing = [name for name in required if not (bundle / name).is_file()]
    if missing:
        raise SystemExit('Windows 安装包缺少文件：' + ', '.join(missing))
    built = windows_product_name(bundle / bundle_executable)
    if built != app.bundle_identifier:
        raise SystemExit(f'构建产物是 {built or "未知版本"}，与要打包的 {app.bundle_identifier} 不符，'
                         '请先用 build_windows.py 以相同参数重新构建。')
    target = output / f'{app.slug}-{version}-windows-x64.zip'
    with zipfile.ZipFile(target, 'w', zipfile.ZIP_DEFLATED) as archive:
        for source in sorted(bundle.rglob('*')):
            if source.is_file():
                relative = source.relative_to(bundle).as_posix()
                if relative == bundle_executable:
                    relative = app.slug + '.exe'
                archive.write(source, relative)
        archive.writestr('userdata/', '')
    artifacts.append(target)
    artifacts.append(package_windows_installer(bundle, bundle_executable))
elif options.platform == 'linux':
    arch = {'x86_64': 'x64', 'amd64': 'x64', 'aarch64': 'arm64', 'arm64': 'arm64'}.get(
        platform.machine().lower(), 'x64'
    )
    bundle = root / 'build' / 'linux' / arch / 'release' / 'bundle'
    if not bundle.is_dir():
        raise SystemExit('缺少 Linux 构建产物：' + str(bundle))
    artifacts.append(package_linux(bundle))
else:
    application = root / 'build' / 'macos' / 'Build' / 'Products' / 'Release' / 'duanju_app.app'
    if not application.is_dir():
        raise SystemExit('缺少 macOS 应用：' + str(application))
    verify_macos_application(application)
    artifacts.append(package_macos(application))

for artifact in artifacts:
    print(artifact)
