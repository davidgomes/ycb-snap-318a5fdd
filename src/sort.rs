use std::cmp::Ordering;
use std::path::{Path, PathBuf};
use std::time::SystemTime;

use crate::cli::SortField;
use crate::config::Config;
use crate::dir_entry::DirEntry;
use crate::filesystem::osstr_to_bytes;

/// Entries in the first partition are listed before all other entries.
#[derive(Copy, Clone, PartialEq, Eq, Debug)]
pub enum Grouping {
    DirsFirst,
    FilesFirst,
}

/// Configuration for sorting search results.
pub struct SortConfig {
    pub keys: Vec<SortField>,
    pub reverse: bool,
    pub grouping: Option<Grouping>,
    pub case_sensitive: bool,
    pub missing_last: bool,
    pub natural: bool,
    pub seed: u64,
}

enum Value {
    /// Compared using the record's path components.
    Path,
    Text(Option<String>),
    Number(Option<u64>),
    Time(Option<SystemTime>),
}

/// Sort keys, computed once per entry.
struct Record {
    group: u8,
    values: Vec<Value>,
    path: Vec<String>,
    raw_path: Vec<u8>,
}

/// Sort `entries` according to `sort`. The result does not depend on the order of the input.
pub fn sort_entries(
    entries: Vec<DirEntry>,
    sort: &SortConfig,
    config: &Config,
    roots: &[PathBuf],
) -> Vec<DirEntry> {
    let mut records: Vec<(Record, DirEntry)> = entries
        .into_iter()
        .map(|entry| (Record::new(&entry, sort, config, roots), entry))
        .collect();

    records.sort_by(|(a, _), (b, _)| compare(a, b, sort));

    if sort.reverse {
        records.reverse();
    }

    records.into_iter().map(|(_, entry)| entry).collect()
}

impl Record {
    fn new(entry: &DirEntry, sort: &SortConfig, config: &Config, roots: &[PathBuf]) -> Self {
        let file_type = entry.file_type();
        let is_dir = file_type.is_some_and(|ft| ft.is_dir());
        let is_file = file_type.is_some_and(|ft| ft.is_file());

        let group = match sort.grouping {
            Some(Grouping::DirsFirst) => u8::from(!is_dir),
            Some(Grouping::FilesFirst) => u8::from(!is_file),
            None => 0,
        };

        let display_path = entry.stripped_path(config);
        let name = entry.path().file_name().map(|n| n.to_string_lossy());
        let text = |s: &str| {
            if sort.case_sensitive {
                s.to_owned()
            } else {
                s.to_lowercase()
            }
        };
        let time = |f: fn(&std::fs::Metadata) -> std::io::Result<SystemTime>| {
            Value::Time(entry.metadata().and_then(|m| f(m).ok()))
        };

        let values = sort
            .keys
            .iter()
            .map(|key| match key {
                SortField::Path => Value::Path,
                SortField::Name => Value::Text(name.as_deref().map(text)),
                SortField::Extension => Value::Text(
                    name.as_deref()
                        .and_then(|n| Path::new(n).extension())
                        .filter(|ext| !ext.is_empty())
                        .map(|ext| text(&ext.to_string_lossy())),
                ),
                SortField::Size => Value::Number(
                    entry
                        .metadata()
                        .filter(|_| is_file)
                        .map(|metadata| metadata.len()),
                ),
                SortField::Modified => time(std::fs::Metadata::modified),
                SortField::Created => time(std::fs::Metadata::created),
                SortField::Accessed => time(std::fs::Metadata::accessed),
                SortField::Depth => Value::Number(
                    entry
                        .depth()
                        .or_else(|| depth_below_roots(entry.path(), roots))
                        .map(|d| d as u64),
                ),
                SortField::Type => Value::Number(Some(match file_type {
                    Some(ft) if ft.is_dir() => 0,
                    Some(ft) if ft.is_symlink() => 1,
                    Some(ft) if ft.is_file() => 2,
                    _ => 3,
                })),
                SortField::NameLength => Value::Number(Some(
                    name.as_deref().map_or(0, |n| n.chars().count() as u64),
                )),
                SortField::PathLength => {
                    Value::Number(Some(display_path.to_string_lossy().chars().count() as u64))
                }
                SortField::Random => Value::Number(Some(random_key(
                    sort.seed,
                    &osstr_to_bytes(entry.path().as_os_str()),
                ))),
            })
            .collect();

        Self {
            group,
            values,
            path: display_path
                .components()
                .map(|c| text(&c.as_os_str().to_string_lossy()))
                .collect(),
            raw_path: osstr_to_bytes(entry.path().as_os_str()).into_owned(),
        }
    }
}

fn compare(a: &Record, b: &Record, sort: &SortConfig) -> Ordering {
    a.group
        .cmp(&b.group)
        .then_with(|| {
            a.values
                .iter()
                .zip(&b.values)
                .map(|(x, y)| compare_values(x, y, a, b, sort))
                .find(|ord| ord.is_ne())
                .unwrap_or(Ordering::Equal)
        })
        .then_with(|| compare_paths(&a.path, &b.path, sort.natural))
        .then_with(|| a.raw_path.cmp(&b.raw_path))
}

fn compare_values(x: &Value, y: &Value, a: &Record, b: &Record, sort: &SortConfig) -> Ordering {
    let missing_last = sort.missing_last;
    match (x, y) {
        (Value::Path, Value::Path) => compare_paths(&a.path, &b.path, sort.natural),
        (Value::Text(x), Value::Text(y)) => {
            compare_optional(x, y, missing_last, |x, y| compare_text(x, y, sort.natural))
        }
        (Value::Number(x), Value::Number(y)) => compare_optional(x, y, missing_last, Ord::cmp),
        (Value::Time(x), Value::Time(y)) => compare_optional(x, y, missing_last, Ord::cmp),
        _ => unreachable!("sort values at the same position always have the same kind"),
    }
}

fn compare_optional<T>(
    x: &Option<T>,
    y: &Option<T>,
    missing_last: bool,
    cmp: impl FnOnce(&T, &T) -> Ordering,
) -> Ordering {
    let missing = if missing_last {
        Ordering::Greater
    } else {
        Ordering::Less
    };
    match (x, y) {
        (Some(x), Some(y)) => cmp(x, y),
        (None, None) => Ordering::Equal,
        (None, Some(_)) => missing,
        (Some(_), None) => missing.reverse(),
    }
}

fn compare_paths(a: &[String], b: &[String], natural: bool) -> Ordering {
    a.iter()
        .zip(b)
        .map(|(x, y)| compare_text(x, y, natural))
        .find(|ord| ord.is_ne())
        .unwrap_or_else(|| a.len().cmp(&b.len()))
}

fn compare_text(a: &str, b: &str, natural: bool) -> Ordering {
    if natural { natural_cmp(a, b) } else { a.cmp(b) }
}

/// Compare strings, treating runs of ASCII digits as numbers. Runs with the same numeric
/// value (e.g. `007` and `7`) compare equal.
fn natural_cmp(a: &str, b: &str) -> Ordering {
    // Byte-wise comparison of UTF-8 agrees with code point order, and ASCII digits never
    // occur inside multi-byte sequences.
    let (a, b) = (a.as_bytes(), b.as_bytes());
    let (mut i, mut j) = (0, 0);
    while i < a.len() && j < b.len() {
        if a[i].is_ascii_digit() && b[j].is_ascii_digit() {
            let start_a = i;
            while i < a.len() && a[i].is_ascii_digit() {
                i += 1;
            }
            let start_b = j;
            while j < b.len() && b[j].is_ascii_digit() {
                j += 1;
            }
            let num_a = trim_leading_zeros(&a[start_a..i]);
            let num_b = trim_leading_zeros(&b[start_b..j]);
            let ord = num_a.len().cmp(&num_b.len()).then_with(|| num_a.cmp(num_b));
            if ord.is_ne() {
                return ord;
            }
        } else {
            let ord = a[i].cmp(&b[j]);
            if ord.is_ne() {
                return ord;
            }
            i += 1;
            j += 1;
        }
    }
    (a.len() - i).cmp(&(b.len() - j))
}

fn trim_leading_zeros(digits: &[u8]) -> &[u8] {
    let zeros = digits.iter().take_while(|&&d| d == b'0').count();
    &digits[zeros..]
}

/// Depth of `path` below the closest search root that contains it.
fn depth_below_roots(path: &Path, roots: &[PathBuf]) -> Option<usize> {
    let components = path.components().count();
    roots
        .iter()
        .filter(|root| path.starts_with(root))
        .map(|root| components.saturating_sub(root.components().count()))
        .min()
}

/// A seeded hash of the path. Using the path rather than the position of an entry makes the
/// order independent of the traversal.
fn random_key(seed: u64, bytes: &[u8]) -> u64 {
    let mut hash = splitmix64(seed);
    for &byte in bytes {
        hash ^= u64::from(byte);
        hash = hash.wrapping_mul(0x0000_0100_0000_01b3);
    }
    splitmix64(hash)
}

fn splitmix64(mut z: u64) -> u64 {
    z = z.wrapping_add(0x9e37_79b9_7f4a_7c15);
    z = (z ^ (z >> 30)).wrapping_mul(0xbf58_476d_1ce4_e5b9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94d0_49bb_1331_11eb);
    z ^ (z >> 31)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn natural_order() {
        assert_eq!(natural_cmp("file9", "file10"), Ordering::Less);
        assert_eq!(natural_cmp("file10", "file20"), Ordering::Less);
        assert_eq!(natural_cmp("file007", "file7"), Ordering::Equal);
        assert_eq!(natural_cmp("file007", "file8"), Ordering::Less);
        assert_eq!(natural_cmp("a1b2", "a1b10"), Ordering::Less);
        assert_eq!(natural_cmp("file", "file1"), Ordering::Less);
        assert_eq!(natural_cmp("File2", "file10"), Ordering::Less);
        assert_eq!(natural_cmp("x1", "xa"), Ordering::Less);
        assert_eq!(natural_cmp("0", "00"), Ordering::Equal);
    }

    #[test]
    fn missing_values() {
        let (none, some) = (None, Some(1u64));
        assert_eq!(
            compare_optional(&none, &some, false, Ord::cmp),
            Ordering::Less
        );
        assert_eq!(
            compare_optional(&none, &some, true, Ord::cmp),
            Ordering::Greater
        );
        assert_eq!(
            compare_optional(&some, &none, true, Ord::cmp),
            Ordering::Less
        );
    }

    #[test]
    fn random_key_depends_on_seed() {
        assert_eq!(random_key(1, b"a"), random_key(1, b"a"));
        assert_ne!(random_key(1, b"a"), random_key(2, b"a"));
        assert_ne!(random_key(1, b"a"), random_key(1, b"b"));
    }
}
