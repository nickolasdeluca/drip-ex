package utils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitFileLoggerWritesToTheGivenPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "service.log")

	if err := InitFileLogger(path, false); err != nil {
		t.Fatalf("InitFileLogger() unexpected error: %v", err)
	}
	t.Cleanup(func() {
		// Windows will not let TempDir remove a file this process still holds
		// open, so the sink has to be closed before the test ends.
		_ = CloseFileLogger()
		logger = nil
	})

	GetLogger().Info("tunnel connected")
	Sync()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read the log file: %v", err)
	}
	if !strings.Contains(string(data), "tunnel connected") {
		t.Fatalf("log file = %q, want it to contain the logged message", data)
	}
}

func TestRotateLogFileKeepsOneGeneration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "service.log")

	if err := os.WriteFile(path, []byte("first"), 0o600); err != nil {
		t.Fatalf("failed to seed the log file: %v", err)
	}

	if err := rotateLogFile(path, 1024); err != nil {
		t.Fatalf("rotateLogFile() unexpected error: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("a log file below the limit should not be rotated: %v", err)
	}

	if err := rotateLogFile(path, 1); err != nil {
		t.Fatalf("rotateLogFile() unexpected error: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the oversized log file should have been renamed, stat err = %v", err)
	}

	rotated, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("failed to read the rotated log file: %v", err)
	}
	if string(rotated) != "first" {
		t.Fatalf("rotated log = %q, want %q", rotated, "first")
	}

	// A second rotation overwrites the previous generation rather than piling up.
	if err := os.WriteFile(path, []byte("second"), 0o600); err != nil {
		t.Fatalf("failed to write the log file: %v", err)
	}
	if err := rotateLogFile(path, 1); err != nil {
		t.Fatalf("rotateLogFile() unexpected error: %v", err)
	}

	rotated, err = os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("failed to read the rotated log file: %v", err)
	}
	if string(rotated) != "second" {
		t.Fatalf("rotated log = %q, want %q", rotated, "second")
	}
}

func TestRotateLogFileIgnoresAMissingFile(t *testing.T) {
	t.Parallel()

	if err := rotateLogFile(filepath.Join(t.TempDir(), "absent.log"), 1); err != nil {
		t.Fatalf("rotateLogFile() unexpected error: %v", err)
	}
}

// A second InitFileLogger must not leak the first file. On Windows the stale
// handle would block deleting or rotating the log it points at.
func TestInitFileLoggerClosesThePreviousFile(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.log")
	second := filepath.Join(dir, "second.log")

	if err := InitFileLogger(first, false); err != nil {
		t.Fatalf("InitFileLogger() unexpected error: %v", err)
	}
	if err := InitFileLogger(second, false); err != nil {
		t.Fatalf("InitFileLogger() unexpected error: %v", err)
	}
	t.Cleanup(func() {
		_ = CloseFileLogger()
		logger = nil
	})

	if err := os.Remove(first); err != nil {
		t.Fatalf("removing the replaced log file failed, so its handle leaked: %v", err)
	}

	GetLogger().Info("still logging")
	Sync()

	data, err := os.ReadFile(second)
	if err != nil {
		t.Fatalf("failed to read the current log file: %v", err)
	}
	if !strings.Contains(string(data), "still logging") {
		t.Fatalf("log file = %q, want the message written after the swap", data)
	}
}

func TestCloseFileLoggerIsSafeWithoutAFile(t *testing.T) {
	if err := CloseFileLogger(); err != nil {
		t.Fatalf("CloseFileLogger() with no file open error = %v", err)
	}
}
