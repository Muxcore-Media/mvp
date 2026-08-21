import json
import time
import ollama

DEFAULT_MODEL = "nomic-embed-text"
BATCH_SIZE = 32
MAX_RETRIES = 3
RETRY_DELAY = 2
MAX_CHARS = 6000  # ~1500 tokens, safe for 2048 context window


class Embedder:
    def __init__(self, model=DEFAULT_MODEL):
        self.model = model
        self._ensure_model()

    def _ensure_model(self):
        try:
            ollama.embeddings(model=self.model, prompt="test")
        except ollama.ResponseError as e:
            if "not found" in str(e).lower():
                print(f"Pulling {self.model} model (first time may take a minute)...")
                ollama.pull(self.model)

    def _truncate(self, text):
        if len(text) > MAX_CHARS:
            return text[:MAX_CHARS] + "..."
        return text

    def embed(self, texts):
        all_embeddings = []
        for i in range(0, len(texts), BATCH_SIZE):
            batch = texts[i : i + BATCH_SIZE]
            batch_embeddings = []
            for text in batch:
                emb = self._embed_with_retry(self._truncate(text))
                batch_embeddings.append(emb)
            all_embeddings.extend(batch_embeddings)
            if i + BATCH_SIZE < len(texts):
                print(f"  embedded {i + len(batch)}/{len(texts)} chunks")
        return all_embeddings

    def _embed_with_retry(self, text):
        for attempt in range(MAX_RETRIES):
            try:
                resp = ollama.embeddings(model=self.model, prompt=text)
                return resp["embedding"]
            except ollama.ResponseError as e:
                if "context length" in str(e).lower():
                    shorter = text[:len(text)//2]
                    return self._embed_with_retry(shorter)
                if attempt < MAX_RETRIES - 1:
                    time.sleep(RETRY_DELAY)
                    continue
                raise

    def embed_one(self, text):
        return self._embed_with_retry(self._truncate(text))
