package game

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"

	"github.com/kjFriday/local-verse-proxy/internal/mintkey"
)

// asLvp is the test program as `lvp mint-key ...`: the same code the
// real lvp runs for it.
func asLvp(argv []string) int {
	if len(argv) == 0 || argv[0] != "mint-key" {
		os.Stderr.WriteString("test lvp: only mint-key\n")
		return 2
	}
	return mintkey.Main(argv[1:], mintkey.OS(), os.Stdout, os.Stderr)
}

// dockerCall is what asDocker sends the test, and what it answers.
type dockerCall struct {
	Argv  []string `json:"argv"`
	Dir   string   `json:"dir"`
	Stdin string   `json:"stdin"`
}

type dockerAnswer struct {
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
	Code   int    `json:"code"`
}

// asDocker is the test program as `docker compose exec ... psql`: it
// sends its arguments and input to the test (GAMETEST_DOCKER_URL),
// which answers from its fake proxy, as the real database would.
func asDocker(argv []string) int {
	in, _ := io.ReadAll(os.Stdin)
	wd, _ := os.Getwd()
	b, _ := json.Marshal(dockerCall{argv, wd, string(in)})
	resp, err := http.Post(os.Getenv("GAMETEST_DOCKER_URL"),
		"application/json", bytes.NewReader(b))
	if err != nil {
		os.Stderr.WriteString("test docker: " + err.Error() + "\n")
		return 1
	}
	defer resp.Body.Close()
	var a dockerAnswer
	if err := json.NewDecoder(resp.Body).Decode(&a); err != nil {
		os.Stderr.WriteString("test docker: " + err.Error() + "\n")
		return 1
	}
	os.Stdout.WriteString(a.Stdout)
	os.Stderr.WriteString(a.Stderr)
	return a.Code
}
