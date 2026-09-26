package main

import (
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audiobookshelf"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/config"
)

func TestNewImageCreatorUsesConfiguredAudiobookshelfTrust(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Audiobookshelf.URL = "https://abs.example"
	cfg.Audiobookshelf.NetworkTrust = audiobookshelf.NetworkTrustPublicOnly
	if _, err := newImageCreator(nil, cfg); err != nil {
		t.Fatalf("public HTTPS Audiobookshelf configuration should be accepted: %v", err)
	}

	cfg.Audiobookshelf.NetworkTrust = "unsupported"
	if _, err := newImageCreator(nil, cfg); err == nil {
		t.Fatal("unsupported Audiobookshelf network trust should be rejected")
	}
}
