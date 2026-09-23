mod testenv;

#[cfg(unix)]
use nix::unistd::{Gid, Group, Uid, User};
use std::fs;
use std::io::Write;
use std::path::Path;
use std::time::{Duration, SystemTime};
use test_case::test_case;

use jiff::Timestamp;
use normpath::PathExt;
use regex::escape;

use crate::testenv::TestEnv;

static DEFAULT_DIRS: &[&str] = &["one/two/three", "one/two/three/directory_foo"];

static DEFAULT_FILES: &[&str] = &[
    "a.foo",
    "one/b.foo",
    "one/two/c.foo",
    "one/two/C.Foo2",
    "one/two/three/d.foo",
    "fdignored.foo",
    "gitignored.foo",
    ".hidden.foo",
    "e1 e2",
];

#[allow(clippy::let_and_return)]
fn get_absolute_root_path(env: &TestEnv) -> String {
    let path = env
        .test_root()
        .normalize()
        .expect("absolute path")
        .as_path()
        .to_str()
        .expect("string")
        .to_string();

    #[cfg(windows)]
    let path = path.trim_start_matches(r"\\?\").to_string();

    path
}

#[cfg(test)]
fn get_test_env_with_abs_path(dirs: &[&'static str], files: &[&'static str]) -> (TestEnv, String) {
    let env = TestEnv::new(dirs, files);
    let root_path = get_absolute_root_path(&env);
    (env, root_path)
}

#[cfg(test)]
fn create_file_with_size<P: AsRef<Path>>(path: P, size_in_bytes: usize) {
    let content = "#".repeat(size_in_bytes);
    let mut f = fs::File::create::<P>(path).unwrap();
    f.write_all(content.as_bytes()).unwrap();
}

/// Simple test
#[test]
fn test_simple() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    te.assert_output(&["a.foo"], "a.foo");
    te.assert_output(&["b.foo"], "one/b.foo");
    te.assert_output(&["d.foo"], "one/two/three/d.foo");

    te.assert_output(
        &["foo"],
        "a.foo
        one/b.foo
        one/two/c.foo
        one/two/C.Foo2
        one/two/three/d.foo
        one/two/three/directory_foo/",
    );
}

static AND_EXTRA_FILES: &[&str] = &[
    "a.foo",
    "one/b.foo",
    "one/two/c.foo",
    "one/two/C.Foo2",
    "one/two/three/baz-quux",
    "one/two/three/Baz-Quux2",
    "one/two/three/d.foo",
    "fdignored.foo",
    "gitignored.foo",
    ".hidden.foo",
    "A-B.jpg",
    "A-C.png",
    "B-A.png",
    "B-C.png",
    "C-A.jpg",
    "C-B.png",
    "e1 e2",
];

/// AND test
#[test]
fn test_and_basic() {
    let te = TestEnv::new(DEFAULT_DIRS, AND_EXTRA_FILES);

    te.assert_output(
        &["foo", "--and", "c"],
        "one/two/C.Foo2
        one/two/c.foo
        one/two/three/directory_foo/",
    );

    te.assert_output(
        &["f", "--and", "[ad]", "--and", "[_]"],
        "one/two/three/directory_foo/",
    );

    te.assert_output(
        &["f", "--and", "[ad]", "--and", "[.]"],
        "a.foo
        one/two/three/d.foo",
    );

    te.assert_output(&["Foo", "--and", "C"], "one/two/C.Foo2");

    te.assert_output(&["foo", "--and", "asdasdasdsadasd"], "");
}

#[test]
fn test_and_empty_pattern() {
    let te = TestEnv::new(DEFAULT_DIRS, AND_EXTRA_FILES);
    te.assert_output(&["Foo", "--and", "2", "--and", ""], "one/two/C.Foo2");
}

#[test]
fn test_and_bad_pattern() {
    let te = TestEnv::new(DEFAULT_DIRS, AND_EXTRA_FILES);

    te.assert_failure(&["Foo", "--and", "2", "--and", "[", "--and", "C"]);
    te.assert_failure(&["Foo", "--and", "[", "--and", "2", "--and", "C"]);
    te.assert_failure(&["Foo", "--and", "2", "--and", "C", "--and", "["]);
    te.assert_failure(&["[", "--and", "2", "--and", "C", "--and", "Foo"]);
}

#[test]
fn test_and_pattern_starts_with_dash() {
    let te = TestEnv::new(DEFAULT_DIRS, AND_EXTRA_FILES);

    te.assert_output(
        &["baz", "--and", "quux"],
        "one/two/three/Baz-Quux2
        one/two/three/baz-quux",
    );
    te.assert_output(
        &["baz", "--and", "-"],
        "one/two/three/Baz-Quux2
        one/two/three/baz-quux",
    );
    te.assert_output(
        &["Quu", "--and", "x", "--and", "-"],
        "one/two/three/Baz-Quux2",
    );
}

#[test]
fn test_and_plus_extension() {
    let te = TestEnv::new(DEFAULT_DIRS, AND_EXTRA_FILES);

    te.assert_output(
        &[
            "A",
            "--and",
            "B",
            "--extension",
            "jpg",
            "--extension",
            "png",
        ],
        "A-B.jpg
        B-A.png",
    );

    te.assert_output(
        &[
            "A",
            "--extension",
            "jpg",
            "--and",
            "B",
            "--extension",
            "png",
        ],
        "A-B.jpg
        B-A.png",
    );
}

#[test]
fn test_and_plus_type() {
    let te = TestEnv::new(DEFAULT_DIRS, AND_EXTRA_FILES);

    te.assert_output(
        &["c", "--type", "d", "--and", "foo"],
        "one/two/three/directory_foo/",
    );

    te.assert_output(
        &["c", "--type", "f", "--and", "foo"],
        "one/two/C.Foo2
        one/two/c.foo",
    );
}

#[test]
fn test_and_plus_glob() {
    let te = TestEnv::new(DEFAULT_DIRS, AND_EXTRA_FILES);

    te.assert_output(&["*foo", "--glob", "--and", "c*"], "one/two/c.foo");
}

#[test]
fn test_and_plus_fixed_strings() {
    let te = TestEnv::new(DEFAULT_DIRS, AND_EXTRA_FILES);

    te.assert_output(
        &["foo", "--fixed-strings", "--and", "c", "--and", "."],
        "one/two/c.foo
        one/two/C.Foo2",
    );

    te.assert_output(
        &["foo", "--fixed-strings", "--and", "[c]", "--and", "."],
        "",
    );

    te.assert_output(
        &["Foo", "--fixed-strings", "--and", "C", "--and", "."],
        "one/two/C.Foo2",
    );
}

#[test]
fn test_and_plus_ignore_case() {
    let te = TestEnv::new(DEFAULT_DIRS, AND_EXTRA_FILES);

    te.assert_output(
        &["Foo", "--ignore-case", "--and", "C", "--and", "[.]"],
        "one/two/C.Foo2
        one/two/c.foo",
    );
}

#[test]
fn test_and_plus_case_sensitive() {
    let te = TestEnv::new(DEFAULT_DIRS, AND_EXTRA_FILES);

    te.assert_output(
        &["foo", "--case-sensitive", "--and", "c", "--and", "[.]"],
        "one/two/c.foo",
    );
}

#[test]
fn test_and_plus_full_path() {
    let te = TestEnv::new(DEFAULT_DIRS, AND_EXTRA_FILES);

    te.assert_output(
        &[
            "three",
            "--full-path",
            "--and",
            "_foo",
            "--and",
            r"[/\\]dir",
        ],
        "one/two/three/directory_foo/",
    );

    te.assert_output(
        &[
            "three",
            "--full-path",
            "--and",
            r"[/\\]two",
            "--and",
            r"[/\\]dir",
        ],
        "one/two/three/directory_foo/",
    );
}

/// Test each pattern type with an empty pattern.
#[test]
fn test_empty_pattern() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);
    let expected = "a.foo
    e1 e2
    one/
    one/b.foo
    one/two/
    one/two/c.foo
    one/two/C.Foo2
    one/two/three/
    one/two/three/d.foo
    one/two/three/directory_foo/
    symlink";

    te.assert_output(&["--regex"], expected);
    te.assert_output(&["--fixed-strings"], expected);
    te.assert_output(&["--glob"], expected);
}

/// Test multiple directory searches
#[test]
fn test_multi_file() {
    let dirs = &["test1", "test2"];
    let files = &["test1/a.foo", "test1/b.foo", "test2/a.foo"];
    let te = TestEnv::new(dirs, files);
    te.assert_output(
        &["a.foo", "test1", "test2"],
        "test1/a.foo
        test2/a.foo",
    );

    te.assert_output(
        &["", "test1", "test2"],
        "test1/a.foo
        test2/a.foo
        test1/b.foo",
    );

    te.assert_output(&["a.foo", "test1"], "test1/a.foo");

    te.assert_output(&["b.foo", "test1", "test2"], "test1/b.foo");
}

/// Test search over multiple directory with missing
#[test]
fn test_multi_file_with_missing() {
    let dirs = &["real"];
    let files = &["real/a.foo", "real/b.foo"];
    let te = TestEnv::new(dirs, files);
    te.assert_output(&["a.foo", "real", "fake"], "real/a.foo");

    te.assert_error(
        &["a.foo", "real", "fake"],
        "[fd error]: Search path 'fake' is not a directory.",
    );

    te.assert_output(
        &["", "real", "fake"],
        "real/a.foo
        real/b.foo",
    );

    te.assert_output(
        &["", "real", "fake1", "fake2"],
        "real/a.foo
        real/b.foo",
    );

    te.assert_error(
        &["", "real", "fake1", "fake2"],
        "[fd error]: Search path 'fake1' is not a directory.
        [fd error]: Search path 'fake2' is not a directory.",
    );

    te.assert_failure_with_error(
        &["", "fake1", "fake2"],
        "[fd error]: Search path 'fake1' is not a directory.
        [fd error]: Search path 'fake2' is not a directory.
        [fd error]: No valid search paths given.",
    );
}

/// Explicit root path
#[test]
fn test_explicit_root_path() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    te.assert_output(
        &["foo", "one"],
        "one/b.foo
        one/two/c.foo
        one/two/C.Foo2
        one/two/three/d.foo
        one/two/three/directory_foo/",
    );

    te.assert_output(
        &["foo", "one/two/three"],
        "one/two/three/d.foo
        one/two/three/directory_foo/",
    );

    te.assert_output_subdirectory(
        "one/two/",
        &["foo", "../../"],
        "../../a.foo
        ../../one/b.foo
        ../../one/two/c.foo
        ../../one/two/C.Foo2
        ../../one/two/three/d.foo
        ../../one/two/three/directory_foo/",
    );

    te.assert_output_subdirectory(
        "one/two/three",
        &["", ".."],
        "../c.foo
        ../C.Foo2
        ../three/
        ../three/d.foo
        ../three/directory_foo/",
    );
}

/// Regex searches
#[test]
fn test_regex_searches() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    te.assert_output(
        &["[a-c].foo"],
        "a.foo
        one/b.foo
        one/two/c.foo
        one/two/C.Foo2",
    );

    te.assert_output(
        &["--case-sensitive", "[a-c].foo"],
        "a.foo
        one/b.foo
        one/two/c.foo",
    );
}

/// Smart case
#[test]
fn test_smart_case() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    te.assert_output(
        &["c.foo"],
        "one/two/c.foo
        one/two/C.Foo2",
    );

    te.assert_output(&["C.Foo"], "one/two/C.Foo2");

    te.assert_output(&["Foo"], "one/two/C.Foo2");

    // Only literal uppercase chars should trigger case sensitivity.
    te.assert_output(
        &["\\Ac"],
        "one/two/c.foo
        one/two/C.Foo2",
    );
    te.assert_output(&["\\AC"], "one/two/C.Foo2");
}

/// Case sensitivity (--case-sensitive)
#[test]
fn test_case_sensitive() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    te.assert_output(&["--case-sensitive", "c.foo"], "one/two/c.foo");

    te.assert_output(&["--case-sensitive", "C.Foo"], "one/two/C.Foo2");

    te.assert_output(
        &["--ignore-case", "--case-sensitive", "C.Foo"],
        "one/two/C.Foo2",
    );
}

/// Case insensitivity (--ignore-case)
#[test]
fn test_case_insensitive() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    te.assert_output(
        &["--ignore-case", "C.Foo"],
        "one/two/c.foo
        one/two/C.Foo2",
    );

    te.assert_output(
        &["--case-sensitive", "--ignore-case", "C.Foo"],
        "one/two/c.foo
        one/two/C.Foo2",
    );
}

/// Glob-based searches (--glob)
#[test]
fn test_glob_searches() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    te.assert_output(
        &["--glob", "*.foo"],
        "a.foo
        one/b.foo
        one/two/c.foo
        one/two/three/d.foo",
    );

    te.assert_output(
        &["--glob", "[a-c].foo"],
        "a.foo
        one/b.foo
        one/two/c.foo",
    );

    te.assert_output(
        &["--glob", "[a-c].foo*"],
        "a.foo
        one/b.foo
        one/two/C.Foo2
        one/two/c.foo",
    );
}

/// Glob-based searches (--glob) in combination with full path searches (--full-path)
#[cfg(not(windows))] // TODO: make this work on Windows
#[test]
fn test_full_path_glob_searches() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    te.assert_output(
        &["--glob", "--full-path", "**/one/**/*.foo"],
        "one/b.foo
        one/two/c.foo
        one/two/three/d.foo",
    );

    te.assert_output(
        &["--glob", "--full-path", "**/one/*/*.foo"],
        " one/two/c.foo",
    );

    te.assert_output(
        &["--glob", "--full-path", "**/one/*/*/*.foo"],
        " one/two/three/d.foo",
    );
}

#[test]
fn test_smart_case_glob_searches() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    te.assert_output(
        &["--glob", "c.foo*"],
        "one/two/C.Foo2
        one/two/c.foo",
    );

    te.assert_output(&["--glob", "C.Foo*"], "one/two/C.Foo2");
}

/// Glob-based searches (--glob) in combination with --case-sensitive
#[test]
fn test_case_sensitive_glob_searches() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    te.assert_output(&["--glob", "--case-sensitive", "c.foo*"], "one/two/c.foo");
}

/// Glob-based searches (--glob) in combination with --extension
#[test]
fn test_glob_searches_with_extension() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    te.assert_output(
        &["--glob", "--extension", "foo2", "[a-z].*"],
        "one/two/C.Foo2",
    );
}

/// Make sure that --regex overrides --glob
#[test]
fn test_regex_overrides_glob() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    te.assert_output(&["--glob", "--regex", "Foo2$"], "one/two/C.Foo2");
}

/// Full path search (--full-path)
#[test]
fn test_full_path() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    let root = te.system_root();
    let prefix = escape(&root.to_string_lossy());

    te.assert_output(
        &["--full-path", &format!("^{prefix}.*three.*foo$")],
        "one/two/three/d.foo
        one/two/three/directory_foo/",
    );
}

/// Hidden files (--hidden)
#[test]
fn test_hidden() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    te.assert_output(
        &["--hidden", "foo"],
        ".hidden.foo
        a.foo
        one/b.foo
        one/two/c.foo
        one/two/C.Foo2
        one/two/three/d.foo
        one/two/three/directory_foo/",
    );
}

/// Hidden file attribute on Windows
#[cfg(windows)]
#[test]
fn test_hidden_file_attribute() {
    use std::os::windows::fs::OpenOptionsExt;

    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    // https://docs.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-setfileattributesa
    const FILE_ATTRIBUTE_HIDDEN: u32 = 2;

    fs::OpenOptions::new()
        .create(true)
        .write(true)
        .attributes(FILE_ATTRIBUTE_HIDDEN)
        .open(te.test_root().join("hidden-file.txt"))
        .unwrap();

    te.assert_output(&["--hidden", "hidden-file.txt"], "hidden-file.txt");
    te.assert_output(&["hidden-file.txt"], "");
}

/// Ignored files (--no-ignore)
#[test]
fn test_no_ignore() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    te.assert_output(
        &["--no-ignore", "foo"],
        "a.foo
        fdignored.foo
        gitignored.foo
        one/b.foo
        one/two/c.foo
        one/two/C.Foo2
        one/two/three/d.foo
        one/two/three/directory_foo/",
    );

    te.assert_output(
        &["--hidden", "--no-ignore", "foo"],
        ".hidden.foo
        a.foo
        fdignored.foo
        gitignored.foo
        one/b.foo
        one/two/c.foo
        one/two/C.Foo2
        one/two/three/d.foo
        one/two/three/directory_foo/",
    );
}

/// .gitignore and .fdignore
#[test]
fn test_gitignore_and_fdignore() {
    let files = &[
        "ignored-by-nothing",
        "ignored-by-fdignore",
        "ignored-by-gitignore",
        "ignored-by-both",
    ];
    let te = TestEnv::new(&[], files);

    fs::File::create(te.test_root().join(".fdignore"))
        .unwrap()
        .write_all(b"ignored-by-fdignore\nignored-by-both")
        .unwrap();

    fs::File::create(te.test_root().join(".gitignore"))
        .unwrap()
        .write_all(b"ignored-by-gitignore\nignored-by-both")
        .unwrap();

    te.assert_output(&["ignored"], "ignored-by-nothing");

    te.assert_output(
        &["--no-ignore-vcs", "ignored"],
        "ignored-by-nothing
        ignored-by-gitignore",
    );

    te.assert_output(
        &["--no-ignore", "ignored"],
        "ignored-by-nothing
        ignored-by-fdignore
        ignored-by-gitignore
        ignored-by-both",
    );
}

/// Ignore parent ignore files (--no-ignore-parent)
#[test]
fn test_no_ignore_parent() {
    let dirs = &["inner"];
    let files = &[
        "inner/parent-ignored",
        "inner/child-ignored",
        "inner/not-ignored",
    ];
    let te = TestEnv::new(dirs, files);

    // Ignore 'parent-ignored' in root
    fs::File::create(te.test_root().join(".gitignore"))
        .unwrap()
        .write_all(b"parent-ignored")
        .unwrap();
    // Ignore 'child-ignored' in inner
    fs::File::create(te.test_root().join("inner/.gitignore"))
        .unwrap()
        .write_all(b"child-ignored")
        .unwrap();

    te.assert_output_subdirectory("inner", &[], "not-ignored");

    te.assert_output_subdirectory(
        "inner",
        &["--no-ignore-parent"],
        "parent-ignored
        not-ignored",
    );
}

/// Ignore parent ignore files (--no-ignore-parent) with an inner git repo
#[test]
fn test_no_ignore_parent_inner_git() {
    let dirs = &["inner"];
    let files = &[
        "inner/parent-ignored",
        "inner/child-ignored",
        "inner/not-ignored",
    ];
    let te = TestEnv::new(dirs, files);

    // Make the inner folder also appear as a git repo
    fs::create_dir_all(te.test_root().join("inner/.git")).unwrap();

    // Ignore 'parent-ignored' in root
    fs::File::create(te.test_root().join(".gitignore"))
        .unwrap()
        .write_all(b"parent-ignored")
        .unwrap();
    // Ignore 'child-ignored' in inner
    fs::File::create(te.test_root().join("inner/.gitignore"))
        .unwrap()
        .write_all(b"child-ignored")
        .unwrap();

    te.assert_output_subdirectory(
        "inner",
        &[],
        "not-ignored
        parent-ignored",
    );

    te.assert_output_subdirectory(
        "inner",
        &["--no-ignore-parent"],
        "not-ignored
        parent-ignored",
    );
}

/// Precedence of .fdignore files
#[test]
fn test_custom_ignore_precedence() {
    let dirs = &["inner"];
    let files = &["inner/foo"];
    let te = TestEnv::new(dirs, files);

    // Ignore 'foo' via .gitignore
    fs::File::create(te.test_root().join("inner/.gitignore"))
        .unwrap()
        .write_all(b"foo")
        .unwrap();

    // Whitelist 'foo' via .fdignore
    fs::File::create(te.test_root().join(".fdignore"))
        .unwrap()
        .write_all(b"!foo")
        .unwrap();

    te.assert_output(&["foo"], "inner/foo");

    te.assert_output(&["--no-ignore-vcs", "foo"], "inner/foo");

    te.assert_output(&["--no-ignore", "foo"], "inner/foo");
}

/// Don't require git to respect gitignore (--no-require-git)
#[test]
fn test_respect_ignore_files() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    // Not in a git repo anymore
    fs::remove_dir(te.test_root().join(".git")).unwrap();

    // don't respect gitignore because we're not in a git repo
    te.assert_output(
        &["foo"],
        "a.foo
        gitignored.foo
        one/b.foo
        one/two/c.foo
        one/two/C.Foo2
        one/two/three/d.foo
        one/two/three/directory_foo/",
    );

    // respect gitignore because we set `--no-require-git`
    te.assert_output(
        &["--no-require-git", "foo"],
        "a.foo
        one/b.foo
        one/two/c.foo
        one/two/C.Foo2
        one/two/three/d.foo
        one/two/three/directory_foo/",
    );

    // make sure overriding works
    te.assert_output(
        &["--no-require-git", "--require-git", "foo"],
        "a.foo
        gitignored.foo
        one/b.foo
        one/two/c.foo
        one/two/C.Foo2
        one/two/three/d.foo
        one/two/three/directory_foo/",
    );

    te.assert_output(
        &["--no-require-git", "--no-ignore", "foo"],
        "a.foo
        gitignored.foo
        fdignored.foo
        one/b.foo
        one/two/c.foo
        one/two/C.Foo2
        one/two/three/d.foo
        one/two/three/directory_foo/",
    );
}

/// VCS ignored files (--no-ignore-vcs)
#[test]
fn test_no_ignore_vcs() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    te.assert_output(
        &["--no-ignore-vcs", "foo"],
        "a.foo
        gitignored.foo
        one/b.foo
        one/two/c.foo
        one/two/C.Foo2
        one/two/three/d.foo
        one/two/three/directory_foo/",
    );
}

/// Test that --no-ignore-vcs still respects .fdignored in parent directory
#[test]
fn test_no_ignore_vcs_child_dir() {
    let te = TestEnv::new(
        &["inner"],
        &["inner/fdignored.foo", "inner/foo", "inner/gitignored.foo"],
    );

    te.assert_output_subdirectory(
        "inner",
        &["--no-ignore-vcs", "foo"],
        "foo
        gitignored.foo",
    );
}

/// Custom ignore files (--ignore-file)
#[test]
fn test_custom_ignore_files() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    // Ignore 'C.Foo2' and everything in 'three'.
    fs::File::create(te.test_root().join("custom.ignore"))
        .unwrap()
        .write_all(b"C.Foo2\nthree")
        .unwrap();

    te.assert_output(
        &["--ignore-file", "custom.ignore", "foo"],
        "a.foo
        one/b.foo
        one/two/c.foo",
    );
}

/// Ignored files with ripgrep aliases (-u / -uu)
#[test]
fn test_no_ignore_aliases() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    te.assert_output(
        &["-u", "foo"],
        ".hidden.foo
        a.foo
        fdignored.foo
        gitignored.foo
        one/b.foo
        one/two/c.foo
        one/two/C.Foo2
        one/two/three/d.foo
        one/two/three/directory_foo/",
    );
}

#[cfg(not(windows))]
#[test]
fn test_global_ignore() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES).global_ignore_file("one");
    te.assert_output(
        &[],
        "a.foo
    e1 e2
    symlink",
    );
}

#[cfg(not(windows))]
#[test_case("--unrestricted", ".hidden.foo
a.foo
fdignored.foo
gitignored.foo
one/b.foo
one/two/c.foo
one/two/C.Foo2
one/two/three/d.foo
one/two/three/directory_foo/"; "unrestricted")]
#[test_case("--no-ignore", "a.foo
fdignored.foo
gitignored.foo
one/b.foo
one/two/c.foo
one/two/C.Foo2
one/two/three/d.foo
one/two/three/directory_foo/"; "no-ignore")]
#[test_case("--no-global-ignore-file", "a.foo
one/b.foo
one/two/c.foo
one/two/C.Foo2
one/two/three/d.foo
one/two/three/directory_foo/"; "no-global-ignore-file")]
fn test_no_global_ignore(flag: &str, expected_output: &str) {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES).global_ignore_file("one");
    te.assert_output(&[flag, "foo"], expected_output);
}

/// Symlinks (--follow)
#[test]
fn test_follow() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    te.assert_output(
        &["--follow", "c.foo"],
        "one/two/c.foo
        one/two/C.Foo2
        symlink/c.foo
        symlink/C.Foo2",
    );
}

// File system boundaries (--one-file-system)
// Limited to Unix because, to the best of my knowledge, there is no easy way to test a use case
// file systems mounted into the tree on Windows.
// Not limiting depth causes massive delay under Darwin, see BurntSushi/ripgrep#1429
#[test]
#[cfg(unix)]
fn test_file_system_boundaries() {
    // Helper function to get the device ID for a given path
    // Inspired by https://github.com/BurntSushi/ripgrep/blob/8892bf648cfec111e6e7ddd9f30e932b0371db68/ignore/src/walk.rs#L1693
    fn device_num(path: impl AsRef<Path>) -> u64 {
        use std::os::unix::fs::MetadataExt;

        path.as_ref().metadata().map(|md| md.dev()).unwrap()
    }

    // Can't simulate file system boundaries
    let te = TestEnv::new(&[], &[]);

    let dev_null = Path::new("/dev/null");

    // /dev/null should exist in all sane Unixes. Skip if it doesn't exist for some reason.
    // Also skip should it be on the same device as the root partition for some reason.
    if !dev_null.is_file() || device_num(dev_null) == device_num("/") {
        return;
    }

    te.assert_output(
        &["--full-path", "--max-depth", "2", "^/dev/null$", "/"],
        "/dev/null",
    );
    te.assert_output(
        &[
            "--one-file-system",
            "--full-path",
            "--max-depth",
            "2",
            "^/dev/null$",
            "/",
        ],
        "",
    );
}

#[test]
fn test_follow_broken_symlink() {
    let mut te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);
    te.create_broken_symlink("broken_symlink")
        .expect("Failed to create broken symlink.");

    te.assert_output(
        &["symlink"],
        "broken_symlink
        symlink",
    );
    te.assert_output(
        &["--type", "symlink", "symlink"],
        "broken_symlink
        symlink",
    );

    te.assert_output(&["--type", "file", "symlink"], "");

    te.assert_output(
        &["--follow", "--type", "symlink", "symlink"],
        "broken_symlink",
    );
    te.assert_output(&["--follow", "--type", "file", "symlink"], "");
}

/// Null separator (--print0)
#[test]
fn test_print0() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    te.assert_output(
        &["--print0", "foo"],
        "./a.fooNULL
        ./one/b.fooNULL
        ./one/two/C.Foo2NULL
        ./one/two/c.fooNULL
        ./one/two/three/d.fooNULL
        ./one/two/three/directory_foo/NULL",
    );
}

/// Maximum depth (--max-depth)
#[test]
fn test_max_depth() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    te.assert_output(
        &["--max-depth", "3"],
        "a.foo
        e1 e2
        one/
        one/b.foo
        one/two/
        one/two/c.foo
        one/two/C.Foo2
        one/two/three/
        symlink",
    );

    te.assert_output(
        &["--max-depth", "2"],
        "a.foo
        e1 e2
        one/
        one/b.foo
        one/two/
        symlink",
    );

    te.assert_output(
        &["--max-depth", "1"],
        "a.foo
        e1 e2
        one/
        symlink",
    );
}

/// Minimum depth (--min-depth)
#[test]
fn test_min_depth() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    te.assert_output(
        &["--min-depth", "3"],
        "one/two/c.foo
        one/two/C.Foo2
        one/two/three/
        one/two/three/d.foo
        one/two/three/directory_foo/",
    );

    te.assert_output(
        &["--min-depth", "4"],
        "one/two/three/d.foo
        one/two/three/directory_foo/",
    );
}

/// Exact depth (--exact-depth)
#[test]
fn test_exact_depth() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    te.assert_output(
        &["--exact-depth", "3"],
        "one/two/c.foo
        one/two/C.Foo2
        one/two/three/",
    );
}

/// Pruning (--prune)
#[test]
fn test_prune() {
    let dirs = &["foo/bar", "bar/foo", "baz"];
    let files = &[
        "foo/foo.file",
        "foo/bar/foo.file",
        "bar/foo.file",
        "bar/foo/foo.file",
        "baz/foo.file",
    ];

    let te = TestEnv::new(dirs, files);

    te.assert_output(
        &["foo"],
        "foo/
        foo/foo.file
        foo/bar/foo.file
        bar/foo.file
        bar/foo/
        bar/foo/foo.file
        baz/foo.file",
    );

    te.assert_output(
        &["--prune", "foo"],
        "foo/
        bar/foo/
        bar/foo.file
        baz/foo.file",
    );
}

/// Absolute paths (--absolute-path)
#[test]
fn test_absolute_path() {
    let (te, abs_path) = get_test_env_with_abs_path(DEFAULT_DIRS, DEFAULT_FILES);

    te.assert_output(
        &["--absolute-path"],
        &format!(
            "{abs_path}/a.foo
            {abs_path}/e1 e2
            {abs_path}/one/
            {abs_path}/one/b.foo
            {abs_path}/one/two/
            {abs_path}/one/two/c.foo
            {abs_path}/one/two/C.Foo2
            {abs_path}/one/two/three/
            {abs_path}/one/two/three/d.foo
            {abs_path}/one/two/three/directory_foo/
            {abs_path}/symlink",
            abs_path = &abs_path
        ),
    );

    te.assert_output(
        &["--absolute-path", "foo"],
        &format!(
            "{abs_path}/a.foo
            {abs_path}/one/b.foo
            {abs_path}/one/two/c.foo
            {abs_path}/one/two/C.Foo2
            {abs_path}/one/two/three/d.foo
            {abs_path}/one/two/three/directory_foo/",
            abs_path = &abs_path
        ),
    );
}

/// Show absolute paths if the path argument is absolute
#[test]
fn test_implicit_absolute_path() {
    let (te, abs_path) = get_test_env_with_abs_path(DEFAULT_DIRS, DEFAULT_FILES);

    te.assert_output(
        &["foo", &abs_path],
        &format!(
            "{abs_path}/a.foo
            {abs_path}/one/b.foo
            {abs_path}/one/two/c.foo
            {abs_path}/one/two/C.Foo2
            {abs_path}/one/two/three/d.foo
            {abs_path}/one/two/three/directory_foo/",
            abs_path = &abs_path
        ),
    );
}

/// Absolute paths should be normalized
#[test]
fn test_normalized_absolute_path() {
    let (te, abs_path) = get_test_env_with_abs_path(DEFAULT_DIRS, DEFAULT_FILES);

    te.assert_output_subdirectory(
        "one",
        &["--absolute-path", "foo", ".."],
        &format!(
            "{abs_path}/a.foo
            {abs_path}/one/b.foo
            {abs_path}/one/two/c.foo
            {abs_path}/one/two/C.Foo2
            {abs_path}/one/two/three/d.foo
            {abs_path}/one/two/three/directory_foo/",
            abs_path = &abs_path
        ),
    );
}

/// File type filter (--type)
#[test]
fn test_type() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    te.assert_output(
        &["--type", "f"],
        "a.foo
        e1 e2
        one/b.foo
        one/two/c.foo
        one/two/C.Foo2
        one/two/three/d.foo",
    );

    te.assert_output(&["--type", "f", "e1"], "e1 e2");

    te.assert_output(
        &["--type", "d"],
        "one/
        one/two/
        one/two/three/
        one/two/three/directory_foo/",
    );

    te.assert_output(
        &["--type", "d", "--type", "l"],
        "one/
        one/two/
        one/two/three/
        one/two/three/directory_foo/
        symlink",
    );

    te.assert_output(&["--type", "l"], "symlink");
}

/// Test `--type executable`
#[cfg(unix)]
#[test]
fn test_type_executable() {
    use std::os::unix::fs::OpenOptionsExt;

    // This test assumes the current user isn't root
    // (otherwise if the executable bit is set for any level, it is executable for the current
    // user)
    if Uid::current().is_root() {
        return;
    }

    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    fs::OpenOptions::new()
        .create_new(true)
        .truncate(true)
        .write(true)
        .mode(0o777)
        .open(te.test_root().join("executable-file.sh"))
        .unwrap();

    fs::OpenOptions::new()
        .create(true)
        .truncate(true)
        .write(true)
        .mode(0o645)
        .open(te.test_root().join("not-user-executable-file.sh"))
        .unwrap();

    te.assert_output(&["--type", "executable"], "executable-file.sh");

    te.assert_output(
        &["--type", "executable", "--type", "directory"],
        "executable-file.sh
        one/
        one/two/
        one/two/three/
        one/two/three/directory_foo/",
    );
}

/// Test `--type empty`
#[test]
fn test_type_empty() {
    let te = TestEnv::new(&["dir_empty", "dir_nonempty"], &[]);

    create_file_with_size(te.test_root().join("0_bytes.foo"), 0);
    create_file_with_size(te.test_root().join("5_bytes.foo"), 5);

    create_file_with_size(te.test_root().join("dir_nonempty").join("2_bytes.foo"), 2);

    te.assert_output(
        &["--type", "empty"],
        "0_bytes.foo
        dir_empty/",
    );

    te.assert_output(
        &["--type", "empty", "--type", "file", "--type", "directory"],
        "0_bytes.foo
        dir_empty/",
    );

    te.assert_output(&["--type", "empty", "--type", "file"], "0_bytes.foo");

    te.assert_output(&["--type", "empty", "--type", "directory"], "dir_empty/");
}

/// File extension (--extension)
#[test]
fn test_extension() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    te.assert_output(
        &["--extension", "foo"],
        "a.foo
        one/b.foo
        one/two/c.foo
        one/two/three/d.foo",
    );

    te.assert_output(
        &["--extension", ".foo"],
        "a.foo
        one/b.foo
        one/two/c.foo
        one/two/three/d.foo",
    );

    te.assert_output(
        &["--extension", ".foo", "--extension", "foo2"],
        "a.foo
        one/b.foo
        one/two/c.foo
        one/two/three/d.foo
        one/two/C.Foo2",
    );

    te.assert_output(&["--extension", ".foo", "a"], "a.foo");

    te.assert_output(&["--extension", "foo2"], "one/two/C.Foo2");

    let te2 = TestEnv::new(&[], &["spam.bar.baz", "egg.bar.baz", "yolk.bar.baz.sig"]);

    te2.assert_output(
        &["--extension", ".bar.baz"],
        "spam.bar.baz
        egg.bar.baz",
    );

    te2.assert_output(&["--extension", "sig"], "yolk.bar.baz.sig");

    te2.assert_output(&["--extension", "bar.baz.sig"], "yolk.bar.baz.sig");

    let te3 = TestEnv::new(&[], &["latin1.e\u{301}xt", "smiley.☻"]);

    te3.assert_output(&["--extension", "☻"], "smiley.☻");

    te3.assert_output(&["--extension", ".e\u{301}xt"], "latin1.e\u{301}xt");

    let te4 = TestEnv::new(&[], &[".hidden", "test.hidden"]);

    te4.assert_output(&["--hidden", "--extension", ".hidden"], "test.hidden");
}

/// No file extension (test for the pattern provided in the --help text)
#[test]
fn test_no_extension() {
    let te = TestEnv::new(
        DEFAULT_DIRS,
        &["a.foo", "aa", "one/b.foo", "one/bb", "one/two/three/d"],
    );

    te.assert_output(
        &["^[^.]+$"],
        "aa
        one/
        one/bb
        one/two/
        one/two/three/
        one/two/three/d
        one/two/three/directory_foo/
        symlink",
    );

    te.assert_output(
        &["^[^.]+$", "--type", "file"],
        "aa
        one/bb
        one/two/three/d",
    );
}

/// Symlink as search directory
#[test]
fn test_symlink_as_root() {
    let mut te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);
    te.create_broken_symlink("broken_symlink")
        .expect("Failed to create broken symlink.");

    // From: http://pubs.opengroup.org/onlinepubs/9699919799/functions/getcwd.html
    // The getcwd() function shall place an absolute pathname of the current working directory in
    // the array pointed to by buf, and return buf. The pathname shall contain no components that
    // are dot or dot-dot, or are symbolic links.
    //
    // Key points:
    // 1. The path of the current working directory of a Unix process cannot contain symlinks.
    // 2. The path of the current working directory of a Windows process can contain symlinks.
    //
    // More:
    // 1. On Windows, symlinks are resolved after the ".." component.
    // 2. On Unix, symlinks are resolved immediately as encountered.

    let parent_parent = if cfg!(windows) { ".." } else { "../.." };
    te.assert_output_subdirectory(
        "symlink",
        &["", parent_parent],
        &format!(
            "{dir}/a.foo
            {dir}/broken_symlink
            {dir}/e1 e2
            {dir}/one/
            {dir}/one/b.foo
            {dir}/one/two/
            {dir}/one/two/c.foo
            {dir}/one/two/C.Foo2
            {dir}/one/two/three/
            {dir}/one/two/three/d.foo
            {dir}/one/two/three/directory_foo/
            {dir}/symlink",
            dir = &parent_parent
        ),
    );
}

#[test]
fn test_symlink_and_absolute_path() {
    let (te, abs_path) = get_test_env_with_abs_path(DEFAULT_DIRS, DEFAULT_FILES);

    let expected_path = if cfg!(windows) { "symlink" } else { "one/two" };

    te.assert_output_subdirectory(
        "symlink",
        &["--absolute-path"],
        &format!(
            "{abs_path}/{expected_path}/c.foo
            {abs_path}/{expected_path}/C.Foo2
            {abs_path}/{expected_path}/three/
            {abs_path}/{expected_path}/three/d.foo
            {abs_path}/{expected_path}/three/directory_foo/",
            abs_path = &abs_path,
            expected_path = expected_path
        ),
    );
}

#[test]
fn test_symlink_as_absolute_root() {
    let (te, abs_path) = get_test_env_with_abs_path(DEFAULT_DIRS, DEFAULT_FILES);

    te.assert_output(
        &["", &format!("{abs_path}/symlink")],
        &format!(
            "{abs_path}/symlink/c.foo
            {abs_path}/symlink/C.Foo2
            {abs_path}/symlink/three/
            {abs_path}/symlink/three/d.foo
            {abs_path}/symlink/three/directory_foo/",
            abs_path = &abs_path
        ),
    );
}

#[test]
fn test_symlink_and_full_path() {
    let (te, abs_path) = get_test_env_with_abs_path(DEFAULT_DIRS, DEFAULT_FILES);
    let root = te.system_root();
    let prefix = escape(&root.to_string_lossy());

    let expected_path = if cfg!(windows) { "symlink" } else { "one/two" };

    te.assert_output_subdirectory(
        "symlink",
        &[
            "--absolute-path",
            "--full-path",
            &format!("^{prefix}.*three"),
        ],
        &format!(
            "{abs_path}/{expected_path}/three/
            {abs_path}/{expected_path}/three/d.foo
            {abs_path}/{expected_path}/three/directory_foo/",
            abs_path = &abs_path,
            expected_path = expected_path
        ),
    );
}

#[test]
fn test_symlink_and_full_path_abs_path() {
    let (te, abs_path) = get_test_env_with_abs_path(DEFAULT_DIRS, DEFAULT_FILES);
    let root = te.system_root();
    let prefix = escape(&root.to_string_lossy());
    te.assert_output(
        &[
            "--full-path",
            &format!("^{prefix}.*symlink.*three"),
            &format!("{abs_path}/symlink"),
        ],
        &format!(
            "{abs_path}/symlink/three/
            {abs_path}/symlink/three/d.foo
            {abs_path}/symlink/three/directory_foo/",
            abs_path = &abs_path
        ),
    );
}
/// Exclude patterns (--exclude)
#[test]
fn test_excludes() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    te.assert_output(
        &["--exclude", "*.foo"],
        "one/
        one/two/
        one/two/C.Foo2
        one/two/three/
        one/two/three/directory_foo/
        e1 e2
        symlink",
    );

    te.assert_output(
        &["--exclude", "*.foo", "--exclude", "*.Foo2"],
        "one/
        one/two/
        one/two/three/
        one/two/three/directory_foo/
        e1 e2
        symlink",
    );

    te.assert_output(
        &["--exclude", "*.foo", "--exclude", "*.Foo2", "foo"],
        "one/two/three/directory_foo/",
    );

    te.assert_output(
        &["--exclude", "one/two/", "foo"],
        "a.foo
        one/b.foo",
    );

    te.assert_output(
        &["--exclude", "one/**/*.foo"],
        "a.foo
        e1 e2
        one/
        one/two/
        one/two/C.Foo2
        one/two/three/
        one/two/three/directory_foo/
        symlink",
    );
}

#[test]
fn format() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    te.assert_output(
        &["--format", "path={}", "--path-separator=/"],
        "path=a.foo
        path=e1 e2
        path=one
        path=one/b.foo
        path=one/two
        path=one/two/C.Foo2
        path=one/two/c.foo
        path=one/two/three
        path=one/two/three/d.foo
        path=one/two/three/directory_foo
        path=symlink",
    );

    te.assert_output(
        &["foo", "--format", "noExt={.}", "--path-separator=/"],
        "noExt=a
        noExt=one/b
        noExt=one/two/C
        noExt=one/two/c
        noExt=one/two/three/d
        noExt=one/two/three/directory_foo",
    );

    te.assert_output(
        &["foo", "--format", "basename={/}", "--path-separator=/"],
        "basename=a.foo
        basename=b.foo
        basename=C.Foo2
        basename=c.foo
        basename=d.foo
        basename=directory_foo",
    );

    te.assert_output(
        &["foo", "--format", "name={/.}", "--path-separator=/"],
        "name=a
        name=b
        name=C
        name=c
        name=d
        name=directory_foo",
    );

    te.assert_output(
        &["foo", "--format", "parent={//}", "--path-separator=/"],
        "parent=.
        parent=one
        parent=one/two
        parent=one/two
        parent=one/two/three
        parent=one/two/three",
    );
}

/// Shell script execution (--exec)
#[test]
fn test_exec() {
    let (te, abs_path) = get_test_env_with_abs_path(DEFAULT_DIRS, DEFAULT_FILES);
    // TODO Windows tests: D:file.txt \file.txt \\server\share\file.txt ...
    if !cfg!(windows) {
        te.assert_output(
            &["--absolute-path", "foo", "--exec", "echo"],
            &format!(
                "{abs_path}/a.foo
                {abs_path}/one/b.foo
                {abs_path}/one/two/C.Foo2
                {abs_path}/one/two/c.foo
                {abs_path}/one/two/three/d.foo
                {abs_path}/one/two/three/directory_foo",
                abs_path = &abs_path
            ),
        );

        te.assert_output(
            &["foo", "--exec", "echo", "{}"],
            "./a.foo
            ./one/b.foo
            ./one/two/C.Foo2
            ./one/two/c.foo
            ./one/two/three/d.foo
            ./one/two/three/directory_foo",
        );

        te.assert_output(
            &["foo", "--strip-cwd-prefix", "--exec", "echo", "{}"],
            "a.foo
            one/b.foo
            one/two/C.Foo2
            one/two/c.foo
            one/two/three/d.foo
            one/two/three/directory_foo",
        );

        te.assert_output(
            &["foo", "--exec", "echo", "{.}"],
            "a
            one/b
            one/two/C
            one/two/c
            one/two/three/d
            one/two/three/directory_foo",
        );

        te.assert_output(
            &["foo", "--exec", "echo", "{/}"],
            "a.foo
            b.foo
            C.Foo2
            c.foo
            d.foo
            directory_foo",
        );

        te.assert_output(
            &["foo", "--exec", "echo", "{/.}"],
            "a
            b
            C
            c
            d
            directory_foo",
        );

        te.assert_output(
            &["foo", "--exec", "echo", "{//}"],
            ".
            ./one
            ./one/two
            ./one/two
            ./one/two/three
            ./one/two/three",
        );

        te.assert_output(&["e1", "--exec", "printf", "%s.%s\n"], "./e1 e2.");
    }
}

// TODO test for windows
#[cfg(not(windows))]
#[test]
fn test_exec_multi() {
    let (te, abs_path) = get_test_env_with_abs_path(DEFAULT_DIRS, DEFAULT_FILES);

    te.assert_output(
        &[
            "--absolute-path",
            "foo",
            "--exec",
            "echo",
            ";",
            "--exec",
            "echo",
            "test",
            "{/}",
        ],
        &format!(
            "{abs_path}/a.foo
                {abs_path}/one/b.foo
                {abs_path}/one/two/C.Foo2
                {abs_path}/one/two/c.foo
                {abs_path}/one/two/three/d.foo
                {abs_path}/one/two/three/directory_foo
                test a.foo
                test b.foo
                test C.Foo2
                test c.foo
                test d.foo
                test directory_foo",
            abs_path = &abs_path
        ),
    );

    te.assert_output(
        &[
            "e1", "--exec", "echo", "{.}", ";", "--exec", "echo", "{/}", ";", "--exec", "echo",
            "{//}", ";", "--exec", "echo", "{/.}",
        ],
        "e1 e2
        e1 e2
        .
        e1 e2",
    );

    // We use printf here because we need to suppress a newline and
    // echo -n is not POSIX-compliant.
    te.assert_output(
        &[
            "foo", "--exec", "printf", "%s", "{/}: ", ";", "--exec", "printf", "%s\\n", "{//}",
        ],
        "a.foo: .
        b.foo: ./one
        C.Foo2: ./one/two
        c.foo: ./one/two
        d.foo: ./one/two/three
        directory_foo: ./one/two/three",
    );
}

#[cfg(not(windows))]
#[test]
fn test_exec_nulls() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);
    te.assert_output(
        &["foo", "--print0", "--exec", "printf", "p=%s"],
        "p=./a.fooNULL
        p=./one/b.fooNULL
        p=./one/two/C.Foo2NULL
        p=./one/two/c.fooNULL
        p=./one/two/three/d.fooNULL
        p=./one/two/three/directory_fooNULL",
    );
}

#[test]
fn test_exec_batch() {
    let (te, abs_path) = get_test_env_with_abs_path(DEFAULT_DIRS, DEFAULT_FILES);
    let te = te.normalize_line(true);

    // TODO Test for windows
    if !cfg!(windows) {
        te.assert_output(
            &["--absolute-path", "foo", "--exec-batch", "echo"],
            &format!(
                "{abs_path}/a.foo {abs_path}/one/b.foo {abs_path}/one/two/C.Foo2 {abs_path}/one/two/c.foo {abs_path}/one/two/three/d.foo {abs_path}/one/two/three/directory_foo",
                abs_path = &abs_path
            ),
        );

        te.assert_output(
            &["foo", "--exec-batch", "echo", "{}"],
            "./a.foo ./one/b.foo ./one/two/C.Foo2 ./one/two/c.foo ./one/two/three/d.foo ./one/two/three/directory_foo",
        );

        te.assert_output(
            &["foo", "--strip-cwd-prefix", "--exec-batch", "echo", "{}"],
            "a.foo one/b.foo one/two/C.Foo2 one/two/c.foo one/two/three/d.foo one/two/three/directory_foo",
        );

        te.assert_output(
            &["foo", "--exec-batch", "echo", "{/}"],
            "a.foo b.foo C.Foo2 c.foo d.foo directory_foo",
        );

        te.assert_output(
            &["no_match", "--exec-batch", "echo", "Matched: ", "{/}"],
            "",
        );

        te.assert_failure_with_error(
            &["foo", "--exec-batch", "echo", "{}", "{}"],
            "error: Only one placeholder allowed for batch commands\n\
            \n\
            Usage: fd [OPTIONS] [pattern] [path]...\n\
            \n\
            For more information, try '--help'.\n\
            ",
        );

        te.assert_failure_with_error(
            &["foo", "--exec-batch", "echo", "{/}", ";", "-x", "echo"],
            "error: the argument '--exec-batch <cmd>...' cannot be used with '--exec <cmd>...'\n\
            \n\
            Usage: fd --exec-batch <cmd>... <pattern> [path]...\n\
            \n\
            For more information, try '--help'.\n\
            ",
        );

        te.assert_failure_with_error(
            &["foo", "--exec-batch"],
            "error: a value is required for '--exec-batch <cmd>...' but none was supplied\n\
            \n\
            For more information, try '--help'.\n\
            ",
        );

        te.assert_failure_with_error(
            &["foo", "--exec-batch", "echo {}"],
            "error: First argument of exec-batch is expected to be a fixed executable\n\
            \n\
            Usage: fd [OPTIONS] [pattern] [path]...\n\
            \n\
            For more information, try '--help'.\n\
            ",
        );

        te.assert_failure_with_error(&["a.foo", "--exec-batch", "bash", "-c", "exit 1"], "");
    }
}

#[test]
fn test_exec_batch_multi() {
    // TODO test for windows
    if cfg!(windows) {
        return;
    }
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    let output = te.assert_success_and_get_output(
        ".",
        &[
            "foo",
            "--exec-batch",
            "echo",
            "{}",
            ";",
            "--exec-batch",
            "echo",
            "{/}",
        ],
    );
    let stdout = std::str::from_utf8(&output.stdout).unwrap();
    let lines: Vec<_> = stdout
        .lines()
        .map(|l| {
            let mut words: Vec<_> = l.split_whitespace().collect();
            words.sort_unstable();
            words
        })
        .collect();

    assert_eq!(
        lines,
        &[
            [
                "./a.foo",
                "./one/b.foo",
                "./one/two/C.Foo2",
                "./one/two/c.foo",
                "./one/two/three/d.foo",
                "./one/two/three/directory_foo"
            ],
            [
                "C.Foo2",
                "a.foo",
                "b.foo",
                "c.foo",
                "d.foo",
                "directory_foo"
            ],
        ]
    );

    te.assert_failure_with_error(
        &[
            "a.foo",
            "--exec-batch",
            "echo",
            ";",
            "--exec-batch",
            "bash",
            "-c",
            "exit 1",
        ],
        "",
    );
}

#[test]
fn test_exec_batch_with_limit() {
    // TODO Test for windows
    if cfg!(windows) {
        return;
    }

    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    let output = te.assert_success_and_get_output(
        ".",
        &["foo", "--batch-size=2", "--exec-batch", "echo", "{}"],
    );
    let stdout = String::from_utf8_lossy(&output.stdout);

    for line in stdout.lines() {
        assert_eq!(2, line.split_whitespace().count());
    }

    let mut paths: Vec<_> = stdout
        .lines()
        .flat_map(|line| line.split_whitespace())
        .collect();
    paths.sort_unstable();
    assert_eq!(
        &paths,
        &[
            "./a.foo",
            "./one/b.foo",
            "./one/two/C.Foo2",
            "./one/two/c.foo",
            "./one/two/three/d.foo",
            "./one/two/three/directory_foo"
        ],
    );
}

/// Shell script execution (--exec) with a custom --path-separator
#[test]
fn test_exec_with_separator() {
    let (te, abs_path) = get_test_env_with_abs_path(DEFAULT_DIRS, DEFAULT_FILES);
    te.assert_output(
        &[
            "--path-separator=#",
            "--absolute-path",
            "foo",
            "--exec",
            "echo",
        ],
        &format!(
            "{abs_path}#a.foo
                {abs_path}#one#b.foo
                {abs_path}#one#two#C.Foo2
                {abs_path}#one#two#c.foo
                {abs_path}#one#two#three#d.foo
                {abs_path}#one#two#three#directory_foo",
            abs_path = abs_path.replace(std::path::MAIN_SEPARATOR, "#"),
        ),
    );

    te.assert_output(
        &["--path-separator=#", "foo", "--exec", "echo", "{}"],
        ".#a.foo
            .#one#b.foo
            .#one#two#C.Foo2
            .#one#two#c.foo
            .#one#two#three#d.foo
            .#one#two#three#directory_foo",
    );

    te.assert_output(
        &["--path-separator=#", "foo", "--exec", "echo", "{.}"],
        "a
            one#b
            one#two#C
            one#two#c
            one#two#three#d
            one#two#three#directory_foo",
    );

    te.assert_output(
        &["--path-separator=#", "foo", "--exec", "echo", "{/}"],
        "a.foo
            b.foo
            C.Foo2
            c.foo
            d.foo
            directory_foo",
    );

    te.assert_output(
        &["--path-separator=#", "foo", "--exec", "echo", "{/.}"],
        "a
            b
            C
            c
            d
            directory_foo",
    );

    te.assert_output(
        &["--path-separator=#", "foo", "--exec", "echo", "{//}"],
        ".
            .#one
            .#one#two
            .#one#two
            .#one#two#three
            .#one#two#three",
    );

    te.assert_output(
        &["--path-separator=#", "e1", "--exec", "printf", "%s.%s\n"],
        ".#e1 e2.",
    );
}

/// Non-zero exit code (--quiet)
#[test]
fn test_quiet() {
    let dirs = &[];
    let files = &["a.foo", "b.foo"];
    let te = TestEnv::new(dirs, files);

    te.assert_output(&["-q"], "");
    te.assert_output(&["--quiet"], "");
    te.assert_output(&["--has-results"], "");
    te.assert_failure_with_error(&["--quiet", "c.foo"], "")
}

/// Literal search (--fixed-strings)
#[test]
fn test_fixed_strings() {
    let dirs = &["test1", "test2"];
    let files = &["test1/a.foo", "test1/a_foo", "test2/Download (1).tar.gz"];
    let te = TestEnv::new(dirs, files);

    // Regex search, dot is treated as "any character"
    te.assert_output(
        &["a.foo"],
        "test1/a.foo
         test1/a_foo",
    );

    // Literal search, dot is treated as character
    te.assert_output(&["--fixed-strings", "a.foo"], "test1/a.foo");

    // Regex search, parens are treated as group
    te.assert_output(&["download (1)"], "");

    // Literal search, parens are treated as characters
    te.assert_output(
        &["--fixed-strings", "download (1)"],
        "test2/Download (1).tar.gz",
    );

    // Combine with --case-sensitive
    te.assert_output(&["--fixed-strings", "--case-sensitive", "download (1)"], "");
}

/// Filenames with invalid UTF-8 sequences
#[cfg(target_os = "linux")]
#[test]
fn test_invalid_utf8() {
    use std::ffi::OsStr;
    use std::os::unix::ffi::OsStrExt;

    let dirs = &["test1"];
    let files = &[];
    let te = TestEnv::new(dirs, files);

    fs::File::create(
        te.test_root()
            .join(OsStr::from_bytes(b"test1/test_\xFEinvalid.txt")),
    )
    .unwrap();

    te.assert_output(&["", "test1/"], "test1/test_�invalid.txt");

    te.assert_output(&["invalid", "test1/"], "test1/test_�invalid.txt");

    // Should not be found under a different extension
    te.assert_output(&["-e", "zip", "", "test1/"], "");
}

/// Filtering for file size (--size)
#[test]
fn test_size() {
    let te = TestEnv::new(&[], &[]);

    create_file_with_size(te.test_root().join("0_bytes.foo"), 0);
    create_file_with_size(te.test_root().join("11_bytes.foo"), 11);
    create_file_with_size(te.test_root().join("30_bytes.foo"), 30);
    create_file_with_size(te.test_root().join("3_kilobytes.foo"), 3 * 1000);
    create_file_with_size(te.test_root().join("4_kibibytes.foo"), 4 * 1024);

    // Zero and non-zero sized files.
    te.assert_output(
        &["", "--size", "+0B"],
        "0_bytes.foo
        11_bytes.foo
        30_bytes.foo
        3_kilobytes.foo
        4_kibibytes.foo",
    );

    // Zero sized files.
    te.assert_output(&["", "--size", "-0B"], "0_bytes.foo");
    te.assert_output(&["", "--size", "0B"], "0_bytes.foo");
    te.assert_output(&["", "--size=0B"], "0_bytes.foo");
    te.assert_output(&["", "-S", "0B"], "0_bytes.foo");

    // Files with 2 bytes or more.
    te.assert_output(
        &["", "--size", "+2B"],
        "11_bytes.foo
        30_bytes.foo
        3_kilobytes.foo
        4_kibibytes.foo",
    );

    // Files with 2 bytes or less.
    te.assert_output(&["", "--size", "-2B"], "0_bytes.foo");

    // Files with size between 1 byte and 11 bytes.
    te.assert_output(&["", "--size", "+1B", "--size", "-11B"], "11_bytes.foo");

    // Files with size equal 11 bytes.
    te.assert_output(&["", "--size", "11B"], "11_bytes.foo");

    // Files with size between 1 byte and 30 bytes.
    te.assert_output(
        &["", "--size", "+1B", "--size", "-30B"],
        "11_bytes.foo
        30_bytes.foo",
    );

    // Combine with a search pattern
    te.assert_output(&["^11_", "--size", "+1B", "--size", "-30B"], "11_bytes.foo");

    // Files with size between 12 and 30 bytes.
    te.assert_output(&["", "--size", "+12B", "--size", "-30B"], "30_bytes.foo");

    // Files with size between 31 and 100 bytes.
    te.assert_output(&["", "--size", "+31B", "--size", "-100B"], "");

    // Files with size between 3 kibibytes and 5 kibibytes.
    te.assert_output(&["", "--size", "+3ki", "--size", "-5ki"], "4_kibibytes.foo");

    // Files with size between 3 kilobytes and 5 kilobytes.
    te.assert_output(
        &["", "--size", "+3k", "--size", "-5k"],
        "3_kilobytes.foo
        4_kibibytes.foo",
    );

    // Files with size greater than 3 kilobytes and less than 3 kibibytes.
    te.assert_output(&["", "--size", "+3k", "--size", "-3ki"], "3_kilobytes.foo");

    // Files with size equal 4 kibibytes.
    te.assert_output(&["", "--size", "+4ki", "--size", "-4ki"], "4_kibibytes.foo");
    te.assert_output(&["", "--size", "4ki"], "4_kibibytes.foo");
}

#[cfg(test)]
fn create_file_with_modified<P: AsRef<Path>>(path: P, duration_in_secs: u64) {
    let st = SystemTime::now() - Duration::from_secs(duration_in_secs);
    let ft = filetime::FileTime::from_system_time(st);
    fs::File::create(&path).expect("creation failed");
    filetime::set_file_times(&path, ft, ft).expect("time modification failed");
}

#[cfg(test)]
fn remove_symlink<P: AsRef<Path>>(path: P) {
    #[cfg(unix)]
    fs::remove_file(path).expect("remove symlink");

    // On Windows, symlinks remember whether they point to files or directories, so try both
    #[cfg(windows)]
    fs::remove_file(path.as_ref())
        .or_else(|_| fs::remove_dir(path.as_ref()))
        .expect("remove symlink");
}

#[test]
fn test_modified_relative() {
    let te = TestEnv::new(&[], &[]);
    remove_symlink(te.test_root().join("symlink"));
    create_file_with_modified(te.test_root().join("foo_0_now"), 0);
    create_file_with_modified(te.test_root().join("bar_1_min"), 60);
    create_file_with_modified(te.test_root().join("foo_10_min"), 600);
    create_file_with_modified(te.test_root().join("bar_1_h"), 60 * 60);
    create_file_with_modified(te.test_root().join("foo_2_h"), 2 * 60 * 60);
    create_file_with_modified(te.test_root().join("bar_1_day"), 24 * 60 * 60);

    te.assert_output(
        &["", "--changed-within", "15min"],
        "foo_0_now
        bar_1_min
        foo_10_min",
    );

    te.assert_output(
        &["", "--change-older-than", "15min"],
        "bar_1_h
        foo_2_h
        bar_1_day",
    );

    te.assert_output(
        &["foo", "--changed-within", "12h"],
        "foo_0_now
        foo_10_min
        foo_2_h",
    );
}

#[cfg(test)]
fn change_file_modified<P: AsRef<Path>>(path: P, iso_date: &str) {
    let st = iso_date
        .parse::<Timestamp>()
        .map(SystemTime::from)
        .expect("invalid date");
    let ft = filetime::FileTime::from_system_time(st);
    filetime::set_file_times(path, ft, ft).expect("time modification failde");
}

#[test]
fn test_modified_absolute() {
    let te = TestEnv::new(&[], &["15mar2018", "30dec2017"]);
    remove_symlink(te.test_root().join("symlink"));
    change_file_modified(te.test_root().join("15mar2018"), "2018-03-15T12:00:00Z");
    change_file_modified(te.test_root().join("30dec2017"), "2017-12-30T23:59:00Z");

    te.assert_output(
        &["", "--change-newer-than", "2018-01-01 00:00:00"],
        "15mar2018",
    );
    te.assert_output(
        &["", "--changed-before", "2018-01-01 00:00:00"],
        "30dec2017",
    );
}

#[cfg(unix)]
#[test]
fn test_owner_ignore_all() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);
    te.assert_output(&["--owner", ":", "a.foo"], "a.foo");
    te.assert_output(&["--owner", "", "a.foo"], "a.foo");
}

#[cfg(unix)]
#[test]
fn test_owner_current_user() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);
    let uid = Uid::current();
    te.assert_output(&["--owner", &uid.to_string(), "a.foo"], "a.foo");
    if let Ok(Some(user)) = User::from_uid(uid) {
        te.assert_output(&["--owner", &user.name, "a.foo"], "a.foo");
    }
}

#[cfg(unix)]
#[test]
fn test_owner_current_group() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);
    let gid = Gid::current();
    te.assert_output(&["--owner", &format!(":{gid}"), "a.foo"], "a.foo");
    if let Ok(Some(group)) = Group::from_gid(gid) {
        te.assert_output(&["--owner", &format!(":{}", group.name), "a.foo"], "a.foo");
    }
}

#[cfg(target_os = "linux")]
#[test]
fn test_owner_root() {
    // This test assumes the current user isn't root
    if Uid::current().is_root() || Gid::current() == Gid::from_raw(0) {
        return;
    }
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);
    te.assert_output(&["--owner", "root", "a.foo"], "");
    te.assert_output(&["--owner", "0", "a.foo"], "");
    te.assert_output(&["--owner", ":root", "a.foo"], "");
    te.assert_output(&["--owner", ":0", "a.foo"], "");
}

#[test]
fn test_custom_path_separator() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    te.assert_output(
        &["foo", "one", "--path-separator", "="],
        "one=b.foo
        one=two=c.foo
        one=two=C.Foo2
        one=two=three=d.foo
        one=two=three=directory_foo=",
    );
}

#[test]
fn test_base_directory() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    te.assert_output(
        &["--base-directory", "one"],
        "b.foo
        two/
        two/c.foo
        two/C.Foo2
        two/three/
        two/three/d.foo
        two/three/directory_foo/",
    );

    te.assert_output(
        &["--base-directory", "one/two/", "foo"],
        "c.foo
        C.Foo2
        three/d.foo
        three/directory_foo/",
    );

    // Explicit root path
    te.assert_output(
        &["--base-directory", "one", "foo", "two"],
        "two/c.foo
        two/C.Foo2
        two/three/d.foo
        two/three/directory_foo/",
    );

    // Ignore base directory when absolute path is used
    let (te, abs_path) = get_test_env_with_abs_path(DEFAULT_DIRS, DEFAULT_FILES);
    let abs_base_dir = &format!("{abs_path}/one/two/", abs_path = &abs_path);
    te.assert_output(
        &["--base-directory", abs_base_dir, "foo", &abs_path],
        &format!(
            "{abs_path}/a.foo
            {abs_path}/one/b.foo
            {abs_path}/one/two/c.foo
            {abs_path}/one/two/C.Foo2
            {abs_path}/one/two/three/d.foo
            {abs_path}/one/two/three/directory_foo/",
            abs_path = &abs_path
        ),
    );
}

#[test]
fn test_max_results() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    // Unrestricted
    te.assert_output(
        &["--max-results=0", "c.foo"],
        "one/two/C.Foo2
         one/two/c.foo",
    );

    // Limited to two results
    te.assert_output(
        &["--max-results=2", "c.foo"],
        "one/two/C.Foo2
         one/two/c.foo",
    );

    // Limited to one result. We could find either C.Foo2 or c.foo
    let assert_just_one_result_with_option = |option| {
        let output = te.assert_success_and_get_output(".", &[option, "c.foo"]);
        let stdout = String::from_utf8_lossy(&output.stdout)
            .trim()
            .replace(&std::path::MAIN_SEPARATOR.to_string(), "/");
        assert!(stdout == "one/two/C.Foo2" || stdout == "one/two/c.foo");
    };
    assert_just_one_result_with_option("--max-results=1");
    assert_just_one_result_with_option("-1");

    // check that --max-results & -1 conflict with --exec
    te.assert_failure(&["thing", "--max-results=0", "--exec=cat"]);
    te.assert_failure(&["thing", "-1", "--exec=cat"]);
    te.assert_failure(&["thing", "--max-results=1", "-1", "--exec=cat"]);
}

/// Filenames with non-utf8 paths are passed to the executed program unchanged
///
/// Note:
/// - the test is disabled on Darwin/OSX, since it coerces file names to UTF-8,
///   even when the requested file name is not valid UTF-8.
/// - the test is currently disabled on Windows because I'm not sure how to create
///   invalid UTF-8 files on Windows
#[cfg(all(unix, not(target_os = "macos")))]
#[test]
fn test_exec_invalid_utf8() {
    use std::ffi::OsStr;
    use std::os::unix::ffi::OsStrExt;

    let dirs = &["test1"];
    let files = &[];
    let te = TestEnv::new(dirs, files);

    fs::File::create(
        te.test_root()
            .join(OsStr::from_bytes(b"test1/test_\xFEinvalid.txt")),
    )
    .unwrap();

    te.assert_output_raw(
        &["", "test1/", "--exec", "echo", "{}"],
        b"test1/test_\xFEinvalid.txt\n",
    );

    te.assert_output_raw(
        &["", "test1/", "--exec", "echo", "{/}"],
        b"test_\xFEinvalid.txt\n",
    );

    te.assert_output_raw(&["", "test1/", "--exec", "echo", "{//}"], b"test1\n");

    te.assert_output_raw(
        &["", "test1/", "--exec", "echo", "{.}"],
        b"test1/test_\xFEinvalid\n",
    );

    te.assert_output_raw(
        &["", "test1/", "--exec", "echo", "{/.}"],
        b"test_\xFEinvalid\n",
    );
}

#[test]
fn test_list_details() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    // Make sure we can execute 'fd --list-details' without any errors.
    te.assert_success_and_get_output(".", &["--list-details"]);
}

#[test]
fn test_single_and_multithreaded_execution() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    te.assert_output(&["--threads=1", "a.foo"], "a.foo");
    te.assert_output(&["--threads=16", "a.foo"], "a.foo");
}

/// Make sure that fd fails if numeric arguments can not be parsed
#[test]
fn test_number_parsing_errors() {
    let te = TestEnv::new(&[], &[]);

    te.assert_failure(&["--threads=a"]);
    te.assert_failure(&["-j", ""]);
    te.assert_failure(&["--threads=0"]);

    te.assert_failure(&["--min-depth=a"]);
    te.assert_failure(&["--mindepth=a"]);
    te.assert_failure(&["--max-depth=a"]);
    te.assert_failure(&["--maxdepth=a"]);
    te.assert_failure(&["--exact-depth=a"]);

    te.assert_failure(&["--max-buffer-time=a"]);

    te.assert_failure(&["--max-results=a"]);
}

#[test_case("--hidden", &["--no-hidden"] ; "hidden")]
#[test_case("--no-ignore", &["--ignore"] ; "no-ignore")]
#[test_case("--no-ignore-vcs", &["--ignore-vcs"] ; "no-ignore-vcs")]
#[test_case("--no-require-git", &["--require-git"] ; "no-require-git")]
#[test_case("--follow", &["--no-follow"] ; "follow")]
#[test_case("--absolute-path", &["--relative-path"] ; "absolute-path")]
#[test_case("-u", &["--ignore", "--no-hidden"] ; "u")]
#[test_case("-uu", &["--ignore", "--no-hidden"] ; "uu")]
fn test_opposing(flag: &str, opposing_flags: &[&str]) {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    let mut flags = vec![flag];
    flags.extend_from_slice(opposing_flags);
    let out_no_flags = te.assert_success_and_get_normalized_output(".", &[]);
    let out_opposing_flags = te.assert_success_and_get_normalized_output(".", &flags);

    assert_eq!(
        out_no_flags,
        out_opposing_flags,
        "{} should override {}",
        opposing_flags.join(" "),
        flag
    );
}

/// Print error if search pattern starts with a dot and --hidden is not set
/// (Unix only, hidden files on Windows work differently)
#[test]
#[cfg(unix)]
fn test_error_if_hidden_not_set_and_pattern_starts_with_dot() {
    let te = TestEnv::new(&[], &[".gitignore", ".whatever", "non-hidden"]);

    te.assert_failure(&["^\\.gitignore"]);
    te.assert_failure(&["--glob", ".gitignore"]);

    te.assert_output(&["--hidden", "^\\.gitignore"], ".gitignore");
    te.assert_output(&["--hidden", "--glob", ".gitignore"], ".gitignore");
    te.assert_output(&[".gitignore"], "");
}

#[test]
fn test_strip_cwd_prefix() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    te.assert_output(
        &["--strip-cwd-prefix", "."],
        "a.foo
        e1 e2
        one/
        one/b.foo
        one/two/
        one/two/c.foo
        one/two/C.Foo2
        one/two/three/
        one/two/three/d.foo
        one/two/three/directory_foo/
        symlink",
    );
}

/// When fd is ran from a non-existent working directory, but an existent
/// directory is passed in the arguments, it should still run fine
#[test]
#[cfg(all(not(windows), not(target_os = "illumos")))]
fn test_invalid_cwd() {
    let te = TestEnv::new(&[], &[]);

    let root = te.test_root().join("foo");
    fs::create_dir(&root).unwrap();
    std::env::set_current_dir(&root).unwrap();
    fs::remove_dir(&root).unwrap();

    let output = std::process::Command::new(te.test_exe())
        .arg("query")
        .arg(te.test_root())
        .output()
        .unwrap();

    if !output.status.success() {
        panic!("{output:?}");
    }
}

/// Test behavior of .git directory with various flags
#[test]
fn test_git_dir() {
    let te = TestEnv::new(
        &[".git/one", "other_dir/.git", "nested/dir/.git"],
        &[
            ".git/one/foo.a",
            ".git/.foo",
            ".git/a.foo",
            "other_dir/.git/foo1",
            "nested/dir/.git/foo2",
        ],
    );

    te.assert_output(
        &["--hidden", "foo"],
        ".git/one/foo.a
        .git/.foo
        .git/a.foo
        other_dir/.git/foo1
        nested/dir/.git/foo2",
    );
    te.assert_output(&["--no-ignore", "foo"], "");
    te.assert_output(
        &["--hidden", "--no-ignore", "foo"],
        ".git/one/foo.a
         .git/.foo
         .git/a.foo
         other_dir/.git/foo1
         nested/dir/.git/foo2",
    );
    te.assert_output(
        &["--hidden", "--no-ignore-vcs", "foo"],
        ".git/one/foo.a
         .git/.foo
         .git/a.foo
         other_dir/.git/foo1
         nested/dir/.git/foo2",
    );
}

#[test]
fn test_gitignore_parent() {
    let te = TestEnv::new(&["sub"], &[".abc", "sub/.abc"]);

    fs::File::create(te.test_root().join(".gitignore"))
        .unwrap()
        .write_all(b".abc\n")
        .unwrap();

    te.assert_output_subdirectory("sub", &["--hidden"], "");
    te.assert_output_subdirectory("sub", &["--hidden", "--search-path", "."], "");
}

#[test]
fn test_hyperlink() {
    let te = TestEnv::new(DEFAULT_DIRS, DEFAULT_FILES);

    #[cfg(unix)]
    let hostname = nix::unistd::gethostname().unwrap().into_string().unwrap();
    #[cfg(not(unix))]
    let hostname = "/";

    let expected = format!(
        "\x1b]8;;file://{}{}/a.foo\x1b\\a.foo\x1b]8;;\x1b\\",
        hostname,
        get_absolute_root_path(&te),
    );

    te.assert_output(&["--hyperlink=always", "a.foo"], &expected);
}

#[test]
fn test_ignore_contain() {
    let te = TestEnv::new(
        &["include", "exclude", "exclude/sub", "other"],
        &[
            "top",
            "include/foo",
            "exclude/CACHEDIR.TAG",
            "exclude/sub/nope",
            "other/ignoremyparent",
        ],
    );
    let expected = "include/
    include/foo
    symlink
    top";
    te.assert_output(
        &[
            "--ignore-contain=CACHEDIR.TAG",
            "--ignore-contain=ignoremyparent",
            ".",
        ],
        expected,
    );
}

#[test]
fn test_ignore_contain_precedence_over_depth_check() {
    let te = TestEnv::new(
        &["include", "exclude", "exclude/sub"],
        &[
            "top",
            "include/foo",
            "exclude/CACHEDIR.TAG",
            "exclude/sub/nope",
        ],
    );
    let expected = "include/foo";
    te.assert_output(
        &["--ignore-contain=CACHEDIR.TAG", "--min-depth=2", "."],
        expected,
    );
}

#[test]
fn test_ignore_contain_precedence_over_root_check() {
    let te = TestEnv::new(&["include"], &["CACHEDIR.TAG", "top", "include/foo"]);
    let expected = "";
    te.assert_output(&["--ignore-contain=CACHEDIR.TAG", "."], expected);
}

fn ordered_lines(te: &TestEnv, args: &[&str]) -> Vec<String> {
    let output = te.assert_success_and_get_output(".", args);
    String::from_utf8_lossy(&output.stdout)
        .replace(std::path::MAIN_SEPARATOR, "/")
        .lines()
        .map(ToString::to_string)
        .collect()
}

fn assert_ordered(te: &TestEnv, args: &[&str], expected: &[&str]) {
    let actual = ordered_lines(te, args);
    assert_eq!(
        actual,
        expected,
        "fd {} produced the wrong order",
        args.join(" ")
    );
}

#[test]
fn test_sort_help_and_rejected_flags() {
    let te = TestEnv::new(&[], &[]);
    let output = te.assert_success_and_get_output(".", &["--help"]);
    let help = String::from_utf8_lossy(&output.stdout);
    for needle in [
        "--sort <field>",
        "--reverse",
        "--dirs-first",
        "--files-first",
        "--sort-case-sensitive",
        "--sort-missing-last",
        "--sort-natural",
        "--sort-seed <n>",
        "name-length",
        "path-length",
    ] {
        assert!(help.contains(needle), "help is missing {needle}");
    }

    for args in [
        &["--reverse"][..],
        &["--dirs-first"],
        &["--files-first"],
        &["--sort-case-sensitive"],
        &["--sort-missing-last"],
        &["--sort-natural"],
        &["--sort-seed", "1"],
        &["--sort", "name", "--dirs-first", "--files-first"],
        &["--sort", "nope"],
        &["--sort", "name", "--exec", "echo"],
        &["--sort", "name", "--exec-batch", "echo"],
        &["--sort", "name", "--list-details"],
        &["--sort", "name", "--sort-seed", "-1"],
    ] {
        te.assert_failure(args);
    }
}

#[test]
fn test_sort_name_path_extensions_and_ties() {
    let te = TestEnv::new(
        &[
            "ord",
            "names/sub",
            "fold/a",
            "fold/b",
            "ext",
            "mk/z",
            "mk/a",
            "mk/m",
            "r1",
            "r2",
        ],
        &[
            "ord/b",
            "ord/a",
            "names/b",
            "names/a",
            "names/c",
            "names/sub/a",
            "fold/b/File",
            "fold/a/file",
            "ext/a.mp10",
            "ext/a.mp9",
            "ext/b.txt",
            "ext/c.TXT",
            "ext/noext",
            "mk/z/a",
            "mk/a/z",
            "mk/m/b",
            "r1/b.txt",
            "r1/a.txt",
            "r2/c.txt",
            "r2/a.txt",
            "other.txt",
        ],
    );
    create_file_with_size(te.test_root().join("mk/z/a"), 10);
    create_file_with_size(te.test_root().join("mk/a/z"), 10);
    create_file_with_size(te.test_root().join("mk/m/b"), 3);

    assert_ordered(
        &te,
        &[".", "names", "--sort", "name"],
        &["names/a", "names/sub/a", "names/b", "names/c", "names/sub/"],
    );
    assert_ordered(
        &te,
        &[".", "names", "--sort", "name", "--threads", "1"],
        &["names/a", "names/sub/a", "names/b", "names/c", "names/sub/"],
    );
    assert_ordered(
        &te,
        &[".", "names", "--sort", "name", "--threads", "8"],
        &["names/a", "names/sub/a", "names/b", "names/c", "names/sub/"],
    );

    // Case-insensitive names tie; the raw path orders `fold/a` before `fold/b`.
    assert_ordered(
        &te,
        &[".", "fold", "-t", "f", "--sort", "name"],
        &["fold/a/file", "fold/b/File"],
    );
    // Case-sensitive name order is `File` then `file`, so the directory no longer decides.
    assert_ordered(
        &te,
        &[
            ".",
            "fold",
            "-t",
            "f",
            "--sort",
            "name",
            "--sort-case-sensitive",
        ],
        &["fold/b/File", "fold/a/file"],
    );

    assert_ordered(
        &te,
        &[".", "ext", "--sort", "extension"],
        &[
            "ext/noext",
            "ext/a.mp10",
            "ext/a.mp9",
            "ext/b.txt",
            "ext/c.TXT",
        ],
    );
    assert_ordered(
        &te,
        &[
            ".",
            "ext",
            "--sort",
            "extension",
            "--sort-natural",
            "--sort-missing-last",
        ],
        &[
            "ext/a.mp9",
            "ext/a.mp10",
            "ext/b.txt",
            "ext/c.TXT",
            "ext/noext",
        ],
    );
    assert_ordered(
        &te,
        &[".", "ext", "--sort", "extension", "--sort-case-sensitive"],
        &[
            "ext/noext",
            "ext/c.TXT",
            "ext/a.mp10",
            "ext/a.mp9",
            "ext/b.txt",
        ],
    );

    assert_ordered(
        &te,
        &[".", "mk", "-t", "f", "--sort", "size"],
        &["mk/m/b", "mk/a/z", "mk/z/a"],
    );
    assert_ordered(
        &te,
        &[".", "mk", "-t", "f", "--sort", "size", "--sort", "name"],
        &["mk/m/b", "mk/z/a", "mk/a/z"],
    );

    assert_ordered(
        &te,
        &[".", "r1", "r2", "--sort", "name", "-t", "f"],
        &["r1/a.txt", "r2/a.txt", "r1/b.txt", "r2/c.txt"],
    );

    assert_ordered(
        &te,
        &[".", "ord", "--sort", "name", "--format", "{/}"],
        &["a", "b"],
    );
    assert_ordered(
        &te,
        &[".", "ord", "--sort", "name", "--path-separator", "|"],
        &["ord|a", "ord|b"],
    );
    let nulled = te.assert_success_and_get_output(".", &[".", "ord", "--sort", "name", "-0"]);
    assert_eq!(nulled.stdout, b"ord/a\0ord/b\0");

    let quiet = te.assert_success_and_get_output(".", &[".", "ord", "--sort", "name", "-q"]);
    assert!(quiet.stdout.is_empty());
    te.assert_failure(&["no-such-sort-pattern-xyz", "--sort", "name", "-q"]);
}

#[test]
fn test_sort_natural_case_length_and_depth() {
    let te = TestEnv::new(
        &[
            "nat/a",
            "nat/b",
            "pnat/dir10",
            "pnat/dir2",
            "pnat/dir9",
            "len",
            "ulen",
            "plen/x",
            "dep/x/y",
        ],
        &[
            "nat/file20",
            "nat/file9",
            "nat/file10",
            "nat/file007",
            "nat/file7",
            "nat/File9",
            "nat/FILE20",
            "nat/a/file7",
            "nat/b/file007",
            "pnat/dir10/a",
            "pnat/dir2/a",
            "pnat/dir9/b",
            "len/bb",
            "len/a",
            "len/dd",
            "len/ccc",
            "ulen/abc",
            "ulen/z",
            "ulen/éé",
            "plen/a",
            "plen/bb",
            "plen/x/y",
            "dep/a",
            "dep/x/y/z",
        ],
    );

    assert_ordered(
        &te,
        &[
            "^file",
            "nat",
            "--case-sensitive",
            "--sort",
            "name",
            "--sort-natural",
            "-t",
            "f",
        ],
        &[
            "nat/a/file7",
            "nat/b/file007",
            "nat/file007",
            "nat/file7",
            "nat/file9",
            "nat/file10",
            "nat/file20",
        ],
    );
    // Numerically equal names tie, then the path orders `nat/a` before `nat/b`.
    assert_ordered(
        &te,
        &[".", "nat/a", "nat/b", "--sort", "name", "--sort-natural"],
        &["nat/a/file7", "nat/b/file007"],
    );
    assert_ordered(
        &te,
        &[
            "^(File9|file10|FILE20)$",
            "nat",
            "--sort",
            "name",
            "--sort-natural",
        ],
        &["nat/File9", "nat/file10", "nat/FILE20"],
    );
    assert_ordered(
        &te,
        &[
            "^(File9|file10|FILE20)$",
            "nat",
            "--sort",
            "name",
            "--sort-natural",
            "--sort-case-sensitive",
        ],
        &["nat/FILE20", "nat/File9", "nat/file10"],
    );
    assert_ordered(
        &te,
        &[
            "^file",
            "nat",
            "--case-sensitive",
            "--sort",
            "name",
            "--sort-case-sensitive",
            "-t",
            "f",
        ],
        &[
            "nat/b/file007",
            "nat/file007",
            "nat/file10",
            "nat/file20",
            "nat/a/file7",
            "nat/file7",
            "nat/file9",
        ],
    );

    assert_ordered(
        &te,
        &[".", "pnat", "-t", "f", "--sort", "path"],
        &["pnat/dir10/a", "pnat/dir2/a", "pnat/dir9/b"],
    );
    assert_ordered(
        &te,
        &[".", "pnat", "-t", "f", "--sort", "path", "--sort-natural"],
        &["pnat/dir2/a", "pnat/dir9/b", "pnat/dir10/a"],
    );

    assert_ordered(
        &te,
        &[".", "len", "--sort", "name-length"],
        &["len/a", "len/bb", "len/dd", "len/ccc"],
    );
    assert_ordered(
        &te,
        &[".", "ulen", "--sort", "name-length", "-t", "f"],
        &["ulen/z", "ulen/éé", "ulen/abc"],
    );
    assert_ordered(
        &te,
        &[".", "plen", "--sort", "path-length"],
        &["plen/a", "plen/x/", "plen/bb", "plen/x/y"],
    );
    assert_ordered(
        &te,
        &[".", "dep", "--sort", "depth"],
        &["dep/a", "dep/x/", "dep/x/y/", "dep/x/y/z"],
    );
}

#[test]
fn test_sort_type_grouping_size_and_time() {
    let mut te = TestEnv::new(
        &["kind/dir", "sz/adir", "tim/dir", "mx"],
        &[
            "kind/file",
            "sz/empty",
            "sz/zeta",
            "sz/large",
            "tim/old",
            "tim/mid",
            "tim/new",
            "mx/mfile",
            "mx/afile",
        ],
    );
    let root = te.test_root();
    create_file_with_size(root.join("sz/empty"), 0);
    create_file_with_size(root.join("sz/zeta"), 1);
    create_file_with_size(root.join("sz/large"), 50);

    filetime::set_file_mtime(
        root.join("tim/old"),
        filetime::FileTime::from_unix_time(1_000_000, 0),
    )
    .unwrap();
    filetime::set_file_mtime(
        root.join("tim/dir"),
        filetime::FileTime::from_unix_time(1_500_000, 0),
    )
    .unwrap();
    filetime::set_file_mtime(
        root.join("tim/mid"),
        filetime::FileTime::from_unix_time(2_000_000, 0),
    )
    .unwrap();
    filetime::set_file_mtime(
        root.join("tim/new"),
        filetime::FileTime::from_unix_time(3_000_000, 0),
    )
    .unwrap();
    for (name, secs) in [("old", 1_100_000), ("mid", 2_100_000), ("new", 3_100_000)] {
        filetime::set_file_atime(
            root.join("tim").join(name),
            filetime::FileTime::from_unix_time(secs, 0),
        )
        .unwrap();
    }

    #[cfg(unix)]
    {
        std::os::unix::fs::symlink("file", root.join("kind/link")).unwrap();
        te.create_broken_symlink("kind/broken").unwrap();
        let fifo = root.join("kind/fifo");
        let fifo_c =
            std::ffi::CString::new(std::os::unix::ffi::OsStrExt::as_bytes(fifo.as_os_str()))
                .unwrap();
        assert_eq!(unsafe { libc::mkfifo(fifo_c.as_ptr(), 0o644) }, 0);
        let sock_path = root.join("kind/sock");
        let _listener = std::os::unix::net::UnixListener::bind(&sock_path).unwrap();

        std::os::unix::fs::symlink("large", root.join("sz/link")).unwrap();

        assert_ordered(
            &te,
            &[".", "kind", "--sort", "type"],
            &[
                "kind/dir/",
                "kind/broken",
                "kind/link",
                "kind/file",
                "kind/fifo",
                "kind/sock",
            ],
        );
        assert_ordered(
            &te,
            &[".", "kind", "--sort", "name", "--dirs-first"],
            &[
                "kind/dir/",
                "kind/broken",
                "kind/fifo",
                "kind/file",
                "kind/link",
                "kind/sock",
            ],
        );
        assert_ordered(
            &te,
            &[".", "kind", "--sort", "name", "--files-first"],
            &[
                "kind/file",
                "kind/broken",
                "kind/dir/",
                "kind/fifo",
                "kind/link",
                "kind/sock",
            ],
        );

        assert_ordered(
            &te,
            &[".", "sz", "--sort", "size"],
            &["sz/adir/", "sz/link", "sz/empty", "sz/zeta", "sz/large"],
        );
        // Keep the socket listener alive until the kind assertions above have run.
        drop(_listener);
    }
    #[cfg(not(unix))]
    {
        assert_ordered(
            &te,
            &[".", "kind", "--sort", "type"],
            &["kind/dir/", "kind/file"],
        );
        assert_ordered(
            &te,
            &[".", "sz", "--sort", "size"],
            &["sz/adir/", "sz/empty", "sz/zeta", "sz/large"],
        );
    }

    assert_ordered(
        &te,
        &[
            ".",
            "sz",
            "--sort",
            "size",
            "--sort-missing-last",
            "-t",
            "f",
        ],
        &["sz/empty", "sz/zeta", "sz/large"],
    );
    assert_ordered(
        &te,
        &[".", "tim", "--sort", "modified"],
        &["tim/old", "tim/dir/", "tim/mid", "tim/new"],
    );
    assert_ordered(
        &te,
        &[".", "tim", "--sort", "modified", "--reverse"],
        &["tim/new", "tim/mid", "tim/dir/", "tim/old"],
    );
    assert_ordered(
        &te,
        &[".", "tim", "-t", "f", "--sort", "accessed"],
        &["tim/old", "tim/mid", "tim/new"],
    );

    let created_once = ordered_lines(&te, &[".", "tim", "-t", "f", "--sort", "created"]);
    let created_twice = ordered_lines(&te, &[".", "tim", "-t", "f", "--sort", "created"]);
    assert_eq!(created_once, created_twice);

    fs::create_dir(root.join("mx/zdir")).unwrap();
    assert_ordered(
        &te,
        &[
            ".",
            "mx",
            "--sort",
            "name",
            "--dirs-first",
            "--max-results",
            "1",
        ],
        &["mx/zdir/"],
    );
    assert_ordered(
        &te,
        &[
            ".",
            "mx",
            "--sort",
            "name",
            "--dirs-first",
            "--reverse",
            "--max-results",
            "2",
        ],
        &["mx/mfile", "mx/afile"],
    );
    assert_ordered(
        &te,
        &[
            ".",
            "mx",
            "--sort",
            "name",
            "--reverse",
            "--max-results",
            "2",
            "-t",
            "f",
        ],
        &["mx/mfile", "mx/afile"],
    );
}

#[test]
fn test_sort_random_is_seeded_and_can_break_ties() {
    let te = TestEnv::new(
        &["rnd", "tie/p", "tie/q"],
        &[
            "rnd/a",
            "rnd/b",
            "rnd/c",
            "rnd/d",
            "rnd/e",
            "rnd/f",
            "tie/p/same",
            "tie/q/same",
            "tie/p/other",
            "tie/q/other",
        ],
    );

    let seed_one = ordered_lines(
        &te,
        &[
            ".",
            "rnd",
            "-t",
            "f",
            "--sort",
            "random",
            "--sort-seed",
            "1",
        ],
    );
    let seed_one_again = ordered_lines(
        &te,
        &[
            ".",
            "rnd",
            "-t",
            "f",
            "--sort",
            "random",
            "--sort-seed",
            "1",
        ],
    );
    assert_eq!(seed_one, seed_one_again);
    let seed_two = ordered_lines(
        &te,
        &[
            ".",
            "rnd",
            "-t",
            "f",
            "--sort",
            "random",
            "--sort-seed",
            "2",
        ],
    );
    assert_ne!(seed_one, seed_two);

    let mut actual_sorted = seed_one.clone();
    actual_sorted.sort();
    let expected = vec![
        "rnd/a".to_string(),
        "rnd/b".to_string(),
        "rnd/c".to_string(),
        "rnd/d".to_string(),
        "rnd/e".to_string(),
        "rnd/f".to_string(),
    ];
    assert_eq!(actual_sorted, expected);

    let mut unseeded_differ = false;
    let mut previous = None;
    for _ in 0..4 {
        let run = ordered_lines(&te, &[".", "rnd", "-t", "f", "--sort", "random"]);
        let mut as_set = run.clone();
        as_set.sort();
        assert_eq!(as_set, expected);
        if let Some(prev) = previous.as_ref()
            && prev != &run
        {
            unseeded_differ = true;
            break;
        }
        previous = Some(run);
        std::thread::sleep(Duration::from_millis(2));
    }
    assert!(
        unseeded_differ,
        "unseeded --sort random did not change between runs"
    );

    let mut saw_name_tie_swap = false;
    for seed in 1..40 {
        let seed = seed.to_string();
        let lines = ordered_lines(
            &te,
            &[
                ".",
                "tie",
                "-t",
                "f",
                "--sort",
                "name",
                "--sort",
                "random",
                "--sort-case-sensitive",
                "--sort-seed",
                &seed,
            ],
        );
        let names: Vec<_> = lines
            .iter()
            .map(|line| line.rsplit('/').next().unwrap())
            .collect();
        assert_eq!(names, ["other", "other", "same", "same"]);
        if lines[0] == "tie/q/other" {
            saw_name_tie_swap = true;
        }
    }
    assert!(
        saw_name_tie_swap,
        "random tie-break never reordered equal names"
    );

    let stable = ordered_lines(
        &te,
        &[
            ".",
            "tie",
            "-t",
            "f",
            "--sort",
            "name",
            "--sort",
            "random",
            "--sort-seed",
            "7",
            "--sort-case-sensitive",
        ],
    );
    let stable_again = ordered_lines(
        &te,
        &[
            ".",
            "tie",
            "-t",
            "f",
            "--sort",
            "name",
            "--sort",
            "random",
            "--sort-seed",
            "7",
            "--sort-case-sensitive",
        ],
    );
    assert_eq!(stable, stable_again);

    let mut random_primary_differs = false;
    for seed in 1..15 {
        let seed = seed.to_string();
        let by_name = ordered_lines(
            &te,
            &[
                ".",
                "tie",
                "-t",
                "f",
                "--sort",
                "name",
                "--sort",
                "random",
                "--sort-seed",
                &seed,
            ],
        );
        let by_random = ordered_lines(
            &te,
            &[
                ".",
                "tie",
                "-t",
                "f",
                "--sort",
                "random",
                "--sort",
                "name",
                "--sort-seed",
                &seed,
            ],
        );
        if by_name != by_random {
            random_primary_differs = true;
            break;
        }
    }
    assert!(random_primary_differs);
}

#[test]
fn test_sort_keeps_filters_and_default_path_order() {
    let te = TestEnv::new(
        &["filt/sub"],
        &[
            "filt/keep.txt",
            "filt/skip.rs",
            "filt/sub/keep.txt",
            "filt/.hidden.txt",
            "filt/gitignored.foo",
        ],
    );

    assert_ordered(
        &te,
        &["keep", "filt", "--sort", "name"],
        &["filt/keep.txt", "filt/sub/keep.txt"],
    );
    assert_ordered(
        &te,
        &[".", "filt", "-e", "txt", "--sort", "name", "-H"],
        &["filt/.hidden.txt", "filt/keep.txt", "filt/sub/keep.txt"],
    );
    assert_ordered(
        &te,
        &[".", "filt", "--sort", "name", "--max-depth", "1"],
        &["filt/keep.txt", "filt/skip.rs", "filt/sub/"],
    );
    let all = ordered_lines(&te, &[".", "filt", "--sort", "name"]);
    assert!(!all.iter().any(|line| line.contains("gitignored")));
    assert!(!all.iter().any(|line| line.contains(".hidden")));

    let implicit = ordered_lines(&te, &["--max-buffer-time", "10000", ".", "filt"]);
    let explicit = ordered_lines(
        &te,
        &["--sort", "path", "--sort-case-sensitive", ".", "filt"],
    );
    assert_eq!(implicit, explicit);
    assert!(!implicit.is_empty());
}

#[test]
fn test_sort_orders_more_results_than_the_output_buffer() {
    let te = TestEnv::new(&["many"], &[]);
    for index in 0..1001 {
        fs::File::create(te.test_root().join(format!("many/n{index:04}"))).unwrap();
    }

    let lines = ordered_lines(&te, &[".", "many", "--sort", "name", "-t", "f"]);
    assert_eq!(lines.len(), 1001);
    assert_eq!(lines.first().map(String::as_str), Some("many/n0000"));
    assert_eq!(lines.last().map(String::as_str), Some("many/n1000"));
    let mut sorted = lines.clone();
    sorted.sort();
    assert_eq!(lines, sorted);

    assert_ordered(
        &te,
        &[
            ".",
            "many",
            "--sort",
            "name",
            "--reverse",
            "--max-results",
            "1",
            "-t",
            "f",
        ],
        &["many/n1000"],
    );
}
