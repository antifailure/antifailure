#!/usr/bin/env python3
"""List the actual built pages the owner can open in the website editor."""

from html import unescape
from html.parser import HTMLParser
from pathlib import Path
from urllib.parse import urlparse
import json
import re
import sys
import xml.etree.ElementTree as ET


class Title(HTMLParser):
    def __init__(self):
        super().__init__()
        self.in_title = False
        self.value = ""
        self.description = ""
        self.published = ""
        self.tags = []

    def handle_starttag(self, tag, attrs):
        attrs = dict(attrs)
        if tag == "title":
            self.in_title = True
        if tag == "meta" and attrs.get("name") == "description":
            self.description = attrs.get("content", "")
        if tag == "meta" and attrs.get("property") == "article:published_time":
            self.published = attrs.get("content", "")
        if tag == "meta" and attrs.get("property") == "article:tag":
            self.tags.append(attrs.get("content", ""))

    def handle_endtag(self, tag):
        if tag == "title":
            self.in_title = False

    def handle_data(self, data):
        if self.in_title:
            self.value += data


def locations(path):
    tree = ET.parse(path)
    return [entry.text for entry in tree.iter() if entry.tag.endswith("}loc") and entry.text]


def main():
    root = Path(sys.argv[1] if len(sys.argv) > 1 else "site")
    urls = locations(root / "sitemap.xml")
    for sitemap in locations(root / "docs" / "sitemap-index.xml"):
        parsed = urlparse(sitemap)
        if parsed.scheme != "https" or parsed.netloc != "antifailure.dev" or not parsed.path.startswith("/docs/sitemap-"):
            raise SystemExit(f"unexpected documentation sitemap: {sitemap}")
        urls.extend(locations(root / parsed.path.lstrip("/")))
    pages = []
    seen = set()
    for url in urls:
        parsed = urlparse(url)
        if parsed.scheme != "https" or parsed.netloc != "antifailure.dev" or parsed.query or parsed.fragment:
            raise SystemExit(f"unexpected sitemap location: {url}")
        path = parsed.path or "/"
        if path in seen:
            raise SystemExit(f"duplicate sitemap page: {path}")
        seen.add(path)
        candidate = root / (path.lstrip("/") + ".html")
        if path == "/":
            candidate = root / "index.html"
        if not candidate.is_file():
            candidate = root / path.lstrip("/") / "index.html"
        if not candidate.is_file():
            raise SystemExit(f"sitemap page has no built HTML: {path}")
        title = Title()
        title.feed(candidate.read_text(encoding="utf-8"))
        label = unescape(title.value).strip()
        if not label:
            raise SystemExit(f"built page has no title: {path}")
        label = re.sub(r"\s+[·|]\s+Antifailure$", "", label)
        section = "Docs" if path.startswith("/docs") else "Writing" if path.startswith("/blog") else "Product" if path.startswith("/product") else "Solutions" if path.startswith("/solutions") else "Website"
        page = {"path": path, "title": label, "section": section}
        if section == "Writing" and path != "/blog":
            page.update({"description": title.description[:300], "published": title.published[:32], "tags": title.tags[:12]})
        pages.append(page)
    if len(pages) < 100:
        raise SystemExit(f"page catalog found only {len(pages)} indexable pages")
    pages.sort(key=lambda item: (item["section"], item["path"]))
    (root / "cms-pages.json").write_text(json.dumps({"schemaVersion": 1, "pages": pages}, separators=(",", ":")) + "\n", encoding="utf-8")
    print(f"CMS page catalog: {len(pages)} built routes")


if __name__ == "__main__":
    main()
