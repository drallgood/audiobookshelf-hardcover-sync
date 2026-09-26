package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEditionConfigUsesEnvironmentWithoutDefaultFile(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("HARDCOVER_TOKEN", "env-token")
	t.Setenv("AUDIOBOOKSHELF_URL", "http://abs.lan:13378")
	t.Setenv("AUDIOBOOKSHELF_NETWORK_TRUST", "public_only")

	if _, err := loadEditionConfig(defaultConfigPath, false); err == nil {
		t.Fatal("public_only from the environment should reject an HTTP Audiobookshelf URL")
	}

	t.Setenv("AUDIOBOOKSHELF_NETWORK_TRUST", "")
	cfg, err := loadEditionConfig(defaultConfigPath, false)
	if err != nil {
		t.Fatalf("missing default config.yaml should leave environment-only configuration: %v", err)
	}
	if cfg.Hardcover.Token != "env-token" || cfg.Audiobookshelf.NetworkTrust != "allow_private" {
		t.Fatalf("unexpected configuration: token %q, trust %q", cfg.Hardcover.Token, cfg.Audiobookshelf.NetworkTrust)
	}

	if _, err := loadEditionConfig(defaultConfigPath, true); err == nil {
		t.Fatal("a file named with --config must exist")
	}
}

func TestLoadEditionConfigEnvironmentOverridesFile(t *testing.T) {
	t.Setenv("HARDCOVER_TOKEN", "")
	t.Setenv("AUDIOBOOKSHELF_URL", "")
	t.Setenv("AUDIOBOOKSHELF_NETWORK_TRUST", "public_only")
	path := filepath.Join(t.TempDir(), "edition.yaml")
	if err := os.WriteFile(path, []byte("audiobookshelf:\n  url: https://abs.example/\nhardcover:\n  token: file-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadEditionConfig(path, true)
	if err != nil {
		t.Fatalf("loadEditionConfig() error = %v", err)
	}
	if cfg.Audiobookshelf.NetworkTrust != "public_only" || cfg.Audiobookshelf.URL != "https://abs.example" || cfg.Hardcover.Token != "file-token" {
		t.Fatalf("unexpected configuration: %+v", cfg.Audiobookshelf)
	}
}
