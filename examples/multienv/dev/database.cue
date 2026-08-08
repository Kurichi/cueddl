package db

import (
	"github.com/kurichi/cueddl/schema"
	// Aliased: a bare "tables" would be shadowed by the tables: field below.
	shared "github.com/kurichi/cueddl/examples/multienv/tables"
)

database: schema.#Database & {
	project:  "demo-project"
	instance: "demo-instance"
	name:     "myapp-dev"

	// Shared tables, plus a dev-only table unified on top.
	tables: shared.Tables
	tables: DebugEvents: {
		columns: {
			EventID: {type: "STRING(36)", notNull: true}
			Payload: {type: "JSON"}
		}
		primaryKey: ["EventID"]
	}

	views: shared.Views
}
