package sim

import (
	"database/sql"
	"fmt"
	"strings"
)

func ensureDocumentsSchema(tx *sql.Tx) error {
	// app_documents is the central v3 table. The additional entity fields make
	// generated training documents addressable by every vertical without
	// changing an original PDF template.
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS app_documents (
		id INTEGER PRIMARY KEY AUTOINCREMENT, transaction_id INTEGER, customer_id INTEGER,
		document_type TEXT NOT NULL, reference TEXT, entity_type TEXT, entity_id INTEGER,
		status TEXT NOT NULL DEFAULT 'GENERATED', path TEXT NOT NULL DEFAULT '', content_json TEXT NOT NULL DEFAULT '{}',
		training_notice TEXT, notice TEXT, created_by TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		return err
	}
	for col, typ := range map[string]string{
		"customer_id": "INTEGER", "reference": "TEXT", "entity_type": "TEXT", "entity_id": "INTEGER",
		"status": "TEXT NOT NULL DEFAULT 'GENERATED'", "path": "TEXT NOT NULL DEFAULT ''", "content_json": "TEXT NOT NULL DEFAULT '{}'",
		"training_notice": "TEXT", "notice": "TEXT", "created_by": "TEXT NOT NULL DEFAULT ''", "created_at": "TEXT NOT NULL DEFAULT ''",
	} {
		if err := addColumnIfMissing(tx, "app_documents", col, typ); err != nil {
			return err
		}
	}
	return nil
}

var trainingTemplates = map[string]string{
	"account_opening":      "01_Hesap_Acilis_Talep_Formu.pdf",
	"address_confirmation": "02_Adres_Iletisim_Teyit_Formu.pdf",
	"income_statement":     "03_Gelir_Beyani_Formu.pdf",
	"credit_application":   "04_Kredi_Basvuru_Ek_Belgesi.pdf",
	"card_application":     "05_Kart_Basvuru_Ek_Belgesi.pdf",
	"endorsement":          "06_Zeyil_Talep_Formu.pdf",
	"policy_change":        "07_Police_Iptal_Yenileme_Talep_Formu.pdf",
	"claim_notice":         "08_Hasar_Ihbar_Formu.pdf",
	"transfer_recall":      "09_Transfer_Iade_Geri_Cagirma_Talebi.pdf",
	"autopay":              "10_Otomatik_Odeme_Talimati.pdf",
	"collateral":           "11_Teminat_Beyan_Formu.pdf",
	"beneficiary_change":   "12_Dain_Murtehin_Lehtar_Degisiklik_Talebi.pdf",
}

func documentRef(p Payload) string {
	if r := Ref(p); r != "" {
		return r
	}
	return fmt.Sprintf("DOC-%s", strings.ReplaceAll(strings.ReplaceAll(Now(), ":", ""), ".", ""))
}

// Documents creates and lists immutable training-document records. Template
// files are never overwritten; `template` records the source template while
// `path` points to a separately generated artifact.
func Documents(tx *sql.Tx, u User, op string, p Payload) (Result, error) {
	if err := ensureDocumentsSchema(tx); err != nil {
		return nil, err
	}
	op = strings.ToLower(strings.TrimSpace(op))
	if strings.HasPrefix(op, "document.") {
		op = strings.TrimPrefix(op, "document.")
	}
	switch op {
	case "create":
		customerID := Int(p, "customer_id")
		if customerID == 0 {
			customerID = u.CustomerID
		}
		if customerID != 0 {
			if _, err := Customer(tx, u, customerID); err != nil {
				return nil, err
			}
		}
		if u.Role == "CUSTOMER" && customerID != u.CustomerID {
			return nil, ErrForbidden
		}
		docType := Str(p, "document_type")
		if docType == "" {
			return nil, fmt.Errorf("DOCUMENT: Belge türü zorunludur.")
		}
		ref := documentRef(p)
		template := Str(p, "template")
		if template == "" {
			template = trainingTemplates[docType]
		}
		content := Str(p, "content_json")
		if content == "" {
			content = JSON(p["fields"])
		}
		path := Str(p, "path")
		if path == "" {
			path = "generated/" + ref + ".pdf"
		}
		if template != "" {
			content = fmt.Sprintf(`{"template":%q,"fields":%s}`, template, content)
		}
		const notice = "EĞİTİM AMAÇLIDIR - RESMÎ BELGE DEĞİLDİR"
		res, err := tx.Exec(`INSERT INTO app_documents(transaction_id,customer_id,document_type,reference,entity_type,entity_id,status,path,content_json,training_notice,notice,created_by,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, Int(p, "transaction_id"), customerID, docType, ref, Str(p, "entity_type"), Int(p, "entity_id"), "GENERATED", path, content, notice, notice, u.Username, Now())
		if err != nil {
			return nil, err
		}
		id, _ := res.LastInsertId()
		return Row(tx, `SELECT * FROM app_documents WHERE id=?`, id)

	case "list":
		customerID := Int(p, "customer_id")
		if u.Role == "CUSTOMER" {
			if customerID != 0 && customerID != u.CustomerID {
				return nil, ErrForbidden
			}
			customerID = u.CustomerID
		}
		var rows []Result
		var err error
		if customerID != 0 {
			rows, err = Rows(tx, `SELECT * FROM app_documents WHERE customer_id=? ORDER BY id DESC`, customerID)
		} else {
			if err = Staff(u); err != nil {
				return nil, err
			}
			rows, err = Rows(tx, `SELECT * FROM app_documents ORDER BY id DESC LIMIT 500`)
		}
		if err != nil {
			return nil, err
		}
		return Result{"documents": rows, "count": len(rows)}, nil
	default:
		return nil, fmt.Errorf("OPERATION: Desteklenmeyen belge işlemi: %s", op)
	}
}
