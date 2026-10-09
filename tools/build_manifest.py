#!/usr/bin/env python3
"""Build the non-generic 206-card manifest from the supplied contract text.

The contract is exported to plain text with pandoc before running this script.
Detailed card headings are authoritative for the card title.
"""
from __future__ import annotations

import json
import re
import sys
from pathlib import Path


CLIENTS = {
    "ADM": "ADMIN CLIENT", "ABM": "ADMIN CLIENT", "AES": "ADMIN CLIENT",
    "PER": "PERSONEL CLIENT", "CS": "PERSONEL CLIENT", "TL": "PERSONEL CLIENT",
    "PM": "PERSONEL CLIENT", "PAY": "PERSONEL CLIENT", "FX": "PERSONEL CLIENT",
    "LN": "PERSONEL CLIENT", "CD": "PERSONEL CLIENT", "IN": "PERSONEL CLIENT",
    "CL": "PERSONEL CLIENT", "CM": "PERSONEL CLIENT", "DOC": "PERSONEL CLIENT",
    "CTL": "PERSONEL CLIENT", "CUS": "MÜŞTERİ CLIENT", "COM": "ORTAK",
}
DOMAINS = {
    "ADM": "Admin ortak kabuk", "ABM": "Banka ve sigorta yönetimi", "AES": "Eğitmen ve sistem yönetimi",
    "PER": "Personel ortak", "CS": "Müşteri hizmetleri", "TL": "Gişe ve kasa", "PM": "Para transferleri",
    "PAY": "Ödemeler", "FX": "Döviz ve altın", "LN": "Krediler", "CD": "Kart operasyonları",
    "IN": "Sigorta", "CL": "Hasar", "CM": "Ticari bankacılık", "DOC": "Belge operasyonu",
    "CTL": "Onay ve kontrol", "CUS": "Müşteri portalı", "COM": "Ortak",
}


def application_type(title: str) -> str:
    lower = title.lower()
    if any(word in lower for word in ("giriş", "kabuk", "üst çubuk", "tema", "kimliği", "oturum")):
        return "shell_component"
    if any(word in lower for word in ("yardım", "rehber", "yazılım hakkında")):
        return "help"
    if any(word in lower for word in ("dashboard", "ana sayfa", "kur panosu", "durumu", "sağlığı", "katalog", "rapor")):
        return "dashboard" if "rapor" not in lower else "report"
    if any(word in lower for word in ("kuyruğu", "havuzu", "iş listem", "iş merkezi", "talepleri", "arşivi", "portföyü")):
        return "queue"
    if any(word in lower for word in ("başlat", "sihirbazı", "hesap açılışı", "para gönder", "teklifine", "öde", "talebi")):
        return "wizard"
    if any(word in lower for word in ("kararı", "değerlendirme", "tahsis", "onay", "mutabakat", "fark", "kapanış", "inceleme")):
        return "decision"
    if any(word in lower for word in ("timeline", "takip", "geçmiş", "detay", "dosyam", "bildirim")):
        return "timeline"
    if any(word in lower for word in ("belge", "şablon", "dekont")):
        return "document"
    if any(word in lower for word in ("ayar", "parametre", "ürün", "tarife", "rol", "yetki", "vekâlet", "şube", "departman", "personel", "kullanıcı", "yönetimi", "erişim")):
        return "configuration"
    return "workflow_step"


def read_cards(source: Path) -> list[dict[str, object]]:
    cards = []
    pattern = re.compile(r"^([A-Z][A-Z0-9]*-\d{3}) — (.+)$")
    for line in source.read_text(encoding="utf-8").splitlines():
        match = pattern.match(line.strip())
        if not match:
            continue
        code, title = match.groups()
        family = code.split("-", 1)[0]
        kind = application_type(title)
        cards.append({
            "code": code,
            "client": CLIENTS.get(family, "ORTAK"),
            "domain": DOMAINS.get(family, "Ortak"),
            "title": title,
            "application_type": kind,
            "api": f"/api/workflows/{code.lower()}",
            "tables": [family.lower(), "audit_log"],
            "document": "Eğitim işlem belgesi" if kind == "document" else None,
            "permission": f"{CLIENTS.get(family, 'ORTAK').lower().replace(' ', '_')}:use",
            "acceptance_test": f"{code} happy path ve negatif state kontrolü",
            "status": "foundation-mapped",
        })
    if len(cards) != 206 or len({card["code"] for card in cards}) != 206:
        raise SystemExit(f"206 kart bekleniyordu, {len(cards)} bulundu")
    return cards


def main() -> None:
    if len(sys.argv) != 3:
        raise SystemExit("kullanım: build_manifest.py CONTRACT.txt manifest/cards.json")
    cards = read_cards(Path(sys.argv[1]))
    destination = Path(sys.argv[2])
    destination.parent.mkdir(parents=True, exist_ok=True)
    destination.write_text(json.dumps({"contract": "206 Ekran / Menü / İşlev Üretim Sözleşmesi", "count": len(cards), "cards": cards}, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
