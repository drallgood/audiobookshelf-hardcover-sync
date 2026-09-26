package state

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStateFileLockProcessHelper(t *testing.T) {
	statePath := os.Getenv("STATE_LOCK_HELPER_STATE")
	if statePath == "" {
		return
	}

	lock, err := AcquireFileLock(statePath)
	if err != nil {
		t.Fatalf("acquire state lock: %v", err)
	}
	defer lock.Close()

	state, err := LoadState(statePath)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	state.UpdateBook("child-book", 0.5, "IN_PROGRESS")
	if err := state.Save(statePath); err != nil {
		t.Fatalf("save state: %v", err)
	}
	if err := os.WriteFile(os.Getenv("STATE_LOCK_HELPER_READY"), []byte("ready"), 0600); err != nil {
		t.Fatalf("write ready marker: %v", err)
	}
	for {
		time.Sleep(time.Minute)
	}
}

func TestAcquireFileLockRejectsEmptyPath(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{
			name: "empty string",
			path: "",
		},
		{
			name: "whitespace only",
			path: "   ",
		},
		{
			name: "tabs and spaces",
			path: "\t  \t",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Change to temp directory to ensure no .lock file is created there
			oldCwd, err := os.Getwd()
			require.NoError(t, err)
			tmpDir := t.TempDir()
			require.NoError(t, os.Chdir(tmpDir))
			defer func() {
				require.NoError(t, os.Chdir(oldCwd))
			}()

			lock, err := AcquireFileLock(tt.path)

			assert.Error(t, err)
			assert.Nil(t, lock)

			// Verify no .lock file was created in the working directory
			entries, err := os.ReadDir(".")
			require.NoError(t, err)
			assert.Empty(t, entries, "no files should be created in the working directory")
		})
	}
}

func TestAcquireFileLockSharesLockAcrossStateSymlink(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	aliasPath := filepath.Join(dir, "alias.json")
	createTestSymlink(t, statePath, aliasPath)

	for _, paths := range [][2]string{{aliasPath, statePath}, {statePath, aliasPath}} {
		lock, err := AcquireFileLock(paths[0])
		require.NoError(t, err)

		competingLock, err := AcquireFileLock(paths[1])
		assert.ErrorIs(t, err, ErrStateFileLocked)
		if competingLock != nil {
			assert.NoError(t, competingLock.Close())
		}
		require.NoError(t, lock.Close())
	}
}

func TestAcquireFileLockExcludesOtherProcessesAndRecoversAfterCrash(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	initial := NewState()
	initial.UpdateBook("initial", 0.1, "IN_PROGRESS")
	require.NoError(t, initial.Save(statePath))
	readyPath := filepath.Join(t.TempDir(), "ready")

	commandPath, err := os.Executable()
	require.NoError(t, err)
	command := exec.Command(commandPath, "-test.run=^TestStateFileLockProcessHelper$")
	command.Env = append(os.Environ(),
		"STATE_LOCK_HELPER_STATE="+statePath,
		"STATE_LOCK_HELPER_READY="+readyPath,
	)
	require.NoError(t, command.Start())
	processExited := false
	defer func() {
		if !processExited {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(readyPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = command.Process.Kill()
			_ = command.Wait()
			processExited = true
			t.Fatal("child process did not save state and acquire the lock")
		}
		time.Sleep(10 * time.Millisecond)
	}

	competingLock, err := AcquireFileLock(statePath)
	assert.ErrorIs(t, err, ErrStateFileLocked)
	if competingLock != nil {
		assert.NoError(t, competingLock.Close())
	}

	// Simulate a crash: process termination bypasses its deferred Close, while
	// the kernel must still release its exclusive lock.
	require.NoError(t, command.Process.Kill())
	require.Error(t, command.Wait())
	processExited = true

	lock, err := AcquireFileLock(statePath)
	require.NoError(t, err)
	require.NoError(t, lock.Close())

	reloaded, err := LoadState(statePath)
	require.NoError(t, err)
	assert.Equal(t, 0.5, reloaded.Books["child-book"].LastProgress)
}
