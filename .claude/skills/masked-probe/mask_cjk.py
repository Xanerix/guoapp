import re
import sys

CJK_PATTERN = re.compile(
    "["
    "ᄀ-ᇿ"
    "⺀-鿿"
    "ꥠ-꥿"
    "가-퟿"
    "豈-﫿"
    "︐-﹏"
    "＀-￯"
    "\U00020000-\U0003ffff"
    "]"
)


def mask_text(text):
    return CJK_PATTERN.sub("*", text)


class MaskedStream:
    def __init__(self, stream):
        self._stream = stream

    def write(self, text):
        return self._stream.write(mask_text(text))

    def flush(self):
        self._stream.flush()

    def __getattr__(self, name):
        return getattr(self._stream, name)


def install():
    if not isinstance(sys.stdout, MaskedStream):
        sys.stdout = MaskedStream(sys.stdout)
    if not isinstance(sys.stderr, MaskedStream):
        sys.stderr = MaskedStream(sys.stderr)


def main():
    sys.stdin.reconfigure(encoding="utf-8", errors="replace")
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
    for line in sys.stdin:
        sys.stdout.write(mask_text(line))
        sys.stdout.flush()


if __name__ == "__main__":
    main()
