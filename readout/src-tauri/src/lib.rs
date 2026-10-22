//! ZTG Readout desktop shell.
//!
//! The WebView renders documents from untrusted sources, so it is given no file system, dialog
//! or shell permission (capabilities/default.json). Everything that touches the machine is one
//! of the commands below, and each one is narrow:
//!
//! - Files are read and written only where the user chose: a file or folder picked in a native
//!   dialog shown by Rust, a file association or command line argument, or a drop onto the
//!   window. Those paths and folder roots go on an allow-list (`Granted`). A path the WebView
//!   invents is refused, and following a link to a file outside every granted folder asks the
//!   user in a native dialog that page script cannot answer.
//! - Only Markdown and text extensions can be read as documents or written, so even a
//!   compromised WebView cannot overwrite an executable, script or configuration file.
//! - Paths inside documents (links and images) are resolved here, never by the WebView, and may
//!   not name a drive, root or UNC host. A Markdown image pointing at `\\host\share\x.png` would
//!   otherwise make Windows authenticate to that host over SMB and leak the user's NTLM hash.
//!   Inside an opened folder they must also stay inside that folder.
//! - Folder listings never follow symbolic links or junctions, so a repository cannot point the
//!   walk outside itself or into a loop.
//! - External links open in the system browser only for http, https and mailto.

use std::collections::HashSet;
use std::fs;
use std::io::Write;
use std::path::{Component, Path, PathBuf};
use std::sync::Mutex;
use std::time::{SystemTime, UNIX_EPOCH};

use serde::Serialize;
use tauri::ipc::{InvokeBody, Request, Response};
use tauri::{AppHandle, DragDropEvent, Emitter, Manager, State, WindowEvent};
use tauri_plugin_dialog::{DialogExt, MessageDialogButtons, MessageDialogKind};
use tauri_plugin_opener::OpenerExt;

const DOCUMENT_EXTENSIONS: &[&str] = &["md", "markdown", "mdown", "mkd", "mkdn", "txt"];
/// Folder listings show Markdown only; a repository's .txt files are rarely documents.
const LISTED_EXTENSIONS: &[&str] = &["md", "markdown", "mdown", "mkd", "mkdn"];
const EXPORT_EXTENSIONS: &[&str] = &["html", "htm"];
const IMAGE_EXTENSIONS: &[&str] = &["png", "jpg", "jpeg", "gif", "webp", "svg", "bmp", "ico", "avif"];
const MAX_DOCUMENT_BYTES: u64 = 64 * 1024 * 1024;
const MAX_IMAGE_BYTES: u64 = 32 * 1024 * 1024;

/// Folders a listing never descends into: version control, dependencies and build output.
/// `.github` and `.gitlab` are kept because they hold templates and contributor guides.
const SKIPPED_FOLDERS: &[&str] = &[
    ".git",
    ".hg",
    ".svn",
    "node_modules",
    "bower_components",
    "target",
    "dist",
    "build",
    "out",
    ".venv",
    "venv",
    "__pycache__",
    ".tox",
    ".next",
    ".nuxt",
    ".cache",
    ".idea",
    ".vs",
    "bin",
    "obj",
    ".terraform",
    ".obsidian",
    ".trash",
];
const KEPT_HIDDEN_FOLDERS: &[&str] = &[".github", ".gitlab"];
const MAX_LISTED_FILES: usize = 20_000;
const MAX_VISITED_ENTRIES: usize = 250_000;
const MAX_DEPTH: usize = 32;

#[derive(Default)]
struct Granted {
    files: Mutex<HashSet<PathBuf>>,
    roots: Mutex<HashSet<PathBuf>>,
    initial: Mutex<Option<Initial>>,
}

#[derive(Clone, Serialize, Debug, PartialEq)]
struct Initial {
    path: String,
    folder: bool,
}

impl Granted {
    fn grant_file(&self, path: &Path) {
        self.files.lock().expect("allow-list lock").insert(path.to_path_buf());
    }

    fn grant_root(&self, path: &Path) {
        self.roots.lock().expect("allow-list lock").insert(path.to_path_buf());
    }

    /// The granted folder that contains path, if any.
    fn root_of(&self, path: &Path) -> Option<PathBuf> {
        self.roots
            .lock()
            .expect("allow-list lock")
            .iter()
            .filter(|root| path.starts_with(root))
            .max_by_key(|root| root.components().count())
            .cloned()
    }

    fn allows(&self, path: &Path) -> bool {
        self.files.lock().expect("allow-list lock").contains(path) || self.root_of(path).is_some()
    }

    fn check(&self, path: &str) -> Result<PathBuf, String> {
        let path = canonical(Path::new(path))?;
        if self.allows(&path) {
            Ok(path)
        } else {
            Err("That file was not opened through this window".into())
        }
    }

    fn check_root(&self, path: &str) -> Result<PathBuf, String> {
        let path = canonical(Path::new(path))?;
        if self.roots.lock().expect("allow-list lock").contains(&path) {
            Ok(path)
        } else {
            Err("That folder was not opened through this window".into())
        }
    }
}

fn has_extension(path: &Path, allowed: &[&str]) -> bool {
    path.extension()
        .and_then(|e| e.to_str())
        .map(|e| allowed.iter().any(|a| a.eq_ignore_ascii_case(e)))
        .unwrap_or(false)
}

/// Canonical form of an existing file, or of a file about to be created in an existing folder.
fn canonical(path: &Path) -> Result<PathBuf, String> {
    if let Ok(p) = fs::canonicalize(path) {
        return Ok(p);
    }
    let parent = path.parent().ok_or("The path has no folder")?;
    let name = path.file_name().ok_or("The path has no file name")?;
    Ok(fs::canonicalize(parent)
        .map_err(|e| format!("The folder is not available: {e}"))?
        .join(name))
}

/// Writes through a temporary file in the same folder, then renames it over the target, so a
/// crash or full disk mid-write leaves the original intact.
fn write_atomic(target: &Path, bytes: &[u8]) -> Result<(), String> {
    let folder = target.parent().ok_or("The path has no folder")?;
    let name = target.file_name().and_then(|n| n.to_str()).unwrap_or("document");
    let nonce = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_nanos())
        .unwrap_or(0);
    let temp = folder.join(format!(".{name}.{}-{nonce}.ztg-tmp", std::process::id()));
    let result = (|| {
        let mut file = fs::OpenOptions::new().write(true).create_new(true).open(&temp)?;
        file.write_all(bytes)?;
        file.sync_all()?;
        drop(file);
        fs::rename(&temp, target)
    })();
    if let Err(e) = result {
        let _ = fs::remove_file(&temp);
        return Err(format!("Could not write the file: {e}"));
    }
    Ok(())
}

fn read_limited(path: &Path, limit: u64) -> Result<Vec<u8>, String> {
    let meta = fs::metadata(path).map_err(|e| format!("Could not read the file: {e}"))?;
    if !meta.is_file() {
        return Err("Not a file".into());
    }
    if meta.len() > limit {
        return Err(format!("The file is larger than {} MB", limit / (1024 * 1024)));
    }
    fs::read(path).map_err(|e| format!("Could not read the file: {e}"))
}

fn raw_body(request: &Request<'_>) -> Result<Vec<u8>, String> {
    match request.body() {
        InvokeBody::Raw(bytes) => Ok(bytes.clone()),
        _ => Err("Expected the document bytes".into()),
    }
}

/// Header values are percent-encoded UTF-8 (encodeURIComponent on the WebView side).
fn header(request: &Request<'_>, name: &str) -> Result<String, String> {
    let value = request
        .headers()
        .get(name)
        .and_then(|v| v.to_str().ok())
        .ok_or_else(|| format!("Missing {name}"))?;
    percent_decode(value).ok_or_else(|| format!("Invalid {name}"))
}

fn percent_decode(input: &str) -> Option<String> {
    let bytes = input.as_bytes();
    let mut out = Vec::with_capacity(bytes.len());
    let mut i = 0;
    while i < bytes.len() {
        if bytes[i] == b'%' {
            let hex = input.get(i + 1..i + 3)?;
            out.push(u8::from_str_radix(hex, 16).ok()?);
            i += 3;
        } else {
            out.push(bytes[i]);
            i += 1;
        }
    }
    String::from_utf8(out).ok()
}

/// A safe file name for a save dialog's suggestion.
fn suggested_name(raw: &str, fallback: &str) -> String {
    let name: String = raw
        .chars()
        .filter(|c| !c.is_control() && !matches!(c, '/' | '\\' | ':' | '*' | '?' | '"' | '<' | '>' | '|'))
        .collect();
    let name = name.trim().trim_matches('.').to_string();
    if name.is_empty() {
        fallback.into()
    } else {
        name
    }
}

/// Resolves a path written inside a document (a link or an image source, already stripped of
/// any query or fragment and percent-decoded by the caller). A leading `/` means the opened
/// folder's root, as on GitHub. Inside a folder the result must stay in that folder; a lone file
/// may refer to its neighbours (`../assets/x.png` is common).
fn resolve_target(document: &Path, target: &str, root: Option<&Path>) -> Result<PathBuf, String> {
    let refused = || "Only relative paths are allowed".to_string();
    if target.is_empty() || target.contains("://") || target.starts_with("\\\\") || target.starts_with("//") {
        return Err(refused());
    }
    let (base, rest) = match (target.strip_prefix('/'), root) {
        (Some(rest), Some(root)) => (root.to_path_buf(), rest),
        (Some(_), None) => return Err(refused()),
        (None, _) => (
            document.parent().ok_or("The document has no folder")?.to_path_buf(),
            target,
        ),
    };
    let rel = Path::new(rest);
    if rel
        .components()
        .any(|c| matches!(c, Component::Prefix(_) | Component::RootDir))
    {
        return Err(refused());
    }
    let path = fs::canonicalize(base.join(rel)).map_err(|_| "Not found".to_string())?;
    if let Some(root) = root {
        if !path.starts_with(root) {
            return Err("That path leaves the open folder".into());
        }
    }
    Ok(path)
}

/// Markdown files under root as sorted, `/`-separated relative paths.
fn list_markdown(root: &Path) -> Result<(Vec<String>, bool), String> {
    let mut files = Vec::new();
    let mut visited = 0usize;
    let mut truncated = false;
    let mut stack = vec![(root.to_path_buf(), 0usize)];
    while let Some((dir, depth)) = stack.pop() {
        let Ok(entries) = fs::read_dir(&dir) else { continue };
        for entry in entries.flatten() {
            visited += 1;
            if visited > MAX_VISITED_ENTRIES || files.len() >= MAX_LISTED_FILES {
                truncated = true;
                break;
            }
            // symlink_metadata does not follow links, so links and junctions are skipped.
            let Ok(meta) = entry.path().symlink_metadata() else {
                continue;
            };
            let name = entry.file_name().to_string_lossy().into_owned();
            if meta.file_type().is_symlink() {
                continue;
            }
            if meta.is_dir() {
                let hidden = name.starts_with('.') && !KEPT_HIDDEN_FOLDERS.contains(&name.as_str());
                if !hidden && !SKIPPED_FOLDERS.iter().any(|s| s.eq_ignore_ascii_case(&name)) && depth < MAX_DEPTH {
                    stack.push((entry.path(), depth + 1));
                }
            } else if meta.is_file() && has_extension(&entry.path(), LISTED_EXTENSIONS) {
                if let Ok(rel) = entry.path().strip_prefix(root) {
                    let parts: Vec<String> = rel
                        .components()
                        .map(|c| c.as_os_str().to_string_lossy().into_owned())
                        .collect();
                    files.push(parts.join("/"));
                }
            }
        }
    }
    files.sort_by_key(|f| f.to_lowercase());
    Ok((files, truncated))
}

/// A folder link opens its README or index, as on GitHub.
fn document_in(path: PathBuf) -> Option<PathBuf> {
    if path.is_file() {
        return has_extension(&path, DOCUMENT_EXTENSIONS).then_some(path);
    }
    ["README.md", "readme.md", "Readme.md", "index.md", "INDEX.md"]
        .iter()
        .map(|n| path.join(n))
        .find(|p| p.is_file())
}

fn startup_argument(args: impl Iterator<Item = String>) -> Option<(PathBuf, bool)> {
    args.map(PathBuf::from)
        .find(|p| p.is_dir() || (p.is_file() && has_extension(p, DOCUMENT_EXTENSIONS)))
        .and_then(|p| fs::canonicalize(&p).ok().map(|c| (c, p.is_dir())))
}

#[derive(Serialize)]
struct Listing {
    root: String,
    name: String,
    files: Vec<String>,
    truncated: bool,
}

#[tauri::command]
async fn choose_document(app: AppHandle, granted: State<'_, Granted>) -> Result<Option<String>, String> {
    let picked = app
        .dialog()
        .file()
        .add_filter("Markdown", &["md", "markdown", "mdown", "mkd", "mkdn"])
        .add_filter("Text", &["txt"])
        .blocking_pick_file();
    let Some(picked) = picked else { return Ok(None) };
    let path = canonical(&picked.into_path().map_err(|e| e.to_string())?)?;
    if !has_extension(&path, DOCUMENT_EXTENSIONS) {
        return Err("Choose a Markdown or text file".into());
    }
    granted.grant_file(&path);
    Ok(Some(path.to_string_lossy().into_owned()))
}

#[tauri::command]
async fn choose_folder(app: AppHandle, granted: State<'_, Granted>) -> Result<Option<String>, String> {
    let Some(picked) = app.dialog().file().blocking_pick_folder() else {
        return Ok(None);
    };
    let path = fs::canonicalize(picked.into_path().map_err(|e| e.to_string())?).map_err(|e| e.to_string())?;
    granted.grant_root(&path);
    Ok(Some(path.to_string_lossy().into_owned()))
}

#[tauri::command]
async fn list_folder(root: String, granted: State<'_, Granted>) -> Result<Listing, String> {
    let root = granted.check_root(&root)?;
    let (files, truncated) = list_markdown(&root)?;
    let name = root
        .file_name()
        .map(|n| n.to_string_lossy().into_owned())
        .unwrap_or_else(|| root.to_string_lossy().into_owned());
    Ok(Listing {
        root: root.to_string_lossy().into_owned(),
        name,
        files,
        truncated,
    })
}

#[tauri::command]
async fn read_document(path: String, granted: State<'_, Granted>) -> Result<Response, String> {
    let path = granted.check(&path)?;
    if !has_extension(&path, DOCUMENT_EXTENSIONS) {
        return Err("Only Markdown and text files can be opened".into());
    }
    Ok(Response::new(read_limited(&path, MAX_DOCUMENT_BYTES)?))
}

#[tauri::command]
async fn save_document(request: Request<'_>, granted: State<'_, Granted>) -> Result<(), String> {
    let path = granted.check(&header(&request, "x-ztg-path")?)?;
    if !has_extension(&path, DOCUMENT_EXTENSIONS) {
        return Err("Only Markdown and text files can be saved".into());
    }
    write_atomic(&path, &raw_body(&request)?)
}

#[tauri::command]
async fn save_document_as(
    app: AppHandle,
    request: Request<'_>,
    granted: State<'_, Granted>,
) -> Result<Option<String>, String> {
    let bytes = raw_body(&request)?;
    let name = suggested_name(&header(&request, "x-ztg-name")?, "Untitled.md");
    let picked = app
        .dialog()
        .file()
        .set_file_name(&name)
        .add_filter("Markdown", &["md", "markdown"])
        .add_filter("Text", &["txt"])
        .blocking_save_file();
    let Some(picked) = picked else { return Ok(None) };
    let mut path = picked.into_path().map_err(|e| e.to_string())?;
    if !has_extension(&path, DOCUMENT_EXTENSIONS) {
        let mut with_ext = path.into_os_string();
        with_ext.push(".md");
        path = PathBuf::from(with_ext);
    }
    let path = canonical(&path)?;
    write_atomic(&path, &bytes)?;
    granted.grant_file(&path);
    Ok(Some(path.to_string_lossy().into_owned()))
}

#[tauri::command]
async fn export_html(app: AppHandle, request: Request<'_>) -> Result<(), String> {
    let bytes = raw_body(&request)?;
    let name = suggested_name(&header(&request, "x-ztg-name")?, "document.html");
    let picked = app
        .dialog()
        .file()
        .set_file_name(&name)
        .add_filter("HTML", &["html", "htm"])
        .blocking_save_file();
    let Some(picked) = picked else { return Ok(()) };
    let path = canonical(&picked.into_path().map_err(|e| e.to_string())?)?;
    if !has_extension(&path, EXPORT_EXTENSIONS) {
        return Err("Exports must be saved as .html".into());
    }
    write_atomic(&path, &bytes)
}

#[tauri::command]
async fn open_external(app: AppHandle, url: String) -> Result<(), String> {
    let lower = url.to_ascii_lowercase();
    let allowed = ["https://", "http://", "mailto:"].iter().any(|p| lower.starts_with(p));
    if !allowed || url.chars().any(|c| c.is_control() || c.is_whitespace()) {
        return Err("Only web and mail links can be opened".into());
    }
    app.opener().open_url(url, None::<&str>).map_err(|e| e.to_string())
}

#[tauri::command]
async fn read_image(document: String, relative: String, granted: State<'_, Granted>) -> Result<Response, String> {
    let document = granted.check(&document)?;
    let root = granted.root_of(&document);
    let path = resolve_target(&document, &relative, root.as_deref())?;
    if !has_extension(&path, IMAGE_EXTENSIONS) {
        return Err("Not an image".into());
    }
    Ok(Response::new(read_limited(&path, MAX_IMAGE_BYTES)?))
}

/// Follows a relative link from one document to another. Inside the open folder this needs no
/// question; a lone file's link to a neighbour asks first, in a dialog only the user can answer.
#[tauri::command]
async fn resolve_link(
    app: AppHandle,
    document: String,
    target: String,
    granted: State<'_, Granted>,
) -> Result<Option<String>, String> {
    let document = granted.check(&document)?;
    let root = granted.root_of(&document);
    let resolved = resolve_target(&document, &target, root.as_deref())?;
    let path = document_in(resolved).ok_or("The link does not point to a Markdown file")?;
    if !granted.allows(&path) {
        let shown = path.to_string_lossy().into_owned();
        let confirmed = app
            .dialog()
            .message(format!(
                "This link opens a file outside the files you opened:\n\n{shown}\n\nOpen it?"
            ))
            .title("Open linked file")
            .kind(MessageDialogKind::Warning)
            .buttons(MessageDialogButtons::OkCancelCustom("Open".into(), "Cancel".into()))
            .blocking_show();
        if !confirmed {
            return Ok(None);
        }
        granted.grant_file(&path);
    }
    Ok(Some(path.to_string_lossy().into_owned()))
}

#[tauri::command]
fn initial_document(granted: State<'_, Granted>) -> Option<Initial> {
    granted.initial.lock().expect("initial lock").take()
}

pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_opener::init())
        .manage(Granted::default())
        .setup(|app| {
            if let Some((path, folder)) = startup_argument(std::env::args().skip(1)) {
                let granted = app.state::<Granted>();
                if folder {
                    granted.grant_root(&path);
                } else {
                    granted.grant_file(&path);
                }
                *granted.initial.lock().expect("initial lock") = Some(Initial {
                    path: path.to_string_lossy().into_owned(),
                    folder,
                });
            }
            Ok(())
        })
        .on_window_event(|window, event| {
            if let WindowEvent::DragDrop(DragDropEvent::Drop { paths, .. }) = event {
                let dropped = paths
                    .iter()
                    .filter(|p| p.is_dir() || has_extension(p, DOCUMENT_EXTENSIONS))
                    .find_map(|p| fs::canonicalize(p).ok());
                if let Some(path) = dropped {
                    let granted = window.state::<Granted>();
                    let folder = path.is_dir();
                    if folder {
                        granted.grant_root(&path);
                    } else {
                        granted.grant_file(&path);
                    }
                    let _ = window.emit(
                        "document-dropped",
                        Initial {
                            path: path.to_string_lossy().into_owned(),
                            folder,
                        },
                    );
                }
            }
        })
        .invoke_handler(tauri::generate_handler![
            choose_document,
            choose_folder,
            list_folder,
            read_document,
            save_document,
            save_document_as,
            export_html,
            open_external,
            read_image,
            resolve_link,
            initial_document
        ])
        .run(tauri::generate_context!())
        .expect("error while running ZTG Readout");
}

#[cfg(test)]
mod tests {
    use super::*;

    fn fixture(name: &str) -> PathBuf {
        let dir = std::env::temp_dir().join(format!("ztg-readout-test-{name}-{}", std::process::id()));
        let _ = fs::remove_dir_all(&dir);
        for d in [
            "repo/docs/images",
            "repo/.git/refs",
            "repo/.github",
            "repo/node_modules/pkg",
            "repo/src",
            "outside",
        ] {
            fs::create_dir_all(dir.join(d)).unwrap();
        }
        for (f, body) in [
            ("repo/README.md", "# readme"),
            ("repo/docs/setup.md", "# setup"),
            ("repo/docs/images/a.png", "png"),
            ("repo/docs/notes.txt", "notes"),
            ("repo/.git/HEAD.md", "no"),
            ("repo/.github/CONTRIBUTING.md", "# contributing"),
            ("repo/node_modules/pkg/README.md", "no"),
            ("repo/src/main.rs", "fn main() {}"),
            ("outside/secret.md", "# secret"),
            ("outside/logo.png", "png"),
        ] {
            fs::write(dir.join(f), body).unwrap();
        }
        fs::canonicalize(dir).unwrap()
    }

    #[test]
    fn extensions_are_case_insensitive() {
        assert!(has_extension(Path::new("A.MD"), DOCUMENT_EXTENSIONS));
        assert!(!has_extension(Path::new("run.ps1"), DOCUMENT_EXTENSIONS));
        assert!(!has_extension(Path::new("README"), DOCUMENT_EXTENSIONS));
    }

    #[test]
    fn allow_list_refuses_paths_the_user_did_not_choose() {
        let dir = fixture("grant");
        let granted = Granted::default();
        let doc = dir.join("outside/secret.md");
        assert!(granted.check(doc.to_str().unwrap()).is_err());
        granted.grant_file(&doc);
        assert!(granted
            .check(dir.join("repo/../outside/secret.md").to_str().unwrap())
            .is_ok());
        assert!(granted.check(dir.join("repo/README.md").to_str().unwrap()).is_err());
    }

    #[test]
    fn a_granted_folder_covers_its_files_only() {
        let dir = fixture("root");
        let granted = Granted::default();
        granted.grant_root(&dir.join("repo"));
        assert!(granted.check(dir.join("repo/docs/setup.md").to_str().unwrap()).is_ok());
        assert!(granted
            .check(dir.join("repo/docs/new-file.md").to_str().unwrap())
            .is_ok());
        assert!(granted.check(dir.join("outside/secret.md").to_str().unwrap()).is_err());
        assert!(granted
            .check(dir.join("repo/../outside/secret.md").to_str().unwrap())
            .is_err());
        assert!(granted.check_root(dir.join("repo/docs").to_str().unwrap()).is_err());
    }

    #[test]
    fn listing_finds_markdown_and_skips_noise() {
        let dir = fixture("list");
        let (files, truncated) = list_markdown(&dir.join("repo")).unwrap();
        assert_eq!(files, vec![".github/CONTRIBUTING.md", "docs/setup.md", "README.md"]);
        assert!(!truncated);
    }

    #[cfg(unix)]
    #[test]
    fn listing_does_not_follow_symlinks() {
        let dir = fixture("symlink");
        std::os::unix::fs::symlink(dir.join("outside"), dir.join("repo/linked")).unwrap();
        std::os::unix::fs::symlink(dir.join("repo"), dir.join("repo/docs/loop")).unwrap();
        let (files, _) = list_markdown(&dir.join("repo")).unwrap();
        assert!(files.iter().all(|f| !f.contains("secret") && !f.contains("loop")));
    }

    #[test]
    fn links_and_images_resolve_inside_the_folder() {
        let dir = fixture("resolve");
        let root = dir.join("repo");
        let doc = root.join("docs/setup.md");
        assert_eq!(
            resolve_target(&doc, "images/a.png", Some(&root)).unwrap(),
            root.join("docs/images/a.png")
        );
        assert_eq!(
            resolve_target(&doc, "../README.md", Some(&root)).unwrap(),
            root.join("README.md")
        );
        assert_eq!(
            resolve_target(&doc, "/README.md", Some(&root)).unwrap(),
            root.join("README.md")
        );
        assert!(resolve_target(&doc, "../../outside/secret.md", Some(&root)).is_err());
        assert!(resolve_target(&doc, "/../outside/logo.png", Some(&root)).is_err());
    }

    #[test]
    fn a_lone_file_may_reach_neighbours_but_never_roots_or_hosts() {
        let dir = fixture("lone");
        let doc = dir.join("outside/secret.md");
        assert!(resolve_target(&doc, "logo.png", None).is_ok());
        assert!(resolve_target(&doc, "../repo/README.md", None).is_ok());
        for bad in [
            "/etc/passwd",
            "\\\\attacker\\share\\x.png",
            "//attacker/share/x.png",
            "file:///etc/x.png",
            "C:\\x.png",
            "",
        ] {
            assert!(resolve_target(&doc, bad, None).is_err(), "{bad}");
        }
    }

    #[test]
    fn folder_links_open_their_readme() {
        let dir = fixture("readme");
        assert_eq!(document_in(dir.join("repo")), Some(dir.join("repo/README.md")));
        assert_eq!(document_in(dir.join("repo/src")), None);
        assert_eq!(document_in(dir.join("repo/src/main.rs")), None);
    }

    #[test]
    fn atomic_write_replaces_content_and_leaves_no_temp_file() {
        let dir = fixture("write");
        let doc = dir.join("repo/README.md");
        write_atomic(&doc, b"# new\r\n").unwrap();
        assert_eq!(fs::read(&doc).unwrap(), b"# new\r\n");
        let leftovers = fs::read_dir(dir.join("repo"))
            .unwrap()
            .filter(|e| e.as_ref().unwrap().file_name().to_string_lossy().ends_with(".ztg-tmp"));
        assert_eq!(leftovers.count(), 0);
    }

    #[test]
    fn percent_decoding_round_trips_unicode_and_rejects_bad_input() {
        assert_eq!(
            percent_decode("C%3A%5CUsers%5Cj%C3%B6rg%5Ca%20b.md").unwrap(),
            "C:\\Users\\jörg\\a b.md"
        );
        assert!(percent_decode("%zz").is_none());
        assert!(percent_decode("%c3").is_none());
    }

    #[test]
    fn suggested_names_cannot_carry_paths() {
        assert_eq!(suggested_name("..\\..\\Windows\\evil.md", "x.md"), "Windowsevil.md");
        assert_eq!(suggested_name("  ", "x.md"), "x.md");
        assert_eq!(suggested_name("report.md", "x.md"), "report.md");
    }

    #[test]
    fn startup_accepts_a_document_or_a_folder() {
        let dir = fixture("args");
        let doc = dir.join("repo/README.md");
        let args = vec![
            "--flag".to_string(),
            dir.join("missing.md").to_string_lossy().into(),
            doc.to_string_lossy().into(),
        ];
        assert_eq!(startup_argument(args.into_iter()), Some((doc, false)));
        let args = vec![dir.join("repo").to_string_lossy().into_owned()];
        assert_eq!(startup_argument(args.into_iter()), Some((dir.join("repo"), true)));
    }
}
