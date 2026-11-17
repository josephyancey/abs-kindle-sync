"""Build a position map between an EPUB (what Audiobookshelf reads) and its AZW3 conversion
(what Amazon's Kindle reader opens).

The Kindle reader's position for an AZW3 is a byte offset into the book's KF8 text stream
(calibre calls it "rawml"); `lastReadPosition=1316` is the first visible text of the book.
Audiobookshelf stores an EPUB CFI (`ebookLocation`) and a fraction (`ebookProgress`).

The map lists every run of text in the EPUB, in reading order, with:

    [cfi, epub_start, length, kindle_start]

- `cfi`: the EPUB CFI of the run's first character, without the character offset
  (for example `epubcfi(/6/8[ch2]!/4/12/1`). Append `:<n>)` for the n-th character.
- `epub_start`: the run's position in the book's text, counted the same way as the CFI
  offsets: every text node under <body>, whitespace included, script/style skipped.
- `length`: characters in the run.
- `kindle_start`: byte offset in the AZW3 text stream where the run's text begins, or
  null when the run couldn't be found (pick a neighbour).

Within a run, positions are treated as linear, which is accurate to a few characters; the
Kindle snaps to the start of the page anyway.

Usage, with calibre installed (calibre-debug provides calibre's Python and lxml):

    calibre-debug kindle_map.py book.epub book.kindle.azw3 book.kindle.map.json
"""

import json
import posixpath
import re
import sys
import zipfile
from html import unescape
from urllib.parse import unquote

from lxml import etree

MAP_VERSION = 1


def _local(tag):
    return tag.rsplit('}', 1)[-1].lower() if isinstance(tag, str) else ''


def _spine(archive):
    """[(idref, zip path), ...] in reading order"""
    container = archive.read('META-INF/container.xml').decode('utf-8', 'replace')
    opf_path = re.search(r'full-path="([^"]+)"', container).group(1)
    opf_dir = posixpath.dirname(opf_path)
    root = etree.fromstring(archive.read(opf_path), etree.XMLParser(recover=True, resolve_entities=False))
    manifest, idrefs = {}, []
    for node in root.iter():
        name = _local(node.tag)
        if name == 'item' and node.get('id') and node.get('href'):
            manifest[node.get('id')] = node.get('href')
        elif name == 'itemref' and node.get('idref'):
            idrefs.append(node.get('idref'))
    names = set(archive.namelist())
    spine = []
    for idref in idrefs:
        href = manifest.get(idref)
        if not href:
            spine.append((idref, None))
            continue
        path = posixpath.normpath(posixpath.join(opf_dir, unquote(href.split('#')[0])))
        raw = posixpath.normpath(posixpath.join(opf_dir, href.split('#')[0]))
        spine.append((idref, path if path in names else raw if raw in names else None))
    return spine


def _walk(node, path, cursor, runs):
    """Record text runs under node in document order: (cfi_path, chunk_offset, start, text).

    CFI numbering: element children take even steps, the text before, between and after
    them odd steps; a comment's tail stays in the chunk it interrupts.
    """
    used = {}

    def add(text, chunk):
        nonlocal cursor
        if not text:
            return
        already = used.get(chunk, 0)
        runs.append(('%s/%d' % (path, chunk), already, cursor, text))
        used[chunk] = already + len(text)
        cursor += len(text)

    add(node.text, 1)
    k = 0
    for child in node:
        if isinstance(child.tag, str):
            k += 1
            if _local(child.tag) not in ('script', 'style'):
                cursor = _walk(child, '%s/%d' % (path, k * 2), cursor, runs)
        add(child.tail, k * 2 + 1)
    return cursor


def epub_runs(epub_path):
    """Every text run in the EPUB with its CFI prefix and global character offset"""
    runs = []
    cursor = 0
    with zipfile.ZipFile(epub_path) as archive:
        for index, (idref, entry) in enumerate(_spine(archive)):
            if not entry:
                continue
            try:
                root = etree.fromstring(archive.read(entry), etree.XMLParser(recover=True, resolve_entities=False, huge_tree=True))
            except (etree.XMLSyntaxError, ValueError):
                continue
            if root is None:
                continue
            body = next(((i, c) for i, c in enumerate([c for c in root if isinstance(c.tag, str)], start=1) if _local(c.tag) == 'body'), None)
            if body is None:
                continue
            chapter_runs = []
            cursor = _walk(body[1], '/%d' % (body[0] * 2), cursor, chapter_runs)
            prefix = 'epubcfi(/6/%d[%s]!' % ((index + 1) * 2, idref)
            for cfi_path, chunk_offset, start, text in chapter_runs:
                runs.append({'cfi': prefix + cfi_path, 'chunk_offset': chunk_offset, 'start': start, 'text': text})
    return runs, cursor


def kindle_rawml(azw3_path):
    """The KF8 text stream, as the Kindle reader addresses it"""
    from calibre.ebooks.mobi.reader.mobi6 import MobiReader
    from calibre.ebooks.mobi.reader.mobi8 import Mobi8Reader
    from calibre.utils.logging import default_log
    reader = MobiReader(azw3_path, default_log)
    reader.check_for_drm()
    Mobi8Reader(reader, default_log)
    reader.extract_text()
    return reader.mobi_html


_TEXT = re.compile(rb'>([^<]+)<')
_SPACE = re.compile(r'\s+')


def kindle_text(rawml):
    """Visible text of the stream with whitespace removed, plus the byte offset of each character"""
    chars, offsets = [], []
    for match in _TEXT.finditer(rawml):
        raw = match.group(1)
        base = match.start(1)
        # Map each decoded character back to the byte where its source starts
        position = 0
        for piece in re.finditer(rb'&[#A-Za-z0-9]+;|[\x00-\x7f]|[\xc0-\xff][\x80-\xbf]*', raw):
            text = unescape(piece.group(0).decode('utf-8', 'replace'))
            for ch in text:
                if not ch.isspace():
                    chars.append(ch)
                    offsets.append(base + piece.start())
            position = piece.end()
    return ''.join(chars), offsets


def build_map(epub_path, azw3_path):
    runs, total = epub_runs(epub_path)
    ktext, koffsets = kindle_text(kindle_rawml(azw3_path))
    cursor = 0
    entries = []
    for run in runs:
        needle = _SPACE.sub('', run['text'])
        if not needle:
            # Whitespace between paragraphs: nothing a reader can be positioned on
            continue
        kindle_start = None
        if needle:
            # Look ahead from where the previous run was found; the text is in the same order
            probe = needle[:40]
            found = ktext.find(probe, cursor, cursor + 200000)
            if found >= 0:
                kindle_start = koffsets[found]
                cursor = found + len(needle)
        cfi = run['cfi']
        entries.append([cfi, run['chunk_offset'], run['start'], len(run['text']), kindle_start])
    found = sum(1 for e in entries if e[4] is not None and e[3] > 0)
    return {
        'version': MAP_VERSION,
        'epub_length': total,
        'kindle_length': koffsets[-1] + 1 if koffsets else 0,
        'matched': found,
        'runs': entries,
    }


def main():
    epub_path, azw3_path, out_path = sys.argv[1:4]
    result = build_map(epub_path, azw3_path)
    with open(out_path, 'w', encoding='utf-8') as handle:
        json.dump(result, handle, separators=(',', ':'), ensure_ascii=False)
    print('runs %d, matched %d, epub chars %d' % (len(result['runs']), result['matched'], result['epub_length']))


if __name__ == '__main__':
    main()
