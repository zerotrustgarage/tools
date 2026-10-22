# Synthetic Windows Server 2025 captures

Captured by Copy RC0 workflow run [39829011377](https://github.com/zerotrustgarage/tools/actions/runs/39829011377)
on October 22, 2026. Source: commit ced28c60a0d60d149af8fc73c359c4e8392955b8. Robocopy SHA-256:
a5d23b26c414c4d844fd232ed53783e943b46bb86bb54128d1e095ea4c55e32f.

Files contain base64 of the exact stdout pipe bytes. Paths refer only to generated fixture
directories on a disposable GitHub-hosted runner. No real client data or credentials.

Findings for this build only:

- /? alone returns the full help, with native exit 16. /UNICODE /? is rejected as an
  invalid parameter and produces a shorter diagnostic, also with exit 16.
- /UNICODE serial and /MT:8 stdout starts with a UTF-16LE BOM but mixes narrow-byte
  text with UTF-16LE fragments. A BOM does not establish the encoding of the full stream.
  The copies succeed and preserve the filename on disk; decoding the entire pipe as
  UTF-16LE does not preserve that name.
- /QUIT succeeds for a valid command; an unknown option produces native exit 16.
- Go quoting preserves spaces, Unicode and trailing backslashes on these synthetic paths.

The production parser must not assume a pure UTF-16LE console stream based on the BOM.
Compare /UNILOG before choosing the authoritative name/summary stream.
These captures do not resolve the full Windows version and locale matrix.

Native /UNILOG evidence was subsequently captured in successful run
[39829570047](https://github.com/zerotrustgarage/tools/actions/runs/39829570047)
from commit ee43e613b6a096b8ec35ca8c4460991ff4ec53a9. `native-log.base64` contains the exact native
log. Its full stream decodes as UTF-16LE and preserves café-日本語-😀.txt, while
the simultaneous console stream still mixes encodings. This supports using native
logs for filename correctness on this build; other builds and locales remain open.
