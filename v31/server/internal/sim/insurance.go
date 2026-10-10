package sim

// Insurance contains the stateful insurance vertical slice.  The handlers are
// deliberately transaction based: the caller owns the transaction and can
// roll it back when the HTTP envelope (audit/idempotency) rejects a request.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

func ensureInsuranceSchema(tx *sql.Tx) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS insurance_quotes (
			id INTEGER PRIMARY KEY AUTOINCREMENT, customer_id INTEGER NOT NULL,
			product TEXT NOT NULL, status TEXT NOT NULL, coverage_json TEXT NOT NULL DEFAULT '{}',
			premium_minor INTEGER NOT NULL, currency TEXT NOT NULL DEFAULT 'TRY',
			reference TEXT NOT NULL UNIQUE, created_by TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS insurance_policies (
			id INTEGER PRIMARY KEY AUTOINCREMENT, customer_id INTEGER NOT NULL, quote_id INTEGER,
			policy_no TEXT NOT NULL UNIQUE, product TEXT NOT NULL, status TEXT NOT NULL,
			coverage_json TEXT NOT NULL DEFAULT '{}', premium_minor INTEGER NOT NULL,
			currency TEXT NOT NULL DEFAULT 'TRY', start_date TEXT NOT NULL, end_date TEXT NOT NULL,
			version INTEGER NOT NULL DEFAULT 1, created_by TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS insurance_endorsements (
			id INTEGER PRIMARY KEY AUTOINCREMENT, policy_id INTEGER NOT NULL, customer_id INTEGER NOT NULL,
			kind TEXT NOT NULL, status TEXT NOT NULL, changes_json TEXT NOT NULL DEFAULT '{}',
			reference TEXT NOT NULL UNIQUE, created_by TEXT NOT NULL, approved_by TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS insurance_premium_payments (
			id INTEGER PRIMARY KEY AUTOINCREMENT, policy_id INTEGER NOT NULL, customer_id INTEGER NOT NULL,
			account_id INTEGER NOT NULL, amount_minor INTEGER NOT NULL, currency TEXT NOT NULL,
			status TEXT NOT NULL, reference TEXT NOT NULL UNIQUE, created_by TEXT NOT NULL, created_at TEXT NOT NULL
		)`,
	}
	for _, stmt := range stmts {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func insuranceJSON(v any) string {
	if v == nil {
		return "{}"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func insuranceRef(p Payload, prefix string) string {
	if r := Ref(p); r != "" {
		return r
	}
	return fmt.Sprintf("%s-%s", prefix, strings.ReplaceAll(strings.ReplaceAll(Now(), ":", ""), ".", ""))
}

func insuranceStaff(u User) error {
	if err := Staff(u); err != nil {
		return err
	}
	if err := Allowed(u, "insurance"); err == nil {
		return nil
	}
	// v3.1 demo staff are provisioned with the common banking permission;
	// department-specific permissions remain supported when configured.
	return Allowed(u, "banking")
}

func insurancePolicy(tx *sql.Tx, u User, id int64) (Result, error) {
	row, err := Row(tx, `SELECT * FROM insurance_policies WHERE id=?`, id)
	if err != nil {
		return nil, err
	}
	if u.Role == "CUSTOMER" && ID(row, "customer_id") != u.CustomerID {
		return nil, ErrForbidden
	}
	return row, nil
}

// Insurance executes insurance.quote, insurance.policy_issue,
// insurance.endorse, insurance.renew, insurance.cancel and
// insurance.premium_collect.
func Insurance(tx *sql.Tx, u User, op string, p Payload) (Result, error) {
	if err := ensureInsuranceSchema(tx); err != nil {
		return nil, err
	}
	op = strings.ToLower(strings.TrimSpace(op))
	if strings.HasPrefix(op, "insurance.") {
		op = strings.TrimPrefix(op, "insurance.")
	}
	now := Now()
	ref := insuranceRef(p, "INS")

	switch op {
	case "quote":
		customerID := Int(p, "customer_id")
		if customerID == 0 {
			customerID = u.CustomerID
		}
		if customerID == 0 {
			return nil, fmt.Errorf("CUSTOMER: Müşteri seçilmelidir.")
		}
		if _, err := Customer(tx, u, customerID); err != nil {
			return nil, err
		}
		product := Str(p, "product")
		if product == "" {
			return nil, fmt.Errorf("PRODUCT: Sigorta ürünü zorunludur.")
		}
		premium, err := Amount(p, "premium_minor")
		if err != nil {
			return nil, err
		}
		currency := Str(p, "currency")
		if currency == "" {
			currency = "TRY"
		}
		coverage := insuranceJSON(p["coverage"])
		res, err := tx.Exec(`INSERT INTO insurance_quotes(customer_id,product,status,coverage_json,premium_minor,currency,reference,created_by,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, customerID, product, "QUOTED", coverage, premium, currency, ref, u.Username, now, now)
		if err != nil {
			return nil, err
		}
		id, _ := res.LastInsertId()
		return Row(tx, `SELECT * FROM insurance_quotes WHERE id=?`, id)

	case "policy_issue":
		if err := insuranceStaff(u); err != nil {
			return nil, err
		}
		quoteID := Int(p, "quote_id")
		quote, err := Row(tx, `SELECT * FROM insurance_quotes WHERE id=?`, quoteID)
		if err != nil {
			return nil, err
		}
		if Text(quote, "status") != "QUOTED" && Text(quote, "status") != "APPROVED" {
			return nil, fmt.Errorf("STATE: Teklif poliçeye dönüştürülebilir durumda değil.")
		}
		customerID := ID(quote, "customer_id")
		if err := KYC(tx, u, customerID); err != nil {
			return nil, err
		}
		start := Str(p, "start_date")
		if start == "" {
			start = now[:10]
		}
		end := Str(p, "end_date")
		if end == "" {
			end = start
		}
		policyNo := Str(p, "policy_no")
		if policyNo == "" {
			policyNo = fmt.Sprintf("POL-%s", ref)
		}
		res, err := tx.Exec(`INSERT INTO insurance_policies(customer_id,quote_id,policy_no,product,status,coverage_json,premium_minor,currency,start_date,end_date,version,created_by,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, customerID, quoteID, policyNo, Text(quote, "product"), "ACTIVE", Text(quote, "coverage_json"), ID(quote, "premium_minor"), Text(quote, "currency"), start, end, 1, u.Username, now, now)
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec(`UPDATE insurance_quotes SET status='ISSUED',updated_at=? WHERE id=?`, now, quoteID); err != nil {
			return nil, err
		}
		id, _ := res.LastInsertId()
		if err = Notify(tx, customerID, "Poliçe oluşturuldu", "Sigorta poliçeniz aktif edildi.", ref); err != nil {
			return nil, err
		}
		return Row(tx, `SELECT * FROM insurance_policies WHERE id=?`, id)

	case "endorse":
		policyID := Int(p, "policy_id")
		policy, err := insurancePolicy(tx, u, policyID)
		if err != nil {
			return nil, err
		}
		if Text(policy, "status") != "ACTIVE" {
			return nil, fmt.Errorf("STATE: Yalnız aktif poliçeye zeyil yapılabilir.")
		}
		customerID := ID(policy, "customer_id")
		kind := Str(p, "kind")
		if kind == "" {
			return nil, fmt.Errorf("ENDORSEMENT: Değişiklik türü zorunludur.")
		}
		status := "PENDING"
		if u.Role != "CUSTOMER" {
			if err := insuranceStaff(u); err != nil {
				return nil, err
			}
			status = "APPROVED"
		} else if u.CustomerID != customerID {
			return nil, ErrForbidden
		}
		res, err := tx.Exec(`INSERT INTO insurance_endorsements(policy_id,customer_id,kind,status,changes_json,reference,created_by,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, policyID, customerID, kind, status, insuranceJSON(p["changes"]), ref, u.Username, now, now)
		if err != nil {
			return nil, err
		}
		id, _ := res.LastInsertId()
		if status == "PENDING" {
			if _, err = Queue(tx, "INSURANCE_ENDORSEMENT", u, customerID, Payload{"endorsement_id": id, "policy_id": policyID, "reference": ref}); err != nil {
				return nil, err
			}
		}
		return Row(tx, `SELECT * FROM insurance_endorsements WHERE id=?`, id)

	case "renew":
		if err := insuranceStaff(u); err != nil {
			return nil, err
		}
		policyID := Int(p, "policy_id")
		old, err := insurancePolicy(tx, u, policyID)
		if err != nil {
			return nil, err
		}
		if Text(old, "status") != "ACTIVE" && Text(old, "status") != "EXPIRED" {
			return nil, fmt.Errorf("STATE: Poliçe yenilenebilir durumda değil.")
		}
		customerID := ID(old, "customer_id")
		policyNo := Str(p, "policy_no")
		if policyNo == "" {
			policyNo = fmt.Sprintf("POL-%s", ref)
		}
		start := Str(p, "start_date")
		if start == "" {
			start = now[:10]
		}
		end := Str(p, "end_date")
		if end == "" {
			end = start
		}
		res, err := tx.Exec(`INSERT INTO insurance_policies(customer_id,quote_id,policy_no,product,status,coverage_json,premium_minor,currency,start_date,end_date,version,created_by,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, customerID, ID(old, "quote_id"), policyNo, Text(old, "product"), "ACTIVE", Text(old, "coverage_json"), ID(old, "premium_minor"), Text(old, "currency"), start, end, ID(old, "version")+1, u.Username, now, now)
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec(`UPDATE insurance_policies SET status='RENEWED',updated_at=? WHERE id=?`, now, policyID); err != nil {
			return nil, err
		}
		id, _ := res.LastInsertId()
		if err = Notify(tx, customerID, "Poliçe yenilendi", "Poliçeniz yenilenerek yeni versiyon oluşturuldu.", ref); err != nil {
			return nil, err
		}
		return Row(tx, `SELECT * FROM insurance_policies WHERE id=?`, id)

	case "cancel":
		policyID := Int(p, "policy_id")
		policy, err := insurancePolicy(tx, u, policyID)
		if err != nil {
			return nil, err
		}
		if Text(policy, "status") != "ACTIVE" {
			return nil, fmt.Errorf("STATE: Poliçe aktif değil.")
		}
		customerID := ID(policy, "customer_id")
		if u.Role == "CUSTOMER" {
			if _, err = Queue(tx, "INSURANCE_CANCEL", u, customerID, Payload{"policy_id": policyID, "reference": ref, "reason": Str(p, "reason")}); err != nil {
				return nil, err
			}
			return Result{"policy_id": policyID, "status": "PENDING", "reference": ref}, nil
		}
		if err := insuranceStaff(u); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(`UPDATE insurance_policies SET status='CANCELLED',updated_at=? WHERE id=? AND status='ACTIVE'`, now, policyID); err != nil {
			return nil, err
		}
		if err = Notify(tx, customerID, "Poliçe iptal edildi", "Poliçeniz iptal edildi.", ref); err != nil {
			return nil, err
		}
		policy["status"] = "CANCELLED"
		return policy, nil

	case "premium_collect":
		policyID := Int(p, "policy_id")
		policy, err := insurancePolicy(tx, u, policyID)
		if err != nil {
			return nil, err
		}
		if Text(policy, "status") != "ACTIVE" {
			return nil, fmt.Errorf("STATE: Poliçe aktif değil.")
		}
		customerID := ID(policy, "customer_id")
		accountID := Int(p, "account_id")
		account, err := Account(tx, u, accountID)
		if err != nil {
			return nil, err
		}
		if ID(account, "customer_id") != customerID {
			return nil, ErrForbidden
		}
		if u.Role == "CUSTOMER" && u.CustomerID != customerID {
			return nil, ErrForbidden
		}
		amount := Int(p, "amount_minor")
		if amount <= 0 {
			amount = ID(policy, "premium_minor")
		}
		if amount <= 0 {
			return nil, fmt.Errorf("AMOUNT: Prim tutarı bulunamadı.")
		}
		currency := Text(policy, "currency")
		if currency == "" {
			currency = Text(account, "currency")
		}
		if currency != Text(account, "currency") {
			return nil, fmt.Errorf("CURRENCY: Hesap ve poliçe para birimi aynı olmalı.")
		}
		if err = Balance(tx, accountID, -amount); err != nil {
			return nil, err
		}
		if err = Journal(tx, ref, fmt.Sprintf("ACCOUNT:%d", accountID), fmt.Sprintf("INSURANCE_PREMIUM:%d", policyID), amount, currency); err != nil {
			return nil, err
		}
		res, err := tx.Exec(`INSERT INTO insurance_premium_payments(policy_id,customer_id,account_id,amount_minor,currency,status,reference,created_by,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, policyID, customerID, accountID, amount, currency, "COMPLETED", ref, u.Username, now)
		if err != nil {
			return nil, err
		}
		id, _ := res.LastInsertId()
		if err = Notify(tx, customerID, "Prim tahsil edildi", "Sigorta primi hesabınızdan tahsil edildi.", ref); err != nil {
			return nil, err
		}
		return Row(tx, `SELECT * FROM insurance_premium_payments WHERE id=?`, id)
	default:
		return nil, fmt.Errorf("OPERATION: Desteklenmeyen sigorta işlemi: %s", op)
	}
}
