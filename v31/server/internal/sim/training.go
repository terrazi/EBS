package sim

import (
	"database/sql"
	"fmt"
	"net/url"
	"strings"
)

func ensureTrainingSchema(tx *sql.Tx) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS training_scenarios(id INTEGER PRIMARY KEY AUTOINCREMENT,code TEXT NOT NULL UNIQUE,title TEXT NOT NULL,description TEXT NOT NULL,module TEXT NOT NULL,steps_json TEXT NOT NULL,max_score INTEGER NOT NULL DEFAULT 100,active INTEGER NOT NULL DEFAULT 1,created_by TEXT NOT NULL,created_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS training_assignments(id INTEGER PRIMARY KEY AUTOINCREMENT,scenario_id INTEGER NOT NULL,trainee_username TEXT NOT NULL,assigned_by TEXT NOT NULL,status TEXT NOT NULL,started_at TEXT,completed_at TEXT,due_at TEXT,score INTEGER,attempts INTEGER NOT NULL DEFAULT 0,created_at TEXT NOT NULL,UNIQUE(scenario_id,trainee_username,status))`,
		`CREATE TABLE IF NOT EXISTS training_attempts(id INTEGER PRIMARY KEY AUTOINCREMENT,assignment_id INTEGER NOT NULL,step_no INTEGER NOT NULL,operation TEXT NOT NULL,status TEXT NOT NULL,reference TEXT,duration_seconds INTEGER NOT NULL DEFAULT 0,attempt_no INTEGER NOT NULL,score INTEGER NOT NULL DEFAULT 0,feedback TEXT NOT NULL,actor TEXT NOT NULL,created_at TEXT NOT NULL)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	var n int
	if err := tx.QueryRow("SELECT COUNT(*) FROM training_scenarios").Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		_, err := tx.Exec(`INSERT INTO training_scenarios(code,title,description,module,steps_json,max_score,created_by,created_at) VALUES
		('SCN-CASH-001','Gişe: Nakit Yatırma ve Limit Kontrolü','Açık kasa, müşteri hesabı ve dengeli jurnal ile nakit yatırma.','Gişe / Kasa','[{"step":1,"operation":"banking.cash_open","label":"Kasa açılışı"},{"step":2,"operation":"banking.deposit","label":"Nakit yatırma"}]',100,'SYSTEM',?),
		('SCN-TRANSFER-001','Transfer: Limit ve Bağımsız Onay','Limit üstü transferi onay kuyruğuna gönderme ve bağımsız yetkili ile sonuçlandırma.','Para Transferleri','[{"step":1,"operation":"banking.transfer","label":"Transfer başlat"},{"step":2,"operation":"approval.decide","label":"Bağımsız onay"}]',100,'SYSTEM',?),
		('SCN-INS-001','Sigorta: Tekliften Poliçeye','Teklif, KYC kontrolü ve poliçe üretimini doğru sırada tamamla.','Sigorta','[{"step":1,"operation":"insurance.quote","label":"Teklif"},{"step":2,"operation":"insurance.policy_issue","label":"Poliçe"}]',100,'SYSTEM',?)`, Now(), Now(), Now())
		return err
	}
	return nil
}

func trainingStaff(u User) error {
	if err := Staff(u); err != nil {
		return err
	}
	if u.Role != "ADMIN" && !strings.Contains(strings.ToLower(u.Department), "eğitim") && !strings.Contains(strings.ToLower(u.Department), "sistem") {
		return ErrForbidden
	}
	return nil
}

func Training(tx *sql.Tx, u User, op string, p Payload) (Result, error) {
	if err := ensureTrainingSchema(tx); err != nil {
		return nil, err
	}
	op = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(op)), "training.")
	now := Now()
	switch op {
	case "scenario_create":
		if err := trainingStaff(u); err != nil {
			return nil, err
		}
		code, title, desc, module := Str(p, "code"), Str(p, "title"), Str(p, "description"), Str(p, "module")
		if code == "" || title == "" {
			return nil, fmt.Errorf("TRAINING: Kod ve başlık zorunludur.")
		}
		steps := Str(p, "steps_json")
		if steps == "" {
			steps = "[]"
		}
		r, err := tx.Exec(`INSERT INTO training_scenarios(code,title,description,module,steps_json,max_score,created_by,created_at) VALUES(?,?,?,?,?,?,?,?)`, code, title, desc, module, steps, 100, u.Username, now)
		if err != nil {
			return nil, err
		}
		id, _ := r.LastInsertId()
		return Row(tx, "SELECT * FROM training_scenarios WHERE id=?", id)
	case "assign":
		if err := trainingStaff(u); err != nil {
			return nil, err
		}
		sid := Int(p, "scenario_id")
		trainee := Str(p, "trainee_username")
		if sid == 0 || trainee == "" {
			return nil, fmt.Errorf("TRAINING: Senaryo ve öğrenci zorunludur.")
		}
		if _, err := Row(tx, "SELECT * FROM training_scenarios WHERE id=? AND active=1", sid); err != nil {
			return nil, err
		}
		if _, err := Row(tx, "SELECT * FROM users WHERE username=? AND role='PERSONEL' AND active=1", trainee); err != nil {
			return nil, fmt.Errorf("TRAINING: Personel bulunamadı.")
		}
		r, err := tx.Exec(`INSERT INTO training_assignments(scenario_id,trainee_username,assigned_by,status,due_at,created_at) VALUES(?,?,?,'ASSIGNED',?,?)`, sid, trainee, u.Username, Str(p, "due_at"), now)
		if err != nil {
			return nil, err
		}
		id, _ := r.LastInsertId()
		return Row(tx, "SELECT * FROM training_assignments WHERE id=?", id)
	case "start":
		id := Int(p, "assignment_id")
		a, err := Row(tx, "SELECT * FROM training_assignments WHERE id=?", id)
		if err != nil {
			return nil, err
		}
		if Text(a, "trainee_username") != u.Username && u.Role != "ADMIN" {
			return nil, ErrForbidden
		}
		if Text(a, "status") != "ASSIGNED" && Text(a, "status") != "IN_PROGRESS" {
			return nil, fmt.Errorf("TRAINING: Atama başlatılabilir durumda değil.")
		}
		if _, err = tx.Exec("UPDATE training_assignments SET status='IN_PROGRESS',started_at=COALESCE(started_at,?) WHERE id=?", now, id); err != nil {
			return nil, err
		}
		return Row(tx, "SELECT * FROM training_assignments WHERE id=?", id)
	case "record_step":
		id, step := Int(p, "assignment_id"), Int(p, "step_no")
		a, err := Row(tx, "SELECT * FROM training_assignments WHERE id=?", id)
		if err != nil {
			return nil, err
		}
		if Text(a, "trainee_username") != u.Username && u.Role != "ADMIN" {
			return nil, ErrForbidden
		}
		if Text(a, "status") != "IN_PROGRESS" {
			return nil, fmt.Errorf("TRAINING: Önce senaryoyu başlatmalısınız.")
		}
		passed := strings.EqualFold(Str(p, "passed"), "true")
		score := int64(0)
		if passed {
			score = Int(p, "score")
			if score <= 0 {
				score = 50
			}
		}
		attempt := Int(p, "attempt_no")
		if attempt <= 0 {
			attempt = 1
		}
		r, err := tx.Exec(`INSERT INTO training_attempts(assignment_id,step_no,operation,status,reference,duration_seconds,attempt_no,score,feedback,actor,created_at) VALUES(?,?,?, ?,?,?,?,?,?,?,?)`, id, step, Str(p, "operation"), map[bool]string{true: "PASSED", false: "FAILED"}[passed], Str(p, "reference"), Int(p, "duration_seconds"), attempt, score, Str(p, "feedback"), u.Username, now)
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec("UPDATE training_assignments SET attempts=attempts+1 WHERE id=?", id); err != nil {
			return nil, err
		}
		return Row(tx, "SELECT * FROM training_attempts WHERE id=?", mustLastID(r))
	case "complete":
		id := Int(p, "assignment_id")
		a, err := Row(tx, "SELECT * FROM training_assignments WHERE id=?", id)
		if err != nil {
			return nil, err
		}
		if Text(a, "trainee_username") != u.Username && u.Role != "ADMIN" {
			return nil, ErrForbidden
		}
		var score int64
		var failed int
		_ = tx.QueryRow("SELECT COALESCE(SUM(score),0),COALESCE(SUM(CASE WHEN status='FAILED' THEN 1 ELSE 0 END),0) FROM training_attempts WHERE assignment_id=?", id).Scan(&score, &failed)
		status := "COMPLETED"
		if failed > 0 {
			status = "COMPLETED_WITH_ERRORS"
		}
		if _, err = tx.Exec("UPDATE training_assignments SET status=?,score=?,completed_at=? WHERE id=?", status, score, now, id); err != nil {
			return nil, err
		}
		return Row(tx, "SELECT * FROM training_assignments WHERE id=?", id)
	default:
		return nil, fmt.Errorf("TRAINING: Desteklenmeyen eğitim işlemi: %s", op)
	}
}

func mustLastID(r sql.Result) int64 {
	id, e := r.LastInsertId()
	if e != nil {
		return 0
	}
	return id
}

func (e *Engine) TrainingQuery(u User, resource string, q url.Values) (any, bool, error) {
	if err := e.ensure(); err != nil {
		return nil, true, err
	}
	resource = strings.ToLower(strings.ReplaceAll(strings.Trim(resource, "/"), "_", "-"))
	if resource != "assignments" && resource != "scenarios" && resource != "leaderboard" && resource != "trainees" {
		return nil, false, nil
	}
	if resource == "trainees" && u.Role != "ADMIN" {
		return nil, true, ErrForbidden
	}
	if resource != "scenarios" && resource != "trainees" && u.Role != "ADMIN" && u.Role != "PERSONEL" {
		return nil, true, ErrForbidden
	}
	switch resource {
	case "trainees":
		rows, err := Rows(e.DB, "SELECT id,username,display_name,department,role FROM users WHERE role='PERSONEL' AND active=1 ORDER BY display_name")
		return rows, true, err
	case "scenarios":
		rows, err := Rows(e.DB, "SELECT * FROM training_scenarios WHERE active=1 ORDER BY id")
		return rows, true, err
	case "assignments":
		if u.Role == "PERSONEL" {
			rows, err := Rows(e.DB, "SELECT * FROM training_assignments WHERE trainee_username=? ORDER BY id DESC", u.Username)
			return rows, true, err
		}
		rows, err := Rows(e.DB, "SELECT * FROM training_assignments ORDER BY id DESC")
		return rows, true, err
	default:
		rows, err := Rows(e.DB, "SELECT trainee_username,COUNT(*) assignments,COALESCE(SUM(score),0) score,COALESCE(SUM(attempts),0) attempts FROM training_assignments GROUP BY trainee_username ORDER BY score DESC")
		return rows, true, err
	}
}
