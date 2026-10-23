ZTG TFTP

Start ZTGTFTP.exe for the desktop app, or run ztg-tftp.exe help for the CLI.
Microsoft Edge WebView2 Runtime is required for the desktop app.

TFTP has no authentication or encryption. Never use for CUI. Prefer SCP,
SFTP or HTTPS where the device supports them. Allowlisting is network access
control and does not authenticate a device or user.

The MSI installs under Program Files and adds inbound UDP program rules on
Domain and Private profiles only. It does not install or start a service.
The portable ZIP changes no firewall settings. Ask your administrator to
permit the app on the intended network if remote devices cannot connect.

Sessions last 60 minutes by default. Closing the app stops its server.
Uploads are opt-in, go to a separate private drop box and never overwrite
or become served automatically. Collected files remain until deleted.

Service configuration, approved-image manifests and Scanner JSON import:
https://github.com/zerotrustgarage/tools/tree/main/tftp

Verify SHA256SUMS.txt and the release's GitHub provenance attestations.
Windows release binaries and MSI are signed by Proudlock Technology LLC.
Unsigned CI builds are for testing only.
