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
				UpdatedAt: {type: "TIMESTAMP"}
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
				LogID:     {type: "STRING(36)", notNull: true}
				UserID:    {type: "STRING(36)", notNull: true}
				Payload:   {type: "JSON"}
				CreatedAt: {type: "TIMESTAMP", notNull: true, default: "CURRENT_TIMESTAMP()"}
			}
			primaryKey: ["LogID"]
			// TTL: logs older than 90 days are deleted in the background.
			rowDeletionPolicy: {column: "CreatedAt", days: 90}
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

	views: {
		UserEmails: {
			definition: "SELECT u.UserID, u.Email FROM Users AS u"
			dependsOn: ["Users"]
		}
	}

	changeStreams: {
		UsersStream: {
			watch: [{table: "Users"}]
		}
	}
}
