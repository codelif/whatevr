#!/usr/bin/env python3
"""One-command release driver for whatevr.

`make release VERSION=x.y.z` funnels here. This is the single point where a new
version and its release notes are authored once and propagated everywhere they
are needed (VERSION fallback, AppStream metainfo), then committed and
annotated-tagged. It refuses to run on a dirty tree and never pushes — review the
commit and run `git push --follow-tags` yourself.
"""

from __future__ import annotations

import argparse
import os
import re
import subprocess
import sys
import tempfile
from datetime import datetime, timezone
from pathlib import Path

VERSION_RE = re.compile(r"^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$")


def fail(msg: str) -> "NoReturn":  # type: ignore[name-defined]
    print(f"release: {msg}", file=sys.stderr)
    raise SystemExit(1)


def git(*args: str, capture: bool = True) -> str:
    result = subprocess.run(
        ["git", *args],
        check=True,
        text=True,
        stdout=subprocess.PIPE if capture else None,
    )
    return (result.stdout or "").strip()


def repo_root() -> Path:
    return Path(git("rev-parse", "--show-toplevel"))


def ensure_clean_tree() -> None:
    if git("status", "--porcelain"):
        fail("working tree is not clean — commit or stash changes first")


def ensure_tag_absent(version: str) -> None:
    tag = f"v{version}"
    if subprocess.run(
        ["git", "rev-parse", "-q", "--verify", f"refs/tags/{tag}"],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    ).returncode == 0:
        fail(f"tag {tag} already exists")


def capture_notes(args: argparse.Namespace) -> list[str]:
    if args.notes is not None:
        raw = args.notes
    elif args.notes_file is not None:
        raw = Path(args.notes_file).read_text(encoding="utf-8")
    else:
        raw = open_editor(args.version)

    lines: list[str] = []
    for line in raw.splitlines():
        stripped = line.strip()
        if not stripped or stripped.startswith("#"):
            continue
        # Normalise a leading bullet marker; we re-emit per-format markers later.
        stripped = re.sub(r"^[-*]\s+", "", stripped)
        lines.append(stripped)
    if not lines:
        fail("release notes are empty — aborting")
    return lines


def open_editor(version: str) -> str:
    editor = os.environ.get("EDITOR") or os.environ.get("VISUAL") or "vi"
    template = (
        f"# Release notes for v{version} — one bullet per line.\n"
        "# Lines starting with '#' are ignored. Save empty to abort.\n"
        "\n"
    )
    with tempfile.NamedTemporaryFile(
        "w+", suffix=".md", prefix="whatevr-release-", delete=False
    ) as tmp:
        tmp.write(template)
        tmp_path = tmp.name
    try:
        subprocess.run([*editor.split(), tmp_path], check=True)
        return Path(tmp_path).read_text(encoding="utf-8")
    finally:
        os.unlink(tmp_path)


# --- file propagation ------------------------------------------------------


def write_version_file(root: Path, version: str) -> Path:
    path = root / "VERSION"
    path.write_text(version + "\n", encoding="utf-8")
    return path


def update_metainfo(root: Path, version: str, notes: list[str], date: str) -> Path:
    path = root / "whatkevr/data/in.codelif.Whatevr.metainfo.xml"
    text = path.read_text(encoding="utf-8")
    marker = "  <releases>\n"
    if marker not in text:
        fail(f"{path}: could not find <releases> element")

    bullets = "\n".join(f"          <li>{xml_escape(n)}</li>" for n in notes)
    block = (
        f'    <release version="{version}" date="{date}">\n'
        "      <description>\n"
        "        <ul>\n"
        f"{bullets}\n"
        "        </ul>\n"
        "      </description>\n"
        "    </release>\n"
    )
    path.write_text(text.replace(marker, marker + block, 1), encoding="utf-8")
    return path


# --- escaping helpers ------------------------------------------------------


def xml_escape(s: str) -> str:
    return s.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")


# --- main ------------------------------------------------------------------


def main() -> None:
    parser = argparse.ArgumentParser(description="cut a whatevr release")
    parser.add_argument("version", help="semantic version, e.g. 0.2.0")
    group = parser.add_mutually_exclusive_group()
    group.add_argument("--notes", help="release notes (one bullet per line)")
    group.add_argument("--notes-file", help="read release notes from a file")
    args = parser.parse_args()

    version = args.version.lstrip("v")
    args.version = version
    if not VERSION_RE.match(version):
        fail(f"invalid version {version!r}; expected X.Y.Z[-suffix]")

    root = repo_root()
    os.chdir(root)
    ensure_clean_tree()
    ensure_tag_absent(version)

    notes = capture_notes(args)
    date = datetime.now(timezone.utc).strftime("%Y-%m-%d")

    changed = [
        write_version_file(root, version),
        update_metainfo(root, version, notes, date),
    ]

    print("Validating metadata...")
    if subprocess.run(["make", "validate"]).returncode != 0:
        fail("`make validate` failed — files were edited but not committed; "
             "fix and re-run, or `git checkout -- .` to discard")

    rel = [str(p.relative_to(root)) for p in changed]
    git("add", *rel, capture=False)
    git("commit", "-m", f"version: {version}", capture=False)

    tag = f"v{version}"
    tag_message = f"{tag}\n\n" + "\n".join(f"- {n}" for n in notes)
    subprocess.run(["git", "tag", "-a", tag, "-m", tag_message], check=True)

    print()
    print(f"Tagged {tag}. Nothing has been pushed.")
    print("Next:  git push --follow-tags")


if __name__ == "__main__":
    main()
