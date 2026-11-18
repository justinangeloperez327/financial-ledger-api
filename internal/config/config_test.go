package config

import "testing"

func TestLoad(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-only-secret-at-least-32-bytes-long")
	t.Setenv("DATABASE_URL", "postgres://example:example@localhost:5432/example")
	t.Setenv("APP_PORT", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if cfg.Port != "8080" {
		t.Fatalf("default port = %q, want 8080", cfg.Port)
	}
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for missing DATABASE_URL")
	}
}

func TestLoadRejectsInvalidPort(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/ledger")
	for _, port := range []string{"abc", "0", "65536"} {
		t.Run(port, func(t *testing.T) {
			t.Setenv("APP_PORT", port)
			if _, err := Load(); err == nil {
				t.Fatalf("expected error for APP_PORT=%s", port)
			}
		})
	}
}

func TestLoadRejectsMissingSecret(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/ledger")
	t.Setenv("APP_PORT", "8080")
	t.Setenv("JWT_SECRET", "short")
	if _, err := Load(); err == nil {
		t.Fatal("expected secret validation")
	}
}
