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
import html
import os
import re
import subprocess
import sys
import tempfile
from datetime import datetime, timezone
from pathlib import Path
from typing import NoReturn

VERSION_RE = re.compile(r"^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$")


def fail(msg: str) -> NoReturn:
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


def capture_notes(args: argparse.Namespace) -> str:
    if args.notes is not None:
        raw = args.notes
    elif args.notes_file is not None:
        raw = Path(args.notes_file).read_text(encoding="utf-8")
    else:
        raw = open_editor(args.version)

    notes = strip_html_comments(raw).strip()
    if not notes:
        fail("release notes are empty — aborting")
    return notes


def open_editor(version: str) -> str:
    editor = os.environ.get("EDITOR") or os.environ.get("VISUAL") or "vi"
    template = (
        "<!--\n"
        f"Release notes for v{version}.\n"
        "Write Markdown. Supported in AppStream: headings, paragraphs, "
        "bullets, numbered lists, emphasis, and inline code.\n"
        "Delete these comments or leave them here; they are ignored.\n"
        "Save empty notes to abort.\n"
        "-->\n\n"
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


def update_metainfo(root: Path, version: str, notes: str, date: str) -> Path:
    path = root / "whatkevr/data/in.codelif.Whatevr.metainfo.xml"
    text = path.read_text(encoding="utf-8")
    marker = "  <releases>\n"
    if marker not in text:
        fail(f"{path}: could not find <releases> element")

    description = "\n".join(markdown_to_appstream_description(notes))
    block = (
        f'    <release version="{version}" date="{date}">\n'
        "      <description>\n"
        f"{description}\n"
        "      </description>\n"
        "    </release>\n"
    )
    path.write_text(text.replace(marker, marker + block, 1), encoding="utf-8")
    return path


def update_aur_packages(root: Path, version: str) -> list[Path]:
    changed: list[Path] = []

    source_pkgbuild = root / "packaging/aur/whatevr/PKGBUILD"
    source_srcinfo = root / "packaging/aur/whatevr/.SRCINFO"
    bin_pkgbuild = root / "packaging/aur/whatevr-bin/PKGBUILD"
    bin_srcinfo = root / "packaging/aur/whatevr-bin/.SRCINFO"

    update_lines(
        source_pkgbuild,
        {
            r"^pkgver=.*$": f"pkgver={version}",
            r"^pkgrel=.*$": "pkgrel=1",
        },
    )
    changed.append(source_pkgbuild)

    update_lines(
        source_srcinfo,
        {
            r"^\tpkgver = .*$": f"\tpkgver = {version}",
            r"^\tpkgrel = .*$": "\tpkgrel = 1",
            r"^\tsource = .*$": (
                "\tsource = whatevr-"
                f"{version}.tar.gz::https://github.com/codelif/whatevr/"
                f"releases/download/v{version}/whatevr-{version}.tar.gz"
            ),
        },
    )
    changed.append(source_srcinfo)

    update_lines(
        bin_pkgbuild,
        {
            r"^pkgver=.*$": f"pkgver={version}",
            r"^pkgrel=.*$": "pkgrel=1",
        },
    )
    changed.append(bin_pkgbuild)

    update_lines(
        bin_srcinfo,
        {
            r"^\tpkgver = .*$": f"\tpkgver = {version}",
            r"^\tpkgrel = .*$": "\tpkgrel = 1",
            r"^\tsource_x86_64 = .*$": (
                "\tsource_x86_64 = whatevr-"
                f"{version}-linux-x86_64.tar.zst::https://github.com/codelif/"
                f"whatevr/releases/download/v{version}/"
                f"whatevr-{version}-linux-x86_64.tar.zst"
            ),
        },
    )
    changed.append(bin_srcinfo)

    return changed


def update_lines(path: Path, replacements: dict[str, str]) -> None:
    text = path.read_text(encoding="utf-8")
    for pattern, replacement in replacements.items():
        text, count = re.subn(pattern, replacement, text, count=1, flags=re.MULTILINE)
        if count != 1:
            fail(f"{path}: expected one match for {pattern!r}, got {count}")
    path.write_text(text, encoding="utf-8")


# --- release note formatting ----------------------------------------------


HTML_COMMENT_RE = re.compile(r"<!--.*?-->", re.DOTALL)
BULLET_RE = re.compile(r"^\s*[-*+]\s+(.+)$")
ORDERED_RE = re.compile(r"^\s*\d+[.)]\s+(.+)$")
FENCE_RE = re.compile(r"^\s*(```|~~~)")
HEADING_RE = re.compile(r"^\s*#{1,6}\s+(.+?)\s*#*\s*$")
INLINE_LINK_RE = re.compile(r"!?\[([^\]]*)\]\([^)]*\)")
INLINE_MARKUP_RE = re.compile(r"`([^`\n]+)`|\*\*([^*\n]+)\*\*|__([^_\n]+)__")


def strip_html_comments(markdown: str) -> str:
    return HTML_COMMENT_RE.sub("", markdown)


def markdown_to_appstream_description(markdown: str) -> list[str]:
    lines: list[str] = []
    for kind, items in markdown_blocks(markdown):
        if kind == "p":
            lines.append(f"        <p>{render_inline_markdown(items[0])}</p>")
            continue

        lines.append(f"        <{kind}>")
        for item in items:
            lines.append(f"          <li>{render_inline_markdown(item)}</li>")
        lines.append(f"        </{kind}>")

    if not lines:
        fail("release notes do not contain AppStream-compatible text")
    return lines


def markdown_blocks(markdown: str) -> list[tuple[str, list[str]]]:
    blocks: list[tuple[str, list[str]]] = []
    source = markdown.replace("\r\n", "\n").replace("\r", "\n").split("\n")
    i = 0
    while i < len(source):
        line = source[i]
        if not line.strip():
            i += 1
            continue

        if FENCE_RE.match(line):
            text: list[str] = []
            fence = line.strip()[:3]
            i += 1
            while i < len(source) and not source[i].strip().startswith(fence):
                if source[i].strip():
                    text.append(source[i].strip())
                i += 1
            if i < len(source):
                i += 1
            if text:
                blocks.append(("p", [" ".join(text)]))
            continue

        if match := HEADING_RE.match(line):
            blocks.append(("p", [clean_block_text(match.group(1))]))
            i += 1
            continue

        list_match = list_item_match(line)
        if list_match is not None:
            kind, text = list_match
            items = [text]
            i += 1

            while i < len(source):
                line = source[i]
                if not line.strip():
                    i += 1
                    break

                next_match = list_item_match(line)
                if next_match is not None:
                    next_kind, next_text = next_match
                    if next_kind != kind:
                        break
                    items.append(next_text)
                    i += 1
                    continue

                if items and (line.startswith(" ") or line.startswith("\t")):
                    items[-1] = f"{items[-1]} {line.strip()}"
                    i += 1
                    continue

                break

            blocks.append((kind, items))
            continue

        paragraph: list[str] = []
        while i < len(source):
            line = source[i]
            if (
                not line.strip()
                or list_item_match(line) is not None
                or FENCE_RE.match(line)
                or HEADING_RE.match(line)
            ):
                break
            paragraph.append(line.strip().removeprefix("> ").removeprefix(">"))
            i += 1
        blocks.append(("p", [" ".join(paragraph)]))

    return blocks


def list_item_match(line: str) -> tuple[str, str] | None:
    if match := BULLET_RE.match(line):
        return "ul", clean_block_text(match.group(1))
    if match := ORDERED_RE.match(line):
        return "ol", clean_block_text(match.group(1))
    return None


def clean_block_text(text: str) -> str:
    return re.sub(r"^#{1,6}\s+", "", text.strip())


def render_inline_markdown(text: str) -> str:
    text = clean_block_text(INLINE_LINK_RE.sub(r"\1", text))
    result: list[str] = []
    cursor = 0

    for match in INLINE_MARKUP_RE.finditer(text):
        result.append(xml_escape(text[cursor:match.start()]))
        code, strong_star, strong_underscore = match.groups()
        if code is not None:
            result.append(f"<code>{xml_escape(code)}</code>")
        else:
            content = strong_star if strong_star is not None else strong_underscore
            result.append(f"<em>{xml_escape(content or '')}</em>")
        cursor = match.end()

    result.append(xml_escape(text[cursor:]))
    return "".join(result)


def xml_escape(s: str) -> str:
    return html.escape(s, quote=False)


# --- main ------------------------------------------------------------------


def main() -> None:
    parser = argparse.ArgumentParser(description="cut a whatevr release")
    parser.add_argument("version", help="semantic version, e.g. 0.2.0")
    group = parser.add_mutually_exclusive_group()
    group.add_argument("--notes", help="Markdown release notes")
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
        *update_aur_packages(root, version),
    ]

    print("Validating metadata...")
    if subprocess.run(["make", "validate"]).returncode != 0:
        fail("`make validate` failed — files were edited but not committed; "
             "fix and re-run, or `git checkout -- .` to discard")

    rel = [str(p.relative_to(root)) for p in changed]
    git("add", *rel, capture=False)
    git("commit", "-m", f"version: {version}", capture=False)

    tag = f"v{version}"
    tag_message = f"{tag}\n\n{notes}"
    subprocess.run(["git", "tag", "-a", tag, "-m", tag_message], check=True)

    print()
    print(f"Tagged {tag}. Nothing has been pushed.")
    print("Next:  git push --follow-tags")


if __name__ == "__main__":
    main()
