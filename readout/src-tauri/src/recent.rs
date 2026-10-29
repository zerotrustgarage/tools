//! The recent-files list: the last documents the reader opened, newest first.
//!
//! The list is kept here, in the app's local data folder beside the session, because the WebView
//! may open only paths on the allow-list (`Granted`). Only a path that was already granted can
//! be added, and `open_recent` grants a path again only when it is on this list, so the WebView
//! cannot use the list to reach any other file. The pure parts (ordering, the cap, reading a
//! damaged file) are here so they can be tested without a window.

use serde_json::{json, Value};

pub const MAX_RECENT: usize = 10;
pub const RECENT_FILE: &str = "recent.json";

/// Two paths are the same file. Windows paths ignore case; elsewhere they do not.
pub fn same_path(a: &str, b: &str) -> bool {
    same_path_with(a, b, cfg!(windows))
}

fn same_path_with(a: &str, b: &str, ignore_case: bool) -> bool {
    if ignore_case {
        a.to_lowercase() == b.to_lowercase()
    } else {
        a == b
    }
}

/// Puts path first, drops an older line for the same file, and keeps the newest `max`.
pub fn push(list: Vec<String>, path: String, max: usize) -> Vec<String> {
    let mut next = Vec::with_capacity(list.len() + 1);
    next.push(path);
    for known in list {
        if !same_path(&known, &next[0]) {
            next.push(known);
        }
    }
    next.truncate(max);
    next
}

pub fn remove(list: Vec<String>, path: &str) -> Vec<String> {
    list.into_iter().filter(|known| !same_path(known, path)).collect()
}

/// Reads a stored list. Anything that is not the expected shape, and every line that is not a
/// non-empty string, is dropped; repeats keep the first (newest); the cap applies. Never fails,
/// so a damaged file only ever means a shorter list.
pub fn parse(bytes: &[u8], max: usize) -> Vec<String> {
    let Ok(Value::Object(root)) = serde_json::from_slice::<Value>(bytes) else {
        return Vec::new();
    };
    let Some(Value::Array(items)) = root.get("files") else {
        return Vec::new();
    };
    let mut list: Vec<String> = Vec::new();
    for item in items {
        if list.len() >= max {
            break;
        }
        match item {
            Value::String(path) if !path.is_empty() && !list.iter().any(|known| same_path(known, path)) => {
                list.push(path.clone())
            }
            _ => {}
        }
    }
    list
}

pub fn serialize(list: &[String]) -> Vec<u8> {
    serde_json::to_vec(&json!({ "files": list })).unwrap_or_default()
}

/// A path as the reader should see it: Windows' extended-length prefix is an implementation
/// detail of canonical paths.
pub fn display_path(path: &str) -> &str {
    match path.strip_prefix(r"\\?\") {
        Some(rest) if !rest.starts_with(r"UNC\") => rest,
        _ => path,
    }
}

/// The file name and the folder it is in, for the list. Both separators count on every system,
/// so a Windows path is split the same way wherever this runs.
pub fn name_and_dir(path: &str) -> (String, String) {
    let shown = display_path(path);
    match shown.rfind(['/', '\\']) {
        Some(at) => {
            let dir = &shown[..at];
            // "C:" and "" are drive and filesystem roots; keep their separator.
            let dir = if dir.is_empty() || dir.ends_with(':') {
                &shown[..=at]
            } else {
                dir
            };
            (shown[at + 1..].to_string(), dir.to_string())
        }
        None => (shown.to_string(), String::new()),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn list(items: &[&str]) -> Vec<String> {
        items.iter().map(|s| s.to_string()).collect()
    }

    #[test]
    fn a_new_document_goes_first_and_a_repeat_moves_up_without_a_duplicate() {
        let next = push(list(&["/a.md", "/b.md"]), "/c.md".into(), MAX_RECENT);
        assert_eq!(next, list(&["/c.md", "/a.md", "/b.md"]));
        let again = push(next, "/b.md".into(), MAX_RECENT);
        assert_eq!(again, list(&["/b.md", "/c.md", "/a.md"]));
    }

    #[test]
    fn the_list_keeps_only_the_newest_entries() {
        let mut current = Vec::new();
        for n in 0..15 {
            current = push(current, format!("/doc{n}.md"), MAX_RECENT);
        }
        assert_eq!(current.len(), MAX_RECENT);
        assert_eq!(current.first().unwrap(), "/doc14.md");
        assert_eq!(current.last().unwrap(), "/doc5.md");
    }

    #[test]
    fn windows_paths_ignore_case_and_others_do_not() {
        assert!(same_path_with(r"C:\Docs\A.md", r"c:\docs\a.md", true));
        assert!(!same_path_with("/Docs/A.md", "/docs/a.md", false));
    }

    #[test]
    fn removing_drops_only_that_file() {
        assert_eq!(
            remove(list(&["/a.md", "/b.md", "/c.md"]), "/b.md"),
            list(&["/a.md", "/c.md"])
        );
        assert_eq!(remove(list(&["/a.md"]), "/zzz.md"), list(&["/a.md"]));
    }

    #[test]
    fn a_list_round_trips() {
        let stored = list(&["/a.md", "C:\\x\\b.md", "/ü ö/ç.md"]);
        assert_eq!(parse(&serialize(&stored), MAX_RECENT), stored);
        assert_eq!(parse(&serialize(&[]), MAX_RECENT), Vec::<String>::new());
    }

    #[test]
    fn a_damaged_file_gives_a_shorter_list_never_an_error() {
        assert!(parse(b"", MAX_RECENT).is_empty());
        assert!(parse(b"not json", MAX_RECENT).is_empty());
        assert!(parse(b"[\"/a.md\"]", MAX_RECENT).is_empty());
        assert!(parse(br#"{"files": "/a.md"}"#, MAX_RECENT).is_empty());
        assert_eq!(
            parse(
                br#"{"files": ["/a.md", 7, null, "", ["/x.md"], {"p": 1}, "/b.md", "/a.md"]}"#,
                MAX_RECENT
            ),
            list(&["/a.md", "/b.md"])
        );
        let many = serialize(&(0..30).map(|n| format!("/{n}.md")).collect::<Vec<_>>());
        assert_eq!(parse(&many, MAX_RECENT).len(), MAX_RECENT);
    }

    #[test]
    fn names_and_folders_are_shown_without_the_extended_length_prefix() {
        assert_eq!(name_and_dir(r"\\?\C:\Docs\notes.md").0, "notes.md");
        assert_eq!(display_path(r"\\?\C:\Docs\notes.md"), r"C:\Docs\notes.md");
        assert_eq!(display_path(r"\\?\UNC\host\share\a.md"), r"\\?\UNC\host\share\a.md");
        let (name, dir) = name_and_dir("/home/u/docs/a.md");
        assert_eq!((name.as_str(), dir.as_str()), ("a.md", "/home/u/docs"));
        assert_eq!(name_and_dir(r"\\?\C:\Docs\notes.md").1, r"C:\Docs");
        assert_eq!(
            name_and_dir(r"C:\notes.md"),
            ("notes.md".to_string(), r"C:\".to_string())
        );
        assert_eq!(name_and_dir("/a.md"), ("a.md".to_string(), "/".to_string()));
        assert_eq!(name_and_dir("a.md"), ("a.md".to_string(), String::new()));
    }
}
