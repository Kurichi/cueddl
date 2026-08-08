package db

import (
	"github.com/kurichi/cueddl/schema"
	// Aliased: a bare "tables" would be shadowed by the tables: field below.
	shared "github.com/kurichi/cueddl/examples/multienv/tables"
)

database: schema.#Database & {
	project:  "demo-project"
	instance: "demo-instance"
	name:     "myapp-prod"

	tables: shared.Tables
	views:  shared.Views
}
