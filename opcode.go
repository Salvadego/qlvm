package qlvm

// Op is a stack machine instruction code.
type Op int

const (
	// -- Field loader -------------------------------------------------------
	// OP_LOAD calls the Resolver with Instruction.StrVal as the field name.
	// It pushes the returned value; if the field is missing it pushes the
	// zero value for the field's registered type.
	OP_LOAD Op = iota

	// -- Literal pushes -----------------------------------------------------
	OP_PUSH_STRING // push Instruction.StrVal
	OP_PUSH_FLOAT  // push Instruction.FloatVal
	OP_PUSH_BOOL   // push Instruction.BoolVal

	// -- Comparisons --------------------------------------------------------
	// All pop (b then a) from the stack and push a bool.
	// String comparisons are case-insensitive.
	// Date comparisons (when FieldType == Date) use time.Parse.
	OP_EQ       // a == b
	OP_NEQ      // a != b
	OP_GT       // a > b
	OP_GTE      // a >= b
	OP_LT       // a < b
	OP_LTE      // a <= b
	OP_CONTAINS // strings.Contains(a, b)  (case-insensitive)
	OP_REGEX    // regexp.MatchString(b, a)

	// -- Logic --------------------------------------------------------------
	OP_AND // pop b, pop a → a && b
	OP_OR  // pop b, pop a → a || b
	OP_NOT // pop a        → !a
)

// Contains is an alias used in schema registration for readability.
//
//	schema.Prefix('.', "ticket", qlvm.Contains)
const Contains = OP_CONTAINS

// EQ, NEQ, GT, GTE, LT, LTE are aliases for schema registration.
const (
	EQ  = OP_EQ
	NEQ = OP_NEQ
	GT  = OP_GT
	GTE = OP_GTE
	LT  = OP_LT
	LTE = OP_LTE
)

// Instruction is one entry in a compiled Program.
type Instruction struct {
	Op        Op
	StrVal    string    // OP_LOAD field name; OP_PUSH_STRING value
	FloatVal  float64   // OP_PUSH_FLOAT value
	BoolVal   bool      // OP_PUSH_BOOL value
	FieldType FieldType // OP_LOAD only — the registered type of the field
}

// Program is the output of compilation: a flat, immutable slice of
// instructions that the VM executes left-to-right.
// Programs are safe to share and reuse across goroutines.
type Program []Instruction
