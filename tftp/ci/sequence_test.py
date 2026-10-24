#!/usr/bin/env python3
"""Regression tests for installer/sequence.py (MSI lifecycle error 2732)."""
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent.parent / "installer" / "sequence.py"
HEADER = ["Action\tCondition\tSequence", "s72\tS255\tI2", "InstallExecuteSequence\tAction"]
STANDARD = {
    "CostInitialize": 800, "FileCost": 900, "CostFinalize": 1000, "FindRelatedProducts": 25,
    "InstallInitialize": 1500, "ProcessComponents": 1600, "RemoveShortcuts": 3200,
    "RemoveFiles": 3500, "InstallFiles": 4000, "InstallFinalize": 6600,
    "RemoveExistingProducts": 6700,
}
FIREWALL = ("FirewallRestore", "FirewallRemove", "FirewallRollback", "FirewallRepairRollback", "FirewallInstall")
ACTIONS = "Action\tType\tSource\tTarget\nx\tx\tx\tx\nCustomAction\tAction\n" + "".join(
    "%s\t3106\tTFTPCLI\t%s\n" % (n, "firewall remove" if "Remove" in n or n == "FirewallRollback" else "firewall install")
    for n in FIREWALL)


def run(sequence):
    with tempfile.TemporaryDirectory() as tmp:
        seq, act = Path(tmp, "seq.idt"), Path(tmp, "act.idt")
        seq.write_text("\r\n".join(HEADER + ["%s\t\t%d" % kv for kv in sequence.items()]) + "\r\n")
        act.write_text(ACTIONS.replace("\n", "\r\n"))
        proc = subprocess.run([sys.executable, str(SCRIPT), str(seq), str(act)], capture_output=True, text=True)
        rows = {}
        if proc.returncode == 0:
            for line in seq.read_text().splitlines()[3:]:
                f = line.split("\t")
                rows[f[0]] = int(f[2])
        return proc, rows


def good_wixl():
    return dict(STANDARD, **{"FirewallRestore": 3201, "FirewallRemove": 3202, "FirewallRollback": 4001,
                              "FirewallRepairRollback": 4002, "FirewallInstall": 4003})


class SequenceTest(unittest.TestCase):
    def test_pins_known_good_layout(self):
        proc, rows = run(good_wixl())
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertEqual((rows["FirewallRestore"], rows["FirewallRemove"]), (3201, 3202))
        self.assertEqual(rows["RemoveExistingProducts"], 1501)

    def test_repairs_run_39961622459_layout(self):
        # wixl placed both removal actions at 1 and 2, before CostInitialize.
        bad = dict(good_wixl(), FirewallRestore=1, FirewallRemove=2)
        proc, rows = run(bad)
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertGreater(rows["FirewallRestore"], rows["CostFinalize"])
        self.assertGreater(rows["FirewallRemove"], rows["FirewallRestore"])
        self.assertLess(rows["FirewallRemove"], rows["RemoveFiles"])

    def test_any_order_of_wixl_output_ends_valid(self):
        for restore, remove in ((1, 2), (2, 1), (30, 31), (5000, 5001), (900, 4500)):
            proc, rows = run(dict(good_wixl(), FirewallRestore=restore, FirewallRemove=remove))
            self.assertEqual(proc.returncode, 0, proc.stderr)
            self.assertTrue(rows["CostFinalize"] < rows["FirewallRestore"] < rows["FirewallRemove"] < rows["RemoveFiles"])
            self.assertTrue(rows["InstallFiles"] < rows["FirewallRollback"] < rows["FirewallRepairRollback"] < rows["FirewallInstall"] < rows["InstallFinalize"])

    def test_refuses_collision(self):
        proc, _ = run(dict(good_wixl(), RemoveFiles=3201))
        self.assertNotEqual(proc.returncode, 0)

    def test_refuses_missing_anchor(self):
        seq = good_wixl()
        del seq["RemoveShortcuts"]
        proc, _ = run(seq)
        self.assertNotEqual(proc.returncode, 0)


if __name__ == "__main__":
    unittest.main()
