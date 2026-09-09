package sqlparam

import "strings"

// rowReturningKeywords are the statements that produce a result set.
var rowReturningKeywords = map[string]bool{
	"SELECT":   true,
	"WITH":     true,
	"SHOW":     true,
	"DESCRIBE": true,
	"DESC":     true,
	"EXPLAIN":  true,
	"VALUES":   true,
	"TABLE":    true,
	"PRAGMA":   true,
	"CALL":     true,
	"ANALYZE":  true,
}

// ReturnsRows reports whether a statement should be run as a query rather than
// as an update.
//
// JDBC answers this at runtime through Statement.getResultSet, but Go's
// database/sql needs the choice made up front: Query gives us the rows, Exec
// gives us the affected-row count and the generated key. So the leading keyword
// decides, with a RETURNING clause pulling a DML statement over to the query
// side, which is how PostgreSQL returns generated columns.
func ReturnsRows(sql string) bool {
	keyword, rest := leadingKeyword(sql)
	if rowReturningKeywords[keyword] {
		return true
	}
	switch keyword {
	case "INSERT", "UPDATE", "DELETE", "MERGE":
		return hasReturningClause(rest)
	default:
		return false
	}
}

// leadingKeyword returns the first keyword of the statement, upper-cased, along
// with the rest of the statement. Comments and leading parentheses are skipped.
func leadingKeyword(sql string) (string, string) {
	s := scanner{input: sql}
	for !s.done() {
		c := s.input[s.pos]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '(' || c == ';' {
			s.pos++
			continue
		}
		if (c == '-' && s.peekAt(1) == '-') || (c == '/' && s.peekAt(1) == '*') {
			if _, ok := s.skipNonParameter(); ok {
				continue
			}
		}
		break
	}

	start := s.pos
	for !s.done() && isIdentifierByte(s.input[s.pos]) {
		s.pos++
	}
	return strings.ToUpper(s.input[start:s.pos]), s.input[s.pos:]
}

// hasReturningClause reports whether the statement has a top-level RETURNING
// clause, ignoring the ones inside string literals, identifiers and comments.
func hasReturningClause(sql string) bool {
	s := scanner{input: sql}
	for !s.done() {
		if _, ok := s.skipNonParameter(); ok {
			continue
		}
		if !isIdentifierByte(s.input[s.pos]) {
			s.pos++
			continue
		}

		start := s.pos
		for !s.done() && isIdentifierByte(s.input[s.pos]) {
			s.pos++
		}
		if strings.EqualFold(s.input[start:s.pos], "RETURNING") {
			return true
		}
	}
	return false
}
