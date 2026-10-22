# Service operation

Service mode is opt-in. Installing the desktop MSI does not install or start a service.
`ztg-tftp service check --config FILE` validates JSON, paths, imports and provisioning rules.
Unknown fields, trailing data and invalid durations are rejected. Relative paths resolve beside FILE.

Copy `config.example.json` and choose your root, bind and allowlist. Keep the configuration,
served files and executables writable only by administrators. Set `upload` to a separate directory
to accept uploads. `upload_max` defaults to 256 MiB and `free_floor` to 1 GiB.
`duration` defaults to `1h`. Only an explicit `"indefinite": true` enables continuous operation.
Collected files and audit logs stay until an operator deletes them. Plan disk monitoring and backups.

## Linux

Install the CLI as `/usr/local/bin/ztg-tftp`, create the `ztg-tftp` system user/group,
and create `/srv/ztg-tftp/images`, `/var/lib/ztg-tftp`, and `/etc/ztg-tftp`.
Grant the service read access to images and its config, and ownership of `/var/lib/ztg-tftp`.
Copy your configuration to `/etc/ztg-tftp/config.json` and the unit to `/etc/systemd/system/`.
Adjust the unit's path restrictions if you choose other paths. The unit grants only the
privileged-port capability and keeps the served tree read-only. Manage it with
`systemctl daemon-reload`, `systemctl start ztg-tftp` and `systemctl stop ztg-tftp`.
Enabling boot startup is a separate operator decision (`systemctl enable ztg-tftp`).
`systemctl reload ztg-tftp` sends SIGHUP.

## Windows

From an administrator terminal, using the installed CLI:

```powershell
& 'C:\Program Files\ZTG TFTP\ztg-tftp.exe' service install --config 'C:\ProgramData\ZTG TFTP\config.json'
Start-Service ZTGTFTP
sc.exe control ZTGTFTP 6
Stop-Service ZTGTFTP
& 'C:\Program Files\ZTG TFTP\ztg-tftp.exe' service remove
```

The service uses LocalService and manual startup. Grant `NT AUTHORITY\LOCAL SERVICE` read
access to config and images and modify access to its audit/drop-box directories; grant those
rights only on the directories you choose. Installation registers the Event Log source.
Stop and remove an explicitly installed service before uninstalling the desktop MSI.
Foreground `service run --config FILE` is also available for diagnosis.

## Reload and forwarding

A reload first parses and validates the next configuration. Invalid configuration leaves the
current session running. A valid reload stops active transfers and starts a new audited generation.
If the new generation cannot start, the previous configuration is restored. A rollback failure stops
the service. Audit generation directories are unique; logs are never replaced.

`"syslog_tls": "logs.example.com:6514"` enables RFC 5424 messages with RFC 6587 octet framing over
TLS 1.2 or later. The destination certificate and hostname must verify against the system trust
store, optionally extended by a PEM `syslog_ca`. No plaintext or insecure TLS fallback exists.
`"event_log": true` forwards to the Windows Application log under provider `ZTGTFTP`.
Forwarding failures stop the generation rather than silently discarding evidence. The local JSONL
file is written first. No file contents or detected credential values are forwarded.

## Provisioning

`remap` entries map an exact requested filename to a contained filename. They are applied once,
without recursive rewriting. `templates` match a filename such as `SEP{mac}.cnf.xml` and select
`phones/{mac}.xml`; `{mac}` must be exactly twelve hex digits and is normalized to lowercase.
These are prewritten per-device files, never dynamic configuration rendering or MAC authentication.
Name policy and os.Root containment still apply. Audit records retain both requested and mapped names.
If a manifest is enabled, it must approve the final mapped filename.
