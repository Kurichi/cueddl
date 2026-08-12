// The same database as examples/simple, one release later. Diffing this
// against a database created from examples/simple exercises every ALTER
// path: add/alter column, recreate an index, replace a check constraint,
// and drop a table (destructive).
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
				Name:      {type: "STRING(MAX)"} // added
				Age:       {type: "INT64", notNull: true, default: "0"} // NOT NULL + default added
				CreatedAt: {type: "TIMESTAMP", notNull: true, default: "CURRENT_TIMESTAMP()"}
				UpdatedAt: {type: "TIMESTAMP", allowCommitTimestamp: true} // option added -> SET OPTIONS
			}
			primaryKey: ["UserID"]
			indexes: {
				UsersByEmail: {
					columns: ["Email"]
					unique: true
				}
			}
			checks: {
				// Tightened expression: the constraint is dropped and re-added.
				CK_Users_AgeNonNegative: {expression: "Age >= 0 AND Age < 200"}
			}
		}

		// Orders was renamed: ALTER TABLE ... RENAME TO, not drop + create.
		Purchases: {
			renamedFrom: "Orders"
			columns: {
				UserID:  {type: "STRING(36)", notNull: true}
				OrderID: {type: "STRING(36)", notNull: true}
				Amount:  {type: "INT64", notNull: true}
				Status:  {type: "STRING(16)", notNull: true, default: "\"pending\""}
			}
			primaryKey: ["UserID", "OrderID"]
			interleave: {
				parent:   "Users"
				onDelete: "CASCADE"
			}
			indexes: {
				// Key shrank and STORING appeared: recreated as drop + create.
				OrdersByStatus: {
					columns: ["Status"]
					storing: ["Amount"]
				}
			}
		}

		// AuditLogs is gone: its FK and table are dropped (destructive).
	}

	views: {
		// Definition changed -> CREATE OR REPLACE VIEW.
		UserEmails: {
			definition: "SELECT u.UserID, u.Email, u.Name FROM Users AS u"
			dependsOn: ["Users"]
		}
	}

	changeStreams: {
		// Narrowed to specific columns (Name is brand new, so the SET FOR
		// runs after ADD COLUMN) and retention extended -> SET OPTIONS.
		UsersStream: {
			watch: [{table: "Users", columns: ["Email", "Name"]}]
			retentionPeriod: "48h"
		}
	}
}
