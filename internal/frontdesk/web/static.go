package web

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"path"

	"github.com/kjFriday/local-verse-proxy/gui"
)

// staticFile is one of the page's files, zipped once.
type staticFile struct {
	body, gz []byte
	etag     string
	ctype    string
}

var types = map[string]string{
	".html": "text/html; charset=utf-8",
	".js":   "text/javascript; charset=utf-8",
	".css":  "text/css; charset=utf-8",
	".svg":  "image/svg+xml",
}

// loadStatic reads the embedded page files: "/<name>", and "/" for
// index.html. Each is gzipped at level 9; its ETag comes from its
// bytes (ledger F-4: Python's BLAKE2 values differ).
func loadStatic(files fs.FS) (map[string]staticFile, error) {
	if files == nil {
		files = gui.Static
	}
	out := map[string]staticFile{}
	entries, err := fs.ReadDir(files, "static")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		ct, ok := types[path.Ext(e.Name())]
		if !ok || e.IsDir() {
			continue
		}
		body, err := fs.ReadFile(files, "static/"+e.Name())
		if err != nil {
			return nil, err
		}
		var b bytes.Buffer
		w, _ := gzip.NewWriterLevel(&b, 9)
		w.Write(body)
		w.Close()
		sum := sha256.Sum256(body)
		out["/"+e.Name()] = staticFile{body, b.Bytes(),
			`"` + hex.EncodeToString(sum[:8]) + `"`, ct}
	}
	if f, ok := out["/index.html"]; ok {
		out["/"] = f
	}
	return out, nil
}

// StaticPaths are the page's paths, for tests.
func (a *App) StaticPaths() []string {
	var out []string
	for p := range a.static {
		out = append(out, p)
	}
	return out
}
