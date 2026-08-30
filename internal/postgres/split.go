package postgres

import "strings"

// splitStatements cuts a.sql file into top-level statements on unquoted
// semicolons.
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

// dollarTag reports the opening delimiter at src[i] if one starts there.
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
