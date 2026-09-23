// pest. The Elegant Parser
// Copyright (c) 2018 Dragoș Tiselice
//
// Licensed under the Apache License, Version 2.0
// <LICENSE-APACHE or http://www.apache.org/licenses/LICENSE-2.0> or the MIT
// license <LICENSE-MIT or http://opensource.org/licenses/MIT>, at your
// option. All files in the project carrying such notice may not be copied,
// modified, or distributed except according to those terms.

#![cfg_attr(not(feature = "std"), no_std)]
extern crate alloc;
use alloc::{format, vec::Vec};

#[macro_use]
extern crate pest;
#[macro_use]
extern crate pest_derive;

use pest::Parser;

#[derive(Parser)]
#[grammar = "../tests/char_class.pest"]
struct CharClassParser;

#[test]
fn word() {
    parses_to! {
        parser: CharClassParser,
        input: "Ab_-.z",
        rule: Rule::word,
        tokens: [
            word(0, 6)
        ]
    };
    assert!(CharClassParser::parse(Rule::word, "1").is_err());
}

#[test]
fn hex() {
    parses_to! {
        parser: CharClassParser,
        input: "aF 09",
        rule: Rule::hex,
        tokens: [
            hex(0, 5)
        ]
    };
    assert!(CharClassParser::parse(Rule::hex, "g").is_err());
}

#[test]
fn prefixed() {
    parses_to! {
        parser: CharClassParser,
        input: "ab",
        rule: Rule::prefixed,
        tokens: [
            prefixed(0, 2)
        ]
    };
    parses_to! {
        parser: CharClassParser,
        input: "c",
        rule: Rule::prefixed,
        tokens: [
            prefixed(0, 1)
        ]
    };
    assert!(CharClassParser::parse(Rule::prefixed, "d").is_err());
}

#[test]
fn quoted() {
    parses_to! {
        parser: CharClassParser,
        input: "\"a b\"",
        rule: Rule::quoted,
        tokens: [
            quoted(0, 5)
        ]
    };
    assert!(CharClassParser::parse(Rule::quoted, "\"a1\"").is_err());
    assert!(CharClassParser::parse(Rule::quoted, "\"a\\\"").is_err());
}

#[test]
fn not_a_skips_whitespace_before_any() {
    parses_to! {
        parser: CharClassParser,
        input: " a",
        rule: Rule::not_a,
        tokens: [
            not_a(0, 2)
        ]
    };
    assert!(CharClassParser::parse(Rule::not_a, "a").is_err());
}
