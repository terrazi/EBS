from __future__ import annotations

import tempfile
import unittest
from pathlib import Path

from ebs.service import EBSService


class EBSServiceTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temp_dir = tempfile.TemporaryDirectory()
        root = Path(self.temp_dir.name)
        self.service = EBSService(root / "ebs.sqlite3", root / "documents")

    def tearDown(self) -> None:
        self.service.close()
        self.temp_dir.cleanup()

    def test_demo_is_connected_and_not_empty(self) -> None:
        counts = self.service.counts()
        self.assertEqual(counts["customers"], 30)
        self.assertEqual(counts["accounts"], 60)
        self.assertEqual(counts["campaigns"], 15)
        profile = self.service.customer_360(1)
        self.assertEqual(profile["demo_identity"], "SCENARIO_DEMO-0001")
        self.assertTrue(profile["accounts"])

    def test_cash_deposit_has_state_journal_audit_document_and_idempotency(self) -> None:
        account = self.service.accounts(1)[0]
        with self.assertRaisesRegex(ValueError, "kasayı açın"):
            self.service.deposit({"customer_id": 1, "account_id": account["id"], "amount": "10.000,00"}, "gise01", "terminal-test", "deposit-closed")
        self.service.open_cash("gise01", "terminal-test")
        before = account["balance_minor"]
        result = self.service.deposit({"customer_id": 1, "account_id": account["id"], "amount": "10.000,00"}, "gise01", "terminal-test", "deposit-1")
        self.assertEqual(result["status"], "COMPLETED")
        self.assertTrue(result["balanced"])
        self.assertTrue((Path(self.temp_dir.name) / "documents" / "CASH-000001_CASH_RECEIPT.pdf").exists())
        replay = self.service.deposit({"customer_id": 1, "account_id": account["id"], "amount": "10.000,00"}, "gise01", "terminal-test", "deposit-1")
        self.assertTrue(replay["idempotent_replay"])
        self.assertEqual(self.service.accounts(1)[0]["balance_minor"], before + 1_000_000)
        self.assertGreaterEqual(len(self.service.audit()), 2)

    def test_transfer_requires_independent_approval_and_is_balanced(self) -> None:
        source = self.service.accounts(1)[0]
        target = self.service.accounts(2)[0]
        pending = self.service.transfer({"source_account_id": source["id"], "target_account_id": target["id"], "amount": "60.000,00", "transfer_type": "FAST"}, "gise01", "terminal-test", "transfer-1")
        self.assertEqual(pending["status"], "PENDING_APPROVAL")
        work = next(item for item in self.service.work_items("PENDING") if item["kind"] == "TRANSFER_APPROVAL")
        with self.assertRaisesRegex(ValueError, "kendi işlemini"):
            self.service.decide_work_item(work["id"], "approve", "gise01", "terminal-test")
        completed = self.service.decide_work_item(work["id"], "approve", "admin01", "terminal-test")
        self.assertEqual(completed["transaction"]["status"], "COMPLETED")
        self.assertTrue(completed["transaction"]["balanced"])

    def test_reset_returns_seed_to_initial_state(self) -> None:
        self.assertEqual(self.service.reset_demo()["customers"], 30)
        self.assertEqual(self.service.transactions(), [])

    def test_v31_login_customer_portal_and_logout(self) -> None:
        staff = self.service.login("gise01", "1234")
        self.assertEqual(staff["role"], "PERSONEL")
        self.assertIn("write", staff["permissions"])
        self.assertEqual(self.service.current_user(staff["token"])["username"], "gise01")

        customer = self.service.login("Ahmet Karaca", "1234")
        self.assertEqual(customer["role"], "CUSTOMER")
        self.assertEqual(customer["customer_id"], 1)
        self.assertIn("self_service", customer["permissions"])
        self.assertTrue(self.service.customer_360(customer["customer_id"])["accounts"])

        self.service.logout(staff["token"])
        self.assertIsNone(self.service.current_user(staff["token"]))

    def test_v31_action_route_is_idempotent_and_queryable(self) -> None:
        first = self.service.record_action("IN-012", "test", {"ok": True}, "gise01", "personnel", "action-1")
        replay = self.service.record_action("IN-012", "test", {"ok": True}, "gise01", "personnel", "action-1")
        self.assertTrue(first["ok"])
        self.assertFalse(first["duplicate"])
        self.assertTrue(replay["duplicate"])
        self.assertEqual(self.service.events()[0]["reference"], first["reference"])
        self.assertEqual(self.service.card_events("in-012")[0]["action"], "test")


if __name__ == "__main__":
    unittest.main()
