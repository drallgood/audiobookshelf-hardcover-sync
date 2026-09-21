//go:build windows

package state

func syncDirectory(string) error {
	// Windows File.Sync maps to FlushFileBuffers, which requires write access;
	// os.Open supplies a read-only directory handle.
	return nil
}
