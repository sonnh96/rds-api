package sqlparam

import (
	"reflect"
	"testing"
)

func TestParseQuestionStyle(t *testing.T) {
	tests := []struct {
		name      string
		sql       string
		wantSQL   string
		wantNames []string
	}{
		{
			name:      "no parameters",
			sql:       "SELECT 1",
			wantSQL:   "SELECT 1",
			wantNames: nil,
		},
		{
			name:      "single parameter",
			sql:       "SELECT * FROM users WHERE id = :id",
			wantSQL:   "SELECT * FROM users WHERE id = ?",
			wantNames: []string{"id"},
		},
		{
			name:      "several parameters",
			sql:       "INSERT INTO users (name, age) VALUES (:name, :age)",
			wantSQL:   "INSERT INTO users (name, age) VALUES (?, ?)",
			wantNames: []string{"name", "age"},
		},
		{
			name:      "repeated parameter gets one placeholder each",
			sql:       "SELECT * FROM users WHERE a = :id OR b = :id",
			wantSQL:   "SELECT * FROM users WHERE a = ? OR b = ?",
			wantNames: []string{"id", "id"},
		},
		{
			name:      "colon inside a string literal is not a parameter",
			sql:       "SELECT ':id' AS value, :real",
			wantSQL:   "SELECT ':id' AS value, ?",
			wantNames: []string{"real"},
		},
		{
			name:      "escaped quote inside a string literal",
			sql:       "SELECT 'it''s :not a param', :yes",
			wantSQL:   "SELECT 'it''s :not a param', ?",
			wantNames: []string{"yes"},
		},
		{
			name:      "backslash escape inside a string literal",
			sql:       `SELECT 'a\':id', :yes`,
			wantSQL:   `SELECT 'a\':id', ?`,
			wantNames: []string{"yes"},
		},
		{
			name:      "quoted identifier is not a parameter",
			sql:       `SELECT "col:name" FROM t WHERE id = :id`,
			wantSQL:   `SELECT "col:name" FROM t WHERE id = ?`,
			wantNames: []string{"id"},
		},
		{
			name:      "backtick identifier is not a parameter",
			sql:       "SELECT `col:name` FROM t WHERE id = :id",
			wantSQL:   "SELECT `col:name` FROM t WHERE id = ?",
			wantNames: []string{"id"},
		},
		{
			name:      "line comment is not a parameter",
			sql:       "SELECT 1 -- :nope\nWHERE id = :id",
			wantSQL:   "SELECT 1 -- :nope\nWHERE id = ?",
			wantNames: []string{"id"},
		},
		{
			name:      "block comment is not a parameter",
			sql:       "SELECT /* :nope */ :id",
			wantSQL:   "SELECT /* :nope */ ?",
			wantNames: []string{"id"},
		},
		{
			name:      "underscores and digits in a parameter name",
			sql:       "SELECT :a_1, :b2",
			wantSQL:   "SELECT ?, ?",
			wantNames: []string{"a_1", "b2"},
		},
		{
			name:      "bare colon is left alone",
			sql:       "SELECT a : b",
			wantSQL:   "SELECT a : b",
			wantNames: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Parse(tc.sql, Question)
			if got.SQL != tc.wantSQL {
				t.Errorf("SQL = %q, want %q", got.SQL, tc.wantSQL)
			}
			if !reflect.DeepEqual(got.Names, tc.wantNames) {
				t.Errorf("Names = %v, want %v", got.Names, tc.wantNames)
			}
		})
	}
}

func TestParseDollarStyle(t *testing.T) {
	tests := []struct {
		name      string
		sql       string
		wantSQL   string
		wantNames []string
	}{
		{
			name:      "numbered placeholders",
			sql:       "INSERT INTO users (name, age) VALUES (:name, :age)",
			wantSQL:   "INSERT INTO users (name, age) VALUES ($1, $2)",
			wantNames: []string{"name", "age"},
		},
		{
			name:      "a repeated parameter reuses its placeholder",
			sql:       "SELECT * FROM users WHERE a = :id OR b = :id",
			wantSQL:   "SELECT * FROM users WHERE a = $1 OR b = $1",
			wantNames: []string{"id"},
		},
		{
			name:      "cast operator is not a parameter",
			sql:       "SELECT :value::text",
			wantSQL:   "SELECT $1::text",
			wantNames: []string{"value"},
		},
		{
			name:      "cast of a literal",
			sql:       "SELECT 'x'::text, :id",
			wantSQL:   "SELECT 'x'::text, $1",
			wantNames: []string{"id"},
		},
		{
			name:      "array cast from the README workaround",
			sql:       "insert into cfg.attributes(id, type, attributes) values(:id, :type, cast(:attributes as text[]))",
			wantSQL:   "insert into cfg.attributes(id, type, attributes) values($1, $2, cast($3 as text[]))",
			wantNames: []string{"id", "type", "attributes"},
		},
		{
			name:      "dollar quoted body is left alone",
			sql:       "CREATE FUNCTION f() RETURNS int AS $$ SELECT :nope $$ LANGUAGE sql; SELECT :id",
			wantSQL:   "CREATE FUNCTION f() RETURNS int AS $$ SELECT :nope $$ LANGUAGE sql; SELECT $1",
			wantNames: []string{"id"},
		},
		{
			name:      "tagged dollar quoted body is left alone",
			sql:       "SELECT $tag$ :nope $tag$, :id",
			wantSQL:   "SELECT $tag$ :nope $tag$, $1",
			wantNames: []string{"id"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Parse(tc.sql, Dollar)
			if got.SQL != tc.wantSQL {
				t.Errorf("SQL = %q, want %q", got.SQL, tc.wantSQL)
			}
			if !reflect.DeepEqual(got.Names, tc.wantNames) {
				t.Errorf("Names = %v, want %v", got.Names, tc.wantNames)
			}
		})
	}
}

func TestStatementArgs(t *testing.T) {
	statement := Parse("SELECT :a, :b, :a", Question)

	args, err := statement.Args(map[string]any{"a": int64(1), "b": "two", "unused": true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := []any{int64(1), "two", int64(1)}; !reflect.DeepEqual(args, want) {
		t.Errorf("Args = %v, want %v", args, want)
	}
}

func TestStatementArgsReportsMissingParameter(t *testing.T) {
	statement := Parse("SELECT :missing", Question)

	_, err := statement.Args(map[string]any{})
	if err == nil {
		t.Fatal("expected an error for the missing parameter")
	}
	if want := "Cannot find parameter: missing"; err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

func TestStatementArgsAcceptsExplicitNull(t *testing.T) {
	statement := Parse("SELECT :a", Question)

	args, err := statement.Args(map[string]any{"a": nil})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(args) != 1 || args[0] != nil {
		t.Errorf("Args = %v, want [<nil>]", args)
	}
}

func TestReturnsRows(t *testing.T) {
	tests := []struct {
		sql  string
		want bool
	}{
		{"SELECT 1", true},
		{"  select 1", true},
		{"\n\tSELECT 1", true},
		{"(SELECT 1)", true},
		{"WITH t AS (SELECT 1) SELECT * FROM t", true},
		{"SHOW DATABASES", true},
		{"show tables", true},
		{"DESCRIBE users", true},
		{"EXPLAIN SELECT 1", true},
		{"VALUES (1)", true},
		{"CALL my_proc()", true},
		{"-- a comment\nSELECT 1", true},
		{"/* a comment */ SELECT 1", true},
		{"INSERT INTO users (name) VALUES ('a')", false},
		{"insert into users (name) values ('a')", false},
		{"UPDATE users SET name = 'a'", false},
		{"DELETE FROM users", false},
		{"CREATE TABLE users (id int)", false},
		{"DROP TABLE users", false},
		{"", false},
		{"INSERT INTO users (name) VALUES ('a') RETURNING id", true},
		{"insert into users (name) values ('a') returning id", true},
		{"UPDATE users SET name = 'a' RETURNING id", true},
		{"DELETE FROM users RETURNING id", true},
		{"INSERT INTO users (name) VALUES ('returning')", false},
		{"INSERT INTO users (name) VALUES ('a') -- returning", false},
	}

	for _, tc := range tests {
		t.Run(tc.sql, func(t *testing.T) {
			if got := ReturnsRows(tc.sql); got != tc.want {
				t.Errorf("ReturnsRows(%q) = %v, want %v", tc.sql, got, tc.want)
			}
		})
	}
}
