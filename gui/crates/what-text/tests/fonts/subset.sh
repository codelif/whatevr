#!/bin/sh
# rebuilds the bundled fonts from the system's noto (arch: noto-fonts,
# noto-fonts-cjk, noto-fonts-emoji), cut down to what samples.txt uses. the
# latin faces also keep printable ascii so tools can label things with them.
# the colr test font is googlefonts/color-fonts' test_glyphs-glyf_colr_1.ttf, kept whole.
set -eu
cd "$(dirname "$0")"
noto=/usr/share/fonts/noto
cut() {
    src=$1 out=$2
    shift 2
    pyftsubset "$src" --output-file="$out" --text-file=samples.txt --layout-features='*' \
        --name-IDs='*' --notdef-outline "$@"
}
ascii=--unicodes=U+0020-007E
cut $noto/NotoSans-Regular.ttf NotoSans-Regular.ttf $ascii
cut $noto/NotoSans-Bold.ttf NotoSans-Bold.ttf $ascii
cut $noto/NotoSans-Italic.ttf NotoSans-Italic.ttf $ascii
cut $noto/NotoSansDevanagari-Regular.ttf NotoSansDevanagari-Regular.ttf
cut $noto/NotoNaskhArabic-Regular.ttf NotoNaskhArabic-Regular.ttf
# 2 is the sc face of the collection
cut /usr/share/fonts/noto-cjk/NotoSansCJK-Regular.ttc NotoSansCJKsc-Regular.otf --font-number=2
cut $noto/NotoColorEmoji.ttf NotoColorEmoji.ttf
