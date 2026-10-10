package sim

import (
	"os"
	"strings"
	"testing"
)

func TestTrainingDocumentMaterializesAndControlsLifecycle(t *testing.T) {
	e, err := Open(t.TempDir() + "/documents.sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	login, err := e.Login("admin", "EBS2026!", "admin")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := e.Session(login["token"].(string))
	if err != nil {
		t.Fatal(err)
	}
	created, err := e.Execute(admin, "document.upload", Payload{
		"customer_id": 1, "document_type": "credit_application", "entity_type": "credit_application",
		"entity_id": 17, "_idempotency_key": "doc-test-1", "_terminal": "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	id := ID(created, "id")
	if id == 0 {
		t.Fatalf("document id missing: %#v", created)
	}
	var path, status string
	if err = e.DB.QueryRow("SELECT path,status FROM app_documents WHERE id=?", id).Scan(&path, &status); err != nil {
		t.Fatal(err)
	}
	if status != "GENERATED" || !strings.HasSuffix(path, ".pdf") {
		t.Fatalf("unexpected document row path=%q status=%q", path, status)
	}
	pdf, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(pdf), "%PDF-1.4\n") {
		t.Fatalf("materialized artifact is not a PDF: %q", string(pdf[:minInt(len(pdf), 20)]))
	}
	if _, err = e.Execute(admin, "document.verify", Payload{"document_id": id, "_idempotency_key": "doc-test-2", "_terminal": "test"}); err != nil {
		t.Fatal(err)
	}
	if err = e.DB.QueryRow("SELECT status FROM app_documents WHERE id=?", id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "VERIFIED" {
		t.Fatalf("document was not verified: %s", status)
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
