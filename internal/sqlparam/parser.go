// Package sqlparam rewrites the `:name` bind parameters of a Data API statement
// into the placeholders a Go SQL driver understands.
//
// The parser skips over the regions where a colon cannot start a parameter:
// string literals, quoted identifiers, comments, dollar-quoted bodies and
// PostgreSQL's `::` cast operator. That is what lets `:value::text` bind
// correctly instead of having to be written as `cast(:value as text)`.
package sqlparam

import (
	"strconv"
	"strings"

	"github.com/sonnh96/local-data-api/internal/apierr"
)

// Style is the placeholder syntax of the target driver.
type Style int

const (
	// Question is the `?` placeholder used by MySQL.
	Question Style = iota
	// Dollar is the `$1` placeholder used by PostgreSQL.
	Dollar
)

// Statement is a statement whose named parameters have been rewritten.
type Statement struct {
	// SQL is the rewritten statement, ready for the driver.
	SQL string
	// Names lists the parameter for each placeholder, in placeholder order.
	Names []string
}

// Parse rewrites the named parameters of sql into placeholders of the given
// style. With Dollar placeholders a name that appears more than once reuses a
// single placeholder; with Question placeholders it gets one per occurrence,
// because that syntax is positional.
func Parse(sql string, style Style) *Statement {
	var out strings.Builder
	out.Grow(len(sql))

	var names []string
	indexByName := make(map[string]int)

	s := scanner{input: sql}
	for !s.done() {
		if skipped, ok := s.skipNonParameter(); ok {
			out.WriteString(skipped)
			continue
		}

		name, ok := s.readParameter()
		if !ok {
			out.WriteByte(s.next())
			continue
		}

		switch style {
		case Dollar:
			index, seen := indexByName[name]
			if !seen {
				names = append(names, name)
				index = len(names)
				indexByName[name] = index
			}
			out.WriteByte('$')
			out.WriteString(strconv.Itoa(index))
		default:
			names = append(names, name)
			out.WriteByte('?')
		}
	}

	return &Statement{SQL: out.String(), Names: names}
}

// Args resolves the placeholders against the supplied parameters, returning the
// argument list in placeholder order. A placeholder with no matching parameter
// is an error, as it is on the real service.
func (s *Statement) Args(params map[string]any) ([]any, error) {
	args := make([]any, 0, len(s.Names))
	for _, name := range s.Names {
		value, ok := params[name]
		if !ok {
			return nil, apierr.BadRequest("Cannot find parameter: %s", name)
		}
		args = append(args, value)
	}
	return args, nil
}

// scanner walks a SQL statement, keeping track of the regions in which a `:`
// cannot introduce a bind parameter.
type scanner struct {
	input string
	pos   int
}

func (s *scanner) done() bool { return s.pos >= len(s.input) }

func (s *scanner) next() byte {
	c := s.input[s.pos]
	s.pos++
	return c
}

func (s *scanner) peekAt(offset int) byte {
	if s.pos+offset >= len(s.input) {
		return 0
	}
	return s.input[s.pos+offset]
}

// skipNonParameter consumes any region that must be copied through verbatim:
// quoted literals and identifiers, comments and the `::` cast operator. It
// returns the consumed text and whether anything was consumed.
func (s *scanner) skipNonParameter() (string, bool) {
	start := s.pos

	switch c := s.input[s.pos]; c {
	case '\'', '"', '`':
		s.skipQuoted(c)
		return s.input[start:s.pos], true
	case '$':
		if s.skipDollarQuoted() {
			return s.input[start:s.pos], true
		}
	case '-':
		if s.peekAt(1) == '-' {
			s.skipLineComment()
			return s.input[start:s.pos], true
		}
	case '/':
		if s.peekAt(1) == '*' {
			s.skipBlockComment()
			return s.input[start:s.pos], true
		}
	case ':':
		// `::` is PostgreSQL's cast operator, never a parameter.
		if s.peekAt(1) == ':' {
			s.pos += 2
			return s.input[start:s.pos], true
		}
	}

	return "", false
}

// skipQuoted consumes a quoted literal or identifier. A doubled quote is an
// escaped quote; in single-quoted strings a backslash escapes the next byte,
// which is how MySQL behaves by default.
func (s *scanner) skipQuoted(quote byte) {
	s.pos++ // opening quote
	for s.pos < len(s.input) {
		c := s.input[s.pos]
		switch {
		case c == '\\' && quote == '\'':
			s.pos += 2
		case c == quote && s.peekAt(1) == quote:
			s.pos += 2
		case c == quote:
			s.pos++
			return
		default:
			s.pos++
		}
	}
}

// skipDollarQuoted consumes a PostgreSQL $tag$ ... $tag$ literal. It reports
// false when the `$` does not open such a literal.
func (s *scanner) skipDollarQuoted() bool {
	end := s.pos + 1
	// The tag is an optional identifier that may not start with a digit, so a
	// literal `$1` is not mistaken for the start of a dollar-quoted string.
	if end < len(s.input) && !isDigit(s.input[end]) {
		for end < len(s.input) && isIdentifierByte(s.input[end]) {
			end++
		}
	}
	if end >= len(s.input) || s.input[end] != '$' {
		return false
	}

	tag := s.input[s.pos : end+1]
	rest := s.input[end+1:]
	closing := strings.Index(rest, tag)
	if closing < 0 {
		s.pos = len(s.input)
		return true
	}
	s.pos = end + 1 + closing + len(tag)
	return true
}

func (s *scanner) skipLineComment() {
	for s.pos < len(s.input) && s.input[s.pos] != '\n' {
		s.pos++
	}
	if s.pos < len(s.input) {
		s.pos++
	}
}

func (s *scanner) skipBlockComment() {
	s.pos += 2
	for s.pos < len(s.input) {
		if s.input[s.pos] == '*' && s.peekAt(1) == '/' {
			s.pos += 2
			return
		}
		s.pos++
	}
}

// readParameter reads a `:name` bind parameter at the current position.
func (s *scanner) readParameter() (string, bool) {
	if s.input[s.pos] != ':' {
		return "", false
	}
	start := s.pos + 1
	end := start
	for end < len(s.input) && isIdentifierByte(s.input[end]) {
		end++
	}
	if end == start {
		return "", false
	}
	s.pos = end
	return s.input[start:end], true
}

func isIdentifierByte(c byte) bool {
	return c == '_' || isDigit(c) ||
		('a' <= c && c <= 'z') ||
		('A' <= c && c <= 'Z')
}

func isDigit(c byte) bool { return '0' <= c && c <= '9' }
