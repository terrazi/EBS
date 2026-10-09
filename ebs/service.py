from __future__ import annotations

import json
import secrets
import sqlite3
import uuid
from datetime import datetime, timedelta, timezone
from decimal import Decimal, InvalidOperation, ROUND_HALF_UP
from pathlib import Path
from typing import Any

from .db import initialize, json_text, row_to_dict, rows_to_dict


NAMES = [
    "Ahmet Karaca", "Selin Yılmaz", "Mehmet Aksoy", "Zeynep Demir", "Emre Kaya",
    "Elif Şahin", "Murat Aydın", "Derya Koç", "Burak Arslan", "Seda Yıldız",
    "Oğuzhan Çelik", "Esra Korkmaz", "Serkan Öztürk", "Gülşah Polat", "Hakan Eren",
    "Ceren Tunç", "Onur Kaplan", "Buse Güneş", "Tolga Yalçın", "İrem Kurt",
    "Kaan Özdemir", "Ece Acar", "Volkan Tekin", "Nazlı Ersoy", "Alper Doğan",
    "Pelin Uçar", "Caner Şimşek", "Melis Taş", "Yusuf Çetin", "Damla Kılıç",
]
SEGMENTS = [
    *("Maaş / mevduat" for _ in range(5)),
    *("Döviz / altın" for _ in range(4)),
    *("Kredi" for _ in range(5)),
    *("Kart" for _ in range(4)),
    *("Sigorta" for _ in range(5)),
    *("Hasar" for _ in range(3)),
    *("Esnaf / ticari" for _ in range(2)),
    *("Karma eğitim" for _ in range(2)),
]
CAMPAIGNS = [
    ("Kredi kartı", "Günlük harcamalarda esnek limit", "Kartını güvenli ve kontrollü kullan; ekstre ve ödeme tarihini tek ekranda izle.", "/customer/cards"),
    ("Nakit avans", "Beklenmedik harcamalara çözüm", "İhtiyacın olduğunda limitini ve toplam maliyeti önceden gör.", "/customer/cards"),
    ("İhtiyaç kredisi", "Planına uygun ödeme", "Tutar, vade ve tahmini taksiti birlikte incele.", "/customer/loans"),
    ("Taşıt kredisi", "Yeni yolculuklara hazırlanın", "Başvurunu belgelerin ve ödeme planınla takip et.", "/customer/loans"),
    ("Konut kredisi", "Uzun vadeli hedefler", "Ön değerlendirme ve ödeme planı adımlarını izle.", "/customer/loans"),
    ("Kasko", "Aracına kapsamlı koruma", "Teminatlarını seç, primi ve yenileme tarihini gör.", "/customer/policies"),
    ("Trafik", "Zorunlu güvenceyi zamanında yenile", "Poliçe ve prim hareketlerini tek yerde takip et.", "/customer/policies"),
    ("Konut", "Evin için güvence", "Risklerini ve teminat limitlerini birlikte değerlendir.", "/customer/policies"),
    ("Sağlık", "Sağlıkta yanında", "Teminat ve asistans bilgilerine hızlıca ulaş.", "/customer/policies"),
    ("Seyahat", "Yolculukta destek", "Seyahat öncesi poliçe kapsamını kontrol et.", "/customer/policies"),
    ("BES / birikim", "Geleceğin için düzenli birikim", "Birikim hedefini ve katkılarını takip et.", "/customer/savings"),
    ("Otomatik ödeme", "Son ödeme tarihini kaçırma", "Fatura talimatlarını güvenli biçimde yönet.", "/customer/payments"),
    ("Mevduat", "Birikimini planla", "Vade ve para birimi seçeneklerini karşılaştır.", "/customer/accounts"),
    ("Döviz / altın", "Birikiminde çeşitlilik", "Kur ve gram fiyatını birimli olarak incele.", "/customer/fx"),
    ("Hasar / koruma", "Dosyanı adım adım izle", "Eksik belge ve dosya durumunu tek ekrandan takip et.", "/customer/claims"),
]


def now() -> str:
    return datetime.now(timezone.utc).replace(microsecond=0).isoformat()


def parse_money(value: Any) -> int:
    """Parse a Turkish or invariant decimal amount into kuruş."""
    if isinstance(value, bool) or value is None:
        raise ValueError("Tutar zorunludur.")
    text = str(value).strip().replace("₺", "").replace("TL", "").replace(" ", "")
    if not text:
        raise ValueError("Tutar zorunludur.")
    # Turkish form: 10.000,50. Invariant form: 10000.50.
    if "," in text and "." in text:
        if text.rfind(",") > text.rfind("."):
            text = text.replace(".", "").replace(",", ".")
        else:
            text = text.replace(",", "")
    elif "," in text:
        text = text.replace(",", ".")
    try:
        amount = Decimal(text).quantize(Decimal("0.01"), rounding=ROUND_HALF_UP)
    except InvalidOperation as exc:
        raise ValueError("Tutar sayı olmalıdır.") from exc
    minor = int(amount * 100)
    if minor <= 0:
        raise ValueError("Tutar sıfırdan büyük olmalıdır.")
    return minor


def format_money(minor: int, currency: str = "TRY") -> str:
    value = Decimal(minor) / 100
    text = f"{value:,.2f}".replace(",", "X").replace(".", ",").replace("X", ".")
    labels = {"TRY": "TL", "USD": "USD", "EUR": "EUR", "XAU": "gr."}
    return f"{text} {labels.get(currency, currency)}"


def make_pdf(path: Path, title: str, lines: list[str]) -> None:
    """Write a tiny valid PDF without an external dependency."""
    path.parent.mkdir(parents=True, exist_ok=True)
    safe = [title, *lines, "EĞİTİM AMAÇLIDIR - RESMÎ BELGE DEĞİLDİR"]
    # WinAnsi cannot encode Turkish. Keep the PDF valid and readable with ASCII fallback.
    def pdf_text(value: str) -> str:
        return value.encode("latin-1", "replace").decode("latin-1").replace("\\", "\\\\").replace("(", "\\(").replace(")", "\\)")

    commands = ["BT", "/F1 11 Tf", "50 790 Td"]
    for index, line in enumerate(safe):
        if index:
            commands.append("0 -18 Td")
        commands.append(f"({pdf_text(line)}) Tj")
    commands.append("ET")
    stream = "\n".join(commands).encode("latin-1", "replace")
    objects = [
        b"<< /Type /Catalog /Pages 2 0 R >>",
        b"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
        b"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>",
        b"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
        b"<< /Length " + str(len(stream)).encode() + b" >>\nstream\n" + stream + b"\nendstream",
    ]
    output = bytearray(b"%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
    offsets = [0]
    for number, obj in enumerate(objects, start=1):
        offsets.append(len(output))
        output.extend(f"{number} 0 obj\n".encode())
        output.extend(obj)
        output.extend(b"\nendobj\n")
    xref = len(output)
    output.extend(f"xref\n0 {len(objects) + 1}\n".encode())
    output.extend(b"0000000000 65535 f \n")
    for offset in offsets[1:]:
        output.extend(f"{offset:010d} 00000 n \n".encode())
    output.extend(f"trailer\n<< /Size {len(objects) + 1} /Root 1 0 R >>\nstartxref\n{xref}\n%%EOF\n".encode())
    path.write_bytes(output)


class EBSService:
    def __init__(self, db_path: Path, document_dir: Path | None = None):
        self.db_path = db_path
        self.document_dir = document_dir or db_path.parent / "documents"
        self.document_dir.mkdir(parents=True, exist_ok=True)
        self.db = initialize(db_path)
        self._ensure_v31_users()
        if self.db.execute("SELECT COUNT(*) FROM customers").fetchone()[0] == 0:
            self.reset_demo()

    def close(self) -> None:
        self.db.close()

    def _ensure_v31_users(self) -> None:
        """Migrate the first vertical slice's demo identities to v3.1 names."""
        aliases = {
            "admin01": "admin",
            "temsilci01": "musteritemsilci",
            "krediyetkilisi01": "krediyetkilisi",
            "sigortayetkilisi01": "sigortayetkilisi",
        }
        for old, new in aliases.items():
            self.db.execute("UPDATE users SET username=? WHERE lower(username)=lower(?) AND NOT EXISTS (SELECT 1 FROM users WHERE lower(username)=lower(?))", (new, old, new))
        staff = [
            ("admin", "Sistem Yöneticisi / Eğitmen", "ADMIN", "Sistem Yönetimi", "EBS2026!"),
            ("gise01", "Gişe Yetkilisi 1", "PERSONEL", "Gişe / Kasa", "1234"),
            ("gise02", "Gişe Yetkilisi 2", "PERSONEL", "Gişe / Kasa", "1234"),
            ("musteritemsilci", "Müşteri Temsilcisi", "PERSONEL", "Müşteri Hizmetleri", "1234"),
            ("operasyonyetkilisi", "Operasyon Yetkilisi", "PERSONEL", "Operasyon", "1234"),
            ("krediyetkilisi", "Kredi Yetkilisi", "PERSONEL", "Krediler", "1234"),
            ("sigortayetkilisi", "Sigorta Yetkilisi", "PERSONEL", "Sigorta", "1234"),
            ("hasaryetkilisi", "Hasar Yetkilisi", "PERSONEL", "Hasar", "1234"),
            ("denetci", "Denetçi", "PERSONEL", "Denetim", "1234"),
        ]
        self.db.executemany(
            "INSERT INTO users(username, display_name, role, department, password) VALUES(?,?,?,?,?) ON CONFLICT(username) DO UPDATE SET password=excluded.password, role=excluded.role, department=excluded.department, active=1",
            staff,
        )
        for customer in self.db.execute("SELECT id, name FROM customers ORDER BY id").fetchall():
            self.db.execute(
                "INSERT INTO users(username, display_name, role, department, password, customer_id) VALUES(?,?,?,?,?,?) ON CONFLICT(username) DO UPDATE SET customer_id=excluded.customer_id, role='CUSTOMER', department='Müşteri Portalı', password='1234', active=1",
                (customer["name"], customer["name"], "CUSTOMER", "Müşteri Portalı", "1234", customer["id"]),
            )

    def _sequence(self, name: str) -> int:
        row = self.db.execute("SELECT value FROM meta WHERE key = ?", (name,)).fetchone()
        value = int(row[0]) + 1 if row else 1
        self.db.execute("INSERT INTO meta(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", (name, str(value)))
        return value

    def _reference(self, prefix: str) -> str:
        return f"{prefix}-{self._sequence('sequence') :06d}"

    def _audit(self, action: str, entity_type: str, entity_id: Any, old: Any, new: Any, actor: str, correlation_id: str, terminal: str) -> None:
        self.db.execute(
            "INSERT INTO audit_log(action, entity_type, entity_id, old_state, new_state, actor, correlation_id, terminal, created_at) VALUES(?,?,?,?,?,?,?,?,?)",
            (action, entity_type, str(entity_id), json_text(old), json_text(new), actor, correlation_id, terminal, now()),
        )

    def _balanced(self, transaction_id: int) -> bool:
        row = self.db.execute(
            "SELECT COALESCE(SUM(CASE WHEN direction='DEBIT' THEN amount_minor ELSE 0 END),0) AS debit, COALESCE(SUM(CASE WHEN direction='CREDIT' THEN amount_minor ELSE 0 END),0) AS credit FROM ledger_entries WHERE transaction_id=?",
            (transaction_id,),
        ).fetchone()
        return row["debit"] == row["credit"] and row["debit"] > 0

    def reset_demo(self) -> dict[str, int]:
        self.db.execute("BEGIN IMMEDIATE")
        try:
            for table in ("sessions", "api_events", "notifications", "campaigns", "claims", "policies", "loans", "cards", "documents", "work_items", "audit_log", "ledger_entries", "transactions", "cash_registers", "accounts", "users", "customers", "meta"):
                self.db.execute(f"DELETE FROM {table}")
            timestamp = now()
            users = [
                ("admin", "Sistem Yöneticisi / Eğitmen", "ADMIN", "Sistem Yönetimi", "EBS2026!", None),
                ("gise01", "Gişe Yetkilisi 1", "PERSONEL", "Gişe / Kasa", "1234", None),
                ("gise02", "Gişe Yetkilisi 2", "PERSONEL", "Gişe / Kasa", "1234", None),
                ("musteritemsilci", "Müşteri Temsilcisi", "PERSONEL", "Müşteri Hizmetleri", "1234", None),
                ("operasyonyetkilisi", "Operasyon Yetkilisi", "PERSONEL", "Operasyon", "1234", None),
                ("krediyetkilisi", "Kredi Yetkilisi", "PERSONEL", "Krediler", "1234", None),
                ("sigortayetkilisi", "Sigorta Yetkilisi", "PERSONEL", "Sigorta", "1234", None),
                ("hasaryetkilisi", "Hasar Yetkilisi", "PERSONEL", "Hasar", "1234", None),
                ("denetci", "Denetçi", "PERSONEL", "Denetim", "1234", None),
            ]
            self.db.executemany("INSERT INTO users(username, display_name, role, department, password, customer_id) VALUES(?,?,?,?,?,?)", users)
            for index, name in enumerate(NAMES, start=1):
                city = ["İstanbul", "Ankara", "İzmir", "Bursa", "Antalya"][index % 5]
                self.db.execute(
                    "INSERT INTO customers(code, name, demo_identity, segment, phone, email, city, created_at) VALUES(?,?,?,?,?,?,?,?)",
                    (f"CUST-{index:04d}", name, f"SCENARIO_DEMO-{index:04d}", SEGMENTS[index - 1], f"+90 555 000 {index:04d}", f"demo{index:02d}@ebs-egitim.invalid", city, timestamp),
                )
                customer_id = self.db.execute("SELECT last_insert_rowid()").fetchone()[0]
                self.db.execute(
                    "INSERT INTO users(username, display_name, role, department, password, customer_id) VALUES(?,?,?,?,?,?)",
                    (name, name, "CUSTOMER", "Müşteri Portalı", "1234", customer_id),
                )
                primary_currency = "TRY" if index <= 18 or index >= 27 else ("USD" if index % 2 else "EUR")
                for account_index, currency in enumerate((primary_currency, "TRY" if primary_currency != "TRY" else "USD"), start=1):
                    iban = f"TR00 0001 0000 0000 0000 {index:04d} {account_index:02d}"
                    # Keep enough deterministic headroom for the approval-path demo
                    # while preserving varied balances across the 30 profiles.
                    balance = (index * 173_00) + (account_index * 100_000_00)
                    self.db.execute(
                        "INSERT INTO accounts(customer_id, iban, account_type, currency, balance_minor, opened_at) VALUES(?,?,?,?,?,?)",
                        (customer_id, iban, "Vadesiz" if account_index == 1 else "Birikim", currency, balance, timestamp),
                    )
                if index <= 15:
                    self.db.execute("INSERT INTO cards(customer_id, card_type, status, limit_minor, masked_number) VALUES(?,?,?,?,?)", (customer_id, "Kredi Kartı", "ACTIVE", (index + 2) * 100_000_00, f"**** **** **** {1000 + index}"))
                if 10 <= index <= 18:
                    principal = (index - 8) * 100_000_00
                    self.db.execute("INSERT INTO loans(customer_id, product, principal_minor, outstanding_minor, status) VALUES(?,?,?,?,?)", (customer_id, "İhtiyaç Kredisi" if index % 2 else "Taşıt Kredisi", principal, principal - 10_000_00, "ACTIVE"))
                if 19 <= index <= 26:
                    policy_id = self.db.execute("INSERT INTO policies(customer_id, product, policy_no, premium_minor, status) VALUES(?,?,?,?,?)", (customer_id, ["Kasko", "Trafik", "Konut", "Sağlık"][index % 4], f"POL-2026-{index:04d}", (index + 1) * 1_250_00, "ACTIVE")).lastrowid
                    if 24 <= index <= 26:
                        self.db.execute("INSERT INTO claims(customer_id, policy_id, claim_no, status, reserve_minor) VALUES(?,?,?,?,?)", (customer_id, policy_id, f"CLM-2026-{index:04d}", ["OPEN", "IN_REVIEW", "PAID"][index - 24], (index - 23) * 50_000_00))
            self.db.execute("INSERT INTO cash_registers(branch, currency, status, balance_minor) VALUES(?,?,?,?)", ("Merkez Şube", "TRY", "CLOSED", 0))
            self.db.executemany("INSERT INTO campaigns(theme, title, description, target_path) VALUES(?,?,?,?)", CAMPAIGNS)
            # A realistic pending item demonstrates maker-checker on first load.
            self.db.execute("INSERT INTO work_items(kind, reference, status, created_by, payload_json, created_at) VALUES(?,?,?,?,?,?)", ("DEMO_REVIEW", "EBS-WORK-000001", "PENDING", "gise01", json_text({"message": "Demo eğitim kontrolü", "next_step": "Bağımsız yetkili incelemesi"}), timestamp))
            self.db.execute("INSERT INTO meta(key, value) VALUES('sequence', '0')")
            self.db.execute("COMMIT")
        except Exception:
            self.db.execute("ROLLBACK")
            raise
        return self.counts()

    def counts(self) -> dict[str, int]:
        return {table: self.db.execute(f"SELECT COUNT(*) FROM {table}").fetchone()[0] for table in ("customers", "accounts", "cards", "loans", "policies", "claims", "campaigns")}

    def health(self) -> dict[str, Any]:
        return {"status": "ok", "service": "EBS Merkezi İşlem Motoru", "version": "0.1.0", "port": 8792, "legacy_optional": True, "login": "central", "database": str(self.db_path), "demo": self.counts()}

    def login(self, username: str, password: str) -> dict[str, Any]:
        row = self.db.execute(
            "SELECT id, username, display_name, role, department, customer_id, active FROM users WHERE lower(username)=lower(?) AND password=? AND active=1",
            (username.strip(), password),
        ).fetchone()
        if not row:
            raise ValueError("Kullanıcı adı veya şifre hatalı.")
        token = secrets.token_urlsafe(32)
        created = datetime.now(timezone.utc)
        expires = created.replace(microsecond=0) + timedelta(hours=8)
        self.db.execute(
            "INSERT INTO sessions(token, user_id, created_at, expires_at) VALUES(?,?,?,?)",
            (token, row["id"], created.replace(microsecond=0).isoformat(), expires.isoformat()),
        )
        result = dict(row)
        result["token"] = token
        result["permissions"] = ["read", "write"] if row["role"] != "CUSTOMER" else ["read", "self_service"]
        return result

    def current_user(self, token: str | None) -> dict[str, Any] | None:
        if not token:
            return None
        row = self.db.execute(
            "SELECT u.id, u.username, u.display_name, u.role, u.department, u.customer_id, u.active FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token=? AND s.expires_at>? AND u.active=1",
            (token, now()),
        ).fetchone()
        return row_to_dict(row)

    def logout(self, token: str | None) -> None:
        if token:
            self.db.execute("DELETE FROM sessions WHERE token=?", (token,))

    def users(self) -> list[dict[str, Any]]:
        return rows_to_dict(self.db.execute("SELECT id, username, display_name, role, department, customer_id, active FROM users WHERE role <> 'CUSTOMER' ORDER BY id").fetchall())

    def dashboard(self) -> dict[str, Any]:
        cash = row_to_dict(self.db.execute("SELECT * FROM cash_registers ORDER BY id LIMIT 1").fetchone())
        if cash:
            cash["balance_display"] = format_money(cash["balance_minor"], cash["currency"])
        recent = rows_to_dict(self.db.execute("SELECT reference, kind, status, amount_minor, currency, actor, created_at FROM transactions ORDER BY id DESC LIMIT 8").fetchall())
        for item in recent:
            item["amount_display"] = format_money(item["amount_minor"], item["currency"])
        pending = self.db.execute("SELECT COUNT(*) FROM work_items WHERE status='PENDING'").fetchone()[0]
        return {"counts": self.counts(), "cash_register": cash, "pending_work_items": pending, "recent_transactions": recent}

    def customers(self, query: str = "") -> list[dict[str, Any]]:
        query = query.strip()
        if query:
            pattern = f"%{query}%"
            rows = self.db.execute("SELECT id, code, name, demo_identity, segment, city, status FROM customers WHERE name LIKE ? OR code LIKE ? OR demo_identity LIKE ? ORDER BY id", (pattern, pattern, pattern)).fetchall()
        else:
            rows = self.db.execute("SELECT id, code, name, demo_identity, segment, city, status FROM customers ORDER BY id").fetchall()
        return rows_to_dict(rows)

    def customer_360(self, customer_id: int) -> dict[str, Any] | None:
        customer = row_to_dict(self.db.execute("SELECT * FROM customers WHERE id=?", (customer_id,)).fetchone())
        if not customer:
            return None
        for key, sql in {
            "accounts": "SELECT id, iban, account_type, currency, balance_minor, status FROM accounts WHERE customer_id=? ORDER BY id",
            "cards": "SELECT id, card_type, status, limit_minor, masked_number FROM cards WHERE customer_id=? ORDER BY id",
            "loans": "SELECT id, product, principal_minor, outstanding_minor, status FROM loans WHERE customer_id=? ORDER BY id",
            "policies": "SELECT id, product, policy_no, premium_minor, status, version FROM policies WHERE customer_id=? ORDER BY id",
            "claims": "SELECT id, claim_no, status, reserve_minor, policy_id FROM claims WHERE customer_id=? ORDER BY id",
        }.items():
            customer[key] = rows_to_dict(self.db.execute(sql, (customer_id,)).fetchall())
        for account in customer["accounts"]:
            account["balance_display"] = format_money(account["balance_minor"], account["currency"])
        for card in customer["cards"]:
            card["limit_display"] = format_money(card["limit_minor"], "TRY")
        for loan in customer["loans"]:
            loan["outstanding_display"] = format_money(loan["outstanding_minor"], "TRY")
        for policy in customer["policies"]:
            policy["premium_display"] = format_money(policy["premium_minor"], "TRY")
        return customer

    def accounts(self, customer_id: int | None = None) -> list[dict[str, Any]]:
        if customer_id:
            rows = self.db.execute("SELECT a.*, c.name AS customer_name FROM accounts a JOIN customers c ON c.id=a.customer_id WHERE customer_id=? ORDER BY a.id", (customer_id,)).fetchall()
        else:
            rows = self.db.execute("SELECT a.*, c.name AS customer_name FROM accounts a JOIN customers c ON c.id=a.customer_id ORDER BY a.id").fetchall()
        result = rows_to_dict(rows)
        for item in result:
            item["balance_display"] = format_money(item["balance_minor"], item["currency"])
        return result

    def campaigns(self) -> list[dict[str, Any]]:
        return rows_to_dict(self.db.execute("SELECT * FROM campaigns ORDER BY id").fetchall())

    def transactions(self, limit: int = 50) -> list[dict[str, Any]]:
        limit = max(1, min(limit, 200))
        rows = rows_to_dict(self.db.execute("SELECT t.*, c.name AS customer_name FROM transactions t LEFT JOIN customers c ON c.id=t.customer_id ORDER BY t.id DESC LIMIT ?", (limit,)).fetchall())
        for item in rows:
            item["amount_display"] = format_money(item["amount_minor"], item["currency"])
            item["payload"] = json.loads(item.pop("payload_json"))
        return rows

    def audit(self, limit: int = 100) -> list[dict[str, Any]]:
        return rows_to_dict(self.db.execute("SELECT * FROM audit_log ORDER BY id DESC LIMIT ?", (max(1, min(limit, 300)),)).fetchall())

    def work_items(self, status: str | None = None) -> list[dict[str, Any]]:
        if status:
            rows = self.db.execute("SELECT * FROM work_items WHERE status=? ORDER BY id DESC", (status,)).fetchall()
        else:
            rows = self.db.execute("SELECT * FROM work_items ORDER BY id DESC").fetchall()
        result = rows_to_dict(rows)
        for item in result:
            item["payload"] = json.loads(item.pop("payload_json"))
        return result

    def record_action(self, card_code: str, action: str, payload: Any, user: str, client: str = "", idempotency_key: str | None = None) -> dict[str, Any]:
        card_code = card_code.strip().upper()
        action = action.strip()
        if not card_code or not action:
            raise ValueError("Kart kodu ve aksiyon zorunludur.")
        if idempotency_key:
            existing = self.db.execute("SELECT * FROM api_events WHERE idempotency_key=?", (idempotency_key,)).fetchone()
            if existing:
                result = dict(existing)
                result["payload"] = json.loads(result.pop("payload_json"))
                result["duplicate"] = True
                return result
        reference = self._reference("EBS3")
        self.db.execute(
            "INSERT INTO api_events(reference, card_code, action, payload_json, user, client, status, idempotency_key, created_at) VALUES(?,?,?,?,?,?,?,?,?)",
            (reference, card_code, action, json_text(payload or {}), user, client, "KAYDEDİLDİ", idempotency_key, now()),
        )
        return {
            "ok": True,
            "reference": reference,
            "card_code": card_code,
            "action": action,
            "status": "KAYDEDİLDİ",
            "user": user,
            "client": client,
            "duplicate": False,
        }

    def events(self, limit: int = 100) -> list[dict[str, Any]]:
        rows = rows_to_dict(self.db.execute("SELECT * FROM api_events ORDER BY id DESC LIMIT ?", (max(1, min(limit, 500)),)).fetchall())
        for row in rows:
            row["payload"] = json.loads(row.pop("payload_json"))
        return rows

    def card_events(self, card_code: str, limit: int = 50) -> list[dict[str, Any]]:
        rows = rows_to_dict(self.db.execute("SELECT * FROM api_events WHERE upper(card_code)=upper(?) ORDER BY id DESC LIMIT ?", (card_code, max(1, min(limit, 200)))).fetchall())
        for row in rows:
            row["payload"] = json.loads(row.pop("payload_json"))
        return rows

    def open_cash(self, actor: str, terminal: str) -> dict[str, Any]:
        correlation = str(uuid.uuid4())
        self.db.execute("BEGIN IMMEDIATE")
        try:
            cash = self.db.execute("SELECT * FROM cash_registers ORDER BY id LIMIT 1").fetchone()
            if not cash:
                raise ValueError("Kasa tanımlı değil.")
            if cash["status"] == "OPEN":
                self.db.execute("COMMIT")
                return {**dict(cash), "already_open": True}
            timestamp = now()
            self.db.execute("UPDATE cash_registers SET status='OPEN', opened_by=?, opened_at=? WHERE id=?", (actor, timestamp, cash["id"]))
            self._audit("CASH_OPEN", "cash_register", cash["id"], {"status": cash["status"]}, {"status": "OPEN"}, actor, correlation, terminal)
            self.db.execute("COMMIT")
            return {**dict(self.db.execute("SELECT * FROM cash_registers WHERE id=?", (cash["id"],)).fetchone()), "correlation_id": correlation}
        except Exception:
            self.db.execute("ROLLBACK")
            raise

    def _find_idempotent(self, key: str | None) -> dict[str, Any] | None:
        if not key:
            return None
        row = self.db.execute("SELECT * FROM transactions WHERE idempotency_key=?", (key,)).fetchone()
        if not row:
            return None
        result = dict(row)
        result["idempotent_replay"] = True
        result["balanced"] = self._balanced(result["id"])
        return result

    def deposit(self, payload: dict[str, Any], actor: str, terminal: str, idempotency_key: str | None = None) -> dict[str, Any]:
        key = idempotency_key or payload.get("idempotency_key")
        existing = self._find_idempotent(key)
        if existing:
            return existing
        amount = parse_money(payload.get("amount", payload.get("amount_minor", 0))) if "amount_minor" not in payload else int(payload["amount_minor"])
        if amount <= 0:
            raise ValueError("Tutar sıfırdan büyük olmalıdır.")
        account_id = int(payload.get("account_id", 0))
        customer_id = int(payload.get("customer_id", 0))
        correlation = str(uuid.uuid4())
        self.db.execute("BEGIN IMMEDIATE")
        try:
            cash = self.db.execute("SELECT * FROM cash_registers ORDER BY id LIMIT 1").fetchone()
            account = self.db.execute("SELECT * FROM accounts WHERE id=? AND customer_id=?", (account_id, customer_id)).fetchone()
            if not cash or cash["status"] != "OPEN":
                raise ValueError("İşleme devam etmek için kasayı açın.")
            if not account or account["status"] != "ACTIVE":
                raise ValueError("Müşterinin aktif hesabı bulunamadı.")
            reference = self._reference("CASH")
            timestamp = now()
            transaction_id = self.db.execute(
                "INSERT INTO transactions(reference, kind, status, idempotency_key, actor, correlation_id, customer_id, target_account_id, amount_minor, currency, payload_json, created_at, completed_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)",
                (reference, "CASH_DEPOSIT", "COMPLETED", key, actor, correlation, customer_id, account_id, amount, account["currency"], json_text(payload), timestamp, timestamp),
            ).lastrowid
            self.db.execute("UPDATE accounts SET balance_minor=balance_minor+? WHERE id=?", (amount, account_id))
            self.db.execute("UPDATE cash_registers SET balance_minor=balance_minor+? WHERE id=?", (amount, cash["id"]))
            self.db.execute("INSERT INTO ledger_entries(transaction_id, account_code, direction, amount_minor, currency) VALUES(?,?,?,?,?)", (transaction_id, f"CASH_REGISTER:{cash['id']}", "DEBIT", amount, account["currency"]))
            self.db.execute("INSERT INTO ledger_entries(transaction_id, account_code, direction, amount_minor, currency) VALUES(?,?,?,?,?)", (transaction_id, f"ACCOUNT:{account_id}", "CREDIT", amount, account["currency"]))
            if not self._balanced(transaction_id):
                raise ValueError("Jurnal dengesi sağlanamadı.")
            self._audit("CASH_DEPOSIT", "transaction", transaction_id, {"balance_minor": account["balance_minor"]}, {"balance_minor": account["balance_minor"] + amount}, actor, correlation, terminal)
            document = self._document(transaction_id, "CASH_RECEIPT", reference, [f"Müşteri: {self.db.execute('SELECT name FROM customers WHERE id=?', (customer_id,)).fetchone()[0]}", f"Tutar: {format_money(amount, account['currency'])}", f"Hesap: {account['iban']}"])
            self.db.execute("COMMIT")
            return self.transaction_result(transaction_id, document)
        except Exception:
            self.db.execute("ROLLBACK")
            raise

    def _document(self, transaction_id: int, document_type: str, reference: str, lines: list[str]) -> dict[str, Any]:
        relative = f"{reference}_{document_type}.pdf"
        path = self.document_dir / relative
        make_pdf(path, f"EBS {document_type} - {reference}", lines)
        document_id = self.db.execute("INSERT INTO documents(transaction_id, document_type, path, training_notice, created_at) VALUES(?,?,?,?,?)", (transaction_id, document_type, relative, "EĞİTİM AMAÇLIDIR - RESMÎ BELGE DEĞİLDİR", now())).lastrowid
        return {"id": document_id, "type": document_type, "path": f"/documents/{relative}", "training_notice": "EĞİTİM AMAÇLIDIR - RESMÎ BELGE DEĞİLDİR"}

    def transaction_result(self, transaction_id: int, document: dict[str, Any] | None = None) -> dict[str, Any]:
        result = row_to_dict(self.db.execute("SELECT * FROM transactions WHERE id=?", (transaction_id,)).fetchone()) or {}
        result["balanced"] = self._balanced(transaction_id)
        result["amount_display"] = format_money(result["amount_minor"], result["currency"])
        if document:
            result["document"] = document
        return result

    def transfer(self, payload: dict[str, Any], actor: str, terminal: str, idempotency_key: str | None = None) -> dict[str, Any]:
        key = idempotency_key or payload.get("idempotency_key")
        existing = self._find_idempotent(key)
        if existing:
            return existing
        amount = parse_money(payload.get("amount", payload.get("amount_minor", 0))) if "amount_minor" not in payload else int(payload["amount_minor"])
        if amount <= 0:
            raise ValueError("Tutar sıfırdan büyük olmalıdır.")
        source_id = int(payload.get("source_account_id", 0))
        target_id = int(payload.get("target_account_id", 0))
        if source_id == target_id:
            raise ValueError("Kaynak ve hedef hesap aynı olamaz.")
        correlation = str(uuid.uuid4())
        self.db.execute("BEGIN IMMEDIATE")
        try:
            source = self.db.execute("SELECT * FROM accounts WHERE id=?", (source_id,)).fetchone()
            target = self.db.execute("SELECT * FROM accounts WHERE id=?", (target_id,)).fetchone()
            if not source or not target or source["status"] != "ACTIVE" or target["status"] != "ACTIVE":
                raise ValueError("Kaynak veya hedef hesap aktif değil.")
            if source["currency"] != target["currency"]:
                raise ValueError("Kaynak ve hedef para birimi aynı olmalıdır.")
            if source["balance_minor"] < amount:
                raise ValueError("Kaynak hesap bakiyesi yetersiz.")
            reference = self._reference("TRF")
            timestamp = now()
            requires_approval = amount > 5_000_000
            status = "PENDING_APPROVAL" if requires_approval else "COMPLETED"
            transaction_id = self.db.execute(
                "INSERT INTO transactions(reference, kind, status, idempotency_key, actor, correlation_id, customer_id, source_account_id, target_account_id, amount_minor, currency, payload_json, created_at, completed_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
                (reference, payload.get("transfer_type", "HAVALE"), status, key, actor, correlation, source["customer_id"], source_id, target_id, amount, source["currency"], json_text(payload), timestamp, None if requires_approval else timestamp),
            ).lastrowid
            if requires_approval:
                work_reference = self._reference("WORK")
                self.db.execute("INSERT INTO work_items(kind, reference, status, created_by, payload_json, created_at) VALUES(?,?,?,?,?,?)", ("TRANSFER_APPROVAL", work_reference, "PENDING", actor, json_text({"transaction_id": transaction_id, "transaction_reference": reference, "amount": format_money(amount, source["currency"]), "source_account_id": source_id, "target_account_id": target_id}), timestamp))
                self._audit("TRANSFER_SUBMIT", "transaction", transaction_id, {"status": "NEW"}, {"status": status}, actor, correlation, terminal)
                self.db.execute("COMMIT")
                return self.transaction_result(transaction_id)
            self._apply_transfer(transaction_id, source, target, amount, actor, terminal, correlation)
            document = self._document(transaction_id, "TRANSFER_RECEIPT", reference, [f"Kaynak: {source['iban']}", f"Hedef: {target['iban']}", f"Tutar: {format_money(amount, source['currency'])}"])
            self.db.execute("COMMIT")
            return self.transaction_result(transaction_id, document)
        except Exception:
            self.db.execute("ROLLBACK")
            raise

    def _apply_transfer(self, transaction_id: int, source: sqlite3.Row, target: sqlite3.Row, amount: int, actor: str, terminal: str, correlation: str) -> None:
        self.db.execute("UPDATE accounts SET balance_minor=balance_minor-? WHERE id=?", (amount, source["id"]))
        self.db.execute("UPDATE accounts SET balance_minor=balance_minor+? WHERE id=?", (amount, target["id"]))
        self.db.execute("INSERT INTO ledger_entries(transaction_id, account_code, direction, amount_minor, currency) VALUES(?,?,?,?,?)", (transaction_id, f"ACCOUNT:{source['id']}", "DEBIT", amount, source["currency"]))
        self.db.execute("INSERT INTO ledger_entries(transaction_id, account_code, direction, amount_minor, currency) VALUES(?,?,?,?,?)", (transaction_id, f"ACCOUNT:{target['id']}", "CREDIT", amount, target["currency"]))
        self.db.execute("UPDATE transactions SET status='COMPLETED', completed_at=? WHERE id=?", (now(), transaction_id))
        if not self._balanced(transaction_id):
            raise ValueError("Jurnal dengesi sağlanamadı.")
        self._audit("TRANSFER_COMPLETE", "transaction", transaction_id, {"status": "PENDING_APPROVAL"}, {"status": "COMPLETED"}, actor, correlation, terminal)

    def decide_work_item(self, work_item_id: int, decision: str, actor: str, terminal: str) -> dict[str, Any]:
        if decision not in {"approve", "reject"}:
            raise ValueError("Geçersiz onay kararı.")
        correlation = str(uuid.uuid4())
        self.db.execute("BEGIN IMMEDIATE")
        try:
            item = self.db.execute("SELECT * FROM work_items WHERE id=?", (work_item_id,)).fetchone()
            if not item or item["status"] != "PENDING":
                raise ValueError("Bekleyen iş bulunamadı.")
            if item["created_by"] == actor:
                raise ValueError("İşlemi başlatan kişi kendi işlemini onaylayamaz.")
            payload = json.loads(item["payload_json"])
            if item["kind"] == "TRANSFER_APPROVAL" and decision == "approve":
                transaction_id = int(payload["transaction_id"])
                transaction = self.db.execute("SELECT * FROM transactions WHERE id=?", (transaction_id,)).fetchone()
                source = self.db.execute("SELECT * FROM accounts WHERE id=?", (transaction["source_account_id"],)).fetchone()
                target = self.db.execute("SELECT * FROM accounts WHERE id=?", (transaction["target_account_id"],)).fetchone()
                if not transaction or transaction["status"] != "PENDING_APPROVAL" or not source or not target:
                    raise ValueError("Onay bekleyen transfer artık geçerli değil.")
                if source["balance_minor"] < transaction["amount_minor"]:
                    raise ValueError("Onay sırasında kaynak bakiye yetersiz.")
                self._apply_transfer(transaction_id, source, target, transaction["amount_minor"], actor, terminal, correlation)
                self._document(transaction_id, "TRANSFER_RECEIPT", transaction["reference"], [f"Kaynak: {source['iban']}", f"Hedef: {target['iban']}", f"Tutar: {format_money(transaction['amount_minor'], transaction['currency'])}"])
            else:
                transaction_id = int(payload.get("transaction_id", 0))
                if transaction_id:
                    self.db.execute("UPDATE transactions SET status='REJECTED', completed_at=? WHERE id=?", (now(), transaction_id))
            status = "APPROVED" if decision == "approve" else "REJECTED"
            self.db.execute("UPDATE work_items SET status=?, assigned_to=?, completed_at=? WHERE id=?", (status, actor, now(), work_item_id))
            self._audit("WORK_DECISION", "work_item", work_item_id, {"status": "PENDING"}, {"status": status}, actor, correlation, terminal)
            self.db.execute("COMMIT")
            return {"work_item_id": work_item_id, "status": status, "transaction": self.transaction_result(transaction_id) if transaction_id else None}
        except Exception:
            self.db.execute("ROLLBACK")
            raise
