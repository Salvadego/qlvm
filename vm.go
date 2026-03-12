package qlvm

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// vm is a stack-based virtual machine that evaluates a compiled Program
// against a single record via a Resolver.
// It is intentionally unexported; use [Engine] or [CompiledQuery].
type vm struct {
	stack   []any
	prog    Program
	resolve Resolver
}

func newVM(prog Program, resolve Resolver) *vm {
	return &vm{prog: prog, resolve: resolve}
}

func (v *vm) push(val any) { v.stack = append(v.stack, val) }

func (v *vm) pop() (any, error) {
	if len(v.stack) == 0 {
		return nil, fmt.Errorf("qlvm: stack underflow")
	}
	top := v.stack[len(v.stack)-1]
	v.stack = v.stack[:len(v.stack)-1]
	return top, nil
}

// run executes all instructions and returns the final bool.
func (v *vm) run() (bool, error) {
	for _, instr := range v.prog {
		if err := v.exec(instr); err != nil {
			return false, err
		}
	}
	if len(v.stack) == 0 {
		return false, fmt.Errorf("qlvm: empty stack after execution")
	}
	result, ok := v.stack[len(v.stack)-1].(bool)
	if !ok {
		return false, fmt.Errorf("qlvm: top of stack is %T, expected bool", v.stack[len(v.stack)-1])
	}
	return result, nil
}

func (v *vm) exec(instr Instruction) error {
	switch instr.Op {

	// -- Loader -------------------------------------------------------------
	case OP_LOAD:
		val, ok := v.resolve(instr.StrVal)
		if !ok {
			// Push the zero value for the field's type rather than erroring.
			val = zeroFor(instr.FieldType)
		}
		v.push(val)

	// -- Literals -----------------------------------------------------------
	case OP_PUSH_STRING:
		v.push(instr.StrVal)
	case OP_PUSH_FLOAT:
		v.push(instr.FloatVal)
	case OP_PUSH_BOOL:
		v.push(instr.BoolVal)

	// -- Comparisons --------------------------------------------------------
	case OP_EQ:
		return v.binary(func(a, b any) (bool, error) { return compareEq(a, b) })
	case OP_NEQ:
		return v.binary(func(a, b any) (bool, error) {
			eq, err := compareEq(a, b)
			return !eq, err
		})
	case OP_GT:
		return v.binary(func(a, b any) (bool, error) { return compareOrd(a, b, ">") })
	case OP_GTE:
		return v.binary(func(a, b any) (bool, error) { return compareOrd(a, b, ">=") })
	case OP_LT:
		return v.binary(func(a, b any) (bool, error) { return compareOrd(a, b, "<") })
	case OP_LTE:
		return v.binary(func(a, b any) (bool, error) { return compareOrd(a, b, "<=") })
	case OP_CONTAINS:
		return v.binary(func(a, b any) (bool, error) {
			sa, ok1 := a.(string)
			sb, ok2 := b.(string)
			if !ok1 || !ok2 {
				return false, fmt.Errorf("qlvm: CONTAINS requires strings, got %T and %T", a, b)
			}
			return strings.Contains(strings.ToLower(sa), strings.ToLower(sb)), nil
		})
	case OP_REGEX:
		return v.binary(func(a, b any) (bool, error) {
			sa, ok1 := a.(string)
			sb, ok2 := b.(string)
			if !ok1 || !ok2 {
				return false, fmt.Errorf("qlvm: ~ requires strings, got %T and %T", a, b)
			}
			re, err := regexp.Compile(sb)
			if err != nil {
				return false, fmt.Errorf("qlvm: invalid regex %q: %w", sb, err)
			}
			return re.MatchString(sa), nil
		})

	// -- Logic --------------------------------------------------------------
	case OP_AND:
		return v.binary(func(a, b any) (bool, error) {
			ba, ok1 := a.(bool)
			bb, ok2 := b.(bool)
			if !ok1 || !ok2 {
				return false, fmt.Errorf("qlvm: AND requires bools, got %T and %T", a, b)
			}
			return ba && bb, nil
		})
	case OP_OR:
		return v.binary(func(a, b any) (bool, error) {
			ba, ok1 := a.(bool)
			bb, ok2 := b.(bool)
			if !ok1 || !ok2 {
				return false, fmt.Errorf("qlvm: OR requires bools, got %T and %T", a, b)
			}
			return ba || bb, nil
		})
	case OP_NOT:
		val, err := v.pop()
		if err != nil {
			return err
		}
		b, ok := val.(bool)
		if !ok {
			return fmt.Errorf("qlvm: NOT requires bool, got %T", val)
		}
		v.push(!b)
	}
	return nil
}

// binary pops b then a, calls fn(a, b), pushes the result.
func (v *vm) binary(fn func(a, b any) (bool, error)) error {
	b, err := v.pop()
	if err != nil {
		return err
	}
	a, err := v.pop()
	if err != nil {
		return err
	}
	result, err := fn(a, b)
	if err != nil {
		return err
	}
	v.push(result)
	return nil
}

// -- Comparison helpers -------------------------------------------------------

func compareEq(a, b any) (bool, error) {
	switch av := a.(type) {
	case string:
		if bv, ok := b.(string); ok {
			return strings.EqualFold(av, bv), nil
		}
	case float64:
		if bv, ok := toFloat(b); ok {
			return av == bv, nil
		}
	case bool:
		if bv, ok := b.(bool); ok {
			return av == bv, nil
		}
	}
	return false, fmt.Errorf("qlvm: incompatible types for =: %T and %T", a, b)
}

func compareOrd(a, b any, op string) (bool, error) {
	// Numeric comparison
	af, aIsFloat := toFloat(a)
	bf, bIsFloat := toFloat(b)
	if aIsFloat && bIsFloat {
		return applyFloatOp(af, bf, op), nil
	}

	// String / date comparison
	as, aIsStr := a.(string)
	bs, bIsStr := b.(string)
	if aIsStr && bIsStr {
		// Try date-aware comparison first; fall back to lexicographic.
		at, errA := parseDate(as)
		bt, errB := parseDate(bs)
		if errA == nil && errB == nil {
			return applyTimeOp(at, bt, op), nil
		}
		return applyStrOp(as, bs, op), nil
	}

	return false, fmt.Errorf("qlvm: cannot compare %T and %T with %s", a, b, op)
}

func applyFloatOp(a, b float64, op string) bool {
	switch op {
	case ">":
		return a > b
	case ">=":
		return a >= b
	case "<":
		return a < b
	case "<=":
		return a <= b
	}
	return false
}

func applyStrOp(a, b, op string) bool {
	switch op {
	case ">":
		return a > b
	case ">=":
		return a >= b
	case "<":
		return a < b
	case "<=":
		return a <= b
	}
	return false
}

func applyTimeOp(a, b time.Time, op string) bool {
	switch op {
	case ">":
		return a.After(b)
	case ">=":
		return !a.Before(b)
	case "<":
		return a.Before(b)
	case "<=":
		return !a.After(b)
	}
	return false
}

var dateLayouts = []string{
	"2006-01-02",
	"2006-01-02T15:04:05Z",
	time.RFC3339,
}

func parseDate(s string) (time.Time, error) {
	for _, l := range dateLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised date: %q", s)
}

func toFloat(v any) (float64, bool) {
	switch v := v.(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	}
	return 0, false
}

func zeroFor(ft FieldType) any {
	switch ft {
	case Number:
		return float64(0)
	case Bool:
		return false
	default:
		return ""
	}
}
