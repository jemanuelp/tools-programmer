// Package toolsprogrammer contains the skill bundled with the executable.
package toolsprogrammer

import "embed"

// SkillFiles embeds the authoritative project skill directly. No generated copy
// or checkout is required when building or running the executable.
//
//go:embed .opencode/skills/tools-programmer/SKILL.md .opencode/skills/tools-programmer/references/cli.md
var SkillFiles embed.FS
