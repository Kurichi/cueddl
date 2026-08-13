// Package model holds the in-memory schema representation shared by the
// CUE-exported desired state and the INFORMATION_SCHEMA-introspected
// actual state.
package model

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

type Database struct {
	Project       string
	Instance      string
	Name          string
	Tables        []*Table // declaration order
	Views         []*View
	ChangeStreams []*ChangeStream
}

type Table struct {
	Name        string
	Columns     []*Column // declaration order
	PrimaryKey  []KeyPart
	Interleave  *Interleave
	Indexes     []*Index
	ForeignKeys []*ForeignKey
	Checks      []*Check
	// DependsOn lists tables that must be created before this one and
	// dropped after it. It orders DDL only and never appears in it.
	DependsOn []string
	// RenamedFrom names the table this one was renamed from. The diff
	// emits a rename instead of drop + create when the old name still
	// exists in the live schema.
	RenamedFrom       string
	RowDeletionPolicy *RowDeletionPolicy
}

// RowDeletionPolicy is Spanner TTL: rows with Column older than Days
// days are deleted in the background.
type RowDeletionPolicy struct {
	Column string
	Days   int64
}

type ChangeStream struct {
	Name   string
	ForAll bool
	Watch  []ChangeStreamTarget
	// RetentionPeriod and ValueCaptureType are empty when unset, meaning
	// Spanner defaults ("1d" / OLD_AND_NEW_VALUES).
	RetentionPeriod  string
	ValueCaptureType string
}

// ChangeStreamTarget watches one table. Columns nil watches every column;
// an explicitly empty slice watches primary keys only.
type ChangeStreamTarget struct {
	Table   string
	Columns []string
}

type Column struct {
	Name    string
	Type    string
	NotNull bool
	Default string // GoogleSQL expression; empty means no default
	// AllowCommitTimestamp maps to OPTIONS (allow_commit_timestamp=true).
	AllowCommitTimestamp bool
	// Generated marks this as a STORED generated column. Mutually
	// exclusive with Default.
	Generated *GeneratedColumn
}

// GeneratedColumn is a STORED generated column: AS (Expression) STORED.
// Spanner does not support non-stored (virtual) generated columns.
type GeneratedColumn struct {
	Expression string
}

type View struct {
	Name         string
	Definition   string // the SELECT statement after AS
	SecurityType string // "INVOKER" | "DEFINER"
	DependsOn    []string
}

type KeyPart struct {
	Column string
	Desc   bool
}

type Interleave struct {
	Parent   string
	OnDelete string // "NO ACTION" | "CASCADE"
}

type Index struct {
	Name         string
	Columns      []KeyPart
	Unique       bool
	NullFiltered bool
	Storing      []string
}

type ForeignKey struct {
	Name       string
	Columns    []string
	RefTable   string
	RefColumns []string
	OnDelete   string // "NO ACTION" | "CASCADE"
}

type Check struct {
	Name       string
	Expression string
}

func (d *Database) Table(name string) *Table {
	for _, t := range d.Tables {
		if t.Name == name {
			return t
		}
	}
	return nil
}

func (d *Database) View(name string) *View {
	for _, v := range d.Views {
		if v.Name == name {
			return v
		}
	}
	return nil
}

func (d *Database) ChangeStream(name string) *ChangeStream {
	for _, cs := range d.ChangeStreams {
		if cs.Name == name {
			return cs
		}
	}
	return nil
}

func (t *Table) Column(name string) *Column {
	for _, c := range t.Columns {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func (t *Table) Index(name string) *Index {
	for _, i := range t.Indexes {
		if i.Name == name {
			return i
		}
	}
	return nil
}

func (t *Table) ForeignKey(name string) *ForeignKey {
	for _, fk := range t.ForeignKeys {
		if fk.Name == name {
			return fk
		}
	}
	return nil
}

func (t *Table) Check(name string) *Check {
	for _, c := range t.Checks {
		if c.Name == name {
			return c
		}
	}
	return nil
}

var typeSpaces = regexp.MustCompile(`\s+`)

// NormalizeType canonicalizes a Spanner type string for comparison:
// uppercase keywords and no internal whitespace, e.g. "array< string(36) >"
// becomes "ARRAY<STRING(36)>". Spanner types contain no string literals,
// so uppercasing the whole spelling is safe.
func NormalizeType(t string) string {
	return strings.ToUpper(typeSpaces.ReplaceAllString(strings.TrimSpace(t), ""))
}

// NormalizeExpr lightly canonicalizes a GoogleSQL expression for comparison.
// INFORMATION_SCHEMA stores expressions close to their original spelling, so
// only whitespace runs and one layer of redundant outer parentheses are
// normalized. Cosmetic rewrites beyond that may still produce spurious diffs.
func NormalizeExpr(e string) string {
	s := strings.TrimSpace(typeSpaces.ReplaceAllString(e, " "))
	for strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") && balancedTrim(s) {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	return s
}

// balancedTrim reports whether the outer parentheses of s wrap the whole
// expression (so "(a) AND (b)" is not stripped to "a) AND (b").
func balancedTrim(s string) bool {
	depth := 0
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 && i != len(s)-1 {
				return false
			}
		}
	}
	return depth == 0
}

// Validate checks cross-references that the CUE schema cannot express:
// key parts, interleave parents, and foreign-key targets must resolve.
func (d *Database) Validate() error {
	for _, t := range d.Tables {
		for _, kp := range t.PrimaryKey {
			if t.Column(kp.Column) == nil {
				return fmt.Errorf("table %s: primary key column %q not declared", t.Name, kp.Column)
			}
		}
		if t.Interleave != nil {
			parent := d.Table(t.Interleave.Parent)
			if parent == nil {
				return fmt.Errorf("table %s: interleave parent %q not declared", t.Name, t.Interleave.Parent)
			}
			// Spanner requires the child PK to start with the parent PK; it
			// may add no columns at all (a 1:1 interleave, e.g. an
			// extension table sharing its parent's exact key), so only a
			// strictly shorter child key is invalid.
			if len(t.PrimaryKey) < len(parent.PrimaryKey) {
				return fmt.Errorf("table %s: interleaved child primary key must contain parent %s primary key", t.Name, parent.Name)
			}
			for i, kp := range parent.PrimaryKey {
				if t.PrimaryKey[i] != kp {
					return fmt.Errorf("table %s: primary key must be prefixed by parent %s primary key", t.Name, parent.Name)
				}
			}
		}
		for _, idx := range t.Indexes {
			for _, kp := range idx.Columns {
				if t.Column(kp.Column) == nil {
					return fmt.Errorf("index %s on %s: column %q not declared", idx.Name, t.Name, kp.Column)
				}
			}
			for _, c := range idx.Storing {
				if t.Column(c) == nil {
					return fmt.Errorf("index %s on %s: storing column %q not declared", idx.Name, t.Name, c)
				}
			}
		}
		for _, dep := range t.DependsOn {
			if d.Table(dep) == nil {
				return fmt.Errorf("table %s: dependsOn target %q not declared", t.Name, dep)
			}
		}
		for _, c := range t.Columns {
			if c.AllowCommitTimestamp && c.Type != "TIMESTAMP" {
				return fmt.Errorf("table %s: column %q: allowCommitTimestamp requires type TIMESTAMP, got %s",
					t.Name, c.Name, c.Type)
			}
			if c.Generated != nil && c.Default != "" {
				return fmt.Errorf("table %s: column %q: generated and default are mutually exclusive", t.Name, c.Name)
			}
		}
		if rdp := t.RowDeletionPolicy; rdp != nil {
			c := t.Column(rdp.Column)
			if c == nil {
				return fmt.Errorf("table %s: rowDeletionPolicy column %q not declared", t.Name, rdp.Column)
			}
			if c.Type != "TIMESTAMP" {
				return fmt.Errorf("table %s: rowDeletionPolicy column %q must be TIMESTAMP, got %s",
					t.Name, rdp.Column, c.Type)
			}
		}
		for _, fk := range t.ForeignKeys {
			for _, c := range fk.Columns {
				if t.Column(c) == nil {
					return fmt.Errorf("foreign key %s on %s: column %q not declared", fk.Name, t.Name, c)
				}
			}
			ref := d.Table(fk.RefTable)
			if ref == nil {
				return fmt.Errorf("foreign key %s on %s: referenced table %q not declared", fk.Name, t.Name, fk.RefTable)
			}
			for _, c := range fk.RefColumns {
				if ref.Column(c) == nil {
					return fmt.Errorf("foreign key %s on %s: referenced column %s.%q not declared", fk.Name, t.Name, fk.RefTable, c)
				}
			}
			if len(fk.Columns) != len(fk.RefColumns) {
				return fmt.Errorf("foreign key %s on %s: column count mismatch", fk.Name, t.Name)
			}
		}
	}
	for _, v := range d.Views {
		for _, dep := range v.DependsOn {
			if d.Table(dep) == nil && d.View(dep) == nil {
				return fmt.Errorf("view %s: dependsOn target %q not declared", v.Name, dep)
			}
		}
	}
	for _, cs := range d.ChangeStreams {
		if cs.ForAll && len(cs.Watch) > 0 {
			return fmt.Errorf("change stream %s: forAll and watch are mutually exclusive", cs.Name)
		}
		if !cs.ForAll && len(cs.Watch) == 0 {
			return fmt.Errorf("change stream %s: either forAll or watch is required", cs.Name)
		}
		for _, w := range cs.Watch {
			t := d.Table(w.Table)
			if t == nil {
				return fmt.Errorf("change stream %s: watched table %q not declared", cs.Name, w.Table)
			}
			for _, col := range w.Columns {
				if t.Column(col) == nil {
					return fmt.Errorf("change stream %s: watched column %s.%q not declared", cs.Name, w.Table, col)
				}
			}
		}
	}
	return d.checkOrderingCycles()
}

// checkOrderingCycles rejects cycles in the creation-order graph formed by
// interleave parents and dependsOn edges; topological sorting would
// otherwise break the cycle at an arbitrary point silently.
func (d *Database) checkOrderingCycles() error {
	const (
		unvisited = iota
		visiting
		done
	)
	state := map[string]int{}
	var visit func(t *Table) error
	visit = func(t *Table) error {
		switch state[t.Name] {
		case visiting:
			return fmt.Errorf("table %s: dependency cycle through interleave/dependsOn", t.Name)
		case done:
			return nil
		}
		state[t.Name] = visiting
		deps := slices.Clone(t.DependsOn)
		if t.Interleave != nil {
			deps = append(deps, t.Interleave.Parent)
		}
		for _, dep := range deps {
			if next := d.Table(dep); next != nil {
				if err := visit(next); err != nil {
					return err
				}
			}
		}
		state[t.Name] = done
		return nil
	}
	for _, t := range d.Tables {
		if err := visit(t); err != nil {
			return err
		}
	}

	// Views form their own graph: they may depend on other views, while
	// tables never depend on views, so a separate walk suffices.
	vstate := map[string]int{}
	var visitView func(v *View) error
	visitView = func(v *View) error {
		switch vstate[v.Name] {
		case visiting:
			return fmt.Errorf("view %s: dependency cycle through dependsOn", v.Name)
		case done:
			return nil
		}
		vstate[v.Name] = visiting
		for _, dep := range v.DependsOn {
			if next := d.View(dep); next != nil {
				if err := visitView(next); err != nil {
					return err
				}
			}
		}
		vstate[v.Name] = done
		return nil
	}
	for _, v := range d.Views {
		if err := visitView(v); err != nil {
			return err
		}
	}
	return nil
}
