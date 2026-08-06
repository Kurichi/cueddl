// Package diff computes the DDL statements that migrate an actual Spanner
// schema to the desired one.
//
// Statement ordering matters in Spanner:
//   - indexes and incoming foreign keys must be gone before DROP TABLE
//   - interleaved parents must exist before children (and drop in reverse)
//   - foreign keys are added last, after every table exists
package diff

import (
	"fmt"
	"slices"

	"github.com/Kurichi/cueddl/internal/ddlgen"
	"github.com/Kurichi/cueddl/internal/model"
)

type Statement struct {
	SQL         string
	Destructive bool // loses data if applied (DROP TABLE / DROP COLUMN)
}

type Plan struct {
	Statements []Statement
	// Warnings lists desired changes that cannot be expressed as Spanner
	// DDL (e.g. primary-key changes) and were therefore skipped.
	Warnings []string
}

func (p *Plan) add(sql string) { p.Statements = append(p.Statements, Statement{SQL: sql}) }
func (p *Plan) addDestructive(sql string) {
	p.Statements = append(p.Statements, Statement{SQL: sql, Destructive: true})
}
func (p *Plan) warnf(format string, args ...any) {
	p.Warnings = append(p.Warnings, fmt.Sprintf(format, args...))
}

func (p *Plan) HasDestructive() bool {
	return slices.ContainsFunc(p.Statements, func(s Statement) bool { return s.Destructive })
}

// Diff returns the ordered migration plan from actual to desired.
func Diff(desired, actual *model.Database) *Plan {
	p := &Plan{}

	dropped := map[string]bool{}
	for _, t := range actual.Tables {
		if desired.Table(t.Name) == nil {
			dropped[t.Name] = true
		}
	}

	// Phase 1: drop constraints and indexes that must go away first.
	for _, at := range actual.Tables {
		dt := desired.Table(at.Name)
		for _, fk := range at.ForeignKeys {
			if dropped[at.Name] || fkNeedsDrop(dt, fk) {
				p.add(ddlgen.DropConstraint(at.Name, fk.Name))
			}
		}
		for _, ck := range at.Checks {
			if !dropped[at.Name] && checkNeedsDrop(dt, ck) {
				p.add(ddlgen.DropConstraint(at.Name, ck.Name))
			}
		}
		for _, idx := range at.Indexes {
			if dropped[at.Name] || indexNeedsDrop(dt, idx) {
				p.add(ddlgen.DropIndex(idx.Name))
			}
		}
	}

	// Phase 2: drop tables, interleaved children before their parents.
	for _, t := range reverseTopo(actual.Tables) {
		if dropped[t.Name] {
			p.addDestructive(ddlgen.DropTable(t.Name))
		}
	}

	// Phase 3: create new tables, parents before children.
	created := map[string]bool{}
	for _, t := range topo(desired.Tables) {
		if actual.Table(t.Name) == nil {
			p.add(ddlgen.CreateTable(t))
			created[t.Name] = true
		}
	}

	// Phase 4: column changes on surviving tables.
	for _, dt := range desired.Tables {
		at := actual.Table(dt.Name)
		if at == nil {
			continue
		}
		diffTable(p, dt, at)
	}

	// Phase 5: create indexes (for new tables and surviving ones).
	for _, dt := range desired.Tables {
		at := actual.Table(dt.Name)
		for _, idx := range dt.Indexes {
			if created[dt.Name] || indexNeedsCreate(at, idx) {
				p.add(ddlgen.CreateIndex(dt.Name, idx))
			}
		}
	}

	// Phase 6: add foreign keys and check constraints, now that every
	// table and column exists.
	for _, dt := range desired.Tables {
		at := actual.Table(dt.Name)
		for _, fk := range dt.ForeignKeys {
			if created[dt.Name] || fkNeedsCreate(at, fk) {
				p.add(ddlgen.AddForeignKey(dt.Name, fk))
			}
		}
		for _, ck := range dt.Checks {
			// Checks on brand-new tables are inline in CREATE TABLE.
			if !created[dt.Name] && checkNeedsCreate(at, ck) {
				p.add(ddlgen.AddCheck(dt.Name, ck))
			}
		}
	}

	return p
}

func diffTable(p *Plan, dt, at *model.Table) {
	if !slices.Equal(dt.PrimaryKey, at.PrimaryKey) {
		p.warnf("table %s: primary key change requires recreating the table; skipped (desired %v, actual %v)",
			dt.Name, dt.PrimaryKey, at.PrimaryKey)
	}
	if !interleaveEqual(dt.Interleave, at.Interleave) {
		p.warnf("table %s: interleave change requires recreating the table; skipped", dt.Name)
	}

	for _, ac := range at.Columns {
		if dt.Column(ac.Name) == nil {
			p.addDestructive(ddlgen.DropColumn(dt.Name, ac.Name))
		}
	}
	for _, dc := range dt.Columns {
		ac := at.Column(dc.Name)
		if ac == nil {
			if dc.NotNull && dc.Default == "" {
				p.warnf("table %s: adding NOT NULL column %q without a default fails on non-empty tables",
					dt.Name, dc.Name)
			}
			p.add(ddlgen.AddColumn(dt.Name, dc))
			continue
		}
		if dc.Type != ac.Type || dc.NotNull != ac.NotNull {
			p.add(ddlgen.AlterColumn(dt.Name, dc))
		}
		if model.NormalizeExpr(dc.Default) != model.NormalizeExpr(ac.Default) {
			p.add(ddlgen.SetColumnDefault(dt.Name, dc))
		}
	}
}

func interleaveEqual(a, b *model.Interleave) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	return a == nil || *a == *b
}

func indexEqual(a, b *model.Index) bool {
	return a.Unique == b.Unique &&
		a.NullFiltered == b.NullFiltered &&
		slices.Equal(a.Columns, b.Columns) &&
		sameSet(a.Storing, b.Storing)
}

func fkEqual(a, b *model.ForeignKey) bool {
	return slices.Equal(a.Columns, b.Columns) &&
		a.RefTable == b.RefTable &&
		slices.Equal(a.RefColumns, b.RefColumns) &&
		a.OnDelete == b.OnDelete
}

func checkEqual(a, b *model.Check) bool {
	return model.NormalizeExpr(a.Expression) == model.NormalizeExpr(b.Expression)
}

// sameSet compares STORING column lists order-insensitively, since
// INFORMATION_SCHEMA does not guarantee their order.
func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	as, bs := slices.Clone(a), slices.Clone(b)
	slices.Sort(as)
	slices.Sort(bs)
	return slices.Equal(as, bs)
}

// The *NeedsDrop helpers take the desired table (nil if the actual table is
// being dropped entirely) and an element of the actual schema; *NeedsCreate
// take the actual table (nil when it is new) and a desired element. Changed
// elements are recreated, so they answer true on both sides.

func indexNeedsDrop(dt *model.Table, idx *model.Index) bool {
	if dt == nil {
		return true
	}
	d := dt.Index(idx.Name)
	return d == nil || !indexEqual(d, idx)
}

func indexNeedsCreate(at *model.Table, idx *model.Index) bool {
	a := at.Index(idx.Name)
	return a == nil || !indexEqual(a, idx)
}

func fkNeedsDrop(dt *model.Table, fk *model.ForeignKey) bool {
	if dt == nil {
		return true
	}
	d := dt.ForeignKey(fk.Name)
	return d == nil || !fkEqual(d, fk)
}

func fkNeedsCreate(at *model.Table, fk *model.ForeignKey) bool {
	a := at.ForeignKey(fk.Name)
	return a == nil || !fkEqual(a, fk)
}

func checkNeedsDrop(dt *model.Table, ck *model.Check) bool {
	if dt == nil {
		return true
	}
	d := dt.Check(ck.Name)
	return d == nil || !checkEqual(d, ck)
}

func checkNeedsCreate(at *model.Table, ck *model.Check) bool {
	a := at.Check(ck.Name)
	return a == nil || !checkEqual(a, ck)
}

// topo orders tables so interleave parents and dependsOn targets precede
// their dependents, preserving declaration order among unrelated tables.
// Cycles are rejected by model.Database.Validate before diffing.
func topo(tables []*model.Table) []*model.Table {
	var out []*model.Table
	visited := map[string]bool{}
	byName := map[string]*model.Table{}
	for _, t := range tables {
		byName[t.Name] = t
	}
	var visit func(t *model.Table)
	visit = func(t *model.Table) {
		if visited[t.Name] {
			return
		}
		visited[t.Name] = true
		deps := t.DependsOn
		if t.Interleave != nil {
			deps = append(slices.Clone(deps), t.Interleave.Parent)
		}
		for _, dep := range deps {
			if target := byName[dep]; target != nil {
				visit(target)
			}
		}
		out = append(out, t)
	}
	for _, t := range tables {
		visit(t)
	}
	return out
}

func reverseTopo(tables []*model.Table) []*model.Table {
	ordered := topo(tables)
	slices.Reverse(ordered)
	return ordered
}
