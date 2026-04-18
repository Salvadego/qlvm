package qlvm

import (
	"strings"
	"unicode"
)

// Lexer converts a query string into a flat token stream.
// It is schema-aware: it recognises registered prefix and suffix
// symbol runes so the compiler can expand them correctly.
type Lexer struct {
	input      []rune
	pos        int
	stashed    *Token // at most one buffered token (suffix detection)
	prefixSyms map[rune]bool
	suffixSyms map[rune]bool
}

func newLexer(input string, schema *Schema) *Lexer {
	return &Lexer{
		input:      []rune(input),
		prefixSyms: schema.prefixRunes(),
		suffixSyms: schema.suffixRunes(),
	}
}

func (l *Lexer) peekRune() (rune, bool) {
	if l.pos >= len(l.input) {
		return 0, false
	}
	return l.input[l.pos], true
}

func (l *Lexer) peekRuneAt(offset int) (rune, bool) {
	i := l.pos + offset
	if i >= len(l.input) {
		return 0, false
	}
	return l.input[i], true
}

func (l *Lexer) advance() (rune, bool) {
	if l.pos >= len(l.input) {
		return 0, false
	}
	ch := l.input[l.pos]
	l.pos++
	return ch, true
}

func (l *Lexer) skipWhitespace() {
	for {
		ch, ok := l.peekRune()
		if !ok || !unicode.IsSpace(ch) {
			break
		}
		l.advance()
	}
}

func (l *Lexer) readString() string {
	l.advance() // consume opening '
	var sb strings.Builder
	for {
		ch, ok := l.advance()
		if !ok || ch == '\'' {
			break
		}
		if ch == '\\' {
			if next, ok := l.peekRune(); ok && next == '\'' {
				l.advance()
				sb.WriteRune('\'')
				continue
			}
		}
		sb.WriteRune(ch)
	}
	return sb.String()
}

func (l *Lexer) readNumber() string {
	var sb strings.Builder
	dotSeen := false
	for {
		ch, ok := l.peekRune()
		if !ok {
			break
		}
		if unicode.IsDigit(ch) {
			l.advance()
			sb.WriteRune(ch)
		} else if ch == '.' && !dotSeen {
			if next, ok := l.peekRuneAt(1); ok && unicode.IsDigit(next) {
				dotSeen = true
				l.advance()
				sb.WriteRune(ch)
			} else {
				break
			}
		} else {
			break
		}
	}
	return sb.String()
}

func (l *Lexer) readIdent() string {
	var sb strings.Builder
	first := true
	for {
		ch, ok := l.peekRune()
		if !ok || unicode.IsSpace(ch) {
			break
		}
		if ch == '=' || ch == '!' || ch == '<' || ch == '>' ||
			ch == '~' || ch == '(' || ch == ')' || ch == '\'' {
			break
		}
		if first && (l.prefixSyms[ch] || l.suffixSyms[ch]) {
			break
		}
		// fix cases like v0.2.0
		if !unicode.IsLetter(ch) &&
			!unicode.IsDigit(ch) &&
			ch != '_' && ch != '-' && ch != '.' {
			break
		}
		first = false
		l.advance()
		sb.WriteRune(ch)
	}
	return sb.String()
}

// nextRaw returns the next token without consulting the stash.
func (l *Lexer) nextRaw() Token {
	l.skipWhitespace()

	ch, ok := l.peekRune()
	if !ok {
		return Token{Type: TOK_EOF}
	}

	if ch == '\'' {
		return Token{Type: TOK_STRING, Literal: l.readString()}
	}
	if unicode.IsDigit(ch) {
		return Token{Type: TOK_NUMBER, Literal: l.readNumber()}
	}
	if l.prefixSyms[ch] {
		l.advance()
		return Token{Type: TOK_SYMBOL, Symbol: ch}
	}

	switch ch {
	case '(':
		l.advance()
		return Token{Type: TOK_LPAREN}
	case ')':
		l.advance()
		return Token{Type: TOK_RPAREN}
	case '~':
		l.advance()
		return Token{Type: TOK_TILDE}
	case '=':
		l.advance()
		return Token{Type: TOK_EQ}
	case '!':
		l.advance()
		if n, ok := l.peekRune(); ok && n == '=' {
			l.advance()
			return Token{Type: TOK_NEQ} // != takes priority over ! prefix
		}
		if l.prefixSyms['!'] {
			return Token{Type: TOK_SYMBOL, Symbol: '!'}
		}
		return Token{Type: TOK_IDENT, Literal: "!"}
	case '>':
		l.advance()
		if n, ok := l.peekRune(); ok && n == '=' {
			l.advance()
			return Token{Type: TOK_GTE}
		}
		return Token{Type: TOK_GT}
	case '<':
		l.advance()
		if n, ok := l.peekRune(); ok && n == '=' {
			l.advance()
			return Token{Type: TOK_LTE}
		}
		return Token{Type: TOK_LT}
	}

	if unicode.IsLetter(ch) || ch == '_' {
		word := l.readIdent()
		// If a suffix symbol follows immediately, buffer it
		if next, ok := l.peekRune(); ok && l.suffixSyms[next] {
			// Peek one further - if it's alphanumeric, it's part of the value not a suffix
			if after, ok := l.peekRuneAt(1); !ok || !unicode.IsLetter(after) && !unicode.IsDigit(after) {
				sym, _ := l.advance()
				l.stashed = &Token{Type: TOK_SYMBOL, Symbol: sym}
			}
		}
		return Token{Type: TOK_IDENT, Literal: word}
	}

	l.advance()
	return Token{Type: TOK_IDENT, Literal: string(ch)}
}

// tokenizeAll fully tokenizes input into a slice ending with TOK_EOF.
func tokenizeAll(input string, schema *Schema) []Token {
	l := newLexer(input, schema)
	var tokens []Token
	for {
		tok := l.nextRaw()
		tokens = append(tokens, tok)
		if l.stashed != nil {
			tokens = append(tokens, *l.stashed)
			l.stashed = nil
		}
		if tok.Type == TOK_EOF {
			break
		}
	}
	return tokens
}
