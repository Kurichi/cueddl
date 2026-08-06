package db

import (
	"encoding/json"
	"tool/cli"
	"tool/exec"
)

// cue cmd plan   — show the DDL needed to reach the declared schema
// cue cmd apply  — apply it (creates the database if missing)
//
// Destructive statements (DROP TABLE / DROP COLUMN) are refused unless
// CUEDDL_ALLOW_DESTRUCTIVE=1 is set in the environment:
//
//	CUEDDL_ALLOW_DESTRUCTIVE=1 cue cmd apply
//
// The cueddl binary is resolved through the Go module tool directive,
// so it works anywhere inside this repository without a global install.

command: plan: {
	run: exec.Run & {
		cmd: ["go", "tool", "cueddl", "plan"]
		stdin:  json.Marshal(database)
		stdout: string
	}
	print: cli.Print & {
		text: run.stdout
	}
}

command: apply: {
	run: exec.Run & {
		cmd: ["go", "tool", "cueddl", "apply"]
		stdin:  json.Marshal(database)
		stdout: string
	}
	print: cli.Print & {
		text: run.stdout
	}
}
