"""Set section, order, and next in docs front matter from one reading order."""
import pathlib, re, sys

DOCS = pathlib.Path(__file__).resolve().parents[1] / "docs"
ORDER = [
    ("Learn", ["start", "tutorial"]),
    ("Core", ["routing", "templates", "data", "forms", "errors"]),
    ("Built-ins", ["sessions", "assets", "content", "realtime", "directives"]),
    ("Production", ["configuration", "security", "deploying", "testing"]),
    ("Integrations", ["integrations", "integration-manager", "templ", "jet", "alpine"]),
    ("Reference", ["helpers", "filters", "apps"]),
]
flat = [(sec, slug) for sec, slugs in ORDER for slug in slugs]
skip = set(sys.argv[1:])
missing = sorted({p.stem for p in DOCS.glob("*.md")} - {s for _, s in flat})
if missing:
    raise SystemExit(f"pages not in ORDER: {missing}")
for i, (section, slug) in enumerate(flat):
    if slug in skip:
        continue
    nxt = flat[(i + 1) % len(flat)][1]
    path = DOCS / f"{slug}.md"
    text = path.read_text()
    head, sep, body = text[4:].partition("\n---\n")
    lines = [l for l in head.splitlines() if not re.match(r"(section|order|next|nextLabel):", l)]
    old_next = re.search(r"^next:\s*(\S+)", head, re.M)
    keep_label = re.search(r"^nextLabel:.*$", head, re.M) if old_next and old_next.group(1) == nxt else None
    lines.insert(1, f"section: {section}")
    lines.insert(2, f"order: {(i + 1) * 10}")
    lines.insert(3, f"next: {nxt}")
    if keep_label:
        lines.insert(4, keep_label.group(0))
    path.write_text("---\n" + "\n".join(lines) + "\n---\n" + body)
    print(f"{(i + 1) * 10:4d} {section:13s} {slug:20s} -> {nxt}")
