package qlvm

import (
	"reflect"
	"strings"
	"sync"
	"time"
)

// ── Tag format ───────────────────────────────────────────────────────────────
//
//   type Record struct {
//       TicketNo    string    `qlvm:"ticket"`           // String (inferred)
//       Quantity    float64   `qlvm:"hours"`            // Number (inferred)
//       DateDoc     string    `qlvm:"date,date"`        // Date   (explicit override)
//       CreatedAt   time.Time `qlvm:"created"`          // Date   (inferred)
//       Active      bool      `qlvm:"active"`           // Bool   (inferred)
//       Internal    string    `qlvm:"-"`                // excluded
//       Unlabelled  string                              // excluded (no tag)
//   }
//
// Tag syntax:  `qlvm:"<name>[,<type>]"`
//   name  — query field name; "-" excludes the field
//   type  — optional explicit FieldType: "string", "number", "bool", "date"
//           If omitted, the type is inferred from the Go type (see inferType).

const tagKey = "qlvm"

// fieldEntry is the pre-computed metadata for one struct field.
// The index path mirrors reflect.Type.FieldByIndex semantics and handles
// anonymous embeds of arbitrary depth.
type fieldEntry struct {
	name      string
	fieldType FieldType
	index     []int // path for reflect.Value.FieldByIndex
}

// typeCache maps reflect.Type → []fieldEntry.
// Populated on first call to schemaFields or resolverFields; never mutated after.
var typeCache sync.Map

// schemaFields returns (or builds and caches) the field metadata for T.
func schemaFields(t reflect.Type) []fieldEntry {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if v, ok := typeCache.Load(t); ok {
		return v.([]fieldEntry)
	}
	entries := walkFields(t, nil)
	typeCache.Store(t, entries)
	return entries
}

// walkFields recursively visits struct fields, flattening anonymous embeds.
// indexPrefix is the accumulated field-index path from parent structs.
func walkFields(t reflect.Type, indexPrefix []int) []fieldEntry {
	var entries []fieldEntry

	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)

		// Skip unexported fields
		if !sf.IsExported() {
			continue
		}

		idx := append(append([]int(nil), indexPrefix...), i)

		// Anonymous (embedded) struct — recurse and flatten
		ft := sf.Type
		if ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		if sf.Anonymous && ft.Kind() == reflect.Struct {
			entries = append(entries, walkFields(ft, idx)...)
			continue
		}

		// Explicit qlvm tag required; fields without a tag are skipped
		tag, ok := sf.Tag.Lookup(tagKey)
		if !ok {
			continue
		}

		// Parse tag
		name, typeHint, _ := strings.Cut(tag, ",")
		name = strings.TrimSpace(name)
		if name == "-" || name == "" {
			continue
		}

		// Determine FieldType
		var ftype FieldType
		if typeHint != "" {
			ftype = parseTypeHint(typeHint)
		} else {
			ftype = inferType(sf.Type)
		}

		entries = append(entries, fieldEntry{
			name:      name,
			fieldType: ftype,
			index:     idx,
		})
	}

	return entries
}

// inferType maps a Go reflect.Type to a qlvm FieldType.
func inferType(t reflect.Type) FieldType {
	// Dereference pointer
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	// time.Time → Date
	if t == reflect.TypeOf(time.Time{}) {
		return Date
	}

	switch t.Kind() {
	case reflect.Bool:
		return Bool
	case reflect.Float32, reflect.Float64,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return Number
	default:
		return String
	}
}

// parseTypeHint converts a tag hint string to a FieldType.
func parseTypeHint(hint string) FieldType {
	switch strings.ToLower(strings.TrimSpace(hint)) {
	case "number", "num", "float", "int":
		return Number
	case "bool", "boolean":
		return Bool
	case "date", "time":
		return Date
	default:
		return String
	}
}

// ── SchemaFromStruct ─────────────────────────────────────────────────────────

// SchemaFromStruct builds a *Schema by reflecting over the fields of T.
// T must be a struct (or pointer to struct).
//
// Use the returned schema directly or chain further registrations:
//
//	schema := qlvm.SchemaFromStruct[MyRecord]().
//	    Prefix('.', "ticket", qlvm.Contains).
//	    Suffix('?', qlvm.Exists())
//
// Fields without a `qlvm` tag, or tagged `qlvm:"-"`, are excluded.
// Anonymous embedded structs are walked recursively and their fields
// are flattened into the schema.
func SchemaFromStruct[T any]() *Schema {
	var zero T
	t := reflect.TypeOf(zero)
	entries := schemaFields(t)

	s := NewSchema()
	for _, e := range entries {
		s.fields[e.name] = fieldDef{typ: e.fieldType}
	}
	return s
}

// ── ResolverOf ───────────────────────────────────────────────────────────────

// ResolverOf returns a Resolver that reads fields from v using the
// pre-computed (and cached) reflection metadata for T.
//
// The returned Resolver is a plain function value — safe to call from
// multiple goroutines, cheap to create (one reflect.ValueOf call).
//
// For fields with custom extraction logic, wrap the returned Resolver:
//
//	base := qlvm.ResolverOf(record)
//	custom := func(field string) (any, bool) {
//	    if field == "project" {
//	        return computeProject(record), true
//	    }
//	    return base(field)
//	}
func ResolverOf[T any](v T) Resolver {
	var zero T
	t := reflect.TypeOf(zero)
	entries := schemaFields(t)

	// Build a name→entry index for O(1) lookup inside the closure.
	// This map is shared across all Resolvers for the same type T
	// (it is part of the cached metadata, allocated once).
	idx := nameIndex(t, entries)

	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}

	return func(field string) (any, bool) {
		e, ok := idx[field]
		if !ok {
			return nil, false
		}
		fv := rv.FieldByIndex(e.index)
		return extractValue(fv, e.fieldType), true
	}
}

// nameIndex returns a name→fieldEntry map for the given type.
// The map itself is also cached inside typeCache under a sentinel key.
func nameIndex(t reflect.Type, entries []fieldEntry) map[string]fieldEntry {
	type indexKey struct{ t reflect.Type }
	key := indexKey{t}
	if v, ok := typeCache.Load(key); ok {
		return v.(map[string]fieldEntry)
	}
	m := make(map[string]fieldEntry, len(entries))
	for _, e := range entries {
		m[e.name] = e
	}
	typeCache.Store(key, m)
	return m
}

// extractValue converts a reflect.Value to the concrete Go value the VM
// expects for each FieldType.
func extractValue(fv reflect.Value, ft FieldType) any {
	// Handle pointer fields — dereference or return zero.
	if fv.Kind() == reflect.Ptr {
		if fv.IsNil() {
			return zeroFor(ft)
		}
		fv = fv.Elem()
	}

	switch ft {
	case Bool:
		return fv.Bool()

	case Number:
		switch fv.Kind() {
		case reflect.Float32, reflect.Float64:
			return fv.Float()
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return float64(fv.Int())
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return float64(fv.Uint())
		}
		return float64(0)

	case Date:
		// time.Time fields — return "YYYY-MM-DD" string for uniform comparison.
		if fv.Type() == reflect.TypeOf(time.Time{}) {
			t := fv.Interface().(time.Time)
			if t.IsZero() {
				return ""
			}
			return t.Format("2006-01-02")
		}
		// Fallthrough: string field tagged as date (e.g. "2025-01-01T00:00:00Z")
		s := fv.String()
		if len(s) >= 10 {
			return s[:10] // trim to YYYY-MM-DD
		}
		return s

	default: // String
		return fv.String()
	}
}
