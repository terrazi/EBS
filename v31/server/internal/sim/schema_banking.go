package sim

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
)

// bankingSchema contains only the tables owned by the banking vertical. The
// root service creates the shared transaction, ledger, queue, notification and
// session tables before running this schema. All amounts are minor units; no
// floating point amount is stored in a financial table.
const bankingSchema = `
CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    salt TEXT NOT NULL,
    display_name TEXT NOT NULL,
    role TEXT NOT NULL,
    department TEXT NOT NULL DEFAULT '',
    customer_id INTEGER,
    permissions_json TEXT NOT NULL DEFAULT '[]',
    active INTEGER NOT NULL DEFAULT 1 CHECK(active IN (0,1))
);
CREATE INDEX IF NOT EXISTS idx_users_customer ON users(customer_id);

CREATE TABLE IF NOT EXISTS customers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    code TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    demo_identity TEXT NOT NULL UNIQUE,
    segment TEXT NOT NULL,
    phone TEXT NOT NULL,
    email TEXT NOT NULL,
    city TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'ACTIVE',
    kyc_status TEXT NOT NULL DEFAULT 'VERIFIED'
);
CREATE INDEX IF NOT EXISTS idx_customers_name ON customers(name);

CREATE TABLE IF NOT EXISTS accounts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    customer_id INTEGER NOT NULL REFERENCES customers(id),
    iban TEXT NOT NULL UNIQUE,
    account_type TEXT NOT NULL,
    currency TEXT NOT NULL CHECK(currency IN ('TRY','USD','EUR','XAU')),
    balance_minor INTEGER NOT NULL DEFAULT 0 CHECK(balance_minor >= 0),
    status TEXT NOT NULL DEFAULT 'ACTIVE',
    opened_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_accounts_customer ON accounts(customer_id);

CREATE TABLE IF NOT EXISTS cash_registers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    branch TEXT NOT NULL,
    currency TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'CLOSED',
    balance_minor INTEGER NOT NULL DEFAULT 0 CHECK(balance_minor >= 0),
    opened_by TEXT,
    opened_at TEXT,
    closed_at TEXT
);

CREATE TABLE IF NOT EXISTS banking_transfers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    reference TEXT NOT NULL UNIQUE,
    customer_id INTEGER NOT NULL REFERENCES customers(id),
    source_account_id INTEGER NOT NULL REFERENCES accounts(id),
    target_account_id INTEGER NOT NULL REFERENCES accounts(id),
    amount_minor INTEGER NOT NULL CHECK(amount_minor > 0),
    currency TEXT NOT NULL,
    transfer_type TEXT NOT NULL,
    status TEXT NOT NULL,
    created_by TEXT NOT NULL,
    created_at TEXT NOT NULL,
    completed_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_banking_transfers_customer ON banking_transfers(customer_id, id);

CREATE TABLE IF NOT EXISTS payments (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    reference TEXT NOT NULL UNIQUE,
    customer_id INTEGER NOT NULL REFERENCES customers(id),
    account_id INTEGER NOT NULL REFERENCES accounts(id),
    institution TEXT NOT NULL,
    amount_minor INTEGER NOT NULL CHECK(amount_minor > 0),
    currency TEXT NOT NULL,
    kind TEXT NOT NULL,
    status TEXT NOT NULL,
    created_by TEXT NOT NULL,
    created_at TEXT NOT NULL,
    completed_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_payments_customer ON payments(customer_id, id);

CREATE TABLE IF NOT EXISTS autopays (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    customer_id INTEGER NOT NULL REFERENCES customers(id),
    account_id INTEGER NOT NULL REFERENCES accounts(id),
    institution TEXT NOT NULL,
    amount_minor INTEGER NOT NULL CHECK(amount_minor > 0),
    currency TEXT NOT NULL,
    due_day INTEGER NOT NULL CHECK(due_day BETWEEN 1 AND 31),
    status TEXT NOT NULL DEFAULT 'ACTIVE',
    created_by TEXT NOT NULL,
    created_at TEXT NOT NULL,
    canceled_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_autopays_customer ON autopays(customer_id, status);

CREATE TABLE IF NOT EXISTS easy_addresses (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    address TEXT NOT NULL UNIQUE,
    customer_id INTEGER NOT NULL REFERENCES customers(id),
    account_id INTEGER NOT NULL REFERENCES accounts(id),
    status TEXT NOT NULL DEFAULT 'ACTIVE',
    created_at TEXT NOT NULL,
    canceled_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_easy_addresses_customer ON easy_addresses(customer_id);

CREATE TABLE IF NOT EXISTS payment_requests (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    reference TEXT NOT NULL UNIQUE,
    requester_customer_id INTEGER NOT NULL REFERENCES customers(id),
    requester_account_id INTEGER NOT NULL REFERENCES accounts(id),
    payer_customer_id INTEGER NOT NULL REFERENCES customers(id),
    payer_account_id INTEGER NOT NULL REFERENCES accounts(id),
    amount_minor INTEGER NOT NULL CHECK(amount_minor > 0),
    currency TEXT NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'PENDING',
    created_by TEXT NOT NULL,
    created_at TEXT NOT NULL,
    completed_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_payment_requests_requester ON payment_requests(requester_customer_id, id);
CREATE INDEX IF NOT EXISTS idx_payment_requests_payer ON payment_requests(payer_customer_id, id);

CREATE TABLE IF NOT EXISTS fx_rates (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    base_currency TEXT NOT NULL,
    quote_currency TEXT NOT NULL,
    rate TEXT NOT NULL,
    as_of TEXT NOT NULL,
    UNIQUE(base_currency, quote_currency)
);

CREATE TABLE IF NOT EXISTS term_deposits (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    reference TEXT NOT NULL UNIQUE,
    customer_id INTEGER NOT NULL REFERENCES customers(id),
    source_account_id INTEGER NOT NULL REFERENCES accounts(id),
    principal_minor INTEGER NOT NULL CHECK(principal_minor > 0),
    currency TEXT NOT NULL,
    maturity_date TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'ACTIVE',
    created_by TEXT NOT NULL,
    created_at TEXT NOT NULL,
    closed_at TEXT
);
`

// demoSalt is deterministic only for the bundled training identities. It
// keeps a reset reproducible while still ensuring that passwords are never
// stored as clear text. Production identities should receive cryptographic
// random salts when they are created.
func demoSalt(username string) string {
	s := sha256.Sum256([]byte("EBS-v3.1-demo-salt:" + strings.ToLower(username)))
	return hex.EncodeToString(s[:16])
}

func seedBanking(tx *sql.Tx) error {
	var n int
	if err := tx.QueryRow("SELECT COUNT(*) FROM customers").Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		customers := []struct {
			name, segment, city string
		}{
			{"Ahmet Karaca", "Maaş / mevduat", "İstanbul"}, {"Selin Yılmaz", "Maaş / mevduat", "Ankara"},
			{"Mehmet Aksoy", "Maaş / mevduat", "İzmir"}, {"Zeynep Demir", "Maaş / mevduat", "Bursa"},
			{"Emre Kaya", "Maaş / mevduat", "Antalya"}, {"Elif Şahin", "Döviz / altın", "İstanbul"},
			{"Murat Aydın", "Döviz / altın", "Ankara"}, {"Derya Koç", "Döviz / altın", "İzmir"},
			{"Burak Arslan", "Döviz / altın", "Bursa"}, {"Seda Yıldız", "Kredi", "Antalya"},
			{"Oğuzhan Çelik", "Kredi", "İstanbul"}, {"Esra Korkmaz", "Kredi", "Ankara"},
			{"Serkan Öztürk", "Kredi", "İzmir"}, {"Gülşah Polat", "Kredi", "Bursa"},
			{"Hakan Eren", "Kredi", "Antalya"}, {"Ceren Tunç", "Kart", "İstanbul"},
			{"Onur Kaplan", "Kart", "Ankara"}, {"Buse Güneş", "Kart", "İzmir"},
			{"Tolga Yalçın", "Kart", "Bursa"}, {"İrem Kurt", "Sigorta", "Antalya"},
			{"Kaan Özdemir", "Sigorta", "İstanbul"}, {"Ece Acar", "Sigorta", "Ankara"},
			{"Volkan Tekin", "Sigorta", "İzmir"}, {"Nazlı Ersoy", "Sigorta", "Bursa"},
			{"Alper Doğan", "Hasar", "Antalya"}, {"Pelin Uçar", "Hasar", "İstanbul"},
			{"Caner Şimşek", "Hasar", "Ankara"}, {"Melis Taş", "Esnaf / ticari", "İzmir"},
			{"Yusuf Çetin", "Esnaf / ticari", "Bursa"}, {"Damla Kılıç", "Karma eğitim", "Antalya"},
		}
		for i, c := range customers {
			code := fmt.Sprintf("CUST-%04d", i+1)
			_, err := tx.Exec(`INSERT INTO customers(code,name,demo_identity,segment,phone,email,city,status,kyc_status)
 VALUES(?,?,?,?,?,?,?,'ACTIVE','VERIFIED')`, code, c.name, fmt.Sprintf("SCENARIO_DEMO-%04d", i+1), c.segment, fmt.Sprintf("+90 555 000 %04d", i+1), fmt.Sprintf("demo%02d@ebs-egitim.invalid", i+1), c.city)
			if err != nil {
				return err
			}
			var customerID int64
			if err = tx.QueryRow("SELECT id FROM customers WHERE code=?", code).Scan(&customerID); err != nil {
				return err
			}
			if err = insertDemoUser(tx, c.name, c.name, "CUSTOMER", "Müşteri Portalı", customerID, []string{"self_service"}); err != nil {
				return err
			}
			for j, currency := range []string{"TRY", "USD"} {
				iban := fmt.Sprintf("TR00 0001 0000 0000 %04d %02d", i+1, j+1)
				balance := int64((i+1)*17300 + (j+1)*10000000)
				if _, err = tx.Exec(`INSERT INTO accounts(customer_id,iban,account_type,currency,balance_minor,status,opened_at)
 VALUES(?,?,? ,?,?,'ACTIVE',?)`, customerID, iban, map[bool]string{true: "Vadesiz", false: "Birikim"}[j == 0], currency, balance, Now()); err != nil {
					return err
				}
			}
		}
	}
	staff := []struct {
		username, display, role, department string
		permissions                           []string
	}{
		{"admin", "Sistem Yöneticisi / Eğitmen", "ADMIN", "Sistem Yönetimi", []string{"*"}},
		{"gise01", "Gişe Yetkilisi 1", "PERSONEL", "Gişe / Kasa", []string{"banking"}},
		{"gise02", "Gişe Yetkilisi 2", "PERSONEL", "Gişe / Kasa", []string{"banking"}},
		{"musteritemsilci", "Müşteri Temsilcisi", "PERSONEL", "Müşteri Hizmetleri", []string{"banking"}},
		{"operasyonyetkilisi", "Operasyon Yetkilisi", "PERSONEL", "Operasyon", []string{"banking", "banking:approve"}},
		{"krediyetkilisi", "Kredi Yetkilisi", "PERSONEL", "Krediler", []string{"banking"}},
		{"sigortayetkilisi", "Sigorta Yetkilisi", "PERSONEL", "Sigorta", []string{"banking"}},
		{"hasaryetkilisi", "Hasar Yetkilisi", "PERSONEL", "Hasar", []string{"banking"}},
		{"denetci", "Denetçi", "PERSONEL", "Denetim", []string{"banking:read"}},
	}
	for _, s := range staff {
		if err := insertDemoUser(tx, s.username, s.display, s.role, s.department, 0, s.permissions); err != nil {
			return err
		}
	}
	var cashCount int
	if err := tx.QueryRow("SELECT COUNT(*) FROM cash_registers").Scan(&cashCount); err != nil {
		return err
	}
	if cashCount == 0 {
		if _, err := tx.Exec("INSERT INTO cash_registers(branch,currency,status,balance_minor) VALUES('Merkez Şube','TRY','CLOSED',0)"); err != nil {
			return err
		}
	}
	for _, quote := range [][3]string{{"USD", "TRY", "34.1500"}, {"EUR", "TRY", "37.2500"}, {"XAU", "TRY", "2850.0000"}, {"TRY", "USD", "0.0292826"}, {"TRY", "EUR", "0.0268456"}} {
		if _, err := tx.Exec("INSERT OR IGNORE INTO fx_rates(base_currency,quote_currency,rate,as_of) VALUES(?,?,?,?)", quote[0], quote[1], quote[2], Now()); err != nil {
			return err
		}
	}
	return nil
}

func insertDemoUser(tx *sql.Tx, username, display, role, department string, customerID int64, permissions []string) error {
	var exists int
	if err := tx.QueryRow("SELECT COUNT(*) FROM users WHERE username=?", username).Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		return nil
	}
	salt := demoSalt(username)
	hash := hashPassword("1234", salt)
	if role == "ADMIN" {
		hash = hashPassword("EBS2026!", salt)
	}
	var customer any
	if customerID > 0 {
		customer = customerID
	}
	_, err := tx.Exec(`INSERT INTO users(username,password_hash,salt,display_name,role,department,customer_id,permissions_json,active)
 VALUES(?,?,?,?,?,?,?,?,1)`, username, hash, salt, display, role, department, customer, JSON(permissions))
	return err
}
