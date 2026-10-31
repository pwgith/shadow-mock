package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Postgres is the database of managed mode (SAAS.md §6). The connection string holds a password, so the file
// names the environment variable that holds it.
type Postgres struct {
	// DSNEnv names the environment variable that holds the connection string.
	DSNEnv string `yaml:"dsnEnv"`
	// Migrate applies the schema's migrations when the engine starts. It is on unless an operator runs them.
	Migrate bool `yaml:"migrate"`
	// PollInterval is how often the engine re-reads the org directory, to catch a change notice it missed.
	PollInterval Duration `yaml:"pollInterval"`
	// DSN is the value of DSNEnv, read by ResolveSecrets.
	DSN string `yaml:"-"`
}

// Redis holds the engine's shared runtime state: call counters and used token ids.
type Redis struct {
	Addr     string `yaml:"addr"`
	Username string `yaml:"username"`
	// PasswordEnv names the environment variable that holds the password.
	PasswordEnv string `yaml:"passwordEnv"`
	TLS         bool   `yaml:"tls"`
	DB          int    `yaml:"db"`
	// Prefix starts every key, so that environments can share a server.
	Prefix string `yaml:"prefix"`
	// Password is the value of PasswordEnv, read by ResolveSecrets.
	Password string `yaml:"-"`
}

// S3 is how to reach the bucket of the s3 blob driver: AWS S3, Cloudflare R2, MinIO or any store that speaks the
// protocol.
type S3 struct {
	Endpoint string `yaml:"endpoint"`
	Region   string `yaml:"region"`
	Bucket   string `yaml:"bucket"`
	// AccessKeyEnv, SecretKeyEnv and SessionTokenEnv name the environment variables that hold the credentials.
	AccessKeyEnv    string `yaml:"accessKeyEnv"`
	SecretKeyEnv    string `yaml:"secretKeyEnv"`
	SessionTokenEnv string `yaml:"sessionTokenEnv"`
	// PathStyle puts the bucket in the path instead of the host name.
	PathStyle bool     `yaml:"pathStyle"`
	Timeout   Duration `yaml:"timeout"`

	AccessKey    string `yaml:"-"`
	SecretKey    string `yaml:"-"`
	SessionToken string `yaml:"-"`
}

// driverPostgres is the name of the database driver of managed mode.
const driverPostgres = "postgres"

// defaultPollInterval is how often the org directory is re-read when the setting is zero.
const defaultPollInterval = 30 * time.Second

// resolveSharedSecrets reads the credentials the shared stores need from the environment.
func resolveSharedSecrets(c *Config, lookup func(string) (string, bool)) error {
	read := func(where, name string, required bool) (string, error) {
		if name == "" {
			return "", nil
		}
		v, ok := lookup(name)
		if (!ok || v == "") && required {
			return "", fmt.Errorf("%s: the environment variable %s is not set", where, name)
		}
		return v, nil
	}
	var err error
	if c.Storage.Global.Driver == driverPostgres {
		if c.Storage.Postgres.DSN, err = read("storage.postgres.dsnEnv", c.Storage.Postgres.DSNEnv, true); err != nil {
			return err
		}
	}
	if c.Storage.Redis.Addr != "" {
		if c.Storage.Redis.Password, err = read("storage.redis.passwordEnv", c.Storage.Redis.PasswordEnv, true); err != nil {
			return err
		}
	}
	if c.Storage.Blobs.Driver == "s3" {
		s3 := &c.Storage.Blobs.S3
		if s3.AccessKey, err = read("storage.blobs.s3.accessKeyEnv", s3.AccessKeyEnv, true); err != nil {
			return err
		}
		if s3.SecretKey, err = read("storage.blobs.s3.secretKeyEnv", s3.SecretKeyEnv, true); err != nil {
			return err
		}
		if s3.SessionToken, err = read("storage.blobs.s3.sessionTokenEnv", s3.SessionTokenEnv, false); err != nil {
			return err
		}
	}
	return nil
}

// validateShared checks the settings of the postgres, redis and s3 drivers that are in use.
func (c *Config) validateShared() error {
	var errs []error
	if c.Storage.Global.Driver == driverPostgres && c.Storage.Postgres.DSNEnv == "" {
		errs = append(errs, errors.New("storage.global.driver: postgres needs storage.postgres.dsnEnv, the name of the environment variable that holds the connection string"))
	}
	if c.Storage.Counters.Driver == "redis" && c.Storage.Redis.Addr == "" {
		errs = append(errs, errors.New("storage.counters.driver: redis needs storage.redis.addr"))
	}
	if c.Storage.Blobs.Driver == "s3" {
		errs = append(errs, c.Storage.Blobs.S3.validate())
	}
	if c.Storage.Postgres.PollInterval < 0 {
		errs = append(errs, errors.New("storage.postgres.pollInterval must not be negative"))
	}
	if c.Storage.Global.Driver == driverPostgres && c.Mode != "managed" {
		errs = append(errs, errors.New("storage.global.driver: postgres is for mode: managed"))
	}
	return errors.Join(errs...)
}

func (s S3) validate() error {
	switch {
	case s.Bucket == "":
		return errors.New("storage.blobs.s3.bucket is needed")
	case s.AccessKeyEnv == "" || s.SecretKeyEnv == "":
		return errors.New("storage.blobs.s3 needs accessKeyEnv and secretKeyEnv, the names of the environment variables that hold the credentials")
	case time.Duration(s.Timeout) < 0:
		return errors.New("storage.blobs.s3.timeout must not be negative")
	}
	if s.Endpoint != "" {
		u, err := url.Parse(s.Endpoint)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return fmt.Errorf("storage.blobs.s3.endpoint %q must be an http or https URL", s.Endpoint)
		}
	}
	return nil
}

// requireSecureLinks is the part of managed mode that insists on protected connections to the shared stores: Redis
// with a password and TLS, and a database connection that is not in the clear (FR-11.1). A single instance for
// development or trials is let off, because it runs on one machine against throwaway stores.
func (c *Config) requireSecureLinks() error {
	var errs []error
	if c.Storage.Redis.Addr != "" && (!c.Storage.Redis.TLS || c.Storage.Redis.PasswordEnv == "") {
		errs = append(errs, errors.New("mode: managed needs storage.redis.tls: true and storage.redis.passwordEnv: Redis must require authentication and TLS"))
	}
	if dsn := c.Storage.Postgres.DSN; dsn != "" && strings.Contains(dsn, "sslmode=disable") {
		errs = append(errs, errors.New("mode: managed refuses a database connection with sslmode=disable"))
	}
	return errors.Join(errs...)
}
