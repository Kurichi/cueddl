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

func TestRenameTable(t *testing.T) {
	actual := &model.Database{Tables: []*model.Table{users(), orders()}}
	desired := &model.Database{Tables: []*model.Table{users(), orders()}}
	desired.Tables[1].Name = "Purchases"
	desired.Tables[1].RenamedFrom = "Orders"

	p := Diff(desired, actual)
	stmts := sqls(p)
	indexOf(t, stmts, "ALTER TABLE `Orders` RENAME TO `Purchases`")
	if len(stmts) != 1 {
		t.Errorf("rename must not produce drop/create, got %v", stmts)
	}
	if p.HasDestructive() {
		t.Error("a pure rename must not be destructive")
	}

	// A stale hint (old table already gone, new name present) is a no-op.
	p = Diff(desired, &model.Database{Tables: []*model.Table{users(), desired.Tables[1]}})
	if len(p.Statements) != 0 {
		t.Errorf("stale rename hint must be ignored, got %v", sqls(p))
	}
}

func TestRenameRewritesInterleaveParent(t *testing.T) {
	actual := &model.Database{Tables: []*model.Table{users(), orders()}}
	desired := &model.Database{Tables: []*model.Table{users(), orders()}}
	desired.Tables[0].Name = "Members"
	desired.Tables[0].RenamedFrom = "Users"
	desired.Tables[0].Indexes = nil
	actual.Tables[0].Indexes = nil
	desired.Tables[1].Interleave.Parent = "Members"

	p := Diff(desired, actual)
	stmts := sqls(p)
	indexOf(t, stmts, "ALTER TABLE `Users` RENAME TO `Members`")
	if len(stmts) != 1 {
		t.Errorf("interleave parent rename must not touch the child, got %v", stmts)
	}
}

func TestAllowCommitTimestampToggle(t *testing.T) {
	withTS := func(on bool) *model.Database {
		u := users()
		u.Columns = append(u.Columns, &model.Column{Name: "UpdatedAt", Type: "TIMESTAMP", AllowCommitTimestamp: on})
		return &model.Database{Tables: []*model.Table{u}}
	}
	p := Diff(withTS(true), withTS(false))
	indexOf(t, sqls(p), "ALTER TABLE `Users` ALTER COLUMN `UpdatedAt` SET OPTIONS (allow_commit_timestamp=true)")
	p = Diff(withTS(false), withTS(true))
	indexOf(t, sqls(p), "SET OPTIONS (allow_commit_timestamp=null)")
}

func TestViewLifecycle(t *testing.T) {
	base := &model.Database{Tables: []*model.Table{users()}}
	v := &model.View{Name: "UserEmails", Definition: "SELECT Email FROM Users", SecurityType: "INVOKER"}

	// Create: after tables.
	desired := &model.Database{Tables: []*model.Table{users()}, Views: []*model.View{v}}
	p := Diff(desired, &model.Database{})
	stmts := sqls(p)
	if indexOf(t, stmts, "CREATE VIEW `UserEmails` SQL SECURITY INVOKER AS SELECT Email FROM Users") <
		indexOf(t, stmts, "CREATE TABLE `Users`") {
		t.Error("views must be created after tables")
	}

	// Change: OR REPLACE.
	changed := &model.View{Name: "UserEmails", Definition: "SELECT UserID, Email FROM Users", SecurityType: "INVOKER"}
	p = Diff(&model.Database{Tables: []*model.Table{users()}, Views: []*model.View{changed}}, desired)
	indexOf(t, sqls(p), "CREATE OR REPLACE VIEW `UserEmails`")

	// Remove: DROP VIEW comes before everything else.
	p = Diff(base, desired)
	if sqls(p)[0] != "DROP VIEW `UserEmails`" {
		t.Errorf("removed view must be dropped first, got %v", sqls(p))
	}
}

func TestColumnDropDeferredAfterViewReplace(t *testing.T) {
	// Email is dropped while the view that referenced it gets a new
	// definition: the DROP COLUMN must come after the view replacement.
	actualView := &model.View{Name: "V", Definition: "SELECT Email FROM Users", SecurityType: "INVOKER"}
	desiredView := &model.View{Name: "V", Definition: "SELECT UserID FROM Users", SecurityType: "INVOKER"}
	actual := &model.Database{Tables: []*model.Table{users()}, Views: []*model.View{actualView}}
	desired := &model.Database{Tables: []*model.Table{users()}, Views: []*model.View{desiredView}}
	desired.Tables[0].Columns = desired.Tables[0].Columns[:1] // drop Email
	desired.Tables[0].Indexes = nil
	p := Diff(desired, actual)
	stmts := sqls(p)
	if indexOf(t, stmts, "DROP COLUMN `Email`") < indexOf(t, stmts, "CREATE OR REPLACE VIEW `V`") {
		t.Errorf("column drop must come after view replacement:\n%s", strings.Join(stmts, "\n"))
	}
}

func TestRowDeletionPolicyLifecycle(t *testing.T) {
	withRDP := func(days int64) *model.Database {
		u := users()
		u.Columns = append(u.Columns, &model.Column{Name: "CreatedAt", Type: "TIMESTAMP", NotNull: true})
		if days > 0 {
			u.RowDeletionPolicy = &model.RowDeletionPolicy{Column: "CreatedAt", Days: days}
		}
		return &model.Database{Tables: []*model.Table{u}}
	}

	// Inline in CREATE TABLE.
	p := Diff(withRDP(30), &model.Database{})
	indexOf(t, sqls(p), "ROW DELETION POLICY (OLDER_THAN(`CreatedAt`, INTERVAL 30 DAY))")

	// Add, replace, drop on an existing table.
	p = Diff(withRDP(30), withRDP(0))
	indexOf(t, sqls(p), "ALTER TABLE `Users` ADD ROW DELETION POLICY (OLDER_THAN(`CreatedAt`, INTERVAL 30 DAY))")
	p = Diff(withRDP(7), withRDP(30))
	indexOf(t, sqls(p), "ALTER TABLE `Users` REPLACE ROW DELETION POLICY (OLDER_THAN(`CreatedAt`, INTERVAL 7 DAY))")
	p = Diff(withRDP(0), withRDP(30))
	indexOf(t, sqls(p), "ALTER TABLE `Users` DROP ROW DELETION POLICY")
}

func csDB(cs ...*model.ChangeStream) *model.Database {
	return &model.Database{Tables: []*model.Table{users()}, ChangeStreams: cs}
}

func TestChangeStreamLifecycle(t *testing.T) {
	watchUsers := &model.ChangeStream{Name: "S", Watch: []model.ChangeStreamTarget{{Table: "Users"}}}

	// Create with options, after tables.
	full := &model.ChangeStream{Name: "S", ForAll: true, RetentionPeriod: "36h", ValueCaptureType: "NEW_ROW"}
	p := Diff(csDB(full), &model.Database{})
	stmts := sqls(p)
	if indexOf(t, stmts, "CREATE CHANGE STREAM `S` FOR ALL OPTIONS (retention_period = '36h', value_capture_type = 'NEW_ROW')") <
		indexOf(t, stmts, "CREATE TABLE `Users`") {
		t.Error("change stream must be created after tables")
	}

	// Retarget: existing targets -> altered early (first statement).
	p = Diff(csDB(watchUsers), csDB(full))
	if sqls(p)[0] != "ALTER CHANGE STREAM `S` SET FOR `Users`" {
		t.Errorf("retarget to existing tables should come first, got %v", sqls(p))
	}
	// Options reset to defaults via null.
	indexOf(t, sqls(p), "ALTER CHANGE STREAM `S` SET OPTIONS (retention_period = null, value_capture_type = null)")

	// Removed stream is dropped first.
	p = Diff(&model.Database{Tables: []*model.Table{users()}}, csDB(watchUsers))
	if sqls(p)[0] != "DROP CHANGE STREAM `S`" {
		t.Errorf("removed stream must be dropped first, got %v", sqls(p))
	}
}

func TestChangeStreamRetargetToNewColumnComesLate(t *testing.T) {
	// The stream starts watching whole Users, then narrows to a column
	// that does not exist yet: SET FOR must come after ADD COLUMN.
	actual := csDB(&model.ChangeStream{Name: "S", Watch: []model.ChangeStreamTarget{{Table: "Users"}}})
	desired := csDB(&model.ChangeStream{Name: "S", Watch: []model.ChangeStreamTarget{{Table: "Users", Columns: []string{"Name"}}}})
	desired.Tables[0].Columns = append(desired.Tables[0].Columns, &model.Column{Name: "Name", Type: "STRING(MAX)"})
	p := Diff(desired, actual)
	stmts := sqls(p)
	if indexOf(t, stmts, "SET FOR `Users`(`Name`)") < indexOf(t, stmts, "ADD COLUMN `Name`") {
		t.Errorf("retarget needing a new column must come after the column exists:\n%s", strings.Join(stmts, "\n"))
	}
}

func TestChangeStreamKeysOnlyVsAllColumns(t *testing.T) {
	keysOnly := csDB(&model.ChangeStream{Name: "S", Watch: []model.ChangeStreamTarget{{Table: "Users", Columns: []string{}}}})
	allCols := csDB(&model.ChangeStream{Name: "S", Watch: []model.ChangeStreamTarget{{Table: "Users"}}})

	p := Diff(keysOnly, allCols)
	indexOf(t, sqls(p), "SET FOR `Users`()")
	if p := Diff(keysOnly, keysOnly); len(p.Statements) != 0 {
		t.Errorf("keys-only watch must be idempotent, got %v", sqls(p))
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
