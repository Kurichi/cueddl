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
				Age:       {type: "INT64"}
				CreatedAt: {type: "TIMESTAMP", notNull: true, default: "CURRENT_TIMESTAMP()"}
			}
			primaryKey: ["UserID"]
			indexes: {
				UsersByEmail: {
					columns: ["Email"]
					unique: true
				}
			}
			checks: {
				CK_Users_AgeNonNegative: {expression: "Age >= 0"}
			}
		}

		Orders: {
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
				OrdersByStatus: {
					columns: [{column: "Status"}, {column: "Amount", desc: true}]
				}
			}
		}

		AuditLogs: {
			// Not required here (FKs are added after all tables exist),
			// but demonstrates explicit creation-order control.
			dependsOn: ["Users"]
			columns: {
				LogID:   {type: "STRING(36)", notNull: true}
				UserID:  {type: "STRING(36)", notNull: true}
				Payload: {type: "JSON"}
			}
			primaryKey: ["LogID"]
			foreignKeys: {
				FK_AuditLogs_Users: {
					columns: ["UserID"]
					references: {
						table: "Users"
						columns: ["UserID"]
					}
				}
			}
		}
	}
}
