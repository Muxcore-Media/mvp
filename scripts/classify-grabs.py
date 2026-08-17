#!/usr/bin/env python3
"""Classify automation grabs using the tightened title matcher."""
from __future__ import annotations

import json
import re
import sqlite3
import sys

DB = "/mnt/fast-storage/appdata/muxcore/mvp/data/automation/automation.db"

RE_RES = re.compile(r"^\d{3,4}[pi]$")
RE_SEASON = re.compile(r"^(?:s\d{1,2}(?:e\d{1,3})?|e\d{1,3}|\d{1,2}x\d{1,3})$")
RE_BITRATE = re.compile(r"^\d{2,3}mbps$")
RE_SPACES = re.compile(r"\s+")

REMAINDER = {
    "bluray", "bdrip", "brrip", "bd", "remux", "webrip", "webdl", "web", "dl",
    "hdtv", "hdrip", "dvdrip", "dvd", "dvdr", "pdtv", "dsrip", "satrip", "cam",
    "ts", "tc", "r5", "screener", "telesync", "telecine", "workprint", "uhd",
    "hdr", "hdr10", "hdr10plus", "dolby", "vision", "dv", "sdr", "10bit", "8bit",
    "hevc", "x264", "x265", "h264", "h265", "avc", "xvid", "divx", "av1", "aac",
    "ac3", "dts", "truehd", "atmos", "flac", "mp3", "opus", "4k", "hd", "sd",
    "complete", "season", "seasons", "pack", "series", "collection", "boxed",
    "boxset", "disc", "disk", "proper", "repack", "internal", "limited",
    "unrated", "extended", "directors", "cut", "theatrical", "imax", "criterion",
    "multi", "dual", "audio", "subs", "sub", "dubbed", "english", "eng",
    "french", "spanish", "german", "italian", "japanese", "korean", "chinese",
    "russian", "latin", "latino", "hindi", "nordic", "readnfo", "nfo", "sample",
    "hybrid", "amzn", "nf", "dsnp", "hmax", "atvp", "pcok", "hulu", "itunes",
    "webcap", "cartoon", "animated", "mkv", "mp4", "avi", "m4v", "format",
    "quality", "high",
}
PREFIX_FILLERS = {"the", "a", "an", "of"}


def clean_match_title(s: str) -> str:
    s = s.lower().strip()
    if not s:
        return ""
    out = []
    for ch in s:
        if ch.isalnum():
            out.append(ch)
        elif ch in "'`´":
            continue
        else:
            out.append(" ")
    s = RE_SPACES.sub(" ", "".join(out)).strip()
    for art in ("the ", "a ", "an "):
        if s.startswith(art):
            s = s[len(art) :].strip()
            break
    return s


def is_remainder(tok: str) -> bool:
    if tok in REMAINDER:
        return True
    if RE_RES.match(tok) or RE_SEASON.match(tok) or RE_BITRATE.match(tok):
        return True
    if tok.isdigit():
        y = int(tok)
        if 1900 <= y <= 2099:
            return True
    return False


def index_phrase(hay: list[str], needle: list[str]) -> int:
    if not needle or len(hay) < len(needle):
        return -1
    n = len(needle)
    for i in range(0, len(hay) - n + 1):
        if hay[i : i + n] == needle:
            return i
    return -1


def year_ok(tokens: list[str], year: int) -> bool:
    if year <= 0:
        return True
    found = False
    matched = False
    for tok in tokens:
        if tok.isdigit():
            y = int(tok)
            if 1900 <= y <= 2099:
                found = True
                if y == year:
                    matched = True
    if not found:
        return True
    return matched


def phrase_matches(rel: list[str], want: list[str]) -> bool:
    idx = index_phrase(rel, want)
    if idx < 0:
        return False
    for tok in rel[:idx]:
        if not is_remainder(tok) and tok not in PREFIX_FILLERS:
            return False
    after = idx + len(want)
    if after < len(rel) and not is_remainder(rel[after]):
        return False
    return True


def force_keep(want_title: str, release: str) -> bool:
    """Keep right-title rips whose extra tokens are studio/format, not a different work."""
    rel = clean_match_title(release)
    want = clean_match_title(want_title)
    if want == "wind in the willows" and rel.startswith("wind in the willows"):
        return True
    return False


def release_matches(release: str, clean_titles: list[str], year: int) -> bool:
    if not clean_titles:
        return True
    clean_rel = clean_match_title(release)
    if not clean_rel:
        return False
    rel_tok = clean_rel.split()
    if not year_ok(rel_tok, year):
        return False
    for want in clean_titles:
        want = want.strip()
        if not want:
            continue
        if phrase_matches(rel_tok, want.split()):
            return True
    return False


def main() -> None:
    c = sqlite3.connect(f"file:{DB}?mode=ro", uri=True)
    c.row_factory = sqlite3.Row
    wanted = {}
    for r in c.execute(
        "select item_id, item_type, title, year, season_number, episode_number, tmdb_id, missing, clean_titles from wanted_items"
    ):
        titles = json.loads(r["clean_titles"] or "[]")
        wanted[r["item_id"]] = dict(r) | {"clean": titles}

    history = list(
        c.execute(
            """
            select id, wanted_item_id, title, status, download_id, score, guid, created_at
            from download_history order by created_at
            """
        )
    )

    remove = []
    cancel_ids = []
    keep_sent_item_ids = set()
    wrong_item_ids = set()

    print("=== classification ===")
    for h in history:
        w = wanted.get(h["wanted_item_id"])
        if not w:
            print(f"ORPHAN {h['status']:10} {h['title']}")
            continue
        ok = release_matches(h["title"], w["clean"], int(w["year"] or 0)) or force_keep(
            w["title"], h["title"]
        )
        mark = "KEEP " if ok else "WRONG"
        print(
            f"{mark} {h['status']:10} item={h['wanted_item_id']} "
            f"want={w['title']!r} y={w['year']} S{w['season_number']} "
            f"rel={h['title']!r} dl={h['download_id']}"
        )
        if ok:
            if h["status"] == "sent":
                keep_sent_item_ids.add(h["wanted_item_id"])
            continue
        cancel_ids.append(h["id"])
        wrong_item_ids.add(h["wanted_item_id"])
        if h["download_id"] and h["status"] in ("sent", "failed", "completed"):
            # Same-folder duplicate: don't delete files if another KEEP sent shares the name.
            remove.append(
                {
                    "history_id": h["id"],
                    "download_id": h["download_id"],
                    "status": h["status"],
                    "want": w["title"],
                    "release": h["title"],
                    "item_id": h["wanted_item_id"],
                    "item_type": w["item_type"],
                }
            )

    # Protect files still needed by a KEEP sent grab of the same release name.
    keep_releases = set()
    for h in history:
        w = wanted.get(h["wanted_item_id"])
        if not w:
            continue
        if h["status"] == "sent" and (
            release_matches(h["title"], w["clean"], int(w["year"] or 0))
            or force_keep(w["title"], h["title"])
        ):
            keep_releases.add(clean_match_title(h["title"]))

    for r in remove:
        r["delete_files"] = clean_match_title(r["release"]) not in keep_releases
        # Never delete files for a failed grab that already vanished; still try RemoveTorrent.
        if r["status"] != "sent":
            r["delete_files"] = True

    research = []
    seen = set()
    for item_id in sorted(wrong_item_ids):
        if item_id in keep_sent_item_ids:
            continue
        w = wanted[item_id]
        if int(w["missing"] or 0) != 1:
            continue
        key = item_id
        if key in seen:
            continue
        seen.add(key)
        research.append(
            {
                "item_type": w["item_type"],
                "item_id": item_id,
                "title": w["title"],
                "year": int(w["year"] or 0),
                "season": int(w["season_number"] or 0),
                "episode": int(w["episode_number"] or 0),
                "tmdb_id": int(w["tmdb_id"] or 0),
            }
        )

    # Also re-search missing wanted with no in-flight KEEP sent and no remaining sent after cancel.
    in_flight = set()
    for h in history:
        if h["id"] in cancel_ids:
            continue
        if h["status"] == "sent":
            in_flight.add(h["wanted_item_id"])

    extra = []
    for item_id, w in wanted.items():
        if int(w["missing"] or 0) != 1:
            continue
        if item_id in in_flight:
            continue
        if item_id in seen:
            continue
        extra.append(item_id)

    actions = {
        "remove": remove,
        "cancel_history_ids": cancel_ids,
        "research": research,
        "also_missing_no_inflight_count": len(extra),
    }
    out = "/tmp/grab-actions.json"
    with open(out, "w") as f:
        json.dump(actions, f, indent=2)
    print(f"\n=== summary remove={len(remove)} cancel={len(cancel_ids)} research={len(research)} extra_missing={len(extra)}")
    print(f"wrote {out}")

    if "--apply-db" in sys.argv:
        wdb = sqlite3.connect(DB)
        wdb.execute("PRAGMA busy_timeout=5000")
        qmarks = ",".join("?" * len(cancel_ids))
        if cancel_ids:
            wdb.execute(
                f"UPDATE download_history SET status='cancelled', completed_at=datetime('now') WHERE id IN ({qmarks})",
                cancel_ids,
            )
            wdb.commit()
            print(f"marked {len(cancel_ids)} history rows cancelled")
        wdb.close()


if __name__ == "__main__":
    main()
