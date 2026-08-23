package postgres

import "strings"

// splitStatements cuts a .sql file into top-level statements on unquoted
// semicolons. It is a lexer, not a parser: it knows exactly enough of
// PostgreSQL's lexical structure to tell a separator from a semicolon inside
// something, and nothing at all about what any statement means.
//
// WHY IT EXISTS AT ALL. `-- migrate:no-transaction` is the escape hatch for
// statements PostgreSQL refuses inside a transaction block, of which
// `CREATE INDEX CONCURRENTLY` is the one that will actually be wanted. The
// simple query protocol wraps MULTIPLE statements sent in one message in an
// IMPLICIT transaction block, so a whole file handed to one Exec re-creates
// the very condition the directive exists to escape. Executed one at a time,
// nothing is wrapped.
//
// The five things it has to know, each of which is a real .sql construct and
// four of which a naive `strings.Split(src, ";")` gets wrong:
//
//   - '...' with ” as the escape, and \ as an escape ONLY in an E'...' string
//     (standard_conforming_strings has been on by default since 9.1, so '\'
//     is a complete string holding a backslash);
//   - "..." quoted identifiers;
//   - $$...$$ and $tag$...$tag$ bodies, which is where a function's own
//     semicolons live — and $1 is a parameter, not an opening tag;
//   - -- to end of line;
//   - /* */ which NESTS in PostgreSQL, unlike C's.
//
// Chunks holding only whitespace and comments are dropped, so a file's header
// comment does not become an empty statement. Comments attached ABOVE a
// statement stay with it, which keeps `each FK carries its comment` true of
// what is executed rather than only of what is checked in.
func splitStatements(src string) []string {
	var out []string
	start := 0
	hasCode := false

	emit := func(end int) {
		if hasCode {
			if s := strings.TrimSpace(src[start:end]); s != "" {
				out = append(out, s)
			}
		}
		start = end + 1
		hasCode = false
	}

	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == ';':
			emit(i)
			i++

		case c == '-' && i+1 < len(src) && src[i+1] == '-':
			for i < len(src) && src[i] != '\n' {
				i++
			}

		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			depth := 1
			i += 2
			for i < len(src) && depth > 0 {
				switch {
				case src[i] == '/' && i+1 < len(src) && src[i+1] == '*':
					depth++
					i += 2
				case src[i] == '*' && i+1 < len(src) && src[i+1] == '/':
					depth--
					i += 2
				default:
					i++
				}
			}

		case c == '\'':
			hasCode = true
			escapes := i > 0 && (src[i-1] == 'E' || src[i-1] == 'e') &&
				(i == 1 || !isIdentByte(src[i-2]))
			i++
			for i < len(src) {
				if escapes && src[i] == '\\' && i+1 < len(src) {
					i += 2
					continue
				}
				if src[i] == '\'' {
					if i+1 < len(src) && src[i+1] == '\'' {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}

		case c == '"':
			hasCode = true
			i++
			for i < len(src) {
				if src[i] == '"' {
					if i+1 < len(src) && src[i+1] == '"' {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}

		case c == '$':
			if tag, ok := dollarTag(src, i); ok {
				hasCode = true
				i += len(tag)
				if j := strings.Index(src[i:], tag); j >= 0 {
					i += j + len(tag)
				} else {
					i = len(src)
				}
				continue
			}
			hasCode = true
			i++

		default:
			if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
				hasCode = true
			}
			i++
		}
	}
	emit(len(src))
	return out
}

// dollarTag reports the opening delimiter at src[i] if one starts there —
// "$$" or "$name$" — and false for a positional parameter such as $1 or $1$,
// since a tag may not begin with a digit.
func dollarTag(src string, i int) (string, bool) {
	j := i + 1
	for j < len(src) && isIdentByte(src[j]) {
		j++
	}
	if j >= len(src) || src[j] != '$' {
		return "", false
	}
	if j > i+1 && src[i+1] >= '0' && src[i+1] <= '9' {
		return "", false
	}
	return src[i : j+1], true
}

func isIdentByte(b byte) bool {
	return b == '_' ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9')
}
