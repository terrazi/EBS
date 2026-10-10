package sim

import (
	"database/sql"
	"fmt"
	"strings"
)

func ensureCreditSchema(tx *sql.Tx) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS credit_applications (
			id INTEGER PRIMARY KEY AUTOINCREMENT, customer_id INTEGER NOT NULL, product TEXT NOT NULL,
			status TEXT NOT NULL, principal_minor INTEGER NOT NULL, currency TEXT NOT NULL DEFAULT 'TRY',
			term_months INTEGER NOT NULL, income_minor INTEGER NOT NULL DEFAULT 0, reference TEXT NOT NULL UNIQUE,
			created_by TEXT NOT NULL, assessed_by TEXT, approved_by TEXT, approved_limit_minor INTEGER NOT NULL DEFAULT 0,
			account_id INTEGER, score INTEGER, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS credit_loans (
			id INTEGER PRIMARY KEY AUTOINCREMENT, application_id INTEGER NOT NULL, customer_id INTEGER NOT NULL,
			account_id INTEGER NOT NULL, principal_minor INTEGER NOT NULL, outstanding_minor INTEGER NOT NULL,
			currency TEXT NOT NULL, status TEXT NOT NULL, reference TEXT NOT NULL UNIQUE,
			created_by TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS credit_installments (
			id INTEGER PRIMARY KEY AUTOINCREMENT, loan_id INTEGER NOT NULL, application_id INTEGER NOT NULL,
			customer_id INTEGER NOT NULL, due_no INTEGER NOT NULL, due_date TEXT NOT NULL,
			principal_minor INTEGER NOT NULL, interest_minor INTEGER NOT NULL, total_minor INTEGER NOT NULL,
			paid_minor INTEGER NOT NULL DEFAULT 0, status TEXT NOT NULL, paid_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS credit_payments (
			id INTEGER PRIMARY KEY AUTOINCREMENT, loan_id INTEGER NOT NULL, installment_id INTEGER,
			customer_id INTEGER NOT NULL, account_id INTEGER NOT NULL, amount_minor INTEGER NOT NULL,
			kind TEXT NOT NULL, status TEXT NOT NULL, reference TEXT NOT NULL UNIQUE,
			created_by TEXT NOT NULL, created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS credit_restructures (
			id INTEGER PRIMARY KEY AUTOINCREMENT, loan_id INTEGER NOT NULL, customer_id INTEGER NOT NULL,
			old_term_months INTEGER NOT NULL, new_term_months INTEGER NOT NULL, status TEXT NOT NULL,
			reason TEXT NOT NULL DEFAULT '', reference TEXT NOT NULL UNIQUE, created_by TEXT NOT NULL, created_at TEXT NOT NULL
		)`,
	}
	for _, stmt := range stmts {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func creditRef(p Payload, prefix string) string {
	if r := Ref(p); r != "" {
		return r
	}
	return fmt.Sprintf("%s-%s", prefix, strings.ReplaceAll(strings.ReplaceAll(Now(), ":", ""), ".", ""))
}

func creditStaff(u User) error {
	if err := Staff(u); err != nil {
		return err
	}
	if err := Allowed(u, "credit"); err == nil {
		return nil
	}
	return Allowed(u, "banking")
}

func creditApplication(tx *sql.Tx, u User, id int64) (Result, error) {
	row, err := Row(tx, `SELECT * FROM credit_applications WHERE id=?`, id)
	if err != nil {
		return nil, err
	}
	if u.Role == "CUSTOMER" && ID(row, "customer_id") != u.CustomerID {
		return nil, ErrForbidden
	}
	return row, nil
}

func creditLoan(tx *sql.Tx, u User, id int64) (Result, error) {
	row, err := Row(tx, `SELECT * FROM credit_loans WHERE id=?`, id)
	if err != nil {
		return nil, err
	}
	if u.Role == "CUSTOMER" && ID(row, "customer_id") != u.CustomerID {
		return nil, ErrForbidden
	}
	return row, nil
}

// Credit executes the credit application, assessment, approval, disbursement,
// collection and restructuring operations.
func Credit(tx *sql.Tx, u User, op string, p Payload) (Result, error) {
	if err := ensureCreditSchema(tx); err != nil {
		return nil, err
	}
	op = strings.ToLower(strings.TrimSpace(op))
	if strings.HasPrefix(op, "credit.") {
		op = strings.TrimPrefix(op, "credit.")
	}
	now := Now()
	ref := creditRef(p, "CRD")

	switch op {
	case "apply":
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
		if err := KYC(tx, u, customerID); err != nil {
			return nil, err
		}
		if err := RequireDocument(tx, u, p, customerID, "credit_application"); err != nil {
			return nil, err
		}
		product := Str(p, "product")
		if product == "" {
			product = "İhtiyaç Kredisi"
		}
		principal, err := Amount(p, "principal_minor")
		if err != nil {
			return nil, err
		}
		term := Int(p, "term_months")
		if term < 1 || term > 360 {
			return nil, fmt.Errorf("TERM: Vade 1 ile 360 ay arasında olmalıdır.")
		}
		currency := Str(p, "currency")
		if currency == "" {
			currency = "TRY"
		}
		income := Int(p, "income_minor")
		res, err := tx.Exec(`INSERT INTO credit_applications(customer_id,product,status,principal_minor,currency,term_months,income_minor,reference,created_by,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, customerID, product, "SUBMITTED", principal, currency, term, income, ref, u.Username, now, now)
		if err != nil {
			return nil, err
		}
		id, _ := res.LastInsertId()
		if u.Role == "CUSTOMER" {
			if err = Notify(tx, customerID, "Kredi başvurusu alındı", "Başvurunuz değerlendirme kuyruğuna alındı.", ref); err != nil {
				return nil, err
			}
		}
		return Row(tx, `SELECT * FROM credit_applications WHERE id=?`, id)

	case "assess":
		if err := creditStaff(u); err != nil {
			return nil, err
		}
		id := Int(p, "application_id")
		app, err := creditApplication(tx, u, id)
		if err != nil {
			return nil, err
		}
		if Text(app, "status") != "SUBMITTED" {
			return nil, fmt.Errorf("STATE: Başvuru değerlendirmeye uygun değil.")
		}
		score := Int(p, "score")
		if score == 0 {
			score = 70
		}
		if score < 0 || score > 100 {
			return nil, fmt.Errorf("SCORE: Değerlendirme puanı 0-100 arasında olmalıdır.")
		}
		if _, err = tx.Exec(`UPDATE credit_applications SET status='ASSESSED',score=?,assessed_by=?,updated_at=? WHERE id=? AND status='SUBMITTED'`, score, u.Username, now, id); err != nil {
			return nil, err
		}
		return Row(tx, `SELECT * FROM credit_applications WHERE id=?`, id)

	case "approve":
		if err := creditStaff(u); err != nil {
			return nil, err
		}
		id := Int(p, "application_id")
		app, err := creditApplication(tx, u, id)
		if err != nil {
			return nil, err
		}
		if Text(app, "status") != "ASSESSED" {
			return nil, fmt.Errorf("STATE: Başvuru onay kararına uygun değil.")
		}
		if Text(app, "created_by") == u.Username {
			return nil, fmt.Errorf("APPROVAL: İşlemi başlatan kişi kendi başvurusunu onaylayamaz.")
		}
		limit := Int(p, "approval_limit_minor")
		amount := ID(app, "principal_minor")
		if limit > 0 && amount > limit {
			if _, err = Queue(tx, "CREDIT_APPROVAL", u, ID(app, "customer_id"), Payload{"application_id": id, "reference": ref, "amount_minor": amount}); err != nil {
				return nil, err
			}
			if _, err = tx.Exec(`UPDATE credit_applications SET status='PENDING_APPROVAL',updated_at=? WHERE id=?`, now, id); err != nil {
				return nil, err
			}
			app["status"] = "PENDING_APPROVAL"
			return app, nil
		}
		if _, err = tx.Exec(`UPDATE credit_applications SET status='APPROVED',approved_by=?,approved_limit_minor=?,updated_at=? WHERE id=? AND status='ASSESSED'`, u.Username, amount, now, id); err != nil {
			return nil, err
		}
		return Row(tx, `SELECT * FROM credit_applications WHERE id=?`, id)

	case "disburse":
		if err := creditStaff(u); err != nil {
			return nil, err
		}
		app, err := creditApplication(tx, u, Int(p, "application_id"))
		if err != nil {
			return nil, err
		}
		if Text(app, "status") != "APPROVED" {
			return nil, fmt.Errorf("STATE: Kredi kullandırım için onaylanmamış başvuru.")
		}
		accountID := Int(p, "account_id")
		account, err := Account(tx, u, accountID)
		if err != nil {
			return nil, err
		}
		customerID := ID(app, "customer_id")
		if ID(account, "customer_id") != customerID {
			return nil, ErrForbidden
		}
		if Text(account, "currency") != Text(app, "currency") {
			return nil, fmt.Errorf("CURRENCY: Kredi ve hesap para birimi aynı olmalı.")
		}
		principal := ID(app, "principal_minor")
		loanRef := creditRef(p, "LON")
		res, err := tx.Exec(`INSERT INTO credit_loans(application_id,customer_id,account_id,principal_minor,outstanding_minor,currency,status,reference,created_by,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, ID(app, "id"), customerID, accountID, principal, principal, Text(app, "currency"), "ACTIVE", loanRef, u.Username, now, now)
		if err != nil {
			return nil, err
		}
		loanID, _ := res.LastInsertId()
		term := ID(app, "term_months")
		interest := principal / 10
		total := principal + interest
		base := total / term
		rem := total - (base * term)
		for n := int64(1); n <= term; n++ {
			part := base
			if n == term {
				part += rem
			}
			partInterest := interest / term
			if n == term {
				partInterest += interest - (interest/term)*term
			}
			partPrincipal := part - partInterest
			if _, err = tx.Exec(`INSERT INTO credit_installments(loan_id,application_id,customer_id,due_no,due_date,principal_minor,interest_minor,total_minor,paid_minor,status) VALUES(?,?,?,?,?,?,?,?,?,?)`, loanID, ID(app, "id"), customerID, n, fmt.Sprintf("%s-%02d", now[:7], n), partPrincipal, partInterest, part, 0, "OPEN"); err != nil {
				return nil, err
			}
		}
		if err = Journal(tx, loanRef, fmt.Sprintf("LOAN:%d", loanID), fmt.Sprintf("ACCOUNT:%d", accountID), principal, Text(app, "currency")); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(`UPDATE credit_applications SET status='DISBURSED',account_id=?,updated_at=? WHERE id=?`, accountID, now, ID(app, "id")); err != nil {
			return nil, err
		}
		if err = Balance(tx, accountID, principal); err != nil {
			return nil, err
		}
		if err = Notify(tx, customerID, "Kredi kullandırıldı", "Kredi tutarı hesabınıza aktarıldı.", loanRef); err != nil {
			return nil, err
		}
		return Row(tx, `SELECT * FROM credit_loans WHERE id=?`, loanID)

	case "installment", "partial_payment":
		if u.Role == "CUSTOMER" && u.CustomerID == 0 {
			return nil, ErrForbidden
		}
		loanID := Int(p, "loan_id")
		loan, err := creditLoan(tx, u, loanID)
		if err != nil {
			return nil, err
		}
		if Text(loan, "status") != "ACTIVE" {
			return nil, fmt.Errorf("STATE: Kredi ödeme yapılabilir durumda değil.")
		}
		accountID := Int(p, "account_id")
		if accountID == 0 {
			accountID = ID(loan, "account_id")
		}
		account, err := Account(tx, u, accountID)
		if err != nil {
			return nil, err
		}
		if ID(account, "customer_id") != ID(loan, "customer_id") {
			return nil, ErrForbidden
		}
		amount, err := Amount(p, "amount_minor")
		if err != nil {
			return nil, err
		}
		instID := Int(p, "installment_id")
		var inst Result
		if instID != 0 {
			inst, err = Row(tx, `SELECT * FROM credit_installments WHERE id=? AND loan_id=?`, instID, loanID)
		} else {
			inst, err = Row(tx, `SELECT * FROM credit_installments WHERE loan_id=? AND status IN ('OPEN','PARTIAL') ORDER BY due_no LIMIT 1`, loanID)
		}
		if err != nil {
			return nil, err
		}
		remaining := ID(inst, "total_minor") - ID(inst, "paid_minor")
		if amount > remaining {
			return nil, fmt.Errorf("AMOUNT: Taksit kalanından fazla ödeme yapılamaz.")
		}
		if err = Balance(tx, accountID, -amount); err != nil {
			return nil, err
		}
		paymentRef := creditRef(p, "PAY")
		if err = Journal(tx, paymentRef, fmt.Sprintf("ACCOUNT:%d", accountID), fmt.Sprintf("LOAN:%d", loanID), amount, Text(loan, "currency")); err != nil {
			return nil, err
		}
		paid := ID(inst, "paid_minor") + amount
		status := "PARTIAL"
		if paid == ID(inst, "total_minor") {
			status = "PAID"
		}
		if _, err = tx.Exec(`UPDATE credit_installments SET paid_minor=?,status=?,paid_at=? WHERE id=?`, paid, status, now, ID(inst, "id")); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(`UPDATE credit_loans SET outstanding_minor=CASE WHEN outstanding_minor>=? THEN outstanding_minor-? ELSE 0 END,updated_at=? WHERE id=?`, amount, amount, now, loanID); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(`INSERT INTO credit_payments(loan_id,installment_id,customer_id,account_id,amount_minor,kind,status,reference,created_by,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, loanID, ID(inst, "id"), ID(loan, "customer_id"), accountID, amount, op, "COMPLETED", paymentRef, u.Username, now); err != nil {
			return nil, err
		}
		if err = Notify(tx, ID(loan, "customer_id"), "Kredi ödemesi alındı", "Kredi ödemeniz işlendi.", paymentRef); err != nil {
			return nil, err
		}
		return Row(tx, `SELECT * FROM credit_loans WHERE id=?`, loanID)

	case "early_close":
		loan, err := creditLoan(tx, u, Int(p, "loan_id"))
		if err != nil {
			return nil, err
		}
		if Text(loan, "status") != "ACTIVE" {
			return nil, fmt.Errorf("STATE: Kredi açık değil.")
		}
		accountID := Int(p, "account_id")
		if accountID == 0 {
			accountID = ID(loan, "account_id")
		}
		account, err := Account(tx, u, accountID)
		if err != nil {
			return nil, err
		}
		if ID(account, "customer_id") != ID(loan, "customer_id") {
			return nil, ErrForbidden
		}
		amount := ID(loan, "outstanding_minor")
		if amount <= 0 {
			return nil, fmt.Errorf("STATE: Kredi bakiyesi zaten kapalı.")
		}
		if err = Balance(tx, accountID, -amount); err != nil {
			return nil, err
		}
		paymentRef := creditRef(p, "CLOSE")
		if err = Journal(tx, paymentRef, fmt.Sprintf("ACCOUNT:%d", accountID), fmt.Sprintf("LOAN:%d", ID(loan, "id")), amount, Text(loan, "currency")); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(`UPDATE credit_loans SET outstanding_minor=0,status='CLOSED',updated_at=? WHERE id=?`, now, ID(loan, "id")); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(`UPDATE credit_installments SET status='PAID',paid_minor=total_minor,paid_at=? WHERE loan_id=? AND status IN ('OPEN','PARTIAL')`, now, ID(loan, "id")); err != nil {
			return nil, err
		}
		if err = Notify(tx, ID(loan, "customer_id"), "Kredi erken kapatıldı", "Krediniz erken kapama ile kapatıldı.", paymentRef); err != nil {
			return nil, err
		}
		loan["outstanding_minor"] = int64(0)
		loan["status"] = "CLOSED"
		return loan, nil

	case "restructure":
		loan, err := creditLoan(tx, u, Int(p, "loan_id"))
		if err != nil {
			return nil, err
		}
		if Text(loan, "status") != "ACTIVE" {
			return nil, fmt.Errorf("STATE: Yalnız aktif kredi yapılandırılabilir.")
		}
		if err := creditStaff(u); err != nil {
			return nil, err
		}
		newTerm := Int(p, "new_term_months")
		if newTerm < 1 || newTerm > 360 {
			return nil, fmt.Errorf("TERM: Yeni vade 1 ile 360 ay arasında olmalıdır.")
		}
		app, err := Row(tx, `SELECT * FROM credit_applications WHERE id=?`, ID(loan, "application_id"))
		if err != nil {
			return nil, err
		}
		oldTerm := ID(app, "term_months")
		restructureRef := creditRef(p, "RST")
		if _, err = tx.Exec(`INSERT INTO credit_restructures(loan_id,customer_id,old_term_months,new_term_months,status,reason,reference,created_by,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, ID(loan, "id"), ID(loan, "customer_id"), oldTerm, newTerm, "PENDING_APPROVAL", Str(p, "reason"), restructureRef, u.Username, now); err != nil {
			return nil, err
		}
		if _, err = Queue(tx, "CREDIT_RESTRUCTURE", u, ID(loan, "customer_id"), Payload{"loan_id": ID(loan, "id"), "new_term_months": newTerm, "reference": restructureRef}); err != nil {
			return nil, err
		}
		return Result{"loan_id": ID(loan, "id"), "status": "PENDING_APPROVAL", "reference": restructureRef}, nil
	default:
		return nil, fmt.Errorf("OPERATION: Desteklenmeyen kredi işlemi: %s", op)
	}
}
