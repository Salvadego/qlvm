package qlvm

// compiler is a single-use recursive-descent parser that turns a token
// stream into a Program using schema metadata.
//
// Grammar:
//
//	query    = "" | expr EOF
//	expr     = or_expr
//	or_expr  = and_expr  ( 'OR'  and_expr )*
//	and_expr = not_expr  ( 'AND' not_expr )*
//	not_expr = 'NOT' not_expr | atom
//	atom     = prefix_sym IDENT           - shorthand expansion
//	         | IDENT suffix_sym            - suffix expansion
//	         | IDENT op value             - comparison
//	         | IDENT                      - bare bool field
//	         | '(' expr ')'
//	op       = '=' | '!=' | '>' | '>=' | '<' | '<=' | '~' | 'contains'
//	value    = STRING | NUMBER

import (
	"fmt"
	"strconv"
	"strings"
)

type compiler struct {
	tokens []Token
	pos    int
	prog   Program
	schema *Schema
}

// compile is the entry point: tokenize -> parse -> return Program.
func compile(query string, schema *Schema) (Program, error) {
	if strings.TrimSpace(query) == "" {
		return Program{}, nil
	}
	c := &compiler{
		tokens: tokenizeAll(query, schema),
		schema: schema,
	}
	if err := c.parseExpr(); err != nil {
		return nil, fmt.Errorf("qlvm compile: %w", err)
	}
	if c.peek().Type != TOK_EOF {
		return nil, fmt.Errorf("qlvm compile: unexpected token %q after expression", c.peek().Literal)
	}
	return c.prog, nil
}

// -- token helpers ------------------------------------------------------------

func (c *compiler) peek() Token {
	if c.pos >= len(c.tokens) {
		return Token{Type: TOK_EOF}
	}
	return c.tokens[c.pos]
}

func (c *compiler) peekNext() Token {
	if c.pos+1 >= len(c.tokens) {
		return Token{Type: TOK_EOF}
	}
	return c.tokens[c.pos+1]
}

func (c *compiler) consume() Token {
	t := c.peek()
	if c.pos < len(c.tokens) {
		c.pos++
	}
	return t
}

func (c *compiler) isKeyword(kw string) bool {
	t := c.peek()
	return t.Type == TOK_IDENT && strings.EqualFold(t.Literal, kw)
}

func (c *compiler) emit(instr Instruction) {
	c.prog = append(c.prog, instr)
}

// -- grammar ------------------------------------------------------------------

func (c *compiler) parseExpr() error { return c.parseOr() }

func (c *compiler) parseOr() error {
	if err := c.parseAnd(); err != nil {
		return err
	}
	for c.isKeyword("OR") {
		c.consume()
		if err := c.parseAnd(); err != nil {
			return err
		}
		c.emit(Instruction{Op: OP_OR})
	}
	return nil
}

func (c *compiler) parseAnd() error {
	if err := c.parseNot(); err != nil {
		return err
	}
	for c.isKeyword("AND") {
		c.consume()
		if err := c.parseNot(); err != nil {
			return err
		}
		c.emit(Instruction{Op: OP_AND})
	}
	return nil
}

func (c *compiler) parseNot() error {
	if c.isKeyword("NOT") {
		c.consume()
		if err := c.parseNot(); err != nil {
			return err
		}
		c.emit(Instruction{Op: OP_NOT})
		return nil
	}
	return c.parseAtom()
}

func (c *compiler) parseAtom() error {
	tok := c.peek()

	// -- ( expr ) ---------------------------------------------------------
	if tok.Type == TOK_LPAREN {
		c.consume()
		if err := c.parseExpr(); err != nil {
			return err
		}
		if c.peek().Type != TOK_RPAREN {
			return fmt.Errorf("expected ')' to close group")
		}
		c.consume()
		return nil
	}

	// -- Prefix symbol: .bug  #42  @alice ---------------------------------
	if tok.Type == TOK_SYMBOL {
		rule, ok := c.schema.prefix[tok.Symbol]
		if !ok {
			return fmt.Errorf("unknown prefix symbol %q", string(tok.Symbol))
		}
		c.consume() // consume the symbol

		// The bare word (or string) following it is the value.
		val := c.consume()
		if val.Type == TOK_EOF {
			return fmt.Errorf("expected value after prefix symbol %q", string(tok.Symbol))
		}

		fieldDef, ok := c.schema.field(rule.Field)
		if !ok {
			return fmt.Errorf("prefix symbol %q references unknown field %q", string(tok.Symbol), rule.Field)
		}

		c.emit(Instruction{Op: OP_LOAD, StrVal: rule.Field, FieldType: fieldDef.typ})
		c.emitLiteralFrom(val, String) // treat prefix value as string
		c.emit(Instruction{Op: rule.Op})
		return nil
	}

	// -- Identifier: field, bare bool, or field with suffix ---------------
	if tok.Type == TOK_IDENT {
		fieldName := strings.ToLower(tok.Literal)

		// Peek at the next token to determine which form this is.
		next := c.peekNext()

		// Form: IDENT TOK_SYMBOL  ->  suffix expansion  (ticket?)
		if next.Type == TOK_SYMBOL {
			if _, isSuffix := c.schema.suffix[next.Symbol]; isSuffix {
				c.consume() // consume ident
				c.consume() // consume suffix symbol
				return c.parseSuffix(fieldName, next.Symbol)
			}
		}

		// Form: IDENT op value  ->  field comparison
		if isOperatorToken(next) || isKeywordOp(next) {
			fieldDef, ok := c.schema.field(fieldName)
			if !ok {
				return fmt.Errorf("unknown field %q - registered fields: %s",
					fieldName, c.fieldList())
			}
			c.consume() // consume ident

			op, err := c.parseOp()
			if err != nil {
				return err
			}

			val := c.consume()
			if val.Type == TOK_EOF {
				return fmt.Errorf("expected value after operator for field %q", fieldName)
			}

			c.emit(Instruction{Op: OP_LOAD, StrVal: fieldName, FieldType: fieldDef.typ})
			if err := c.emitLiteralFrom(val, fieldDef.typ); err != nil {
				return err
			}
			c.emit(Instruction{Op: op})
			return nil
		}

		// Form: bare IDENT  ->  must be a Bool field
		fieldDef, ok := c.schema.field(fieldName)
		if !ok {
			return fmt.Errorf("unknown field %q - registered fields: %s",
				fieldName, c.fieldList())
		}
		if fieldDef.typ != Bool {
			return fmt.Errorf("field %q is not a Bool field; cannot be used bare", fieldName)
		}
		c.consume()
		c.emit(Instruction{Op: OP_LOAD, StrVal: fieldName, FieldType: Bool})
		return nil
	}

	return fmt.Errorf("unexpected token %q (type %v)", tok.Literal, tok.Type)
}

// parseSuffix emits the expansion for  fieldName + suffixSym.
func (c *compiler) parseSuffix(fieldName string, sym rune) error {
	fieldDef, ok := c.schema.field(fieldName)
	if !ok {
		return fmt.Errorf("suffix symbol %q applied to unknown field %q",
			string(sym), fieldName)
	}
	rule := c.schema.suffix[sym]
	exp, ok := rule.ByType[fieldDef.typ]
	if !ok {
		return fmt.Errorf("suffix symbol %q has no expansion for field type of %q",
			string(sym), fieldName)
	}

	c.emit(Instruction{Op: OP_LOAD, StrVal: fieldName, FieldType: fieldDef.typ})

	switch fieldDef.typ {
	case Number:
		c.emit(Instruction{Op: OP_PUSH_FLOAT, FloatVal: exp.FloatVal})
	case Bool:
		c.emit(Instruction{Op: OP_PUSH_BOOL, BoolVal: exp.BoolVal})
	default: // String, Date
		c.emit(Instruction{Op: OP_PUSH_STRING, StrVal: exp.StrVal})
	}
	c.emit(Instruction{Op: exp.Op})
	return nil
}

// parseOp consumes and returns the comparison opcode.
func (c *compiler) parseOp() (Op, error) {
	tok := c.consume()
	switch tok.Type {
	case TOK_EQ:
		return OP_EQ, nil
	case TOK_NEQ:
		return OP_NEQ, nil
	case TOK_GT:
		return OP_GT, nil
	case TOK_GTE:
		return OP_GTE, nil
	case TOK_LT:
		return OP_LT, nil
	case TOK_LTE:
		return OP_LTE, nil
	case TOK_TILDE:
		return OP_REGEX, nil
	case TOK_IDENT:
		if strings.EqualFold(tok.Literal, "contains") {
			return OP_CONTAINS, nil
		}
	}
	return 0, fmt.Errorf("expected operator (=, !=, >, >=, <, <=, ~, contains), got %q", tok.Literal)
}

// emitLiteralFrom emits a push instruction for the given token value.
// fieldType is used as a hint when the token is ambiguous (e.g. a number
// stored in a Date field should still be emitted as a string).
func (c *compiler) emitLiteralFrom(tok Token, fieldType FieldType) error {
	switch tok.Type {
	case TOK_STRING:
		c.emit(Instruction{Op: OP_PUSH_STRING, StrVal: tok.Literal})
	case TOK_IDENT:
		// Bare word used as a value after a prefix symbol
		c.emit(Instruction{Op: OP_PUSH_STRING, StrVal: tok.Literal})
	case TOK_NUMBER:
		if fieldType == String || fieldType == Date {
			// Numbers in string context (e.g. date fields) are pushed as strings
			c.emit(Instruction{Op: OP_PUSH_STRING, StrVal: tok.Literal})
			return nil
		}
		f, err := strconv.ParseFloat(tok.Literal, 64)
		if err != nil {
			return fmt.Errorf("invalid number %q: %w", tok.Literal, err)
		}
		c.emit(Instruction{Op: OP_PUSH_FLOAT, FloatVal: f})
	default:
		return fmt.Errorf("expected a value (string or number), got %q", tok.Literal)
	}
	return nil
}

// -- helpers ------------------------------------------------------------------

func isOperatorToken(t Token) bool {
	switch t.Type {
	case TOK_EQ, TOK_NEQ, TOK_GT, TOK_GTE, TOK_LT, TOK_LTE, TOK_TILDE:
		return true
	}
	return false
}

func isKeywordOp(t Token) bool {
	return t.Type == TOK_IDENT && strings.EqualFold(t.Literal, "contains")
}

func (c *compiler) fieldList() string {
	names := make([]string, 0, len(c.schema.fields))
	for n := range c.schema.fields {
		names = append(names, n)
	}
	return strings.Join(names, ", ")
}
