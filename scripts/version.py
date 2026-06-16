#!/usr/bin/env python3
"""Resolve whatevr build versions."""

from __future__ import annotations

import argparse
import re
import subprocess
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
SEMVER_PREFIX_RE = re.compile(r"^([0-9]+\.[0-9]+\.[0-9]+)")


def git_describe() -> str | None:
    result = subprocess.run(
        ["git", "-C", str(ROOT), "describe", "--tags", "--dirty", "--always"],
        check=False,
        stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL,
        text=True,
    )
    if result.returncode != 0:
        return None

    version = result.stdout.strip()
    if not version:
        return None
    return version.removeprefix("v")


def version_file() -> str | None:
    path = ROOT / "VERSION"
    if not path.exists():
        return None

    version = path.read_text(encoding="utf-8").strip()
    return version or None


def full_version() -> str:
    return git_describe() or version_file() or "0.0.0-unknown"


def numeric_version() -> str:
    for candidate in (full_version(), version_file()):
        if not candidate:
            continue
        match = SEMVER_PREFIX_RE.match(candidate)
        if match:
            return match.group(1)
    return "0.0.0"


def main() -> None:
    parser = argparse.ArgumentParser(description="resolve whatevr build versions")
    parser.add_argument("kind", choices=("full", "numeric"))
    args = parser.parse_args()

    if args.kind == "full":
        print(full_version())
    else:
        print(numeric_version())


if __name__ == "__main__":
    main()
