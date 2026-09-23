//! Deterministic multi-key sorting for search results.
//!
//! Sort keys are compared left to right. Later keys break ties from earlier
//! keys. A path comparison, then the raw path bytes, is always applied last so
//! the order does not depend on traversal order.

use std::cmp::Ordering;
use std::time::SystemTime;

use clap::ValueEnum;

use crate::dir_entry::DirEntry;
use crate::filesystem;

/// Fields accepted by `--sort`.
#[derive(Copy, Clone, PartialEq, Eq, Debug, ValueEnum)]
pub enum SortKey {
    /// Full path
    Path,
    /// File or directory name
    Name,
    /// File extension, without the leading dot
    Extension,
    /// Size in bytes; only regular files have a size
    Size,
    /// Modification time
    Modified,
    /// Creation time
    Created,
    /// Access time
    Accessed,
    /// Directory depth
    Depth,
    /// directory < symlink < regular file < other
    Type,
    /// Length of the file name
    NameLength,
    /// Length of the path
    PathLength,
    /// Pseudo-random order
    Random,
}

/// Sorting controls collected from the command line.
#[derive(Clone, Debug)]
pub struct SortOptions {
    pub keys: Vec<SortKey>,
    pub reverse: bool,
    pub dirs_first: bool,
    pub files_first: bool,
    pub case_sensitive: bool,
    pub missing_last: bool,
    pub natural: bool,
    pub seed: u64,
}

/// Seed used for `--sort random` when `--sort-seed` is not given.
pub fn time_seed() -> u64 {
    use std::time::{SystemTime, UNIX_EPOCH};

    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_nanos())
        .unwrap_or(0);
    let mixed = nanos ^ (std::process::id() as u128).wrapping_mul(0x9E37_79B9_7F4A_7C15);
    (mixed as u64) ^ ((mixed >> 64) as u64)
}

/// Sort `entries` in place.
///
/// `--reverse` reverses the finished order. Callers apply `--max-results`
/// after this returns.
pub fn sort_entries(entries: &mut Vec<DirEntry>, opts: &SortOptions) {
    if entries.is_empty() {
        return;
    }

    let prepared: Vec<Prepared> = entries.iter().map(|entry| prepare(entry, opts)).collect();
    let mut order: Vec<usize> = (0..entries.len()).collect();
    order.sort_by(|&i, &j| compare_prepared(&prepared[i], &prepared[j], opts));
    if opts.reverse {
        order.reverse();
    }

    let mut original: Vec<Option<DirEntry>> =
        std::mem::take(entries).into_iter().map(Some).collect();
    for index in order {
        entries.push(original[index].take().unwrap());
    }
}

struct Prepared {
    group: u8,
    name: String,
    path: String,
    extension: Option<String>,
    size: Option<u64>,
    modified: Option<SystemTime>,
    created: Option<SystemTime>,
    accessed: Option<SystemTime>,
    depth: Option<usize>,
    type_rank: u8,
    name_len: usize,
    path_len: usize,
    random: u64,
    raw_path: Vec<u8>,
}

fn prepare(entry: &DirEntry, opts: &SortOptions) -> Prepared {
    let path = entry.path();
    let path_raw = filesystem::osstr_to_bytes(path.as_os_str());
    let path_string = path.to_string_lossy().into_owned();

    let name_os = path.file_name().unwrap_or(path.as_os_str());
    let name_raw = filesystem::osstr_to_bytes(name_os);
    let name = name_os.to_string_lossy().into_owned();

    let file_type = entry.file_type();
    let wants_metadata = opts.keys.iter().any(|key| {
        matches!(
            key,
            SortKey::Modified | SortKey::Created | SortKey::Accessed
        ) || (*key == SortKey::Size && file_type.as_ref().is_some_and(|ft| ft.is_file()))
    });
    let metadata = if wants_metadata {
        entry.metadata()
    } else {
        None
    };

    let size = if opts.keys.contains(&SortKey::Size) {
        match file_type.as_ref() {
            Some(ft) if ft.is_file() => metadata.map(|meta| meta.len()),
            _ => None,
        }
    } else {
        None
    };

    Prepared {
        group: group_rank(file_type.as_ref(), opts),
        extension: path
            .extension()
            .map(|ext| ext.to_string_lossy().into_owned()),
        size,
        modified: metadata.and_then(|meta| meta.modified().ok()),
        created: metadata.and_then(|meta| meta.created().ok()),
        accessed: metadata.and_then(|meta| meta.accessed().ok()),
        depth: entry.depth(),
        type_rank: type_rank(file_type.as_ref()),
        name_len: name_raw.len(),
        path_len: path_raw.len(),
        random: if opts.keys.contains(&SortKey::Random) {
            random_key(opts.seed, path_raw.as_ref())
        } else {
            0
        },
        raw_path: path_raw.into_owned(),
        name,
        path: path_string,
    }
}

fn group_rank(file_type: Option<&std::fs::FileType>, opts: &SortOptions) -> u8 {
    if opts.dirs_first {
        u8::from(!file_type.is_some_and(|ft| ft.is_dir()))
    } else if opts.files_first {
        u8::from(!file_type.is_some_and(|ft| ft.is_file()))
    } else {
        0
    }
}

/// directory < symlink < regular file < other/unknown.
fn type_rank(file_type: Option<&std::fs::FileType>) -> u8 {
    match file_type {
        Some(ft) if ft.is_dir() => 0,
        Some(ft) if ft.is_symlink() => 1,
        Some(ft) if ft.is_file() => 2,
        _ => 3,
    }
}

fn compare_prepared(left: &Prepared, right: &Prepared, opts: &SortOptions) -> Ordering {
    if opts.dirs_first || opts.files_first {
        let ordering = left.group.cmp(&right.group);
        if ordering != Ordering::Equal {
            return ordering;
        }
    }

    for key in &opts.keys {
        let ordering = compare_key(left, right, *key, opts);
        if ordering != Ordering::Equal {
            return ordering;
        }
    }

    let ordering = compare_text(&left.path, &right.path, opts.case_sensitive, opts.natural);
    if ordering != Ordering::Equal {
        return ordering;
    }
    left.raw_path.cmp(&right.raw_path)
}

fn compare_key(left: &Prepared, right: &Prepared, key: SortKey, opts: &SortOptions) -> Ordering {
    match key {
        SortKey::Path => compare_text(&left.path, &right.path, opts.case_sensitive, opts.natural),
        SortKey::Name => compare_text(&left.name, &right.name, opts.case_sensitive, opts.natural),
        SortKey::Extension => cmp_text_opt(
            left.extension.as_deref(),
            right.extension.as_deref(),
            opts.case_sensitive,
            opts.natural,
            opts.missing_last,
        ),
        SortKey::Size => cmp_opt(left.size, right.size, opts.missing_last),
        SortKey::Modified => cmp_opt(left.modified, right.modified, opts.missing_last),
        SortKey::Created => cmp_opt(left.created, right.created, opts.missing_last),
        SortKey::Accessed => cmp_opt(left.accessed, right.accessed, opts.missing_last),
        SortKey::Depth => cmp_opt(left.depth, right.depth, opts.missing_last),
        SortKey::Type => left.type_rank.cmp(&right.type_rank),
        SortKey::NameLength => left.name_len.cmp(&right.name_len),
        SortKey::PathLength => left.path_len.cmp(&right.path_len),
        SortKey::Random => left.random.cmp(&right.random),
    }
}

fn cmp_opt<T: Ord>(left: Option<T>, right: Option<T>, missing_last: bool) -> Ordering {
    match (left, right) {
        (Some(left), Some(right)) => left.cmp(&right),
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

fn cmp_text_opt(
    left: Option<&str>,
    right: Option<&str>,
    case_sensitive: bool,
    natural: bool,
    missing_last: bool,
) -> Ordering {
    match (left, right) {
        (Some(left), Some(right)) => compare_text(left, right, case_sensitive, natural),
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

fn compare_text(left: &str, right: &str, case_sensitive: bool, natural: bool) -> Ordering {
    if natural {
        compare_natural(left, right, case_sensitive)
    } else if case_sensitive {
        left.cmp(right)
    } else {
        cmp_ignore_case(left, right)
    }
}

fn cmp_ignore_case(left: &str, right: &str) -> Ordering {
    let mut left = left.chars().flat_map(|ch| ch.to_lowercase());
    let mut right = right.chars().flat_map(|ch| ch.to_lowercase());
    loop {
        match (left.next(), right.next()) {
            (None, None) => return Ordering::Equal,
            (None, Some(_)) => return Ordering::Less,
            (Some(_), None) => return Ordering::Greater,
            (Some(left), Some(right)) => {
                let ordering = left.cmp(&right);
                if ordering != Ordering::Equal {
                    return ordering;
                }
            }
        }
    }
}

/// Natural order: ASCII digit runs compare as integers.
///
/// Leading zeros do not change the numeric value, so `file007` and `file7`
/// compare equal here. Callers break that tie with the raw path.
fn compare_natural(left: &str, right: &str, case_sensitive: bool) -> Ordering {
    let mut left = left.chars().peekable();
    let mut right = right.chars().peekable();
    loop {
        match (left.peek().copied(), right.peek().copied()) {
            (None, None) => return Ordering::Equal,
            (None, Some(_)) => return Ordering::Less,
            (Some(_), None) => return Ordering::Greater,
            (Some(l), Some(r)) if l.is_ascii_digit() && r.is_ascii_digit() => {
                let ordering = cmp_digit_runs(&mut left, &mut right);
                if ordering != Ordering::Equal {
                    return ordering;
                }
            }
            _ => {
                let ordering = cmp_nondigit_runs(&mut left, &mut right, case_sensitive);
                if ordering != Ordering::Equal {
                    return ordering;
                }
            }
        }
    }
}

fn cmp_digit_runs(
    left: &mut std::iter::Peekable<std::str::Chars<'_>>,
    right: &mut std::iter::Peekable<std::str::Chars<'_>>,
) -> Ordering {
    while left.peek() == Some(&'0') {
        left.next();
    }
    while right.peek() == Some(&'0') {
        right.next();
    }

    let mut left_digits = String::new();
    let mut right_digits = String::new();
    while left.peek().is_some_and(|ch| ch.is_ascii_digit()) {
        left_digits.push(left.next().unwrap());
    }
    while right.peek().is_some_and(|ch| ch.is_ascii_digit()) {
        right_digits.push(right.next().unwrap());
    }

    match left_digits.len().cmp(&right_digits.len()) {
        Ordering::Equal => left_digits.cmp(&right_digits),
        ordering => ordering,
    }
}

fn cmp_nondigit_runs(
    left: &mut std::iter::Peekable<std::str::Chars<'_>>,
    right: &mut std::iter::Peekable<std::str::Chars<'_>>,
    case_sensitive: bool,
) -> Ordering {
    let mut left_run = String::new();
    let mut right_run = String::new();
    while left.peek().is_some_and(|ch| !ch.is_ascii_digit()) {
        left_run.push(left.next().unwrap());
    }
    while right.peek().is_some_and(|ch| !ch.is_ascii_digit()) {
        right_run.push(right.next().unwrap());
    }

    if case_sensitive {
        left_run.cmp(&right_run)
    } else {
        cmp_ignore_case(&left_run, &right_run)
    }
}

fn random_key(seed: u64, bytes: &[u8]) -> u64 {
    let mut hash = seed ^ 0x517C_C1B7_2722_0A95;
    for byte in bytes {
        hash = hash
            .wrapping_mul(0x100_0000_01B3)
            .wrapping_add(u64::from(*byte));
    }
    // SplitMix64 finalizer so nearby paths do not stay in lexicographic order.
    hash = (hash ^ (hash >> 30)).wrapping_mul(0xBF58_476D_1CE4_E5B9);
    hash = (hash ^ (hash >> 27)).wrapping_mul(0x94D0_49BB_1331_11EB);
    hash ^ (hash >> 31)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn natural_orders_embedded_numbers() {
        assert_eq!(compare_natural("file9", "file10", true), Ordering::Less);
        assert_eq!(compare_natural("file10", "file20", true), Ordering::Less);
        assert_eq!(compare_natural("file20", "file9", true), Ordering::Greater);
        assert_eq!(compare_natural("file099", "file100", true), Ordering::Less);
    }

    #[test]
    fn natural_leading_zeros_are_numerically_equal() {
        assert_eq!(compare_natural("file007", "file7", true), Ordering::Equal);
        assert_eq!(compare_natural("file0", "file00", true), Ordering::Equal);
        assert_eq!(compare_natural("1.001", "1.1", true), Ordering::Equal);
        assert_eq!(compare_natural("1.002", "1.02", true), Ordering::Equal);
        assert_eq!(compare_natural("1.02", "1.3", true), Ordering::Less);
        assert_eq!(compare_natural("1.3", "1.010", true), Ordering::Less);
    }

    #[test]
    fn natural_respects_case_sensitivity() {
        assert_eq!(compare_natural("File10", "file9", false), Ordering::Greater);
        assert_eq!(compare_natural("File10", "file9", true), Ordering::Less);
        assert_eq!(compare_natural("FILE20", "File10", true), Ordering::Less);
    }

    #[test]
    fn case_folding_equates_ascii_case() {
        assert_eq!(cmp_ignore_case("Foo", "foo"), Ordering::Equal);
        assert_eq!(cmp_ignore_case("a", "B"), Ordering::Less);
        assert_eq!(compare_text("B", "a", false, false), Ordering::Greater);
        assert_eq!(compare_text("B", "a", true, false), Ordering::Less);
    }

    #[test]
    fn missing_values_sort_first_or_last() {
        assert_eq!(cmp_opt(None::<u64>, Some(1), false), Ordering::Less);
        assert_eq!(cmp_opt(Some(1), None::<u64>, false), Ordering::Greater);
        assert_eq!(cmp_opt(None::<u64>, Some(1), true), Ordering::Greater);
        assert_eq!(cmp_opt(None::<u64>, None, true), Ordering::Equal);
        assert_eq!(cmp_opt(Some(1u64), Some(2), false), Ordering::Less);
    }

    #[test]
    fn random_key_depends_on_seed_and_path() {
        let path = b"dir/file";
        assert_ne!(random_key(1, path), random_key(2, path));
        assert_ne!(random_key(1, path), random_key(1, b"dir/other"));
        assert_eq!(random_key(42, path), random_key(42, path));
    }
}
