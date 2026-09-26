package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunOneTimeSyncHonorsPublicOnlyNetworkTrust(t *testing.T) {
	const childEnv = "RUN_ONCE_SYNC_PUBLIC_ONLY_TEST_CHILD"
	if os.Getenv(childEnv) == "1" {
		RunOneTimeSync(&configFlags{})
		return
	}

	tempDir := t.TempDir()

	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestRunOneTimeSyncHonorsPublicOnlyNetworkTrust$")
	cmd.Env = []string{
		"HOME=" + tempDir,
		"TMPDIR=" + tempDir,
		childEnv + "=1",
		"AUDIOBOOKSHELF_URL=https://127.0.0.1:443",
		"AUDIOBOOKSHELF_TOKEN=test-abs-token",
		"AUDIOBOOKSHELF_NETWORK_TRUST=public_only",
		"HARDCOVER_TOKEN=test-hardcover-token",
		"HARDCOVER_BASE_URL=https://127.0.0.1:1/graphql",
		"DATA_DIR=" + filepath.Join(tempDir, "data"),
		"CACHE_DIR=" + filepath.Join(tempDir, "cache"),
		"MISMATCH_OUTPUT_DIR=" + filepath.Join(tempDir, "mismatches"),
		"SYNC_STATE_FILE=" + filepath.Join(tempDir, "sync_state.json"),
	}
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("one-time sync subprocess exceeded its timeout: %v\n%s", ctx.Err(), output)
	}
	if err == nil {
		t.Fatalf("one-time sync succeeded; want public_only rejection\n%s", output)
	}
	if !strings.Contains(string(output), "not allowed by public_only") {
		t.Fatalf("one-time sync error = %v, output = %s; want public_only destination rejection", err, output)
	}
}
