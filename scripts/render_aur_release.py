#!/usr/bin/env python3
"""Patch AUR package templates for a tagged release."""

from __future__ import annotations

import argparse
import re
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]


def replace_once(path: Path, pattern: str, replacement: str) -> None:
    text = path.read_text(encoding="utf-8")
    new, count = re.subn(pattern, replacement, text, count=1, flags=re.MULTILINE)
    if count != 1:
        raise SystemExit(f"{path}: expected one match for {pattern!r}, got {count}")
    path.write_text(new, encoding="utf-8")


def main() -> None:
    parser = argparse.ArgumentParser(description="render AUR packages for release")
    parser.add_argument("--version", required=True, help="release version without leading v")
    parser.add_argument("--source-sha", required=True, help="source tarball sha256")
    parser.add_argument("--bin-sha-x86_64", required=True, help="x86_64 binary tarball sha256")
    args = parser.parse_args()

    source_pkg = ROOT / "packaging/aur/whatevr/PKGBUILD"
    bin_pkg = ROOT / "packaging/aur/whatevr-bin/PKGBUILD"

    replace_once(source_pkg, r"^pkgver=.*$", f"pkgver={args.version}")
    replace_once(source_pkg, r"^sha256sums=\('.*'\)$", f"sha256sums=('{args.source_sha}')")

    replace_once(bin_pkg, r"^pkgver=.*$", f"pkgver={args.version}")
    replace_once(
        bin_pkg,
        r"^sha256sums_x86_64=\('.*'\)$",
        f"sha256sums_x86_64=('{args.bin_sha_x86_64}')",
    )


if __name__ == "__main__":
    main()
