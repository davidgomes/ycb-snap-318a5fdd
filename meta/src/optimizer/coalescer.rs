// pest. The Elegant Parser
// Copyright (c) 2018 Dragoș Tiselice
//
// Licensed under the Apache License, Version 2.0
// <LICENSE-APACHE or http://www.apache.org/licenses/LICENSE-2.0> or the MIT
// license <LICENSE-MIT or http://opensource.org/licenses/MIT>, at your
// option. All files in the project carrying such notice may not be copied,
// modified, or distributed except according to those terms.

use std::collections::HashMap;
use std::mem;

use crate::optimizer::*;

pub fn coalesce(rule: OptimizedRule, rules: &HashMap<String, OptimizedExpr>) -> OptimizedRule {
    let OptimizedRule { name, ty, expr } = rule;
    // `!class ~ ANY` only behaves like a single negated class when no implicit whitespace or
    // comment can be skipped between the predicate and `ANY`.
    let collapse_negations = matches!(ty, RuleType::Atomic | RuleType::CompoundAtomic)
        || name == "WHITESPACE"
        || name == "COMMENT"
        || !(rules.contains_key("WHITESPACE") || rules.contains_key("COMMENT"));
    let expr = coalesce_expr(expr, collapse_negations);
    OptimizedRule { name, ty, expr }
}

fn coalesce_expr(expr: OptimizedExpr, collapse_negations: bool) -> OptimizedExpr {
    let coalesce = |expr: Box<OptimizedExpr>| Box::new(coalesce_expr(*expr, collapse_negations));
    match expr {
        OptimizedExpr::Choice(lhs, rhs) => coalesce_choice(*lhs, *rhs, collapse_negations),
        OptimizedExpr::Seq(lhs, rhs) => coalesce_seq(*lhs, *rhs, collapse_negations),
        OptimizedExpr::PosPred(expr) => OptimizedExpr::PosPred(coalesce(expr)),
        OptimizedExpr::NegPred(expr) => OptimizedExpr::NegPred(coalesce(expr)),
        OptimizedExpr::Opt(expr) => OptimizedExpr::Opt(coalesce(expr)),
        OptimizedExpr::Rep(expr) => OptimizedExpr::Rep(coalesce(expr)),
        #[cfg(feature = "grammar-extras")]
        OptimizedExpr::RepOnce(expr) => OptimizedExpr::RepOnce(coalesce(expr)),
        OptimizedExpr::Push(expr) => OptimizedExpr::Push(coalesce(expr)),
        #[cfg(feature = "grammar-extras")]
        OptimizedExpr::NodeTag(expr, tag) => OptimizedExpr::NodeTag(coalesce(expr), tag),
        OptimizedExpr::RestoreOnErr(expr) => OptimizedExpr::RestoreOnErr(coalesce(expr)),
        expr => expr,
    }
}

fn coalesce_choice(
    lhs: OptimizedExpr,
    rhs: OptimizedExpr,
    collapse_negations: bool,
) -> OptimizedExpr {
    let mut alternatives = vec![lhs];
    let mut current = rhs;
    while let OptimizedExpr::Choice(lhs, rhs) = current {
        alternatives.push(*lhs);
        current = *rhs;
    }
    alternatives.push(current);

    let ranges: Vec<_> = alternatives.iter().map(char_ranges).collect();
    // A chain made up entirely of character alternatives is coalesced as a whole, while a mixed
    // chain only has its runs of at least three character alternatives coalesced.
    let min_run_len = if ranges.iter().all(Option::is_some) {
        2
    } else {
        3
    };

    let mut coalesced = Vec::with_capacity(alternatives.len());
    let mut run = vec![];
    for (alternative, ranges) in alternatives.into_iter().zip(ranges) {
        match ranges {
            Some(ranges) => run.push((alternative, ranges)),
            None => {
                coalesced.extend(coalesce_run(mem::take(&mut run), min_run_len));
                coalesced.push(coalesce_expr(alternative, collapse_negations));
            }
        }
    }
    coalesced.extend(coalesce_run(run, min_run_len));

    let mut coalesced = coalesced.into_iter().rev();
    let last = coalesced.next().expect("choice without alternatives");
    coalesced.fold(last, |rhs, lhs| {
        OptimizedExpr::Choice(Box::new(lhs), Box::new(rhs))
    })
}

fn coalesce_run(
    run: Vec<(OptimizedExpr, Vec<(char, char)>)>,
    min_len: usize,
) -> Vec<OptimizedExpr> {
    if run.len() >= min_len {
        let ranges = merge_ranges(
            run.iter()
                .flat_map(|(_, ranges)| ranges.iter().copied())
                .collect(),
        );
        if ranges.len() < run.len() {
            return vec![char_class(ranges)];
        }
    }
    run.into_iter()
        .map(|(alternative, _)| alternative)
        .collect()
}

fn coalesce_seq(lhs: OptimizedExpr, rhs: OptimizedExpr, collapse_negations: bool) -> OptimizedExpr {
    let negated = if collapse_negations {
        neg_char_class(&lhs)
    } else {
        None
    };
    match (negated, rhs) {
        (Some(negated), rhs) if is_any(&rhs) => negated,
        (Some(negated), OptimizedExpr::Seq(next, rest)) if is_any(&next) => OptimizedExpr::Seq(
            Box::new(negated),
            Box::new(coalesce_expr(*rest, collapse_negations)),
        ),
        (_, rhs) => OptimizedExpr::Seq(
            Box::new(coalesce_expr(lhs, collapse_negations)),
            Box::new(coalesce_expr(rhs, collapse_negations)),
        ),
    }
}

fn is_any(expr: &OptimizedExpr) -> bool {
    matches!(expr, OptimizedExpr::Ident(name) if name == "ANY")
}

/// Converts `!(c1 | c2 | ...)` into the class that `!(c1 | c2 | ...) ~ ANY` matches.
fn neg_char_class(expr: &OptimizedExpr) -> Option<OptimizedExpr> {
    let OptimizedExpr::NegPred(expr) = expr else {
        return None;
    };
    let mut ranges = vec![];
    let mut current = expr.as_ref();
    while let OptimizedExpr::Choice(lhs, rhs) = current {
        ranges.extend(char_ranges(lhs)?);
        current = rhs;
    }
    ranges.extend(char_ranges(current)?);
    Some(OptimizedExpr::NegCharClass(to_string_ranges(merge_ranges(
        ranges,
    ))))
}

fn char_class(ranges: Vec<(char, char)>) -> OptimizedExpr {
    if let [(start, end)] = ranges[..] {
        return if start == end {
            OptimizedExpr::Str(start.to_string())
        } else {
            OptimizedExpr::Range(start.to_string(), end.to_string())
        };
    }
    OptimizedExpr::CharClass(to_string_ranges(ranges))
}

/// Returns the ranges of characters matched by `expr` if it can be part of a character class.
fn char_ranges(expr: &OptimizedExpr) -> Option<Vec<(char, char)>> {
    match expr {
        OptimizedExpr::Str(string) => single_char(string).map(|c| vec![(c, c)]),
        // `match_insensitive` only folds ASCII case.
        OptimizedExpr::Insens(string) => single_char(string).map(|c| {
            let (lower, upper) = (c.to_ascii_lowercase(), c.to_ascii_uppercase());
            vec![(lower, lower), (upper, upper)]
        }),
        OptimizedExpr::Range(start, end) => char_range(start, end).map(|range| vec![range]),
        OptimizedExpr::CharClass(ranges) => ranges
            .iter()
            .map(|(start, end)| char_range(start, end))
            .collect(),
        OptimizedExpr::RestoreOnErr(expr) => char_ranges(expr),
        _ => None,
    }
}

fn char_range(start: &str, end: &str) -> Option<(char, char)> {
    let (start, end) = (single_char(start)?, single_char(end)?);
    (start <= end).then_some((start, end))
}

fn single_char(string: &str) -> Option<char> {
    let mut chars = string.chars();
    match (chars.next(), chars.next()) {
        (Some(c), None) => Some(c),
        _ => None,
    }
}

/// Sorts `ranges` and merges the ones that overlap or are adjacent.
fn merge_ranges(mut ranges: Vec<(char, char)>) -> Vec<(char, char)> {
    ranges.sort_unstable();
    let mut merged: Vec<(char, char)> = Vec::with_capacity(ranges.len());
    for (start, end) in ranges {
        match merged.last_mut() {
            Some((_, last_end)) if start as u32 <= *last_end as u32 + 1 => {
                *last_end = end.max(*last_end);
            }
            _ => merged.push((start, end)),
        }
    }
    merged
}

fn to_string_ranges(ranges: Vec<(char, char)>) -> Vec<(String, String)> {
    ranges
        .into_iter()
        .map(|(start, end)| (start.to_string(), end.to_string()))
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::optimizer::OptimizedExpr::*;

    fn coalesced(ty: RuleType, expr: OptimizedExpr) -> OptimizedExpr {
        coalesced_in("rule", ty, expr, &HashMap::new())
    }

    fn coalesced_in(
        name: &str,
        ty: RuleType,
        expr: OptimizedExpr,
        rules: &HashMap<String, OptimizedExpr>,
    ) -> OptimizedExpr {
        let rule = OptimizedRule {
            name: name.to_owned(),
            ty,
            expr,
        };
        coalesce(rule, rules).expr
    }

    fn string(string: &str) -> OptimizedExpr {
        Str(string.to_owned())
    }

    fn insens(string: &str) -> OptimizedExpr {
        Insens(string.to_owned())
    }

    fn range(start: &str, end: &str) -> OptimizedExpr {
        Range(start.to_owned(), end.to_owned())
    }

    fn ident(name: &str) -> OptimizedExpr {
        Ident(name.to_owned())
    }

    fn ranges(ranges: &[(&str, &str)]) -> Vec<(String, String)> {
        ranges
            .iter()
            .map(|&(start, end)| (start.to_owned(), end.to_owned()))
            .collect()
    }

    fn choice(alternatives: Vec<OptimizedExpr>) -> OptimizedExpr {
        alternatives
            .into_iter()
            .rev()
            .reduce(|rhs, lhs| Choice(Box::new(lhs), Box::new(rhs)))
            .unwrap()
    }

    fn seq(exprs: Vec<OptimizedExpr>) -> OptimizedExpr {
        exprs
            .into_iter()
            .rev()
            .reduce(|rhs, lhs| Seq(Box::new(lhs), Box::new(rhs)))
            .unwrap()
    }

    fn not(expr: OptimizedExpr) -> OptimizedExpr {
        NegPred(Box::new(expr))
    }

    fn restore(expr: OptimizedExpr) -> OptimizedExpr {
        RestoreOnErr(Box::new(expr))
    }

    #[test]
    fn single_range() {
        let expr = choice(vec![string("a"), string("b"), string("c")]);
        assert_eq!(coalesced(RuleType::Normal, expr), range("a", "c"));

        let expr = choice(vec![range("a", "m"), range("f", "z"), string("b")]);
        assert_eq!(coalesced(RuleType::Normal, expr), range("a", "z"));
    }

    #[test]
    fn single_char() {
        let expr = choice(vec![string("a"), string("a")]);
        assert_eq!(coalesced(RuleType::Normal, expr), string("a"));

        let expr = choice(vec![insens("1"), string("1"), range("1", "1")]);
        assert_eq!(coalesced(RuleType::Normal, expr), string("1"));
    }

    #[test]
    fn sorted_merged_ranges() {
        let expr = choice(vec![
            string("z"),
            range("a", "c"),
            string("x"),
            string("d"),
            string("y"),
        ]);
        assert_eq!(
            coalesced(RuleType::Normal, expr),
            CharClass(ranges(&[("a", "d"), ("x", "z")]))
        );
    }

    #[test]
    fn no_fewer_ranges() {
        let expr = choice(vec![string("a"), string("c"), string("e")]);
        assert_eq!(coalesced(RuleType::Normal, expr.clone()), expr);

        let expr = choice(vec![range("a", "c"), range("x", "z")]);
        assert_eq!(coalesced(RuleType::Normal, expr.clone()), expr);
    }

    #[test]
    fn insensitive() {
        let expr = choice(vec![insens("a"), insens("b"), insens("c")]);
        assert_eq!(
            coalesced(RuleType::Normal, expr),
            CharClass(ranges(&[("A", "C"), ("a", "c")]))
        );

        let expr = choice(vec![insens("A"), insens("b")]);
        assert_eq!(coalesced(RuleType::Normal, expr.clone()), expr);

        let expr = choice(vec![insens("1"), string("2")]);
        assert_eq!(coalesced(RuleType::Normal, expr), range("1", "2"));

        let expr = choice(vec![insens("é"), string("é")]);
        assert_eq!(coalesced(RuleType::Normal, expr), string("é"));
    }

    #[test]
    fn multiple_chars() {
        let expr = choice(vec![string("ab"), insens("cd"), string(""), string("e")]);
        assert_eq!(coalesced(RuleType::Normal, expr.clone()), expr);

        let expr = choice(vec![range("z", "a"), string("b")]);
        assert_eq!(coalesced(RuleType::Normal, expr.clone()), expr);
    }

    #[test]
    fn absorb_char_class() {
        let expr = choice(vec![
            CharClass(ranges(&[("a", "c"), ("x", "z")])),
            string("d"),
            string("w"),
        ]);
        assert_eq!(
            coalesced(RuleType::Normal, expr),
            CharClass(ranges(&[("a", "d"), ("w", "z")]))
        );

        let expr = choice(vec![CharClass(ranges(&[("a", "c")])), string("d")]);
        assert_eq!(coalesced(RuleType::Normal, expr), range("a", "d"));
    }

    #[test]
    fn restore_on_err() {
        let expr = choice(vec![restore(string("a")), restore(range("b", "c"))]);
        assert_eq!(coalesced(RuleType::Normal, expr), range("a", "c"));

        let expr = choice(vec![restore(string("a")), string("c")]);
        assert_eq!(coalesced(RuleType::Normal, expr.clone()), expr);

        let expr = choice(vec![restore(ident("a")), string("b"), string("c")]);
        assert_eq!(coalesced(RuleType::Normal, expr.clone()), expr);
    }

    #[test]
    fn runs_in_mixed_choice() {
        let expr = choice(vec![
            ident("x"),
            string("a"),
            string("b"),
            string("c"),
            ident("y"),
            string("d"),
            string("e"),
        ]);
        assert_eq!(
            coalesced(RuleType::Normal, expr),
            choice(vec![
                ident("x"),
                range("a", "c"),
                ident("y"),
                string("d"),
                string("e"),
            ])
        );

        let expr = choice(vec![
            string("a"),
            restore(string("b")),
            string("d"),
            ident("x"),
        ]);
        assert_eq!(
            coalesced(RuleType::Normal, expr),
            choice(vec![
                CharClass(ranges(&[("a", "b"), ("d", "d")])),
                ident("x")
            ])
        );
    }

    #[test]
    fn short_runs_in_mixed_choice() {
        let expr = choice(vec![ident("x"), string("a"), string("b")]);
        assert_eq!(coalesced(RuleType::Normal, expr.clone()), expr);

        let expr = choice(vec![string("a"), string("b"), ident("x")]);
        assert_eq!(coalesced(RuleType::Normal, expr.clone()), expr);

        let expr = choice(vec![ident("x"), string("a"), string("c"), string("e")]);
        assert_eq!(coalesced(RuleType::Normal, expr.clone()), expr);
    }

    #[test]
    fn nested() {
        let expr = Rep(Box::new(seq(vec![
            Opt(Box::new(choice(vec![string("a"), string("b")]))),
            choice(vec![
                seq(vec![ident("x"), choice(vec![string("c"), string("d")])]),
                ident("y"),
            ]),
            restore(Push(Box::new(choice(vec![string("e"), string("f")])))),
        ])));
        assert_eq!(
            coalesced(RuleType::Normal, expr),
            Rep(Box::new(seq(vec![
                Opt(Box::new(range("a", "b"))),
                choice(vec![seq(vec![ident("x"), range("c", "d")]), ident("y")]),
                restore(Push(Box::new(range("e", "f")))),
            ])))
        );
    }

    #[test]
    #[cfg(feature = "grammar-extras")]
    fn nested_grammar_extras() {
        let expr = RepOnce(Box::new(NodeTag(
            Box::new(choice(vec![string("a"), string("b")])),
            "tag".to_owned(),
        )));
        assert_eq!(
            coalesced(RuleType::Normal, expr),
            RepOnce(Box::new(NodeTag(
                Box::new(range("a", "b")),
                "tag".to_owned()
            )))
        );
    }

    #[test]
    fn neg_char_class() {
        let expr = seq(vec![
            not(choice(vec![string("\""), string("\\")])),
            ident("ANY"),
        ]);
        assert_eq!(
            coalesced(RuleType::Normal, expr),
            NegCharClass(ranges(&[("\"", "\""), ("\\", "\\")]))
        );

        let expr = seq(vec![not(range("a", "z")), ident("ANY")]);
        assert_eq!(
            coalesced(RuleType::Normal, expr),
            NegCharClass(ranges(&[("a", "z")]))
        );

        let expr = seq(vec![
            not(choice(vec![insens("a"), string("b"), restore(string("c"))])),
            ident("ANY"),
        ]);
        assert_eq!(
            coalesced(RuleType::Normal, expr),
            NegCharClass(ranges(&[("A", "A"), ("a", "c")]))
        );
    }

    #[test]
    fn neg_char_class_in_sequence() {
        let expr = seq(vec![
            string("a"),
            not(string("b")),
            ident("ANY"),
            not(string("c")),
            ident("ANY"),
            string("d"),
        ]);
        assert_eq!(
            coalesced(RuleType::Normal, expr),
            seq(vec![
                string("a"),
                NegCharClass(ranges(&[("b", "b")])),
                NegCharClass(ranges(&[("c", "c")])),
                string("d"),
            ])
        );
    }

    #[test]
    fn no_neg_char_class() {
        let expr = seq(vec![
            not(choice(vec![
                string("a"),
                string("b"),
                string("c"),
                ident("x"),
            ])),
            ident("ANY"),
        ]);
        assert_eq!(
            coalesced(RuleType::Normal, expr),
            seq(vec![
                not(choice(vec![range("a", "c"), ident("x")])),
                ident("ANY"),
            ])
        );

        let expr = seq(vec![
            not(choice(vec![string("a"), string("b")])),
            ident("x"),
        ]);
        assert_eq!(
            coalesced(RuleType::Normal, expr),
            seq(vec![not(range("a", "b")), ident("x")])
        );
    }

    #[test]
    fn neg_char_class_with_implicit_whitespace() {
        let negation = || {
            seq(vec![
                not(choice(vec![string("a"), string("b")])),
                ident("ANY"),
            ])
        };
        let negated = NegCharClass(ranges(&[("a", "b")]));
        let kept = seq(vec![not(range("a", "b")), ident("ANY")]);

        for skipped in ["WHITESPACE", "COMMENT"] {
            let rules = HashMap::from([(skipped.to_owned(), string(" "))]);

            for ty in [RuleType::Normal, RuleType::Silent, RuleType::NonAtomic] {
                assert_eq!(coalesced_in("rule", ty, negation(), &rules), kept);
                assert_eq!(coalesced_in(skipped, ty, negation(), &rules), negated);
            }
            for ty in [RuleType::Atomic, RuleType::CompoundAtomic] {
                assert_eq!(coalesced_in("rule", ty, negation(), &rules), negated);
            }
        }
    }
}
