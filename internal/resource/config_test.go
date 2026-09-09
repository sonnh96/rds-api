package resource

import (
	"strings"
	"testing"
)

func TestMySQLDSN(t *testing.T) {
	config := Config{Engine: MySQL, ResourceArn: "arn", Host: "db", Port: 3306}

	dsn, err := config.DSN("root", "example", "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, want := range []string{"root:example@tcp(db:3306)/test", "clientFoundRows=true"} {
		if !strings.Contains(dsn, want) {
			t.Errorf("DSN %q does not contain %q", dsn, want)
		}
	}
	// Timestamps must arrive as text so they can be rendered the JDBC way.
	if strings.Contains(dsn, "parseTime=true") {
		t.Errorf("DSN %q enables parseTime", dsn)
	}
}

func TestMySQLDSNEscapesCredentials(t *testing.T) {
	config := Config{Engine: MySQL, Host: "db", Port: 3306}

	dsn, err := config.DSN("root", "p@ss:word/with(specials)", "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(dsn, "@tcp(db:3306)/test") {
		t.Errorf("DSN %q is not addressed correctly", dsn)
	}
}

func TestMySQLDSNWithoutDatabase(t *testing.T) {
	config := Config{Engine: MySQL, Host: "db", Port: 3306}

	dsn, err := config.DSN("root", "example", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(dsn, "@tcp(db:3306)/?") && !strings.HasSuffix(dsn, "@tcp(db:3306)/") {
		t.Errorf("DSN %q should have an empty database", dsn)
	}
}

func TestPostgresDSN(t *testing.T) {
	config := Config{Engine: Postgres, Host: "db", Port: 5432, Options: map[string]string{"sslmode": "disable"}}

	dsn, err := config.DSN("postgres", "example", "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, want := range []string{"host='db'", "port='5432'", "user='postgres'", "password='example'", "dbname='test'", "sslmode='disable'"} {
		if !strings.Contains(dsn, want) {
			t.Errorf("DSN %q does not contain %q", dsn, want)
		}
	}
}

func TestPostgresDSNWithoutDatabase(t *testing.T) {
	config := Config{Engine: Postgres, Host: "db", Port: 5432}

	dsn, err := config.DSN("postgres", "example", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(dsn, "dbname") {
		t.Errorf("DSN %q should not name a database", dsn)
	}
}

func TestPostgresDSNQuotesSpecialCharacters(t *testing.T) {
	config := Config{Engine: Postgres, Host: "db", Port: 5432}

	dsn, err := config.DSN("postgres", `pass' word\`, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(dsn, `password='pass\' word\\'`) {
		t.Errorf("DSN %q does not quote the password", dsn)
	}
}

func TestPostgresDSNOptionsOverrideDefaults(t *testing.T) {
	config := Config{Engine: Postgres, Host: "db", Port: 5432, Options: map[string]string{"sslmode": "require"}}

	dsn, err := config.DSN("postgres", "example", "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(dsn, "sslmode='require'") {
		t.Errorf("DSN %q does not honour the sslmode option", dsn)
	}
}

func TestEngineFromName(t *testing.T) {
	tests := []struct {
		name string
		want Engine
	}{
		{"MySQL", MySQL},
		{"mysql", MySQL},
		{"MySQLJDBC", MySQL},
		{"PostgreSQL", Postgres},
		{"PostgreSQLJDBC", Postgres},
		{"", Postgres},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := EngineFromName(tc.name); got != tc.want {
				t.Errorf("EngineFromName(%q) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestEngineDriverAndParameterStyle(t *testing.T) {
	if got := MySQL.DriverName(); got != "mysql" {
		t.Errorf("MySQL driver = %q", got)
	}
	if got := Postgres.DriverName(); got != "postgres" {
		t.Errorf("Postgres driver = %q", got)
	}
	if MySQL.ParameterStyle() == Postgres.ParameterStyle() {
		t.Error("MySQL and PostgreSQL should use different placeholder styles")
	}
}
