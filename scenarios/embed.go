// Package scenarios embeds the scenario files so every binary carries them.
package scenarios

import "embed"

// FS holds every *.yaml scenario file in this directory.
//
//go:embed *.yaml
var FS embed.FS
