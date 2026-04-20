// Package docsschemas carries the embed.FS of the hand-written JSON
// Schemas shipped under docs/schemas/. It exists only so Go's embed
// directive can reach those files from a Go source location within
// their directory tree — embed patterns cannot cross parent
// boundaries, so a package living next to the schemas is the cleanest
// way to keep docs/ as the canonical authoring location while still
// serving the files at /api/v1/schemas/tools/{tool} (SPEC-SCHED1.5 R3).
package docsschemas

import "embed"

// Tools is the filesystem view of docs/schemas/tools. Consumers access
// files by their relative path, e.g. "tools/nuclei.json".
//
//go:embed tools/*.json
var Tools embed.FS
