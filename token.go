package qlvm

// TokenType is the lexical category of a token.
type TokenType int

const (
	TOK_IDENT   TokenType = iota // bare word: field name or keyword (AND/OR/NOT)
	TOK_STRING                   // single-quoted literal: 'hello'
	TOK_NUMBER                   // numeric literal: 42 or 3.14
	TOK_BOOL                     // true / false (parsed from TOK_IDENT)
	TOK_SYMBOL                   // registered prefix or suffix rune: . # @ ?
	TOK_EQ                       // =
	TOK_NEQ                      // !=
	TOK_GT                       // >
	TOK_GTE                      // >=
	TOK_LT                       // <
	TOK_LTE                      // <=
	TOK_TILDE                    // ~ (regex match)
	TOK_LPAREN                   // (
	TOK_RPAREN                   // )
	TOK_EOF
)

// Token is a single lexical unit produced by the [Lexer].
type Token struct {
	Type    TokenType
	Literal string // text of the token (IDENT, STRING, NUMBER)
	Symbol  rune   // rune value for TOK_SYMBOL tokens
}
