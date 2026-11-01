package game

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/kjFriday/local-verse-proxy/internal/pyfmt"
)

// proxyURL is office.py's proxy_url(): LVG_PROXY_URL, else the last
// PROXY_URL= line of seats.env (only that line is read), else
// http://localhost:4000; a trailing / cut.
func (g *game) proxyURL() string {
	url := g.getenv("LVG_PROXY_URL")
	if url == "" {
		b, err := os.ReadFile(filepath.Join(g.root, "seats.env"))
		if err == nil {
			for _, line := range pyfmt.SplitLines(string(b)) {
				if strings.HasPrefix(line, "PROXY_URL=") {
					_, v, _ := strings.Cut(line, "=")
					url = pyfmt.Strip(v)
				}
			}
		}
	}
	if url == "" {
		url = "http://localhost:4000"
	}
	return strings.TrimRight(url, "/")
}
