package sim

import (
	"database/sql"
	"fmt"
	"math/big"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func bankReference(p Payload, prefix string) string {
	if r := Ref(p); r != "" {
		return r
	}
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func bankAmount(p Payload) (int64, error) {
	key := "amount_minor"
	if Str(p, key) == "" {
		key = "amount"
	}
	return Amount(p, key)
}

func staffWrite(u User) error {
	if u.Role == "CUSTOMER" {
		return ErrForbidden
	}
	return Allowed(u, "banking")
}

func staffRead(u User) error {
	if u.Role == "ADMIN" {
		return nil
	}
	if u.Role != "PERSONEL" {
		return ErrForbidden
	}
	for _, p := range u.Permissions {
		if p == "*" || p == "banking" || p == "banking:read" || p == "banking:*" {
			return nil
		}
	}
	return ErrForbidden
}

func customerIDFor(tx *sql.Tx, u User, p Payload) (int64, error) {
	id := Int(p, "customer_id")
	if u.Role == "CUSTOMER" {
		if id == 0 {
			id = u.CustomerID
		}
		if id != u.CustomerID {
			return 0, ErrForbidden
		}
	} else if id == 0 {
		return 0, fmt.Errorf("CUSTOMER: müşteri seçimi zorunludur.")
	}
	if _, err := Customer(tx, u, id); err != nil {
		return 0, err
	}
	return id, nil
}

func accountFor(tx *sql.Tx, u User, p Payload, key string) (Result, error) {
	id := Int(p, key)
	if id == 0 {
		return nil, fmt.Errorf("ACCOUNT: Hesap seçimi zorunludur.")
	}
	return Account(tx, u, id)
}

func accountByID(tx *sql.Tx, id int64) (Result, error) {
	if id == 0 {
		return nil, fmt.Errorf("ACCOUNT: Hesap seçimi zorunludur.")
	}
	return Row(tx, "SELECT * FROM accounts WHERE id=? AND status='ACTIVE'", id)
}

func activeCash(tx *sql.Tx) (Result, error) {
	return Row(tx, "SELECT * FROM cash_registers WHERE currency='TRY' ORDER BY id LIMIT 1")
}

func result(ref, op, status string) Result {
	return Result{"reference": ref, "operation": op, "status": status}
}

func amountResult(out Result, amount int64, currency string) Result {
	out["amount_minor"] = amount
	out["currency"] = currency
	return out
}

func customerNotify(tx *sql.Tx, customerID int64, title, body, ref string) error {
	if customerID == 0 {
		return nil
	}
	return Notify(tx, customerID, title, body, ref)
}

func bankingCashOpen(tx *sql.Tx, u User, p Payload) (Result, error) {
	if err := staffWrite(u); err != nil {
		return nil, err
	}
	cash, err := activeCash(tx)
	if err != nil {
		return nil, err
	}
	if Text(cash, "status") == "OPEN" {
		return Result{"status": "OPEN", "already_open": true, "cash_register_id": ID(cash, "id")}, nil
	}
	if _, err = tx.Exec("UPDATE cash_registers SET status='OPEN',opened_by=?,opened_at=?,closed_at=NULL WHERE id=?", u.Username, Now(), ID(cash, "id")); err != nil {
		return nil, err
	}
	return Result{"status": "OPEN", "cash_register_id": ID(cash, "id"), "opened_by": u.Username}, nil
}

func bankingCashClose(tx *sql.Tx, u User, p Payload) (Result, error) {
	if err := staffWrite(u); err != nil {
		return nil, err
	}
	cash, err := activeCash(tx)
	if err != nil {
		return nil, err
	}
	if Text(cash, "status") != "OPEN" {
		return nil, fmt.Errorf("STATE: Kasa zaten kapalı.")
	}
	if expected := Str(p, "expected_balance_minor"); expected != "" {
		n, parseErr := strconv.ParseInt(expected, 10, 64)
		if parseErr != nil || n < 0 || n != ID(cash, "balance_minor") {
			return nil, fmt.Errorf("CASH: Kapanış bakiyesi kasa bakiyesiyle eşleşmiyor.")
		}
	}
	if _, err = tx.Exec("UPDATE cash_registers SET status='CLOSED',closed_at=? WHERE id=?", Now(), ID(cash, "id")); err != nil {
		return nil, err
	}
	return Result{"status": "CLOSED", "cash_register_id": ID(cash, "id"), "balance_minor": ID(cash, "balance_minor")}, nil
}

func bankingDeposit(tx *sql.Tx, u User, p Payload) (Result, error) {
	if err := staffWrite(u); err != nil {
		return nil, err
	}
	account, err := accountFor(tx, User{Role: "ADMIN"}, p, "account_id")
	if err != nil {
		return nil, err
	}
	amount, err := bankAmount(p)
	if err != nil {
		return nil, err
	}
	cash, err := activeCash(tx)
	if err != nil {
		return nil, err
	}
	if Text(cash, "status") != "OPEN" {
		return nil, fmt.Errorf("STATE: İşlem için kasa açık olmalıdır.")
	}
	if Text(account, "currency") != "TRY" {
		return nil, fmt.Errorf("CURRENCY: Kasa yalnızca TRY hesabını destekliyor.")
	}
	ref := bankReference(p, "CASH")
	if err = Balance(tx, ID(account, "id"), amount); err != nil {
		return nil, err
	}
	if _, err = tx.Exec("UPDATE cash_registers SET balance_minor=balance_minor+? WHERE id=?", amount, ID(cash, "id")); err != nil {
		return nil, err
	}
	if err = Journal(tx, ref, fmt.Sprintf("CASH_REGISTER:%d", ID(cash, "id")), fmt.Sprintf("ACCOUNT:%d", ID(account, "id")), amount, "TRY"); err != nil {
		return nil, err
	}
	customerID := ID(account, "customer_id")
	if _, err = tx.Exec(`INSERT INTO payments(reference,customer_id,account_id,institution,amount_minor,currency,kind,status,created_by,created_at,completed_at)
 VALUES(?,?,?,'CASH',?,'TRY','CASH_DEPOSIT','COMPLETED',?,?,?)`, ref, customerID, ID(account, "id"), amount, u.Username, Now(), Now()); err != nil {
		return nil, err
	}
	if err = customerNotify(tx, customerID, "Nakit yatırma", fmt.Sprintf("%d kuruş hesabınıza yatırıldı.", amount), ref); err != nil {
		return nil, err
	}
	return amountResult(result(ref, "banking.deposit", "COMPLETED"), amount, "TRY"), nil
}

func bankingWithdraw(tx *sql.Tx, u User, p Payload) (Result, error) {
	if err := staffWrite(u); err != nil {
		return nil, err
	}
	account, err := accountFor(tx, User{Role: "ADMIN"}, p, "account_id")
	if err != nil {
		return nil, err
	}
	amount, err := bankAmount(p)
	if err != nil {
		return nil, err
	}
	cash, err := activeCash(tx)
	if err != nil {
		return nil, err
	}
	if Text(cash, "status") != "OPEN" {
		return nil, fmt.Errorf("STATE: İşlem için kasa açık olmalıdır.")
	}
	if Text(account, "currency") != "TRY" {
		return nil, fmt.Errorf("CURRENCY: Kasa yalnızca TRY hesabını destekliyor.")
	}
	if ID(cash, "balance_minor") < amount {
		return nil, fmt.Errorf("BALANCE: Kasada yeterli bakiye yok.")
	}
	ref := bankReference(p, "CASH")
	if err = Balance(tx, ID(account, "id"), -amount); err != nil {
		return nil, err
	}
	r, err := tx.Exec("UPDATE cash_registers SET balance_minor=balance_minor-? WHERE id=? AND balance_minor>=?", amount, ID(cash, "id"), amount)
	if err != nil {
		return nil, err
	}
	if n, _ := r.RowsAffected(); n != 1 {
		return nil, fmt.Errorf("BALANCE: Kasada yeterli bakiye yok.")
	}
	if err = Journal(tx, ref, fmt.Sprintf("ACCOUNT:%d", ID(account, "id")), fmt.Sprintf("CASH_REGISTER:%d", ID(cash, "id")), amount, "TRY"); err != nil {
		return nil, err
	}
	customerID := ID(account, "customer_id")
	if _, err = tx.Exec(`INSERT INTO payments(reference,customer_id,account_id,institution,amount_minor,currency,kind,status,created_by,created_at,completed_at)
 VALUES(?,?,?,'CASH',?,'TRY','CASH_WITHDRAW','COMPLETED',?,?,?)`, ref, customerID, ID(account, "id"), amount, u.Username, Now(), Now()); err != nil {
		return nil, err
	}
	return amountResult(result(ref, "banking.withdraw", "COMPLETED"), amount, "TRY"), nil
}

func bankingAccountOpen(tx *sql.Tx, u User, p Payload) (Result, error) {
	if u.Role != "CUSTOMER" {
		if err := staffWrite(u); err != nil {
			return nil, err
		}
	}
	customerID, err := customerIDFor(tx, u, p)
	if err != nil {
		return nil, err
	}
	if err = KYC(tx, u, customerID); err != nil {
		return nil, err
	}
	currency := strings.ToUpper(Str(p, "currency"))
	if currency == "" {
		currency = "TRY"
	}
	if currency != "TRY" && currency != "USD" && currency != "EUR" && currency != "XAU" {
		return nil, fmt.Errorf("CURRENCY: Desteklenmeyen para birimi.")
	}
	accountType := Str(p, "account_type")
	if accountType == "" {
		accountType = "Vadesiz"
	}
	opened := Now()
	res, err := tx.Exec("INSERT INTO accounts(customer_id,iban,account_type,currency,balance_minor,status,opened_at) VALUES(?,?,?,?,0,'ACTIVE',?)", customerID, "PENDING-IBAN", accountType, currency, opened)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	iban := fmt.Sprintf("TR00 0010 0000 0000 %08d %02d", id, customerID%100)
	if _, err = tx.Exec("UPDATE accounts SET iban=? WHERE id=?", iban, id); err != nil {
		return nil, err
	}
	return Result{"operation": "banking.account_open", "status": "COMPLETED", "account_id": id, "customer_id": customerID, "iban": iban, "currency": currency, "balance_minor": int64(0)}, nil
}

func bankingAccountClose(tx *sql.Tx, u User, p Payload) (Result, error) {
	if u.Role != "CUSTOMER" {
		if err := staffWrite(u); err != nil {
			return nil, err
		}
	}
	account, err := accountFor(tx, u, p, "account_id")
	if err != nil {
		return nil, err
	}
	if ID(account, "balance_minor") != 0 {
		return nil, fmt.Errorf("STATE: Hesap kapanmadan önce bakiye sıfırlanmalıdır.")
	}
	if _, err = tx.Exec("UPDATE accounts SET status='CLOSED' WHERE id=? AND status='ACTIVE'", ID(account, "id")); err != nil {
		return nil, err
	}
	return Result{"operation": "banking.account_close", "status": "COMPLETED", "account_id": ID(account, "id")}, nil
}

func bankingTermDeposit(tx *sql.Tx, u User, p Payload) (Result, error) {
	if u.Role != "CUSTOMER" {
		if err := staffWrite(u); err != nil {
			return nil, err
		}
	}
	account, err := accountFor(tx, u, p, "source_account_id")
	if err != nil {
		return nil, err
	}
	amount, err := bankAmount(p)
	if err != nil {
		return nil, err
	}
	days := Int(p, "days")
	if days <= 0 || days > 3650 {
		return nil, fmt.Errorf("TERM: Vade gün sayısı 1 ile 3650 arasında olmalıdır.")
	}
	ref := bankReference(p, "TERM")
	maturity := time.Now().UTC().Add(time.Duration(days) * 24 * time.Hour).Format("2006-01-02")
	if err = Balance(tx, ID(account, "id"), -amount); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(`INSERT INTO term_deposits(reference,customer_id,source_account_id,principal_minor,currency,maturity_date,status,created_by,created_at)
 VALUES(?,?,?,?,?,?,'ACTIVE',?,?)`, ref, ID(account, "customer_id"), ID(account, "id"), amount, Text(account, "currency"), maturity, u.Username, Now()); err != nil {
		return nil, err
	}
	if err = Journal(tx, ref, fmt.Sprintf("ACCOUNT:%d", ID(account, "id")), "TERM_DEPOSIT", amount, Text(account, "currency")); err != nil {
		return nil, err
	}
	return amountResult(Result{"reference": ref, "operation": "banking.term_deposit", "status": "COMPLETED", "maturity_date": maturity}, amount, Text(account, "currency")), nil
}

func bankingTransfer(tx *sql.Tx, u User, p Payload) (Result, error) {
	if u.Role != "CUSTOMER" {
		if err := staffWrite(u); err != nil {
			return nil, err
		}
	}
	source, err := accountFor(tx, u, p, "source_account_id")
	if err != nil {
		return nil, err
	}
	target, err := accountByID(tx, Int(p, "target_account_id"))
	if err != nil {
		return nil, err
	}
	if u.Role == "CUSTOMER" && ID(target, "customer_id") != u.CustomerID {
		return nil, ErrForbidden
	}
	if ID(source, "id") == ID(target, "id") {
		return nil, fmt.Errorf("TRANSFER: Kaynak ve hedef hesap aynı olamaz.")
	}
	if Text(source, "currency") != Text(target, "currency") {
		return nil, fmt.Errorf("CURRENCY: Havale için para birimleri aynı olmalıdır.")
	}
	amount, err := bankAmount(p)
	if err != nil {
		return nil, err
	}
	if ID(source, "balance_minor") < amount {
		return nil, fmt.Errorf("BALANCE: Kaynak hesap bakiyesi yetersiz.")
	}
	ref := bankReference(p, "TRF")
	typeName := strings.ToUpper(Str(p, "transfer_type"))
	if typeName == "" {
		typeName = "HAVALE"
	}
	status := "COMPLETED"
	if amount > 5_000_000 {
		status = "PENDING_APPROVAL"
	}
	inserted, err := tx.Exec(`INSERT INTO banking_transfers(reference,customer_id,source_account_id,target_account_id,amount_minor,currency,transfer_type,status,created_by,created_at)
	 VALUES(?,?,?,?,?,?,?,?,?,?)`, ref, ID(source, "customer_id"), ID(source, "id"), ID(target, "id"), amount, Text(source, "currency"), typeName, status, u.Username, Now())
	if err != nil {
		return nil, err
	}
	if status == "PENDING_APPROVAL" {
		transferID, idErr := inserted.LastInsertId()
		if idErr != nil {
			return nil, idErr
		}
		if _, err = Queue(tx, "TRANSFER_APPROVAL", u, ID(source, "customer_id"), Payload{"reference": ref, "transfer_id": transferID, "amount_minor": amount, "source_account_id": ID(source, "id"), "target_account_id": ID(target, "id")}); err != nil {
			return nil, err
		}
		return amountResult(Result{"reference": ref, "operation": "banking.transfer", "status": status, "requires_approval": true}, amount, Text(source, "currency")), nil
	}
	if err = Balance(tx, ID(source, "id"), -amount); err != nil {
		return nil, err
	}
	if err = Balance(tx, ID(target, "id"), amount); err != nil {
		return nil, err
	}
	if err = Journal(tx, ref, fmt.Sprintf("ACCOUNT:%d", ID(source, "id")), fmt.Sprintf("ACCOUNT:%d", ID(target, "id")), amount, Text(source, "currency")); err != nil {
		return nil, err
	}
	if _, err = tx.Exec("UPDATE banking_transfers SET completed_at=? WHERE reference=?", Now(), ref); err != nil {
		return nil, err
	}
	if err = customerNotify(tx, ID(target, "customer_id"), "Hesaba transfer", fmt.Sprintf("%d kuruş hesabınıza aktarıldı.", amount), ref); err != nil {
		return nil, err
	}
	return amountResult(Result{"reference": ref, "operation": "banking.transfer", "status": status}, amount, Text(source, "currency")), nil
}

func bankingRecall(tx *sql.Tx, u User, p Payload) (Result, error) {
	if u.Role != "CUSTOMER" {
		if err := staffWrite(u); err != nil {
			return nil, err
		}
	}
	ref := Str(p, "reference")
	if ref == "" {
		ref = Str(p, "transfer_reference")
	}
	if ref == "" {
		return nil, fmt.Errorf("TRANSFER: Geri çağırma referansı zorunludur.")
	}
	transfer, err := Row(tx, "SELECT * FROM banking_transfers WHERE reference=?", ref)
	if err != nil {
		return nil, err
	}
	if u.Role == "CUSTOMER" && ID(transfer, "customer_id") != u.CustomerID {
		return nil, ErrForbidden
	}
	if Text(transfer, "status") != "COMPLETED" {
		return nil, fmt.Errorf("STATE: Yalnızca tamamlanmış transfer geri çağrılabilir.")
	}
	source, err := accountByID(tx, ID(transfer, "source_account_id"))
	if err != nil {
		return nil, err
	}
	target, err := accountByID(tx, ID(transfer, "target_account_id"))
	if err != nil {
		return nil, err
	}
	amount := ID(transfer, "amount_minor")
	if err = Balance(tx, ID(target, "id"), -amount); err != nil {
		return nil, fmt.Errorf("BALANCE: Hedef hesapta geri çağırma için yeterli bakiye yok.")
	}
	if err = Balance(tx, ID(source, "id"), amount); err != nil {
		return nil, err
	}
	if err = Journal(tx, ref+"-RECALL", fmt.Sprintf("ACCOUNT:%d", ID(target, "id")), fmt.Sprintf("ACCOUNT:%d", ID(source, "id")), amount, Text(transfer, "currency")); err != nil {
		return nil, err
	}
	if _, err = tx.Exec("UPDATE banking_transfers SET status='RECALLED',completed_at=? WHERE reference=?", Now(), ref); err != nil {
		return nil, err
	}
	return Result{"reference": ref, "operation": "banking.transfer_recall", "status": "RECALLED", "amount_minor": amount, "currency": Text(transfer, "currency")}, nil
}

func bankingBillPay(tx *sql.Tx, u User, p Payload) (Result, error) {
	if u.Role != "CUSTOMER" {
		if err := staffWrite(u); err != nil {
			return nil, err
		}
	}
	account, err := accountFor(tx, u, p, "account_id")
	if err != nil {
		return nil, err
	}
	institution := Str(p, "institution")
	if institution == "" {
		return nil, fmt.Errorf("PAYMENT: Kurum bilgisi zorunludur.")
	}
	amount, err := bankAmount(p)
	if err != nil {
		return nil, err
	}
	ref := bankReference(p, "PAY")
	if err = Balance(tx, ID(account, "id"), -amount); err != nil {
		return nil, err
	}
	if err = Journal(tx, ref, fmt.Sprintf("ACCOUNT:%d", ID(account, "id")), "BILLER:"+institution, amount, Text(account, "currency")); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(`INSERT INTO payments(reference,customer_id,account_id,institution,amount_minor,currency,kind,status,created_by,created_at,completed_at)
 VALUES(?,?,?,?,?,?,'BILL_PAYMENT','COMPLETED',?,?,?)`, ref, ID(account, "customer_id"), ID(account, "id"), institution, amount, Text(account, "currency"), u.Username, Now(), Now()); err != nil {
		return nil, err
	}
	return amountResult(result(ref, "banking.bill_pay", "COMPLETED"), amount, Text(account, "currency")), nil
}

func bankingAutopayCreate(tx *sql.Tx, u User, p Payload) (Result, error) {
	if u.Role != "CUSTOMER" {
		if err := staffWrite(u); err != nil {
			return nil, err
		}
	}
	account, err := accountFor(tx, u, p, "account_id")
	if err != nil {
		return nil, err
	}
	institution := Str(p, "institution")
	amount, err := bankAmount(p)
	if err != nil || institution == "" {
		if err == nil {
			err = fmt.Errorf("PAYMENT: Kurum bilgisi zorunludur.")
		}
		return nil, err
	}
	dueDay := Int(p, "due_day")
	if dueDay < 1 || dueDay > 31 {
		return nil, fmt.Errorf("PAYMENT: Son ödeme günü 1 ile 31 arasında olmalıdır.")
	}
	r, err := tx.Exec(`INSERT INTO autopays(customer_id,account_id,institution,amount_minor,currency,due_day,status,created_by,created_at)
 VALUES(?,?,?,?,?,?,'ACTIVE',?,?)`, ID(account, "customer_id"), ID(account, "id"), institution, amount, Text(account, "currency"), dueDay, u.Username, Now())
	if err != nil {
		return nil, err
	}
	id, _ := r.LastInsertId()
	return Result{"operation": "banking.autopay_create", "status": "ACTIVE", "autopay_id": id, "customer_id": ID(account, "customer_id")}, nil
}

func bankingAutopayCancel(tx *sql.Tx, u User, p Payload) (Result, error) {
	if u.Role != "CUSTOMER" {
		if err := staffWrite(u); err != nil {
			return nil, err
		}
	}
	id := Int(p, "autopay_id")
	item, err := Row(tx, "SELECT * FROM autopays WHERE id=?", id)
	if err != nil {
		return nil, err
	}
	if u.Role == "CUSTOMER" && ID(item, "customer_id") != u.CustomerID {
		return nil, ErrForbidden
	}
	if Text(item, "status") != "ACTIVE" {
		return nil, fmt.Errorf("STATE: Otomatik ödeme zaten aktif değil.")
	}
	if _, err = tx.Exec("UPDATE autopays SET status='CANCELED',canceled_at=? WHERE id=?", Now(), id); err != nil {
		return nil, err
	}
	return Result{"operation": "banking.autopay_cancel", "status": "CANCELED", "autopay_id": id}, nil
}

func parseRate(value string) (*big.Rat, error) {
	value = strings.TrimSpace(value)
	r := new(big.Rat)
	if value == "" || !strings.Contains(value, ".") && !strings.Contains(value, "/") {
		return nil, fmt.Errorf("FX: Geçerli kur zorunludur.")
	}
	if _, ok := r.SetString(value); !ok || r.Sign() <= 0 {
		return nil, fmt.Errorf("FX: Geçerli kur zorunludur.")
	}
	return r, nil
}

func multiplyRate(amount int64, rate *big.Rat) int64 {
	product := new(big.Rat).Mul(new(big.Rat).SetInt64(amount), rate)
	num, den := product.Num(), product.Denom()
	// Round half up to the target currency's minor unit.
	q, rem := new(big.Int).QuoRem(num, den, new(big.Int))
	if new(big.Int).Lsh(rem, 1).Cmp(den) >= 0 {
		q.Add(q, big.NewInt(1))
	}
	return q.Int64()
}

func bankingFXTrade(tx *sql.Tx, u User, p Payload) (Result, error) {
	if u.Role != "CUSTOMER" {
		if err := staffWrite(u); err != nil {
			return nil, err
		}
	}
	source, err := accountFor(tx, u, p, "source_account_id")
	if err != nil {
		return nil, err
	}
	target, err := accountByID(tx, Int(p, "target_account_id"))
	if err != nil {
		return nil, err
	}
	if u.Role == "CUSTOMER" && ID(target, "customer_id") != u.CustomerID {
		return nil, ErrForbidden
	}
	base, quote := Text(source, "currency"), Text(target, "currency")
	if base == quote {
		return nil, fmt.Errorf("FX: Kaynak ve hedef para birimi farklı olmalıdır.")
	}
	amount, err := bankAmount(p)
	if err != nil {
		return nil, err
	}
	rateText := Str(p, "rate")
	if rateText == "" {
		if err = tx.QueryRow("SELECT rate FROM fx_rates WHERE base_currency=? AND quote_currency=?", base, quote).Scan(&rateText); err != nil {
			return nil, fmt.Errorf("FX: Kur bulunamadı.")
		}
	}
	rate, err := parseRate(rateText)
	if err != nil {
		return nil, err
	}
	targetAmount := multiplyRate(amount, rate)
	if targetAmount <= 0 {
		return nil, fmt.Errorf("FX: Hedef tutar sıfır olamaz.")
	}
	if err = Balance(tx, ID(source, "id"), -amount); err != nil {
		return nil, err
	}
	if err = Balance(tx, ID(target, "id"), targetAmount); err != nil {
		return nil, err
	}
	ref := bankReference(p, "FX")
	if err = Journal(tx, ref+"-BASE", fmt.Sprintf("ACCOUNT:%d", ID(source, "id")), "FX_CLEARING:"+base, amount, base); err != nil {
		return nil, err
	}
	if err = Journal(tx, ref+"-QUOTE", "FX_CLEARING:"+quote, fmt.Sprintf("ACCOUNT:%d", ID(target, "id")), targetAmount, quote); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(`INSERT INTO payments(reference,customer_id,account_id,institution,amount_minor,currency,kind,status,created_by,created_at,completed_at)
 VALUES(?,?,?,?,? ,?,'FX_TRADE','COMPLETED',?,?,?)`, ref, ID(source, "customer_id"), ID(source, "id"), "FX:"+base+"/"+quote, amount, base, u.Username, Now(), Now()); err != nil {
		return nil, err
	}
	return Result{"reference": ref, "operation": "banking.fx_trade", "status": "COMPLETED", "base_amount_minor": amount, "base_currency": base, "target_amount_minor": targetAmount, "target_currency": quote, "rate": rateText}, nil
}

func bankingEasyAddress(tx *sql.Tx, u User, p Payload) (Result, error) {
	if u.Role != "CUSTOMER" {
		if err := staffWrite(u); err != nil {
			return nil, err
		}
	}
	account, err := accountFor(tx, u, p, "account_id")
	if err != nil {
		return nil, err
	}
	address := strings.ToLower(strings.TrimSpace(Str(p, "address")))
	if address == "" || !strings.Contains(address, "@") {
		return nil, fmt.Errorf("ADDRESS: Geçerli kolay adres zorunludur.")
	}
	if _, err = tx.Exec("INSERT INTO easy_addresses(address,customer_id,account_id,status,created_at) VALUES(?,?,?,'ACTIVE',?)", address, ID(account, "customer_id"), ID(account, "id"), Now()); err != nil {
		return nil, fmt.Errorf("ADDRESS: Kolay adres zaten kullanımda: %w", err)
	}
	return Result{"operation": "banking.easy_address_create", "status": "ACTIVE", "address": address, "account_id": ID(account, "id")}, nil
}

func bankingPaymentRequest(tx *sql.Tx, u User, p Payload) (Result, error) {
	if u.Role != "CUSTOMER" {
		if err := staffWrite(u); err != nil {
			return nil, err
		}
	}
	requester, err := accountFor(tx, u, p, "requester_account_id")
	if err != nil {
		return nil, err
	}
	payer, err := accountByID(tx, Int(p, "payer_account_id"))
	if err != nil {
		return nil, err
	}
	if Text(requester, "currency") != Text(payer, "currency") {
		return nil, fmt.Errorf("CURRENCY: Ödeme talebinde para birimleri aynı olmalıdır.")
	}
	amount, err := bankAmount(p)
	if err != nil {
		return nil, err
	}
	ref := bankReference(p, "REQ")
	if _, err = tx.Exec(`INSERT INTO payment_requests(reference,requester_customer_id,requester_account_id,payer_customer_id,payer_account_id,amount_minor,currency,note,status,created_by,created_at)
 VALUES(?,?,?,?,?,?,?,?,'PENDING',?,?)`, ref, ID(requester, "customer_id"), ID(requester, "id"), ID(payer, "customer_id"), ID(payer, "id"), amount, Text(requester, "currency"), Str(p, "note"), u.Username, Now()); err != nil {
		return nil, err
	}
	if err = customerNotify(tx, ID(payer, "customer_id"), "Ödeme talebi", fmt.Sprintf("%d kuruş ödeme talebi aldınız.", amount), ref); err != nil {
		return nil, err
	}
	return amountResult(Result{"reference": ref, "operation": "banking.payment_request", "status": "PENDING", "payer_customer_id": ID(payer, "customer_id")}, amount, Text(requester, "currency")), nil
}

func bankingCustomerUpdate(tx *sql.Tx, u User, p Payload) (Result, error) {
	if u.Role != "CUSTOMER" {
		if err := staffWrite(u); err != nil {
			return nil, err
		}
	}
	id, err := customerIDFor(tx, u, p)
	if err != nil {
		return nil, err
	}
	fields := map[string]string{"phone": Str(p, "phone"), "email": Str(p, "email"), "city": Str(p, "city")}
	changed := 0
	for column, value := range fields {
		if value == "" {
			continue
		}
		if _, err = tx.Exec("UPDATE customers SET "+column+"=? WHERE id=?", value, id); err != nil {
			return nil, err
		}
		changed++
	}
	if changed == 0 {
		return nil, fmt.Errorf("CUSTOMER: Güncellenecek alan bulunamadı.")
	}
	return Result{"operation": "banking.customer_update", "status": "COMPLETED", "customer_id": id}, nil
}

// Banking executes one banking domain operation in the transaction supplied
// by the root service. It never commits: the root envelope owns commit,
// idempotency and the common transaction/audit record.
func (e *Engine) Banking(tx *sql.Tx, u User, op string, p Payload) (Result, bool, error) {
	switch strings.ToLower(strings.TrimSpace(op)) {
	case "banking.cash_open":
		r, err := bankingCashOpen(tx, u, p)
		return r, true, err
	case "banking.cash_close":
		r, err := bankingCashClose(tx, u, p)
		return r, true, err
	case "banking.deposit":
		r, err := bankingDeposit(tx, u, p)
		return r, true, err
	case "banking.withdraw":
		r, err := bankingWithdraw(tx, u, p)
		return r, true, err
	case "banking.account_open":
		r, err := bankingAccountOpen(tx, u, p)
		return r, true, err
	case "banking.account_close":
		r, err := bankingAccountClose(tx, u, p)
		return r, true, err
	case "banking.term_deposit":
		r, err := bankingTermDeposit(tx, u, p)
		return r, true, err
	case "banking.transfer":
		r, err := bankingTransfer(tx, u, p)
		return r, true, err
	case "banking.transfer_recall":
		r, err := bankingRecall(tx, u, p)
		return r, true, err
	case "banking.bill_pay":
		r, err := bankingBillPay(tx, u, p)
		return r, true, err
	case "banking.autopay_create":
		r, err := bankingAutopayCreate(tx, u, p)
		return r, true, err
	case "banking.autopay_cancel":
		r, err := bankingAutopayCancel(tx, u, p)
		return r, true, err
	case "banking.fx_trade":
		r, err := bankingFXTrade(tx, u, p)
		return r, true, err
	case "banking.easy_address_create":
		r, err := bankingEasyAddress(tx, u, p)
		return r, true, err
	case "banking.payment_request":
		r, err := bankingPaymentRequest(tx, u, p)
		return r, true, err
	case "banking.customer_update":
		r, err := bankingCustomerUpdate(tx, u, p)
		return r, true, err
	default:
		return nil, false, nil
	}
}

func queryCustomerFilter(u User, q url.Values, key string) (string, []any, error) {
	id := QueryID(q, "customer_id")
	if u.Role == "CUSTOMER" {
		if id != 0 && id != u.CustomerID {
			return "", nil, ErrForbidden
		}
		return " AND customer_id=?", []any{u.CustomerID}, nil
	}
	if err := staffRead(u); err != nil {
		return "", nil, err
	}
	if id != 0 {
		return " AND " + key + "=?", []any{id}, nil
	}
	return "", nil, nil
}

func queryRows(db *sql.DB, query string, args ...any) (any, error) {
	return Rows(db, query, args...)
}

// BankingQuery exposes only ownership-filtered read models. The bool return
// tells the root router whether this resource belongs to the banking vertical.
func (e *Engine) BankingQuery(u User, resource string, q url.Values) (any, bool, error) {
	resource = strings.ToLower(strings.Trim(strings.TrimSpace(resource), "/"))
	known := map[string]bool{
		"customers": true, "accounts": true, "cash": true, "transfers": true,
		"payments": true, "autopays": true, "easy-addresses": true,
		"payment-requests": true, "fx-rates": true, "dashboard": true,
		"customer-summary": true,
	}
	if !known[resource] {
		return nil, false, nil
	}
	var where string
	var args []any
	var err error
	if resource == "customer-summary" && u.Role == "CUSTOMER" {
		// The session identity is authoritative; a spoofed query parameter is
		// deliberately ignored for this aggregate.
		where, args = "", nil
	} else {
		where, args, err = queryCustomerFilter(u, q, "customer_id")
	}
	if resource == "fx-rates" || resource == "dashboard" {
		where, args, err = "", nil, nil
		if resource == "fx-rates" {
			err = staffRead(u)
		}
		if resource == "dashboard" && u.Role == "CUSTOMER" {
			where, args = " AND customer_id=?", []any{u.CustomerID}
		} else if resource == "dashboard" {
			err = staffRead(u)
		}
	}
	if err != nil {
		return nil, true, err
	}
	var out any
	switch resource {
	case "customers":
		if u.Role == "CUSTOMER" {
			return nil, true, ErrForbidden
		}
		if err = staffRead(u); err != nil {
			return nil, true, err
		}
		search := strings.TrimSpace(q.Get("q"))
		if search == "" {
			out, err = queryRows(e.DB, "SELECT id,code,name,demo_identity,segment,phone,email,city,status,kyc_status FROM customers ORDER BY id")
		} else {
			like := "%" + search + "%"
			out, err = queryRows(e.DB, "SELECT id,code,name,demo_identity,segment,phone,email,city,status,kyc_status FROM customers WHERE name LIKE ? OR code LIKE ? OR demo_identity LIKE ? ORDER BY id", like, like, like)
		}
	case "accounts":
		out, err = queryRows(e.DB, "SELECT * FROM accounts WHERE 1=1"+where+" ORDER BY id", args...)
	case "cash":
		if err = staffRead(u); err == nil {
			out, err = queryRows(e.DB, "SELECT * FROM cash_registers ORDER BY id")
		}
	case "transfers":
		out, err = queryRows(e.DB, "SELECT * FROM banking_transfers WHERE 1=1"+where+" ORDER BY id DESC", args...)
	case "payments":
		out, err = queryRows(e.DB, "SELECT * FROM payments WHERE 1=1"+where+" ORDER BY id DESC", args...)
	case "autopays":
		out, err = queryRows(e.DB, "SELECT * FROM autopays WHERE 1=1"+where+" ORDER BY id DESC", args...)
	case "easy-addresses":
		out, err = queryRows(e.DB, "SELECT * FROM easy_addresses WHERE 1=1"+where+" ORDER BY id DESC", args...)
	case "payment-requests":
		if u.Role == "CUSTOMER" {
			out, err = queryRows(e.DB, "SELECT * FROM payment_requests WHERE requester_customer_id=? OR payer_customer_id=? ORDER BY id DESC", u.CustomerID, u.CustomerID)
		} else {
			if err = staffRead(u); err == nil {
				out, err = queryRows(e.DB, "SELECT * FROM payment_requests ORDER BY id DESC")
			}
		}
	case "fx-rates":
		out, err = queryRows(e.DB, "SELECT * FROM fx_rates ORDER BY base_currency,quote_currency")
	case "dashboard":
		if u.Role != "CUSTOMER" {
			if err = staffRead(u); err != nil {
				return nil, true, err
			}
		}
		var customers, accounts, transfers, payments int64
		if err = e.DB.QueryRow("SELECT COUNT(*) FROM customers WHERE 1=1"+where, args...).Scan(&customers); err == nil {
			if err = e.DB.QueryRow("SELECT COUNT(*) FROM accounts WHERE 1=1"+where, args...).Scan(&accounts); err == nil {
				if err = e.DB.QueryRow("SELECT COUNT(*) FROM banking_transfers WHERE 1=1"+where, args...).Scan(&transfers); err == nil {
					err = e.DB.QueryRow("SELECT COUNT(*) FROM payments WHERE 1=1"+where, args...).Scan(&payments)
				}
			}
		}
		out = Result{"customers": customers, "accounts": accounts, "transfers": transfers, "payments": payments}
	case "customer-summary":
		id := QueryID(q, "customer_id")
		if u.Role == "CUSTOMER" {
			id = u.CustomerID
		} else if err = staffRead(u); err == nil && id == 0 {
			return nil, true, fmt.Errorf("CUSTOMER: customer_id zorunludur.")
		}
		if err != nil {
			return nil, true, err
		}
		customer, lookupErr := Row(e.DB, "SELECT id,code,name,demo_identity,segment,phone,email,city,status,kyc_status FROM customers WHERE id=?", id)
		if lookupErr != nil {
			return nil, true, lookupErr
		}
		accountsRows, lookupErr := Rows(e.DB, "SELECT * FROM accounts WHERE customer_id=? ORDER BY id", id)
		if lookupErr != nil {
			return nil, true, lookupErr
		}
		paymentsRows, lookupErr := Rows(e.DB, "SELECT * FROM payments WHERE customer_id=? ORDER BY id DESC", id)
		if lookupErr != nil {
			return nil, true, lookupErr
		}
		out = Result{"customer": customer, "accounts": accountsRows, "payments": paymentsRows}
	default:
		return nil, false, nil
	}
	return out, true, err
}
