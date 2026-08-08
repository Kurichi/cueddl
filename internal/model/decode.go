package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// DecodeJSON parses a #Database value exported by `cue export`.
//
// CUE emits struct fields in declaration order, but decoding into a Go map
// would destroy that order — and column order is meaningful in CREATE TABLE.
// So objects whose order matters (tables, columns, ...) are walked with
// json.Decoder tokens instead of map-based unmarshalling.
func DecodeJSON(r io.Reader) (*Database, error) {
	dec := json.NewDecoder(r)
	dec.UseNumber()
	db := &Database{}
	err := decodeObject(dec, func(key string) error {
		switch key {
		case "project":
			return decodeString(dec, &db.Project)
		case "instance":
			return decodeString(dec, &db.Instance)
		case "name":
			return decodeString(dec, &db.Name)
		case "tables":
			return decodeObject(dec, func(name string) error {
				t, err := decodeTable(dec)
				if err != nil {
					return fmt.Errorf("table %s: %w", name, err)
				}
				db.Tables = append(db.Tables, t)
				return nil
			})
		case "views":
			return decodeObject(dec, func(name string) error {
				v := &View{}
				if err := decodeValue(dec, v); err != nil {
					return fmt.Errorf("view %s: %w", name, err)
				}
				db.Views = append(db.Views, v)
				return nil
			})
		default:
			return skipValue(dec)
		}
	})
	if err != nil {
		return nil, err
	}
	return db, db.Validate()
}

func decodeTable(dec *json.Decoder) (*Table, error) {
	t := &Table{}
	err := decodeObject(dec, func(key string) error {
		switch key {
		case "name":
			return decodeString(dec, &t.Name)
		case "columns":
			return decodeObject(dec, func(name string) error {
				c := &Column{}
				if err := decodeValue(dec, c); err != nil {
					return fmt.Errorf("column %s: %w", name, err)
				}
				c.Type = NormalizeType(c.Type)
				t.Columns = append(t.Columns, c)
				return nil
			})
		case "primaryKey":
			parts, err := decodeKeyParts(dec)
			t.PrimaryKey = parts
			return err
		case "interleave":
			t.Interleave = &Interleave{}
			return decodeValue(dec, t.Interleave)
		case "dependsOn":
			return decodeValue(dec, &t.DependsOn)
		case "renamedFrom":
			return decodeString(dec, &t.RenamedFrom)
		case "indexes":
			return decodeObject(dec, func(name string) error {
				idx, err := decodeIndex(dec)
				if err != nil {
					return fmt.Errorf("index %s: %w", name, err)
				}
				t.Indexes = append(t.Indexes, idx)
				return nil
			})
		case "foreignKeys":
			return decodeObject(dec, func(name string) error {
				var raw struct {
					Name       string   `json:"name"`
					Columns    []string `json:"columns"`
					References struct {
						Table   string   `json:"table"`
						Columns []string `json:"columns"`
					} `json:"references"`
					OnDelete string `json:"onDelete"`
				}
				if err := decodeValue(dec, &raw); err != nil {
					return fmt.Errorf("foreign key %s: %w", name, err)
				}
				t.ForeignKeys = append(t.ForeignKeys, &ForeignKey{
					Name:       raw.Name,
					Columns:    raw.Columns,
					RefTable:   raw.References.Table,
					RefColumns: raw.References.Columns,
					OnDelete:   raw.OnDelete,
				})
				return nil
			})
		case "checks":
			return decodeObject(dec, func(name string) error {
				c := &Check{}
				if err := decodeValue(dec, c); err != nil {
					return fmt.Errorf("check %s: %w", name, err)
				}
				t.Checks = append(t.Checks, c)
				return nil
			})
		default:
			return skipValue(dec)
		}
	})
	return t, err
}

func decodeIndex(dec *json.Decoder) (*Index, error) {
	idx := &Index{}
	err := decodeObject(dec, func(key string) error {
		switch key {
		case "name":
			return decodeString(dec, &idx.Name)
		case "columns":
			parts, err := decodeKeyParts(dec)
			idx.Columns = parts
			return err
		case "unique":
			return decodeValue(dec, &idx.Unique)
		case "nullFiltered":
			return decodeValue(dec, &idx.NullFiltered)
		case "storing":
			return decodeValue(dec, &idx.Storing)
		default:
			return skipValue(dec)
		}
	})
	return idx, err
}

// decodeKeyParts accepts the #KeyPart shorthand: "col" or {column, desc}.
func decodeKeyParts(dec *json.Decoder) ([]KeyPart, error) {
	var raws []json.RawMessage
	if err := decodeValue(dec, &raws); err != nil {
		return nil, err
	}
	parts := make([]KeyPart, 0, len(raws))
	for _, raw := range raws {
		if len(raw) > 0 && raw[0] == '"' {
			var col string
			if err := json.Unmarshal(raw, &col); err != nil {
				return nil, err
			}
			parts = append(parts, KeyPart{Column: col})
			continue
		}
		var kp struct {
			Column string `json:"column"`
			Desc   bool   `json:"desc"`
		}
		if err := json.Unmarshal(raw, &kp); err != nil {
			return nil, err
		}
		parts = append(parts, KeyPart{Column: kp.Column, Desc: kp.Desc})
	}
	return parts, nil
}

// decodeObject reads an object token by token, invoking field for each key
// with the decoder positioned at the value.
func decodeObject(dec *json.Decoder, field func(key string) error) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return fmt.Errorf("expected object, got %v", tok)
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		if err := field(tok.(string)); err != nil {
			return err
		}
	}
	_, err = dec.Token() // consume '}'
	return err
}

func decodeString(dec *json.Decoder, dst *string) error {
	return decodeValue(dec, dst)
}

func decodeValue(dec *json.Decoder, dst any) error {
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	return d.Decode(dst)
}

func skipValue(dec *json.Decoder) error {
	var raw json.RawMessage
	return dec.Decode(&raw)
}
