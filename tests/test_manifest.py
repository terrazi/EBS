from __future__ import annotations

import json
import unittest
from pathlib import Path


class ManifestTests(unittest.TestCase):
    def test_contract_has_206_non_generic_cards(self) -> None:
        manifest = json.loads((Path(__file__).parents[1] / "manifest" / "cards.json").read_text(encoding="utf-8"))
        self.assertEqual(manifest["count"], 206)
        self.assertEqual(len(manifest["cards"]), 206)
        self.assertEqual(len({card["code"] for card in manifest["cards"]}), 206)
        self.assertNotIn("generic", {card["application_type"] for card in manifest["cards"]})
        self.assertTrue(all(card["api"] and card["acceptance_test"] for card in manifest["cards"]))


if __name__ == "__main__":
    unittest.main()
