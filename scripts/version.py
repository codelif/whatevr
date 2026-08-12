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


def version_describe(version: str) -> str | None:
    result = subprocess.run(
        ["git", "-C", str(ROOT), "log", "-1", "--format=%H", "--", "VERSION"],
        check=False,
        stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL,
        text=True,
    )
    release_commit = result.stdout.strip()
    if result.returncode != 0 or not release_commit:
        return None

    count = subprocess.run(
        ["git", "-C", str(ROOT), "rev-list", "--count", f"{release_commit}..HEAD"],
        check=False,
        stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL,
        text=True,
    )
    commits = count.stdout.strip()
    if count.returncode != 0 or not commits:
        return None
    if commits == "0":
        return version

    head = subprocess.run(
        ["git", "-C", str(ROOT), "rev-parse", "--short", "HEAD"],
        check=False,
        stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL,
        text=True,
    )
    short_head = head.stdout.strip()
    if head.returncode != 0 or not short_head:
        return None

    dirty = subprocess.run(
        ["git", "-C", str(ROOT), "diff-index", "--quiet", "HEAD", "--"],
        check=False,
        stderr=subprocess.DEVNULL,
    ).returncode
    suffix = "-dirty" if dirty else ""
    return f"{version}-{commits}-g{short_head}{suffix}"


def version_file() -> str | None:
    path = ROOT / "VERSION"
    if not path.exists():
        return None

    version = path.read_text(encoding="utf-8").strip()
    return version or None


def full_version() -> str:
    described = git_describe()
    file_version = version_file()
    if described and file_version and not described.startswith(file_version):
        return version_describe(file_version) or described
    return described or file_version or "0.0.0-unknown"


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
