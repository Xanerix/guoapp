import json
import os
import shutil
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
BUNDLED_DIRECTORY = ROOT / 'native' / 'core' / 'bundled'
MODULE_FILES = ('manifest.json', 'main.js')
ENVIRONMENT_DEFINES = ['PROJECT_URL', 'TELEGRAM_GROUP_URL', 'TELEGRAM_CHANNEL_URL']


class AppBuild:
    slug = 'guoapp'
    display_name = '小果影视'

    @property
    def bundle_identifier(self):
        return os.environ.get('PACKAGE_NAME', '').strip() or 'com.guoapp.app'

    @property
    def flutter_arguments(self):
        arguments = []
        for name in ENVIRONMENT_DEFINES:
            value = os.environ.get(name, '').strip()
            if value:
                arguments.append(f'--dart-define={name}={value}')
        return arguments

    @property
    def go_arguments(self):
        return ['-ldflags=' + self.linker_flags]

    @property
    def linker_flags(self):
        flags = '-s -w'
        update_server = os.environ.get('UPDATE_SERVER_URL', '').strip()
        if update_server:
            flags += ' -X duanjuapp/native/core.nativeUpdateServer=' + update_server
        module_update_server = os.environ.get('MODULE_UPDATE_URL', '').strip()
        if module_update_server:
            flags += ' -X duanjuapp/native/core.nativeModuleUpdateServer=' + module_update_server
        module_public_key = os.environ.get('MODULE_UPDATE_PUBLIC_KEY', '').strip()
        if module_public_key:
            flags += ' -X duanjuapp/native/core.nativeModuleUpdatePublicKey=' + module_public_key
        project = os.environ.get('PROJECT_URL', '').strip()
        if project:
            flags += ' -X duanjuapp/native/core.nativeProjectURL=' + project
        return flags


app = AppBuild()


def prepare_bundled_modules(extra=()):
    BUNDLED_DIRECTORY.mkdir(parents=True, exist_ok=True)
    for child in BUNDLED_DIRECTORY.iterdir():
        if child.name != '.gitkeep':
            if child.is_dir():
                shutil.rmtree(child)
            else:
                child.unlink()
    for item in [*os.environ.get('BUNDLED_MODULES', '').split(','), *extra]:
        if not item.strip():
            continue
        source = Path(item.strip())
        if not source.is_absolute():
            source = ROOT / source
        missing = [name for name in MODULE_FILES if not (source / name).is_file()]
        if missing:
            raise SystemExit(f'内置模块 {source} 缺少 {"、".join(missing)}')
        manifest = json.loads((source / 'manifest.json').read_text(encoding='utf-8'))
        target = BUNDLED_DIRECTORY / manifest['id']
        target.mkdir()
        for name in MODULE_FILES:
            shutil.copy2(source / name, target / name)
        print(f'内置模块：{manifest["id"]} {manifest.get("version", "")}', flush=True)
