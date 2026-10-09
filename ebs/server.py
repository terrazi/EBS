from __future__ import annotations

import argparse
import json
import mimetypes
import sqlite3
import threading
from http import HTTPStatus
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import parse_qs, unquote, urlparse

from .service import EBSService


ROOT = Path(__file__).resolve().parent.parent
STATIC_DIR = ROOT / "static"


class EBSRequestHandler(BaseHTTPRequestHandler):
    server_version = "EBS/0.1"

    @property
    def ebs_server(self) -> "EBSServer":
        return self.server  # type: ignore[return-value]

    def log_message(self, format: str, *args: object) -> None:
        # Keep operational logs concise and Turkish UI responses clean.
        print(f"[{self.log_date_time_string()}] {format % args}")

    def _actor(self) -> str:
        return self.headers.get("X-EBS-Actor", "gise01")

    def _terminal(self) -> str:
        return self.headers.get("X-EBS-Terminal", "terminal-local")

    def _token(self) -> str | None:
        header = self.headers.get("Authorization", "")
        if header.lower().startswith("bearer "):
            return header[7:].strip()
        return self.headers.get("X-EBS-Session")

    def _user(self, required: bool = False) -> dict | None:
        user = self.ebs_server.service.current_user(self._token())
        if required and not user:
            self._error("Oturum gerekli.", HTTPStatus.UNAUTHORIZED)
            return None
        return user

    def _json_body(self) -> dict:
        length = int(self.headers.get("Content-Length", "0"))
        if length > 1_000_000:
            raise ValueError("İstek gövdesi çok büyük.")
        raw = self.rfile.read(length) if length else b"{}"
        try:
            value = json.loads(raw.decode("utf-8"))
        except (UnicodeDecodeError, json.JSONDecodeError) as exc:
            raise ValueError("Geçerli JSON gönderin.") from exc
        if not isinstance(value, dict):
            raise ValueError("İstek gövdesi nesne olmalıdır.")
        return value

    def _send_json(self, payload: object, status: int = HTTPStatus.OK) -> None:
        body = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        self.wfile.write(body)

    def _send_bytes(self, body: bytes, content_type: str, status: int = HTTPStatus.OK) -> None:
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _error(self, message: str, status: int = HTTPStatus.BAD_REQUEST) -> None:
        self._send_json({"error": message}, status)

    def do_GET(self) -> None:  # noqa: N802
        parsed = urlparse(self.path)
        path = unquote(parsed.path)
        query = parse_qs(parsed.query)
        try:
            if path == "/" or path == "/index.html":
                body = (STATIC_DIR / "index.html").read_bytes()
                return self._send_bytes(body, "text/html; charset=utf-8")
            if path.startswith("/static/"):
                relative = (STATIC_DIR / path.removeprefix("/static/")).resolve()
                if STATIC_DIR not in relative.parents:
                    return self._error("Dosya yolu geçersiz.", HTTPStatus.NOT_FOUND)
                return self._send_bytes(relative.read_bytes(), mimetypes.guess_type(relative.name)[0] or "application/octet-stream")
            if path.startswith("/documents/"):
                relative = Path(path.removeprefix("/documents/"))
                document = (self.ebs_server.service.document_dir / relative).resolve()
                if self.ebs_server.service.document_dir.resolve() not in document.parents:
                    return self._error("Belge yolu geçersiz.", HTTPStatus.NOT_FOUND)
                return self._send_bytes(document.read_bytes(), "application/pdf")
            if path == "/api/health":
                return self._send_json(self.ebs_server.service.health())
            if path == "/health":
                return self._send_json(self.ebs_server.service.health())
            if path == "/api/session":
                user = self._user(required=True)
                if user is not None:
                    return self._send_json(user)
                return
            if path == "/api/me":
                user = self._user(required=True)
                if user is not None:
                    return self._send_json({"user": user, "permissions": ["read", "self_service"] if user["role"] == "CUSTOMER" else ["read", "write"]})
                return
            if path == "/api/dashboard":
                return self._send_json(self.ebs_server.service.dashboard())
            if path == "/api/manifest":
                manifest = ROOT / "manifest" / "cards.json"
                return self._send_bytes(manifest.read_bytes(), "application/json; charset=utf-8")
            if path == "/api/catalog":
                manifest = ROOT / "manifest" / "cards.json"
                return self._send_bytes(manifest.read_bytes(), "application/json; charset=utf-8")
            if path.startswith("/api/card/"):
                code = path.removeprefix("/api/card/").strip()
                if not code:
                    return self._error("Kart kodu zorunludur.")
                return self._send_json({"events": self.ebs_server.service.card_events(code)})
            if path == "/design_cards.json":
                manifest = ROOT / "manifest" / "cards.json"
                return self._send_bytes(manifest.read_bytes(), "application/json; charset=utf-8")
            if path == "/runtime.json":
                return self._send_json({"version": "0.1.0", "port": 8792, "legacy_url": "/legacy/", "legacy_required": False, "server": "central-v3"})
            if path == "/api/users":
                if self._user(required=True) is None:
                    return
                return self._send_json(self.ebs_server.service.users())
            if path == "/api/customer-portal/summary":
                user = self._user(required=True)
                if user is None:
                    return
                if user["role"] != "CUSTOMER" or not user.get("customer_id"):
                    return self._error("Bu endpoint yalnız müşteri oturumu içindir.", HTTPStatus.FORBIDDEN)
                result = self.ebs_server.service.customer_360(int(user["customer_id"]))
                if not result:
                    return self._send_json({"error": "Müşteri bulunamadı."}, HTTPStatus.NOT_FOUND)
                return self._send_json({"customer": {key: result[key] for key in ("id", "code", "name", "demo_identity", "segment", "phone", "email", "city", "status")}, **{key: result[key] for key in ("accounts", "cards", "loans", "policies", "claims")}})
            if path == "/api/customers":
                return self._send_json(self.ebs_server.service.customers(query.get("q", [""])[0]))
            if path.startswith("/api/customers/") and path.endswith("/360"):
                customer_id = int(path.split("/")[3])
                result = self.ebs_server.service.customer_360(customer_id)
                return self._send_json(result or {"error": "Müşteri bulunamadı."}, HTTPStatus.OK if result else HTTPStatus.NOT_FOUND)
            if path == "/api/accounts":
                customer_id = int(query["customer_id"][0]) if query.get("customer_id") else None
                return self._send_json(self.ebs_server.service.accounts(customer_id))
            if path == "/api/campaigns":
                return self._send_json(self.ebs_server.service.campaigns())
            if path == "/api/transactions":
                return self._send_json(self.ebs_server.service.transactions(int(query.get("limit", [50])[0])))
            if path == "/api/audit":
                return self._send_json(self.ebs_server.service.audit(int(query.get("limit", [100])[0])))
            if path == "/api/work-items":
                return self._send_json(self.ebs_server.service.work_items(query.get("status", [None])[0]))
            if path == "/api/events":
                return self._send_json({"events": self.ebs_server.service.events(int(query.get("limit", [100])[0]))})
            return self._error("Kaynak bulunamadı.", HTTPStatus.NOT_FOUND)
        except FileNotFoundError:
            self._error("Kaynak bulunamadı.", HTTPStatus.NOT_FOUND)
        except (ValueError, KeyError) as exc:
            self._error(str(exc))
        except Exception as exc:  # keep server alive while returning useful diagnostics
            self._error(f"Sunucu hatası: {exc}", HTTPStatus.INTERNAL_SERVER_ERROR)

    def do_POST(self) -> None:  # noqa: N802
        path = unquote(urlparse(self.path).path)
        try:
            body = self._json_body()
            service = self.ebs_server.service
            if path == "/api/login":
                username = str(body.get("username", ""))
                password = str(body.get("password", ""))
                try:
                    return self._send_json(service.login(username, password))
                except ValueError as exc:
                    return self._error(str(exc), HTTPStatus.UNAUTHORIZED)
            if path == "/api/logout":
                service.logout(self._token())
                return self._send_json({"status": "signed_out"})
            if path == "/api/demo/reset":
                if self._user(required=True) is None:
                    return
                return self._send_json({"status": "reset", "counts": service.reset_demo()})
            if path == "/api/cash/open":
                user = self._user(required=True)
                if user is None:
                    return
                return self._send_json(service.open_cash(user["username"], self._terminal()))
            if path == "/api/cash/deposit":
                user = self._user(required=True)
                if user is None:
                    return
                return self._send_json(service.deposit(body, user["username"], self._terminal(), self.headers.get("Idempotency-Key")))
            if path == "/api/transfers":
                user = self._user(required=True)
                if user is None:
                    return
                return self._send_json(service.transfer(body, user["username"], self._terminal(), self.headers.get("Idempotency-Key")))
            if path.startswith("/api/work-items/"):
                user = self._user(required=True)
                if user is None:
                    return
                work_item_id = int(path.split("/")[3])
                decision = path.split("/")[4]
                return self._send_json(service.decide_work_item(work_item_id, decision, user["username"], self._terminal()))
            if path in {"/api/action", "/v3api/api/action"}:
                user = self._user(required=True)
                if user is None:
                    return
                return self._send_json(service.record_action(
                    str(body.get("card_code", "")),
                    str(body.get("action", "")),
                    body.get("payload", {}),
                    user["username"],
                    str(body.get("client", "")),
                    self.headers.get("Idempotency-Key"),
                ))
            return self._error("Kaynak bulunamadı.", HTTPStatus.NOT_FOUND)
        except ValueError as exc:
            self._error(str(exc))
        except sqlite3.IntegrityError as exc:  # type: ignore[name-defined]
            self._error(f"Kayıt çakışması: {exc}", HTTPStatus.CONFLICT)
        except Exception as exc:
            self._error(f"Sunucu hatası: {exc}", HTTPStatus.INTERNAL_SERVER_ERROR)


class EBSServer(ThreadingHTTPServer):
    allow_reuse_address = True

    def __init__(self, address: tuple[str, int], service: EBSService):
        super().__init__(address, EBSRequestHandler)
        self.service = service
        self._shutdown_lock = threading.Lock()


def main() -> None:
    parser = argparse.ArgumentParser(description="EBS merkezi eğitim simülasyonu API")
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=8792)
    parser.add_argument("--db", type=Path, default=ROOT / "var" / "ebs.sqlite3")
    args = parser.parse_args()
    service = EBSService(args.db)
    server = EBSServer((args.host, args.port), service)
    print(f"EBS hazır: http://{args.host}:{args.port}/")
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print("\nEBS durduruluyor.")
    finally:
        server.server_close()
        service.close()


if __name__ == "__main__":
    main()
