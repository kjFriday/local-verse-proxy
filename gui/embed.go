// Package gui holds Front Desk's page: the files in static/, built
// into lvp, so the program needs nothing beside it.
package gui

import "embed"

// Static is static/'s page files.
//
//go:embed static/*.html static/*.js static/*.css static/*.svg
var Static embed.FS
