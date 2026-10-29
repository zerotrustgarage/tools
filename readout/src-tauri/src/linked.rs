//! Links from a document to files that are not Markdown (RD16): a PDF, a spreadsheet, a script.
//!
//! A Markdown file is untrusted input, so a link must never become code execution by one click.
//! The rules live here, as plain functions that can be tested without a window:
//!
//! - The path is resolved in Rust against the open document with the same rules as every other
//!   link (`resolve_target`): relative only, canonical, it must exist, and inside the open folder.
//!   Without an open folder the file must be inside the document's own folder. A drive, a root, a
//!   UNC host (`\\host\share`, which would make Windows authenticate to it) and an alternate data
//!   stream (`file.pdf:stream`, any `:` in the target) are refused.
//! - What opens is decided by an allow-list of document extensions (`OPENABLE`), checked on both
//!   the name as written and the canonical name, so a symlink cannot rename a program into a
//!   document. Anything executable or script-like (`PROGRAMS`) and anything without an extension
//!   is refused outright with a message that says Readout does not run programs. HTML and SVG are
//!   not on the allow-list because the default handler is a browser that would run their script.
//! - The user confirms in the WebView (Cancel is the default button) and Rust opens the file with
//!   the operating system's default handler, with no arguments and no shell: on Windows the
//!   opener plugin calls `ShellExecuteExW` with the file only (its `shellexecute-on-windows`
//!   feature), on Linux and macOS `xdg-open` and `open`.

use std::fs;
use std::path::Path;

use super::resolve_target;

/// Document types that open in their default application after one confirmation, with the
/// description shown in the confirmation. Lower case, no dot.
pub const OPENABLE: &[(&str, &str)] = &[
    ("pdf", "PDF document"),
    ("png", "PNG image"),
    ("jpg", "JPEG image"),
    ("jpeg", "JPEG image"),
    ("gif", "GIF image"),
    ("webp", "WebP image"),
    ("txt", "Text file"),
    ("csv", "CSV data file"),
    ("log", "Log file"),
    ("docx", "Word document"),
    ("xlsx", "Excel workbook"),
    ("pptx", "PowerPoint presentation"),
    ("odt", "OpenDocument text"),
    ("ods", "OpenDocument spreadsheet"),
    ("odp", "OpenDocument presentation"),
    ("rtf", "Rich Text document"),
    ("json", "JSON data file"),
    ("xml", "XML data file"),
    ("yaml", "YAML data file"),
    ("yml", "YAML data file"),
];

/// Programs and scripts: refused outright, never opened and never run. This is the explicit list
/// behind the message; a type that is in neither list is refused too (see `Kind::Unlisted`).
pub const PROGRAMS: &[&str] = &[
    "exe",
    "com",
    "bat",
    "cmd",
    "ps1",
    "psm1",
    "vbs",
    "vbe",
    "js",
    "jse",
    "wsf",
    "wsh",
    "msi",
    "msp",
    "scr",
    "pif",
    "lnk",
    "url",
    "hta",
    "cpl",
    "jar",
    "reg",
    "inf",
    "chm",
    "appx",
    "msix",
    "application",
    "gadget",
    // Further program and script types, beyond the list the roadmap names.
    "dll",
    "sys",
    "ocx",
    "drv",
    "mjs",
    "cjs",
    "ps1xml",
    "psd1",
    "psc1",
    "vb",
    "ws",
    "wsc",
    "sct",
    "msc",
    "mst",
    "appxbundle",
    "msixbundle",
    "appinstaller",
    "appref-ms",
    "settingcontent-ms",
    "diagcab",
    "library-ms",
    "scf",
    "py",
    "pyw",
    "pl",
    "rb",
    "php",
    "sh",
    "bash",
    "zsh",
];

#[derive(Debug, PartialEq)]
pub enum Kind {
    /// On the allow-list: opens after a confirmation.
    Openable { label: &'static str },
    /// A program or script.
    Program,
    /// No extension, so nothing says what it is: treated as a program.
    NoExtension,
    /// A document-like type that is not on the allow-list (an archive, HTML, SVG).
    Unlisted,
}

/// The extension of a file name, lower case, or None. Trailing dots and spaces are ignored
/// because Windows ignores them (`run.exe.` is `run.exe`); a name that is only an extension
/// (`.pdf`) has none.
pub fn extension_of(name: &str) -> Option<String> {
    let name = name.trim_end_matches(['.', ' ']);
    let (stem, ext) = name.rsplit_once('.')?;
    if stem.is_empty() || ext.is_empty() {
        return None;
    }
    Some(ext.to_lowercase())
}

pub fn classify(name: &str) -> Kind {
    let Some(ext) = extension_of(name) else {
        return Kind::NoExtension;
    };
    if let Some((_, label)) = OPENABLE.iter().find(|(e, _)| *e == ext) {
        Kind::Openable { label }
    } else if PROGRAMS.contains(&ext.as_str()) {
        Kind::Program
    } else {
        Kind::Unlisted
    }
}

/// The last segment of a path written in a document, with either separator.
pub fn file_name_of(path: &str) -> &str {
    path.rsplit(['/', '\\']).next().unwrap_or(path)
}

/// The message for a name that may not be opened, or None when it may.
pub fn refusal_for(name: &str) -> Option<String> {
    match classify(name) {
        Kind::Openable { .. } => None,
        Kind::Program | Kind::NoExtension => Some(format!(
            "Readout does not run programs, so it will not open \"{name}\". \
             Open it from File Explorer yourself if you trust it."
        )),
        Kind::Unlisted => {
            let types: Vec<&str> = OPENABLE.iter().map(|(e, _)| *e).collect();
            Some(format!(
                "Readout does not open \"{name}\" from a link. It opens only these file types: {}.",
                types.join(", ")
            ))
        }
    }
}

/// Why a link target as written in the document must not be followed, before any file is touched.
pub fn target_problem(target: &str) -> Option<&'static str> {
    if target.chars().any(char::is_control) {
        return Some("the link contains a control character");
    }
    if target.starts_with("\\\\") || target.starts_with("//") {
        return Some("the link names a network share");
    }
    // Any colon: a drive (`C:`), a URL scheme (`file:`) or an alternate data stream (`a.pdf:x`).
    if target.contains(':') {
        return Some("the link names a drive, a scheme or a data stream");
    }
    None
}

/// Strips Windows' verbatim prefix from a drive path (`\\?\C:\x` becomes `C:\x`), which
/// `canonicalize` adds and many handlers do not accept. Other paths are returned as they are.
pub fn simplified(path: &str) -> &str {
    match path.strip_prefix("\\\\?\\") {
        Some(rest) if rest.len() >= 2 && rest.as_bytes()[0].is_ascii_alphabetic() && rest.as_bytes()[1] == b':' => rest,
        _ => path,
    }
}

/// Why a resolved path must not be opened: a UNC path, or an alternate data stream.
pub fn path_problem(path: &str) -> Option<&'static str> {
    if path.starts_with("\\\\?\\UNC\\") || path.to_ascii_lowercase().starts_with("\\\\?\\unc\\") {
        return Some("the file is on a network share");
    }
    let plain = simplified(path);
    if plain.starts_with("\\\\") || plain.starts_with("//") {
        return Some("the file is on a network share");
    }
    let rest = match plain.as_bytes() {
        [drive, b':', ..] if drive.is_ascii_alphabetic() => &plain[2..],
        _ => plain,
    };
    if rest.contains(':') {
        return Some("the path names a data stream");
    }
    None
}

#[derive(Debug, PartialEq)]
pub struct LinkedFile {
    /// The canonical path, in the form handed to the operating system.
    pub path: String,
    pub name: String,
    pub kind: String,
}

#[derive(Debug, PartialEq)]
pub enum LinkCheck {
    /// The link points to a folder: the caller's Markdown rules apply, not these.
    Folder,
    Refused(String),
    Confirm(LinkedFile),
}

/// Resolves and judges a link from `document` (a granted, canonical path) to `target` (decoded,
/// without query or fragment). `root` is the open folder that contains the document, if any.
pub fn check(document: &Path, target: &str, root: Option<&Path>) -> LinkCheck {
    let written = file_name_of(target);
    if let Some(why) = target_problem(target) {
        return LinkCheck::Refused(format!("Readout will not follow this link ({why}): {target}"));
    }
    let resolved = match resolve_target(document, target, root) {
        Ok(path) => path,
        Err(e) => {
            return LinkCheck::Refused(match e.as_str() {
                "Not found" => format!("The linked file was not found: {target}"),
                "That path leaves the open folder" => {
                    format!("That link leaves the open folder, so Readout will not open it: {target}")
                }
                _ => format!("Readout follows only relative links to files next to the document: {target}"),
            })
        }
    };
    if root.is_none() && document.parent().is_none_or(|parent| !resolved.starts_with(parent)) {
        return LinkCheck::Refused(format!(
            "Without an open folder, Readout opens linked files only from this document's own folder: {target}"
        ));
    }
    let text = resolved.to_string_lossy().into_owned();
    if let Some(why) = path_problem(&text) {
        return LinkCheck::Refused(format!("Readout will not open this file ({why}): {target}"));
    }
    let Ok(meta) = fs::metadata(&resolved) else {
        return LinkCheck::Refused(format!("The linked file was not found: {target}"));
    };
    if meta.is_dir() {
        return LinkCheck::Folder;
    }
    if !meta.is_file() {
        return LinkCheck::Refused(format!(
            "\"{written}\" is not a regular file, so Readout will not open it."
        ));
    }
    // Judged by the name as written and by the canonical name: both must be a document type.
    let name = resolved
        .file_name()
        .map(|n| n.to_string_lossy().into_owned())
        .unwrap_or_else(|| written.to_string());
    for candidate in [written, name.as_str()] {
        if let Some(message) = refusal_for(candidate) {
            return LinkCheck::Refused(message);
        }
    }
    let Kind::Openable { label } = classify(&name) else {
        return LinkCheck::Refused(format!("Readout will not open \"{name}\"."));
    };
    let kind = format!("{label} (.{})", extension_of(&name).unwrap_or_default());
    LinkCheck::Confirm(LinkedFile {
        path: simplified(&text).to_string(),
        name,
        kind,
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::path::PathBuf;

    fn fixture(name: &str) -> PathBuf {
        let dir = std::env::temp_dir().join(format!("ztg-readout-linked-{name}-{}", std::process::id()));
        let _ = fs::remove_dir_all(&dir);
        for d in ["repo/docs", "repo/files", "outside", "lone"] {
            fs::create_dir_all(dir.join(d)).unwrap();
        }
        for f in [
            "repo/README.md",
            "repo/docs/setup.md",
            "repo/files/spec.pdf",
            "repo/files/Report.XLSX",
            "repo/files/run.exe",
            "repo/files/deploy.ps1",
            "repo/files/page.html",
            "repo/files/Makefile",
            "repo/files/archive.zip",
            "outside/secret.pdf",
            "lone/note.md",
            "lone/menu.pdf",
        ] {
            fs::write(dir.join(f), "x").unwrap();
        }
        fs::canonicalize(dir).unwrap()
    }

    #[test]
    fn extensions_are_read_from_the_last_dot_and_ignore_trailing_dots_and_spaces() {
        assert_eq!(extension_of("a.PDF").as_deref(), Some("pdf"));
        assert_eq!(extension_of("invoice.pdf.exe").as_deref(), Some("exe"));
        assert_eq!(extension_of("run.exe.").as_deref(), Some("exe"));
        assert_eq!(extension_of("run.exe . ").as_deref(), Some("exe"));
        assert_eq!(extension_of("Makefile"), None);
        assert_eq!(extension_of(".pdf"), None);
        assert_eq!(extension_of("trailing."), None);
        assert_eq!(extension_of(""), None);
    }

    #[test]
    fn the_allow_list_opens_documents_and_nothing_else() {
        for ext in [
            "pdf", "png", "jpg", "jpeg", "gif", "webp", "txt", "csv", "log", "docx", "xlsx", "pptx", "odt", "ods",
            "odp", "rtf", "json", "xml", "yaml", "yml",
        ] {
            assert!(
                matches!(classify(&format!("file.{ext}")), Kind::Openable { .. }),
                "{ext}"
            );
            assert!(
                matches!(classify(&format!("FILE.{}", ext.to_uppercase())), Kind::Openable { .. }),
                "{ext}"
            );
        }
        assert_eq!(classify("a.pdf"), Kind::Openable { label: "PDF document" });
        for ext in ["html", "htm", "svg", "zip", "docm", "xlsm", "dll", "iso"] {
            assert!(
                !matches!(classify(&format!("file.{ext}")), Kind::Openable { .. }),
                "{ext}"
            );
        }
        // No type is on both lists.
        assert!(OPENABLE.iter().all(|(e, _)| !PROGRAMS.contains(e)));
    }

    #[test]
    fn every_program_and_script_type_is_refused_in_any_case() {
        let named = [
            "exe",
            "com",
            "bat",
            "cmd",
            "ps1",
            "psm1",
            "vbs",
            "vbe",
            "js",
            "jse",
            "wsf",
            "wsh",
            "msi",
            "msp",
            "scr",
            "pif",
            "lnk",
            "url",
            "hta",
            "cpl",
            "jar",
            "reg",
            "inf",
            "chm",
            "appx",
            "msix",
            "application",
            "gadget",
        ];
        for ext in named {
            for name in [
                format!("x.{ext}"),
                format!("X.{}", ext.to_uppercase()),
                format!("x.pdf.{ext}"),
            ] {
                assert_eq!(classify(&name), Kind::Program, "{name}");
                let message = refusal_for(&name).unwrap();
                assert!(message.contains("does not run programs"), "{message}");
                assert!(message.contains(&name), "{message}");
            }
        }
        for name in ["Makefile", "LICENSE", ".pdf", "run."] {
            assert_eq!(classify(name), Kind::NoExtension, "{name}");
            assert!(refusal_for(name).unwrap().contains("does not run programs"));
        }
    }

    #[test]
    fn other_types_are_refused_with_the_list_of_what_opens() {
        let message = refusal_for("page.html").unwrap();
        assert!(message.contains("page.html"));
        assert!(message.contains("pdf, png"));
        assert!(!message.contains("does not run programs"));
        assert!(refusal_for("a.pdf").is_none());
    }

    #[test]
    fn targets_with_a_drive_scheme_stream_or_share_are_refused() {
        for bad in [
            "C:\\x.pdf",
            "c:/x.pdf",
            "file:///etc/x.pdf",
            "file:x.pdf",
            "a.pdf:evil.exe",
            "a.pdf::$DATA",
            "docs/a.pdf:stream",
            "\\\\host\\share\\a.pdf",
            "//host/share/a.pdf",
            "a\u{0}.pdf",
            "a\n.pdf",
        ] {
            assert!(target_problem(bad).is_some(), "{bad:?}");
        }
        for good in [
            "a.pdf",
            "docs/a b.pdf",
            "../files/a.pdf",
            "/files/a.pdf",
            "caf\u{e9}.pdf",
        ] {
            assert_eq!(target_problem(good), None, "{good}");
        }
    }

    #[test]
    fn resolved_paths_on_a_share_or_with_a_stream_are_refused() {
        for bad in [
            "\\\\?\\UNC\\host\\share\\a.pdf",
            "\\\\?\\unc\\host\\share\\a.pdf",
            "\\\\host\\share\\a.pdf",
            "//host/share/a.pdf",
            "C:\\docs\\a.pdf:evil.exe",
            "\\\\?\\C:\\docs\\a.pdf:stream",
            "/home/user/a.pdf:stream",
        ] {
            assert!(path_problem(bad).is_some(), "{bad}");
        }
        for good in [
            "C:\\docs\\a.pdf",
            "\\\\?\\C:\\docs\\a.pdf",
            "/home/user/a.pdf",
            "D:/docs/a b.pdf",
        ] {
            assert_eq!(path_problem(good), None, "{good}");
        }
    }

    #[test]
    fn the_verbatim_prefix_is_dropped_for_drive_paths_only() {
        assert_eq!(simplified("\\\\?\\C:\\docs\\a.pdf"), "C:\\docs\\a.pdf");
        assert_eq!(simplified("\\\\?\\UNC\\h\\s\\a.pdf"), "\\\\?\\UNC\\h\\s\\a.pdf");
        assert_eq!(simplified("C:\\a.pdf"), "C:\\a.pdf");
        assert_eq!(simplified("/home/a.pdf"), "/home/a.pdf");
        assert_eq!(simplified("\\\\?\\"), "\\\\?\\");
    }

    #[test]
    fn file_names_are_taken_with_either_separator() {
        assert_eq!(file_name_of("a/b/c.pdf"), "c.pdf");
        assert_eq!(file_name_of("a\\b\\c.pdf"), "c.pdf");
        assert_eq!(file_name_of("c.pdf"), "c.pdf");
    }

    fn confirmed(result: LinkCheck) -> LinkedFile {
        match result {
            LinkCheck::Confirm(file) => file,
            other => panic!("expected a confirmation, got {other:?}"),
        }
    }

    fn refused(result: LinkCheck) -> String {
        match result {
            LinkCheck::Refused(message) => message,
            other => panic!("expected a refusal, got {other:?}"),
        }
    }

    #[test]
    fn inside_an_open_folder_a_document_file_asks_and_a_program_is_refused() {
        let dir = fixture("folder");
        let root = dir.join("repo");
        let doc = root.join("docs/setup.md");
        let file = confirmed(check(&doc, "../files/spec.pdf", Some(&root)));
        assert_eq!(file.name, "spec.pdf");
        assert_eq!(file.kind, "PDF document (.pdf)");
        assert!(Path::new(&file.path).ends_with("files/spec.pdf"));
        assert!(Path::new(&file.path).is_absolute());
        assert_eq!(
            confirmed(check(&doc, "/files/Report.XLSX", Some(&root))).kind,
            "Excel workbook (.xlsx)"
        );

        let exe = refused(check(&doc, "../files/run.exe", Some(&root)));
        assert!(
            exe.contains("run.exe") && exe.contains("does not run programs"),
            "{exe}"
        );
        let script = refused(check(&doc, "../files/deploy.ps1", Some(&root)));
        assert!(
            script.contains("deploy.ps1") && script.contains("does not run programs"),
            "{script}"
        );
        let bare = refused(check(&doc, "../files/Makefile", Some(&root)));
        assert!(
            bare.contains("Makefile") && bare.contains("does not run programs"),
            "{bare}"
        );
        assert!(refused(check(&doc, "../files/page.html", Some(&root))).contains("page.html"));
        assert!(refused(check(&doc, "../files/archive.zip", Some(&root))).contains("archive.zip"));
    }

    #[test]
    fn a_link_cannot_leave_the_open_folder_or_name_a_stream() {
        let dir = fixture("leave");
        let root = dir.join("repo");
        let doc = root.join("docs/setup.md");
        assert!(refused(check(&doc, "../../outside/secret.pdf", Some(&root))).contains("leaves the open folder"));
        assert!(refused(check(&doc, "/../outside/secret.pdf", Some(&root))).contains("leaves the open folder"));
        assert!(refused(check(&doc, "../files/spec.pdf:evil.exe", Some(&root))).contains("data stream"));
        assert!(refused(check(&doc, "C:\\Windows\\a.pdf", Some(&root))).contains("drive"));
        assert!(refused(check(&doc, "\\\\host\\share\\a.pdf", Some(&root))).contains("network share"));
        assert!(refused(check(&doc, "file:///etc/a.pdf", Some(&root))).contains("scheme"));
        assert!(refused(check(&doc, "../files/missing.pdf", Some(&root))).contains("not found"));
        assert!(refused(check(&doc, "", Some(&root))).contains("relative"));
    }

    #[test]
    fn a_folder_link_is_left_to_the_markdown_rules() {
        let dir = fixture("folderlink");
        let root = dir.join("repo");
        let doc = root.join("docs/setup.md");
        assert_eq!(check(&doc, "../files", Some(&root)), LinkCheck::Folder);
        assert_eq!(check(&doc, "..", Some(&root)), LinkCheck::Folder);
    }

    #[test]
    fn a_lone_document_opens_files_from_its_own_folder_only() {
        let dir = fixture("lone");
        let doc = dir.join("lone/note.md");
        assert_eq!(confirmed(check(&doc, "menu.pdf", None)).name, "menu.pdf");
        assert_eq!(confirmed(check(&doc, "./menu.pdf", None)).name, "menu.pdf");
        assert!(refused(check(&doc, "../outside/secret.pdf", None)).contains("own folder"));
        assert!(refused(check(&doc, "../repo/files/spec.pdf", None)).contains("own folder"));
        assert!(refused(check(&doc, "/etc/passwd", None)).contains("relative"));
    }

    #[cfg(unix)]
    #[test]
    fn a_symlink_cannot_reach_outside_or_rename_a_program() {
        let dir = fixture("symlink");
        let root = dir.join("repo");
        let doc = root.join("docs/setup.md");
        std::os::unix::fs::symlink(dir.join("outside/secret.pdf"), root.join("files/escape.pdf")).unwrap();
        assert!(refused(check(&doc, "../files/escape.pdf", Some(&root))).contains("leaves the open folder"));
        // A program with a document name: the canonical name is judged too.
        std::os::unix::fs::symlink(root.join("files/run.exe"), root.join("files/innocent.pdf")).unwrap();
        let message = refused(check(&doc, "../files/innocent.pdf", Some(&root)));
        assert!(
            message.contains("run.exe") && message.contains("does not run programs"),
            "{message}"
        );
        // A document with a program name is refused by the name as written.
        std::os::unix::fs::symlink(root.join("files/spec.pdf"), root.join("files/setup.exe")).unwrap();
        let message = refused(check(&doc, "../files/setup.exe", Some(&root)));
        assert!(
            message.contains("setup.exe") && message.contains("does not run programs"),
            "{message}"
        );
    }
}
