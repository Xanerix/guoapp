import os
from dataclasses import dataclass

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
        arguments = ['--dart-define=ALL_SOURCES=' + str(self.all_sources).lower()]
        for name in ENVIRONMENT_DEFINES:
            value = os.environ.get(name, '').strip()
            if value:
                arguments.append(f'--dart-define={name}={value}')
        return arguments

    @property
    def linker_flags(self):
        flags = '-s -w -X duanjuapp/native/core.buildAllSources=' + str(self.all_sources).lower()
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
