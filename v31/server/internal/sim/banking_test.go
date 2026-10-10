package sim

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"sync"
	"testing"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

func testEngine(t *testing.T) *Engine {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "banking.db"))+"?mode=rwc&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err = db.Exec(bankingSchema); err != nil {
		t.Fatal(err)
	}
	// These are owned by the root engine in production. The fixture keeps the
	// vertical tests independent while preserving the exact helper columns.
	common := `
CREATE TABLE sessions(token TEXT PRIMARY KEY,user_id INTEGER,created_at TEXT,expires_at TEXT);
CREATE TABLE ledger_entries(id INTEGER PRIMARY KEY AUTOINCREMENT,reference TEXT,account_code TEXT,direction TEXT,amount_minor INTEGER,currency TEXT);
CREATE TABLE notifications(id INTEGER PRIMARY KEY AUTOINCREMENT,customer_id INTEGER,title TEXT,body TEXT,reference TEXT,created_at TEXT);
CREATE TABLE work_items(id INTEGER PRIMARY KEY AUTOINCREMENT,kind TEXT,status TEXT,created_by TEXT,customer_id INTEGER,source TEXT,payload_json TEXT,department TEXT,created_at TEXT);
`
	if _, err = db.Exec(common); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = seedBanking(tx); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return &Engine{DB: db, Dir: t.TempDir()}
}

func applyBanking(t *testing.T, e *Engine, u User, op string, p Payload) Result {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	tx, err := e.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	r, handled, err := e.Banking(tx, u, op, p)
	if !handled {
		_ = tx.Rollback()
		t.Fatalf("operation was not handled: %s", op)
	}
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("%s: %v", op, err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSeedBankingCreatesV31DemoUniverse(t *testing.T) {
	e := testEngine(t)
	var customers, accounts int
	if err := e.DB.QueryRow("SELECT COUNT(*) FROM customers").Scan(&customers); err != nil {
		t.Fatal(err)
	}
	if err := e.DB.QueryRow("SELECT COUNT(*) FROM accounts").Scan(&accounts); err != nil {
		t.Fatal(err)
	}
	if customers != 30 || accounts != 60 {
		t.Fatalf("demo counts customers=%d accounts=%d", customers, accounts)
	}
	var clearPassword string
	if err := e.DB.QueryRow("SELECT password_hash FROM users WHERE username='gise01'").Scan(&clearPassword); err != nil {
		t.Fatal(err)
	}
	if clearPassword == "1234" || clearPassword == "" {
		t.Fatal("demo password was stored in clear text")
	}
}

func TestLoginSessionClientRoleAndLogout(t *testing.T) {
	e := testEngine(t)
	staff, err := e.Login("gise01", "1234", "personel")
	if err != nil {
		t.Fatal(err)
	}
	token := staff["token"].(string)
	u, err := e.Session(token)
	if err != nil || u.Role != "PERSONEL" {
		t.Fatalf("session user=%+v err=%v", u, err)
	}
	if _, err = e.Login("gise01", "1234", "musteri"); err == nil {
		t.Fatal("personel could log in through customer client")
	}
	if err = e.Logout(token); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Session(token); err == nil {
		t.Fatal("logged out token remained valid")
	}
}

func TestCustomerOwnershipAndStaffDepositLedger(t *testing.T) {
	e := testEngine(t)
	var customerID, ownAccount, otherAccount int64
	if err := e.DB.QueryRow("SELECT id FROM customers WHERE code='CUST-0001'").Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if err := e.DB.QueryRow("SELECT id FROM accounts WHERE customer_id=? ORDER BY id LIMIT 1", customerID).Scan(&ownAccount); err != nil {
		t.Fatal(err)
	}
	if err := e.DB.QueryRow("SELECT id FROM accounts WHERE customer_id=2 ORDER BY id LIMIT 1").Scan(&otherAccount); err != nil {
		t.Fatal(err)
	}
	customer := User{ID: 10, Username: "Ahmet Karaca", Name: "Ahmet Karaca", Role: "CUSTOMER", CustomerID: customerID, Permissions: []string{"self_service"}}
	tx, err := e.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	_, handled, err := e.Banking(tx, customer, "banking.transfer", Payload{"source_account_id": otherAccount, "target_account_id": ownAccount, "amount_minor": "1000"})
	if !handled || err != ErrForbidden {
		_ = tx.Rollback()
		t.Fatalf("cross-customer transfer err=%v handled=%v", err, handled)
	}
	_ = tx.Rollback()

	staff := User{ID: 1, Username: "gise01", Role: "PERSONEL", Department: "Gişe / Kasa", Permissions: []string{"banking"}}
	applyBanking(t, e, staff, "banking.cash_open", nil)
	before := int64(0)
	if err = e.DB.QueryRow("SELECT balance_minor FROM accounts WHERE id=?", ownAccount).Scan(&before); err != nil {
		t.Fatal(err)
	}
	applyBanking(t, e, staff, "banking.deposit", Payload{"account_id": ownAccount, "amount_minor": "12500", "_reference": "TEST-CASH-1"})
	var after, debit, credit int64
	if err = e.DB.QueryRow("SELECT balance_minor FROM accounts WHERE id=?", ownAccount).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before+12500 {
		t.Fatalf("deposit balance before=%d after=%d", before, after)
	}
	if err = e.DB.QueryRow("SELECT SUM(CASE WHEN direction='DEBIT' THEN amount_minor ELSE 0 END),SUM(CASE WHEN direction='CREDIT' THEN amount_minor ELSE 0 END) FROM ledger_entries WHERE reference='TEST-CASH-1'").Scan(&debit, &credit); err != nil {
		t.Fatal(err)
	}
	if debit != credit || debit != 12500 {
		t.Fatalf("unbalanced deposit debit=%d credit=%d", debit, credit)
	}
}

func TestLargeTransferQueuesWithoutChangingBalances(t *testing.T) {
	e := testEngine(t)
	var source, target, before int64
	if err := e.DB.QueryRow("SELECT id FROM accounts WHERE customer_id=1 ORDER BY id LIMIT 1").Scan(&source); err != nil {
		t.Fatal(err)
	}
	if err := e.DB.QueryRow("SELECT id FROM accounts WHERE customer_id=2 ORDER BY id LIMIT 1").Scan(&target); err != nil {
		t.Fatal(err)
	}
	if err := e.DB.QueryRow("SELECT balance_minor FROM accounts WHERE id=?", source).Scan(&before); err != nil {
		t.Fatal(err)
	}
	staff := User{ID: 2, Username: "gise02", Role: "PERSONEL", Department: "Gişe / Kasa", Permissions: []string{"banking"}}
	queued := applyBanking(t, e, staff, "banking.transfer", Payload{"source_account_id": source, "target_account_id": target, "amount_minor": "6000000", "_reference": "TEST-TRANSFER-1"})
	if queued["status"] != "PENDING_APPROVAL" {
		t.Fatalf("status=%v", queued["status"])
	}
	var after, journalRows, workRows int64
	if err := e.DB.QueryRow("SELECT balance_minor FROM accounts WHERE id=?", source).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if err := e.DB.QueryRow("SELECT COUNT(*) FROM ledger_entries WHERE reference='TEST-TRANSFER-1'").Scan(&journalRows); err != nil {
		t.Fatal(err)
	}
	if err := e.DB.QueryRow("SELECT COUNT(*) FROM work_items WHERE status='PENDING' AND kind='TRANSFER_APPROVAL'").Scan(&workRows); err != nil {
		t.Fatal(err)
	}
	if after != before || journalRows != 0 || workRows != 1 {
		t.Fatalf("queued transfer mutated state balance %d/%d journal=%d work=%d", before, after, journalRows, workRows)
	}
}

func TestConcurrentAccountReadsRemainOwnerFiltered(t *testing.T) {
	e := testEngine(t)
	customer := User{ID: 10, Username: "Ahmet Karaca", Role: "CUSTOMER", CustomerID: 1, Permissions: []string{"self_service"}}
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows, handled, err := e.BankingQuery(customer, "accounts", url.Values{})
			if err != nil || !handled {
				errs <- err
				return
			}
			for _, row := range rows.([]Result) {
				if ID(row, "customer_id") != customer.CustomerID {
					errs <- ErrForbidden
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestQueriesUnknownResourceAndCustomerSummary(t *testing.T) {
	e := testEngine(t)
	customer := User{ID: 10, Username: "Ahmet Karaca", Role: "CUSTOMER", CustomerID: 1, Permissions: []string{"self_service"}}
	if _, handled, err := e.BankingQuery(customer, "does-not-exist", url.Values{}); handled || err != nil {
		t.Fatalf("unknown resource handled=%v err=%v", handled, err)
	}
	out, handled, err := e.BankingQuery(customer, "customer-summary", url.Values{"customer_id": []string{"2"}})
	if !handled || err != nil {
		t.Fatalf("summary err=%v handled=%v", err, handled)
	}
	// Customer query ignores a spoofed customer_id and returns the session owner.
	summary := out.(Result)
	customerRow := summary["customer"].(Result)
	if ID(customerRow, "id") != 1 {
		t.Fatalf("customer summary leaked customer id %d", ID(customerRow, "id"))
	}
	if b, err := json.Marshal(summary); err != nil || len(b) == 0 {
		t.Fatal("summary was not serializable")
	}
}

func TestBankingProductFlowsPersistStateAndJournal(t *testing.T) {
	e := testEngine(t)
	customer := User{ID: 10, Username: "Ahmet Karaca", Role: "CUSTOMER", CustomerID: 1, Permissions: []string{"self_service"}}
	var tryAccount, usdAccount, recipient int64
	if err := e.DB.QueryRow("SELECT id FROM accounts WHERE customer_id=1 AND currency='TRY' ORDER BY id LIMIT 1").Scan(&tryAccount); err != nil {
		t.Fatal(err)
	}
	if err := e.DB.QueryRow("SELECT id FROM accounts WHERE customer_id=1 AND currency='USD' ORDER BY id LIMIT 1").Scan(&usdAccount); err != nil {
		t.Fatal(err)
	}
	if err := e.DB.QueryRow("SELECT id FROM accounts WHERE customer_id=2 AND currency='TRY' ORDER BY id LIMIT 1").Scan(&recipient); err != nil {
		t.Fatal(err)
	}
	opened := applyBanking(t, e, customer, "banking.account_open", Payload{"currency": "EUR", "account_type": "Birikim"})
	if opened["status"] != "COMPLETED" {
		t.Fatalf("account open result: %#v", opened)
	}
	term := applyBanking(t, e, customer, "banking.term_deposit", Payload{"source_account_id": tryAccount, "amount_minor": "1000", "days": "30", "_reference": "TEST-TERM-1"})
	if term["status"] != "COMPLETED" {
		t.Fatalf("term deposit result: %#v", term)
	}
	payment := applyBanking(t, e, customer, "banking.bill_pay", Payload{"account_id": tryAccount, "amount_minor": "1000", "institution": "Elektrik", "_reference": "TEST-BILL-1"})
	if payment["status"] != "COMPLETED" {
		t.Fatalf("bill payment result: %#v", payment)
	}
	autopay := applyBanking(t, e, customer, "banking.autopay_create", Payload{"account_id": tryAccount, "amount_minor": "1000", "institution": "Su", "due_day": "15"})
	applyBanking(t, e, customer, "banking.autopay_cancel", Payload{"autopay_id": fmt.Sprint(autopay["autopay_id"])})
	applyBanking(t, e, customer, "banking.easy_address_create", Payload{"account_id": tryAccount, "address": "ahmet@example.invalid"})
	request := applyBanking(t, e, customer, "banking.payment_request", Payload{"requester_account_id": tryAccount, "payer_account_id": recipient, "amount_minor": "1000", "note": "Test talep", "_reference": "TEST-REQ-1"})
	if request["status"] != "PENDING" {
		t.Fatalf("request result: %#v", request)
	}
	fx := applyBanking(t, e, customer, "banking.fx_trade", Payload{"source_account_id": usdAccount, "target_account_id": tryAccount, "amount_minor": "100", "_reference": "TEST-FX-1"})
	if fx["status"] != "COMPLETED" || fx["target_amount_minor"].(int64) <= 0 {
		t.Fatalf("fx result: %#v", fx)
	}
	staff := User{ID: 2, Username: "gise02", Role: "PERSONEL", Department: "Gişe / Kasa", Permissions: []string{"banking"}}
	transfer := applyBanking(t, e, staff, "banking.transfer", Payload{"source_account_id": tryAccount, "target_account_id": recipient, "amount_minor": "1000", "_reference": "TEST-TRF-1"})
	if transfer["status"] != "COMPLETED" {
		t.Fatalf("transfer result: %#v", transfer)
	}
	recall := applyBanking(t, e, staff, "banking.transfer_recall", Payload{"reference": "TEST-TRF-1"})
	if recall["status"] != "RECALLED" {
		t.Fatalf("recall result: %#v", recall)
	}
	var journalRows int
	if err := e.DB.QueryRow("SELECT COUNT(*) FROM ledger_entries WHERE reference LIKE 'TEST-%'").Scan(&journalRows); err != nil {
		t.Fatal(err)
	}
	if journalRows < 10 {
		t.Fatalf("expected journals for product flows, got %d", journalRows)
	}
}
