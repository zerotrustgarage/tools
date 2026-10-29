//! Pasting or dropping an image into a document (RD13): the image is saved in an `images` folder
//! beside the document and the editor inserts a relative link.
//!
//! The WebView never names a path to write. Two commands (`paste_image` and `copy_dropped_image`
//! in lib.rs) take a document the app has granted and either the clipboard's bytes or a file the
//! user dropped on the window (recorded by Rust when the drop happened). The rules live here as
//! plain functions that can be tested without a window:
//!
//! - Only PNG, JPEG, GIF and WebP. The bytes must start with the magic bytes of the type the
//!   clipboard or the file name claimed, so a script renamed to `x.png` is refused. SVG (it can
//!   carry script) and every other type are refused.
//! - At most `MAX_BYTES`, and never empty.
//! - The name is generated (`image-YYYYMMDD-HHMMSS.png`, UTC, because the standard library has no
//!   local time) or, for a dropped file, its name cut down to `[A-Za-z0-9._-]` with the extension
//!   taken from the sniffed type, never from the name. Windows device names are avoided.
//! - The folder is `images` directly inside the document's folder. If `images` exists as a link
//!   (a symbolic link or a junction) or as anything but a folder, nothing is written. Its
//!   canonical path must be a direct child of the document's folder.
//! - The file is created with create-new semantics, so an existing file is never overwritten; a
//!   taken name gets a counter (`-2`, `-3`, ...). A failed write removes the file this call made.
//! - A dropped file is copied; the original stays where it was.

use std::fs;
use std::io::{ErrorKind, Write};
use std::path::{Path, PathBuf};

/// The most an image may weigh: 20 MiB.
pub const MAX_BYTES: u64 = 20 * 1024 * 1024;
/// The folder beside the document that receives images.
pub const FOLDER: &str = "images";
/// Extensions of the types that may be saved, for judging a dropped file by its name.
pub const EXTENSIONS: &[&str] = &["png", "jpg", "jpeg", "gif", "webp"];

const MAX_STEM: usize = 64;
const MAX_ATTEMPTS: u32 = 999;
const RESERVED: &[&str] = &[
    "con", "prn", "aux", "nul", "com1", "com2", "com3", "com4", "com5", "com6", "com7", "com8", "com9", "lpt1", "lpt2",
    "lpt3", "lpt4", "lpt5", "lpt6", "lpt7", "lpt8", "lpt9",
];

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Kind {
    Png,
    Jpeg,
    Gif,
    Webp,
}

impl Kind {
    /// The extension the saved file gets.
    pub fn extension(self) -> &'static str {
        match self {
            Kind::Png => "png",
            Kind::Jpeg => "jpg",
            Kind::Gif => "gif",
            Kind::Webp => "webp",
        }
    }

    pub fn from_mime(mime: &str) -> Option<Kind> {
        match mime.trim().to_ascii_lowercase().as_str() {
            "image/png" => Some(Kind::Png),
            "image/jpeg" | "image/jpg" => Some(Kind::Jpeg),
            "image/gif" => Some(Kind::Gif),
            "image/webp" => Some(Kind::Webp),
            _ => None,
        }
    }

    pub fn from_extension(extension: &str) -> Option<Kind> {
        match extension.to_ascii_lowercase().as_str() {
            "png" => Some(Kind::Png),
            "jpg" | "jpeg" => Some(Kind::Jpeg),
            "gif" => Some(Kind::Gif),
            "webp" => Some(Kind::Webp),
            _ => None,
        }
    }

    /// The type the bytes say they are, from their first bytes alone.
    pub fn sniff(bytes: &[u8]) -> Option<Kind> {
        if bytes.starts_with(&[0x89, b'P', b'N', b'G', 0x0D, 0x0A, 0x1A, 0x0A]) {
            Some(Kind::Png)
        } else if bytes.starts_with(&[0xFF, 0xD8, 0xFF]) {
            Some(Kind::Jpeg)
        } else if bytes.starts_with(b"GIF87a") || bytes.starts_with(b"GIF89a") {
            Some(Kind::Gif)
        } else if bytes.len() >= 12 && &bytes[0..4] == b"RIFF" && &bytes[8..12] == b"WEBP" {
            Some(Kind::Webp)
        } else {
            None
        }
    }
}

/// Refuses empty or oversized bytes, and bytes that are not the type that was claimed.
pub fn check_bytes(bytes: &[u8], claimed: Kind) -> Result<Kind, String> {
    if bytes.is_empty() {
        return Err("The image is empty".into());
    }
    if bytes.len() as u64 > MAX_BYTES {
        return Err(format!("The image is larger than {} MB", MAX_BYTES / (1024 * 1024)));
    }
    match Kind::sniff(bytes) {
        Some(found) if found == claimed => Ok(found),
        _ => Err("The file is not the kind of image it claims to be".into()),
    }
}

/// A file name cut down to `[A-Za-z0-9._-]`, without its extension, or None when nothing usable
/// is left (an all non-ASCII name, for instance), in which case the caller generates a name.
pub fn sanitize_stem(raw: &str) -> Option<String> {
    let without_extension = match raw.rsplit_once('.') {
        Some((stem, extension)) if !stem.is_empty() && Kind::from_extension(extension).is_some() => stem,
        _ => raw,
    };
    let mut stem = String::new();
    for c in without_extension.chars() {
        let c = if c.is_ascii_alphanumeric() || matches!(c, '.' | '_' | '-') {
            c
        } else {
            '-'
        };
        if c == '-' && stem.ends_with('-') {
            continue;
        }
        stem.push(c);
    }
    let stem = stem.trim_matches(|c| c == '.' || c == '-' || c == '_');
    let stem: String = stem.chars().take(MAX_STEM).collect();
    let stem = stem.trim_end_matches(['.', '-', '_']).to_string();
    if stem.is_empty() {
        return None;
    }
    // A device name stays reserved on Windows whatever extension follows it.
    let first = stem.split('.').next().unwrap_or("");
    if RESERVED.iter().any(|r| r.eq_ignore_ascii_case(first)) {
        return Some(format!("image-{stem}"));
    }
    Some(stem)
}

/// `image-YYYYMMDD-HHMMSS` for a time given as seconds since 1970 (UTC).
pub fn timestamp_stem(seconds: u64) -> String {
    let days = (seconds / 86_400) as i64;
    let rest = seconds % 86_400;
    // Days since 1970-01-01 to a civil date (Howard Hinnant's algorithm).
    let z = days + 719_468;
    let era = z.div_euclid(146_097);
    let day_of_era = z.rem_euclid(146_097);
    let year_of_era = (day_of_era - day_of_era / 1_460 + day_of_era / 36_524 - day_of_era / 146_096) / 365;
    let day_of_year = day_of_era - (365 * year_of_era + year_of_era / 4 - year_of_era / 100);
    let shifted_month = (5 * day_of_year + 2) / 153;
    let day = day_of_year - (153 * shifted_month + 2) / 5 + 1;
    let month = if shifted_month < 10 {
        shifted_month + 3
    } else {
        shifted_month - 9
    };
    let year = year_of_era + era * 400 + i64::from(month <= 2);
    format!(
        "image-{year:04}{month:02}{day:02}-{:02}{:02}{:02}",
        rest / 3_600,
        rest % 3_600 / 60,
        rest % 60
    )
}

/// The name a new image starts from: the dropped file's cleaned name, else a timestamp.
pub fn stem_for(original: Option<&str>, seconds: u64) -> String {
    original
        .and_then(sanitize_stem)
        .unwrap_or_else(|| timestamp_stem(seconds))
}

/// The `images` folder beside a document, created when missing. `document_folder` is the
/// document's folder. Refuses a link or a non-folder named `images`, and anything whose real
/// location is not a direct child of the document's folder.
pub fn images_folder(document_folder: &Path) -> Result<PathBuf, String> {
    let base = fs::canonicalize(document_folder).map_err(|e| format!("The document's folder is not available: {e}"))?;
    let images = base.join(FOLDER);
    match fs::symlink_metadata(&images) {
        Ok(meta) if meta.file_type().is_symlink() => {
            return Err("The images folder is a link. Readout does not save images through links".into())
        }
        Ok(meta) if !meta.is_dir() => {
            return Err("There is a file named images beside the document, not a folder".into())
        }
        Ok(_) => {}
        Err(e) if e.kind() == ErrorKind::NotFound => {
            fs::create_dir(&images).map_err(|e| format!("Could not create the images folder: {e}"))?;
        }
        Err(e) => return Err(format!("Could not check the images folder: {e}")),
    }
    // Looked at again after any creation, with links resolved: it must still be right here.
    let real = fs::canonicalize(&images).map_err(|e| format!("Could not check the images folder: {e}"))?;
    let meta = fs::symlink_metadata(&images).map_err(|e| format!("Could not check the images folder: {e}"))?;
    if meta.file_type().is_symlink() || !meta.is_dir() || real.parent() != Some(base.as_path()) {
        return Err("The images folder is not directly beside the document".into());
    }
    Ok(real)
}

/// Writes the bytes to a new file in `folder`, named from `stem` and the type, adding a counter
/// while the name is taken. Create-new, so nothing is ever overwritten. Returns the file's name.
pub fn write_new(folder: &Path, stem: &str, kind: Kind, bytes: &[u8]) -> Result<String, String> {
    for attempt in 1..=MAX_ATTEMPTS {
        let name = if attempt == 1 {
            format!("{stem}.{}", kind.extension())
        } else {
            format!("{stem}-{attempt}.{}", kind.extension())
        };
        let path = folder.join(&name);
        let mut file = match fs::OpenOptions::new().write(true).create_new(true).open(&path) {
            Ok(file) => file,
            Err(e) if e.kind() == ErrorKind::AlreadyExists => continue,
            Err(e) => return Err(format!("Could not save the image: {e}")),
        };
        let written = file.write_all(bytes).and_then(|()| file.sync_all());
        drop(file);
        if let Err(e) = written {
            // The file is the one this call just created, so removing it loses nothing.
            let _ = fs::remove_file(&path);
            return Err(format!("Could not save the image: {e}"));
        }
        return Ok(name);
    }
    Err("Could not find a free name for the image".into())
}

#[derive(Debug)]
pub struct Saved {
    pub name: String,
    /// The link to put in the document, relative to it: `images/<name>`.
    pub link: String,
}

/// Saves a checked image beside `document` (a granted document's canonical path).
pub fn save(
    document: &Path,
    original_name: Option<&str>,
    kind: Kind,
    bytes: &[u8],
    seconds: u64,
) -> Result<Saved, String> {
    let folder = document.parent().ok_or("The document has no folder")?;
    let images = images_folder(folder)?;
    let name = write_new(&images, &stem_for(original_name, seconds), kind, bytes)?;
    Ok(Saved {
        link: format!("{FOLDER}/{name}"),
        name,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    const PNG: &[u8] = &[0x89, b'P', b'N', b'G', 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 0];
    const JPEG: &[u8] = &[0xFF, 0xD8, 0xFF, 0xE0, 0, 0x10];
    const GIF: &[u8] = b"GIF89a\x01\x00";
    const WEBP: &[u8] = b"RIFF\x24\x00\x00\x00WEBPVP8 ";

    fn temp(name: &str) -> PathBuf {
        let dir = std::env::temp_dir().join(format!("ztg-readout-images-{name}-{}", std::process::id()));
        let _ = fs::remove_dir_all(&dir);
        fs::create_dir_all(&dir).unwrap();
        fs::canonicalize(dir).unwrap()
    }

    #[test]
    fn magic_bytes_decide_the_type() {
        assert_eq!(Kind::sniff(PNG), Some(Kind::Png));
        assert_eq!(Kind::sniff(JPEG), Some(Kind::Jpeg));
        assert_eq!(Kind::sniff(GIF), Some(Kind::Gif));
        assert_eq!(Kind::sniff(b"GIF87a"), Some(Kind::Gif));
        assert_eq!(Kind::sniff(WEBP), Some(Kind::Webp));
        assert_eq!(Kind::sniff(b"<svg xmlns='http://www.w3.org/2000/svg'/>"), None);
        assert_eq!(Kind::sniff(b"MZ\x90\x00"), None);
        assert_eq!(Kind::sniff(b"RIFF\x00\x00\x00\x00WAVE"), None);
        assert_eq!(Kind::sniff(b"RIFF"), None);
        assert_eq!(Kind::sniff(&[]), None);
    }

    #[test]
    fn claimed_type_has_to_match_the_bytes() {
        assert_eq!(check_bytes(PNG, Kind::Png), Ok(Kind::Png));
        assert!(check_bytes(PNG, Kind::Jpeg).is_err());
        assert!(check_bytes(b"<svg/>", Kind::Png).is_err());
        assert!(check_bytes(&[], Kind::Png).is_err());
        let mut big = PNG.to_vec();
        big.resize(MAX_BYTES as usize + 1, 0);
        assert!(check_bytes(&big, Kind::Png).unwrap_err().contains("larger than 20 MB"));
        big.truncate(MAX_BYTES as usize);
        assert!(check_bytes(&big, Kind::Png).is_ok());
    }

    #[test]
    fn mime_types_and_extensions_name_four_kinds_only() {
        assert_eq!(Kind::from_mime("image/png"), Some(Kind::Png));
        assert_eq!(Kind::from_mime(" IMAGE/JPEG "), Some(Kind::Jpeg));
        assert_eq!(Kind::from_mime("image/webp"), Some(Kind::Webp));
        for refused in [
            "image/svg+xml",
            "image/bmp",
            "image/x-icon",
            "text/html",
            "application/octet-stream",
            "",
        ] {
            assert_eq!(Kind::from_mime(refused), None, "{refused}");
        }
        assert_eq!(Kind::from_extension("JPG"), Some(Kind::Jpeg));
        assert_eq!(Kind::from_extension("jpeg"), Some(Kind::Jpeg));
        for refused in ["svg", "svgz", "bmp", "exe", "html", ""] {
            assert_eq!(Kind::from_extension(refused), None, "{refused}");
        }
        assert!(EXTENSIONS.iter().all(|e| Kind::from_extension(e).is_some()));
        assert_eq!(Kind::Jpeg.extension(), "jpg");
    }

    #[test]
    fn names_are_cut_down_to_safe_characters() {
        assert_eq!(
            sanitize_stem("Screenshot 2026-10-29.png").as_deref(),
            Some("Screenshot-2026-10-29")
        );
        assert_eq!(sanitize_stem("my photo (1).JPG").as_deref(), Some("my-photo-1"));
        assert_eq!(sanitize_stem("../../etc/passwd.png").as_deref(), Some("etc-passwd"));
        assert_eq!(sanitize_stem("..\\..\\boot.png").as_deref(), Some("boot"));
        assert_eq!(sanitize_stem("a:b.png").as_deref(), Some("a-b"));
        assert_eq!(sanitize_stem("shot.png.png").as_deref(), Some("shot.png"));
        assert_eq!(sanitize_stem("report.exe").as_deref(), Some("report.exe"));
        assert_eq!(sanitize_stem("...hidden.png").as_deref(), Some("hidden"));
        assert_eq!(sanitize_stem("屏幕截图.png"), None);
        assert_eq!(sanitize_stem(".png").as_deref(), Some("png"));
        assert_eq!(sanitize_stem("---"), None);
        assert_eq!(sanitize_stem(""), None);
        let long = sanitize_stem(&format!("{}.png", "a".repeat(200))).unwrap();
        assert_eq!(long.len(), MAX_STEM);
        for raw in ["a b/c\\d:e*f?g\"h<i>j|k.png", "x\u{0}y.png", "tab\there.png"] {
            let stem = sanitize_stem(raw).unwrap();
            assert!(
                stem.chars().all(|c| c.is_ascii_alphanumeric() || "._-".contains(c)),
                "{stem}"
            );
        }
    }

    #[test]
    fn windows_device_names_are_avoided() {
        assert_eq!(sanitize_stem("CON.png").as_deref(), Some("image-CON"));
        assert_eq!(sanitize_stem("nul.png").as_deref(), Some("image-nul"));
        assert_eq!(sanitize_stem("com1.jpg").as_deref(), Some("image-com1"));
        assert_eq!(sanitize_stem("lpt9.x.png").as_deref(), Some("image-lpt9.x"));
        assert_eq!(sanitize_stem("console.png").as_deref(), Some("console"));
    }

    #[test]
    fn generated_names_carry_the_utc_time() {
        assert_eq!(timestamp_stem(0), "image-19700101-000000");
        // 2026-10-29 13:45:09 UTC
        assert_eq!(timestamp_stem(1_793_281_509), "image-20261029-134509");
        // A leap day, and the last second of a year.
        assert_eq!(timestamp_stem(1_709_164_800), "image-20240229-000000");
        assert_eq!(timestamp_stem(1_767_225_599), "image-20251231-235959");
        assert_eq!(stem_for(None, 0), "image-19700101-000000");
        assert_eq!(stem_for(Some("屏幕.png"), 0), "image-19700101-000000");
        assert_eq!(stem_for(Some("diagram.png"), 0), "diagram");
    }

    #[test]
    fn a_new_image_never_overwrites_an_existing_file() {
        let dir = temp("no-overwrite");
        fs::write(dir.join("shot.png"), b"precious").unwrap();
        let first = write_new(&dir, "shot", Kind::Png, PNG).unwrap();
        let second = write_new(&dir, "shot", Kind::Png, PNG).unwrap();
        assert_eq!((first.as_str(), second.as_str()), ("shot-2.png", "shot-3.png"));
        assert_eq!(fs::read(dir.join("shot.png")).unwrap(), b"precious");
        assert_eq!(fs::read(dir.join("shot-2.png")).unwrap(), PNG);
        // The extension comes from the sniffed type: a JPEG saved under a PNG's stem is .jpg.
        assert_eq!(write_new(&dir, "shot", Kind::Jpeg, JPEG).unwrap(), "shot.jpg");
        let _ = fs::remove_dir_all(dir);
    }

    #[test]
    fn saving_creates_images_beside_the_document_and_links_to_it() {
        let dir = temp("save");
        let document = dir.join("notes.md");
        fs::write(&document, "# notes").unwrap();
        let saved = save(&document, None, Kind::Png, PNG, 1_793_281_509).unwrap();
        assert_eq!(saved.name, "image-20261029-134509.png");
        assert_eq!(saved.link, "images/image-20261029-134509.png");
        assert_eq!(fs::read(dir.join("images").join(&saved.name)).unwrap(), PNG);
        // Same second again: a counter, and the first file is untouched.
        let again = save(&document, None, Kind::Png, PNG, 1_793_281_509).unwrap();
        assert_eq!(again.link, "images/image-20261029-134509-2.png");
        assert_eq!(fs::read(dir.join("images").join(&saved.name)).unwrap(), PNG);
        // A dropped file keeps its cleaned name.
        let dropped = save(&document, Some("Network Map.PNG"), Kind::Png, PNG, 0).unwrap();
        assert_eq!(dropped.link, "images/Network-Map.png");
        // Only the images folder and the document are there.
        let mut names: Vec<_> = fs::read_dir(&dir).unwrap().map(|e| e.unwrap().file_name()).collect();
        names.sort();
        assert_eq!(names, ["images", "notes.md"]);
        let _ = fs::remove_dir_all(dir);
    }

    #[test]
    fn an_existing_images_folder_is_reused() {
        let dir = temp("reuse");
        fs::create_dir(dir.join("images")).unwrap();
        fs::write(dir.join("images").join("old.png"), b"old").unwrap();
        let folder = images_folder(&dir).unwrap();
        assert_eq!(folder, dir.join("images"));
        assert_eq!(fs::read(folder.join("old.png")).unwrap(), b"old");
        let _ = fs::remove_dir_all(dir);
    }

    #[test]
    fn a_file_named_images_is_refused_and_left_alone() {
        let dir = temp("file-images");
        fs::write(dir.join("images"), b"not a folder").unwrap();
        let error = images_folder(&dir).unwrap_err();
        assert!(error.contains("not a folder"), "{error}");
        assert_eq!(fs::read(dir.join("images")).unwrap(), b"not a folder");
        let _ = fs::remove_dir_all(dir);
    }

    #[cfg(unix)]
    #[test]
    fn a_linked_images_folder_is_refused() {
        let dir = temp("link-images");
        let outside = temp("link-outside");
        std::os::unix::fs::symlink(&outside, dir.join("images")).unwrap();
        let document = dir.join("notes.md");
        fs::write(&document, "# notes").unwrap();
        let error = save(&document, None, Kind::Png, PNG, 0).unwrap_err();
        assert!(error.contains("a link"), "{error}");
        assert_eq!(fs::read_dir(&outside).unwrap().count(), 0, "nothing written outside");
        // A link to a folder elsewhere inside the document's own folder is refused as well.
        let _ = fs::remove_file(dir.join("images"));
        fs::create_dir(dir.join("real")).unwrap();
        std::os::unix::fs::symlink(dir.join("real"), dir.join("images")).unwrap();
        assert!(save(&document, None, Kind::Png, PNG, 0).is_err());
        assert_eq!(fs::read_dir(dir.join("real")).unwrap().count(), 0);
        let _ = fs::remove_dir_all(dir);
        let _ = fs::remove_dir_all(outside);
    }

    #[cfg(unix)]
    #[test]
    fn a_linked_document_folder_still_resolves_to_its_real_place() {
        let real = temp("real-folder");
        let holder = temp("holder");
        std::os::unix::fs::symlink(&real, holder.join("via")).unwrap();
        let folder = images_folder(&holder.join("via")).unwrap();
        assert_eq!(folder, real.join("images"));
        let _ = fs::remove_dir_all(real);
        let _ = fs::remove_dir_all(holder);
    }

    #[test]
    fn a_missing_document_folder_is_an_error_and_creates_nothing() {
        let dir = temp("missing");
        let gone = dir.join("gone");
        assert!(images_folder(&gone).is_err());
        assert!(!gone.exists());
        let _ = fs::remove_dir_all(dir);
    }
}
