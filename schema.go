// Package qlvm is a generic, embeddable query-language virtual machine.
//
// It compiles a human-readable filter string into a stack-based Program
// and runs it against any data type through a [Resolver] function —
// no reflection, no interface assertions in hot paths, no coupling to
// your types.
//
// Quick start:
//
//	engine := qlvm.New(
//	    qlvm.NewSchema().
//	        Field("ticket",      qlvm.String).
//	        Field("hours",       qlvm.Number).
//	        Field("date",        qlvm.Date).
//	        Field("has_ticket",  qlvm.Bool).
//	        Prefix('.', "ticket", qlvm.Contains).  // .bug  → ticket CONTAINS 'bug'
//	        Suffix('?', qlvm.Exists()),             // hours? → hours > 0
//	)
//
//	match, err := engine.Match("hours >= 4 AND .bug", func(field string) (any, bool) {
//	    switch field {
//	    case "ticket": return myRecord.Ticket, true
//	    case "hours":  return myRecord.Hours,  true
//	    }
//	    return nil, false
//	})
package qlvm

// FieldType is the semantic type of a registered field.
// The VM uses it to apply the right comparison semantics
// (e.g. lexicographic vs numeric vs date ordering).
type FieldType int

const (
	String FieldType = iota // case-insensitive string
	Number                  // float64
	Bool                    // bool
	Date                    // "YYYY-MM-DD" — ordered via time.Parse
)

// PrefixRule defines how a prefix symbol expands into an expression.
//
// Example: registering '.' as PrefixRule{Field:"ticket", Op:Contains}
// makes ".bug" compile to:  LOAD("ticket")  PUSH_STRING("bug")  CONTAINS
type PrefixRule struct {
	Field string
	Op    Op
}

// SuffixExpansion is the operator + constant emitted when a suffix
// symbol is applied to a field of a particular FieldType.
type SuffixExpansion struct {
	Op       Op
	StrVal   string
	FloatVal float64
	BoolVal  bool
}

// SuffixRule maps each FieldType to the expansion to emit.
// Use [Exists] for the common "field is non-empty / non-zero" pattern.
type SuffixRule struct {
	ByType map[FieldType]SuffixExpansion
}

// Exists returns a SuffixRule that means "has a value":
//   - String → field != ""
//   - Number → field > 0
//   - Bool   → field == true
//   - Date   → field != ""
func Exists() SuffixRule {
	return SuffixRule{ByType: map[FieldType]SuffixExpansion{
		String: {Op: OP_NEQ, StrVal: ""},
		Number: {Op: OP_GT, FloatVal: 0},
		Bool:   {Op: OP_EQ, BoolVal: true},
		Date:   {Op: OP_NEQ, StrVal: ""},
	}}
}

type fieldDef struct {
	typ FieldType
}

// Schema describes the query language for one specific domain:
// which fields exist, what their types are, and what shorthand
// symbols are available.
//
// Build one with [NewSchema] and register everything before passing
// it to [New]. Schemas are read-only at runtime; share freely.
type Schema struct {
	fields map[string]fieldDef
	prefix map[rune]PrefixRule
	suffix map[rune]SuffixRule
}

// NewSchema returns an empty Schema ready for field and symbol
// registration via the fluent builder methods.
func NewSchema() *Schema {
	return &Schema{
		fields: make(map[string]fieldDef),
		prefix: make(map[rune]PrefixRule),
		suffix: make(map[rune]SuffixRule),
	}
}

// Field registers a named field with its semantic type.
// Field names must be valid identifiers (letters, digits, underscores).
func (s *Schema) Field(name string, typ FieldType) *Schema {
	s.fields[name] = fieldDef{typ: typ}
	return s
}

// Prefix registers a symbol rune that can appear before a bare word.
//
//	s.Prefix('.', "ticket", qlvm.Contains)  // .bug  → ticket CONTAINS 'bug'
//	s.Prefix('#', "id",     qlvm.EQ)        // #42   → id = '42'
//	s.Prefix('@', "user",   qlvm.EQ)        // @alice → user = 'alice'
func (s *Schema) Prefix(sym rune, field string, op Op) *Schema {
	s.prefix[sym] = PrefixRule{Field: field, Op: op}
	return s
}

// Suffix registers a symbol rune that can appear directly after a
// field name (no space).
//
//	s.Suffix('?', qlvm.Exists())   // ticket? → ticket != ''
func (s *Schema) Suffix(sym rune, rule SuffixRule) *Schema {
	s.suffix[sym] = rule
	return s
}

// field looks up a registered field definition.
func (s *Schema) field(name string) (fieldDef, bool) {
	f, ok := s.fields[name]
	return f, ok
}

// prefixRunes returns the set of registered prefix symbol runes.
// Used by the lexer to recognise symbol characters.
func (s *Schema) prefixRunes() map[rune]bool {
	m := make(map[rune]bool, len(s.prefix))
	for r := range s.prefix {
		m[r] = true
	}
	return m
}

// suffixRunes returns the set of registered suffix symbol runes.
func (s *Schema) suffixRunes() map[rune]bool {
	m := make(map[rune]bool, len(s.suffix))
	for r := range s.suffix {
		m[r] = true
	}
	return m
}
