#!/usr/bin/env python3
"""Fill wixl gaps for rollback-capable upgrades and directory-source actions."""
from pathlib import Path
import sys

path = Path(sys.argv[1])
rows = [line.split("\t") for line in path.read_text().splitlines()]
actions = {row[0]: row for row in rows[3:]}
position = int(actions["InstallInitialize"][2]) + 1
if position >= int(actions["ProcessComponents"][2]):
    raise SystemExit("upgrade must precede script-generating actions")
if any(int(row[2]) == position for row in rows[3:] if row[0] != "RemoveExistingProducts"):
    raise SystemExit("upgrade position is occupied")
actions["RemoveExistingProducts"][2] = str(position)
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
        row[3] = '\"[INSTALLDIR]ztg-tftp.exe\" ' + row[3]
actions_path.write_bytes(("\r\n".join("\t".join(row) for row in rows) + "\r\n").encode())
