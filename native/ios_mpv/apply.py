"""Add GuoApp's pixel-buffer render backend to an mpv 0.36.0 source tree."""

import argparse
from pathlib import Path
import shutil


def apply(source: Path) -> None:
    target = source / 'video' / 'out' / 'vo_libmpv.c'
    original = target.read_text()
    marker = 'const struct render_backend_fns *render_backends[] = {'
    include = '#include "guoapp_pixelbuffer.inc"'
    if include not in original:
        if original.count(marker) != 1:
            raise SystemExit('Expected the mpv 0.36.0 render backend registry')
        updated = original.replace(
            marker,
            '#include "config.h"\n#if HAVE_IOS_GL\n' + include + '\n#endif\n\n' + marker
            + '\n#if HAVE_IOS_GL\n    &guoapp_pixelbuffer_backend,\n#endif',
        )
        target.write_text(updated)
    shutil.copyfile(Path(__file__).with_name('pixelbuffer.inc'),
                    target.with_name('guoapp_pixelbuffer.inc'))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('source', type=Path)
    apply(parser.parse_args().source)
