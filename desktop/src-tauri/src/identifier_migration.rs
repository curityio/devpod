//! DevPod Desktop's identifier used to be upstream's `sh.loft.devpod`. Tauri keeps the app's data in
//! directories named after the identifier: the settings and provider stores, action logs and the
//! Windows CLI shims (in the app data dir), logs, and on Windows and Linux the webview's local storage
//! (in the app local data dir; on macOS it's in `~/Library/WebKit/<identifier>`).
//!
//! On the first start with the new identifier, the old directories are copied to the new ones. They
//! are copied, not moved, because the user's `PATH` on Windows can point at the old CLI shims, and so
//! that an older DevPod Desktop still finds its data.
//!
//! This has to run before Tauri opens the stores or creates the webview.

use std::{
    fs, io,
    path::{Path, PathBuf},
};

const OLD_IDENTIFIER_PREFIX: &str = "sh.loft.devpod";
const NEW_IDENTIFIER_PREFIX: &str = "io.curity.devpod";

// Webview caches, which can be large and are rebuilt when missing.
const SKIPPED_DIR_NAMES: [&str; 7] = [
    "Cache",
    "Code Cache",
    "GPUCache",
    "GrShaderCache",
    "GraphiteDawnCache",
    "ShaderCache",
    "DawnCache",
];

/// Copies the app's directories from the old identifier to `identifier`, and returns what was done
/// (or failed) so that it can be logged once logging is set up.
pub fn migrate_app_dirs(identifier: &str) -> Vec<Result<String, String>> {
    let Some(suffix) = identifier.strip_prefix(NEW_IDENTIFIER_PREFIX) else {
        return vec![];
    };
    let old_identifier = format!("{}{}", OLD_IDENTIFIER_PREFIX, suffix);

    #[allow(unused_mut)]
    let mut bases: Vec<PathBuf> = [dirs::data_dir(), dirs::data_local_dir()]
        .into_iter()
        .flatten()
        .collect();
    #[cfg(target_os = "macos")]
    if let Some(home) = dirs::home_dir() {
        bases.push(home.join("Library/Logs"));
        bases.push(home.join("Library/WebKit"));
    }
    // On macOS and Linux, the data and local data dirs are the same.
    bases.sort();
    bases.dedup();

    bases
        .iter()
        .filter_map(|base| migrate_dir(&base.join(&old_identifier), &base.join(identifier)))
        .collect()
}

fn migrate_dir(old: &Path, new: &Path) -> Option<Result<String, String>> {
    if !old.is_dir() || new.exists() {
        return None;
    }

    // Copy to a temporary directory first, so that an interrupted copy is retried on the next start.
    let mut tmp_name = new.file_name()?.to_os_string();
    tmp_name.push(".migrating");
    let tmp = new.with_file_name(tmp_name);
    if tmp.exists() {
        let _ = fs::remove_dir_all(&tmp);
    }

    let result = copy_dir(old, &tmp).and_then(|_| fs::rename(&tmp, new));
    Some(match result {
        Ok(()) => Ok(format!("Copied {} to {}", old.display(), new.display())),
        Err(err) => {
            let _ = fs::remove_dir_all(&tmp);
            Err(format!(
                "Failed to copy {} to {}: {}",
                old.display(),
                new.display(),
                err
            ))
        }
    })
}

fn copy_dir(from: &Path, to: &Path) -> io::Result<()> {
    fs::create_dir_all(to)?;
    for entry in fs::read_dir(from)? {
        let entry = entry?;
        let file_type = entry.file_type()?;
        let target = to.join(entry.file_name());
        if file_type.is_dir() {
            if SKIPPED_DIR_NAMES
                .iter()
                .any(|name| entry.file_name() == *name)
            {
                continue;
            }
            copy_dir(&entry.path(), &target)?;
        } else if file_type.is_file() {
            fs::copy(entry.path(), &target)?;
        } else if file_type.is_symlink() {
            #[cfg(unix)]
            std::os::unix::fs::symlink(fs::read_link(entry.path())?, &target)?;
        }
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn temp_dir(name: &str) -> PathBuf {
        let dir = std::env::temp_dir().join(format!(
            "devpod-identifier-migration-{}-{}",
            name,
            std::process::id()
        ));
        let _ = fs::remove_dir_all(&dir);
        fs::create_dir_all(&dir).unwrap();
        dir
    }

    #[test]
    fn copies_old_dir_to_new_dir() {
        let base = temp_dir("copies");
        let old = base.join("sh.loft.devpod");
        let new = base.join("io.curity.devpod");
        fs::create_dir_all(old.join("bin")).unwrap();
        fs::create_dir_all(old.join("EBWebView/Default/Cache")).unwrap();
        fs::create_dir_all(old.join("EBWebView/Default/Local Storage")).unwrap();
        fs::write(old.join(".settings.json"), "{\"a\":1}").unwrap();
        fs::write(old.join("bin/devpod.cmd"), "cmd").unwrap();
        fs::write(old.join("EBWebView/Default/Cache/data"), "cache").unwrap();
        fs::write(old.join("EBWebView/Default/Local Storage/ls"), "ls").unwrap();

        let result = migrate_dir(&old, &new);

        assert!(matches!(result, Some(Ok(_))), "{:?}", result);
        assert_eq!(
            fs::read_to_string(new.join(".settings.json")).unwrap(),
            "{\"a\":1}"
        );
        assert_eq!(
            fs::read_to_string(new.join("bin/devpod.cmd")).unwrap(),
            "cmd"
        );
        assert_eq!(
            fs::read_to_string(new.join("EBWebView/Default/Local Storage/ls")).unwrap(),
            "ls"
        );
        assert!(!new.join("EBWebView/Default/Cache").exists());
        assert!(old.join(".settings.json").exists(), "the old dir is kept");
        assert!(!base.join("io.curity.devpod.migrating").exists());
        fs::remove_dir_all(&base).unwrap();
    }

    #[test]
    fn keeps_existing_new_dir() {
        let base = temp_dir("keeps");
        let old = base.join("sh.loft.devpod");
        let new = base.join("io.curity.devpod");
        fs::create_dir_all(&old).unwrap();
        fs::create_dir_all(&new).unwrap();
        fs::write(old.join(".settings.json"), "old").unwrap();
        fs::write(new.join(".settings.json"), "new").unwrap();

        assert!(migrate_dir(&old, &new).is_none());
        assert_eq!(
            fs::read_to_string(new.join(".settings.json")).unwrap(),
            "new"
        );
        fs::remove_dir_all(&base).unwrap();
    }

    #[test]
    fn does_nothing_without_old_dir() {
        let base = temp_dir("nothing");
        let new = base.join("io.curity.devpod");
        assert!(migrate_dir(&base.join("sh.loft.devpod"), &new).is_none());
        assert!(!new.exists());
        fs::remove_dir_all(&base).unwrap();
    }

    #[test]
    fn retries_after_interrupted_copy() {
        let base = temp_dir("retries");
        let old = base.join("sh.loft.devpod");
        let new = base.join("io.curity.devpod");
        fs::create_dir_all(&old).unwrap();
        fs::write(old.join(".settings.json"), "old").unwrap();
        fs::create_dir_all(base.join("io.curity.devpod.migrating")).unwrap();
        fs::write(base.join("io.curity.devpod.migrating/partial"), "x").unwrap();

        assert!(matches!(migrate_dir(&old, &new), Some(Ok(_))));
        assert!(!new.join("partial").exists());
        assert_eq!(
            fs::read_to_string(new.join(".settings.json")).unwrap(),
            "old"
        );
        fs::remove_dir_all(&base).unwrap();
    }

    #[test]
    fn ignores_other_identifiers() {
        assert!(migrate_app_dirs("com.example.app").is_empty());
    }
}
