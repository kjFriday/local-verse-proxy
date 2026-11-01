//go:build windows

package seats

import "strings"

// Windows environment names match whatever their case.
func foldName(s string) string { return strings.ToUpper(s) }
