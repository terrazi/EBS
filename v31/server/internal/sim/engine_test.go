package sim

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ncruces/go-sqlite3"
	"github.com/ncruces/go-sqlite3/vfs"
)

func TestSQLiteDSNUsesURIAndEscapesWindowsPath(t *testing.T) {
	got := sqliteDSN(`C:\Program Files\EBS Bankacılık\müşteri #1?.sqlite3`)
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "file" || u.Opaque != "C:/Program%20Files/EBS%20Bankac%C4%B1l%C4%B1k/m%C3%BC%C5%9Fteri%20%231%3F.sqlite3" {
		t.Fatalf("unexpected file URI: %s (opaque=%q)", got, u.Opaque)
	}
	q := u.Query()
	if got := q["_pragma"]; len(got) != 2 || got[0] != "foreign_keys(1)" || got[1] != "busy_timeout(10000)" {
		t.Fatalf("pragma query values: %#v in %s", got, got)
	}
	if strings.Contains(got, `\Program`) || strings.Contains(got, "#1?.sqlite3?") {
		t.Fatalf("Windows path was not URI-escaped: %s", got)
	}
	if !strings.Contains(got, "%20") || !strings.Contains(got, "%23") || !strings.Contains(got, "%3F") {
		t.Fatalf("special path characters were not escaped: %s", got)
	}
}

// The embedded SQLite WASM parser is platform independent. Capture the real
// filename it passes to the Go VFS, rather than merely checking net/url output.
// This catches file:///C:/... being handed to Windows as the invalid /C:/....
func TestSQLiteDSNPassesNativePathsToActualSQLiteVFS(t *testing.T) {
	cases := []struct {
		name, path, want string
	}{
		{"windows_drive", `C:\Program Files\EBS Bankacılık\müşteri #1?.sqlite3`, "C:/Program Files/EBS Bankacılık/müşteri #1?.sqlite3"},
		{"unc", `\\server\share\EBS Bankacılık\müşteri #1?.sqlite3`, "//server/share/EBS Bankacılık/müşteri #1?.sqlite3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			capture := &sqlitePathCaptureVFS{}
			const vfsName = "ebs-dsn-regression"
			vfs.Register(vfsName, capture)
			defer vfs.Unregister(vfsName)
			conn, err := sqlite3.Open(sqliteDSN(tc.path) + "&vfs=" + vfsName)
			if conn != nil {
				conn.Close()
			}
			if err == nil {
				t.Fatal("capture VFS should stop before filesystem access")
			}
			if capture.path != tc.want {
				t.Fatalf("actual SQLite VFS filename=%q, want %q; DSN=%s", capture.path, tc.want, sqliteDSN(tc.path))
			}
		})
	}
}

type sqlitePathCaptureVFS struct{ path string }

func (v *sqlitePathCaptureVFS) FullPathname(path string) (string, error) {
	v.path = path
	return "", errors.New("stop after capturing SQLite VFS path")
}
func (*sqlitePathCaptureVFS) Open(_ string, flags vfs.OpenFlag) (vfs.File, vfs.OpenFlag, error) {
	return nil, flags, errors.New("unexpected filesystem access")
}
func (*sqlitePathCaptureVFS) Delete(string, bool) error { return errors.New("unexpected delete") }
func (*sqlitePathCaptureVFS) Access(string, vfs.AccessFlag) (bool, error) {
	return false, errors.New("unexpected filesystem access")
}

func TestEngineOpenPersistsAndAppliesSQLitePragmas(t *testing.T) {
	dir := t.TempDir()
	// '?' is illegal in a native Windows filename; retain Unicode, spaces and '#'.
	path := filepath.Join(dir, "müşteri #1.sqlite3")
	e, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var foreignKeys, busyTimeout int
	if err = e.DB.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if err = e.DB.QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 || busyTimeout != 10000 {
		t.Fatalf("SQLite pragmas foreign_keys=%d busy_timeout=%d", foreignKeys, busyTimeout)
	}
	if _, err = e.DB.Exec("CREATE TABLE persistence_marker(value TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err = e.DB.Exec("INSERT INTO persistence_marker(value) VALUES(?)", "ok"); err != nil {
		t.Fatal(err)
	}
	if err = e.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatalf("database path was not created: %v", err)
	}
	// Enumerating the directory works on Windows too: os.Stat on a name with
	// '?' returns an invalid-name error there rather than os.ErrNotExist.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), "?_pragma=") {
			t.Fatalf("legacy query-suffixed database unexpectedly exists: %s", entry.Name())
		}
	}
	e, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	var value string
	if err = e.DB.QueryRow("SELECT value FROM persistence_marker").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "ok" {
		t.Fatalf("persisted marker=%q", value)
	}
}

func TestEngineOpenLoginAndAtomicDeposit(t *testing.T) {
	e, err := Open(t.TempDir() + "/ebs.sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	login, err := e.Login("gise01", "1234", "personel")
	if err != nil {
		t.Fatal(err)
	}
	u, err := e.Session(login["token"].(string))
	if err != nil {
		t.Fatal(err)
	}
	var account int64
	if err = e.DB.QueryRow("SELECT id FROM accounts ORDER BY id LIMIT 1").Scan(&account); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Execute(u, "banking.cash_open", Payload{"_terminal": "test"}); err != nil {
		t.Fatal(err)
	}
	before := int64(0)
	if err = e.DB.QueryRow("SELECT balance_minor FROM accounts WHERE id=?", account).Scan(&before); err != nil {
		t.Fatal(err)
	}
	result, err := e.Execute(u, "banking.deposit", Payload{"account_id": account, "amount_minor": "12500", "_idempotency_key": "engine-deposit-1", "_terminal": "test"})
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != "COMPLETED" {
		t.Fatalf("unexpected result: %#v", result)
	}
	var after int64
	if err = e.DB.QueryRow("SELECT balance_minor FROM accounts WHERE id=?", account).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before+12500 {
		t.Fatalf("balance before=%d after=%d", before, after)
	}
	dup, err := e.Execute(u, "banking.deposit", Payload{"account_id": account, "amount_minor": "12500", "_idempotency_key": "engine-deposit-1", "_terminal": "test"})
	if err != nil {
		t.Fatal(err)
	}
	if dup["duplicate"] != true {
		t.Fatalf("idempotency result: %#v", dup)
	}
	if err = e.DB.QueryRow("SELECT balance_minor FROM accounts WHERE id=?", account).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before+12500 {
		t.Fatalf("duplicate changed balance: %d", after)
	}
}

func TestTrainingAssignmentProgressAndScore(t *testing.T) {
	e, err := Open(t.TempDir() + "/training.sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	adminLogin, err := e.Login("admin", "EBS2026!", "admin")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := e.Session(adminLogin["token"].(string))
	if err != nil {
		t.Fatal(err)
	}
	studentLogin, err := e.Login("gise01", "1234", "personnel")
	if err != nil {
		t.Fatal(err)
	}
	student, err := e.Session(studentLogin["token"].(string))
	if err != nil {
		t.Fatal(err)
	}
	assigned, err := e.Execute(admin, "training.assign", Payload{"scenario_id": "1", "trainee_username": "gise01"})
	if err != nil {
		t.Fatal(err)
	}
	id := fmt.Sprint(assigned["id"])
	if _, err = e.Execute(student, "training.start", Payload{"assignment_id": id}); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Execute(student, "training.record_step", Payload{"assignment_id": id, "step_no": "1", "operation": "banking.cash_open", "passed": "true", "score": "50", "attempt_no": "1", "reference": "EBS-TRAIN-1"}); err != nil {
		t.Fatal(err)
	}
	completed, err := e.Execute(student, "training.complete", Payload{"assignment_id": id})
	if err != nil {
		t.Fatal(err)
	}
	if completed["status"] != "COMPLETED" {
		t.Fatalf("training result: %#v", completed)
	}
}
