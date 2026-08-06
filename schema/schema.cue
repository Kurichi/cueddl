// Package schema defines the declarative schema model for cueddl.
//
// Users import this package and declare a concrete #Database value.
// `cue cmd plan` / `cue cmd apply` (see ddl_tool.cue in your project)
// export the value as JSON and feed it to the cueddl binary, which
// diffs it against the live Cloud Spanner schema.
package schema

// #Type matches Cloud Spanner GoogleSQL column types.
// Scalar types, parameterized STRING/BYTES, and one level of ARRAY<>.
#ScalarType: "BOOL" | "INT64" | "FLOAT32" | "FLOAT64" | "NUMERIC" |
	"DATE" | "TIMESTAMP" | "JSON" |
	=~"^STRING\\((\\d+|MAX)\\)$" |
	=~"^BYTES\\((\\d+|MAX)\\)$"

#Type: #ScalarType | =~"^ARRAY<.+>$"

// #Column is a single table column.
#Column: {
	// name is filled in from the struct key in #Table.columns.
	name!: string
	type!: #Type
	notNull: *false | bool
	// default is a GoogleSQL expression, e.g. "CURRENT_TIMESTAMP()" or "0".
	default?: string
}

// #KeyPart is one component of a primary key or index key.
// A bare string is shorthand for {column: name}.
#KeyPart: string | {
	column!: string
	desc:    *false | bool
}

// #Index is a secondary index.
#Index: {
	name!:   string
	columns!: [...#KeyPart] & [_, ...]
	unique:       *false | bool
	nullFiltered: *false | bool
	// storing lists non-key columns to copy into the index (STORING clause).
	storing?: [...string]
}

// #ForeignKey declares a foreign key constraint.
#ForeignKey: {
	name!:    string
	columns!: [...string] & [_, ...]
	references!: {
		table!:   string
		columns!: [...string] & [_, ...]
	}
	// onDelete maps to ON DELETE; Spanner supports NO ACTION and CASCADE.
	onDelete: *"NO ACTION" | "CASCADE"
}

// #Check declares a CHECK constraint with a GoogleSQL expression.
#Check: {
	name!:       string
	expression!: string
}

// #Interleave places a table in its parent's key range.
#Interleave: {
	parent!:  string
	onDelete: *"NO ACTION" | "CASCADE"
}

// #Table is a Spanner table.
#Table: {
	name!: string
	columns!: [Name=string]: #Column & {name: Name}
	primaryKey!: [...#KeyPart] & [_, ...]
	interleave?: #Interleave
	indexes: [Name=string]: #Index & {name: Name}
	foreignKeys: [Name=string]: #ForeignKey & {name: Name}
	checks: [Name=string]: #Check & {name: Name}
	// dependsOn forces the listed tables to be created before this one
	// (and dropped after it). Interleave parents are ordered automatically;
	// use this for dependencies cueddl cannot infer.
	dependsOn?: [...string]
}

// #Database is the root of a cueddl configuration: the connection target
// plus the full desired schema. Anything not declared here is a candidate
// for DROP when applying with --allow-destructive.
#Database: {
	project!:  string
	instance!: string
	name!:     string
	tables: [Name=string]: #Table & {name: Name}
}
