//! Multi-key ordering for search results.
//!
//! Comparisons never depend on the order results were discovered. A final
//! bytewise path comparison breaks any remaining tie, so repeated runs with
//! the same inputs produce the same output.

use std::cmp::Ordering;
use std::ffi::OsStr;
use std::path::Path;
use std::time::{SystemTime, UNIX_EPOCH};

use crate::cli::SortKey;
use crate::config::{Config, SortOptions};
use crate::dir_entry::DirEntry;
use crate::filesystem;

/// Order `entries` by the configured keys, then reverse the finished list.
pub fn sort_entries(entries: &mut [DirEntry], config: &Config) {
    let Some(opts) = config.sort.as_ref() else {
        return;
    };

    // Hash the path with one seed so `--sort random` does not follow walk order.
    let seed = if opts.keys.contains(&SortKey::Random) {
        opts.seed.unwrap_or_else(time_based_seed)
    } else {
        0
    };

    entries.sort_by(|left, right| compare_entries(left, right, config, opts, seed));
    if opts.reverse {
        entries.reverse();
    }
}

fn compare_entries(
    left: &DirEntry,
    right: &DirEntry,
    config: &Config,
    opts: &SortOptions,
    seed: u64,
) -> Ordering {
    if opts.dirs_first || opts.files_first {
        let by_group = group_rank(left, opts).cmp(&group_rank(right, opts));
        if by_group != Ordering::Equal {
            return by_group;
        }
    }

    for key in &opts.keys {
        let by_key = compare_key(left, right, *key, config, opts, seed);
        if by_key != Ordering::Equal {
            return by_key;
        }
    }

    // Raw walker path, not the case-folded or natural key above.
    left.path().cmp(right.path())
}

/// Directories (`--dirs-first`) or regular files (`--files-first`) are group 0.
///
/// Symlinks are never in that group: a symlink to a directory is not a
/// directory, and a symlink to a file is not a regular file.
fn group_rank(entry: &DirEntry, opts: &SortOptions) -> u8 {
    let primary = if opts.dirs_first {
        is_directory(entry)
    } else {
        is_regular_file(entry)
    };
    if primary { 0 } else { 1 }
}

fn compare_key(
    left: &DirEntry,
    right: &DirEntry,
    key: SortKey,
    config: &Config,
    opts: &SortOptions,
    seed: u64,
) -> Ordering {
    match key {
        SortKey::Name => cmp_text(
            entry_name(left),
            entry_name(right),
            opts.case_sensitive,
            opts.natural,
        ),
        SortKey::Path => cmp_text(
            displayed_path(left, config).as_os_str(),
            displayed_path(right, config).as_os_str(),
            opts.case_sensitive,
            opts.natural,
        ),
        SortKey::Extension => {
            cmp_optional_text(left.path().extension(), right.path().extension(), opts)
        }
        SortKey::Size => cmp_option(
            regular_file_size(left),
            regular_file_size(right),
            opts.missing_last,
        ),
        SortKey::Modified => cmp_option(
            timestamp(left, Stamp::Modified),
            timestamp(right, Stamp::Modified),
            opts.missing_last,
        ),
        SortKey::Created => cmp_option(
            timestamp(left, Stamp::Created),
            timestamp(right, Stamp::Created),
            opts.missing_last,
        ),
        SortKey::Accessed => cmp_option(
            timestamp(left, Stamp::Accessed),
            timestamp(right, Stamp::Accessed),
            opts.missing_last,
        ),
        SortKey::Depth => cmp_option(left.depth(), right.depth(), opts.missing_last),
        SortKey::Type => type_rank(left).cmp(&type_rank(right)),
        SortKey::NameLength => text_len(entry_name(left)).cmp(&text_len(entry_name(right))),
        SortKey::PathLength => text_len(displayed_path(left, config).as_os_str())
            .cmp(&text_len(displayed_path(right, config).as_os_str())),
        SortKey::Random => random_rank(seed, left).cmp(&random_rank(seed, right)),
    }
}

fn displayed_path<'a>(entry: &'a DirEntry, config: &Config) -> &'a Path {
    entry.stripped_path(config)
}

fn entry_name(entry: &DirEntry) -> &OsStr {
    entry
        .path()
        .file_name()
        .unwrap_or_else(|| entry.path().as_os_str())
}

fn is_directory(entry: &DirEntry) -> bool {
    entry.file_type().is_some_and(|ft| ft.is_dir())
}

fn is_regular_file(entry: &DirEntry) -> bool {
    entry.file_type().is_some_and(|ft| ft.is_file())
}

/// `directory < symlink < regular file < other/unknown`.
///
/// This order belongs to the `type` key only. `--dirs-first` and
/// `--files-first` use [`group_rank`] instead.
fn type_rank(entry: &DirEntry) -> u8 {
    match entry.file_type() {
        Some(ft) if ft.is_symlink() => 1,
        Some(ft) if ft.is_dir() => 0,
        Some(ft) if ft.is_file() => 2,
        _ => 3,
    }
}

/// Size is defined only for regular files. Directories, symlinks, and other
/// types are missing even when `metadata().len()` would return a number.
fn regular_file_size(entry: &DirEntry) -> Option<u64> {
    if is_regular_file(entry) {
        entry.metadata().map(|meta| meta.len())
    } else {
        None
    }
}

#[derive(Copy, Clone)]
enum Stamp {
    Modified,
    Created,
    Accessed,
}

fn timestamp(entry: &DirEntry, stamp: Stamp) -> Option<SystemTime> {
    let meta = entry.metadata()?;
    match stamp {
        Stamp::Modified => meta.modified().ok(),
        Stamp::Created => meta.created().ok(),
        Stamp::Accessed => meta.accessed().ok(),
    }
}

/// Unicode scalar values when the text is valid UTF-8, otherwise raw bytes.
fn text_len(text: &OsStr) -> usize {
    match text.to_str() {
        Some(text) => text.chars().count(),
        None => filesystem::osstr_to_bytes(text).len(),
    }
}

fn random_rank(seed: u64, entry: &DirEntry) -> u64 {
    let bytes = filesystem::osstr_to_bytes(entry.path().as_os_str());
    hash_bytes(seed, bytes.as_ref())
}

fn time_based_seed() -> u64 {
    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|duration| duration.as_nanos())
        .unwrap_or(0);
    let mixed = (nanos as u64) ^ ((nanos >> 64) as u64);
    // The pid keeps two processes started in the same nanosecond apart.
    let pid = u64::from(std::process::id());
    let seed = mixed ^ pid.wrapping_mul(0x9E37_79B9_7F4A_7C15);
    if seed == 0 {
        0xA076_1D64_78BD_642F
    } else {
        seed
    }
}

/// FNV-1a mixed with the seed, then a splitmix finalizer.
///
/// The same seed and path always hash to the same `u64`, independent of when
/// the entry was visited.
fn hash_bytes(seed: u64, bytes: &[u8]) -> u64 {
    let mut hash = 0xcbf2_9ce4_8422_2325 ^ seed.wrapping_mul(0x9E37_79B9_7F4A_7C15);
    for &byte in bytes {
        hash ^= u64::from(byte);
        hash = hash.wrapping_mul(0x0000_0100_0000_01b3);
    }
    let mut mixed = hash.wrapping_add(0x9E37_79B9_7F4A_7C15);
    mixed = (mixed ^ (mixed >> 30)).wrapping_mul(0xBF58_476D_1CE4_E5B9);
    mixed = (mixed ^ (mixed >> 27)).wrapping_mul(0x94D0_49BB_1331_11EB);
    mixed ^ (mixed >> 31)
}

fn cmp_option<T: Ord>(left: Option<T>, right: Option<T>, missing_last: bool) -> Ordering {
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

fn cmp_optional_text(left: Option<&OsStr>, right: Option<&OsStr>, opts: &SortOptions) -> Ordering {
    match (left, right) {
        (None, None) => Ordering::Equal,
        (None, Some(_)) => missing_ordering(opts.missing_last),
        (Some(_), None) => missing_ordering(opts.missing_last).reverse(),
        (Some(left), Some(right)) => cmp_text(left, right, opts.case_sensitive, opts.natural),
    }
}

fn cmp_text(left: &OsStr, right: &OsStr, case_sensitive: bool, natural: bool) -> Ordering {
    if case_sensitive && !natural {
        return left.cmp(right);
    }
    let left_bytes = filesystem::osstr_to_bytes(left);
    let right_bytes = filesystem::osstr_to_bytes(right);
    cmp_text_bytes(
        left_bytes.as_ref(),
        right_bytes.as_ref(),
        case_sensitive,
        natural,
    )
}

fn cmp_text_bytes(left: &[u8], right: &[u8], case_sensitive: bool, natural: bool) -> Ordering {
    if case_sensitive {
        if natural {
            cmp_natural_bytes(left, right)
        } else {
            left.cmp(right)
        }
    } else {
        let left_folded = fold_case_bytes(left);
        let right_folded = fold_case_bytes(right);
        if natural {
            cmp_natural_bytes(&left_folded, &right_folded)
        } else {
            left_folded.cmp(&right_folded)
        }
    }
}

fn fold_case_bytes(bytes: &[u8]) -> Vec<u8> {
    match std::str::from_utf8(bytes) {
        Ok(text) => text.to_lowercase().into_bytes(),
        Err(_) => bytes
            .iter()
            .copied()
            .map(|byte| byte.to_ascii_lowercase())
            .collect(),
    }
}

/// Natural order over bytes.
///
/// A maximal run of ASCII digits is one integer. Leading zeros do not change
/// that integer, so `file007` and `file7` compare equal and the caller falls
/// through to the next key. Overflow is avoided by comparing the significant
/// digits as strings: the longer significant run is the greater number.
fn cmp_natural_bytes(left: &[u8], right: &[u8]) -> Ordering {
    let mut left_index = 0;
    let mut right_index = 0;
    while left_index < left.len() && right_index < right.len() {
        let left_digit = left[left_index].is_ascii_digit();
        let right_digit = right[right_index].is_ascii_digit();
        if left_digit && right_digit {
            let (ordering, next_left, next_right) =
                cmp_digit_runs(left, left_index, right, right_index);
            if ordering != Ordering::Equal {
                return ordering;
            }
            left_index = next_left;
            right_index = next_right;
        } else {
            match left[left_index].cmp(&right[right_index]) {
                Ordering::Equal => {
                    left_index += 1;
                    right_index += 1;
                }
                ordering => return ordering,
            }
        }
    }

    match (left_index == left.len(), right_index == right.len()) {
        (true, true) => Ordering::Equal,
        (true, false) => Ordering::Less,
        (false, true) => Ordering::Greater,
        (false, false) => unreachable!("both sides still have bytes"),
    }
}

/// Compare the digit runs starting at `left_index` and `right_index`.
///
/// Returns the ordering and the indexes just past each run.
fn cmp_digit_runs(
    left: &[u8],
    left_index: usize,
    right: &[u8],
    right_index: usize,
) -> (Ordering, usize, usize) {
    let (left_end, left_digits) = significant_digits(left, left_index);
    let (right_end, right_digits) = significant_digits(right, right_index);
    let ordering = left_digits
        .len()
        .cmp(&right_digits.len())
        .then_with(|| left_digits.cmp(right_digits));
    (ordering, left_end, right_end)
}

fn significant_digits(bytes: &[u8], start: usize) -> (usize, &[u8]) {
    let mut index = start;
    while index < bytes.len() && bytes[index] == b'0' {
        index += 1;
    }
    let digits_start = index;
    while index < bytes.len() && bytes[index].is_ascii_digit() {
        index += 1;
    }
    (index, &bytes[digits_start..index])
}

#[cfg(test)]
mod tests {
    use super::*;

    fn cmp_str(left: &str, right: &str, case_sensitive: bool, natural: bool) -> Ordering {
        cmp_text_bytes(left.as_bytes(), right.as_bytes(), case_sensitive, natural)
    }

    #[test]
    fn natural_orders_embedded_numbers_and_treats_leading_zeros_as_equal() {
        assert_eq!(cmp_str("file9", "file10", true, true), Ordering::Less);
        assert_eq!(cmp_str("file10", "file20", true, true), Ordering::Less);
        assert_eq!(cmp_str("file20", "file9", true, true), Ordering::Greater);
        assert_eq!(cmp_str("file007", "file7", true, true), Ordering::Equal);
        assert_eq!(cmp_str("file007b", "file7a", true, true), Ordering::Greater);
        assert_eq!(cmp_str("file1a", "file10", true, true), Ordering::Less);
        assert_eq!(cmp_str("file", "file1", true, true), Ordering::Less);
        assert_eq!(cmp_str("0", "00", true, true), Ordering::Equal);
        assert_eq!(cmp_str("02", "2", true, true), Ordering::Equal);
        assert_eq!(cmp_str("2", "10", true, true), Ordering::Less);
        assert_eq!(
            cmp_str(
                "f0000000000000000000000000000000000000001",
                "f1",
                true,
                true
            ),
            Ordering::Equal
        );
        assert_eq!(
            cmp_str(
                "f99999999999999999999",
                "f100000000000000000000",
                true,
                true
            ),
            Ordering::Less
        );
    }

    #[test]
    fn natural_respects_case_sensitivity_outside_digit_runs() {
        assert_eq!(cmp_str("File10", "file9", false, true), Ordering::Greater);
        assert_eq!(cmp_str("File10", "file9", true, true), Ordering::Less);
        assert_eq!(cmp_str("FILE20", "File9", true, true), Ordering::Less);
        assert_eq!(cmp_str("B10", "a2", true, true), Ordering::Less);
        assert_eq!(cmp_str("B10", "a2", false, true), Ordering::Greater);
    }

    #[test]
    fn case_folding_without_natural_order() {
        assert_eq!(cmp_str("B", "b", false, false), Ordering::Equal);
        assert_eq!(cmp_str("B", "b", true, false), Ordering::Less);
        assert_eq!(cmp_str("é", "É", false, false), Ordering::Equal);
        assert_eq!(cmp_str("é", "É", true, false), Ordering::Greater);
        assert_eq!(cmp_str("b", "C", false, false), Ordering::Less);
        assert_eq!(cmp_str("b", "C", true, false), Ordering::Greater);
    }

    #[test]
    fn text_comparison_is_a_total_order() {
        let samples = [
            "", "0", "00", "1", "01", "2", "10", "02", "a", "a0", "a1", "a01", "a10", "a2", "ab",
            "A", "a1b", "a1a", "file", "file1", "file1a", "file10", "file007", "file7", "file9",
            "file20", "File9", "é", "É", "İ",
        ];
        for case_sensitive in [false, true] {
            for natural in [false, true] {
                for left in samples {
                    for right in samples {
                        let forward = cmp_str(left, right, case_sensitive, natural);
                        let backward = cmp_str(right, left, case_sensitive, natural);
                        assert_eq!(
                            forward,
                            backward.reverse(),
                            "{left:?} vs {right:?} case_sensitive={case_sensitive} natural={natural}"
                        );
                    }
                }
                for left in samples {
                    for middle in samples {
                        for right in samples {
                            let left_middle = cmp_str(left, middle, case_sensitive, natural);
                            let middle_right = cmp_str(middle, right, case_sensitive, natural);
                            let left_right = cmp_str(left, right, case_sensitive, natural);
                            if left_middle != Ordering::Greater && middle_right != Ordering::Greater
                            {
                                assert_ne!(
                                    left_right,
                                    Ordering::Greater,
                                    "{left:?} <= {middle:?} <= {right:?} but {left:?} > {right:?} \
                                     case_sensitive={case_sensitive} natural={natural}"
                                );
                            }
                        }
                    }
                }
            }
        }
    }

    #[test]
    fn random_hash_depends_on_seed_and_path_only() {
        assert_eq!(hash_bytes(1, b"a/b"), hash_bytes(1, b"a/b"));
        assert_ne!(hash_bytes(1, b"a/b"), hash_bytes(2, b"a/b"));
        assert_ne!(hash_bytes(1, b"a/b"), hash_bytes(1, b"a/c"));
        assert_ne!(hash_bytes(0, b"a"), hash_bytes(0, b"b"));
    }

    #[test]
    fn character_length_counts_scalars() {
        assert_eq!(text_len(OsStr::new("abc")), 3);
        assert_eq!(text_len(OsStr::new("éé")), 2);
        assert_eq!(text_len(OsStr::new("")), 0);
    }
}
