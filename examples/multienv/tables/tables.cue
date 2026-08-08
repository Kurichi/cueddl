// Package tables holds the schema shared by every environment.
// Environment packages import it and unify it into their #Database,
// optionally adding environment-specific tables or overrides.
package tables

import "github.com/kurichi/cueddl/schema"

Tables: [Name=string]: schema.#Table & {name: Name}
Tables: {
	Users: {
		columns: {
			UserID:    {type: "STRING(36)", notNull: true}
			Email:     {type: "STRING(MAX)", notNull: true}
			CreatedAt: {type: "TIMESTAMP", notNull: true, default: "CURRENT_TIMESTAMP()"}
		}
		primaryKey: ["UserID"]
		indexes: UsersByEmail: {
			columns: ["Email"]
			unique: true
		}
	}

	Orders: {
		columns: {
			UserID:  {type: "STRING(36)", notNull: true}
			OrderID: {type: "STRING(36)", notNull: true}
			Amount:  {type: "INT64", notNull: true}
		}
		primaryKey: ["UserID", "OrderID"]
		interleave: {parent: "Users", onDelete: "CASCADE"}
	}
}

Views: [Name=string]: schema.#View & {name: Name}
Views: {
	UserEmails: {
		definition: "SELECT u.UserID, u.Email FROM Users AS u"
		dependsOn: ["Users"]
	}
}
