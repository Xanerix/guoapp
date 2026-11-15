import os

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
        project = os.environ.get('PROJECT_URL', '').strip()
        if project:
            flags += ' -X duanjuapp/native/core.nativeProjectURL=' + project
        return flags


app = AppBuild()
