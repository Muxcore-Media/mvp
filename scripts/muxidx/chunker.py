import hashlib
import json
import os
import re
from pathlib import Path

from tree_sitter import Language, Parser
import tree_sitter_go as tsgo

WORKSPACE_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "../.."))

REPO_PATHS: dict[str, str] = {}
REPO_TAGS: dict[str, list[str]] = {}
REPO_NAMES: dict[str, str] = {}  # module_dir -> display_name from muxcore.json


def _discover_repos():
    """Auto-discover all repos (core, wiki, modules) at workspace root."""
    builtins = {
        "core": os.path.join(WORKSPACE_ROOT, "core"),
        "wiki": os.path.join(WORKSPACE_ROOT, "core.wiki"),
    }
    EXCLUDED = {".git", ".venv", ".muxidx", ".claude", "core", "core.wiki",
                "scripts", "notes", "docs", "node_modules"}

    for name, path in builtins.items():
        if os.path.isdir(path):
            REPO_PATHS[name] = os.path.abspath(path)

    for entry in sorted(os.listdir(WORKSPACE_ROOT)):
        if entry.startswith(".") or entry in EXCLUDED:
            continue
        d = os.path.join(WORKSPACE_ROOT, entry)
        if not os.path.isdir(d):
            continue
        # Detect module: has go.mod, muxcore.json, Cargo.toml, or main.go
        if not any(os.path.isfile(os.path.join(d, f))
                   for f in ("go.mod", "muxcore.json", "Cargo.toml", "main.py")):
            continue
        REPO_PATHS[entry] = os.path.abspath(d)
        # Load tags from muxcore.json
        _load_module_tags(entry, d)


def _load_module_tags(repo_name: str, repo_path: str):
    muxcore = os.path.join(repo_path, "muxcore.json")
    tags = []
    display = repo_name
    if os.path.isfile(muxcore):
        try:
            with open(muxcore) as f:
                meta = json.load(f)
            tags = meta.get("capabilities", [])
            if not tags:
                tags = meta.get("roles", [])
            display = meta.get("name", repo_name)
        except Exception:
            pass
    if not tags:
        tags = [repo_name.replace("-", ".")]
    REPO_TAGS[repo_name] = tags
    REPO_NAMES[repo_name] = display


# Run discovery at import time
_discover_repos()
REPO_TAG_INDEX: dict[str, list[str]] = {}  # tag -> [repo_name, ...]
for rname, rtags in REPO_TAGS.items():
    for t in rtags:
        REPO_TAG_INDEX.setdefault(t, []).append(rname)
REPO_LIST = sorted(REPO_PATHS.keys())

IGNORE_DIRS = {".git", "node_modules", "vendor", ".venv", "__pycache__", "proto/gen"}
IGNORE_EXTS = {".sum", ".mod", ".gitignore", ".dockerignore"}

GO_LANGUAGE = Language(tsgo.language())
GO_PARSER = Parser(GO_LANGUAGE)


def chunk_id(file_path, pkg, name, kind, start, end):
    raw = f"{file_path}:{pkg}:{name}:{kind}:{start}:{end}"
    return hashlib.sha256(raw.encode()).hexdigest()[:32]


def get_git_sha(file_path):
    try:
        import subprocess
        result = subprocess.run(
            ["git", "log", "-1", "--format=%H", "--", file_path],
            capture_output=True, text=True, timeout=5,
            cwd=os.path.dirname(file_path) or ".",
        )
        sha = result.stdout.strip()
        return sha if sha else None
    except Exception:
        return None


def resolve_repo(file_path):
    abs_path = os.path.abspath(file_path)
    for name, repo_root in REPO_PATHS.items():
        if abs_path.startswith(repo_root):
            rel = os.path.relpath(abs_path, repo_root)
            return name, rel
    return "external", abs_path


def _ts_find(node, kind):
    if not node:
        return None
    for c in node.children:
        if c.type == kind:
            return c
    return None


def _ts_find_all(node, kind):
    if not node:
        return []
    return [c for c in node.children if c.type == kind]


class Chunker:
    def __init__(self, file_path):
        self.file_path = os.path.abspath(file_path)
        self.repo, self.rel_path = resolve_repo(file_path)
        self.ext = os.path.splitext(file_path)[1].lower()
        self.git_sha = get_git_sha(file_path)
        self.last_modified = None
        try:
            self.last_modified = os.path.getmtime(file_path)
        except OSError:
            pass
        with open(file_path, "rb") as f:
            self.raw_bytes = f.read()
        self.content = self.raw_bytes.decode("utf-8", errors="replace")
        self.lines = self.content.split("\n")
        self.chunks = []
        self.edges = []

    def run(self):
        if self.ext == ".go":
            self._chunk_go()
        elif self.ext == ".proto":
            self._chunk_proto()
        elif self.ext == ".md":
            self._chunk_markdown()
        else:
            self._chunk_file()
        return self.chunks, self.edges

    def _node_text(self, node):
        return self.raw_bytes[node.start_byte:node.end_byte].decode("utf-8", errors="replace")

    def _make_chunk(self, chunk_type, name, start_line, end_line, content_text):
        cid = chunk_id(self.rel_path, self.repo, name, chunk_type, start_line, end_line)
        self.chunks.append({
            "id": cid,
            "file_path": self.rel_path,
            "repo": self.repo,
            "chunk_type": chunk_type,
            "name": name,
            "package": self.repo,
            "start_line": start_line,
            "end_line": end_line,
            "content": content_text,
            "git_sha": self.git_sha,
            "last_modified": self.last_modified,
            "tags": ",".join(REPO_TAGS.get(self.repo, [self.repo])),
        })
        return cid

    def _add_edge(self, source, target, relation, weight=1.0, metadata=None):
        self.edges.append({
            "source": source, "target": target, "relation": relation,
            "weight": weight, "metadata": json.dumps(metadata) if metadata else "",
        })

    def _chunk_file(self):
        self._make_chunk("file", self.rel_path, 0, len(self.lines), self.content)

    # ---- Go via tree-sitter ----
    def _chunk_go(self):
        tree = GO_PARSER.parse(self.raw_bytes)
        root = tree.root_node

        file_cid = self._make_chunk("file", self.rel_path, 0, len(self.lines), self.content)

        pkg_name = "unknown"
        pkg_node = _ts_find(root, "source_file")
        # package clause
        for c in root.children:
            if c.type == "package_clause":
                name_node = _ts_find(c, "package_identifier")
                if name_node:
                    pkg_name = self._node_text(name_node)
                break

        pkg_cid = chunk_id(self.rel_path, self.repo, pkg_name, "package", 0, 0)
        self._add_edge(file_cid, pkg_cid, "belongs_to")

        # Imports
        for c in root.children:
            if c.type == "import_declaration":
                for spec in _ts_find_all(c, "import_spec"):
                    path_node = _ts_find(spec, "interpreted_string_literal")
                    name_node = _ts_find(spec, "package_identifier")
                    if path_node:
                        imp_path = self._node_text(path_node).strip('"')
                        imp_name = self._node_text(name_node) if name_node else imp_path.split("/")[-1]
                        meta = json.dumps({"name": imp_name, "path": imp_path})
                        self._add_edge(file_cid, imp_path, "imports", metadata={"name": imp_name, "path": imp_path})
                        self._add_edge(pkg_cid, imp_path, "imports", metadata={"name": imp_name, "path": imp_path})

        # Type declarations
        for c in root.children:
            if c.type == "type_declaration":
                for ts in _ts_find_all(c, "type_spec"):
                    name_node = _ts_find(ts, "type_identifier")
                    if not name_node:
                        continue
                    type_name = self._node_text(name_node)
                    start = ts.start_point[0]
                    end = ts.end_point[0]
                    text = self._node_text(ts)
                    type_cid = self._make_chunk("type", type_name, start, end, text)
                    self._add_edge(file_cid, type_cid, "defines_type")

                    struct_body = _ts_find(ts, "struct_type")
                    if struct_body:
                        self._extract_struct_fields(type_name, type_cid, struct_body)

                    iface_body = _ts_find(ts, "interface_type")
                    if iface_body:
                        self._extract_interface_refs(type_name, type_cid, iface_body)

        # Function and method declarations
        for c in root.children:
            if c.type == "function_declaration":
                self._extract_func(c, file_cid, pkg_name, False)
            elif c.type == "method_declaration":
                self._extract_func(c, file_cid, pkg_name, True)

    def _extract_func(self, node, file_cid, pkg_name, is_method):
        name_node = None
        for c in node.children:
            if c.type == "field_identifier":
                name_node = c
                break
            if not is_method and c.type == "identifier":
                name_node = c

        if not name_node:
            return

        func_name = self._node_text(name_node)

        if is_method:
            recv_param = None
            for c in node.children:
                if c.type == "parameter_list" and recv_param is None:
                    recv_param = c
                    break
            if recv_param:
                for pd in _ts_find_all(recv_param, "parameter_declaration"):
                    type_node = _ts_find(pd, "type_identifier")
                    if type_node:
                        recv_type = self._node_text(type_node)
                        func_name = f"({recv_type}).{func_name}"
                        break

        start = node.start_point[0]
        end = node.end_point[0]
        text = self._node_text(node)
        kind = "method" if is_method else "function"
        cid = self._make_chunk(kind, func_name, start, end, text)
        self._add_edge(file_cid, cid, "contains")

        # Extract calls
        self._extract_calls(node, cid, pkg_name)

    def _extract_calls(self, node, source_cid, pkg_name):
        for call in _ts_find_all(node, "call_expression"):
            ident = _ts_find(call, "identifier")
            if ident:
                target_name = self._node_text(ident)
                self._add_edge(source_cid, f"func:{pkg_name}.{target_name}", "calls", weight=0.8)
                continue
            sel = _ts_find(call, "selector_expression")
            if sel:
                operand = _ts_find(sel, "identifier")
                field = _ts_find(sel, "field_identifier")
                if operand and field:
                    pkg_ref = self._node_text(operand)
                    func_ref = self._node_text(field)
                    self._add_edge(source_cid, f"func:{pkg_ref}.{func_ref}", "calls",
                                   weight=0.7, metadata={"qualified": f"{pkg_ref}.{func_ref}"})

    def _extract_struct_fields(self, struct_name, struct_cid, node):
        for fd in _ts_find_all(node, "field_declaration"):
            type_node = _ts_find(fd, "type_identifier") or _ts_find(fd, "qualified_type")
            if not type_node:
                continue
            field_type = self._node_text(type_node)
            names = _ts_find_all(fd, "field_identifier")
            if not names:
                self._add_edge(struct_cid, f"type:{field_type}", "embeds")
            else:
                for fn in names:
                    self._add_edge(struct_cid, f"type:{field_type}", "composes",
                                   metadata={"field": self._node_text(fn)})

    def _extract_interface_refs(self, iface_name, iface_cid, node):
        for ms in _ts_find_all(node, "method_spec"):
            qual = _ts_find(ms, "qualified_type")
            if qual:
                qtext = self._node_text(qual)
                self._add_edge(iface_cid, f"type:{qtext.split('.')[-1]}", "extends",
                               metadata={"qualified": qtext})
            name_node = _ts_find(ms, "field_identifier")
            if name_node:
                self._add_edge(iface_cid, f"method:{self._node_text(name_node)}", "declares")

    # ---- Proto ----
    def _chunk_proto(self):
        file_cid = self._make_chunk("file", self.rel_path, 0, len(self.lines), self.content)
        i = 0
        while i < len(self.lines):
            line = self.lines[i]
            m = re.match(r'\s*(message|service|enum|rpc)\s+(\w+)', line)
            if m:
                kind_map = {"message": "message", "service": "service", "enum": "enum", "rpc": "rpc"}
                kind = kind_map[m.group(1)]
                name = m.group(2)
                j = i
                brace = 0
                started = '{' in line
                if '{' in line:
                    brace += line.count('{')
                while j < len(self.lines) - 1:
                    j += 1
                    if '{' in self.lines[j]:
                        brace += self.lines[j].count('{')
                        started = True
                    if started and '}' in self.lines[j]:
                        brace -= self.lines[j].count('}')
                        if brace <= 0:
                            break
                text = "\n".join(self.lines[i:j+1])
                cid = self._make_chunk(kind, name, i, j, text)
                self._add_edge(file_cid, cid, "contains")
                i = j
            i += 1

    # ---- Markdown ----
    def _chunk_markdown(self):
        file_cid = self._make_chunk("file", self.rel_path, 0, len(self.lines), self.content)
        heading_starts = []
        for i, line in enumerate(self.lines):
            if re.match(r'^#{1,6}\s', line):
                heading_starts.append(i)
        if not heading_starts:
            return
        sections = []
        for idx, start in enumerate(heading_starts):
            end = heading_starts[idx + 1] if idx + 1 < len(heading_starts) else len(self.lines)
            sections.append((start, end))
        for start, end in sections:
            heading = self.lines[start].strip()
            name = re.sub(r'^#+\s*', '', heading)
            text = "\n".join(self.lines[start:end])
            cid = self._make_chunk("section", name, start, end - 1, text)
            self._add_edge(file_cid, cid, "contains")
            for link in re.finditer(r'\[([^\]]+)\]\(([^)]+)\)', text):
                self._add_edge(cid, link.group(2), "see_also", weight=0.5,
                               metadata={"text": link.group(1)})


def chunk_file(file_path):
    chunker = Chunker(file_path)
    return chunker.run()
