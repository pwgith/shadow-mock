package config_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pwgith/shadow-mock/internal/auth/authtest"
	"github.com/pwgith/shadow-mock/internal/clock"
)

// managedYAML is a managed-mode configuration on shared stores, with extra lines for the test to add.
func managedYAML(t *testing.T, extra string) string {
	t.Helper()
	issuer := authtest.NewIssuer("management-app", "https://shadowmock.example", "mocks", clock.Real{})
	key := strings.ReplaceAll(strings.TrimSpace(issuer.PublicPEM()), "\n", "\n        ")
	return fmt.Sprintf(`
mode: managed
admin: { globalWrites: deny }
mappings: { unknownFields: keep }
egress: { blockPrivate: true }
auth:
  issuers:
    - name: management-app
      issuer: https://shadowmock.example
      audience: mocks
      publicKey: |
        %s
storage:
  global: { driver: postgres, watch: false }
  counters: { driver: redis }
  blobs:
    driver: s3
    s3: { endpoint: "https://acct.r2.cloudflarestorage.com", region: auto, bucket: mocks, accessKeyEnv: S3_KEY, secretKeyEnv: S3_SECRET }
  postgres: { dsnEnv: PG_DSN }
  redis: { addr: "redis.internal:6380", passwordEnv: REDIS_PW, tls: true }
%s`, key, extra)
}

var sharedEnv = map[string]string{
	"S3_KEY": "AKIA", "S3_SECRET": "secret", "PG_DSN": "postgres://u:p@db.internal/sm?sslmode=require", "REDIS_PW": "pw",
}

func TestManagedModeOnSharedStoresIsValidAndReadsItsSecrets(t *testing.T) {
	t.Parallel()
	cfg, err := load(t, managedYAML(t, ""), sharedEnv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Storage.Postgres.DSN != sharedEnv["PG_DSN"] || cfg.Storage.Redis.Password != "pw" ||
		cfg.Storage.Blobs.S3.AccessKey != "AKIA" || cfg.Storage.Blobs.S3.SecretKey != "secret" {
		t.Errorf("secrets were not read from the environment: %+v", cfg.Storage)
	}
	if !cfg.Storage.Postgres.Migrate || cfg.Storage.Redis.Prefix != "sm:" {
		t.Errorf("defaults lost: %+v %+v", cfg.Storage.Postgres, cfg.Storage.Redis)
	}
}

func TestSharedStoresNeedTheirEnvironment(t *testing.T) {
	t.Parallel()
	for _, missing := range []string{"S3_KEY", "S3_SECRET", "PG_DSN", "REDIS_PW"} {
		t.Run(missing, func(t *testing.T) {
			t.Parallel()
			env := map[string]string{}
			for k, v := range sharedEnv {
				if k != missing {
					env[k] = v
				}
			}
			_, err := load(t, managedYAML(t, ""), env)
			if err == nil || !strings.Contains(err.Error(), missing) {
				t.Errorf("error = %v, want it to name %s", err, missing)
			}
		})
	}
}

func TestManagedModeRefusesLinksThatAreNotProtected(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		edit func(string) string
		env  map[string]string
		want string
	}{
		{"redis without tls", func(y string) string { return strings.Replace(y, "tls: true", "tls: false", 1) }, nil, "storage.redis.tls"},
		{"redis without a password", func(y string) string { return strings.Replace(y, "passwordEnv: REDIS_PW, ", "", 1) }, nil, "storage.redis.passwordEnv"},
		{"a database in the clear", func(y string) string { return y }, map[string]string{"PG_DSN": "postgres://u:p@db/sm?sslmode=disable"}, "sslmode=disable"},
		{"counters left in memory", func(y string) string {
			return strings.Replace(y, "counters: { driver: redis }", "counters: { driver: memory }", 1)
		}, nil, "shared stores"},
		{"blobs left on disk", func(y string) string { return strings.Replace(y, "driver: s3", "driver: fs", 1) }, nil, "storage.blobs.driver: s3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := map[string]string{}
			for k, v := range sharedEnv {
				env[k] = v
			}
			for k, v := range tt.env {
				env[k] = v
			}
			_, err := load(t, tt.edit(managedYAML(t, "")), env)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want one mentioning %q", err, tt.want)
			}
		})
	}
}

func TestASingleInstanceMayUseSomeSharedStoresAndNotOthers(t *testing.T) {
	t.Parallel()
	y := managedYAML(t, "managed: { singleInstance: true }")
	y = strings.Replace(y, "counters: { driver: redis }", "counters: { driver: memory }", 1)
	y = strings.Replace(y, "redis: { addr: \"redis.internal:6380\", passwordEnv: REDIS_PW, tls: true }", "", 1)
	if _, err := load(t, y, sharedEnv); err != nil {
		t.Errorf("one instance on postgres and s3 with counters in memory: %v", err)
	}
}

func TestPostgresIsOnlyForManagedMode(t *testing.T) {
	t.Parallel()
	_, err := load(t, "storage: { global: { driver: postgres }, postgres: { dsnEnv: PG_DSN } }", sharedEnv)
	if err == nil || !strings.Contains(err.Error(), "managed") {
		t.Errorf("error = %v", err)
	}
}
