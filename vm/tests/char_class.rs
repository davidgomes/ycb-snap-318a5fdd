// pest. The Elegant Parser
// Copyright (c) 2018 Dragoș Tiselice
//
// Licensed under the Apache License, Version 2.0
// <LICENSE-APACHE or http://www.apache.org/licenses/LICENSE-2.0> or the MIT
// license <LICENSE-MIT or http://opensource.org/licenses/MIT>, at your
// option. All files in the project carrying such notice may not be copied,
// modified, or distributed except according to those terms.

extern crate pest;
extern crate pest_meta;
#[macro_use]
extern crate pest_vm;

use pest_meta::parser::Rule;
use pest_meta::{optimizer, parser};
use pest_vm::Vm;

const GRAMMAR: &str = include_str!("char_class.pest");

fn vm() -> Vm {
    let pairs = parser::parse(Rule::grammar_rules, GRAMMAR).unwrap();
    let ast = parser::consume_rules(pairs).unwrap();
    Vm::new(optimizer::optimize(ast))
}

#[test]
fn word() {
    parses_to! {
        parser: vm(),
        input: "Ab_-.z",
        rule: "word",
        tokens: [
            word(0, 6)
        ]
    };
    assert!(vm().parse("word", "1").is_err());
}

#[test]
fn hex() {
    parses_to! {
        parser: vm(),
        input: "aF 09",
        rule: "hex",
        tokens: [
            hex(0, 5)
        ]
    };
    assert!(vm().parse("hex", "g").is_err());
}

#[test]
fn prefixed() {
    parses_to! {
        parser: vm(),
        input: "ab",
        rule: "prefixed",
        tokens: [
            prefixed(0, 2)
        ]
    };
    parses_to! {
        parser: vm(),
        input: "c",
        rule: "prefixed",
        tokens: [
            prefixed(0, 1)
        ]
    };
    assert!(vm().parse("prefixed", "d").is_err());
}

#[test]
fn quoted() {
    parses_to! {
        parser: vm(),
        input: "\"a b\"",
        rule: "quoted",
        tokens: [
            quoted(0, 5)
        ]
    };
    assert!(vm().parse("quoted", "\"a1\"").is_err());
    assert!(vm().parse("quoted", "\"a\\\"").is_err());
}

#[test]
fn not_a_skips_whitespace_before_any() {
    parses_to! {
        parser: vm(),
        input: " a",
        rule: "not_a",
        tokens: [
            not_a(0, 2)
        ]
    };
    assert!(vm().parse("not_a", "a").is_err());
}
