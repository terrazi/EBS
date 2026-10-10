package sim

import (
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
