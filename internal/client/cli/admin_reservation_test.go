package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// writeServerConfig writes a server config naming a database, and returns the
// two paths.
func writeServerConfig(t *testing.T, body string) (configPath, dbPath string) {
	t.Helper()

	dir := t.TempDir()
	dbPath = filepath.Join(dir, "control.db")
	if err := os.WriteFile(dbPath, []byte("not really sqlite"), 0o600); err != nil {
		t.Fatalf("write database: %v", err)
	}

	configPath = filepath.Join(dir, "server.yaml")
	content := fmt.Sprintf("db_path: %s\n%s", dbPath, body)
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return configPath, dbPath
}

func TestTCPPortRangeComesFromTheConfigThatNamesThisDatabase(t *testing.T) {
	configPath, dbPath := writeServerConfig(t, "tcp_port_min: 33000\ntcp_port_max: 33020\n")

	min, max, source := tcpPortRangeFromConfig(configPath, dbPath)
	if min != 33000 || max != 33020 {
		t.Errorf("range = %d-%d, want 33000-33020", min, max)
	}
	if source != configPath {
		t.Errorf("source = %q, want %q", source, configPath)
	}
}

// An omitted range is not an absent one: the server fills it with the same
// defaults, so the check has to use them too.
func TestTCPPortRangeFallsBackToTheServerDefaults(t *testing.T) {
	configPath, dbPath := writeServerConfig(t, "")

	min, max, _ := tcpPortRangeFromConfig(configPath, dbPath)
	if min != 20000 || max != 40000 {
		t.Errorf("range = %d-%d, want the 20000-40000 defaults", min, max)
	}
}

// A config describing some other deployment must not police this one.
func TestTCPPortRangeIsIgnoredWhenTheConfigNamesAnotherDatabase(t *testing.T) {
	configPath, _ := writeServerConfig(t, "tcp_port_min: 33000\ntcp_port_max: 33020\n")

	min, max, source := tcpPortRangeFromConfig(configPath, filepath.Join(t.TempDir(), "other.db"))
	if min != 0 || max != 0 || source != "" {
		t.Errorf("range = %d-%d from %q, want nothing", min, max, source)
	}
}

func TestTCPPortRangeIsEmptyWithoutAServerConfig(t *testing.T) {
	dir := t.TempDir()

	min, max, source := tcpPortRangeFromConfig(filepath.Join(dir, "absent.yaml"), filepath.Join(dir, "control.db"))
	if min != 0 || max != 0 || source != "" {
		t.Errorf("range = %d-%d from %q, want nothing", min, max, source)
	}
}

// The same database reached by two spellings is one database, or a relative
// --db would silently escape the check.
func TestSameDatabaseSeesThroughPathSpelling(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "control.db")
	if err := os.WriteFile(path, []byte("db"), 0o600); err != nil {
		t.Fatalf("write database: %v", err)
	}

	if !sameDatabase(path, filepath.Join(dir, ".", "control.db")) {
		t.Error("a path with a . segment should still name the same file")
	}
	if sameDatabase(path, filepath.Join(dir, "other.db")) {
		t.Error("two different names are not the same database")
	}
}

// Neither file existing is the install-time case: compare the paths rather
// than refusing to answer.
func TestSameDatabaseComparesPathsWhenNeitherFileExists(t *testing.T) {
	dir := t.TempDir()

	if !sameDatabase(filepath.Join(dir, "a.db"), filepath.Join(dir, "a.db")) {
		t.Error("identical paths should compare equal")
	}
	if sameDatabase(filepath.Join(dir, "a.db"), filepath.Join(dir, "b.db")) {
		t.Error("different paths should not compare equal")
	}
}
