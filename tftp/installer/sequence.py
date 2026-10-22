#!/usr/bin/env python3
"""Pin the early, rollback-capable major-upgrade position missing from wixl."""
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
