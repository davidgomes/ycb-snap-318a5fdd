//! Deterministic multi-key sorting for search results.
//!
//! Keys are applied left to right. A raw path comparison is the final tie-break
//! so the order does not depend on which thread discovered an entry. `--reverse`
//! is applied to that finished sequence, not to each key on its own.

use std::cmp::Ordering;
use std::ffi::OsStr;
use std::fs::Metadata;
use std::time::{SystemTime, UNIX_EPOCH};

use crate::cli::SortKey;
use crate::dir_entry::DirEntry;

/// How to order matches when `--sort` is present.
#[derive(Clone, Debug)]
pub struct SortConfig {
    pub keys: Vec<SortKey>,
    pub reverse: bool,
    pub dirs_first: bool,
    pub files_first: bool,
    pub case_sensitive: bool,
    pub missing_last: bool,
    pub natural: bool,
    pub seed: u64,
}

/// Seed derived from the current time, used for `--sort random` without `--sort-seed`.
pub fn time_seed() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|duration| {
            duration.as_secs().wrapping_mul(1_000_000_000) ^ (duration.subsec_nanos() as u64)
        })
        .unwrap_or(0x9E37_79B9_7F4A_7C15)
}

const RANK_DIR: u8 = 0;
const RANK_SYMLINK: u8 = 1;
const RANK_FILE: u8 = 2;
const RANK_OTHER: u8 = 3;

struct SortData {
    name: Option<std::ffi::OsString>,
    extension: Option<std::ffi::OsString>,
    path: std::ffi::OsString,
    size: Option<u64>,
    modified: Option<SystemTime>,
    created: Option<SystemTime>,
    accessed: Option<SystemTime>,
    depth: Option<usize>,
    type_rank: u8,
    name_len: Option<usize>,
    path_len: usize,
    random: u64,
    /// `0` for the primary `--dirs-first` / `--files-first` partition.
    group: u8,
}

/// Sort `entries` in place. Reverses the finished order when requested.
pub fn sort_entries(entries: &mut Vec<DirEntry>, cfg: &SortConfig) {
    let mut items: Vec<(DirEntry, SortData)> = entries
        .drain(..)
        .map(|entry| {
            let data = SortData::from_entry(&entry, cfg);
            (entry, data)
        })
        .collect();

    items.sort_by(|left, right| compare_items(left, right, cfg));

    if cfg.reverse {
        items.reverse();
    }

    entries.extend(items.into_iter().map(|(entry, _)| entry));
}

fn compare_items(
    left: &(DirEntry, SortData),
    right: &(DirEntry, SortData),
    cfg: &SortConfig,
) -> Ordering {
    if cfg.dirs_first || cfg.files_first {
        match left.1.group.cmp(&right.1.group) {
            Ordering::Equal => {}
            ordering => return ordering,
        }
    }

    for key in &cfg.keys {
        match compare_key(&left.1, &right.1, *key, cfg) {
            Ordering::Equal => {}
            ordering => return ordering,
        }
    }

    // Always case-sensitive and independent of natural order, so ties stay stable
    // across runs even when the user keys fold case or ignore leading zeros.
    left.0.path().cmp(right.0.path())
}

fn compare_key(left: &SortData, right: &SortData, key: SortKey, cfg: &SortConfig) -> Ordering {
    match key {
        SortKey::Path => cmp_text(&left.path, &right.path, cfg),
        SortKey::Name => cmp_optional_text(left.name.as_deref(), right.name.as_deref(), cfg),
        SortKey::Extension => {
            cmp_optional_text(left.extension.as_deref(), right.extension.as_deref(), cfg)
        }
        SortKey::Size => cmp_opt(left.size, right.size, cfg.missing_last),
        SortKey::Modified => cmp_opt(left.modified, right.modified, cfg.missing_last),
        SortKey::Created => cmp_opt(left.created, right.created, cfg.missing_last),
        SortKey::Accessed => cmp_opt(left.accessed, right.accessed, cfg.missing_last),
        SortKey::Depth => cmp_opt(left.depth, right.depth, cfg.missing_last),
        SortKey::Type => left.type_rank.cmp(&right.type_rank),
        SortKey::NameLength => cmp_opt(left.name_len, right.name_len, cfg.missing_last),
        SortKey::PathLength => left.path_len.cmp(&right.path_len),
        SortKey::Random => left.random.cmp(&right.random),
    }
}

fn cmp_optional_text(left: Option<&OsStr>, right: Option<&OsStr>, cfg: &SortConfig) -> Ordering {
    match (left, right) {
        (Some(left), Some(right)) => cmp_text(left, right, cfg),
        (None, None) => Ordering::Equal,
        (None, Some(_)) => missing_ordering(cfg.missing_last),
        (Some(_), None) => missing_ordering(cfg.missing_last).reverse(),
    }
}

fn cmp_opt<T: Ord>(left: Option<T>, right: Option<T>, missing_last: bool) -> Ordering {
    match (left, right) {
        (Some(left), Some(right)) => left.cmp(&right),
        (None, None) => Ordering::Equal,
        (None, Some(_)) => missing_ordering(missing_last),
        (Some(_), None) => missing_ordering(missing_last).reverse(),
    }
}

fn missing_ordering(missing_last: bool) -> Ordering {
    if missing_last {
        Ordering::Greater
    } else {
        Ordering::Less
    }
}

fn cmp_text(left: &OsStr, right: &OsStr, cfg: &SortConfig) -> Ordering {
    if cfg.natural {
        natural_cmp(
            &left.to_string_lossy(),
            &right.to_string_lossy(),
            cfg.case_sensitive,
        )
    } else if cfg.case_sensitive {
        left.cmp(right)
    } else {
        cmp_ignore_case(&left.to_string_lossy(), &right.to_string_lossy())
    }
}

impl SortData {
    fn from_entry(entry: &DirEntry, cfg: &SortConfig) -> Self {
        let path = entry.path();
        let path_os = path.as_os_str();
        let name = path.file_name().map(|name| name.to_os_string());
        let extension = path.extension().map(|extension| extension.to_os_string());
        let stat = if needs_stat(cfg) {
            Some(stat_entry(entry))
        } else {
            None
        };

        let (type_rank, size, modified, created, accessed, is_dir, is_file) = match stat {
            Some(stat) => (
                stat.type_rank,
                stat.size,
                stat.modified,
                stat.created,
                stat.accessed,
                stat.is_dir,
                stat.is_file,
            ),
            None => (RANK_OTHER, None, None, None, None, false, false),
        };
        let group = if cfg.dirs_first {
            if is_dir { 0 } else { 1 }
        } else if cfg.files_first {
            if is_file { 0 } else { 1 }
        } else {
            1
        };

        let random = if cfg.keys.contains(&SortKey::Random) {
            random_key(cfg.seed, path_os.as_encoded_bytes())
        } else {
            0
        };

        Self {
            name_len: name.as_deref().map(os_len),
            name,
            extension,
            path_len: os_len(path_os),
            path: path_os.to_os_string(),
            size,
            modified,
            created,
            accessed,
            depth: entry.depth(),
            type_rank,
            random,
            group,
        }
    }
}

fn needs_stat(cfg: &SortConfig) -> bool {
    cfg.dirs_first
        || cfg.files_first
        || cfg.keys.iter().any(|key| {
            matches!(
                key,
                SortKey::Size
                    | SortKey::Modified
                    | SortKey::Created
                    | SortKey::Accessed
                    | SortKey::Type
            )
        })
}

struct EntryStat {
    type_rank: u8,
    size: Option<u64>,
    modified: Option<SystemTime>,
    created: Option<SystemTime>,
    accessed: Option<SystemTime>,
    is_dir: bool,
    is_file: bool,
}

fn stat_entry(entry: &DirEntry) -> EntryStat {
    // `symlink_metadata` does not follow links, so a symlink is never classified
    // as the file or directory it points at.
    let metadata = entry
        .path()
        .symlink_metadata()
        .ok()
        .or_else(|| entry.metadata().cloned());

    let (type_rank, is_dir, is_file) = match metadata.as_ref().map(Metadata::file_type) {
        Some(file_type) if file_type.is_symlink() => (RANK_SYMLINK, false, false),
        Some(file_type) if file_type.is_dir() => (RANK_DIR, true, false),
        Some(file_type) if file_type.is_file() => (RANK_FILE, false, true),
        _ => (RANK_OTHER, false, false),
    };

    // Size is defined only for regular files. Directories, symlinks, and other
    // types are missing even when the operating system reports a byte size.
    let size = if is_file {
        metadata.as_ref().map(Metadata::len)
    } else {
        None
    };

    EntryStat {
        type_rank,
        size,
        modified: metadata.as_ref().and_then(|meta| meta.modified().ok()),
        created: metadata.as_ref().and_then(|meta| meta.created().ok()),
        accessed: metadata.as_ref().and_then(|meta| meta.accessed().ok()),
        is_dir,
        is_file,
    }
}

fn os_len(value: &OsStr) -> usize {
    value.as_encoded_bytes().len()
}

/// Hash of the path mixed with the seed. Equal paths always share a key, and the
/// key does not depend on discovery order.
///
/// The seed is mixed before and after the path bytes. Mixing it only as the
/// initial state of a multiplicative hash keeps the relative order of names
/// that differ by one trailing byte, which makes `--sort-seed` look ignored.
fn random_key(seed: u64, path: &[u8]) -> u64 {
    let mut hash = seed ^ path.len() as u64 ^ 0xA076_1D64_78BD_642F;
    for (index, byte) in path.iter().enumerate() {
        hash ^= u64::from(*byte).wrapping_shl((index % 8) as u32);
        hash = hash.wrapping_mul(0x9E37_79B1_85EB_CA87);
        hash = hash.rotate_left(13) ^ seed.rotate_left((index % 63) as u32);
    }
    hash ^= hash >> 33;
    hash = hash.wrapping_mul(0xBF58_476D_1CE4_E5B9);
    hash ^= hash >> 29;
    hash
}

/// Natural order for text fields.
///
/// ASCII digit runs are compared by numeric value. When the values are equal,
/// the shorter run (fewer leading zeros) sorts first, but only after the rest
/// of the string has been compared — a later non-digit difference wins over
/// zero-padding. Non-digit runs use ordinary text order, folded unless
/// `case_sensitive` is set.
fn natural_cmp(left: &str, right: &str, case_sensitive: bool) -> Ordering {
    let mut pending = Ordering::Equal;
    let mut left_rest = left;
    let mut right_rest = right;

    loop {
        if left_rest.is_empty() && right_rest.is_empty() {
            return pending;
        }
        if left_rest.is_empty() {
            return Ordering::Less;
        }
        if right_rest.is_empty() {
            return Ordering::Greater;
        }

        let left_digit = left_rest.as_bytes()[0].is_ascii_digit();
        let right_digit = right_rest.as_bytes()[0].is_ascii_digit();

        if left_digit && right_digit {
            let (left_digits, left_len) = take_digits(left_rest);
            let (right_digits, right_len) = take_digits(right_rest);
            left_rest = &left_rest[left_len..];
            right_rest = &right_rest[right_len..];
            match cmp_numeric(left_digits, right_digits) {
                Ordering::Equal => {
                    if pending == Ordering::Equal {
                        pending = left_digits.len().cmp(&right_digits.len());
                    }
                }
                ordering => return ordering,
            }
        } else {
            let (left_text, left_len) = take_text(left_rest);
            let (right_text, right_len) = take_text(right_rest);
            left_rest = &left_rest[left_len..];
            right_rest = &right_rest[right_len..];
            let ordering = if case_sensitive {
                left_text.cmp(right_text)
            } else {
                cmp_ignore_case(left_text, right_text)
            };
            if ordering != Ordering::Equal {
                return ordering;
            }
        }
    }
}

fn take_digits(value: &str) -> (&str, usize) {
    let end = value
        .bytes()
        .position(|byte| !byte.is_ascii_digit())
        .unwrap_or(value.len());
    (&value[..end], end)
}

fn take_text(value: &str) -> (&str, usize) {
    let mut end = 0;
    for (index, ch) in value.char_indices() {
        if ch.is_ascii_digit() {
            break;
        }
        end = index + ch.len_utf8();
    }
    (&value[..end], end)
}

fn cmp_numeric(left: &str, right: &str) -> Ordering {
    let left_sig = left.trim_start_matches('0');
    let right_sig = right.trim_start_matches('0');
    match left_sig.len().cmp(&right_sig.len()) {
        Ordering::Equal => left_sig.cmp(right_sig),
        ordering => ordering,
    }
}

fn cmp_ignore_case(left: &str, right: &str) -> Ordering {
    let mut left_chars = left.chars().flat_map(|ch| ch.to_lowercase());
    let mut right_chars = right.chars().flat_map(|ch| ch.to_lowercase());
    loop {
        match (left_chars.next(), right_chars.next()) {
            (None, None) => return Ordering::Equal,
            (None, Some(_)) => return Ordering::Less,
            (Some(_), None) => return Ordering::Greater,
            (Some(left_ch), Some(right_ch)) => {
                if left_ch != right_ch {
                    return left_ch.cmp(&right_ch);
                }
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::natural_cmp;
    use std::cmp::Ordering;

    fn nat(left: &str, right: &str) -> Ordering {
        natural_cmp(left, right, false)
    }

    fn nat_cs(left: &str, right: &str) -> Ordering {
        natural_cmp(left, right, true)
    }

    #[test]
    fn natural_orders_digit_runs_numerically() {
        assert_eq!(nat("file9", "file10"), Ordering::Less);
        assert_eq!(nat("file10", "file20"), Ordering::Less);
        assert_eq!(nat("file20", "file9"), Ordering::Greater);
    }

    #[test]
    fn natural_leading_zeros_sort_after_the_shorter_run() {
        assert_eq!(nat("file7", "file007"), Ordering::Less);
        assert_eq!(nat("file007", "file0007"), Ordering::Less);
        assert_eq!(nat("file007a", "file7b"), Ordering::Less);
        assert_eq!(nat("file7a", "file007b"), Ordering::Less);
        assert_eq!(nat("0", "00"), Ordering::Less);
    }

    #[test]
    fn random_key_depends_on_seed_and_path() {
        assert_ne!(
            super::random_key(1, b"./sort_r0"),
            super::random_key(99, b"./sort_r0")
        );
        let mut order_a: Vec<_> = (0..6)
            .map(|index| super::random_key(42, format!("./sort_r{index}").as_bytes()))
            .collect();
        let mut order_b: Vec<_> = (0..6)
            .map(|index| super::random_key(99, format!("./sort_r{index}").as_bytes()))
            .collect();
        order_a.sort_unstable();
        order_b.sort_unstable();
        assert_ne!(order_a, order_b);
        let rank = |seed: u64| {
            let mut items: Vec<_> = (0..6)
                .map(|index| {
                    (
                        super::random_key(seed, format!("./sort_r{index}").as_bytes()),
                        index,
                    )
                })
                .collect();
            items.sort_unstable();
            items
                .into_iter()
                .map(|(_, index)| index)
                .collect::<Vec<_>>()
        };
        let ranks: Vec<_> = [1u64, 2, 42, 99, 1000].map(rank).to_vec();
        for index in 0..ranks.len() {
            for other in (index + 1)..ranks.len() {
                assert_ne!(ranks[index], ranks[other], "seeds collided: {ranks:?}");
            }
        }
    }

    #[test]
    fn natural_respects_case_sensitivity() {
        assert_eq!(nat("file9", "File10"), Ordering::Less);
        assert_eq!(nat_cs("File10", "file9"), Ordering::Less);
        assert_eq!(nat("File", "file"), Ordering::Equal);
        assert_eq!(nat_cs("File", "file"), Ordering::Less);
    }
}
