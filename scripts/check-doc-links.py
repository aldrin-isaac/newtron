#!/usr/bin/env python3
"""Check internal markdown links across the docs tree.

Validates two things for every relative link in the given roots (default: docs/):

  1. File existence — `](path/to/file.md)` resolves to a real file.
  2. Anchor fragments — `](file.md#heading)` and same-file `](#heading)` point
     at a heading that actually exists in the target, using GitHub's slug
     algorithm (lowercase; drop punctuation incl. em-dashes, parens, slashes;
     spaces -> hyphens individually so "Foo — Bar" -> "foo--bar"; duplicate
     headings get -1/-2 suffixes).

External links (http/https/mailto/tel) are skipped. Links inside fenced code
blocks (``` or ~~~) are skipped — they are examples, not navigation.

Exit status: 0 if every internal link resolves, 1 otherwise (with a report).

Usage: scripts/check-doc-links.py [root ...]   # roots default to ["docs"]

A root may be a directory (walked for *.md) or a single .md file, so root-level
docs are covered too, e.g.:  scripts/check-doc-links.py docs README.md CLAUDE.md
"""

import os
import re
import sys

LINK = re.compile(r"\]\(([^)]+)\)")
FENCE = re.compile(r"^\s*(```|~~~)")
HEADING = re.compile(r"^#{1,6}\s")
EXPLICIT_ANCHOR = re.compile(r"\{#([\w-]+)\}\s*$")  # `### Title {#custom-id}` override
EXTERNAL = ("http://", "https://", "mailto:", "tel:", "//")


def gh_slug(heading_line: str) -> str:
    """GitHub's heading-to-anchor slug (text form, ignoring any {#id} override)."""
    h = EXPLICIT_ANCHOR.sub("", heading_line).lstrip("#").strip().lower()
    h = re.sub(r"[^\w\s-]", "", h)  # drop punctuation, incl. em-dash / parens / slashes
    return h.replace(" ", "-")      # each space individually (NOT collapsed)


def headings_of(path: str) -> set:
    """Every anchor slug the file exposes: text-derived slugs (with GitHub's
    -1/-2 dedup suffixes) plus any explicit `{#custom-id}` overrides."""
    counts: dict[str, int] = {}
    explicit = set()
    in_fence = False
    with open(path, encoding="utf-8", errors="replace") as fh:
        for line in fh:
            if FENCE.match(line):
                in_fence = not in_fence
                continue
            if in_fence or not HEADING.match(line):
                continue
            m = EXPLICIT_ANCHOR.search(line)
            if m:
                explicit.add(m.group(1).lower())
            s = gh_slug(line)
            counts[s] = counts.get(s, 0) + 1
    slugs = set(explicit)
    for s, c in counts.items():
        slugs.add(s)
        for i in range(1, c):
            slugs.add(f"{s}-{i}")
    return slugs


def iter_links(path: str):
    """Yield (lineno, target) for each markdown link outside fenced code."""
    in_fence = False
    with open(path, encoding="utf-8", errors="replace") as fh:
        for lineno, line in enumerate(fh, 1):
            if FENCE.match(line):
                in_fence = not in_fence
                continue
            if in_fence:
                continue
            for m in LINK.finditer(line):
                yield lineno, m.group(1).strip()


def main(argv: list[str]) -> int:
    roots = argv[1:] or ["docs"]
    md_files = []
    for root in roots:
        if os.path.isfile(root):  # a root may be a single .md file (README.md, CLAUDE.md)
            if root.endswith(".md"):
                md_files.append(root)
            continue
        for dirpath, _, files in os.walk(root):
            md_files.extend(os.path.join(dirpath, f) for f in files if f.endswith(".md"))

    headings_cache: dict[str, set] = {}
    broken = []
    checked = 0

    for path in sorted(md_files):
        base = os.path.dirname(path)
        for lineno, target in iter_links(path):
            if target.startswith(EXTERNAL):
                continue
            filepart, _, frag = target.partition("#")
            filepart = filepart.split(" ", 1)[0].strip()  # drop any "title"

            if not filepart:  # same-file anchor: ](#heading)
                if not frag:
                    continue
                checked += 1
                heads = headings_cache.setdefault(path, headings_of(path))
                if frag.lower() not in heads:
                    broken.append((path, lineno, target, "no such heading in this file"))
                continue

            checked += 1
            resolved = os.path.normpath(os.path.join(base, filepart))
            if not os.path.exists(resolved):
                broken.append((path, lineno, target, f"missing file: {resolved}"))
                continue
            if frag and resolved.endswith(".md"):
                heads = headings_cache.setdefault(resolved, headings_of(resolved))
                if frag.lower() not in heads:
                    broken.append((path, lineno, target, f"no such heading in {os.path.basename(resolved)}"))

    print(f"checked {checked} internal links across {', '.join(roots)}")
    if not broken:
        print("all internal links resolve ✓")
        return 0
    print(f"\n{len(broken)} broken:\n")
    for path, lineno, target, why in broken:
        print(f"  {path}:{lineno}\n     {target}\n     -> {why}")
    return 1


if __name__ == "__main__":
    sys.exit(main(sys.argv))
