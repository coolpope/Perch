// Package skills holds the agent skills that ship inside Perch.
package skills

import _ "embed"

// Perch is the skill that teaches a coding agent to work through Perch.
//
//go:embed perch/SKILL.md
var Perch string
