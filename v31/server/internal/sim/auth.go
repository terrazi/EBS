package sim

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const passwordIterations = 120000

func hashPassword(password, salt string) string {
	// A small stdlib-only iterated hash keeps this source tree dependency-light.
	// New production deployments should use Argon2id; demo credentials are
	// intentionally isolated in the training database.
	digest := sha256.Sum256([]byte(salt + "\x00" + password))
	buf := digest[:]
	for i := 1; i < passwordIterations; i++ {
		next := sha256.Sum256(append([]byte(salt), buf...))
		buf = next[:]
	}
	return hex.EncodeToString(buf)
}

func newSessionToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func parsePermissions(value string) []string {
	var permissions []string
	if json.Unmarshal([]byte(value), &permissions) != nil {
		return []string{}
	}
	return permissions
}

func clientAllowed(role, client string) bool {
	client = strings.ToLower(strings.TrimSpace(client))
	if client == "" || client == "web" || client == "server" {
		return true
	}
	switch role {
	case "ADMIN":
		return client == "admin" || client == "personel" || client == "personnel"
	case "PERSONEL":
		return client == "personel" || client == "personnel" || client == "admin"
	case "CUSTOMER":
		return client == "musteri" || client == "customer" || client == "client"
	default:
		return false
	}
}

func userFromColumns(id int64, username, displayName, role, department string, customerID sql.NullInt64, permissions string) User {
	var cid int64
	if customerID.Valid {
		cid = customerID.Int64
	}
	return User{ID: id, Username: username, Name: displayName, Role: role, Department: department, CustomerID: cid, Permissions: parsePermissions(permissions)}
}

// Login validates a salted demo credential and creates an expiring central
// session. Passwords are never returned in the Result or User value.
func (e *Engine) Login(username, password, client string) (Result, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return nil, errors.New("AUTH: Kullanıcı adı ve şifre zorunludur.")
	}
	var id int64
	var display, role, department, hash, salt, permissions string
	var customerID sql.NullInt64
	var active int
	err := e.DB.QueryRow(`SELECT id,display_name,role,department,customer_id,password_hash,salt,permissions_json,active
 FROM users WHERE lower(username)=lower(?)`, username).Scan(&id, &display, &role, &department, &customerID, &hash, &salt, &permissions, &active)
	if err != nil || active != 1 || hashPassword(password, salt) != hash {
		return nil, errors.New("AUTH: Kullanıcı adı veya şifre hatalı.")
	}
	if !clientAllowed(role, client) {
		return nil, fmt.Errorf("CLIENT: %s istemcisi bu kullanıcı rolüyle uyumlu değil.", client)
	}
	token, err := newSessionToken()
	if err != nil {
		return nil, fmt.Errorf("AUTH: Oturum üretilemedi: %w", err)
	}
	created := time.Now().UTC().Truncate(time.Second)
	expires := created.Add(8 * time.Hour)
	if _, err = e.DB.Exec(`INSERT INTO sessions(token,user_id,created_at,expires_at) VALUES(?,?,?,?)`, token, id, created.Format(time.RFC3339), expires.Format(time.RFC3339)); err != nil {
		return nil, fmt.Errorf("AUTH: Oturum kaydedilemedi: %w", err)
	}
	u := userFromColumns(id, username, display, role, department, customerID, permissions)
	return Result{
		"token":       token,
		"user":        Result{"id": u.ID, "username": u.Username, "display_name": u.Name, "role": u.Role, "department": u.Department, "customer_id": u.CustomerID, "permissions": u.Permissions},
		"permissions": u.Permissions,
		"expires_at":  expires.Format(time.RFC3339),
	}, nil
}

// Session resolves a bearer token and rejects expired or disabled identities.
func (e *Engine) Session(token string) (User, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return User{}, ErrUnauthorized
	}
	var id int64
	var username, display, role, department, permissions string
	var customerID sql.NullInt64
	var active int
	var expires string
	err := e.DB.QueryRow(`SELECT u.id,u.username,u.display_name,u.role,u.department,u.customer_id,u.permissions_json,u.active,s.expires_at
 FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token=?`, token).Scan(&id, &username, &display, &role, &department, &customerID, &permissions, &active, &expires)
	if err != nil || active != 1 {
		return User{}, ErrUnauthorized
	}
	deadline, err := time.Parse(time.RFC3339, expires)
	if err != nil || !time.Now().UTC().Before(deadline) {
		return User{}, ErrUnauthorized
	}
	return userFromColumns(id, username, display, role, department, customerID, permissions), nil
}

func (e *Engine) Logout(token string) error {
	if strings.TrimSpace(token) == "" {
		return nil
	}
	_, err := e.DB.Exec("DELETE FROM sessions WHERE token=?", strings.TrimSpace(token))
	return err
}
