package game

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain lets the test program stand in for the programs the game
// runs: with GAMETEST_AS=mint it is the goldens' fake mint-key, with
// GAMETEST_AS=lvp it is `lvp mint-key` (lvp_test.go), and named
// docker it is a docker that asks the test for answers.
func TestMain(m *testing.M) {
	base := strings.ToLower(filepath.Base(os.Args[0]))
	if os.Getenv("GAMETEST_DOCKER_URL") != "" &&
		strings.HasPrefix(base, "docker") {
		os.Exit(asDocker(os.Args[1:]))
	}
	switch os.Getenv("GAMETEST_AS") {
	case "mint":
		os.Exit(fakeMint(os.Args[1:]))
	case "lvp":
		os.Exit(asLvp(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// fakeMint is the goldens' fake mint-key: GOLDEN_MINT maps a seat (or
// "*") to what to print and the exit code; each call is logged.
func fakeMint(argv []string) int {
	f, err := os.OpenFile(os.Getenv("GOLDEN_MINT_LOG"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		wd, _ := os.Getwd()
		b, _ := json.Marshal(map[string]any{"argv": argv, "dir": wd})
		f.Write(append(b, '\n'))
		f.Close()
	}
	var script map[string]struct {
		Out, Err string
		Code     int
		Sleep    float64
	}
	json.Unmarshal([]byte(os.Getenv("GOLDEN_MINT")), &script)
	key := ""
	if len(argv) > 1 && argv[0] == "--revoke-seat" {
		key = argv[1]
	} else if len(argv) > 0 {
		key = argv[0]
	}
	ans, ok := script[key]
	if !ok {
		ans = script["*"]
	}
	time.Sleep(time.Duration(ans.Sleep * float64(time.Second)))
	fmt.Fprint(os.Stdout, ans.Out)
	fmt.Fprint(os.Stderr, ans.Err)
	return ans.Code
}
