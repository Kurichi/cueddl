// Package ddlgen renders model types as Cloud Spanner GoogleSQL DDL.
package ddlgen

import (
	"fmt"
	"strings"

	"github.com/Kurichi/cueddl/internal/model"
)

// Q quotes an identifier with backticks so reserved words stay valid.
func Q(ident string) string {
	return "`" + ident + "`"
}

func keyList(parts []model.KeyPart) string {
	elems := make([]string, len(parts))
	for i, p := range parts {
		elems[i] = Q(p.Column)
		if p.Desc {
			elems[i] += " DESC"
		}
	}
	return strings.Join(elems, ", ")
}

func ColumnDef(c *model.Column) string {
	var b strings.Builder
	b.WriteString(Q(c.Name))
	b.WriteString(" ")
	b.WriteString(c.Type)
	if c.NotNull {
		b.WriteString(" NOT NULL")
	}
	switch {
	case c.Generated != nil:
		fmt.Fprintf(&b, " AS (%s) STORED", c.Generated.Expression)
	case c.Default != "":
		fmt.Fprintf(&b, " DEFAULT (%s)", c.Default)
	}
	if c.AllowCommitTimestamp {
		b.WriteString(" OPTIONS (allow_commit_timestamp=true)")
	}
	return b.String()
}

// CreateTable renders CREATE TABLE with inline CHECK constraints.
// Foreign keys are intentionally omitted: the diff engine adds them with
// ALTER TABLE after all tables exist, so creation order never has to
// satisfy reference order.
func CreateTable(t *model.Table) string {
	var defs []string
	for _, c := range t.Columns {
		defs = append(defs, "  "+ColumnDef(c))
	}
	for _, ck := range t.Checks {
		defs = append(defs, fmt.Sprintf("  CONSTRAINT %s CHECK (%s)", Q(ck.Name), ck.Expression))
	}
	stmt := fmt.Sprintf("CREATE TABLE %s (\n%s,\n) PRIMARY KEY (%s)",
		Q(t.Name), strings.Join(defs, ",\n"), keyList(t.PrimaryKey))
	if t.Interleave != nil {
		stmt += fmt.Sprintf(",\nINTERLEAVE IN PARENT %s ON DELETE %s",
			Q(t.Interleave.Parent), t.Interleave.OnDelete)
	}
	if t.RowDeletionPolicy != nil {
		stmt += ",\n" + rdpClause(t.RowDeletionPolicy)
	}
	return stmt
}

func rdpClause(p *model.RowDeletionPolicy) string {
	return fmt.Sprintf("ROW DELETION POLICY (OLDER_THAN(%s, INTERVAL %d DAY))", Q(p.Column), p.Days)
}

func AddRowDeletionPolicy(table string, p *model.RowDeletionPolicy) string {
	return fmt.Sprintf("ALTER TABLE %s ADD %s", Q(table), rdpClause(p))
}

func ReplaceRowDeletionPolicy(table string, p *model.RowDeletionPolicy) string {
	return fmt.Sprintf("ALTER TABLE %s REPLACE %s", Q(table), rdpClause(p))
}

func DropRowDeletionPolicy(table string) string {
	return fmt.Sprintf("ALTER TABLE %s DROP ROW DELETION POLICY", Q(table))
}

func CreateIndex(table string, idx *model.Index) string {
	var b strings.Builder
	b.WriteString("CREATE ")
	if idx.Unique {
		b.WriteString("UNIQUE ")
	}
	if idx.NullFiltered {
		b.WriteString("NULL_FILTERED ")
	}
	fmt.Fprintf(&b, "INDEX %s ON %s (%s)", Q(idx.Name), Q(table), keyList(idx.Columns))
	if len(idx.Storing) > 0 {
		quoted := make([]string, len(idx.Storing))
		for i, c := range idx.Storing {
			quoted[i] = Q(c)
		}
		fmt.Fprintf(&b, " STORING (%s)", strings.Join(quoted, ", "))
	}
	return b.String()
}

func DropIndex(name string) string {
	return "DROP INDEX " + Q(name)
}

func AddColumn(table string, c *model.Column) string {
	return fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", Q(table), ColumnDef(c))
}

func DropColumn(table, column string) string {
	return fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s", Q(table), Q(column))
}

// AlterColumn re-states type and nullability; Spanner requires the full
// column definition (minus default) in ALTER COLUMN. A generated column
// re-states its expression too, so this also alters an existing
// generated column's expression.
func AlterColumn(table string, c *model.Column) string {
	def := Q(c.Name) + " " + c.Type
	if c.NotNull {
		def += " NOT NULL"
	}
	if c.Generated != nil {
		def += fmt.Sprintf(" AS (%s) STORED", c.Generated.Expression)
	}
	return fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s", Q(table), def)
}

func SetColumnDefault(table string, c *model.Column) string {
	if c.Default == "" {
		return fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s DROP DEFAULT", Q(table), Q(c.Name))
	}
	return fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s SET DEFAULT (%s)", Q(table), Q(c.Name), c.Default)
}

func AddForeignKey(table string, fk *model.ForeignKey) string {
	cols := make([]string, len(fk.Columns))
	for i, c := range fk.Columns {
		cols[i] = Q(c)
	}
	refCols := make([]string, len(fk.RefColumns))
	for i, c := range fk.RefColumns {
		refCols[i] = Q(c)
	}
	stmt := fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s (%s)",
		Q(table), Q(fk.Name), strings.Join(cols, ", "), Q(fk.RefTable), strings.Join(refCols, ", "))
	if fk.OnDelete == "CASCADE" {
		stmt += " ON DELETE CASCADE"
	}
	return stmt
}

func AddCheck(table string, ck *model.Check) string {
	return fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s CHECK (%s)", Q(table), Q(ck.Name), ck.Expression)
}

func DropConstraint(table, name string) string {
	return fmt.Sprintf("ALTER TABLE %s DROP CONSTRAINT %s", Q(table), Q(name))
}

func DropTable(name string) string {
	return "DROP TABLE " + Q(name)
}

func RenameTable(from, to string) string {
	return fmt.Sprintf("ALTER TABLE %s RENAME TO %s", Q(from), Q(to))
}

// SetColumnOptions toggles allow_commit_timestamp; Spanner clears an
// option by setting it to null.
func SetColumnOptions(table string, c *model.Column) string {
	value := "null"
	if c.AllowCommitTimestamp {
		value = "true"
	}
	return fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s SET OPTIONS (allow_commit_timestamp=%s)",
		Q(table), Q(c.Name), value)
}

func CreateView(v *model.View, orReplace bool) string {
	create := "CREATE VIEW"
	if orReplace {
		create = "CREATE OR REPLACE VIEW"
	}
	return fmt.Sprintf("%s %s SQL SECURITY %s AS %s", create, Q(v.Name), v.SecurityType, v.Definition)
}

func DropView(name string) string {
	return "DROP VIEW " + Q(name)
}

// forClause renders the FOR part of a change stream: FOR ALL, or the
// watched tables where nil columns means the whole table and an empty
// slice means primary keys only ("Users()").
func forClause(cs *model.ChangeStream) string {
	if cs.ForAll {
		return "FOR ALL"
	}
	targets := make([]string, len(cs.Watch))
	for i, w := range cs.Watch {
		targets[i] = Q(w.Table)
		if w.Columns != nil {
			cols := make([]string, len(w.Columns))
			for j, c := range w.Columns {
				cols[j] = Q(c)
			}
			targets[i] += "(" + strings.Join(cols, ", ") + ")"
		}
	}
	return "FOR " + strings.Join(targets, ", ")
}

// changeStreamOptions renders explicitly set options; withDefaults also
// emits null for unset ones so ALTER can reset them to Spanner defaults.
func changeStreamOptions(cs *model.ChangeStream, withDefaults bool) string {
	var opts []string
	switch {
	case cs.RetentionPeriod != "":
		opts = append(opts, fmt.Sprintf("retention_period = '%s'", cs.RetentionPeriod))
	case withDefaults:
		opts = append(opts, "retention_period = null")
	}
	switch {
	case cs.ValueCaptureType != "":
		opts = append(opts, fmt.Sprintf("value_capture_type = '%s'", cs.ValueCaptureType))
	case withDefaults:
		opts = append(opts, "value_capture_type = null")
	}
	if len(opts) == 0 {
		return ""
	}
	return "OPTIONS (" + strings.Join(opts, ", ") + ")"
}

func CreateChangeStream(cs *model.ChangeStream) string {
	stmt := fmt.Sprintf("CREATE CHANGE STREAM %s %s", Q(cs.Name), forClause(cs))
	if opts := changeStreamOptions(cs, false); opts != "" {
		stmt += " " + opts
	}
	return stmt
}

func AlterChangeStreamSetFor(cs *model.ChangeStream) string {
	return fmt.Sprintf("ALTER CHANGE STREAM %s SET %s", Q(cs.Name), forClause(cs))
}

func AlterChangeStreamSetOptions(cs *model.ChangeStream) string {
	return fmt.Sprintf("ALTER CHANGE STREAM %s SET %s", Q(cs.Name), changeStreamOptions(cs, true))
}

func DropChangeStream(name string) string {
	return "DROP CHANGE STREAM " + Q(name)
}
