package sim

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

// Engine is the single durable transaction boundary for the v3.1 verticals.
// HTTP handlers must use Execute; individual domain functions intentionally
// accept *sql.Tx so a failed validation can never leave a partial state.
type Engine struct {
	DB  *sql.DB
	Dir string
	mu  sync.Mutex
}

var commonSchema = `
PRAGMA foreign_keys=ON;
CREATE TABLE IF NOT EXISTS sessions(token TEXT PRIMARY KEY,user_id INTEGER NOT NULL,created_at TEXT NOT NULL,expires_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS operation_requests(id INTEGER PRIMARY KEY AUTOINCREMENT,idem_key TEXT NOT NULL UNIQUE,reference TEXT NOT NULL,actor TEXT NOT NULL,operation TEXT NOT NULL,payload_hash TEXT NOT NULL,result_json TEXT NOT NULL,created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS audit_log(id INTEGER PRIMARY KEY AUTOINCREMENT,reference TEXT NOT NULL,entity_type TEXT NOT NULL,entity_id TEXT NOT NULL,action TEXT NOT NULL,old_state TEXT NOT NULL,new_state TEXT NOT NULL,actor TEXT NOT NULL,department TEXT NOT NULL,terminal TEXT NOT NULL,correlation_id TEXT NOT NULL,created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS ledger_entries(id INTEGER PRIMARY KEY AUTOINCREMENT,reference TEXT NOT NULL,account_code TEXT NOT NULL,direction TEXT NOT NULL,amount_minor INTEGER NOT NULL,currency TEXT NOT NULL,created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS work_items(id INTEGER PRIMARY KEY AUTOINCREMENT,kind TEXT NOT NULL,status TEXT NOT NULL,created_by TEXT NOT NULL,customer_id INTEGER,source TEXT NOT NULL,payload_json TEXT NOT NULL,department TEXT NOT NULL,assigned_to TEXT,created_at TEXT NOT NULL,completed_at TEXT);
CREATE TABLE IF NOT EXISTS notifications(id INTEGER PRIMARY KEY AUTOINCREMENT,customer_id INTEGER,title TEXT NOT NULL,body TEXT NOT NULL,reference TEXT NOT NULL,read_at TEXT,created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS app_documents(id INTEGER PRIMARY KEY AUTOINCREMENT,reference TEXT NOT NULL,customer_id INTEGER,document_type TEXT NOT NULL,path TEXT NOT NULL,notice TEXT NOT NULL,created_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS idx_audit_reference ON audit_log(reference);
CREATE INDEX IF NOT EXISTS idx_work_status ON work_items(status,department);
`

func Open(path string) (*Engine, error) {
	if path == "" {
		path = filepath.Join(os.TempDir(), "ebs-v31.sqlite3")
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite3", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(10000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	e := &Engine{DB: db, Dir: filepath.Dir(path)}
	if err = e.ensure(); err != nil {
		db.Close()
		return nil, err
	}
	return e, nil
}

func (e *Engine) ensure() error {
	tx, err := e.DB.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	for _, stmt := range strings.Split(commonSchema, ";") {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err = tx.Exec(stmt); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(bankingSchema); err != nil {
		return err
	}
	if err = seedBanking(tx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return nil
}

func payloadHash(op string, p Payload) string {
	b, _ := json.Marshal(p)
	h := sha256.Sum256([]byte(op + "\x00" + string(b)))
	return fmt.Sprintf("%x", h[:])
}

func (e *Engine) Execute(u User, op string, p Payload) (out Result, err error) {
	op = strings.ToLower(strings.TrimSpace(op))
	if op == "" {
		return nil, errors.New("OPERATION: İşlem adı zorunludur.")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	tx, err := e.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	ref := Ref(p)
	if ref == "" {
		ref = fmt.Sprintf("EBS-%d", time.Now().UTC().UnixNano())
		p["_reference"] = ref
	}
	idem := Str(p, "_idempotency_key")
	if idem != "" {
		var prior string
		if e := tx.QueryRow("SELECT result_json FROM operation_requests WHERE idem_key=?", idem).Scan(&prior); e == nil {
			var r Result
			if json.Unmarshal([]byte(prior), &r) == nil {
				r["duplicate"] = true
				if err = tx.Commit(); err != nil {
					return nil, err
				}
				return r, nil
			}
		}
	}
	var handled bool
	switch {
	case strings.HasPrefix(op, "banking."):
		out, handled, err = e.Banking(tx, u, op, p)
	case strings.HasPrefix(op, "insurance."):
		out, err = Insurance(tx, u, op, p)
		handled = true
	case strings.HasPrefix(op, "credit."):
		out, err = Credit(tx, u, op, p)
		handled = true
	case strings.HasPrefix(op, "card."):
		out, err = Cards(tx, u, op, p)
		handled = true
	case strings.HasPrefix(op, "document."):
		out, err = Documents(tx, u, op, p)
		handled = true
	default:
		err = fmt.Errorf("OPERATION: Desteklenmeyen işlem: %s", op)
	}
	if err != nil {
		return nil, err
	}
	if !handled {
		return nil, fmt.Errorf("OPERATION: Uygulanmamış işlem: %s", op)
	}
	if out == nil {
		out = Result{}
	}
	if _, ok := out["reference"]; !ok {
		out["reference"] = ref
	}
	if _, ok := out["operation"]; !ok {
		out["operation"] = op
	}
	if _, err = tx.Exec(`INSERT INTO audit_log(reference,entity_type,entity_id,action,old_state,new_state,actor,department,terminal,correlation_id,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, ref, "operation", ref, op, "", "COMPLETED", u.Username, u.Department, Str(p, "_terminal"), Str(p, "_correlation_id"), Now()); err != nil {
		return nil, err
	}
	if idem != "" {
		b, _ := json.Marshal(out)
		if _, err = tx.Exec(`INSERT INTO operation_requests(idem_key,reference,actor,operation,payload_hash,result_json,created_at) VALUES(?,?,?,?,?,?,?)`, idem, ref, u.Username, op, payloadHash(op, p), string(b), Now()); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func (e *Engine) Close() error {
	if e == nil || e.DB == nil {
		return nil
	}
	return e.DB.Close()
}
