// A minimal schema for examples/ci/README.md's CI walkthrough. The schema
// content itself doesn't matter here — what this example is really about
// is the .github/workflows/ next to it.
package db

import "github.com/kurichi/cueddl/schema"

database: schema.#Database & {
	project:  "demo-project"
	instance: "demo-instance"
	name:     "demo-db"

	tables: {
		Users: {
			columns: {
				UserID:    {type: "STRING(36)", notNull: true}
				Email:     {type: "STRING(MAX)", notNull: true}
				CreatedAt: {type: "TIMESTAMP", notNull: true, default: "CURRENT_TIMESTAMP()"}
			}
			primaryKey: ["UserID"]
			indexes: {
				UsersByEmail: {
					columns: ["Email"]
					unique: true
				}
			}
		}
	}
}
