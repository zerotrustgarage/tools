ZTG Scanner
Local network discovery and inventory. https://ztg.sh

ZTGScanner.exe is the desktop app. ztg-scanner.exe is the command-line tool:
  ztg-scanner.exe scan 192.168.1.0/24
  ztg-scanner.exe scan --csv -o site.csv 10.0.0.0/24
  ztg-scanner.exe scan -h

The desktop app requires Windows 10 or 11 x64 and the Microsoft Edge WebView2
Runtime. If it is missing, install it from Microsoft's official website:
https://developer.microsoft.com/microsoft-edge/webview2/
The CLI does not need WebView2. Neither tool needs administrator rights to scan.

MSI: installs both tools and a Start menu shortcut for all users. Installing or
removing the MSI requires administrator rights. For managed deployment:
  msiexec /i ZTG-Scanner-<version>-x64.msi /qn /norestart
  msiexec /x ZTG-Scanner-<version>-x64.msi /qn /norestart
The MSI requires WebView2; provision the runtime for the machine before deploying
through Intune, Group Policy or an RMM. No runtime is downloaded by the installer.

Portable ZIP: extract the entire archive to a writable folder and run either EXE.
No installer or administrator rights are needed. Desktop preferences still use
the current user's WebView2 storage; portable packaging does not isolate settings.

No service, agent, scheduled scan, firewall exception or PATH change is installed.
Nothing starts automatically. Scan only networks you are authorized to inspect.
Results stay local unless you export them. SNMP credentials are never saved.
Uninstalling leaves the user's exported scans and desktop preferences in place.

Verify the download with SHA256SUMS.txt and GitHub's provenance attestations:
  gh attestation verify <download> --repo zerotrustgarage/tools
Authenticode signatures must be valid and identify Proudlock Technology LLC.

THIRD-PARTY-NOTICES.txt contains dependency license texts and data attribution.
scanner.cdx.json is the CycloneDX module inventory of the shipped executables.
