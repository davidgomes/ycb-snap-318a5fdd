use std::borrow::Cow;
use std::cmp::Ordering;
use std::time::{SystemTime, UNIX_EPOCH};

use crate::cli::SortField;
use crate::config::SortOptions;
use crate::dir_entry::DirEntry;
use crate::filesystem;

const FNV_OFFSET: u64 = 0xcbf29ce484222325;
const FNV_PRIME: u64 = 0x100000001b3;

pub fn random_seed() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|duration| duration.as_secs() ^ u64::from(duration.subsec_nanos()))
        .unwrap_or_default()
}

pub fn sort_entries(entries: &mut [DirEntry], options: &SortOptions) {
    entries.sort_unstable_by(|a, b| compare_entries(a, b, options));
    if options.reverse {
        entries.reverse();
    }
}

fn compare_entries(a: &DirEntry, b: &DirEntry, options: &SortOptions) -> Ordering {
    if options.dirs_first || options.files_first {
        let a_group = grouping_value(a, options);
        let b_group = grouping_value(b, options);
        let ordering = a_group.cmp(&b_group);
        if ordering != Ordering::Equal {
            return ordering;
        }
    }

    for field in &options.fields {
        let ordering = match field {
            SortField::Path => compare_text(
                Some(os_str_bytes(a.path().as_os_str())),
                Some(os_str_bytes(b.path().as_os_str())),
                options,
            ),
            SortField::Name => compare_optional_text(
                a.path().file_name().map(os_str_bytes),
                b.path().file_name().map(os_str_bytes),
                options,
            ),
            SortField::Extension => compare_optional_text(
                a.path().extension().map(os_str_bytes),
                b.path().extension().map(os_str_bytes),
                options,
            ),
            SortField::Size => {
                compare_optional(regular_file_size(a), regular_file_size(b), options)
            }
            SortField::Modified => compare_optional(
                a.metadata().and_then(|metadata| metadata.modified().ok()),
                b.metadata().and_then(|metadata| metadata.modified().ok()),
                options,
            ),
            SortField::Created => compare_optional(
                a.metadata().and_then(|metadata| metadata.created().ok()),
                b.metadata().and_then(|metadata| metadata.created().ok()),
                options,
            ),
            SortField::Accessed => compare_optional(
                a.metadata().and_then(|metadata| metadata.accessed().ok()),
                b.metadata().and_then(|metadata| metadata.accessed().ok()),
                options,
            ),
            SortField::Depth => compare_optional(a.depth(), b.depth(), options),
            SortField::Type => entry_type(a).cmp(&entry_type(b)),
            SortField::NameLength => compare_optional(
                a.path()
                    .file_name()
                    .map(os_str_bytes)
                    .map(|bytes| bytes.len()),
                b.path()
                    .file_name()
                    .map(os_str_bytes)
                    .map(|bytes| bytes.len()),
                options,
            ),
            SortField::PathLength => a
                .path()
                .as_os_str()
                .as_encoded_bytes()
                .len()
                .cmp(&b.path().as_os_str().as_encoded_bytes().len()),
            SortField::Random => random_value(a, options.seed).cmp(&random_value(b, options.seed)),
        };

        if ordering != Ordering::Equal {
            return ordering;
        }
    }

    a.path().cmp(b.path())
}

fn grouping_value(entry: &DirEntry, options: &SortOptions) -> u8 {
    let is_dir = !entry.is_symlink()
        && entry
            .file_type()
            .is_some_and(|file_type| file_type.is_dir());
    let is_file = !entry.is_symlink()
        && entry
            .file_type()
            .is_some_and(|file_type| file_type.is_file());

    if options.dirs_first {
        u8::from(!is_dir)
    } else {
        u8::from(!is_file)
    }
}

fn regular_file_size(entry: &DirEntry) -> Option<u64> {
    if !entry.is_symlink()
        && entry
            .file_type()
            .is_some_and(|file_type| file_type.is_file())
    {
        entry.metadata().map(std::fs::Metadata::len)
    } else {
        None
    }
}

fn entry_type(entry: &DirEntry) -> u8 {
    match entry.file_type() {
        _ if entry.is_symlink() => 1,
        Some(file_type) if file_type.is_dir() => 0,
        Some(file_type) if file_type.is_file() => 2,
        _ => 3,
    }
}

fn compare_optional<T: Ord>(a: Option<T>, b: Option<T>, options: &SortOptions) -> Ordering {
    match (a, b) {
        (None, None) => Ordering::Equal,
        (None, Some(_)) => {
            if options.missing_last {
                Ordering::Greater
            } else {
                Ordering::Less
            }
        }
        (Some(_), None) => {
            if options.missing_last {
                Ordering::Less
            } else {
                Ordering::Greater
            }
        }
        (Some(a), Some(b)) => a.cmp(&b),
    }
}

fn compare_optional_text(
    a: Option<Cow<'_, [u8]>>,
    b: Option<Cow<'_, [u8]>>,
    options: &SortOptions,
) -> Ordering {
    match (a, b) {
        (None, None) => Ordering::Equal,
        (None, Some(_)) => {
            if options.missing_last {
                Ordering::Greater
            } else {
                Ordering::Less
            }
        }
        (Some(_), None) => {
            if options.missing_last {
                Ordering::Less
            } else {
                Ordering::Greater
            }
        }
        (Some(a), Some(b)) => compare_text(Some(a), Some(b), options),
    }
}

fn compare_text(
    a: Option<Cow<'_, [u8]>>,
    b: Option<Cow<'_, [u8]>>,
    options: &SortOptions,
) -> Ordering {
    match (a, b) {
        (Some(a), Some(b)) if options.natural => compare_natural(&a, &b, options.case_sensitive),
        (Some(a), Some(b)) => compare_folded(&a, &b, options.case_sensitive),
        _ => Ordering::Equal,
    }
}

fn compare_folded(a: &[u8], b: &[u8], case_sensitive: bool) -> Ordering {
    if case_sensitive {
        return a.cmp(b);
    }

    a.iter()
        .map(u8::to_ascii_lowercase)
        .cmp(b.iter().map(u8::to_ascii_lowercase))
}

fn compare_natural(a: &[u8], b: &[u8], case_sensitive: bool) -> Ordering {
    let mut a_index = 0;
    let mut b_index = 0;

    while a_index < a.len() && b_index < b.len() {
        let a_is_digit = a[a_index].is_ascii_digit();
        let b_is_digit = b[b_index].is_ascii_digit();

        if a_is_digit && b_is_digit {
            let a_end = digit_run_end(a, a_index);
            let b_end = digit_run_end(b, b_index);
            let ordering = compare_digit_runs(&a[a_index..a_end], &b[b_index..b_end]);
            if ordering != Ordering::Equal {
                return ordering;
            }
            a_index = a_end;
            b_index = b_end;
        } else {
            let a_byte = if case_sensitive {
                a[a_index]
            } else {
                a[a_index].to_ascii_lowercase()
            };
            let b_byte = if case_sensitive {
                b[b_index]
            } else {
                b[b_index].to_ascii_lowercase()
            };
            if a_byte != b_byte {
                return a_byte.cmp(&b_byte);
            }
            a_index += 1;
            b_index += 1;
        }
    }

    a_index.cmp(&a.len()).then_with(|| b_index.cmp(&b.len()))
}

fn digit_run_end(bytes: &[u8], start: usize) -> usize {
    bytes[start..]
        .iter()
        .position(|byte| !byte.is_ascii_digit())
        .map_or(bytes.len(), |offset| start + offset)
}

fn compare_digit_runs(a: &[u8], b: &[u8]) -> Ordering {
    let a_trimmed = a
        .iter()
        .position(|byte| *byte != b'0')
        .map_or(&a[a.len()..], |index| &a[index..]);
    let b_trimmed = b
        .iter()
        .position(|byte| *byte != b'0')
        .map_or(&b[b.len()..], |index| &b[index..]);

    a_trimmed
        .len()
        .cmp(&b_trimmed.len())
        .then_with(|| a_trimmed.cmp(b_trimmed))
}

fn os_str_bytes(path: &std::ffi::OsStr) -> Cow<'_, [u8]> {
    filesystem::osstr_to_bytes(path)
}

fn random_value(entry: &DirEntry, seed: u64) -> u64 {
    entry
        .path()
        .as_os_str()
        .as_encoded_bytes()
        .iter()
        .fold(FNV_OFFSET ^ seed, |hash, byte| {
            (hash ^ u64::from(*byte)).wrapping_mul(FNV_PRIME)
        })
}
