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
//! - A link to a file that is not Markdown (`linked.rs`, RD16) is resolved here with the same
//!   rules, judged by an allow-list of document types (programs and scripts are refused, never
//!   run), shown to the reader in a confirmation, and only then opened in the operating system's
//!   default application, with no arguments and no shell.
//! - Pasting or dropping an image (`images.rs`, RD13) saves it in an `images` folder beside a
//!   granted document, never through a link, never over a file, only PNG, JPEG, GIF or WebP whose
//!   bytes match, up to 20 MiB. A dropped file is one Rust saw dropped on the window and is copied.
//! - The session (the open folder and document, so the next start reopens them) is kept by Rust
//!   in the app's local data folder. The WebView can only ask to remember paths that are already
//!   granted, and a stored path is checked again (it must still exist and be a folder or a
//!   document) before it is granted at the next start.
//! - The recent-files list is kept the same way (`recent.rs`). Only a granted document can be
//!   added, and `open_recent` grants a path only when it is on that list.
//! - The window's size and position are remembered (`window_state.rs`) with Tauri's own window
//!   API and restored onto a monitor that exists now. The WebView is given no window permission
//!   for it and holds no say in it.

mod images;
mod linked;
mod recent;
mod window_state;

use std::collections::HashSet;
use std::fs;
use std::io::Write;
use std::path::{Component, Path, PathBuf};
use std::sync::Mutex;
use std::time::{SystemTime, UNIX_EPOCH};

use serde::{Deserialize, Serialize};
use tauri::ipc::{InvokeBody, Request, Response};
use tauri::{AppHandle, DragDropEvent, Emitter, Manager, State, Window, WindowEvent};
use tauri_plugin_dialog::{DialogExt, MessageDialogButtons, MessageDialogKind};
use tauri_plugin_opener::OpenerExt;

const DOCUMENT_EXTENSIONS: &[&str] = &["md", "markdown", "mdown", "mkd", "mkdn", "txt"];
/// Folder listings show Markdown only; a repository's .txt files are rarely documents.
const LISTED_EXTENSIONS: &[&str] = &["md", "markdown", "mdown", "mkd", "mkdn"];
const EXPORT_EXTENSIONS: &[&str] = &["html", "htm"];
const IMAGE_EXTENSIONS: &[&str] = &["png", "jpg", "jpeg", "gif", "webp", "svg", "bmp", "ico", "avif"];
const MAX_DOCUMENT_BYTES: u64 = 64 * 1024 * 1024;
const MAX_IMAGE_BYTES: u64 = 32 * 1024 * 1024;
const SESSION_FILE: &str = "session.json";
const MAX_SESSION_BYTES: u64 = 64 * 1024;

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
    /// Image files dropped on the window, the only ones `copy_dropped_image` may copy. Each drop
    /// replaces the set, and a file leaves it when it is copied.
    dropped_images: Mutex<HashSet<PathBuf>>,
    /// Held while the recent-files file is read and rewritten, so two quick opens cannot drop one.
    recent_lock: Mutex<()>,
}

/// The window's latest normal rectangle (kept from move and resize events) and whether it is
/// maximized, written when the window closes.
#[derive(Default)]
struct TrackedWindow(Mutex<Option<window_state::WindowState>>);

#[derive(Clone, Serialize, Debug, PartialEq)]
struct Initial {
    path: String,
    folder: bool,
    /// A restored session's document inside the folder, opened instead of its README.
    document: Option<String>,
}

/// What was open when the app last changed document: a folder, a document, or a document in a
/// folder. Stored as JSON in the app's local data folder.
#[derive(Serialize, Deserialize, Default, Debug, PartialEq)]
struct Session {
    folder: Option<PathBuf>,
    document: Option<PathBuf>,
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

    /// Remembers the image file of the latest drop (none clears the earlier one).
    fn offer_dropped_image(&self, path: Option<&Path>) {
        let mut set = self.dropped_images.lock().expect("allow-list lock");
        set.clear();
        if let Some(path) = path {
            set.insert(path.to_path_buf());
        }
    }

    /// Takes a dropped image off the list: the path must be one Rust saw dropped.
    fn take_dropped_image(&self, path: &str) -> Result<PathBuf, String> {
        let path = canonical(Path::new(path))?;
        if self.dropped_images.lock().expect("allow-list lock").remove(&path) {
            Ok(path)
        } else {
            Err("That image was not dropped on this window".into())
        }
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

    /// The session to store for what the WebView reports as open. Only granted paths qualify,
    /// so remembering can never widen what the next start may read.
    fn session(&self, folder: Option<&str>, document: Option<&str>) -> Result<Session, String> {
        let folder = folder.map(|f| self.check_root(f)).transpose()?;
        let document = document.map(|d| self.check(d)).transpose()?;
        if let Some(document) = &document {
            if !has_extension(document, DOCUMENT_EXTENSIONS) {
                return Err("Only Markdown and text files can be remembered".into());
            }
            if folder.as_ref().is_some_and(|f| !document.starts_with(f)) {
                return Err("The document is not in the open folder".into());
            }
        }
        Ok(Session { folder, document })
    }

    /// Grants what a startup argument or a restored session opens.
    fn grant_initial(&self, initial: Initial) {
        if initial.folder {
            self.grant_root(Path::new(&initial.path));
        } else {
            self.grant_file(Path::new(&initial.path));
        }
        *self.initial.lock().expect("initial lock") = Some(initial);
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

/// The first dropped document or folder, canonical, or the name of what was refused.
fn dropped_target(paths: &[PathBuf]) -> Result<(PathBuf, bool), String> {
    paths
        .iter()
        .filter(|p| p.is_dir() || (p.is_file() && has_extension(p, DOCUMENT_EXTENSIONS)))
        .find_map(|p| fs::canonicalize(p).ok().map(|c| (c, p.is_dir())))
        .ok_or_else(|| {
            paths
                .first()
                .and_then(|p| p.file_name())
                .map(|n| n.to_string_lossy().into_owned())
                .unwrap_or_default()
        })
}

/// What a drop of image files names: the first image file, and its canonical path when it is a
/// type that may be saved (PNG, JPEG, GIF, WebP; SVG and the rest are named but not offered).
#[derive(Clone, Serialize, Debug, PartialEq)]
struct DroppedImage {
    name: String,
    path: Option<String>,
}

fn dropped_image(paths: &[PathBuf]) -> Option<DroppedImage> {
    let file = paths
        .iter()
        .find(|p| p.is_file() && has_extension(p, IMAGE_EXTENSIONS))?;
    let name = file.file_name()?.to_string_lossy().into_owned();
    let path = if has_extension(file, images::EXTENSIONS) {
        fs::canonicalize(file).ok().map(|c| c.to_string_lossy().into_owned())
    } else {
        None
    };
    Some(DroppedImage { name, path })
}

fn startup_argument(args: impl Iterator<Item = String>) -> Option<(PathBuf, bool)> {
    args.map(PathBuf::from)
        .find(|p| p.is_dir() || (p.is_file() && has_extension(p, DOCUMENT_EXTENSIONS)))
        .and_then(|p| fs::canonicalize(&p).ok().map(|c| (c, p.is_dir())))
}

fn session_file(app: &AppHandle) -> Result<PathBuf, String> {
    app.path()
        .app_local_data_dir()
        .map(|dir| dir.join(SESSION_FILE))
        .map_err(|e| format!("No folder for the session: {e}"))
}

fn read_session(file: &Path) -> Option<Session> {
    serde_json::from_slice(&read_limited(file, MAX_SESSION_BYTES).ok()?).ok()
}

/// Writes the session, or removes the file when nothing is open.
fn write_session(file: &Path, session: &Session) -> Result<(), String> {
    if session.folder.is_none() && session.document.is_none() {
        return match fs::remove_file(file) {
            Err(e) if e.kind() != std::io::ErrorKind::NotFound => Err(format!("Could not forget the session: {e}")),
            _ => Ok(()),
        };
    }
    let folder = file.parent().ok_or("The path has no folder")?;
    fs::create_dir_all(folder).map_err(|e| format!("Could not create the session folder: {e}"))?;
    write_atomic(file, &serde_json::to_vec(session).map_err(|e| e.to_string())?)
}

/// What a stored session reopens, checked as a startup argument would be: the folder must still
/// be a folder, the document a Markdown or text file, inside the folder when there is one. A
/// folder whose document has gone opens on its README; a lone document that has gone opens
/// nothing.
fn restored(session: Session) -> Option<Initial> {
    let document = session
        .document
        .and_then(|d| fs::canonicalize(d).ok())
        .filter(|d| d.is_file() && has_extension(d, DOCUMENT_EXTENSIONS));
    let text = |p: &Path| p.to_string_lossy().into_owned();
    match session
        .folder
        .and_then(|f| fs::canonicalize(f).ok())
        .filter(|f| f.is_dir())
    {
        Some(folder) => Some(Initial {
            path: text(&folder),
            folder: true,
            document: document.filter(|d| d.starts_with(&folder)).map(|d| text(&d)),
        }),
        None => document.map(|d| Initial {
            path: text(&d),
            folder: false,
            document: None,
        }),
    }
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

#[derive(Serialize)]
struct SavedImage {
    name: String,
    link: String,
}

fn now_seconds() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0)
}

/// The document an image is saved beside: granted, and a Markdown or text file.
fn image_document(path: &str, granted: &Granted) -> Result<PathBuf, String> {
    let document = granted.check(path)?;
    if !has_extension(&document, DOCUMENT_EXTENSIONS) {
        return Err("Images are saved only beside a Markdown or text document".into());
    }
    Ok(document)
}

/// Saves pasted image bytes in the `images` folder beside a granted document.
#[tauri::command]
async fn paste_image(request: Request<'_>, granted: State<'_, Granted>) -> Result<SavedImage, String> {
    let document = image_document(&header(&request, "x-ztg-path")?, &granted)?;
    let claimed = images::Kind::from_mime(&header(&request, "x-ztg-type")?)
        .ok_or("Only PNG, JPEG, GIF and WebP images can be added")?;
    let bytes = raw_body(&request)?;
    let kind = images::check_bytes(&bytes, claimed)?;
    let saved = images::save(&document, None, kind, &bytes, now_seconds())?;
    Ok(SavedImage {
        name: saved.name,
        link: saved.link,
    })
}

/// Copies an image file the user dropped on the window into the `images` folder beside a granted
/// document. The original is read, never moved or changed.
#[tauri::command]
async fn copy_dropped_image(
    document: String,
    source: String,
    granted: State<'_, Granted>,
) -> Result<SavedImage, String> {
    let document = image_document(&document, &granted)?;
    let source = granted.take_dropped_image(&source)?;
    let claimed = source
        .extension()
        .and_then(|e| e.to_str())
        .and_then(images::Kind::from_extension)
        .ok_or("Only PNG, JPEG, GIF and WebP images can be added")?;
    let bytes = read_limited(&source, images::MAX_BYTES)?;
    let kind = images::check_bytes(&bytes, claimed)?;
    let original = source.file_name().map(|n| n.to_string_lossy().into_owned());
    let saved = images::save(&document, original.as_deref(), kind, &bytes, now_seconds())?;
    Ok(SavedImage {
        name: saved.name,
        link: saved.link,
    })
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

/// Which of the two answers a link to a non-Markdown file gets (RD16).
#[derive(Serialize)]
struct LinkFileCheck {
    /// `confirm` (ask the reader), `refused` (say why) or `folder` (a Markdown rule applies).
    status: &'static str,
    /// The resolved full path, shown in the confirmation.
    path: String,
    name: String,
    /// The type, for example `PDF document (.pdf)`.
    kind: String,
    /// The reason, when refused.
    message: String,
}

fn link_check(document: &str, target: &str, granted: &Granted) -> Result<linked::LinkCheck, String> {
    let document = granted.check(document)?;
    let root = granted.root_of(&document);
    Ok(linked::check(&document, target, root.as_deref()))
}

/// Judges a link to a file that is not Markdown without opening anything: the WebView shows the
/// answer and asks the reader.
#[tauri::command]
async fn inspect_link_file(
    document: String,
    target: String,
    granted: State<'_, Granted>,
) -> Result<LinkFileCheck, String> {
    let blank = |status, message: String| LinkFileCheck {
        status,
        path: String::new(),
        name: String::new(),
        kind: String::new(),
        message,
    };
    Ok(match link_check(&document, &target, &granted)? {
        linked::LinkCheck::Folder => blank("folder", String::new()),
        linked::LinkCheck::Refused(message) => blank("refused", message),
        linked::LinkCheck::Confirm(file) => LinkFileCheck {
            status: "confirm",
            path: file.path,
            name: file.name,
            kind: file.kind,
            message: String::new(),
        },
    })
}

/// Opens a linked file in its default application. Everything is resolved and judged again, and
/// the file must still be the one the reader confirmed. No arguments, no shell.
#[tauri::command]
async fn open_link_file(
    app: AppHandle,
    document: String,
    target: String,
    confirmed: String,
    granted: State<'_, Granted>,
) -> Result<(), String> {
    match link_check(&document, &target, &granted)? {
        linked::LinkCheck::Confirm(file) if file.path == confirmed => app
            .opener()
            .open_path(file.path, None::<&str>)
            .map_err(|e| format!("Could not open the file: {e}")),
        linked::LinkCheck::Confirm(_) => Err("The link now points to a different file".into()),
        linked::LinkCheck::Refused(message) => Err(message),
        linked::LinkCheck::Folder => Err("The link points to a folder".into()),
    }
}

#[tauri::command]
fn initial_document(granted: State<'_, Granted>) -> Option<Initial> {
    granted.initial.lock().expect("initial lock").take()
}

/// Stores what is open so the next start reopens it; with neither, forgets the session.
#[tauri::command]
async fn remember_session(
    app: AppHandle,
    folder: Option<String>,
    document: Option<String>,
    granted: State<'_, Granted>,
) -> Result<(), String> {
    let session = granted.session(folder.as_deref(), document.as_deref())?;
    write_session(&session_file(&app)?, &session)
}

fn local_file(app: &AppHandle, name: &str) -> Result<PathBuf, String> {
    app.path()
        .app_local_data_dir()
        .map(|dir| dir.join(name))
        .map_err(|e| format!("No folder for {name}: {e}"))
}

fn load_recent(app: &AppHandle) -> Vec<String> {
    local_file(app, recent::RECENT_FILE)
        .ok()
        .and_then(|file| read_limited(&file, MAX_SESSION_BYTES).ok())
        .map(|bytes| recent::parse(&bytes, recent::MAX_RECENT))
        .unwrap_or_default()
}

/// Writes the list, or removes the file when it is empty.
fn store_recent(app: &AppHandle, list: &[String]) -> Result<(), String> {
    let file = local_file(app, recent::RECENT_FILE)?;
    if list.is_empty() {
        return match fs::remove_file(&file) {
            Err(e) if e.kind() != std::io::ErrorKind::NotFound => Err(format!("Could not clear the recent files: {e}")),
            _ => Ok(()),
        };
    }
    let folder = file.parent().ok_or("The path has no folder")?;
    fs::create_dir_all(folder).map_err(|e| format!("Could not create the data folder: {e}"))?;
    write_atomic(&file, &recent::serialize(list))
}

#[derive(Serialize)]
struct RecentFile {
    path: String,
    name: String,
    dir: String,
    exists: bool,
}

/// The recent documents, newest first, each marked when it is no longer there.
#[tauri::command]
async fn recent_files(app: AppHandle, granted: State<'_, Granted>) -> Result<Vec<RecentFile>, String> {
    let _guard = granted.recent_lock.lock().expect("recent lock");
    Ok(load_recent(&app)
        .into_iter()
        .map(|path| {
            let (name, dir) = recent::name_and_dir(&path);
            let exists = Path::new(&path).is_file();
            RecentFile {
                path,
                name,
                dir,
                exists,
            }
        })
        .collect())
}

/// Puts a document first on the list. Only a document the user already opened is accepted.
#[tauri::command]
async fn add_recent(app: AppHandle, path: String, granted: State<'_, Granted>) -> Result<(), String> {
    let path = granted.check(&path)?;
    if !has_extension(&path, DOCUMENT_EXTENSIONS) {
        return Err("Only Markdown and text files can be remembered".into());
    }
    let _guard = granted.recent_lock.lock().expect("recent lock");
    let list = recent::push(
        load_recent(&app),
        path.to_string_lossy().into_owned(),
        recent::MAX_RECENT,
    );
    store_recent(&app, &list)
}

#[tauri::command]
async fn remove_recent(app: AppHandle, path: String, granted: State<'_, Granted>) -> Result<(), String> {
    let _guard = granted.recent_lock.lock().expect("recent lock");
    store_recent(&app, &recent::remove(load_recent(&app), &path))
}

#[tauri::command]
async fn clear_recent(app: AppHandle, granted: State<'_, Granted>) -> Result<(), String> {
    let _guard = granted.recent_lock.lock().expect("recent lock");
    store_recent(&app, &[])
}

/// Grants a document from the list again and returns its path; None when it no longer exists.
/// A path that is not on the list is refused, which is what keeps this from being a way in.
#[tauri::command]
async fn open_recent(app: AppHandle, path: String, granted: State<'_, Granted>) -> Result<Option<String>, String> {
    {
        let _guard = granted.recent_lock.lock().expect("recent lock");
        if !load_recent(&app).iter().any(|known| recent::same_path(known, &path)) {
            return Err("That file is not in the recent files".into());
        }
    }
    let Ok(file) = fs::canonicalize(&path) else {
        return Ok(None);
    };
    if !file.is_file() || !has_extension(&file, DOCUMENT_EXTENSIONS) {
        return Ok(None);
    }
    granted.grant_file(&file);
    Ok(Some(file.to_string_lossy().into_owned()))
}

/// Puts the window where it was, on a monitor that exists now, before it is shown. Best effort:
/// whatever fails, the window is still shown where the system put it.
fn restore_window(app: &tauri::App) {
    let Some(window) = app.get_webview_window("main") else {
        return;
    };
    let state = local_file(app.handle(), window_state::WINDOW_FILE)
        .ok()
        .and_then(|file| read_limited(&file, MAX_SESSION_BYTES).ok())
        .and_then(|bytes| window_state::parse(&bytes));
    if let Some(state) = state {
        let primary = window.primary_monitor().ok().flatten();
        let mut areas: Vec<window_state::Area> = window
            .available_monitors()
            .unwrap_or_default()
            .iter()
            .map(|m| {
                let work = m.work_area();
                window_state::Area {
                    work: window_state::Rect {
                        x: work.position.x,
                        y: work.position.y,
                        width: work.size.width,
                        height: work.size.height,
                    },
                    scale: m.scale_factor(),
                }
            })
            .collect();
        // The primary monitor first: it is where a window with no monitor of its own goes.
        if let Some(primary) = primary {
            let at = primary.work_area().position;
            if let Some(i) = areas.iter().position(|a| a.work.x == at.x && a.work.y == at.y) {
                areas.swap(0, i);
            }
        }
        if let Some(rect) = window_state::place(&state.rect(), &areas, window_state::MIN_LOGICAL) {
            // Position first: moving to a monitor with another scale factor can resize the
            // window, so the size is set once it is there, and the position once more after.
            let position = tauri::PhysicalPosition::new(rect.x, rect.y);
            let size = tauri::PhysicalSize::new(rect.width, rect.height);
            let _ = window.set_position(position);
            let _ = window.set_size(size);
            let _ = window.set_position(position);
        }
        if state.maximized {
            let _ = window.maximize();
        }
    }
    let _ = window.show();
    track_window(&window.as_ref().window());
}

/// Keeps the window's normal rectangle current. A maximized, minimized or full-screen window
/// reports a rectangle that is not the one to come back to, so only the maximized flag changes.
fn track_window(window: &Window) {
    if window.label() != "main" {
        return;
    }
    let tracked = window.state::<TrackedWindow>();
    let mut current = tracked.0.lock().expect("window state lock");
    if window.is_minimized().unwrap_or(false) || window.is_fullscreen().unwrap_or(false) {
        return;
    }
    if window.is_maximized().unwrap_or(false) {
        if let Some(state) = current.as_mut() {
            state.maximized = true;
        }
        return;
    }
    let (Ok(position), Ok(size)) = (window.outer_position(), window.outer_size()) else {
        return;
    };
    *current = Some(window_state::WindowState {
        x: position.x,
        y: position.y,
        width: size.width,
        height: size.height,
        maximized: false,
    });
}

/// Writes the window's state; best effort, called when the window is closing.
fn save_window(window: &Window) {
    if window.label() != "main" {
        return;
    }
    let Some(state) = *window.state::<TrackedWindow>().0.lock().expect("window state lock") else {
        return;
    };
    let Ok(file) = local_file(window.app_handle(), window_state::WINDOW_FILE) else {
        return;
    };
    let Some(folder) = file.parent() else { return };
    if fs::create_dir_all(folder).is_ok() {
        let _ = write_atomic(&file, &window_state::serialize(&state));
    }
}

pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_opener::init())
        .manage(Granted::default())
        .manage(TrackedWindow::default())
        .setup(|app| {
            restore_window(app);
            // A file or folder named on the command line wins over the stored session.
            let initial = match startup_argument(std::env::args().skip(1)) {
                Some((path, folder)) => Some(Initial {
                    path: path.to_string_lossy().into_owned(),
                    folder,
                    document: None,
                }),
                None => session_file(app.handle())
                    .ok()
                    .and_then(|file| read_session(&file))
                    .and_then(restored),
            };
            if let Some(initial) = initial {
                app.state::<Granted>().grant_initial(initial);
            }
            Ok(())
        })
        .on_window_event(|window, event| {
            let drag = match event {
                WindowEvent::DragDrop(drag) => drag,
                WindowEvent::Moved(_) | WindowEvent::Resized(_) => return track_window(window),
                // Closing is where the state is written; a refused close does no harm.
                WindowEvent::CloseRequested { .. } | WindowEvent::Destroyed => return save_window(window),
                _ => return,
            };
            match drag {
                DragDropEvent::Enter { .. } => {
                    let _ = window.emit("document-drag", true);
                }
                DragDropEvent::Leave => {
                    let _ = window.emit("document-drag", false);
                }
                DragDropEvent::Drop { paths, .. } => {
                    let _ = window.emit("document-drag", false);
                    match dropped_target(paths) {
                        Ok((path, folder)) => {
                            let granted = window.state::<Granted>();
                            if folder {
                                granted.grant_root(&path);
                            } else {
                                granted.grant_file(&path);
                            }
                            let initial = Initial {
                                path: path.to_string_lossy().into_owned(),
                                folder,
                                document: None,
                            };
                            let _ = window.emit("document-dropped", initial);
                        }
                        Err(name) => match dropped_image(paths) {
                            Some(image) => {
                                let granted = window.state::<Granted>();
                                granted.offer_dropped_image(image.path.as_deref().map(Path::new));
                                let _ = window.emit("image-dropped", image);
                            }
                            None => {
                                let _ = window.emit("document-drop-refused", name);
                            }
                        },
                    }
                }
                _ => {}
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
            paste_image,
            copy_dropped_image,
            resolve_link,
            inspect_link_file,
            open_link_file,
            initial_document,
            remember_session,
            recent_files,
            add_recent,
            remove_recent,
            clear_recent,
            open_recent
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
    fn drops_open_documents_and_folders_and_name_anything_else() {
        let dir = fixture("drop");
        let doc = dir.join("repo/README.md");
        assert_eq!(
            dropped_target(&[dir.join("repo/src/main.rs"), doc.clone()]),
            Ok((doc, false))
        );
        assert_eq!(dropped_target(&[dir.join("repo")]), Ok((dir.join("repo"), true)));
        assert_eq!(
            dropped_target(&[dir.join("repo/src/main.rs")]),
            Err("main.rs".to_string())
        );
        assert_eq!(dropped_target(&[]), Err(String::new()));
    }

    #[test]
    fn a_dropped_image_is_named_and_only_the_safe_types_are_offered() {
        let dir = fixture("drop-image");
        let png = dir.join("outside/logo.png");
        let svg = dir.join("outside/logo.svg");
        fs::write(&svg, "<svg/>").unwrap();
        let found = dropped_image(std::slice::from_ref(&png)).unwrap();
        assert_eq!(found.name, "logo.png");
        assert_eq!(found.path.as_deref(), Some(png.to_string_lossy().as_ref()));
        let found = dropped_image(&[svg]).unwrap();
        assert_eq!((found.name.as_str(), found.path), ("logo.svg", None));
        assert_eq!(dropped_image(&[dir.join("outside/secret.md")]), None);
        assert_eq!(dropped_image(&[dir.join("repo")]), None);
        let _ = fs::remove_dir_all(dir);
    }

    #[test]
    fn only_the_latest_dropped_image_can_be_copied_and_only_once() {
        let dir = fixture("drop-take");
        let png = dir.join("outside/logo.png");
        let other = dir.join("outside/other.png");
        fs::write(&other, "png").unwrap();
        let granted = Granted::default();
        let invented = png.to_string_lossy().into_owned();
        assert!(granted.take_dropped_image(&invented).is_err(), "never dropped");
        granted.offer_dropped_image(Some(&png));
        assert_eq!(granted.take_dropped_image(&invented).unwrap(), png);
        assert!(granted.take_dropped_image(&invented).is_err(), "single use");
        granted.offer_dropped_image(Some(&png));
        granted.offer_dropped_image(Some(&other));
        assert!(
            granted.take_dropped_image(&invented).is_err(),
            "replaced by a later drop"
        );
        granted.offer_dropped_image(None);
        assert!(granted.take_dropped_image(&other.to_string_lossy()).is_err());
        let _ = fs::remove_dir_all(dir);
    }

    #[test]
    fn images_are_saved_only_beside_a_granted_document() {
        let dir = fixture("image-document");
        let granted = Granted::default();
        let doc = dir.join("repo/docs/setup.md");
        let outside = dir.join("outside/secret.md");
        assert!(image_document(&doc.to_string_lossy(), &granted).is_err(), "not granted");
        granted.grant_root(&dir.join("repo"));
        assert_eq!(image_document(&doc.to_string_lossy(), &granted).unwrap(), doc);
        assert!(
            image_document(&outside.to_string_lossy(), &granted).is_err(),
            "outside the folder"
        );
        granted.grant_file(&dir.join("repo/src/main.rs"));
        assert!(
            image_document(&dir.join("repo/src/main.rs").to_string_lossy(), &granted).is_err(),
            "not a document"
        );
        let _ = fs::remove_dir_all(dir);
    }

    #[test]
    fn only_granted_paths_are_remembered() {
        let dir = fixture("remember");
        let root = dir.join("repo");
        let doc = root.join("docs/setup.md");
        let (root_s, doc_s) = (root.to_str().unwrap(), doc.to_str().unwrap());
        let secret = dir.join("outside/secret.md");
        let granted = Granted::default();
        assert!(granted.session(Some(root_s), None).is_err());
        assert!(granted.session(None, Some(doc_s)).is_err());
        granted.grant_root(&root);
        assert_eq!(
            granted.session(Some(root_s), Some(doc_s)).unwrap(),
            Session {
                folder: Some(root.clone()),
                document: Some(doc.clone())
            }
        );
        assert!(granted
            .session(None, Some(root.join("docs/images/a.png").to_str().unwrap()))
            .is_err());
        assert!(granted
            .session(Some(root.join("docs").to_str().unwrap()), None)
            .is_err());
        granted.grant_file(&secret);
        assert!(granted.session(Some(root_s), Some(secret.to_str().unwrap())).is_err());
        assert!(granted.session(None, Some(secret.to_str().unwrap())).is_ok());
        assert_eq!(granted.session(None, None).unwrap(), Session::default());
    }

    #[test]
    fn a_session_round_trips_and_an_empty_one_removes_the_file() {
        let dir = fixture("session");
        let file = dir.join("data/sh.ztg.readout").join(SESSION_FILE);
        let session = Session {
            folder: Some(dir.join("repo")),
            document: Some(dir.join("repo/README.md")),
        };
        write_session(&file, &session).unwrap();
        assert_eq!(read_session(&file), Some(session));
        write_session(&file, &Session::default()).unwrap();
        assert!(!file.exists());
        write_session(&file, &Session::default()).unwrap();
        fs::write(&file, "not json").unwrap();
        assert_eq!(read_session(&file), None);
    }

    #[test]
    fn a_restored_session_reopens_only_what_still_qualifies() {
        let dir = fixture("restore");
        let root = dir.join("repo");
        let text = |p: PathBuf| p.to_string_lossy().into_owned();
        let session = |folder: Option<&str>, document: Option<&str>| Session {
            folder: folder.map(|f| dir.join(f)),
            document: document.map(|d| dir.join(d)),
        };
        assert_eq!(
            restored(session(Some("repo"), Some("repo/docs/../docs/setup.md"))),
            Some(Initial {
                path: text(root.clone()),
                folder: true,
                document: Some(text(root.join("docs/setup.md")))
            })
        );
        let readme_only = Some(Initial {
            path: text(root.clone()),
            folder: true,
            document: None,
        });
        assert_eq!(restored(session(Some("repo"), Some("repo/gone.md"))), readme_only);
        assert_eq!(restored(session(Some("repo"), Some("outside/secret.md"))), readme_only);
        assert_eq!(restored(session(Some("repo"), Some("repo/src/main.rs"))), readme_only);
        assert_eq!(
            restored(session(Some("gone"), Some("outside/secret.md"))),
            Some(Initial {
                path: text(dir.join("outside/secret.md")),
                folder: false,
                document: None
            })
        );
        assert_eq!(restored(session(Some("repo/README.md"), None)), None);
        assert_eq!(restored(session(None, Some("repo/docs/images/a.png"))), None);
        assert_eq!(restored(session(None, Some("gone.md"))), None);
        assert_eq!(restored(Session::default()), None);
    }

    #[test]
    fn a_restored_session_is_granted() {
        let dir = fixture("grant-initial");
        let granted = Granted::default();
        let initial = restored(Session {
            folder: Some(dir.join("repo")),
            document: None,
        })
        .unwrap();
        granted.grant_initial(initial.clone());
        assert!(granted.check(dir.join("repo/docs/setup.md").to_str().unwrap()).is_ok());
        assert!(granted.check(dir.join("outside/secret.md").to_str().unwrap()).is_err());
        assert_eq!(granted.initial.lock().unwrap().take(), Some(initial));
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
