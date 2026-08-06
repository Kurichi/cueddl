package diff

import (
	"strings"
	"testing"

	"github.com/Kurichi/cueddl/internal/model"
)

func users() *model.Table {
	return &model.Table{
		Name: "Users",
		Columns: []*model.Column{
			{Name: "UserID", Type: "STRING(36)", NotNull: true},
			{Name: "Email", Type: "STRING(MAX)", NotNull: true},
		},
		PrimaryKey: []model.KeyPart{{Column: "UserID"}},
		Indexes: []*model.Index{
			{Name: "UsersByEmail", Columns: []model.KeyPart{{Column: "Email"}}, Unique: true},
		},
	}
}

func orders() *model.Table {
	return &model.Table{
		Name: "Orders",
		Columns: []*model.Column{
			{Name: "UserID", Type: "STRING(36)", NotNull: true},
			{Name: "OrderID", Type: "STRING(36)", NotNull: true},
		},
		PrimaryKey: []model.KeyPart{{Column: "UserID"}, {Column: "OrderID"}},
		Interleave: &model.Interleave{Parent: "Users", OnDelete: "CASCADE"},
	}
}

func sqls(p *Plan) []string {
	out := make([]string, len(p.Statements))
	for i, s := range p.Statements {
		out[i] = s.SQL
	}
	return out
}

func indexOf(t *testing.T, stmts []string, substr string) int {
	t.Helper()
	for i, s := range stmts {
		if strings.Contains(s, substr) {
			return i
		}
	}
	t.Fatalf("no statement containing %q in %q", substr, stmts)
	return -1
}

func TestCreateFromScratchOrdersParentsFirst(t *testing.T) {
	// Declare the child before the parent to prove topological ordering.
	desired := &model.Database{Tables: []*model.Table{orders(), users()}}
	p := Diff(desired, &model.Database{})
	if len(p.Warnings) > 0 {
		t.Fatalf("unexpected warnings: %v", p.Warnings)
	}
	stmts := sqls(p)
	if indexOf(t, stmts, "CREATE TABLE `Users`") > indexOf(t, stmts, "CREATE TABLE `Orders`") {
		t.Errorf("interleave parent must be created before child:\n%s", strings.Join(stmts, "\n"))
	}
	if indexOf(t, stmts, "CREATE UNIQUE INDEX `UsersByEmail`") < indexOf(t, stmts, "CREATE TABLE `Orders`") {
		t.Errorf("indexes should come after table creation")
	}
}

func TestNoChanges(t *testing.T) {
	desired := &model.Database{Tables: []*model.Table{users(), orders()}}
	actual := &model.Database{Tables: []*model.Table{orders(), users()}} // order must not matter
	p := Diff(desired, actual)
	if len(p.Statements) != 0 {
		t.Errorf("expected empty plan, got %v", sqls(p))
	}
}

func TestDropTableDropsIndexesFirstAndIsDestructive(t *testing.T) {
	actual := &model.Database{Tables: []*model.Table{users(), orders()}}
	p := Diff(&model.Database{}, actual)
	stmts := sqls(p)
	if indexOf(t, stmts, "DROP INDEX `UsersByEmail`") > indexOf(t, stmts, "DROP TABLE `Users`") {
		t.Errorf("index must be dropped before its table:\n%s", strings.Join(stmts, "\n"))
	}
	if indexOf(t, stmts, "DROP TABLE `Orders`") > indexOf(t, stmts, "DROP TABLE `Users`") {
		t.Errorf("interleaved child must be dropped before parent")
	}
	if !p.HasDestructive() {
		t.Error("dropping tables must be destructive")
	}
}

func TestColumnChanges(t *testing.T) {
	desired := &model.Database{Tables: []*model.Table{users()}}
	desired.Tables[0].Columns = append(desired.Tables[0].Columns,
		&model.Column{Name: "Age", Type: "INT64"})
	desired.Tables[0].Columns[1].Type = "STRING(100)" // Email narrowed

	actual := &model.Database{Tables: []*model.Table{users()}}
	actual.Tables[0].Columns = append(actual.Tables[0].Columns,
		&model.Column{Name: "Legacy", Type: "BOOL"})

	p := Diff(desired, actual)
	stmts := sqls(p)
	indexOf(t, stmts, "ALTER TABLE `Users` ADD COLUMN `Age` INT64")
	indexOf(t, stmts, "ALTER TABLE `Users` ALTER COLUMN `Email` STRING(100) NOT NULL")
	i := indexOf(t, stmts, "ALTER TABLE `Users` DROP COLUMN `Legacy`")
	if !p.Statements[i].Destructive {
		t.Error("DROP COLUMN must be destructive")
	}
}

func TestPrimaryKeyChangeIsWarnedNotPlanned(t *testing.T) {
	desired := &model.Database{Tables: []*model.Table{users()}}
	desired.Tables[0].PrimaryKey = []model.KeyPart{{Column: "Email"}}
	actual := &model.Database{Tables: []*model.Table{users()}}
	p := Diff(desired, actual)
	if len(p.Warnings) == 0 {
		t.Fatal("expected a warning about primary key change")
	}
	if len(p.Statements) != 0 {
		t.Errorf("primary key change must not produce DDL, got %v", sqls(p))
	}
}

func TestIndexChangeRecreates(t *testing.T) {
	desired := &model.Database{Tables: []*model.Table{users()}}
	desired.Tables[0].Indexes[0].Unique = false
	actual := &model.Database{Tables: []*model.Table{users()}}
	p := Diff(desired, actual)
	stmts := sqls(p)
	if indexOf(t, stmts, "DROP INDEX `UsersByEmail`") > indexOf(t, stmts, "CREATE INDEX `UsersByEmail`") {
		t.Errorf("changed index must be dropped before recreation")
	}
}

func TestFKAddedAfterAllTables(t *testing.T) {
	logs := &model.Table{
		Name: "AuditLogs",
		Columns: []*model.Column{
			{Name: "LogID", Type: "STRING(36)", NotNull: true},
			{Name: "UserID", Type: "STRING(36)", NotNull: true},
		},
		PrimaryKey: []model.KeyPart{{Column: "LogID"}},
		ForeignKeys: []*model.ForeignKey{{
			Name: "FK_AuditLogs_Users", Columns: []string{"UserID"},
			RefTable: "Users", RefColumns: []string{"UserID"}, OnDelete: "NO ACTION",
		}},
	}
	// FK source declared before its target table.
	desired := &model.Database{Tables: []*model.Table{logs, users()}}
	p := Diff(desired, &model.Database{})
	stmts := sqls(p)
	if indexOf(t, stmts, "ADD CONSTRAINT `FK_AuditLogs_Users`") < indexOf(t, stmts, "CREATE TABLE `Users`") {
		t.Errorf("foreign keys must be added after all tables exist:\n%s", strings.Join(stmts, "\n"))
	}
}

func TestDependsOnOrdersCreatesAndDrops(t *testing.T) {
	standalone := &model.Table{
		Name:       "Settings",
		Columns:    []*model.Column{{Name: "Key", Type: "STRING(64)", NotNull: true}},
		PrimaryKey: []model.KeyPart{{Column: "Key"}},
		DependsOn:  []string{"Users"},
	}
	// Dependent declared first: dependsOn must still order Users first.
	desired := &model.Database{Tables: []*model.Table{standalone, users()}}
	p := Diff(desired, &model.Database{})
	stmts := sqls(p)
	if indexOf(t, stmts, "CREATE TABLE `Users`") > indexOf(t, stmts, "CREATE TABLE `Settings`") {
		t.Errorf("dependsOn target must be created first:\n%s", strings.Join(stmts, "\n"))
	}

	// And dropped in reverse: the dependent goes first.
	p = Diff(&model.Database{}, desired)
	stmts = sqls(p)
	if indexOf(t, stmts, "DROP TABLE `Settings`") > indexOf(t, stmts, "DROP TABLE `Users`") {
		t.Errorf("dependsOn dependent must be dropped first:\n%s", strings.Join(stmts, "\n"))
	}
}

func TestDefaultChange(t *testing.T) {
	desired := &model.Database{Tables: []*model.Table{users()}}
	desired.Tables[0].Columns[1].Default = `"unknown"`
	actual := &model.Database{Tables: []*model.Table{users()}}
	p := Diff(desired, actual)
	indexOf(t, sqls(p), "ALTER COLUMN `Email` SET DEFAULT (\"unknown\")")

	// And dropping it again.
	p = Diff(actual, desired)
	indexOf(t, sqls(p), "ALTER COLUMN `Email` DROP DEFAULT")
}
