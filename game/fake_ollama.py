"""Practice model server for the office game: a tiny Ollama look-alike.

It does no AI at all. It answers with filler text, but it counts tokens
like a real model (about 4 characters per token), so a long prompt
really costs more mana. Standard library only.

Speaks just enough of the Ollama API for LiteLLM: /, /api/version,
/api/tags, /api/show and /api/chat (streamed and not).
"""
import json
import random
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PORT = 11434
MODELS = ("office-local", "office-pro")
WORDS = ("draft report review ticket merge spec deck budget memo plan "
         "test fix note sync chart email summary agenda invoice").split()


def tokens(text):
    # Rough rule of thumb: one token is about 4 characters.
    return max(1, round(len(text) / 4))


def prompt_text(messages):
    parts = []
    for m in messages or []:
        c = m.get("content") or ""
        if isinstance(c, list):  # OpenAI-style content parts
            c = " ".join(p.get("text", "") for p in c if isinstance(p, dict))
        parts.append(str(c))
    return "\n".join(parts)


def filler(n_tokens):
    out, size = [], 0
    while size < n_tokens * 4:
        w = random.choice(WORDS)
        out.append(w)
        size += len(w) + 1
    return " ".join(out).capitalize() + "."


def now():
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())


class Handler(BaseHTTPRequestHandler):
    def send(self, code, body, ctype="application/json"):
        data = body if isinstance(body, bytes) else json.dumps(body).encode()
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        if self.path == "/":
            return self.send(200, b"Ollama is running", "text/plain")
        if self.path == "/api/version":
            return self.send(200, {"version": "0.0.0-practice"})
        if self.path == "/api/tags":
            return self.send(200, {"models": [
                {"name": m + ":latest", "model": m + ":latest"}
                for m in MODELS]})
        self.send(404, {"error": "not found"})

    def do_POST(self):
        n = int(self.headers.get("Content-Length") or 0)
        try:
            req = json.loads(self.rfile.read(n) or b"{}")
        except ValueError:
            return self.send(400, {"error": "bad json"})
        if self.path == "/api/show":
            return self.send(200, {
                "modelfile": "", "parameters": "",
                "template": "{{ .Prompt }}",
                "details": {"family": "llama", "format": "gguf"},
                "model_info": {"llama.context_length": 32768},
                "capabilities": ["completion"]})
        if self.path == "/api/chat":
            return self.chat(req)
        self.send(404, {"error": "not found"})

    def chat(self, req):
        model = req.get("model", MODELS[0])
        p_tok = tokens(prompt_text(req.get("messages")))
        limit = int((req.get("options") or {}).get("num_predict") or 128)
        limit = max(1, min(limit, 4096))
        reply = filler(random.randint(max(1, limit // 2), limit))
        r_tok = tokens(reply)
        # Small random think time, a bit longer for long answers.
        time.sleep(random.uniform(0.05, 0.25) + r_tok * 0.0005)
        last = {"model": model, "created_at": now(), "done": True,
                "done_reason": "stop", "prompt_eval_count": p_tok,
                "eval_count": r_tok}
        if not req.get("stream"):
            last["message"] = {"role": "assistant", "content": reply}
            return self.send(200, last)
        # Streamed: one JSON object per line, then a final "done" line.
        lines = []
        words = reply.split(" ")
        for i in range(0, len(words), 8):
            chunk = " ".join(words[i:i + 8]) + " "
            lines.append({"model": model, "created_at": now(),
                          "done": False, "message": {
                              "role": "assistant", "content": chunk}})
        last["message"] = {"role": "assistant", "content": ""}
        lines.append(last)
        body = "".join(json.dumps(x) + "\n" for x in lines).encode()
        self.send(200, body, "application/x-ndjson")

    def log_message(self, *args):
        pass  # quiet: never log prompts


if __name__ == "__main__":
    print(f"practice model server on :{PORT}", flush=True)
    ThreadingHTTPServer(("0.0.0.0", PORT), Handler).serve_forever()
