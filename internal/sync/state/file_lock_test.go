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
