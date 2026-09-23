use std::cmp::Ordering;
use std::path::Path;
use std::time::SystemTime;

use crate::cli::SortKey;
use crate::config::Config;
use crate::dir_entry::DirEntry;
use crate::filesystem;

#[derive(Copy, Clone, PartialEq, Eq, Debug)]
pub enum Grouping {
    None,
    DirsFirst,
    FilesFirst,
}

/// Sorting options for printed search results.
pub struct SortConfig {
    pub keys: Vec<SortKey>,
    pub reverse: bool,
    pub grouping: Grouping,
    pub case_sensitive: bool,
    pub missing_last: bool,
    pub natural: bool,
    pub seed: u64,
}

impl SortConfig {
    pub fn default_seed() -> u64 {
        SystemTime::now()
            .duration_since(SystemTime::UNIX_EPOCH)
            .map(|d| d.as_nanos() as u64)
            .unwrap_or(0)
            ^ u64::from(std::process::id()).rotate_left(32)
    }
}

/// Entry kinds in the order used by `--sort type`.
#[derive(Copy, Clone, PartialEq, Eq, PartialOrd, Ord)]
enum Kind {
    Directory,
    Symlink,
    File,
    Other,
}

/// Precomputed sort values of one entry. Text fields are already case-folded
/// unless sorting is case-sensitive.
struct Record {
    kind: Kind,
    components: Vec<String>,
    name: String,
    extension: Option<String>,
    size: Option<u64>,
    modified: Option<SystemTime>,
    created: Option<SystemTime>,
    accessed: Option<SystemTime>,
    depth: Option<usize>,
    name_length: usize,
    path_length: usize,
    random: u64,
}

impl Record {
    fn new(entry: &DirEntry, sort: &SortConfig, config: &Config) -> Self {
        let fold = |s: &str| {
            if sort.case_sensitive {
                s.to_owned()
            } else {
                s.to_lowercase()
            }
        };
        let needs = |key: SortKey| sort.keys.contains(&key);

        let path = entry.stripped_path(config);
        let raw_name = file_name(entry.path());

        let kind = match entry.file_type() {
            Some(t) if t.is_dir() => Kind::Directory,
            Some(t) if t.is_symlink() => Kind::Symlink,
            Some(t) if t.is_file() => Kind::File,
            _ => Kind::Other,
        };

        let metadata = if needs(SortKey::Size)
            || needs(SortKey::Modified)
            || needs(SortKey::Created)
            || needs(SortKey::Accessed)
        {
            entry.metadata()
        } else {
            None
        };

        Record {
            kind,
            components: path
                .components()
                .map(|c| fold(&c.as_os_str().to_string_lossy()))
                .collect(),
            name: fold(&raw_name),
            extension: Path::new(&raw_name)
                .extension()
                .map(|e| e.to_string_lossy())
                .filter(|e| !e.is_empty())
                .map(|e| fold(&e)),
            size: metadata.filter(|_| kind == Kind::File).map(|m| m.len()),
            modified: metadata.and_then(|m| m.modified().ok()),
            created: metadata.and_then(|m| m.created().ok()),
            accessed: metadata.and_then(|m| m.accessed().ok()),
            depth: entry.depth(),
            name_length: raw_name.chars().count(),
            path_length: path.to_string_lossy().chars().count(),
            random: if needs(SortKey::Random) {
                random_key(
                    sort.seed,
                    &filesystem::osstr_to_bytes(entry.path().as_os_str()),
                )
            } else {
                0
            },
        }
    }
}

fn file_name(path: &Path) -> String {
    path.components()
        .next_back()
        .map(|c| c.as_os_str())
        .unwrap_or_else(|| path.as_os_str())
        .to_string_lossy()
        .into_owned()
}

fn splitmix64(mut x: u64) -> u64 {
    x = x.wrapping_add(0x9e37_79b9_7f4a_7c15);
    x = (x ^ (x >> 30)).wrapping_mul(0xbf58_476d_1ce4_e5b9);
    x = (x ^ (x >> 27)).wrapping_mul(0x94d0_49bb_1331_11eb);
    x ^ (x >> 31)
}

/// A seeded hash of the path, so the shuffle does not depend on traversal order.
fn random_key(seed: u64, bytes: &[u8]) -> u64 {
    let mut hash = 0xcbf2_9ce4_8422_2325 ^ splitmix64(seed);
    for &b in bytes {
        hash ^= u64::from(b);
        hash = hash.wrapping_mul(0x0000_0100_0000_01b3);
    }
    splitmix64(hash)
}

fn trim_leading_zeros(digits: &[u8]) -> &[u8] {
    let zeros = digits.iter().take_while(|&&c| c == b'0').count();
    &digits[zeros..]
}

/// Compare strings, treating runs of ASCII digits as numbers.
fn natural_cmp(a: &str, b: &str) -> Ordering {
    let (a, b) = (a.as_bytes(), b.as_bytes());
    let (mut i, mut j) = (0, 0);
    while i < a.len() && j < b.len() {
        if a[i].is_ascii_digit() && b[j].is_ascii_digit() {
            let (start_i, start_j) = (i, j);
            while i < a.len() && a[i].is_ascii_digit() {
                i += 1;
            }
            while j < b.len() && b[j].is_ascii_digit() {
                j += 1;
            }
            let (num_a, num_b) = (
                trim_leading_zeros(&a[start_i..i]),
                trim_leading_zeros(&b[start_j..j]),
            );
            let ord = num_a.len().cmp(&num_b.len()).then_with(|| num_a.cmp(num_b));
            if ord != Ordering::Equal {
                return ord;
            }
        } else {
            let ord = a[i].cmp(&b[j]);
            if ord != Ordering::Equal {
                return ord;
            }
            i += 1;
            j += 1;
        }
    }
    (a.len() - i).cmp(&(b.len() - j))
}

impl SortConfig {
    fn cmp_text(&self, a: &str, b: &str) -> Ordering {
        if self.natural {
            // Numerically equal digit runs (e.g. "007" and "7") fall back to
            // plain comparison, so the key stays a total order.
            natural_cmp(a, b).then_with(|| a.cmp(b))
        } else {
            a.cmp(b)
        }
    }

    fn cmp_path(&self, a: &[String], b: &[String]) -> Ordering {
        a.iter()
            .zip(b)
            .map(|(x, y)| self.cmp_text(x, y))
            .find(|o| o.is_ne())
            .unwrap_or_else(|| a.len().cmp(&b.len()))
    }

    fn cmp_optional<T>(
        &self,
        a: &Option<T>,
        b: &Option<T>,
        cmp: impl FnOnce(&T, &T) -> Ordering,
    ) -> Ordering {
        match (a, b) {
            (Some(x), Some(y)) => cmp(x, y),
            (None, None) => Ordering::Equal,
            (None, Some(_)) if self.missing_last => Ordering::Greater,
            (None, Some(_)) => Ordering::Less,
            (Some(_), None) if self.missing_last => Ordering::Less,
            (Some(_), None) => Ordering::Greater,
        }
    }

    fn cmp_key(&self, key: SortKey, a: &Record, b: &Record) -> Ordering {
        match key {
            SortKey::Path => self.cmp_path(&a.components, &b.components),
            SortKey::Name => self.cmp_text(&a.name, &b.name),
            SortKey::Extension => {
                self.cmp_optional(&a.extension, &b.extension, |x, y| self.cmp_text(x, y))
            }
            SortKey::Size => self.cmp_optional(&a.size, &b.size, Ord::cmp),
            SortKey::Modified => self.cmp_optional(&a.modified, &b.modified, Ord::cmp),
            SortKey::Created => self.cmp_optional(&a.created, &b.created, Ord::cmp),
            SortKey::Accessed => self.cmp_optional(&a.accessed, &b.accessed, Ord::cmp),
            SortKey::Depth => self.cmp_optional(&a.depth, &b.depth, Ord::cmp),
            SortKey::Type => a.kind.cmp(&b.kind),
            SortKey::NameLength => a.name_length.cmp(&b.name_length),
            SortKey::PathLength => a.path_length.cmp(&b.path_length),
            SortKey::Random => a.random.cmp(&b.random),
        }
    }

    fn group(&self, record: &Record) -> u8 {
        match self.grouping {
            Grouping::None => 0,
            Grouping::DirsFirst => u8::from(record.kind != Kind::Directory),
            Grouping::FilesFirst => u8::from(record.kind != Kind::File),
        }
    }

    fn cmp_entries(
        &self,
        (ra, ea): &(Record, DirEntry),
        (rb, eb): &(Record, DirEntry),
    ) -> Ordering {
        self.group(ra)
            .cmp(&self.group(rb))
            .then_with(|| {
                self.keys
                    .iter()
                    .map(|&key| self.cmp_key(key, ra, rb))
                    .find(|o| o.is_ne())
                    .unwrap_or(Ordering::Equal)
            })
            .then_with(|| self.cmp_path(&ra.components, &rb.components))
            .then_with(|| ea.path().as_os_str().cmp(eb.path().as_os_str()))
    }

    /// Sort the entries according to this configuration.
    pub fn sort(&self, entries: Vec<DirEntry>, config: &Config) -> Vec<DirEntry> {
        let mut records: Vec<(Record, DirEntry)> = entries
            .into_iter()
            .map(|entry| (Record::new(&entry, self, config), entry))
            .collect();
        records.sort_by(|a, b| self.cmp_entries(a, b));
        if self.reverse {
            records.reverse();
        }
        records.into_iter().map(|(_, entry)| entry).collect()
    }
}

#[cfg(test)]
mod tests {
    use super::natural_cmp;
    use std::cmp::Ordering::*;

    #[test]
    fn natural_order() {
        assert_eq!(natural_cmp("file9", "file10"), Less);
        assert_eq!(natural_cmp("file10", "file20"), Less);
        assert_eq!(natural_cmp("file007", "file7"), Equal);
        assert_eq!(natural_cmp("file007", "file8"), Less);
        assert_eq!(natural_cmp("a2b10", "a2b9"), Greater);
        assert_eq!(natural_cmp("file", "file1"), Less);
        assert_eq!(natural_cmp("file1a", "file1"), Greater);
    }
}
