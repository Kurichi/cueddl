// Package introspect reads the live schema of a Cloud Spanner database
// from INFORMATION_SCHEMA into the shared model.
package introspect

import (
	"context"
	"fmt"
	"strings"

	"cloud.google.com/go/spanner"
	"google.golang.org/api/iterator"

	"github.com/Kurichi/cueddl/internal/model"
)

// Introspect returns the current schema of the database the client is
// connected to. Only the default schema (”) is inspected.
func Introspect(ctx context.Context, client *spanner.Client) (*model.Database, error) {
	db := &model.Database{}
	tables := map[string]*model.Table{}

	// Tables and interleave relationships.
	err := query(ctx, client, `
		SELECT t.table_name, t.parent_table_name, t.on_delete_action
		FROM information_schema.tables AS t
		WHERE t.table_schema = '' AND t.table_type = 'BASE TABLE'
		ORDER BY t.table_name`,
		func(row *spanner.Row) error {
			var name string
			var parent, onDelete spanner.NullString
			if err := row.Columns(&name, &parent, &onDelete); err != nil {
				return err
			}
			t := &model.Table{Name: name}
			if parent.Valid {
				t.Interleave = &model.Interleave{Parent: parent.StringVal, OnDelete: "NO ACTION"}
				if onDelete.Valid {
					t.Interleave.OnDelete = onDelete.StringVal
				}
			}
			tables[name] = t
			db.Tables = append(db.Tables, t)
			return nil
		})
	if err != nil {
		return nil, fmt.Errorf("introspect tables: %w", err)
	}

	// Columns in ordinal order.
	err = query(ctx, client, `
		SELECT c.table_name, c.column_name, c.spanner_type, c.is_nullable, c.column_default
		FROM information_schema.columns AS c
		JOIN information_schema.tables AS t
		  ON t.table_schema = c.table_schema AND t.table_name = c.table_name
		WHERE c.table_schema = '' AND t.table_type = 'BASE TABLE'
		ORDER BY c.table_name, c.ordinal_position`,
		func(row *spanner.Row) error {
			var table, name, typ, nullable string
			var def spanner.NullString
			if err := row.Columns(&table, &name, &typ, &nullable, &def); err != nil {
				return err
			}
			t := tables[table]
			if t == nil {
				return nil
			}
			t.Columns = append(t.Columns, &model.Column{
				Name:    name,
				Type:    model.NormalizeType(typ),
				NotNull: nullable == "NO",
				Default: def.StringVal,
			})
			return nil
		})
	if err != nil {
		return nil, fmt.Errorf("introspect columns: %w", err)
	}

	// Primary keys and secondary index key parts come from index_columns.
	// Rows with a NULL ordinal_position are STORING columns.
	indexes := map[string]*model.Index{} // "table\x00index" -> index
	err = query(ctx, client, `
		SELECT i.table_name, i.index_name, i.index_type, i.is_unique, i.is_null_filtered
		FROM information_schema.indexes AS i
		WHERE i.table_schema = '' AND i.index_type = 'INDEX' AND i.spanner_is_managed = FALSE
		ORDER BY i.table_name, i.index_name`,
		func(row *spanner.Row) error {
			var table, name, typ string
			var unique, nullFiltered bool
			if err := row.Columns(&table, &name, &typ, &unique, &nullFiltered); err != nil {
				return err
			}
			t := tables[table]
			if t == nil {
				return nil
			}
			idx := &model.Index{Name: name, Unique: unique, NullFiltered: nullFiltered}
			indexes[table+"\x00"+name] = idx
			t.Indexes = append(t.Indexes, idx)
			return nil
		})
	if err != nil {
		return nil, fmt.Errorf("introspect indexes: %w", err)
	}

	err = query(ctx, client, `
		SELECT ic.table_name, ic.index_name, ic.index_type, ic.column_name,
		       ic.ordinal_position, ic.column_ordering
		FROM information_schema.index_columns AS ic
		WHERE ic.table_schema = ''
		ORDER BY ic.table_name, ic.index_name, ic.ordinal_position`,
		func(row *spanner.Row) error {
			var table, index, typ, column string
			var pos spanner.NullInt64
			var ordering spanner.NullString
			if err := row.Columns(&table, &index, &typ, &column, &pos, &ordering); err != nil {
				return err
			}
			switch typ {
			case "PRIMARY_KEY":
				if t := tables[table]; t != nil {
					t.PrimaryKey = append(t.PrimaryKey, model.KeyPart{
						Column: column,
						Desc:   ordering.Valid && strings.HasPrefix(ordering.StringVal, "DESC"),
					})
				}
			case "INDEX":
				idx := indexes[table+"\x00"+index]
				if idx == nil {
					return nil // managed index
				}
				if !pos.Valid {
					idx.Storing = append(idx.Storing, column)
					return nil
				}
				idx.Columns = append(idx.Columns, model.KeyPart{
					Column: column,
					Desc:   ordering.Valid && strings.HasPrefix(ordering.StringVal, "DESC"),
				})
			}
			return nil
		})
	if err != nil {
		return nil, fmt.Errorf("introspect index columns: %w", err)
	}

	// Foreign keys: pair the constrained columns with the columns of the
	// referenced unique/primary-key constraint by ordinal position.
	type fkInfo struct {
		fk         *model.ForeignKey
		uniqueName string
	}
	fks := map[string]*fkInfo{} // constraint name -> info
	var fkOrder []string
	err = query(ctx, client, `
		SELECT tc.constraint_name, tc.table_name, rc.unique_constraint_name, rc.delete_rule
		FROM information_schema.table_constraints AS tc
		JOIN information_schema.referential_constraints AS rc
		  ON rc.constraint_schema = tc.constraint_schema
		 AND rc.constraint_name = tc.constraint_name
		WHERE tc.constraint_schema = '' AND tc.constraint_type = 'FOREIGN KEY'
		ORDER BY tc.table_name, tc.constraint_name`,
		func(row *spanner.Row) error {
			var name, table, unique, deleteRule string
			if err := row.Columns(&name, &table, &unique, &deleteRule); err != nil {
				return err
			}
			t := tables[table]
			if t == nil {
				return nil
			}
			fk := &model.ForeignKey{Name: name, OnDelete: deleteRule}
			t.ForeignKeys = append(t.ForeignKeys, fk)
			fks[name] = &fkInfo{fk: fk, uniqueName: unique}
			fkOrder = append(fkOrder, name)
			return nil
		})
	if err != nil {
		return nil, fmt.Errorf("introspect foreign keys: %w", err)
	}

	if len(fks) > 0 {
		// All key_column_usage rows: both FK columns and the referenced
		// constraints' columns.
		type kcuRow struct {
			table  string
			column string
		}
		uniqueCols := map[string][]kcuRow{}
		err = query(ctx, client, `
			SELECT kcu.constraint_name, kcu.table_name, kcu.column_name
			FROM information_schema.key_column_usage AS kcu
			WHERE kcu.constraint_schema = ''
			ORDER BY kcu.constraint_name, kcu.ordinal_position`,
			func(row *spanner.Row) error {
				var constraint, table, column string
				if err := row.Columns(&constraint, &table, &column); err != nil {
					return err
				}
				if info, ok := fks[constraint]; ok {
					info.fk.Columns = append(info.fk.Columns, column)
					return nil
				}
				uniqueCols[constraint] = append(uniqueCols[constraint], kcuRow{table, column})
				return nil
			})
		if err != nil {
			return nil, fmt.Errorf("introspect key column usage: %w", err)
		}
		for _, name := range fkOrder {
			info := fks[name]
			for _, r := range uniqueCols[info.uniqueName] {
				info.fk.RefTable = r.table
				info.fk.RefColumns = append(info.fk.RefColumns, r.column)
			}
		}
	}

	// User-defined CHECK constraints. Spanner also surfaces NOT NULL as
	// system check constraints named CK_IS_NOT_NULL_*; those are skipped.
	err = query(ctx, client, `
		SELECT tc.table_name, cc.constraint_name, cc.check_clause
		FROM information_schema.check_constraints AS cc
		JOIN information_schema.table_constraints AS tc
		  ON tc.constraint_schema = cc.constraint_schema
		 AND tc.constraint_name = cc.constraint_name
		WHERE cc.constraint_schema = '' AND tc.constraint_type = 'CHECK'
		  AND NOT STARTS_WITH(cc.constraint_name, 'CK_IS_NOT_NULL')
		ORDER BY tc.table_name, cc.constraint_name`,
		func(row *spanner.Row) error {
			var table, name, clause string
			if err := row.Columns(&table, &name, &clause); err != nil {
				return err
			}
			if t := tables[table]; t != nil {
				t.Checks = append(t.Checks, &model.Check{Name: name, Expression: clause})
			}
			return nil
		})
	if err != nil {
		return nil, fmt.Errorf("introspect check constraints: %w", err)
	}

	return db, nil
}

func query(ctx context.Context, client *spanner.Client, sql string, fn func(*spanner.Row) error) error {
	iter := client.Single().Query(ctx, spanner.NewStatement(sql))
	defer iter.Stop()
	for {
		row, err := iter.Next()
		if err == iterator.Done {
			return nil
		}
		if err != nil {
			return err
		}
		if err := fn(row); err != nil {
			return err
		}
	}
}
