import json
import os
import re
import subprocess

from chunker import REPO_PATHS, chunk_file
from store import Store


def build_wiki_cross_refs(store: Store, chunks: list):
    """Link wiki sections to Go packages/functions they reference."""
    go_pkgs = set()
    for ch in chunks:
        if ch.get("repo") == "core" and ch.get("chunk_type") in ("package", "file"):
            go_pkgs.add(ch.get("name", ""))

    wiki_chunks = [c for c in chunks if c.get("repo") == "wiki" and c.get("chunk_type") == "section"]

    for wc in wiki_chunks:
        content = wc.get("content", "")
        # Match backtick references like `internal/events`, `EventBus`
        refs = re.findall(r'`([^`]+)`', content)
        for ref in refs:
            ref_lower = ref.lower()
            matched = False
            # Try to find Go chunk by name
            for gc in chunks:
                if gc.get("repo") != "core":
                    continue
                gc_name = (gc.get("name") or "").lower()
                gc_path = (gc.get("file_path") or "").lower()
                if ref_lower in gc_name or ref_lower in gc_path:
                    store.graph_conn.execute(
                        "INSERT OR IGNORE INTO edges (source_id, target_id, relation, weight, metadata) VALUES (?,?,?,?,?)",
                        (wc["id"], gc["id"], "documents", 0.7,
                         json.dumps({"ref": ref, "match_type": "name"}))
                    )
                    matched = True
            if not matched:
                # Link to broader package if ref looks like a package path
                for pkg_name in go_pkgs:
                    if pkg_name and ref_lower in pkg_name.lower():
                        pkg_id = f"pkg:{pkg_name}"
                        store.graph_conn.execute(
                            "INSERT OR IGNORE INTO edges (source_id, target_id, relation, weight, metadata) VALUES (?,?,?,?,?)",
                            (wc["id"], pkg_id, "documents", 0.5,
                             json.dumps({"ref": ref, "match_type": "package"}))
                        )
                        break


def build_markdown_links(store: Store, chunks: list):
    """Extract wiki-to-wiki and doc-to-doc cross-links."""
    for ch in chunks:
        if ch.get("chunk_type") != "section":
            continue
        content = ch.get("content", "")
        for link in re.finditer(r'\[([^\]]+)\]\(([^)]+)\)', content):
            target = link.group(2)
            text = link.group(1)
            if target.startswith("http"):
                continue
            target_name = os.path.splitext(os.path.basename(target))[0].replace("-", " ")
            for other in chunks:
                if other["id"] == ch["id"]:
                    continue
                other_name = (other.get("name") or "").lower()
                if target_name.lower() in other_name or other_name in target_name.lower():
                    store.graph_conn.execute(
                        "INSERT OR IGNORE INTO edges (source_id, target_id, relation, weight, metadata) VALUES (?,?,?,?,?)",
                        (ch["id"], other["id"], "see_also", 0.5,
                         json.dumps({"link_text": text, "href": target}))
                    )
                    break


def build_call_graph_substitute(store: Store, chunks: list):
    """Link function chunks to other function chunks within same package by name reference."""
    funcs_by_pkg = {}
    for ch in chunks:
        if ch.get("chunk_type") in ("function", "method"):
            pkg = ch.get("package", "")
            funcs_by_pkg.setdefault(pkg, []).append(ch)

    for pkg, funcs in funcs_by_pkg.items():
        for fch in funcs:
            content = fch.get("content", "")
            for other in funcs:
                if other["id"] == fch["id"]:
                    continue
                other_name = other.get("name", "").split(".")[-1]
                if other_name and other_name in content and len(other_name) > 2:
                    # Heuristic: name appears in the function body
                    store.graph_conn.execute(
                        "INSERT OR IGNORE INTO edges (source_id, target_id, relation, weight, metadata) VALUES (?,?,?,?,?)",
                        (fch["id"], other["id"], "references", 0.3,
                         json.dumps({"type": "name_in_body"}))
                    )


def build_all(store: Store, chunks: list):
    print("  building knowledge graph cross-references...")
    build_wiki_cross_refs(store, chunks)
    build_markdown_links(store, chunks)
    build_call_graph_substitute(store, chunks)
    store.graph_conn.commit()
