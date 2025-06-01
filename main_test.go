package main

import (
	"testing"
)

func TestFetchAudiobookShelfStats_NoEnv(t *testing.T) {
	// This test expects no env vars set, so the function should fail
	_, err := fetchAudiobookShelfStats()
	if err == nil {
		t.Error("expected error when env vars are missing, got nil")
	}
}

func TestSyncToHardcover_NotFinished(t *testing.T) {
	book := Audiobook{Title: "Test", Author: "Author", Progress: 0.5}
	err := syncToHardcover(book)
	if err != nil {
		t.Errorf("expected nil error for unfinished book, got %v", err)
	}
}

func TestSyncToHardcover_Finished_NoToken(t *testing.T) {
	book := Audiobook{Title: "Test", Author: "Author", Progress: 1.0}
	// Save and clear HARDCOVER_TOKEN
	token := hardcoverToken
	hardcoverToken = ""
	defer func() { hardcoverToken = token }()
	err := syncToHardcover(book)
	if err == nil {
		t.Error("expected error when HARDCOVER_TOKEN is missing, got nil")
	}
}

func TestRunSync_NoPanic(t *testing.T) {
	// Should not panic or crash even if env vars are missing
	runSync()
}
