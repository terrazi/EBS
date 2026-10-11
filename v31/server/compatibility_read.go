package main

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"ebs-v3-service/internal/sim"
)

// Compatibility reads use the same customer IDs and records as transaction
// operations. The shell must not show a separate invented customer/account set.
func compatibilityCustomer(row sim.Result) sim.Result {
	full := fmt.Sprint(row["name"])
	parts := strings.Fields(full)
	row["full_name"] = full
	row["name"] = full
	row["surname"] = ""
	if len(parts) > 1 {
		row["name"] = strings.Join(parts[:len(parts)-1], " ")
		row["surname"] = parts[len(parts)-1]
	}
	row["customer_number"] = row["code"]
	row["tckn"] = row["demo_identity"]
	return row
}

func compatibilityRows(e *sim.Engine, u sim.User, resource string, customerID int64) ([]sim.Result, error) {
	// Table names are constants selected here, never request input.
	tables := map[string]string{
		"accounts": "accounts", "cards": "cards", "loans": "credit_loans",
		"policies": "insurance_policies", "transfers": "banking_transfers",
		"payments": "payments",
	}
	table, ok := tables[resource]
	if !ok {
		return []sim.Result{}, nil
	}
	var exists int
	if err := e.DB.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&exists); err != nil {
		return nil, err
	}
	if exists == 0 {
		// Optional vertical schemas are created by their first transaction.
		return []sim.Result{}, nil
	}
	where := ""
	var args []any
	if u.Role == "CUSTOMER" {
		customerID = u.CustomerID
	}
	if customerID > 0 {
		where = " WHERE customer_id=?"
		args = []any{customerID}
	}
	rows, err := sim.Rows(e.DB, "SELECT * FROM "+table+where+" ORDER BY id", args...)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		for minor, display := range map[string]string{"balance_minor": "balance", "amount_minor": "amount", "premium_minor": "premium", "limit_minor": "limit", "principal_minor": "principal", "outstanding_minor": "remaining_principal"} {
			if val, ok := row[minor].(int64); ok {
				row[display] = float64(val) / 100
			}
		}
		if resource == "accounts" {
			row["available_balance"] = row["balance"]
		}
		if resource == "policies" {
			row["policy_number"] = row["policy_no"]
			row["product_name"] = row["product"]
		}
		if resource == "cards" {
			row["masked_card"] = row["masked_number"]
		}
	}
	return rows, nil
}

func compatibilityCustomerSummary(e *sim.Engine, u sim.User, customerID int64) (sim.Result, error) {
	if u.Role == "CUSTOMER" && customerID != u.CustomerID {
		return nil, sim.ErrForbidden
	}
	customer, err := sim.Row(e.DB, "SELECT * FROM customers WHERE id=?", customerID)
	if err != nil {
		return nil, err
	}
	out := sim.Result{"customer": compatibilityCustomer(customer), "claims": []sim.Result{}}
	for _, resource := range []string{"accounts", "cards", "loans", "policies", "transfers", "payments"} {
		rows, err := compatibilityRows(e, u, resource, customerID)
		if err != nil {
			return nil, err
		}
		out[resource] = rows
	}
	return out, nil
}

func servePersistentCompatibility(w http.ResponseWriter, r *http.Request, e *sim.Engine, u sim.User) bool {
	if r.Method != http.MethodGet {
		return false
	}
	p := r.URL.Path
	var out any
	var err error
	switch {
	case p == "/api/users":
		if err = sim.Staff(u); err != nil {
			break
		}
		var rows []sim.Result
		rows, err = sim.Rows(e.DB, "SELECT id,username,display_name AS full_name,role,department,active FROM users WHERE role!='CUSTOMER' ORDER BY id")
		for _, row := range rows {
			row["role"] = compatibilityUser(sim.User{
				Role: fmt.Sprint(row["role"]), Department: fmt.Sprint(row["department"]),
			}).Role
		}
		out = rows
	case p == "/api/customers":
		where := " WHERE 1=1"
		var args []any
		if u.Role == "CUSTOMER" {
			where += " AND id=?"
			args = append(args, u.CustomerID)
		}
		if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
			where += " AND (name LIKE ? OR code LIKE ?)"
			args = append(args, "%"+q+"%", "%"+q+"%")
		}
		var rows []sim.Result
		rows, err = sim.Rows(e.DB, "SELECT * FROM customers"+where+" ORDER BY id", args...)
		for _, row := range rows {
			compatibilityCustomer(row)
		}
		out = rows
	case p == "/api/customer-portal/summary":
		if u.Role != "CUSTOMER" {
			err = sim.ErrForbidden
			break
		}
		out, err = compatibilityCustomerSummary(e, u, u.CustomerID)
	case strings.HasPrefix(p, "/api/customers/"):
		var id int64
		id, err = strconv.ParseInt(strings.TrimPrefix(p, "/api/customers/"), 10, 64)
		if err == nil && id <= 0 {
			err = fmt.Errorf("NOT_FOUND: Müşteri bulunamadı.")
		}
		if err == nil {
			out, err = compatibilityCustomerSummary(e, u, id)
		}
	case p == "/api/accounts" || p == "/api/cards" || p == "/api/loans" || p == "/api/policies" || p == "/api/transfers" || p == "/api/payments" || p == "/api/claims":
		out, err = compatibilityRows(e, u, strings.TrimPrefix(p, "/api/"), 0)
	case p == "/api/dashboard":
		out, _, err = e.BankingQuery(u, "dashboard", r.URL.Query())
	default:
		return false
	}
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, sim.ErrForbidden) {
			status = http.StatusForbidden
		} else if strings.HasPrefix(err.Error(), "NOT_FOUND:") || strings.HasPrefix(p, "/api/customers/") {
			status = http.StatusNotFound
		}
		jsonOut(w, status, map[string]string{"message": err.Error()})
	} else {
		jsonOut(w, http.StatusOK, out)
	}
	return true
}
