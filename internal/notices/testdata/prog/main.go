// Command prog links every module lvp will link, for the notices
// test (a test binary records none of its modules).
package main

import (
	"fmt"

	"github.com/kjFriday/local-verse-proxy/internal/fsx"
	"github.com/kjFriday/local-verse-proxy/internal/gamestate"
	"github.com/kjFriday/local-verse-proxy/internal/pgread"
	"github.com/kjFriday/local-verse-proxy/internal/rules"
)

func main() {
	fmt.Println(gamestate.Schema != "", pgread.OnceArgv[0],
		rules.SeatNames, fsx.RetryFor)
}
