import os
from dataclasses import dataclass
from pathlib import Path

SOURCES_MODULE = 'guoapp/sources'
NATIVE_ROOT = Path(__file__).resolve().parents[1] / 'native'
ENVIRONMENT_DEFINES = ['PROJECT_URL', 'TELEGRAM_GROUP_URL', 'TELEGRAM_CHANNEL_URL',
                       'ZHENGUOJIAN_DISPLAY_NAME', 'ZHENGUOJIAN_LATIN_NAME']


@dataclass(frozen=True)
class BuildVariant:
    all_sources: bool = False

    @property
    def slug(self):
        return 'zhenguojian' if self.all_sources else 'hongguojian'

    @property
    def bundle_identifier(self):
        if not self.all_sources:
            return 'com.guoapp.phoenix'
        return os.environ.get('ZHENGUOJIAN_PACKAGE_NAME', '').strip() or 'com.guoapp.all'

    @property
    def display_name(self):
        if not self.all_sources:
            return '红果鉴'
        return os.environ.get('ZHENGUOJIAN_DISPLAY_NAME', '').strip() or '真果鉴'

    @property
    def arguments(self):
        return ['--all-sources'] if self.all_sources else []

    @property
    def flutter_arguments(self):
        arguments = ['--dart-define=APP_FLAVOR=' + self.slug]
        for name in ENVIRONMENT_DEFINES:
            value = os.environ.get(name, '').strip()
            if value:
                arguments.append(f'--dart-define={name}={value}')
        return arguments

    @property
    def go_arguments(self):
        return ['-tags=' + self.slug, '-ldflags=' + self.linker_flags]

    @property
    def linker_flags(self):
        flags = '-s -w'
        update_server = os.environ.get('UPDATE_SERVER_URL', '').strip()
        if update_server:
            flags += ' -X duanjuapp/native/core.nativeUpdateServer=' + update_server
        project = os.environ.get('PROJECT_URL', '').strip()
        if project:
            flags += ' -X duanjuapp/native/core.nativeProjectURL=' + project
        return flags


def add_variant_argument(parser):
    parser.add_argument('--all-sources', action='store_true',
                        help='构建包含全部站源的全站源版；默认构建仅红果的红果鉴')


def add_sources_argument(parser):
    parser.add_argument('--sources', type=Path,
                        help='站源仓库路径；接入一次后会记住，之后可省略')


def link_sources(path):
    workspace = NATIVE_ROOT / 'go.work'
    anchor = NATIVE_ROOT / 'bridge' / 'sources_gen.go'
    if path:
        directory = path.expanduser().resolve()
        manifest = directory / 'go.mod'
        declared = manifest.read_text(encoding='utf-8').splitlines() if manifest.is_file() else []
        if f'module {SOURCES_MODULE}' not in (line.strip() for line in declared):
            raise SystemExit(f'站源仓库路径无效，需要模块 {SOURCES_MODULE} 所在目录：' + str(directory))
        workspace.write_text(f'go 1.24.1\n\nuse (\n\t.\n\t{directory.as_posix()}\n)\n', encoding='utf-8')
        anchor.write_text(f'package main\n\nimport _ "{SOURCES_MODULE}"\n', encoding='utf-8')
    if not workspace.is_file() or not anchor.is_file():
        raise SystemExit('尚未接入站源仓库，请加上 --sources <站源仓库路径>。')
