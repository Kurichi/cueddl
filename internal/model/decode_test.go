package model

import (
	"strings"
	"testing"
)

const exported = `{
	"project": "p", "instance": "i", "name": "d",
	"tables": {
		"Orders": {
			"name": "Orders",
			"columns": {
				"UserID":  {"name": "UserID", "type": "string(36)", "notNull": true},
				"OrderID": {"name": "OrderID", "type": "STRING(36)", "notNull": true},
				"Amount":  {"name": "Amount", "type": "INT64", "notNull": false, "default": "0"}
			},
			"primaryKey": ["UserID", {"column": "OrderID", "desc": true}],
			"interleave": {"parent": "Users", "onDelete": "CASCADE"},
			"indexes": {
				"ByAmount": {"name": "ByAmount", "columns": [{"column": "Amount", "desc": true}], "unique": false, "nullFiltered": true}
			}
		},
		"Users": {
			"name": "Users",
			"columns": {"UserID": {"name": "UserID", "type": "STRING(36)", "notNull": true}},
			"primaryKey": ["UserID"]
		}
	}
}`

func TestDecodePreservesColumnOrder(t *testing.T) {
	db, err := DecodeJSON(strings.NewReader(exported))
	if err != nil {
		t.Fatal(err)
	}
	orders := db.Table("Orders")
	if orders == nil {
		t.Fatal("Orders not decoded")
	}
	got := make([]string, len(orders.Columns))
	for i, c := range orders.Columns {
		got[i] = c.Name
	}
	want := []string{"UserID", "OrderID", "Amount"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("column order = %v, want %v", got, want)
		}
	}
	if orders.Columns[0].Type != "STRING(36)" {
		t.Errorf("type not normalized: %q", orders.Columns[0].Type)
	}
	if !orders.PrimaryKey[1].Desc || orders.PrimaryKey[1].Column != "OrderID" {
		t.Errorf("key part shorthand mix decoded wrong: %+v", orders.PrimaryKey)
	}
	if orders.Interleave == nil || orders.Interleave.OnDelete != "CASCADE" {
		t.Errorf("interleave = %+v", orders.Interleave)
	}
	if idx := orders.Index("ByAmount"); idx == nil || !idx.NullFiltered || !idx.Columns[0].Desc {
		t.Errorf("index decoded wrong: %+v", idx)
	}
	if orders.Columns[2].Default != "0" {
		t.Errorf("default = %q", orders.Columns[2].Default)
	}
}

func TestValidateRejectsDanglingReferences(t *testing.T) {
	bad := strings.Replace(exported, `"parent": "Users"`, `"parent": "Nope"`, 1)
	if _, err := DecodeJSON(strings.NewReader(bad)); err == nil {
		t.Fatal("expected validation error for missing interleave parent")
	}
}

func TestValidateAcceptsOneToOneInterleave(t *testing.T) {
	const oneToOne = `{
		"project": "p", "instance": "i", "name": "d",
		"tables": {
			"Users": {"name": "Users", "columns": {"UserID": {"name": "UserID", "type": "STRING(36)", "notNull": true}},
			          "primaryKey": ["UserID"]},
			"UserExtras": {"name": "UserExtras", "columns": {"UserID": {"name": "UserID", "type": "STRING(36)", "notNull": true}},
			               "primaryKey": ["UserID"], "interleave": {"parent": "Users", "onDelete": "CASCADE"}}
		}
	}`
	if _, err := DecodeJSON(strings.NewReader(oneToOne)); err != nil {
		t.Fatalf("1:1 interleave (child PK == parent PK) should be valid, got: %v", err)
	}
}

func TestValidateRejectsShorterInterleaveChildKey(t *testing.T) {
	const tooShort = `{
		"project": "p", "instance": "i", "name": "d",
		"tables": {
			"Users": {"name": "Users", "columns": {
				"UserID": {"name": "UserID", "type": "STRING(36)", "notNull": true},
				"OrgID":  {"name": "OrgID", "type": "STRING(36)", "notNull": true}
			}, "primaryKey": ["UserID", "OrgID"]},
			"Bad": {"name": "Bad", "columns": {"UserID": {"name": "UserID", "type": "STRING(36)", "notNull": true}},
			        "primaryKey": ["UserID"], "interleave": {"parent": "Users", "onDelete": "CASCADE"}}
		}
	}`
	if _, err := DecodeJSON(strings.NewReader(tooShort)); err == nil {
		t.Fatal("expected validation error: child primary key shorter than parent's")
	}
}

func TestDecodeGeneratedColumn(t *testing.T) {
	const withGenerated = `{
		"project": "p", "instance": "i", "name": "d",
		"tables": {
			"KeibaRaces": {"name": "KeibaRaces", "columns": {
				"RaceId": {"name": "RaceId", "type": "STRING(12)", "notNull": true},
				"Date":   {"name": "Date", "type": "STRING(8)", "notNull": true,
				           "generated": {"expression": "SUBSTR(RaceId, 5, 8)"}}
			}, "primaryKey": ["RaceId"]}
		}
	}`
	db, err := DecodeJSON(strings.NewReader(withGenerated))
	if err != nil {
		t.Fatal(err)
	}
	date := db.Table("KeibaRaces").Column("Date")
	if date.Generated == nil || date.Generated.Expression != "SUBSTR(RaceId, 5, 8)" {
		t.Errorf("generated = %+v", date.Generated)
	}
}

func TestDecodeIgnoresCodegenOnlyColumnMarker(t *testing.T) {
	// schema.#Column.ignored is a hint for downstream code generators only;
	// the DDL side must treat a marked column exactly like an unmarked one.
	const withIgnored = `{
		"project": "p", "instance": "i", "name": "d",
		"tables": {
			"Users": {"name": "Users", "columns": {
				"UserID": {"name": "UserID", "type": "STRING(36)", "notNull": true},
				"Legacy": {"name": "Legacy", "type": "INT64", "ignored": true}
			}, "primaryKey": ["UserID"]}
		}
	}`
	db, err := DecodeJSON(strings.NewReader(withIgnored))
	if err != nil {
		t.Fatal(err)
	}
	legacy := db.Table("Users").Column("Legacy")
	if legacy == nil || legacy.Type != "INT64" {
		t.Errorf("ignored-marked column must decode as a normal column, got %+v", legacy)
	}
}

func TestValidateRejectsGeneratedWithDefault(t *testing.T) {
	const both = `{
		"project": "p", "instance": "i", "name": "d",
		"tables": {
			"T": {"name": "T", "columns": {
				"A": {"name": "A", "type": "INT64", "default": "0",
				      "generated": {"expression": "1"}}
			}, "primaryKey": ["A"]}
		}
	}`
	if _, err := DecodeJSON(strings.NewReader(both)); err == nil {
		t.Fatal("expected validation error: generated and default are mutually exclusive")
	}
}

func TestValidateRejectsDependencyCycle(t *testing.T) {
	const cyclic = `{
		"project": "p", "instance": "i", "name": "d",
		"tables": {
			"A": {"name": "A", "columns": {"ID": {"name": "ID", "type": "INT64", "notNull": true}},
			      "primaryKey": ["ID"], "dependsOn": ["B"]},
			"B": {"name": "B", "columns": {"ID": {"name": "ID", "type": "INT64", "notNull": true}},
			      "primaryKey": ["ID"], "dependsOn": ["A"]}
		}
	}`
	if _, err := DecodeJSON(strings.NewReader(cyclic)); err == nil {
		t.Fatal("expected validation error for dependsOn cycle")
	}

	dangling := strings.Replace(cyclic, `"dependsOn": ["A"]`, `"dependsOn": ["Nope"]`, 1)
	if _, err := DecodeJSON(strings.NewReader(dangling)); err == nil {
		t.Fatal("expected validation error for missing dependsOn target")
	}
}

func TestNormalizeExpr(t *testing.T) {
	for _, tc := range [][2]string{
		{"( Age >= 0 )", "Age >= 0"},
		{"(a) AND (b)", "(a) AND (b)"}, // outer parens are not a wrapper here
		{"  x  +  1 ", "x + 1"},
	} {
		if got := NormalizeExpr(tc[0]); got != tc[1] {
			t.Errorf("NormalizeExpr(%q) = %q, want %q", tc[0], got, tc[1])
		}
	}
}
