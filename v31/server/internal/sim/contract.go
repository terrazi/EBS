package sim

import (
 "context"
 "database/sql"
 "encoding/json"
 "errors"
 "fmt"
 "net/url"
 "strconv"
 "strings"
 "sync"
 "time"
)

type Payload map[string]any
type Result map[string]any
type User struct {
 ID int64 `json:"id"`
 Username string `json:"username"`
 Name string `json:"display_name"`
 Role string `json:"role"`
 Department string `json:"department"`
 CustomerID int64 `json:"customer_id"`
 Permissions []string `json:"permissions"`
}
type Engine struct { DB *sql.DB; mu sync.Mutex; Dir string }
var ErrUnauthorized=errors.New("AUTH: Oturum gerekli.")
var ErrForbidden=errors.New("ROLE: Bu işlem için yetkiniz yok.")

func Str(p Payload,k string) string { v,ok:=p[k];if !ok || v==nil{return ""};return strings.TrimSpace(fmt.Sprint(v)) }
func Int(p Payload,k string) int64 { v:=Str(p,k); n,_:=strconv.ParseInt(v,10,64);return n }
func Amount(p Payload,k string)(int64,error){s:=Str(p,k);n,err:=strconv.ParseInt(s,10,64);if err!=nil||n<=0||n>100000000000{return 0,fmt.Errorf("AMOUNT: %s pozitif tamsayı alt birim olmalıdır.",k)};return n,nil}
func Now() string{return time.Now().UTC().Format(time.RFC3339Nano)}
func JSON(v any) string{b,_:=json.Marshal(v);return string(b)}
func Staff(u User)error{if u.Role!="ADMIN"&&u.Role!="PERSONEL"{return ErrForbidden};return nil}
func Admin(u User)error{if u.Role!="ADMIN"{return ErrForbidden};return nil}
func Allowed(u User,domain string)error {
 if u.Role=="ADMIN" {return nil}; if u.Role!="PERSONEL" {return ErrForbidden}
 for _,p:=range u.Permissions {if p=="*"||p==domain||p==domain+":write"{return nil}}
 return ErrForbidden
}
func Row(q interface{Query(string,...any)(*sql.Rows,error)},query string,args ...any)(Result,error){rows,err:=Rows(q,query,args...);if err!=nil{return nil,err};if len(rows)==0{return nil,fmt.Errorf("NOT_FOUND: Kayıt bulunamadı.")};return rows[0],nil}
func Rows(q interface{Query(string,...any)(*sql.Rows,error)},query string,args ...any)([]Result,error){
 r,err:=q.Query(query,args...);if err!=nil{return nil,err};defer r.Close();cols,err:=r.Columns();if err!=nil{return nil,err};out:=[]Result{}
 for r.Next(){values:=make([]any,len(cols));refs:=make([]any,len(cols));for i:=range values{refs[i]=&values[i]};if err=r.Scan(refs...);err!=nil{return nil,err};m:=Result{};for i,c:=range cols{if b,ok:=values[i].([]byte);ok{values[i]=string(b)};m[c]=values[i]};out=append(out,m)};return out,r.Err()
}
func ID(m Result,k string)int64 {switch v:=m[k].(type){case int64:return v;case int:return int64(v);case float64:return int64(v);default:n,_:=strconv.ParseInt(fmt.Sprint(v),10,64);return n}}
func Text(m Result,k string)string{if m[k]==nil{return ""};return fmt.Sprint(m[k])}
func Customer(tx *sql.Tx,u User,id int64)(Result,error){m,err:=Row(tx,"SELECT * FROM customers WHERE id=?",id);if err!=nil{return nil,err};if u.Role=="CUSTOMER"&&u.CustomerID!=id{return nil,ErrForbidden};if Text(m,"status")!="ACTIVE"{return nil,fmt.Errorf("STATE: Müşteri aktif değil.")};return m,nil}
func Account(tx *sql.Tx,u User,id int64)(Result,error){m,err:=Row(tx,"SELECT * FROM accounts WHERE id=?",id);if err!=nil{return nil,err};if _,err=Customer(tx,u,ID(m,"customer_id"));err!=nil{return nil,err};if Text(m,"status")!="ACTIVE"{return nil,fmt.Errorf("STATE: Hesap açık değil.")};return m,nil}
func KYC(tx *sql.Tx,u User,id int64)error{m,err:=Customer(tx,u,id);if err!=nil{return err};if Text(m,"kyc_status")!="VERIFIED"{return fmt.Errorf("KYC: Kimlik doğrulaması tamamlanmalı.")};return nil}
func Balance(tx *sql.Tx,id,delta int64)error{r,err:=tx.Exec("UPDATE accounts SET balance_minor=balance_minor+? WHERE id=? AND status='ACTIVE' AND balance_minor+?>=0",delta,id,delta);if err!=nil{return err};n,_:=r.RowsAffected();if n!=1{return fmt.Errorf("BALANCE: Yetersiz bakiye veya kapalı hesap.")};return nil}
func Notify(tx *sql.Tx,customerID int64,title,body,reference string)error{_,err:=tx.Exec("INSERT INTO notifications(customer_id,title,body,reference,created_at) VALUES(?,?,?,?,?)",customerID,title,body,reference,Now());return err}
// Journal adds the debit and credit together; the execution envelope validates all currencies before commit.
func Journal(tx *sql.Tx,ref,debit,credit string,amount int64,currency string)error{if amount<=0{return fmt.Errorf("AMOUNT: Yevmiye tutarı pozitif olmalı.")};_,err:=tx.Exec("INSERT INTO ledger_entries(reference,account_code,direction,amount_minor,currency) VALUES(?,?,'DEBIT',?,?),(?,?,'CREDIT',?,?)",ref,debit,amount,currency,ref,credit,amount,currency);return err}
func Queue(tx *sql.Tx,kind string,u User,customerID int64,p Payload)(int64,error){r,err:=tx.Exec("INSERT INTO work_items(kind,status,created_by,customer_id,source,payload_json,department,created_at) VALUES(?,'PENDING',?,?,?,? ,?,?)",kind,u.Username,customerID,"APPROVAL_SYSTEM",JSON(p),u.Department,Now());if err!=nil{return 0,err};return r.LastInsertId()}
func Ref(p Payload)string{return Str(p,"_reference")}
func Context()context.Context{return context.Background()}
func OwnerWhere(u User,column string)(string,[]any){if u.Role=="CUSTOMER"{return " WHERE "+column+"=?",[]any{u.CustomerID}};return "",nil}
func QueryID(v url.Values,key string)int64{n,_:=strconv.ParseInt(v.Get(key),10,64);return n}
