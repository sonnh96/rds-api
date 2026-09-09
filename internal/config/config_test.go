package config

import (
	"testing"

	"github.com/sonnh96/local-data-api/internal/resource"
	"github.com/sonnh96/local-data-api/internal/secrets"
)

func TestLoadDefaultsToMySQL(t *testing.T) {
	settings := Load()

	if settings.Engine != resource.MySQL {
		t.Errorf("Engine = %q, want %q", settings.Engine, resource.MySQL)
	}
	if settings.Host != "127.0.0.1" || settings.Port != 3306 {
		t.Errorf("host:port = %s:%d, want 127.0.0.1:3306", settings.Host, settings.Port)
	}
	if settings.UserName != "root" || settings.Password != "example" {
		t.Errorf("credentials = %s/%s, want root/example", settings.UserName, settings.Password)
	}
	if settings.ResourceArn != DefaultResourceArn || settings.SecretArn != DefaultSecretArn {
		t.Errorf("arns = %s / %s", settings.ResourceArn, settings.SecretArn)
	}
	if settings.Address != ":80" {
		t.Errorf("Address = %q, want :80", settings.Address)
	}
}

// The JDBC-era engine names are still accepted, so an existing deployment's
// ENGINE value keeps selecting the same database.
func TestLoadEngineNames(t *testing.T) {
	tests := []struct {
		engine string
		want   resource.Engine
	}{
		{"MySQL", resource.MySQL},
		{"mysql", resource.MySQL},
		{"MySQLJDBC", resource.MySQL},
		{"PostgreSQL", resource.Postgres},
		{"PostgreSQLJDBC", resource.Postgres},
		{"PostgresSQL", resource.Postgres},
		{"anything else", resource.Postgres},
	}

	for _, tc := range tests {
		t.Run(tc.engine, func(t *testing.T) {
			t.Setenv("ENGINE", tc.engine)
			if got := Load().Engine; got != tc.want {
				t.Errorf("Engine = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLoadPostgresDefaults(t *testing.T) {
	t.Setenv("ENGINE", "PostgreSQL")

	settings := Load()
	if settings.Host != "127.0.0.1" || settings.Port != 5432 {
		t.Errorf("host:port = %s:%d, want 127.0.0.1:5432", settings.Host, settings.Port)
	}
	if settings.UserName != "postgres" || settings.Password != "example" {
		t.Errorf("credentials = %s/%s, want postgres/example", settings.UserName, settings.Password)
	}
	if settings.Options["sslmode"] != "disable" {
		t.Errorf("sslmode = %q, want disable", settings.Options["sslmode"])
	}
}

func TestLoadReadsTheEnvironment(t *testing.T) {
	t.Setenv("ENGINE", "MySQL")
	t.Setenv("MYSQL_HOST", "db")
	t.Setenv("MYSQL_PORT", "3307")
	t.Setenv("MYSQL_USER", "someone")
	t.Setenv("MYSQL_PASSWORD", "secret")
	t.Setenv("RESOURCE_ARN", "arn:resource")
	t.Setenv("SECRET_ARN", "arn:secret")
	t.Setenv("PORT", "8080")

	settings := Load()
	if settings.Host != "db" || settings.Port != 3307 {
		t.Errorf("host:port = %s:%d", settings.Host, settings.Port)
	}
	if settings.UserName != "someone" || settings.Password != "secret" {
		t.Errorf("credentials = %s/%s", settings.UserName, settings.Password)
	}
	if settings.ResourceArn != "arn:resource" || settings.SecretArn != "arn:secret" {
		t.Errorf("arns = %s / %s", settings.ResourceArn, settings.SecretArn)
	}
	if settings.Address != ":8080" {
		t.Errorf("Address = %q, want :8080", settings.Address)
	}
}

func TestLoadIgnoresAnUnparseablePort(t *testing.T) {
	t.Setenv("ENGINE", "MySQL")
	t.Setenv("MYSQL_PORT", "not a number")

	if got := Load().Port; got != 3306 {
		t.Errorf("Port = %d, want the 3306 default", got)
	}
}

func TestApplyRegistersTheResourceAndSecret(t *testing.T) {
	t.Setenv("ENGINE", "MySQL")

	resources := resource.NewManager()
	t.Cleanup(func() { resources.Close() })
	secretManager := secrets.NewManager()

	settings := Load()
	settings.Apply(resources, secretManager)

	config, ok := resources.Config(settings.ResourceArn)
	if !ok {
		t.Fatal("resource ARN was not registered")
	}
	if config.Engine != resource.MySQL || config.Host != "127.0.0.1" || config.Port != 3306 {
		t.Errorf("config = %+v", config)
	}

	secret, err := secretManager.GetSecret(settings.SecretArn)
	if err != nil {
		t.Fatalf("secret ARN was not registered: %v", err)
	}
	if secret.UserName != "root" || secret.Password != "example" {
		t.Errorf("secret = %+v", secret)
	}
}
