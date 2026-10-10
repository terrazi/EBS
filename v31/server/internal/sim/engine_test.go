package sim

import (
	"fmt"
	"testing"
)

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
