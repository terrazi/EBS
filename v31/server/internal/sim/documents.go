package sim

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
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
		"transaction_id": "INTEGER", "customer_id": "INTEGER", "reference": "TEXT", "entity_type": "TEXT", "entity_id": "INTEGER",
		"status": "TEXT NOT NULL DEFAULT 'GENERATED'", "path": "TEXT NOT NULL DEFAULT ''", "content_json": "TEXT NOT NULL DEFAULT '{}'",
		"training_notice": "TEXT", "notice": "TEXT", "created_by": "TEXT NOT NULL DEFAULT ''", "created_at": "TEXT NOT NULL DEFAULT ''",
		"file_hash": "TEXT NOT NULL DEFAULT ''", "file_size": "INTEGER NOT NULL DEFAULT 0",
	} {
		if err := addColumnIfMissing(tx, "app_documents", col, typ); err != nil {
			return err
		}
	}
	return nil
}

var trainingTemplates = map[string]string{
	"account_opening":      "01_Hesap_Acilis_Talep_Formu.pdf",
	"insurance_quote":      "13_Sigorta_Teklif_Bilgi_Formu.pdf",
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

	case "upload":
		// Training uploads are represented by a generated, immutable virtual
		// document. A real file is materialized after commit, so a student can
		// complete the same document-control step without using personal data.
		p["document_type"] = Str(p, "document_type")
		if Str(p, "document_type") == "" {
			return nil, fmt.Errorf("DOCUMENT: Sanal belge türü zorunludur.")
		}
		p["source_type"] = "TRAINING_GENERATED_UPLOAD"
		return Documents(tx, u, "create", p)

	case "verify", "reject", "delete", "attach":
		id := Int(p, "document_id")
		if id == 0 {
			return nil, fmt.Errorf("DOCUMENT: Belge ID zorunludur.")
		}
		doc, err := Row(tx, `SELECT * FROM app_documents WHERE id=?`, id)
		if err != nil {
			return nil, err
		}
		customerID := ID(doc, "customer_id")
		if u.Role == "CUSTOMER" && customerID != u.CustomerID {
			return nil, ErrForbidden
		}
		if op != "attach" {
			if err := Staff(u); err != nil {
				return nil, err
			}
		}
		status := Text(doc, "status")
		switch op {
		case "verify":
			if status != "GENERATED" && status != "UPLOADED" {
				return nil, fmt.Errorf("DOCUMENT: Yalnız üretilmiş/yüklenmiş belgeler doğrulanabilir.")
			}
			_, err = tx.Exec(`UPDATE app_documents SET status='VERIFIED' WHERE id=?`, id)
		case "reject":
			if status == "DELETED" {
				return nil, fmt.Errorf("DOCUMENT: Silinmiş belge geri gönderilemez.")
			}
			_, err = tx.Exec(`UPDATE app_documents SET status='REJECTED',notice=? WHERE id=?`, Str(p, "reason"), id)
		case "delete":
			if u.Role != "ADMIN" && Text(doc, "created_by") != u.Username {
				return nil, ErrForbidden
			}
			_, err = tx.Exec(`UPDATE app_documents SET status='DELETED',notice=? WHERE id=?`, Str(p, "reason"), id)
		case "attach":
			if status == "DELETED" || status == "REJECTED" {
				return nil, fmt.Errorf("DOCUMENT: Reddedilmiş/silinmiş belge işleme bağlanamaz.")
			}
			if u.Role == "CUSTOMER" && customerID != u.CustomerID {
				return nil, ErrForbidden
			}
			_, err = tx.Exec(`UPDATE app_documents SET transaction_id=?,entity_type=?,entity_id=?,reference=COALESCE(NULLIF(?,''),reference) WHERE id=?`, Int(p, "transaction_id"), Str(p, "entity_type"), Int(p, "entity_id"), Str(p, "reference"), id)
		}
		if err != nil {
			return nil, err
		}
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

// RequireDocument enforces the document matrix at the transaction boundary.
// The caller may create a training document first with document.upload/create;
// rejected or deleted documents can never satisfy a business operation.
func RequireDocument(tx *sql.Tx, u User, p Payload, customerID int64, expected ...string) error {
	id := Int(p, "document_id")
	ref := Str(p, "document_reference")
	if id == 0 && ref == "" {
		return fmt.Errorf("DOCUMENT: İşleme başlamadan önce sanal belge oluşturup Belge ID'sini girin.")
	}
	query := `SELECT * FROM app_documents WHERE status IN ('GENERATED','UPLOADED','VERIFIED') AND `
	var arg any
	if id != 0 {
		query += "id=?"
		arg = id
	} else {
		query += "reference=?"
		arg = ref
	}
	doc, err := Row(tx, query, arg)
	if err != nil {
		return fmt.Errorf("DOCUMENT: Geçerli sanal belge bulunamadı.")
	}
	if customerID != 0 && ID(doc, "customer_id") != 0 && ID(doc, "customer_id") != customerID {
		return ErrForbidden
	}
	if u.Role == "CUSTOMER" && ID(doc, "customer_id") != u.CustomerID {
		return ErrForbidden
	}
	if len(expected) > 0 {
		kind := strings.ToLower(Text(doc, "document_type"))
		matched := false
		for _, want := range expected {
			if kind == strings.ToLower(want) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("DOCUMENT: Bu işlem için uygun belge türü bulunamadı.")
		}
	}
	return nil
}

// MaterializeDocuments creates a safe local PDF artifact after a successful
// transaction. It is deliberately a small training PDF and carries a clear
// non-official notice; uploaded/generated records never overwrite templates.
func (e *Engine) MaterializeDocuments(reference string) {
	if e == nil || reference == "" {
		return
	}
	rows, err := Rows(e.DB, `SELECT * FROM app_documents WHERE reference=? AND status<>'DELETED'`, reference)
	if err != nil {
		return
	}
	dir := filepath.Join(e.Dir, "documents")
	if os.MkdirAll(dir, 0755) != nil {
		return
	}
	for _, row := range rows {
		name := filepath.Base(Text(row, "path"))
		if name == "." || name == "" || name == string(filepath.Separator) {
			name = fmt.Sprintf("%s-%d.pdf", reference, ID(row, "id"))
		}
		if !strings.HasSuffix(strings.ToLower(name), ".pdf") {
			name += ".pdf"
		}
		path := filepath.Join(dir, name)
		body := fmt.Sprintf("EBS EGITIM SIMULASYONU\nE G I T I M  A M A C L I  -  R E S M I  B E L G E  D E G I L D I R\nBelge turu: %s\nReferans: %s\nOlusturan: %s\nKayit zamani: %s", Text(row, "document_type"), Text(row, "reference"), Text(row, "created_by"), Text(row, "created_at"))
		pdf := trainingPDF(body)
		if os.WriteFile(path, pdf, 0644) != nil {
			continue
		}
		hash := sha256.Sum256(pdf)
		_, _ = e.DB.Exec(`UPDATE app_documents SET path=?,file_hash=?,file_size=? WHERE id=?`, path, hex.EncodeToString(hash[:]), len(pdf), ID(row, "id"))
	}
}

func trainingPDF(text string) []byte {
	clean := strings.ReplaceAll(strings.ReplaceAll(text, "\\", "\\\\"), "(", "\\(")
	clean = strings.ReplaceAll(clean, ")", "\\)")
	stream := fmt.Sprintf("BT /F1 11 Tf 48 760 Td (%s) Tj ET", strings.ReplaceAll(clean, "\n", ") Tj 0 -18 Td ("))
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>", "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>", fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream)}
	result := []byte("%PDF-1.4\n")
	offsets := []int{0}
	for i, object := range objects {
		offsets = append(offsets, len(result))
		result = append(result, []byte(fmt.Sprintf("%d 0 obj\n%s\nendobj\n", i+1, object))...)
	}
	xref := len(result)
	result = append(result, []byte(fmt.Sprintf("xref\n0 %d\n0000000000 65535 f \n", len(objects)+1))...)
	for _, off := range offsets[1:] {
		result = append(result, []byte(fmt.Sprintf("%010d 00000 n \n", off))...)
	}
	result = append(result, []byte(fmt.Sprintf("trailer << /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref))...)
	return result
}

func (e *Engine) DocumentsQuery(u User, resource string, q url.Values) (any, bool, error) {
	if strings.ToLower(strings.Trim(strings.ReplaceAll(resource, "_", "-"), "/")) != "documents" {
		return nil, false, nil
	}
	if u.Role == "CUSTOMER" {
		rows, err := Rows(e.DB, `SELECT id,customer_id,document_type,reference,status,path,training_notice,created_at FROM app_documents WHERE customer_id=? AND status<>'DELETED' ORDER BY id DESC`, u.CustomerID)
		return rows, true, err
	}
	if err := Staff(u); err != nil {
		return nil, true, err
	}
	rows, err := Rows(e.DB, `SELECT id,customer_id,document_type,reference,status,path,training_notice,created_at FROM app_documents WHERE status<>'DELETED' ORDER BY id DESC LIMIT 500`)
	return rows, true, err
}
