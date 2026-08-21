#!/usr/bin/env python3
"""Filesystem watcher that triggers incremental re-indexing."""

import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from watchdog.observers import Observer
from watchdog.events import FileSystemEventHandler

from chunker import REPO_PATHS
from main import reindex_changed
from embedder import Embedder
from store import Store

DEBOUNCE_SECONDS = 2.0


class MuxIdxHandler(FileSystemEventHandler):
    def __init__(self):
        self.pending = set()
        self.last_trigger = 0
        self.embedder = Embedder()

    def on_modified(self, event):
        if event.is_directory:
            return
        if not self._is_indexable(event.src_path):
            return
        self.pending.add(event.src_path)
        self._debounce()

    def on_created(self, event):
        if not self._is_indexable(event.src_path):
            return
        self.pending.add(event.src_path)
        self._debounce()

    def on_deleted(self, event):
        if event.is_directory:
            return
        rel = self._get_rel_path(event.src_path)
        if rel:
            store = Store()
            store.remove_file_chunks(rel)
            store.save()
            store.close()
            print(f"  removed chunks for {rel}")

    def _is_indexable(self, path):
        ext = os.path.splitext(path)[1].lower()
        return ext in (".go", ".proto", ".md", ".json", ".yaml", ".yml")

    def _get_rel_path(self, path):
        abs_path = os.path.abspath(path)
        for name, root in REPO_PATHS.items():
            root = os.path.abspath(root)
            if abs_path.startswith(root):
                return os.path.relpath(abs_path, root)
        return None

    def _debounce(self):
        now = time.time()
        if now - self.last_trigger < DEBOUNCE_SECONDS:
            return
        self.last_trigger = now
        if self.pending:
            files = list(self.pending)
            self.pending.clear()
            print(f"  watching: {len(files)} files changed, re-indexing...")
            store = Store()
            reindex_changed(store, self.embedder, files)
            store.close()


def start_watching():
    print("muxidx watch: starting file watcher...")
    event_handler = MuxIdxHandler()
    observer = Observer()
    for name, path in REPO_PATHS.items():
        if os.path.isdir(path):
            observer.schedule(event_handler, path, recursive=True)
            print(f"  watching {name}: {path}")

    observer.start()
    try:
        while True:
            time.sleep(1)
    except KeyboardInterrupt:
        observer.stop()
    observer.join()


if __name__ == "__main__":
    start_watching()
