// The app manifest lists every command, so none is callable until a capability grants it
// (capabilities/default.json). Without it Tauri allows all app commands to every window.
const COMMANDS: &[&str] = &[
    "choose_document",
    "choose_folder",
    "list_folder",
    "read_document",
    "save_document",
    "save_document_as",
    "export_html",
    "open_external",
    "read_image",
    "resolve_link",
    "initial_document",
    "remember_session",
    "recent_files",
    "add_recent",
    "remove_recent",
    "clear_recent",
    "open_recent",
];

fn main() {
    tauri_build::try_build(
        tauri_build::Attributes::new().app_manifest(tauri_build::AppManifest::new().commands(COMMANDS)),
    )
    .expect("failed to run tauri-build");
}
