use std::cmp::Ordering;
use std::time::SystemTime;

use clap::ValueEnum;

use crate::dir_entry::DirEntry;

#[derive(Copy, Clone, Debug, PartialEq, Eq, ValueEnum)]
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

#[derive(Copy, Clone, PartialEq, Eq, PartialOrd, Ord)]
enum Kind {
    Dir,
    Symlink,
    File,
    Other,
}

enum Value {
    Text(String),
    Num(u128),
    Time(Option<SystemTime>),
    OptNum(Option<u128>),
}

struct Keyed {
    entry: DirEntry,
    kind: Kind,
    path: String,
    values: Vec<Value>,
}

fn kind_of(entry: &DirEntry) -> Kind {
    match entry.file_type() {
        Some(ft) if ft.is_dir() => Kind::Dir,
        Some(ft) if ft.is_symlink() => Kind::Symlink,
        Some(ft) if ft.is_file() => Kind::File,
        _ => Kind::Other,
    }
}

fn mix(mut x: u64) -> u64 {
    x = x.wrapping_add(0x9e37_79b9_7f4a_7c15);
    x = (x ^ (x >> 30)).wrapping_mul(0xbf58_476d_1ce4_e5b9);
    x = (x ^ (x >> 27)).wrapping_mul(0x94d0_49bb_1331_11eb);
    x ^ (x >> 31)
}

fn random_rank(path: &[u8], seed: u64) -> u64 {
    let mut h = mix(seed);
    for &b in path {
        h = mix(h ^ u64::from(b));
    }
    h
}

impl SortConfig {
    fn value(&self, key: SortKey, entry: &DirEntry, kind: Kind, path: &str) -> Value {
        let p = entry.path();
        match key {
            SortKey::Path => Value::Text(path.to_owned()),
            SortKey::Name => Value::Text(
                p.file_name()
                    .map(|n| n.to_string_lossy().into_owned())
                    .unwrap_or_default(),
            ),
            SortKey::Extension => match p.extension() {
                Some(e) => Value::Text(e.to_string_lossy().into_owned()),
                None => Value::OptNum(None),
            },
            SortKey::Size => Value::OptNum(
                (kind == Kind::File)
                    .then(|| entry.metadata().map(|m| u128::from(m.len())))
                    .flatten(),
            ),
            SortKey::Modified => Value::Time(entry.metadata().and_then(|m| m.modified().ok())),
            SortKey::Created => Value::Time(entry.metadata().and_then(|m| m.created().ok())),
            SortKey::Accessed => Value::Time(entry.metadata().and_then(|m| m.accessed().ok())),
            SortKey::Depth => Value::OptNum(entry.depth().map(|d| d as u128)),
            SortKey::Type => Value::Num(kind as u128),
            SortKey::NameLength => Value::Num(
                p.file_name()
                    .map_or(0, |n| n.to_string_lossy().chars().count()) as u128,
            ),
            SortKey::PathLength => Value::Num(path.chars().count() as u128),
            SortKey::Random => Value::Num(u128::from(random_rank(
                p.as_os_str().as_encoded_bytes(),
                self.seed,
            ))),
        }
    }

    fn missing<T: Ord>(&self, a: &Option<T>, b: &Option<T>) -> Ordering {
        match (a, b) {
            (Some(x), Some(y)) => x.cmp(y),
            (None, None) => Ordering::Equal,
            (None, Some(_)) if self.missing_last => Ordering::Greater,
            (None, Some(_)) => Ordering::Less,
            (Some(_), None) if self.missing_last => Ordering::Less,
            (Some(_), None) => Ordering::Greater,
        }
    }

    fn cmp_value(&self, a: &Value, b: &Value) -> Ordering {
        match (a, b) {
            (Value::Text(x), Value::Text(y)) => self.cmp_text(x, y),
            (Value::Num(x), Value::Num(y)) => x.cmp(y),
            (Value::Time(x), Value::Time(y)) => self.missing(x, y),
            (Value::OptNum(x), Value::OptNum(y)) => self.missing(x, y),
            // Extension: present text vs. missing
            (Value::Text(_), _) => self.missing(&Some(()), &None),
            (_, Value::Text(_)) => self.missing(&None, &Some(())),
            _ => Ordering::Equal,
        }
    }

    fn cmp_text(&self, a: &str, b: &str) -> Ordering {
        if self.natural {
            natural_cmp(a, b, self.case_sensitive)
        } else if self.case_sensitive {
            a.cmp(b)
        } else {
            fold_cmp(a, b)
        }
    }

    fn group(&self, kind: Kind) -> u8 {
        let first = (self.dirs_first && kind == Kind::Dir)
            || (self.files_first && kind == Kind::File);
        u8::from(!first)
    }

    fn cmp(&self, a: &Keyed, b: &Keyed) -> Ordering {
        self.group(a.kind)
            .cmp(&self.group(b.kind))
            .then_with(|| {
                a.values
                    .iter()
                    .zip(&b.values)
                    .map(|(x, y)| self.cmp_value(x, y))
                    .find(|o| o.is_ne())
                    .unwrap_or(Ordering::Equal)
            })
            .then_with(|| self.cmp_text(&a.path, &b.path))
            .then_with(|| a.entry.path().cmp(b.entry.path()))
            .then_with(|| {
                a.entry
                    .path()
                    .as_os_str()
                    .as_encoded_bytes()
                    .cmp(b.entry.path().as_os_str().as_encoded_bytes())
            })
    }

    /// Sort entries, apply `--reverse`, then truncate to `limit`.
    pub fn sort(&self, entries: Vec<DirEntry>, limit: Option<usize>) -> Vec<DirEntry> {
        let mut keyed: Vec<Keyed> = entries
            .into_iter()
            .map(|entry| {
                let kind = kind_of(&entry);
                let path = entry.path().to_string_lossy().into_owned();
                let values = self
                    .keys
                    .iter()
                    .map(|&k| self.value(k, &entry, kind, &path))
                    .collect();
                Keyed {
                    entry,
                    kind,
                    path,
                    values,
                }
            })
            .collect();
        keyed.sort_by(|a, b| self.cmp(a, b));
        if self.reverse {
            keyed.reverse();
        }
        if let Some(n) = limit {
            keyed.truncate(n);
        }
        keyed.into_iter().map(|k| k.entry).collect()
    }
}

fn fold_cmp(a: &str, b: &str) -> Ordering {
    a.chars()
        .flat_map(char::to_lowercase)
        .cmp(b.chars().flat_map(char::to_lowercase))
}

fn chunks(s: &str) -> impl Iterator<Item = (bool, &str)> {
    let mut rest = s;
    std::iter::from_fn(move || {
        let first = rest.chars().next()?;
        let digit = first.is_ascii_digit();
        let end = rest
            .find(|c: char| c.is_ascii_digit() != digit)
            .unwrap_or(rest.len());
        let (chunk, tail) = rest.split_at(end);
        rest = tail;
        Some((digit, chunk))
    })
}

/// Natural ordering: ASCII digit runs compare numerically (ties on value are
/// broken by fewer leading zeros first), other runs compare as text.
fn natural_cmp(a: &str, b: &str, case_sensitive: bool) -> Ordering {
    let mut ia = chunks(a);
    let mut ib = chunks(b);
    loop {
        let ord = match (ia.next(), ib.next()) {
            (None, None) => return Ordering::Equal,
            (None, Some(_)) => return Ordering::Less,
            (Some(_), None) => return Ordering::Greater,
            (Some((true, x)), Some((true, y))) => {
                let tx = x.trim_start_matches('0');
                let ty = y.trim_start_matches('0');
                tx.len()
                    .cmp(&ty.len())
                    .then_with(|| tx.cmp(ty))
                    .then_with(|| x.len().cmp(&y.len()))
            }
            (Some((_, x)), Some((_, y))) => {
                if case_sensitive {
                    x.cmp(y)
                } else {
                    fold_cmp(x, y)
                }
            }
        };
        if ord.is_ne() {
            return ord;
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn natural() {
        assert_eq!(natural_cmp("file9", "file10", false), Ordering::Less);
        assert_eq!(natural_cmp("file10", "file20", false), Ordering::Less);
        assert_eq!(natural_cmp("file7", "file007", false), Ordering::Less);
        assert_eq!(natural_cmp("file007", "file8", false), Ordering::Less);
        assert_eq!(natural_cmp("File2", "file10", false), Ordering::Less);
        assert_eq!(natural_cmp("File2", "file1", true), Ordering::Less);
    }
}
