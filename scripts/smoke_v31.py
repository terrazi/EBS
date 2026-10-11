#!/usr/bin/env python3
"""Verify the real central Server over HTTP, including all bundled role logins."""
import argparse
import json
import time
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen


def verify(base):
    def request(path, *, method="GET", body=None, token=None, status=200):
        headers = {"Content-Type": "application/json"}
        if token:
            headers["Authorization"] = "Bearer " + token
        data = json.dumps(body, ensure_ascii=False).encode() if body is not None else None
        req = Request(base.rstrip("/") + path, data=data, headers=headers, method=method)
        try:
            response = urlopen(req, timeout=5)
        except HTTPError as error:
            response = error
        with response:
            assert response.status == status, f"{path}: HTTP {response.status}, expected {status}"
            assert "application/json" in response.headers.get("Content-Type", ""), path
            return json.load(response)

    deadline = time.monotonic() + 90
    while True:
        try:
            health = request("/health")
            assert health["engine_ready"] and health["ok"]
            assert health["protocol"] == "ebs-v31"
            assert health["build"] == "20261011-startup-fix-1"
            break
        except (AssertionError, URLError, TimeoutError):
            if time.monotonic() >= deadline:
                raise
            time.sleep(0.3)
    roles = [("admin", "EBS2026!", "admin"),
             *[(name, "1234", "personnel") for name in (
                 "gise01", "gise02", "musteritemsilci", "operasyonyetkilisi",
                 "krediyetkilisi", "sigortayetkilisi", "hasaryetkilisi", "denetci")],
             *[(name, "1234", "customer") for name in (
                 "Ahmet Karaca", "Selin Yılmaz", "Zeynep Demir")]]
    for username, password, client in roles:
        login = request("/api/login", method="POST", body={
            "username": username, "password": password, "client": client})
        token = login["token"]
        assert token and login["v3_token"] == token, username
        assert login["expires_at"], username
        session = request("/api/me", token=token)
        assert session["user"]["username"] == username, username
        v3 = request("/v3api/api/v3/session", token=token)
        assert v3["username"] == username, username
        if client == "customer":
            summary = request("/api/customer-portal/summary", token=token)
            assert summary["customer"]["full_name"] == username, username
            assert summary["accounts"], username
            customer_id = int(login["user"]["customer_id"])
            assert all(int(row["customer_id"]) == customer_id for row in summary["accounts"])
        request("/api/logout", method="POST", body={}, token=token)
        request("/api/me", token=token, status=401)
        request("/v3api/api/v3/session", token=token, status=401)
    request("/api/login", method="POST", body={
        "username": "admin", "password": "wrong", "client": "admin"}, status=401)
    request("/api/login", method="POST", body={
        "username": "Ahmet Karaca", "password": "1234", "client": "admin"}, status=401)
    print(f"v3.1 HTTP smoke: ready, {len(roles)} role logins, scoped customer accounts, logout and invalid credentials PASS")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", required=True)
    args = parser.parse_args()
    verify(args.base_url)
