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
	if c.Default != "" {
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
	return stmt
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
// column definition (minus default) in ALTER COLUMN.
func AlterColumn(table string, c *model.Column) string {
	def := Q(c.Name) + " " + c.Type
	if c.NotNull {
		def += " NOT NULL"
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
