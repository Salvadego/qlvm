package qlvm

import "strings"

// Resolver provides field values for a single record.
// The field name is exactly as registered in the Schema.
// Return (value, true) if the field has a value, (nil, false) if missing.
// Missing fields push a zero value (empty string / 0 / false) rather than
// causing an error.
type Resolver func(field string) (any, bool)

// Engine holds a compiled Schema and exposes the compile/match/filter API.
// Create one with [New]; it is safe to share across goroutines.
type Engine struct {
	schema *Schema
}

// New creates an Engine from a Schema.
//
//	engine := qlvm.New(
//	    qlvm.NewSchema().
//	        Field("name",  qlvm.String).
//	        Field("score", qlvm.Number).
//	        Prefix('@', "name", qlvm.EQ).
//	        Suffix('?', qlvm.Exists()),
//	)
func New(schema *Schema) *Engine {
	return &Engine{schema: schema}
}

// CompiledQuery is a pre-compiled, reusable query.
// Compile once, run many times — no re-parsing overhead.
type CompiledQuery struct {
	prog   Program
	engine *Engine
}

// Compile parses and compiles a query string.
// An empty string compiles to a "match all" query.
// The returned *CompiledQuery is safe to call from multiple goroutines.
func (e *Engine) Compile(query string) (*CompiledQuery, error) {
	prog, err := compile(query, e.schema)
	if err != nil {
		return nil, err
	}
	return &CompiledQuery{prog: prog, engine: e}, nil
}

// Match compiles query and evaluates it against the provided Resolver.
// Use [CompiledQuery.Match] if you need to evaluate the same query
// against many records.
func (e *Engine) Match(query string, resolve Resolver) (bool, error) {
	if strings.TrimSpace(query) == "" {
		return true, nil
	}
	cq, err := e.Compile(query)
	if err != nil {
		return false, err
	}
	return cq.Match(resolve)
}

// Match evaluates the pre-compiled query against a single record.
func (cq *CompiledQuery) Match(resolve Resolver) (bool, error) {
	if len(cq.prog) == 0 {
		return true, nil
	}
	return newVM(cq.prog, resolve).run()
}

// Filter is a generic helper that compiles query once and applies it to
// a slice of items. Items that fail to match or produce a runtime error
// are excluded from the result.
//
// An empty query returns items unchanged.
//
//	results, err := qlvm.Filter(engine, "score >= 90", students, func(s Student) qlvm.Resolver {
//	    return func(field string) (any, bool) {
//	        switch field {
//	        case "name":  return s.Name,  true
//	        case "score": return s.Score, true
//	        }
//	        return nil, false
//	    }
//	})
func Filter[T any](e *Engine, query string, items []T, resolve func(T) Resolver) ([]T, error) {
	if strings.TrimSpace(query) == "" {
		return items, nil
	}
	cq, err := e.Compile(query)
	if err != nil {
		return nil, err
	}
	return FilterCompiled(cq, items, resolve)
}

// FilterCompiled applies a pre-compiled query to a slice.
// Use this when you need to filter many independent slices with the same
// compiled query without recompiling.
func FilterCompiled[T any](cq *CompiledQuery, items []T, resolve func(T) Resolver) ([]T, error) {
	var result []T
	for _, item := range items {
		match, err := cq.Match(resolve(item))
		if err != nil {
			continue // runtime mismatch — skip rather than halt
		}
		if match {
			result = append(result, item)
		}
	}
	return result, nil
}

// Schema returns the Engine's Schema, useful for documentation tools
// or completion helpers that want to list available fields and symbols.
func (e *Engine) Schema() *Schema { return e.schema }

// Fields returns the names of all registered fields, sorted alphabetically.
func (s *Schema) Fields() []string {
	names := make([]string, 0, len(s.fields))
	for n := range s.fields {
		names = append(names, n)
	}
	// simple sort without importing sort to keep the package light
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if names[i] > names[j] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	return names
}

// PrefixSymbols returns a map of registered prefix rune → PrefixRule.
func (s *Schema) PrefixSymbols() map[rune]PrefixRule {
	out := make(map[rune]PrefixRule, len(s.prefix))
	for k, v := range s.prefix {
		out[k] = v
	}
	return out
}

// SuffixSymbols returns a map of registered suffix rune → SuffixRule.
func (s *Schema) SuffixSymbols() map[rune]SuffixRule {
	out := make(map[rune]SuffixRule, len(s.suffix))
	for k, v := range s.suffix {
		out[k] = v
	}
	return out
}
