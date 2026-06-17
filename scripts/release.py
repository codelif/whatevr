#!/usr/bin/env python3
# /// script
# requires-python = ">=3.10"
# dependencies = ["marko"]
# ///
"""One-command release driver for whatevr.

`just release x.y.z` funnels here. This is the single point where a new
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

from marko import Markdown

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
        "Write GitHub-Flavored Markdown; the GitHub release shows it verbatim.\n"
        "The AppStream store description is a reduced rendering: headings become\n"
        "emphasised text, links keep their URL inline, bold/italic both become\n"
        "emphasis, plus lists and inline code. Anything else flattens to text.\n"
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


def regenerate_doodle(root: Path, version: str) -> Path:
    """Regenerate the bundled chat-wallpaper doodle, seeded by the release
    version (numeric x.y.z, any -suffix dropped) so each release ships a
    deterministic, version-specific pattern. Mirrors the `just gen-doodle` call.
    """
    seed = version.split("-")[0]
    output = root / "whatkevr/data/wallpapers/doodle.svg"
    subprocess.run(
        [sys.executable, str(root / "scripts/gen_doodle.py"),
         "--seed", seed, "--output", str(output)],
        check=True,
    )
    return output


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

# The notes are authored once as GitHub-Flavored Markdown and reach the GitHub
# release verbatim (via the annotated tag). AppStream's <description> is far more
# restrictive: it allows only <p>, <ul>, <ol>, <li>, <em> and <code>. The
# converter below downgrades every other construct into that subset without
# dropping information -- headings become emphasised paragraphs, links keep their
# URL inline, code blocks collapse to inline <code>, and so on. Only CommonMark
# is interpreted; rare GFM extras (tables, strikethrough) pass through as text,
# which still renders fully on GitHub.
BLOCK_INDENT = "        "   # 8 spaces: matches the metainfo's <p>/<ul>/<ol>
ITEM_INDENT = "          "  # 10 spaces: matches the metainfo's <li>


def strip_html_comments(markdown: str) -> str:
    return HTML_COMMENT_RE.sub("", markdown)


def markdown_to_appstream_description(markdown: str) -> list[str]:
    document = Markdown().parse(markdown)
    lines: list[str] = []
    for block in document.children:
        lines.extend(render_block(block))
    if not lines:
        fail("release notes do not contain AppStream-compatible text")
    return lines


def render_block(block) -> list[str]:
    name = block.__class__.__name__

    if name == "Paragraph":
        return [f"{BLOCK_INDENT}<p>{render_inline(block)}</p>"]

    if name in ("Heading", "SetextHeading"):
        return [f"{BLOCK_INDENT}<p><em>{render_inline(block)}</em></p>"]

    if name == "List":
        tag = "ol" if block.ordered else "ul"
        lines = [f"{BLOCK_INDENT}<{tag}>"]
        for item in block.children:
            lines.extend(render_list_item(item))
        lines.append(f"{BLOCK_INDENT}</{tag}>")
        return lines

    if name == "Quote":
        lines: list[str] = []
        for child in block.children:
            lines.extend(render_block(child))
        return lines

    if name in ("FencedCode", "CodeBlock"):
        code = collapse_code(block)
        return [f"{BLOCK_INDENT}<p><code>{xml_escape(code)}</code></p>"] if code else []

    if name == "HTMLBlock" and isinstance(block.children, str):
        text = xml_escape(block.children.strip())
        return [f"{BLOCK_INDENT}<p>{text}</p>"] if text else []

    # BlankLine, ThematicBreak, LinkRefDef and anything else: nothing to emit.
    return []


def render_list_item(item) -> list[str]:
    """Render one <li>, flattening any nested list into sibling <li> entries."""
    text_parts: list[str] = []
    nested: list = []
    for child in item.children:
        cname = child.__class__.__name__
        if cname == "List":
            nested.append(child)
        elif cname in ("Paragraph", "Heading", "SetextHeading"):
            text_parts.append(render_inline(child))
        elif cname in ("FencedCode", "CodeBlock"):
            code = collapse_code(child)
            if code:
                text_parts.append(f"<code>{xml_escape(code)}</code>")

    lines: list[str] = []
    text = " ".join(part for part in text_parts if part)
    if text:
        lines.append(f"{ITEM_INDENT}<li>{text}</li>")
    for sublist in nested:
        for subitem in sublist.children:
            lines.extend(render_list_item(subitem))
    return lines


def collapse_code(block) -> str:
    """Flatten a code block to one line -- AppStream has no <pre>/code block."""
    raw = block.children[0].children if block.children else ""
    return " ".join(line for line in raw.splitlines() if line.strip())


def render_inline(element) -> str:
    name = element.__class__.__name__

    if name in ("RawText", "Literal", "InlineHTML"):
        return xml_escape(element.children)

    if name == "LineBreak":
        return " "

    if name == "CodeSpan":
        return f"<code>{xml_escape(element.children)}</code>"

    if name in ("Emphasis", "StrongEmphasis"):
        return f"<em>{render_children(element)}</em>"

    if name in ("Link", "Image"):
        text = render_children(element)
        url = strip_url_scheme(element.dest)
        if not url:
            return text
        return f"{text} ({xml_escape(url)})" if text else f"({xml_escape(url)})"

    if name == "AutoLink":
        return xml_escape(strip_url_scheme(element.dest))

    if isinstance(getattr(element, "children", None), str):
        return xml_escape(element.children)

    return render_children(element)


def render_children(element) -> str:
    return "".join(render_inline(child) for child in element.children)


def strip_url_scheme(url: str | None) -> str:
    """Drop the leading scheme (https://, ...) from a URL.

    AppStream forbids 'scheme://' URLs in release descriptions and refuses to let
    such files validate. Stripping the scheme keeps the destination visible
    (e.g. `docs (codelif.in/x)`) while passing `appstreamcli validate`; the full
    clickable link still lives in the verbatim GitHub release notes.
    """
    return re.sub(r"^[a-zA-Z][a-zA-Z0-9+.-]*://", "", (url or "").strip())


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
        regenerate_doodle(root, version),
        update_metainfo(root, version, notes, date),
        *update_aur_packages(root, version),
    ]

    print("Validating metadata...")
    if subprocess.run(["just", "validate"]).returncode != 0:
        fail("`just validate` failed — files were edited but not committed; "
             "fix and re-run, or `git checkout -- .` to discard")

    rel = [str(p.relative_to(root)) for p in changed]
    git("add", *rel, capture=False)
    git("commit", "-m", f"version: {version}", capture=False)

    tag = f"v{version}"
    # Pass the notes through a file with --cleanup=verbatim so Markdown survives
    # byte-for-byte. The default `git tag -m` cleanup strips every line starting
    # with '#', which would silently delete headings before they reach the
    # GitHub release (the workflow reads them back from the annotated tag body).
    tag_message = f"{tag}\n\n{notes}\n"
    with tempfile.NamedTemporaryFile(
        "w", suffix=".txt", prefix="whatevr-tag-", delete=False, encoding="utf-8"
    ) as tag_file:
        tag_file.write(tag_message)
        tag_msg_path = tag_file.name
    try:
        subprocess.run(
            ["git", "tag", "-a", tag, "--cleanup=verbatim", "-F", tag_msg_path],
            check=True,
        )
    finally:
        os.unlink(tag_msg_path)

    print()
    print(f"Tagged {tag}. Nothing has been pushed.")
    print("Next:  git push --follow-tags")


if __name__ == "__main__":
    main()
