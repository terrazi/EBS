package sim

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
)

func ensureCardsSchema(tx *sql.Tx) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS card_applications (
			id INTEGER PRIMARY KEY AUTOINCREMENT, customer_id INTEGER NOT NULL, card_type TEXT NOT NULL,
			status TEXT NOT NULL, limit_minor INTEGER NOT NULL, account_id INTEGER, delivery TEXT NOT NULL DEFAULT '',
			reference TEXT NOT NULL UNIQUE, created_by TEXT NOT NULL, evaluated_by TEXT, approved_by TEXT,
			created_at TEXT NOT NULL, updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS cards (
			id INTEGER PRIMARY KEY AUTOINCREMENT, customer_id INTEGER NOT NULL, application_id INTEGER,
			card_type TEXT NOT NULL, status TEXT NOT NULL, limit_minor INTEGER NOT NULL DEFAULT 0,
			account_id INTEGER, masked_number TEXT NOT NULL, pin_hash TEXT NOT NULL DEFAULT '',
			channel_json TEXT NOT NULL DEFAULT '{}', reference TEXT NOT NULL UNIQUE, created_by TEXT NOT NULL,
			created_at TEXT NOT NULL, updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS card_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT, card_id INTEGER NOT NULL, customer_id INTEGER NOT NULL,
			action TEXT NOT NULL, status TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '',
			reference TEXT NOT NULL, actor TEXT NOT NULL, created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS card_payments (
			id INTEGER PRIMARY KEY AUTOINCREMENT, card_id INTEGER NOT NULL, customer_id INTEGER NOT NULL,
			account_id INTEGER NOT NULL, amount_minor INTEGER NOT NULL, currency TEXT NOT NULL,
			status TEXT NOT NULL, reference TEXT NOT NULL UNIQUE, created_by TEXT NOT NULL, created_at TEXT NOT NULL
		)`,
	}
	for _, stmt := range stmts {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	// A pre-v3 database may already contain a compact cards table. Additive
	// columns keep that data usable while the new workflows use the same table.
	for col, typ := range map[string]string{
		"application_id": "INTEGER", "account_id": "INTEGER", "pin_hash": "TEXT NOT NULL DEFAULT ''",
		"channel_json": "TEXT NOT NULL DEFAULT '{}'", "reference": "TEXT", "created_by": "TEXT NOT NULL DEFAULT ''",
		"created_at": "TEXT NOT NULL DEFAULT ''", "updated_at": "TEXT NOT NULL DEFAULT ''",
	} {
		if err := addColumnIfMissing(tx, "cards", col, typ); err != nil {
			return err
		}
	}
	return nil
}

func addColumnIfMissing(tx *sql.Tx, table, column, typ string) error {
	rows, err := tx.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt any
		if err = rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == column {
			found = true
			break
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if found {
		return nil
	}
	_, err = tx.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + column + ` ` + typ)
	return err
}

func cardRef(p Payload, prefix string) string {
	if r := Ref(p); r != "" {
		return r
	}
	return fmt.Sprintf("%s-%s", prefix, strings.ReplaceAll(strings.ReplaceAll(Now(), ":", ""), ".", ""))
}

func cardStaff(u User) error {
	if err := Staff(u); err != nil {
		return err
	}
	if err := Allowed(u, "cards"); err == nil {
		return nil
	}
	return Allowed(u, "banking")
}

func cardApplication(tx *sql.Tx, u User, id int64) (Result, error) {
	r, err := Row(tx, `SELECT * FROM card_applications WHERE id=?`, id)
	if err != nil {
		return nil, err
	}
	if u.Role == "CUSTOMER" && ID(r, "customer_id") != u.CustomerID {
		return nil, ErrForbidden
	}
	return r, nil
}

func cardRow(tx *sql.Tx, u User, id int64) (Result, error) {
	r, err := Row(tx, `SELECT * FROM cards WHERE id=?`, id)
	if err != nil {
		return nil, err
	}
	if u.Role == "CUSTOMER" && ID(r, "customer_id") != u.CustomerID {
		return nil, ErrForbidden
	}
	return r, nil
}

func cardEvent(tx *sql.Tx, cardID, customerID int64, action, status, reason, ref string, u User) error {
	_, err := tx.Exec(`INSERT INTO card_events(card_id,customer_id,action,status,reason,reference,actor,created_at) VALUES(?,?,?,?,?,?,?,?)`, cardID, customerID, action, status, reason, ref, u.Username, Now())
	return err
}

// Cards executes card.apply/evaluate/approve/activate/block/lost/pin/limit/
// renew/payment. PIN values are never persisted; only a SHA-256 digest is
// retained for this training simulation.
func Cards(tx *sql.Tx, u User, op string, p Payload) (Result, error) {
	if err := ensureCardsSchema(tx); err != nil {
		return nil, err
	}
	op = strings.ToLower(strings.TrimSpace(op))
	if strings.HasPrefix(op, "card.") {
		op = strings.TrimPrefix(op, "card.")
	}
	now := Now()
	ref := cardRef(p, "CARD")

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
		cardType := Str(p, "card_type")
		if cardType == "" {
			cardType = "Banka Kartı"
		}
		limit, err := Amount(p, "limit_minor")
		if err != nil {
			return nil, err
		}
		res, err := tx.Exec(`INSERT INTO card_applications(customer_id,card_type,status,limit_minor,account_id,delivery,reference,created_by,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, customerID, cardType, "SUBMITTED", limit, Int(p, "account_id"), Str(p, "delivery"), ref, u.Username, now, now)
		if err != nil {
			return nil, err
		}
		id, _ := res.LastInsertId()
		return Row(tx, `SELECT * FROM card_applications WHERE id=?`, id)

	case "evaluate":
		if err := cardStaff(u); err != nil {
			return nil, err
		}
		id := Int(p, "application_id")
		app, err := cardApplication(tx, u, id)
		if err != nil {
			return nil, err
		}
		if Text(app, "status") != "SUBMITTED" {
			return nil, fmt.Errorf("STATE: Kart başvurusu değerlendirmeye uygun değil.")
		}
		if _, err = tx.Exec(`UPDATE card_applications SET status='EVALUATED',evaluated_by=?,updated_at=? WHERE id=? AND status='SUBMITTED'`, u.Username, now, id); err != nil {
			return nil, err
		}
		return Row(tx, `SELECT * FROM card_applications WHERE id=?`, id)

	case "approve":
		if err := cardStaff(u); err != nil {
			return nil, err
		}
		id := Int(p, "application_id")
		app, err := cardApplication(tx, u, id)
		if err != nil {
			return nil, err
		}
		if Text(app, "status") != "EVALUATED" {
			return nil, fmt.Errorf("STATE: Kart başvurusu onay kararına uygun değil.")
		}
		if Text(app, "created_by") == u.Username {
			return nil, fmt.Errorf("APPROVAL: Başvuruyu yapan kişi aynı başvuruyu onaylayamaz.")
		}
		if _, err = tx.Exec(`UPDATE card_applications SET status='APPROVED',approved_by=?,updated_at=? WHERE id=? AND status='EVALUATED'`, u.Username, now, id); err != nil {
			return nil, err
		}
		return Row(tx, `SELECT * FROM card_applications WHERE id=?`, id)

	case "activate":
		id := Int(p, "application_id")
		app, err := cardApplication(tx, u, id)
		if err != nil {
			return nil, err
		}
		if Text(app, "status") != "APPROVED" {
			return nil, fmt.Errorf("STATE: Kart başvurusu aktivasyona uygun değil.")
		}
		if u.Role != "CUSTOMER" {
			if err = cardStaff(u); err != nil {
				return nil, err
			}
		}
		customerID := ID(app, "customer_id")
		masked := Str(p, "masked_number")
		if masked == "" {
			masked = fmt.Sprintf("**** **** **** %04d", (id%9000)+1000)
		}
		accountID := ID(app, "account_id")
		if accountID != 0 {
			if _, err = Account(tx, u, accountID); err != nil {
				return nil, err
			}
		}
		res, err := tx.Exec(`INSERT INTO cards(customer_id,application_id,card_type,status,limit_minor,account_id,masked_number,pin_hash,channel_json,reference,created_by,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, customerID, id, Text(app, "card_type"), "ACTIVE", ID(app, "limit_minor"), accountID, masked, "", "{}", ref, u.Username, now, now)
		if err != nil {
			return nil, err
		}
		cardID, _ := res.LastInsertId()
		if _, err = tx.Exec(`UPDATE card_applications SET status='ACTIVATED',updated_at=? WHERE id=?`, now, id); err != nil {
			return nil, err
		}
		if err = cardEvent(tx, cardID, customerID, "ACTIVATE", "ACTIVE", "", ref, u); err != nil {
			return nil, err
		}
		if err = Notify(tx, customerID, "Kart aktifleştirildi", "Kartınız kullanıma açıldı.", ref); err != nil {
			return nil, err
		}
		return Row(tx, `SELECT * FROM cards WHERE id=?`, cardID)

	case "block", "lost":
		cardID := Int(p, "card_id")
		card, err := cardRow(tx, u, cardID)
		if err != nil {
			return nil, err
		}
		if Text(card, "status") != "ACTIVE" {
			return nil, fmt.Errorf("STATE: Kart zaten aktif değil.")
		}
		status := "BLOCKED"
		if op == "lost" {
			status = "LOST"
		}
		if u.Role != "CUSTOMER" {
			if err = cardStaff(u); err != nil {
				return nil, err
			}
		}
		if _, err = tx.Exec(`UPDATE cards SET status=?,updated_at=? WHERE id=? AND status='ACTIVE'`, status, now, cardID); err != nil {
			return nil, err
		}
		if err = cardEvent(tx, cardID, ID(card, "customer_id"), strings.ToUpper(op), status, Str(p, "reason"), ref, u); err != nil {
			return nil, err
		}
		card["status"] = status
		return card, nil

	case "pin":
		cardID := Int(p, "card_id")
		card, err := cardRow(tx, u, cardID)
		if err != nil {
			return nil, err
		}
		pin := Str(p, "pin")
		if len(pin) != 4 && len(pin) != 6 {
			return nil, fmt.Errorf("PIN: PIN 4 veya 6 haneli olmalıdır.")
		}
		for _, r := range pin {
			if r < '0' || r > '9' {
				return nil, fmt.Errorf("PIN: PIN yalnızca rakamlardan oluşmalıdır.")
			}
		}
		digest := sha256.Sum256([]byte(pin))
		if _, err = tx.Exec(`UPDATE cards SET pin_hash=?,updated_at=? WHERE id=? AND status IN ('ACTIVE','BLOCKED','LOST')`, hex.EncodeToString(digest[:]), now, cardID); err != nil {
			return nil, err
		}
		if err = cardEvent(tx, cardID, ID(card, "customer_id"), "PIN", "PIN_SET", "", ref, u); err != nil {
			return nil, err
		}
		card["pin_hash"] = hex.EncodeToString(digest[:])
		return card, nil

	case "limit":
		cardID := Int(p, "card_id")
		card, err := cardRow(tx, u, cardID)
		if err != nil {
			return nil, err
		}
		newLimit, err := Amount(p, "limit_minor")
		if err != nil {
			return nil, err
		}
		customerID := ID(card, "customer_id")
		if u.Role == "CUSTOMER" {
			if _, err = Queue(tx, "CARD_LIMIT_CHANGE", u, customerID, Payload{"card_id": cardID, "limit_minor": newLimit, "reference": ref}); err != nil {
				return nil, err
			}
			return Result{"card_id": cardID, "status": "PENDING", "limit_minor": newLimit, "reference": ref}, nil
		}
		if err = cardStaff(u); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(`UPDATE cards SET limit_minor=?,updated_at=? WHERE id=?`, newLimit, now, cardID); err != nil {
			return nil, err
		}
		if err = cardEvent(tx, cardID, customerID, "LIMIT", "UPDATED", "", ref, u); err != nil {
			return nil, err
		}
		card["limit_minor"] = newLimit
		return card, nil

	case "renew":
		if err := cardStaff(u); err != nil {
			return nil, err
		}
		old, err := cardRow(tx, u, Int(p, "card_id"))
		if err != nil {
			return nil, err
		}
		if Text(old, "status") != "ACTIVE" && Text(old, "status") != "EXPIRED" {
			return nil, fmt.Errorf("STATE: Kart yenilenebilir durumda değil.")
		}
		if _, err = tx.Exec(`UPDATE cards SET status='RENEWED',updated_at=? WHERE id=?`, now, ID(old, "id")); err != nil {
			return nil, err
		}
		newRef := cardRef(p, "CARD-RENEW")
		res, err := tx.Exec(`INSERT INTO cards(customer_id,application_id,card_type,status,limit_minor,account_id,masked_number,pin_hash,channel_json,reference,created_by,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, ID(old, "customer_id"), ID(old, "application_id"), Text(old, "card_type"), "ACTIVE", ID(old, "limit_minor"), ID(old, "account_id"), Text(old, "masked_number"), "", Text(old, "channel_json"), newRef, u.Username, now, now)
		if err != nil {
			return nil, err
		}
		id, _ := res.LastInsertId()
		if err = cardEvent(tx, ID(old, "id"), ID(old, "customer_id"), "RENEW", "RENEWED", "", newRef, u); err != nil {
			return nil, err
		}
		return Row(tx, `SELECT * FROM cards WHERE id=?`, id)

	case "payment":
		card, err := cardRow(tx, u, Int(p, "card_id"))
		if err != nil {
			return nil, err
		}
		if Text(card, "status") != "ACTIVE" && Text(card, "status") != "BLOCKED" {
			return nil, fmt.Errorf("STATE: Kart ödemesi yapılamaz.")
		}
		accountID := Int(p, "account_id")
		account, err := Account(tx, u, accountID)
		if err != nil {
			return nil, err
		}
		if ID(account, "customer_id") != ID(card, "customer_id") {
			return nil, ErrForbidden
		}
		amount, err := Amount(p, "amount_minor")
		if err != nil {
			return nil, err
		}
		currency := Text(account, "currency")
		if err = Balance(tx, accountID, -amount); err != nil {
			return nil, err
		}
		if err = Journal(tx, ref, fmt.Sprintf("ACCOUNT:%d", accountID), fmt.Sprintf("CARD_SETTLEMENT:%d", ID(card, "id")), amount, currency); err != nil {
			return nil, err
		}
		res, err := tx.Exec(`INSERT INTO card_payments(card_id,customer_id,account_id,amount_minor,currency,status,reference,created_by,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, ID(card, "id"), ID(card, "customer_id"), accountID, amount, currency, "COMPLETED", ref, u.Username, now)
		if err != nil {
			return nil, err
		}
		id, _ := res.LastInsertId()
		if err = Notify(tx, ID(card, "customer_id"), "Kart borcu ödendi", "Kart ödemeniz işlendi.", ref); err != nil {
			return nil, err
		}
		return Row(tx, `SELECT * FROM card_payments WHERE id=?`, id)
	default:
		return nil, fmt.Errorf("OPERATION: Desteklenmeyen kart işlemi: %s", op)
	}
}
