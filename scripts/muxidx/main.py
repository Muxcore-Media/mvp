#!/usr/bin/env python3
"""muxidx — MuxCore vector knowledge graph search."""

import json
import os
import sys
import time

import click

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from chunker import Chunker, REPO_PATHS, REPO_TAGS, REPO_LIST, IGNORE_DIRS, IGNORE_EXTS
from embedder import Embedder
from store import Store, load_manifest, save_manifest, MUXIDX_DIR
from graph import build_all

EMBED_CHUNK_TYPES = {"function", "method", "type", "variable", "section", "message", "service", "enum", "rpc"}


def walk_repo_files(repo_name, repo_root):
    for root, dirs, files in os.walk(repo_root):
        dirs[:] = [d for d in dirs if d not in IGNORE_DIRS]
        for fname in files:
            ext = os.path.splitext(fname)[1].lower()
            if ext in IGNORE_EXTS:
                continue
            fpath = os.path.join(root, fname)
            rel = os.path.relpath(fpath, repo_root)
            yield rel, fpath


def index_all(store, embedder, repos=None):
    if repos is None:
        repos = list(REPO_PATHS.keys())

    all_chunks = []
    all_edges = []
    all_texts = []
    embed_indices = []

    for repo_name in repos:
        repo_root = REPO_PATHS.get(repo_name)
        if not repo_root or not os.path.isdir(repo_root):
            print(f"  warning: repo '{repo_name}' not found at {repo_root}, skipping")
            continue
        print(f"  indexing {repo_name} from {repo_root}")
        for rel, fpath in walk_repo_files(repo_name, repo_root):
            try:
                chunks, edges = Chunker(fpath).run()
                for c in chunks:
                    c["repo"] = repo_name
                all_chunks.extend(chunks)
                all_edges.extend(edges)
                for i, c in enumerate(chunks):
                    if c["chunk_type"] in EMBED_CHUNK_TYPES:
                        embed_indices.append(len(all_chunks) - len(chunks) + i)
                        all_texts.append(c["content"])
            except Exception as e:
                print(f"    error chunking {rel}: {e}")

    print(f"  generated {len(all_chunks)} chunks, {len(all_edges)} edges, {len(all_texts)} to embed")

    if not all_chunks:
        print("  no chunks generated, nothing to index")
        return

    if all_texts:
        print("  embedding chunks...")
        embeddings = embedder.embed(all_texts)
        embed_map = dict(zip(embed_indices, embeddings))
    else:
        embed_map = {}

    print("  storing in vector index...")
    embed_chunks = [all_chunks[i] for i in embed_indices]
    embed_vectors = [embed_map[i] for i in embed_indices]
    store.insert_chunks(embed_chunks, embed_vectors)

    store.insert_edges(all_edges)

    build_all(store, all_chunks)

    manifest = load_manifest()
    for ch in all_chunks:
        path = ch["file_path"]
        if path not in manifest["files"]:
            manifest["files"][path] = {}
        manifest["files"][path] = {
            "git_sha": ch.get("git_sha"),
            "last_modified": ch.get("last_modified"),
            "chunk_ids": [ch["id"]],
        }
    manifest["last_indexed"] = time.time()
    manifest["total_chunks"] = len(all_chunks)
    save_manifest(manifest)
    store.save()

    print(f"  done — {len(all_chunks)} chunks indexed ({len(embed_vectors)} embedded)")


def reindex_changed(store, embedder, changed_files):
    if not changed_files:
        return

    all_chunks = []
    all_edges = []
    all_texts = []
    embed_indices = []

    for fpath in changed_files:
        if not os.path.isfile(fpath):
            continue
        ext = os.path.splitext(fpath)[1].lower()
        if ext not in (".go", ".proto", ".md", ".json", ".yaml", ".yml"):
            continue
        repo_name = None
        for rname, rroot in REPO_PATHS.items():
            if os.path.abspath(fpath).startswith(os.path.abspath(rroot)):
                repo_name = rname
                break
        if not repo_name:
            continue

        rel = os.path.relpath(fpath, REPO_PATHS[repo_name])
        print(f"  re-indexing {rel}")

        store.remove_file_chunks(rel)
        try:
            chunks, edges = Chunker(fpath).run()
            for c in chunks:
                c["repo"] = repo_name
            all_chunks.extend(chunks)
            all_edges.extend(edges)
            for i, c in enumerate(chunks):
                if c["chunk_type"] in EMBED_CHUNK_TYPES:
                    embed_indices.append(len(all_chunks) - len(chunks) + i)
                    all_texts.append(c["content"])
        except Exception as e:
            print(f"    error: {e}")

    if all_chunks:
        if all_texts:
            print(f"  embedding {len(all_texts)} new chunks...")
            embeddings = embedder.embed(all_texts)
            embed_map = dict(zip(embed_indices, embeddings))
        else:
            embed_map = {}

        embed_chunks = [all_chunks[i] for i in embed_indices]
        embed_vectors = [embed_map[i] for i in embed_indices]
        store.insert_chunks(embed_chunks, embed_vectors)
        store.insert_edges(all_edges)
        build_all(store, all_chunks)
        manifest = load_manifest()
        for ch in all_chunks:
            path = ch["file_path"]
            manifest["files"][path] = {
                "git_sha": ch.get("git_sha"),
                "last_modified": ch.get("last_modified"),
                "chunk_ids": [ch["id"]],
            }
        manifest["last_indexed"] = time.time()
        save_manifest(manifest)
        store.save()
        print(f"  re-indexed {len(all_chunks)} chunks")


@click.group()
def cli():
    pass


@cli.command()
@click.option("--repos", multiple=True)
def index(repos):
    print("muxidx: building full index...")
    print(f"  discovered repos ({len(REPO_PATHS)}):")
    for r in REPO_LIST:
        tags = REPO_TAGS.get(r, [])
        tags_str = f" [{', '.join(tags)}]" if tags else ""
        print(f"    {r}{tags_str}")
    store = Store()
    embedder = Embedder()
    index_all(store, embedder, repos=repos or None)
    store.close()


@cli.command()
@click.option("--paths", multiple=True)
@click.option("--git-dir")
def reindex(paths, git_dir):
    changed = list(paths)
    if git_dir:
        try:
            import subprocess
            result = subprocess.run(
                ["git", "diff", "--name-only", "HEAD~1", "--diff-filter=ACM"],
                capture_output=True, text=True, timeout=30, cwd=git_dir,
            )
            for line in result.stdout.strip().split("\n"):
                line = line.strip()
                if line:
                    full = os.path.join(git_dir, line)
                    if os.path.isfile(full):
                        changed.append(full)
        except Exception:
            pass
    if not changed:
        print("no files to re-index")
        return
    print(f"muxidx: re-indexing {len(changed)} files...")
    store = Store()
    embedder = Embedder()
    reindex_changed(store, embedder, changed)
    store.close()


@cli.command()
@click.argument("query_str")
@click.option("--repo", help="Filter by repo name or tag (e.g. 'core', 'auth', 'cache-redis'). Comma-separated for multiple.")
@click.option("--top-k", default=10)
@click.option("--chunk-type", multiple=True)
@click.option("--graph/--no-graph", default=True)
@click.option("--json-output", is_flag=True)
def query(query_str, repo, top_k, chunk_type, graph, json_output):
    store = Store()
    embedder = Embedder()
    emb = embedder.embed_one(query_str)
    results = store.search(emb, top_k=top_k, repo=repo, chunk_types=chunk_type or None)
    if graph:
        for r in results:
            neighbors = store.get_graph_neighbors(r["chunk_id"], max_depth=1)
            r["graph_neighbors"] = neighbors[:5]
    store.close()
    if json_output:
        click.echo(json.dumps(results, indent=2, default=str))
        return
    if not results:
        click.echo("No results found.")
        return
    for i, r in enumerate(results):
        click.echo(f"\n--- [{i+1}] score={r['score']:.4f} ---")
        click.echo(f"  {r['repo']}/{r['file_path']}:{r.get('start_line', '?')}-{r.get('end_line', '?')}")
        click.echo(f"  [{r['chunk_type']}] {r['name']}")
        content = r["content"]
        if len(content) > 300:
            content = content[:300] + "..."
        click.echo(f"  {content}")
        if "graph_neighbors" in r and r["graph_neighbors"]:
            for n in r["graph_neighbors"]:
                click.echo(f"    -> [{n['relation']}] {n['chunk_id'][:16]}...")


@cli.command()
@click.argument("chunk_id")
@click.option("--relation")
@click.option("--max-depth", default=2)
@click.option("--json-output", is_flag=True)
def graph_walk(chunk_id, relation, max_depth, json_output):
    store = Store()
    results = store.get_graph_neighbors(chunk_id, relation=relation, max_depth=max_depth)
    store.close()
    if json_output:
        click.echo(json.dumps(results, indent=2, default=str))
        return
    for r in results:
        click.echo(f"  [{r['relation']:>12}] {r['chunk_id'][:32]}...")


@cli.command()
@click.argument("chunk_id")
@click.option("--json-output", is_flag=True)
def get(chunk_id, json_output):
    store = Store()
    chunk = store.get_chunk(chunk_id)
    store.close()
    if not chunk:
        click.echo("chunk not found")
        return
    if json_output:
        click.echo(json.dumps(chunk, indent=2, default=str))
        return
    click.echo(f"  {chunk['repo']}/{chunk['file_path']}:{chunk['start_line']}-{chunk['end_line']}")
    click.echo(f"  [{chunk['chunk_type']}] {chunk['name']}")


@cli.command()
def stats():
    store = Store()
    s = store.stats()
    store.close()
    click.echo("muxidx index stats:")
    click.echo(f"  Total chunks:    {s['total_chunks']}")
    click.echo(f"  FAISS vectors:   {s['faiss_vectors']}")
    click.echo(f"  Graph nodes:     {s['total_nodes']}")
    click.echo(f"  Graph edges:     {s['total_edges']}")
    click.echo(f"  By repo:         {s['by_repo']}")
    click.echo(f"  By type:         {s['by_type']}")


@cli.command()
def watch():
    from watcher import start_watching
    start_watching()


@cli.command()
def mcp():
    from mcp_server import main as mcp_main
    mcp_main()


@cli.command()
@click.argument("file_path")
def extract(file_path):
    chunks, edges = Chunker(file_path).run()
    click.echo(json.dumps({"chunks": chunks, "edges": edges}, indent=2, default=str))


if __name__ == "__main__":
    cli()
