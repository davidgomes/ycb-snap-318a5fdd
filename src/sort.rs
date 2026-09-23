use std::cmp::Ordering;
use std::time::SystemTime;

use clap::ValueEnum;

use crate::dir_entry::DirEntry;
use crate::filesystem;

/// Fields accepted by `--sort`.
#[derive(Copy, Clone, PartialEq, Eq, Debug, ValueEnum)]
pub enum SortKey {
    Path,
    Name,
    Extension,
    Size,
    Modified,
    Created,
    Accessed,
    Depth,
    Type,
    NameLength,
    PathLength,
    Random,
}

/// Sorting controls. Present only when the user passed at least one `--sort`.
#[derive(Clone)]
pub struct SortSpec {
    pub keys: Vec<SortKey>,
    pub reverse: bool,
    pub dirs_first: bool,
    pub files_first: bool,
    pub case_sensitive: bool,
    pub missing_last: bool,
    pub natural: bool,
    /// Seed for the `random` key. Unused when that key is absent.
    pub seed: u64,
}

pub fn sort_entries(entries: &mut [DirEntry], spec: &SortSpec, strip_cwd_prefix: bool) {
    entries.sort_by(|a, b| cmp_entries(a, b, spec, strip_cwd_prefix));
}

fn cmp_entries(a: &DirEntry, b: &DirEntry, spec: &SortSpec, strip_cwd_prefix: bool) -> Ordering {
    if spec.dirs_first || spec.files_first {
        let ord = group_rank(a, spec).cmp(&group_rank(b, spec));
        if ord != Ordering::Equal {
            return ord;
        }
    }

    for key in &spec.keys {
        let ord = match key {
            SortKey::Path => cmp_text_paths(
                display_bytes(a, strip_cwd_prefix).as_ref(),
                display_bytes(b, strip_cwd_prefix).as_ref(),
                spec,
            ),
            SortKey::Name => cmp_text_paths(name_bytes(a).as_ref(), name_bytes(b).as_ref(), spec),
            SortKey::Extension => cmp_optional_text(
                extension_bytes(a).as_deref(),
                extension_bytes(b).as_deref(),
                spec,
            ),
            SortKey::Size => cmp_opt(file_size(a), file_size(b), spec.missing_last),
            SortKey::Modified => cmp_opt(mtime(a), mtime(b), spec.missing_last),
            SortKey::Created => cmp_opt(btime(a), btime(b), spec.missing_last),
            SortKey::Accessed => cmp_opt(atime(a), atime(b), spec.missing_last),
            SortKey::Depth => cmp_opt(a.depth(), b.depth(), spec.missing_last),
            SortKey::Type => type_rank(a).cmp(&type_rank(b)),
            SortKey::NameLength => name_bytes(a).len().cmp(&name_bytes(b).len()),
            SortKey::PathLength => display_bytes(a, strip_cwd_prefix)
                .len()
                .cmp(&display_bytes(b, strip_cwd_prefix).len()),
            SortKey::Random => {
                hash_path(spec.seed, path_bytes(a)).cmp(&hash_path(spec.seed, path_bytes(b)))
            }
        };
        if ord != Ordering::Equal {
            return ord;
        }
    }

    // Final tie-break is the raw path so the order does not depend on traversal.
    path_bytes(a).cmp(&path_bytes(b))
}

/// Directories (or regular files) form the primary partition; every other kind
/// shares the secondary partition.
fn group_rank(entry: &DirEntry, spec: &SortSpec) -> u8 {
    let ft = entry.file_type();
    let primary = if spec.dirs_first {
        ft.is_some_and(|t| t.is_dir())
    } else {
        ft.is_some_and(|t| t.is_file())
    };
    if primary { 0 } else { 1 }
}

/// directory < symlink < regular file < other/unknown
fn type_rank(entry: &DirEntry) -> u8 {
    match entry.file_type() {
        Some(ft) if ft.is_dir() => 0,
        Some(ft) if ft.is_symlink() => 1,
        Some(ft) if ft.is_file() => 2,
        _ => 3,
    }
}

fn file_size(entry: &DirEntry) -> Option<u64> {
    let ft = entry.file_type()?;
    if ft.is_file() {
        Some(entry.metadata()?.len())
    } else {
        None
    }
}

fn mtime(entry: &DirEntry) -> Option<SystemTime> {
    entry.metadata()?.modified().ok()
}

fn atime(entry: &DirEntry) -> Option<SystemTime> {
    entry.metadata()?.accessed().ok()
}

fn btime(entry: &DirEntry) -> Option<SystemTime> {
    entry.metadata()?.created().ok()
}

fn cmp_opt<T: Ord>(a: Option<T>, b: Option<T>, missing_last: bool) -> Ordering {
    match (a, b) {
        (Some(a), Some(b)) => a.cmp(&b),
        (None, None) => Ordering::Equal,
        (None, Some(_)) => {
            if missing_last {
                Ordering::Greater
            } else {
                Ordering::Less
            }
        }
        (Some(_), None) => {
            if missing_last {
                Ordering::Less
            } else {
                Ordering::Greater
            }
        }
    }
}

fn cmp_optional_text(a: Option<&[u8]>, b: Option<&[u8]>, spec: &SortSpec) -> Ordering {
    match (a, b) {
        (Some(a), Some(b)) => cmp_text_paths(a, b, spec),
        (None, None) => Ordering::Equal,
        (None, Some(_)) => {
            if spec.missing_last {
                Ordering::Greater
            } else {
                Ordering::Less
            }
        }
        (Some(_), None) => {
            if spec.missing_last {
                Ordering::Less
            } else {
                Ordering::Greater
            }
        }
    }
}

fn cmp_text_paths(a: &[u8], b: &[u8], spec: &SortSpec) -> Ordering {
    cmp_text(a, b, spec.case_sensitive, spec.natural)
}

/// Compare text. `natural` compares ASCII digit runs numerically. When a digit
/// run is numerically equal, the shorter run (fewer leading zeros) comes first.
pub fn cmp_text(a: &[u8], b: &[u8], case_sensitive: bool, natural: bool) -> Ordering {
    if !natural {
        return if case_sensitive {
            a.cmp(b)
        } else {
            fold_case(a).cmp(&fold_case(b))
        };
    }

    let mut ia = 0;
    let mut ib = 0;
    while ia < a.len() && ib < b.len() {
        if a[ia].is_ascii_digit() && b[ib].is_ascii_digit() {
            let a_start = ia;
            let b_start = ib;
            while ia < a.len() && a[ia].is_ascii_digit() {
                ia += 1;
            }
            while ib < b.len() && b[ib].is_ascii_digit() {
                ib += 1;
            }
            let ord = cmp_digit_run(&a[a_start..ia], &b[b_start..ib]);
            if ord != Ordering::Equal {
                return ord;
            }
        } else {
            let a_start = ia;
            let b_start = ib;
            while ia < a.len() && !a[ia].is_ascii_digit() {
                ia += 1;
            }
            while ib < b.len() && !b[ib].is_ascii_digit() {
                ib += 1;
            }
            let ord = if case_sensitive {
                a[a_start..ia].cmp(&b[b_start..ib])
            } else {
                fold_case(&a[a_start..ia]).cmp(&fold_case(&b[b_start..ib]))
            };
            if ord != Ordering::Equal {
                return ord;
            }
        }
    }

    ia.cmp(&ib).then(a.len().cmp(&b.len()))
}

fn cmp_digit_run(a: &[u8], b: &[u8]) -> Ordering {
    let sa = strip_zeros(a);
    let sb = strip_zeros(b);
    sa.len()
        .cmp(&sb.len())
        .then_with(|| sa.cmp(sb))
        .then_with(|| a.len().cmp(&b.len()))
}

fn strip_zeros(run: &[u8]) -> &[u8] {
    let i = run.iter().position(|&c| c != b'0').unwrap_or(run.len());
    &run[i..]
}

fn fold_case(bytes: &[u8]) -> Vec<u8> {
    match std::str::from_utf8(bytes) {
        Ok(s) => s.to_lowercase().into_bytes(),
        Err(_) => bytes.iter().map(u8::to_ascii_lowercase).collect(),
    }
}

fn path_bytes(entry: &DirEntry) -> std::borrow::Cow<'_, [u8]> {
    filesystem::osstr_to_bytes(entry.path().as_os_str())
}

fn display_bytes(entry: &DirEntry, strip_cwd_prefix: bool) -> std::borrow::Cow<'_, [u8]> {
    let path = if strip_cwd_prefix {
        filesystem::strip_current_dir(entry.path())
    } else {
        entry.path()
    };
    filesystem::osstr_to_bytes(path.as_os_str())
}

fn name_bytes(entry: &DirEntry) -> std::borrow::Cow<'_, [u8]> {
    let name = entry
        .path()
        .file_name()
        .unwrap_or_else(|| entry.path().as_os_str());
    filesystem::osstr_to_bytes(name)
}

fn extension_bytes(entry: &DirEntry) -> Option<std::borrow::Cow<'_, [u8]>> {
    entry
        .path()
        .extension()
        .map(|ext| filesystem::osstr_to_bytes(ext))
}

/// FNV-1a 64 mixed with the seed. Stable for a given seed and path.
fn hash_path(seed: u64, bytes: std::borrow::Cow<'_, [u8]>) -> u64 {
    let mut h = seed ^ 0xcbf29ce484222325;
    for &b in bytes.as_ref() {
        h ^= b as u64;
        h = h.wrapping_mul(0x100000001b3);
    }
    // splitmix64 finalizer
    let mut z = h.wrapping_add(0x9e3779b97f4a7c15);
    z = (z ^ (z >> 30)).wrapping_mul(0xbf58476d1ce4e5b9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94d049bb133111eb);
    z ^ (z >> 31)
}

#[cfg(test)]
mod tests {
    use super::cmp_text;
    use std::cmp::Ordering::*;

    fn n(a: &str, b: &str) -> std::cmp::Ordering {
        cmp_text(a.as_bytes(), b.as_bytes(), false, true)
    }

    #[test]
    fn natural_numeric_runs() {
        assert_eq!(n("file9", "file10"), Less);
        assert_eq!(n("file10", "file20"), Less);
        assert_eq!(n("file20", "file9"), Greater);
    }

    #[test]
    fn natural_leading_zeros() {
        assert_eq!(n("file7", "file007"), Less);
        assert_eq!(n("file007", "file7"), Greater);
        assert_eq!(n("file007", "file007"), Equal);
    }

    #[test]
    fn natural_case_fold() {
        assert_eq!(n("File10", "file9"), Greater);
        assert_eq!(cmp_text(b"File10", b"file9", true, true), Less);
    }
}
