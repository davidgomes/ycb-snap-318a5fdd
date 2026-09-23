// pest. The Elegant Parser
//
// Licensed under the Apache License, Version 2.0
// <LICENSE-APACHE or http://www.apache.org/licenses/LICENSE-2.0> or the MIT
// license <LICENSE-MIT or http://opensource.org/licenses/MIT>, at your
// option. All files in the project carrying such notice may not be copied,
// modified, or distributed except according to those terms.

extern crate pest;
extern crate pest_meta;
extern crate pest_vm;

use pest_meta::optimizer;
use pest_meta::parser::{self, Rule};
use pest_vm::Vm;

const GRAMMAR: &str = r#"
WHITESPACE = _{ " " }
ident = @{ ("_" | 'a'..'z' | 'A'..'Z' | ^"q")+ }
not_newline = @{ (!("\n" | "\r") ~ ANY)+ }
spaced = { !("a" | "b") ~ ANY }
"#;

fn rules() -> Vec<optimizer::OptimizedRule> {
    let pairs = parser::parse(Rule::grammar_rules, GRAMMAR).unwrap();
    let ast = parser::consume_rules(pairs).unwrap();
    optimizer::optimize(ast)
}

fn parsed_len(vm: &Vm, rule: &str, input: &str) -> Option<usize> {
    vm.parse(rule, input)
        .ok()
        .map(|mut pairs| pairs.next().unwrap().as_span().end())
}

#[test]
fn optimizes_to_char_classes() {
    let rules = rules();
    let expr = |name: &str| {
        rules
            .iter()
            .find(|r| r.name == name)
            .unwrap()
            .expr
            .to_string()
    };

    assert!(expr("ident").contains("('A'..'Z' | \"_\" | 'a'..'z')"));
    assert!(!expr("ident").contains("^\"q\""));
    assert_eq!(expr("spaced"), "(!('a'..'b') ~ ANY)");
    assert!(expr("not_newline").contains(r#"(!("\n" | "\r") ~ ANY)"#));
}

#[test]
fn char_class_matches() {
    let vm = Vm::new(rules());

    assert_eq!(parsed_len(&vm, "ident", "Foo_bar9"), Some(7));
    assert_eq!(parsed_len(&vm, "ident", "9"), None);
}

#[test]
fn neg_char_class_matches() {
    let vm = Vm::new(rules());

    assert_eq!(parsed_len(&vm, "not_newline", "ab c\nd"), Some(4));
    assert_eq!(parsed_len(&vm, "not_newline", "\n"), None);
    assert_eq!(parsed_len(&vm, "not_newline", ""), None);
}

#[test]
fn neg_char_class_keeps_implicit_whitespace() {
    let vm = Vm::new(rules());

    assert_eq!(parsed_len(&vm, "spaced", " a"), Some(2));
    assert_eq!(parsed_len(&vm, "spaced", "a"), None);
    assert_eq!(parsed_len(&vm, "spaced", "c"), Some(1));
}
