#!/usr/bin/env python3
"""Fill wixl gaps for rollback-capable upgrades and directory-source actions."""
from pathlib import Path
import sys

# wixl resolves Before=/After= relative to other custom actions in an order that
# varies between builds of identical input (hash iteration order). One build of
# the 0.3.1 fixture placed FirewallRestore and FirewallRemove at sequence 1 and 2,
# before CostInitialize, so removal ran them with no directory manager (error
# 2732, result 1603). Every firewall action is therefore pinned to an absolute
# number derived from a standard anchor, then the whole table is verified.
# See tftp/docs/msi-lifecycle-2732.md.
PINNED = (  # (action, anchor, offset)
    ("FirewallRestore", "RemoveShortcuts", 1),
    ("FirewallRemove", "RemoveShortcuts", 2),
    ("FirewallRollback", "InstallFiles", 1),
    ("FirewallRepairRollback", "InstallFiles", 2),
    ("FirewallInstall", "InstallFiles", 3),
)


def pin_firewall(actions):
    for name, anchor, offset in PINNED:
        if name not in actions or anchor not in actions:
            raise SystemExit("missing sequence row: %s or %s" % (name, anchor))
        actions[name][2] = str(int(actions[anchor][2]) + offset)


def verify(actions):
    seq = {name: int(row[2]) for name, row in actions.items()}
    need = ("CostFinalize", "InstallInitialize", "RemoveFiles", "InstallFiles", "InstallFinalize", "RemoveExistingProducts")
    for name in need:
        if name not in seq:
            raise SystemExit("missing sequence row: " + name)
    if len(set(seq.values())) != len(seq):
        raise SystemExit("duplicate sequence numbers")
    for name, _, _ in PINNED:
        if seq[name] <= seq["CostFinalize"]:
            raise SystemExit("%s runs before CostFinalize (directory manager not initialized, error 2732)" % name)
    if not seq["InstallInitialize"] < seq["RemoveExistingProducts"] < seq["InstallFiles"]:
        raise SystemExit("RemoveExistingProducts must follow InstallInitialize and precede InstallFiles")
    for name in ("FirewallRestore", "FirewallRemove"):
        if not seq["InstallInitialize"] < seq[name] < seq["RemoveFiles"]:
            raise SystemExit(name + " must run between InstallInitialize and RemoveFiles")
    if not seq["FirewallRestore"] < seq["FirewallRemove"]:
        raise SystemExit("FirewallRestore must be scheduled before FirewallRemove")
    for name in ("FirewallRollback", "FirewallRepairRollback", "FirewallInstall"):
        if not seq["InstallFiles"] < seq[name] < seq["InstallFinalize"]:
            raise SystemExit(name + " must run between InstallFiles and InstallFinalize")
    if not seq["FirewallRollback"] < seq["FirewallRepairRollback"] < seq["FirewallInstall"]:
        raise SystemExit("firewall install-side actions are out of order")


def main():
    path = Path(sys.argv[1])
    rows = [line.split("\t") for line in path.read_text().splitlines()]
    actions = {row[0]: row for row in rows[3:]}
    position = int(actions["InstallInitialize"][2]) + 1
    if position >= int(actions["ProcessComponents"][2]):
        raise SystemExit("upgrade must precede script-generating actions")
    if any(int(row[2]) == position for row in rows[3:] if row[0] != "RemoveExistingProducts"):
        raise SystemExit("upgrade position is occupied")
    actions["RemoveExistingProducts"][2] = str(position)
    pin_firewall(actions)
    verify(actions)
    path.write_bytes(("\r\n".join("\t".join(row) for row in rows) + "\r\n").encode())

    # Directory-source EXE actions do not require a file to be marked for reinstall.
    # Preserve deferred/rollback/elevation/return flags; change only the source type.
    actions_path = Path(sys.argv[2])
    rows = [line.split("\t") for line in actions_path.read_text().splitlines()]
    for row in rows[3:]:
        if row[0].startswith("Firewall"):
            if row[2] != "TFTPCLI" or row[3] not in ("firewall install", "firewall remove"):
                raise SystemExit("unexpected firewall action")
            row[1] = str((int(row[1]) & ~63) | 34)
            row[2] = "INSTALLDIR"
            row[3] = '"[INSTALLDIR]ztg-tftp.exe" ' + row[3]
    actions_path.write_bytes(("\r\n".join("\t".join(row) for row in rows) + "\r\n").encode())


if __name__ == "__main__":
    main()
