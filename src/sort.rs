use std::cmp::Ordering;
use std::ffi::OsStr;
use std::path::Path;
use std::time::{SystemTime, UNIX_EPOCH};

use crate::cli::SortField;
use crate::config::Config;
use crate::dir_entry::DirEntry;
use crate::filesystem;

/// Which kind of entries to list before all others.
#[derive(Copy, Clone, PartialEq, Eq, Debug)]
pub enum SortGroup {
    DirsFirst,
    FilesFirst,
}

/// Configuration for sorting the search results.
pub struct SortOptions {
    /// The sort keys, in order of precedence.
    pub keys: Vec<SortField>,

    /// Whether to reverse the final order.
    pub reverse: bool,

    /// Entries to list before all others, regardless of the sort keys.
    pub group: Option<SortGroup>,

    /// Whether text keys are compared case-sensitively.
    pub case_sensitive: bool,

    /// Whether entries with a missing value sort after entries with a value.
    pub missing_last: bool,

    /// Whether digit runs in text keys are compared numerically.
    pub natural: bool,

    /// Seed for `SortField::Random`.
    pub seed: u64,
}

/// The value of a sort key for a single entry. All values of a given key have the same variant.
#[derive(PartialEq, Eq, PartialOrd, Ord)]
enum Value {
    Number(u64),
    Time(SystemTime),
    Text(String),
}

struct SortKey {
    /// `false` for entries of the group that is listed first.
    secondary: bool,
    /// One value per sort key, followed by the path as an implicit tie-breaker.
    values: Vec<Option<Value>>,
}

impl SortOptions {
    /// Whether computing the sort keys requires the metadata of the entries.
    pub fn needs_metadata(&self) -> bool {
        self.keys.iter().any(|key| {
            matches!(
                key,
                SortField::Size | SortField::Modified | SortField::Created | SortField::Accessed
            )
        })
    }

    /// Sort the entries and return them in the order in which they should be printed.
    pub fn sort(&self, entries: Vec<DirEntry>, config: &Config) -> Vec<DirEntry> {
        let keys: Vec<SortKey> = entries
            .iter()
            .map(|entry| self.key(entry, config))
            .collect();

        // Sort indices rather than the (large) entries themselves
        let mut order: Vec<usize> = (0..entries.len()).collect();
        order.sort_by(|&a, &b| {
            self.compare(&keys[a], &keys[b])
                // Distinguishes paths that are only equal after case folding or natural comparison
                .then_with(|| {
                    let (a, b) = (entries[a].path(), entries[b].path());
                    a.as_os_str().cmp(b.as_os_str())
                })
        });
        if self.reverse {
            order.reverse();
        }

        let mut entries: Vec<Option<DirEntry>> = entries.into_iter().map(Some).collect();
        order
            .into_iter()
            .map(|i| entries[i].take().expect("every index occurs exactly once"))
            .collect()
    }

    fn key(&self, entry: &DirEntry, config: &Config) -> SortKey {
        let path = entry.stripped_path(config);

        let mut values: Vec<Option<Value>> = Vec::with_capacity(self.keys.len() + 1);
        values.extend(self.keys.iter().map(|&key| self.value(key, entry, path)));
        values.push(Some(self.text(path.as_os_str())));

        let secondary = match self.group {
            Some(SortGroup::DirsFirst) => !entry.file_type().is_some_and(|ft| ft.is_dir()),
            Some(SortGroup::FilesFirst) => !entry.file_type().is_some_and(|ft| ft.is_file()),
            None => false,
        };

        SortKey { secondary, values }
    }

    fn value(&self, key: SortField, entry: &DirEntry, path: &Path) -> Option<Value> {
        let time = |get: fn(&std::fs::Metadata) -> std::io::Result<SystemTime>| {
            entry.metadata().and_then(|m| get(m).ok()).map(Value::Time)
        };

        match key {
            SortField::Path => Some(self.text(path.as_os_str())),
            SortField::Name => Some(self.text(file_name(path))),
            SortField::Extension => path.extension().map(|ext| self.text(ext)),
            SortField::Size => entry
                .file_type()
                .filter(|ft| ft.is_file())
                .and_then(|_| entry.metadata())
                .map(|m| Value::Number(m.len())),
            SortField::Modified => time(std::fs::Metadata::modified),
            SortField::Created => time(std::fs::Metadata::created),
            SortField::Accessed => time(std::fs::Metadata::accessed),
            SortField::Depth => entry.depth().map(|d| Value::Number(d as u64)),
            SortField::Type => Some(Value::Number(type_rank(entry))),
            SortField::NameLength => Some(Value::Number(char_count(file_name(path)))),
            SortField::PathLength => Some(Value::Number(char_count(path.as_os_str()))),
            SortField::Random => Some(Value::Number(random_rank(self.seed, path))),
        }
    }

    fn text(&self, s: &OsStr) -> Value {
        let s = s.to_string_lossy();
        Value::Text(if self.case_sensitive {
            s.into_owned()
        } else {
            s.to_lowercase()
        })
    }

    fn compare(&self, a: &SortKey, b: &SortKey) -> Ordering {
        a.secondary.cmp(&b.secondary).then_with(|| {
            a.values
                .iter()
                .zip(&b.values)
                .map(|(x, y)| self.compare_values(x.as_ref(), y.as_ref()))
                .find(|ordering| ordering.is_ne())
                .unwrap_or(Ordering::Equal)
        })
    }

    fn compare_values(&self, a: Option<&Value>, b: Option<&Value>) -> Ordering {
        match (a, b) {
            (Some(Value::Text(a)), Some(Value::Text(b))) if self.natural => natural_cmp(a, b),
            (Some(a), Some(b)) => a.cmp(b),
            (None, None) => Ordering::Equal,
            (None, Some(_)) if self.missing_last => Ordering::Greater,
            (None, Some(_)) => Ordering::Less,
            (Some(_), None) if self.missing_last => Ordering::Less,
            (Some(_), None) => Ordering::Greater,
        }
    }
}

fn file_name(path: &Path) -> &OsStr {
    path.file_name().unwrap_or(path.as_os_str())
}

fn char_count(s: &OsStr) -> u64 {
    s.to_string_lossy().chars().count() as u64
}

fn type_rank(entry: &DirEntry) -> u64 {
    match entry.file_type() {
        Some(ft) if ft.is_dir() => 0,
        Some(ft) if ft.is_symlink() => 1,
        Some(ft) if ft.is_file() => 2,
        _ => 3,
    }
}

/// Compare two strings, treating each run of ASCII digits as a single number.
///
/// Numbers that only differ in leading zeros compare equal.
fn natural_cmp(a: &str, b: &str) -> Ordering {
    // ASCII digits never occur inside multi-byte UTF-8 sequences, and byte order matches
    // code point order, so it is safe to operate on bytes.
    let (a, b) = (a.as_bytes(), b.as_bytes());
    let (mut i, mut j) = (0, 0);

    while i < a.len() && j < b.len() {
        if a[i].is_ascii_digit() && b[j].is_ascii_digit() {
            let (start_a, start_b) = (i, j);
            while i < a.len() && a[i].is_ascii_digit() {
                i += 1;
            }
            while j < b.len() && b[j].is_ascii_digit() {
                j += 1;
            }
            let num_a = trim_leading_zeros(&a[start_a..i]);
            let num_b = trim_leading_zeros(&b[start_b..j]);
            let ordering = num_a.len().cmp(&num_b.len()).then_with(|| num_a.cmp(num_b));
            if ordering.is_ne() {
                return ordering;
            }
        } else {
            let ordering = a[i].cmp(&b[j]);
            if ordering.is_ne() {
                return ordering;
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

/// Derive a seed for `--sort random` from the current time.
pub fn seed_from_time() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |d| d.as_nanos() as u64)
}

/// A pseudo-random rank for the given path, which only depends on the seed and the path itself,
/// so that the order is independent of the traversal order.
fn random_rank(seed: u64, path: &Path) -> u64 {
    const FNV_PRIME: u64 = 0x0000_0100_0000_01b3;

    let mut hash = splitmix64(seed);
    for &byte in filesystem::osstr_to_bytes(path.as_os_str()).iter() {
        hash = (hash ^ u64::from(byte)).wrapping_mul(FNV_PRIME);
    }
    splitmix64(hash)
}

fn splitmix64(x: u64) -> u64 {
    let x = x.wrapping_add(0x9e37_79b9_7f4a_7c15);
    let x = (x ^ (x >> 30)).wrapping_mul(0xbf58_476d_1ce4_e5b9);
    let x = (x ^ (x >> 27)).wrapping_mul(0x94d0_49bb_1331_11eb);
    x ^ (x >> 31)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn natural_sorted(names: &[&str]) -> Vec<String> {
        let mut names: Vec<String> = names.iter().map(|s| s.to_string()).collect();
        names.sort_by(|a, b| natural_cmp(a, b));
        names
    }

    #[test]
    fn natural_numbers_compare_numerically() {
        assert_eq!(
            natural_sorted(&["file20", "file10", "file9", "file1"]),
            ["file1", "file9", "file10", "file20"]
        );
        assert_eq!(natural_cmp("a2b10", "a2b9"), Ordering::Greater);
        assert_eq!(natural_cmp("v1.10.0", "v1.9.3"), Ordering::Greater);
    }

    #[test]
    fn natural_leading_zeros_are_equal() {
        assert_eq!(natural_cmp("file007", "file7"), Ordering::Equal);
        assert_eq!(natural_cmp("file007", "file8"), Ordering::Less);
        assert_eq!(natural_cmp("file0", "file000"), Ordering::Equal);
    }

    #[test]
    fn natural_large_numbers_do_not_overflow() {
        assert_eq!(
            natural_cmp("x123456789012345678901234567890", "x99999999999999999999"),
            Ordering::Greater
        );
    }

    #[test]
    fn natural_non_digits_compare_bytewise() {
        assert_eq!(natural_cmp("abc", "abd"), Ordering::Less);
        assert_eq!(natural_cmp("ab", "abc"), Ordering::Less);
        assert_eq!(natural_cmp("a1", "a"), Ordering::Greater);
        assert_eq!(natural_cmp("a-", "a1b"), Ordering::Less);
        assert_eq!(natural_cmp("B1", "a1"), Ordering::Less);
        assert_eq!(natural_cmp("", ""), Ordering::Equal);
    }

    #[test]
    fn random_rank_depends_on_seed_and_path() {
        let a = Path::new("a");
        let b = Path::new("b");
        assert_eq!(random_rank(1, a), random_rank(1, a));
        assert_ne!(random_rank(1, a), random_rank(2, a));
        assert_ne!(random_rank(1, a), random_rank(1, b));
    }
}
