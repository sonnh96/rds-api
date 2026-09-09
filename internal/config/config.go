// Package config reads the environment into the resource and secret registries.
// It is a port of setup() in LocalDataApi.kt.
package config

import (
	"os"
	"strconv"

	"github.com/sonnh96/local-data-api/internal/resource"
	"github.com/sonnh96/local-data-api/internal/secrets"
)

// Defaults matching the ARNs used throughout the documentation and the bundled
// compose files, so the documented AWS CLI invocations work out of the box.
const (
	DefaultResourceArn = "arn:aws:rds:us-east-1:123456789012:cluster:dummy"
	DefaultSecretArn   = "arn:aws:secretsmanager:us-east-1:123456789012:secret:dummy"
	DefaultPort        = "80"
)

// Settings is the resolved server configuration.
type Settings struct {
	Engine      resource.Engine
	ResourceArn string
	SecretArn   string
	Host        string
	Port        int
	UserName    string
	Password    string
	Options     map[string]string
	// Address is the TCP address the HTTP server listens on.
	Address string
}

// Load reads the environment.
//
// ENGINE selects the database: any value containing "mysql" means MySQL, and
// anything else means PostgreSQL. The older JDBC-era names (MySQLJDBC,
// PostgreSQLJDBC) therefore still resolve correctly.
func Load() Settings {
	engine := resource.EngineFromName(env("ENGINE", "MySQL"))

	settings := Settings{
		Engine:      engine,
		ResourceArn: env("RESOURCE_ARN", DefaultResourceArn),
		SecretArn:   env("SECRET_ARN", DefaultSecretArn),
		Options:     map[string]string{},
		Address:     ":" + env("PORT", DefaultPort),
	}

	if engine == resource.MySQL {
		settings.Host = env("MYSQL_HOST", "127.0.0.1")
		settings.Port = envInt("MYSQL_PORT", 3306)
		settings.UserName = env("MYSQL_USER", "root")
		settings.Password = env("MYSQL_PASSWORD", "example")
	} else {
		settings.Host = env("POSTGRES_HOST", "127.0.0.1")
		settings.Port = envInt("POSTGRES_PORT", 5432)
		settings.UserName = env("POSTGRES_USER", "postgres")
		settings.Password = env("POSTGRES_PASSWORD", "example")
		settings.Options["sslmode"] = env("POSTGRES_SSLMODE", "disable")
	}

	return settings
}

// Apply registers the settings with the resource and secret managers.
func (s Settings) Apply(resources *resource.Manager, secretManager *secrets.Manager) {
	resources.SetResource(s.Engine, s.ResourceArn, s.Host, s.Port, s.Options)
	secretManager.SetSecret(s.UserName, s.Password, s.SecretArn)
}

func env(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok && value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value, ok := os.LookupEnv(key)
	if !ok || value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}
