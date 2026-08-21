import json
import os
import re
import sqlite3
import numpy as np

try:
    import faiss
    HAS_FAISS = True
except ImportError:
    HAS_FAISS = False

MUXIDX_DIR = os.path.abspath(os.path.join(os.path.dirname(__file__), "../../.muxidx"))
CHUNKS_DB = os.path.join(MUXIDX_DIR, "chunks.db")
GRAPH_DB = os.path.join(MUXIDX_DIR, "graph.db")
FAISS_INDEX = os.path.join(MUXIDX_DIR, "faiss.index")
EMBEDDINGS_NPY = os.path.join(MUXIDX_DIR, "embeddings.npy")
MANIFEST_PATH = os.path.join(MUXIDX_DIR, "index_meta.json")
DIMENSION = 768

# Import repo/tag index for search resolution
try:
    from chunker import REPO_PATHS, REPO_TAGS, REPO_TAG_INDEX
except ImportError:
    REPO_PATHS = {}
    REPO_TAGS = {}
    REPO_TAG_INDEX = {}


def ensure_dir():
    os.makedirs(MUXIDX_DIR, exist_ok=True)


class Store:
    def __init__(self):
        ensure_dir()
        self.chunks_conn = sqlite3.connect(CHUNKS_DB)
        self.graph_conn = sqlite3.connect(GRAPH_DB)
        self._init_schema()
        self.faiss_index = None
        self._next_faiss_id = 0
        self._load_faiss()

    def _init_schema(self):
        c = self.chunks_conn
        c.execute("""
            CREATE TABLE IF NOT EXISTS chunks (
                id TEXT PRIMARY KEY,
                faiss_id INTEGER UNIQUE,
                file_path TEXT, repo TEXT, chunk_type TEXT,
                name TEXT, package TEXT, tags TEXT,
                start_line INT, end_line INT,
                content TEXT, git_sha TEXT,
                last_modified REAL, token_count INT
            )
        """)
        # Add tags column if it doesn't exist (migration for existing DBs)
        try:
            c.execute("ALTER TABLE chunks ADD COLUMN tags TEXT DEFAULT ''")
        except sqlite3.OperationalError:
            pass  # column already exists
        c.execute("CREATE INDEX IF NOT EXISTS idx_chunks_file ON chunks(file_path)")
        c.execute("CREATE INDEX IF NOT EXISTS idx_chunks_repo ON chunks(repo)")
        c.execute("CREATE INDEX IF NOT EXISTS idx_chunks_type ON chunks(chunk_type)")
        c.execute("CREATE INDEX IF NOT EXISTS idx_chunks_faiss ON chunks(faiss_id)")

        g = self.graph_conn
        g.execute("""
            CREATE TABLE IF NOT EXISTS nodes (
                id TEXT PRIMARY KEY,
                label TEXT, node_type TEXT, metadata TEXT
            )
        """)
        g.execute("""
            CREATE TABLE IF NOT EXISTS edges (
                source_id TEXT, target_id TEXT, relation TEXT,
                weight REAL DEFAULT 1.0, metadata TEXT,
                PRIMARY KEY (source_id, target_id, relation)
            )
        """)
        g.execute("CREATE INDEX IF NOT EXISTS idx_edges_source ON edges(source_id)")
        g.execute("CREATE INDEX IF NOT EXISTS idx_edges_target ON edges(target_id)")
        g.execute("CREATE INDEX IF NOT EXISTS idx_edges_rel ON edges(relation)")

    def _load_faiss(self):
        if not HAS_FAISS:
            self.faiss_index = None
            return
        if os.path.exists(FAISS_INDEX):
            self.faiss_index = faiss.read_index(FAISS_INDEX)
            self._next_faiss_id = self.faiss_index.ntotal
        else:
            self.faiss_index = faiss.IndexIDMap(faiss.IndexFlatIP(DIMENSION))
            self._next_faiss_id = 0

    def insert_chunks(self, chunks, embeddings):
        if not chunks:
            return
        c = self.chunks_conn
        if not HAS_FAISS or self.faiss_index is None:
            return
        ids = np.arange(self._next_faiss_id, self._next_faiss_id + len(chunks), dtype=np.int64)
        emb_array = np.array(embeddings, dtype=np.float32)
        faiss.normalize_L2(emb_array)
        self.faiss_index.add_with_ids(emb_array, ids)

        for i, ch in enumerate(chunks):
            c.execute(
                "INSERT OR REPLACE INTO chunks (id, faiss_id, file_path, repo, chunk_type, name, package, tags, start_line, end_line, content, git_sha, last_modified) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)",
                (ch["id"], int(ids[i]), ch["file_path"], ch["repo"], ch["chunk_type"],
                 ch["name"], ch.get("package", ""), ch.get("tags", ""),
                 ch["start_line"], ch["end_line"],
                 ch["content"], ch.get("git_sha"), ch.get("last_modified"))
            )
        self.chunks_conn.commit()
        self._next_faiss_id += len(chunks)

    def insert_edges(self, edges):
        g = self.graph_conn
        for e in edges:
            try:
                g.execute(
                    "INSERT OR IGNORE INTO edges (source_id, target_id, relation, weight, metadata) VALUES (?,?,?,?,?)",
                    (e["source"], e["target"], e["relation"], e.get("weight", 1.0), e.get("metadata", ""))
                )
                for node_id in (e["source"], e["target"]):
                    g.execute(
                        "INSERT OR IGNORE INTO nodes (id, node_type) VALUES (?, 'chunk')",
                        (node_id,)
                    )
            except Exception:
                pass
        self.graph_conn.commit()

    def remove_file_chunks(self, file_path):
        c = self.chunks_conn
        rows = c.execute("SELECT id, faiss_id FROM chunks WHERE file_path = ?", (file_path,)).fetchall()
        faiss_ids = [r[1] for r in rows if r[1] is not None]
        chunk_ids = [r[0] for r in rows]

        c.execute("DELETE FROM chunks WHERE file_path = ?", (file_path,))
        self.chunks_conn.commit()

        g = self.graph_conn
        for cid in chunk_ids:
            g.execute("DELETE FROM edges WHERE source_id = ? OR target_id = ?", (cid, cid))
        self.graph_conn.commit()

        if faiss_ids and HAS_FAISS and self.faiss_index is not None:
            try:
                self.faiss_index.remove_ids(np.array(faiss_ids, dtype=np.int64))
            except Exception:
                pass

    def _resolve_repos(self, repo_spec):
        """Resolve a repo filter to a set of repo names.
        Supports: exact name, comma-separated names, tag-based expansion."""
        if not repo_spec:
            return None
        repos = set()
        for part in re.split(r'[,\s]+', repo_spec):
            part = part.strip()
            if not part:
                continue
            if part in REPO_PATHS:
                repos.add(part)
            elif part in REPO_TAG_INDEX:
                repos.update(REPO_TAG_INDEX[part])
            else:
                repos.add(part)
        return repos

    def search(self, query_embedding, top_k=10, repo=None, chunk_types=None):
        if not HAS_FAISS or self.faiss_index is None or self.faiss_index.ntotal == 0:
            return []
        emb = np.array([query_embedding], dtype=np.float32)
        faiss.normalize_L2(emb)
        scores, ids = self.faiss_index.search(emb, top_k * 3)
        results = []
        c = self.chunks_conn
        repo_filter = self._resolve_repos(repo)
        for score, faiss_id in zip(scores[0], ids[0]):
            if faiss_id < 0:
                continue
            row = c.execute("SELECT id, file_path, repo, chunk_type, name, package, tags, content, git_sha FROM chunks WHERE faiss_id = ?",
                          (int(faiss_id),)).fetchone()
            if not row:
                continue
            if repo_filter and row[2] not in repo_filter:
                continue
            if chunk_types and row[3] not in chunk_types:
                continue
            results.append({
                "chunk_id": row[0], "file_path": row[1], "repo": row[2],
                "chunk_type": row[3], "name": row[4], "package": row[5],
                "tags": row[6], "content": row[7][:2000], "git_sha": row[8],
                "score": float(score),
            })
            if len(results) >= top_k:
                break
        return results

    def get_chunk(self, chunk_id):
        row = self.chunks_conn.execute(
            "SELECT id, file_path, repo, chunk_type, name, package, start_line, end_line, content, git_sha FROM chunks WHERE id = ?",
            (chunk_id,)
        ).fetchone()
        if not row:
            return None
        return {
            "chunk_id": row[0], "file_path": row[1], "repo": row[2],
            "chunk_type": row[3], "name": row[4], "package": row[5],
            "start_line": row[6], "end_line": row[7], "content": row[8], "git_sha": row[9],
        }

    def get_graph_neighbors(self, chunk_id, relation=None, max_depth=1, direction="both"):
        visited = {chunk_id}
        queue = [(chunk_id, 0)]
        results = []
        g = self.graph_conn
        while queue:
            current, depth = queue.pop(0)
            if depth >= max_depth:
                continue
            if direction in ("out", "both"):
                rows = g.execute(
                    "SELECT target_id, relation, weight, metadata FROM edges WHERE source_id = ?"
                    + (" AND relation = ?" if relation else ""),
                    (current, relation) if relation else (current,)
                ).fetchall()
                for target, rel, weight, meta in rows:
                    if target not in visited:
                        visited.add(target)
                        chunk = self.get_chunk(target)
                        results.append({
                            "chunk_id": target, "relation": rel, "weight": weight,
                            "metadata": meta, "content": chunk["content"][:500] if chunk else None,
                        })
                        queue.append((target, depth + 1))
            if direction in ("in", "both"):
                rows = g.execute(
                    "SELECT source_id, relation, weight, metadata FROM edges WHERE target_id = ?"
                    + (" AND relation = ?" if relation else ""),
                    (current, relation) if relation else (current,)
                ).fetchall()
                for source, rel, weight, meta in rows:
                    if source not in visited:
                        visited.add(source)
                        chunk = self.get_chunk(source)
                        results.append({
                            "chunk_id": source, "relation": rel, "weight": weight,
                            "metadata": meta, "content": chunk["content"][:500] if chunk else None,
                        })
                        queue.append((source, depth + 1))
        return results

    def stats(self):
        c = self.chunks_conn
        total = c.execute("SELECT COUNT(*) FROM chunks").fetchone()[0]
        by_repo = c.execute("SELECT repo, COUNT(*) FROM chunks GROUP BY repo").fetchall()
        by_type = c.execute("SELECT chunk_type, COUNT(*) FROM chunks GROUP BY chunk_type").fetchall()
        edge_count = self.graph_conn.execute("SELECT COUNT(*) FROM edges").fetchone()[0]
        node_count = self.graph_conn.execute("SELECT COUNT(*) FROM nodes").fetchone()[0]
        faiss_count = self.faiss_index.ntotal if HAS_FAISS and self.faiss_index is not None else 0
        return {
            "total_chunks": total,
            "by_repo": dict(by_repo),
            "by_type": dict(by_type),
            "total_edges": edge_count,
            "total_nodes": node_count,
            "faiss_vectors": faiss_count,
        }

    def save(self):
        if HAS_FAISS and self.faiss_index is not None:
            faiss.write_index(self.faiss_index, FAISS_INDEX)
        self.chunks_conn.commit()
        self.graph_conn.commit()

    def close(self):
        self.save()
        self.chunks_conn.close()
        self.graph_conn.close()


def load_manifest():
    ensure_dir()
    if os.path.exists(MANIFEST_PATH):
        with open(MANIFEST_PATH) as f:
            return json.load(f)
    return {"files": {}}


def save_manifest(manifest):
    ensure_dir()
    with open(MANIFEST_PATH, "w") as f:
        json.dump(manifest, f, indent=2)
