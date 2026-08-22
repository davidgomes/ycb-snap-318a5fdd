use std::borrow::Cow;
use std::cmp::Ordering;
use std::path::Path;
use std::time::SystemTime;

use crate::dir_entry::DirEntry;

#[derive(Copy, Clone, Debug, Eq, PartialEq, clap::ValueEnum)]
pub enum SortField {
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

pub struct SortConfig {
    pub fields: Vec<SortField>,
    pub reverse: bool,
    pub dirs_first: bool,
    pub files_first: bool,
    pub case_sensitive: bool,
    pub missing_last: bool,
    pub natural: bool,
    pub seed: u64,
}

pub fn compare_entries(a: &DirEntry, b: &DirEntry, config: &SortConfig) -> Ordering {
    let mut ordering = compare_group(a, b, config);

    if ordering == Ordering::Equal {
        ordering = config
            .fields
            .iter()
            .map(|field| compare_field(a, b, *field, config))
            .find(|ordering| *ordering != Ordering::Equal)
            .unwrap_or_else(|| a.path().cmp(b.path()));
    }

    if config.reverse {
        ordering.reverse()
    } else {
        ordering
    }
}

fn compare_group(a: &DirEntry, b: &DirEntry, config: &SortConfig) -> Ordering {
    if config.dirs_first {
        return is_directory(b).cmp(&is_directory(a));
    }
    if config.files_first {
        return is_regular_file(b).cmp(&is_regular_file(a));
    }
    Ordering::Equal
}

fn compare_field(a: &DirEntry, b: &DirEntry, field: SortField, config: &SortConfig) -> Ordering {
    match field {
        SortField::Path => compare_text(
            &a.path().to_string_lossy(),
            &b.path().to_string_lossy(),
            config,
        ),
        SortField::Name => compare_text(
            &entry_name(a).to_string_lossy(),
            &entry_name(b).to_string_lossy(),
            config,
        ),
        SortField::Extension => {
            compare_optional_text(entry_extension(a), entry_extension(b), config)
        }
        SortField::Size => compare_optional(
            regular_file_size(a),
            regular_file_size(b),
            config.missing_last,
        ),
        SortField::Modified => compare_optional(
            entry_time(a, |metadata| metadata.modified()),
            entry_time(b, |metadata| metadata.modified()),
            config.missing_last,
        ),
        SortField::Created => compare_optional(
            entry_time(a, |metadata| metadata.created()),
            entry_time(b, |metadata| metadata.created()),
            config.missing_last,
        ),
        SortField::Accessed => compare_optional(
            entry_time(a, |metadata| metadata.accessed()),
            entry_time(b, |metadata| metadata.accessed()),
            config.missing_last,
        ),
        SortField::Depth => compare_optional(a.depth(), b.depth(), config.missing_last),
        SortField::Type => entry_type(a).cmp(&entry_type(b)),
        SortField::NameLength => entry_name(a)
            .to_string_lossy()
            .chars()
            .count()
            .cmp(&entry_name(b).to_string_lossy().chars().count()),
        SortField::PathLength => a
            .path()
            .to_string_lossy()
            .chars()
            .count()
            .cmp(&b.path().to_string_lossy().chars().count()),
        SortField::Random => {
            random_value(config.seed, a.path()).cmp(&random_value(config.seed, b.path()))
        }
    }
}

fn compare_optional<T: Ord>(a: Option<T>, b: Option<T>, missing_last: bool) -> Ordering {
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

fn compare_optional_text(
    a: Option<Cow<'_, str>>,
    b: Option<Cow<'_, str>>,
    config: &SortConfig,
) -> Ordering {
    match (a, b) {
        (Some(a), Some(b)) => compare_text(&a, &b, config),
        (None, None) => Ordering::Equal,
        (None, Some(_)) => {
            if config.missing_last {
                Ordering::Greater
            } else {
                Ordering::Less
            }
        }
        (Some(_), None) => {
            if config.missing_last {
                Ordering::Less
            } else {
                Ordering::Greater
            }
        }
    }
}

fn compare_text(a: &str, b: &str, config: &SortConfig) -> Ordering {
    if config.natural {
        natural_cmp(a, b, config.case_sensitive)
    } else if config.case_sensitive {
        a.cmp(b)
    } else {
        a.to_lowercase().cmp(&b.to_lowercase())
    }
}

fn natural_cmp(a: &str, b: &str, case_sensitive: bool) -> Ordering {
    let mut a = a.chars().peekable();
    let mut b = b.chars().peekable();

    loop {
        match (a.peek().copied(), b.peek().copied()) {
            (None, None) => return Ordering::Equal,
            (None, Some(_)) => return Ordering::Less,
            (Some(_), None) => return Ordering::Greater,
            (Some(a_char), Some(b_char)) if a_char.is_ascii_digit() && b_char.is_ascii_digit() => {
                let a_digits = take_ascii_digits(&mut a);
                let b_digits = take_ascii_digits(&mut b);
                let a_significant = a_digits.trim_start_matches('0');
                let b_significant = b_digits.trim_start_matches('0');
                let a_significant = if a_significant.is_empty() {
                    "0"
                } else {
                    a_significant
                };
                let b_significant = if b_significant.is_empty() {
                    "0"
                } else {
                    b_significant
                };

                match a_significant
                    .len()
                    .cmp(&b_significant.len())
                    .then_with(|| a_significant.cmp(b_significant))
                {
                    Ordering::Equal => {
                        // Equal numeric runs are left equal so that a later
                        // sort key, or the raw path tie-breaker, decides.
                    }
                    ordering => return ordering,
                }
            }
            (Some(_), Some(_)) => {
                let a_run = take_non_digits(&mut a);
                let b_run = take_non_digits(&mut b);
                let a_run = if case_sensitive {
                    a_run
                } else {
                    a_run.to_lowercase()
                };
                let b_run = if case_sensitive {
                    b_run
                } else {
                    b_run.to_lowercase()
                };
                if a_run != b_run {
                    return a_run.cmp(&b_run);
                }
            }
        }
    }
}

fn take_ascii_digits<I>(chars: &mut std::iter::Peekable<I>) -> String
where
    I: Iterator<Item = char>,
{
    let mut result = String::new();
    while chars.peek().is_some_and(char::is_ascii_digit) {
        result.push(chars.next().unwrap());
    }
    result
}

fn take_non_digits<I>(chars: &mut std::iter::Peekable<I>) -> String
where
    I: Iterator<Item = char>,
{
    let mut result = String::new();
    while chars
        .peek()
        .is_some_and(|character| !character.is_ascii_digit())
    {
        result.push(chars.next().unwrap());
    }
    result
}

fn entry_name(entry: &DirEntry) -> &std::ffi::OsStr {
    entry
        .path()
        .file_name()
        .unwrap_or_else(|| entry.path().as_os_str())
}

fn entry_extension(entry: &DirEntry) -> Option<Cow<'_, str>> {
    entry
        .path()
        .extension()
        .map(|extension| extension.to_string_lossy())
}

fn regular_file_size(entry: &DirEntry) -> Option<u64> {
    if is_regular_file(entry) {
        entry.metadata().map(std::fs::Metadata::len)
    } else {
        None
    }
}

fn entry_time(
    entry: &DirEntry,
    getter: impl Fn(&std::fs::Metadata) -> std::io::Result<SystemTime>,
) -> Option<SystemTime> {
    entry.metadata().and_then(|metadata| getter(metadata).ok())
}

fn is_directory(entry: &DirEntry) -> bool {
    entry
        .file_type()
        .is_some_and(|file_type| file_type.is_dir())
}

fn is_regular_file(entry: &DirEntry) -> bool {
    entry
        .file_type()
        .is_some_and(|file_type| file_type.is_file())
}

fn entry_type(entry: &DirEntry) -> u8 {
    match entry.file_type() {
        Some(file_type) if file_type.is_dir() => 0,
        Some(file_type) if file_type.is_symlink() => 1,
        Some(file_type) if file_type.is_file() => 2,
        _ => 3,
    }
}

fn random_value(seed: u64, path: &Path) -> u64 {
    let mut value = seed ^ 0x9e37_79b9_7f4a_7c15;
    for byte in path.to_string_lossy().bytes() {
        value ^= u64::from(byte);
        value = value.wrapping_mul(0xbf58_476d_1ce4_e5b9);
        value ^= value >> 32;
    }
    value ^= value >> 30;
    value = value.wrapping_mul(0xbf58_476d_1ce4_e5b9);
    value ^= value >> 27;
    value = value.wrapping_mul(0x94d0_49bb_1331_11eb);
    value ^ (value >> 31)
}
