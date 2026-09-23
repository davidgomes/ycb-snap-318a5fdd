// pest. The Elegant Parser
// Copyright (c) 2018 Dragoș Tiselice
//
// Licensed under the Apache License, Version 2.0
// <LICENSE-APACHE or http://www.apache.org/licenses/LICENSE-2.0> or the MIT
// license <LICENSE-MIT or http://opensource.org/licenses/MIT>, at your
// option. All files in the project carrying such notice may not be copied,
// modified, or distributed except according to those terms.

use crate::ast::RuleType;
use crate::optimizer::*;

const MIN_PARTIAL_RUN: usize = 3;

pub fn coalesce(rule: OptimizedRule, implicit_skip: bool) -> OptimizedRule {
    let OptimizedRule { name, ty, expr } = rule;
    // In rules that may run non-atomically, `!e ~ ANY` skips whitespace/comments
    // between the lookahead and ANY, which a single character class cannot express.
    let allow_neg = !implicit_skip || matches!(ty, RuleType::Atomic | RuleType::CompoundAtomic);
    let expr = expr.map_top_down(|expr| match expr {
        OptimizedExpr::Choice(..) => coalesce_choice(expr),
        OptimizedExpr::Seq(lhs, rhs) if allow_neg => coalesce_neg_seq(*lhs, *rhs),
        expr => expr,
    });
    OptimizedRule { name, ty, expr }
}

fn coalesce_neg_seq(lhs: OptimizedExpr, rhs: OptimizedExpr) -> OptimizedExpr {
    let (any, rest) = match rhs {
        OptimizedExpr::Seq(head, rest) => (*head, Some(rest)),
        rhs => (rhs, None),
    };

    let ranges = match (&lhs, &any) {
        (OptimizedExpr::NegPred(inner), OptimizedExpr::Ident(ident)) if ident == "ANY" => {
            excluded_ranges(inner)
        }
        _ => None,
    };

    match (ranges, rest) {
        (Some(ranges), None) => OptimizedExpr::NegCharClass(to_string_ranges(&ranges)),
        (Some(ranges), Some(rest)) => OptimizedExpr::Seq(
            Box::new(OptimizedExpr::NegCharClass(to_string_ranges(&ranges))),
            rest,
        ),
        (None, None) => OptimizedExpr::Seq(Box::new(lhs), Box::new(any)),
        (None, Some(rest)) => OptimizedExpr::Seq(
            Box::new(lhs),
            Box::new(OptimizedExpr::Seq(Box::new(any), rest)),
        ),
    }
}

fn excluded_ranges(expr: &OptimizedExpr) -> Option<Vec<(char, char)>> {
    let mut ranges = vec![];
    for alternative in flatten_choice(expr) {
        ranges.extend(qualifying_ranges(alternative)?);
    }
    Some(merge(ranges))
}

fn coalesce_choice(expr: OptimizedExpr) -> OptimizedExpr {
    let alternatives: Vec<OptimizedExpr> = flatten_choice(&expr).into_iter().cloned().collect();
    let qualifying: Vec<Option<Vec<(char, char)>>> =
        alternatives.iter().map(qualifying_ranges).collect();

    if qualifying.iter().all(Option::is_some) {
        let ranges = qualifying.into_iter().flatten().flatten().collect();
        return coalesce_run(ranges, alternatives.len()).unwrap_or(expr);
    }

    let mut result = Vec::with_capacity(alternatives.len());
    let mut changed = false;
    let mut i = 0;
    while i < alternatives.len() {
        let end = (i..alternatives.len())
            .find(|&j| qualifying[j].is_none())
            .unwrap_or(alternatives.len());

        if end - i >= MIN_PARTIAL_RUN {
            let ranges = qualifying[i..end]
                .iter()
                .flatten()
                .flatten()
                .copied()
                .collect();
            if let Some(coalesced) = coalesce_run(ranges, end - i) {
                result.push(coalesced);
                changed = true;
                i = end;
                continue;
            }
        }

        let stop = if end == i { i + 1 } else { end };
        result.extend_from_slice(&alternatives[i..stop]);
        i = stop;
    }

    if changed {
        build_choice(result)
    } else {
        expr
    }
}

fn coalesce_run(ranges: Vec<(char, char)>, alternative_count: usize) -> Option<OptimizedExpr> {
    let merged = merge(ranges);
    if merged.len() >= alternative_count {
        return None;
    }

    Some(match merged.as_slice() {
        [(start, end)] if start == end => OptimizedExpr::Str(start.to_string()),
        [(start, end)] => OptimizedExpr::Range(start.to_string(), end.to_string()),
        _ => OptimizedExpr::CharClass(to_string_ranges(&merged)),
    })
}

fn flatten_choice(expr: &OptimizedExpr) -> Vec<&OptimizedExpr> {
    let mut alternatives = vec![];
    let mut current = expr;
    while let OptimizedExpr::Choice(lhs, rhs) = current {
        alternatives.push(lhs.as_ref());
        current = rhs;
    }
    alternatives.push(current);
    alternatives
}

fn build_choice(mut alternatives: Vec<OptimizedExpr>) -> OptimizedExpr {
    let mut result = alternatives
        .pop()
        .expect("choice has at least one alternative");
    while let Some(lhs) = alternatives.pop() {
        result = OptimizedExpr::Choice(Box::new(lhs), Box::new(result));
    }
    result
}

fn single_char(string: &str) -> Option<char> {
    let mut chars = string.chars();
    match (chars.next(), chars.next()) {
        (Some(c), None) => Some(c),
        _ => None,
    }
}

fn qualifying_ranges(expr: &OptimizedExpr) -> Option<Vec<(char, char)>> {
    match expr {
        OptimizedExpr::Str(string) => single_char(string).map(|c| vec![(c, c)]),
        OptimizedExpr::Insens(string) => single_char(string).map(|c| {
            // Insensitive matching is ASCII-only, so only ASCII letters gain a second case.
            if c.is_ascii_alphabetic() {
                let lower = c.to_ascii_lowercase();
                let upper = c.to_ascii_uppercase();
                vec![(lower, lower), (upper, upper)]
            } else {
                vec![(c, c)]
            }
        }),
        OptimizedExpr::Range(start, end) => match (single_char(start), single_char(end)) {
            (Some(start), Some(end)) if start <= end => Some(vec![(start, end)]),
            _ => None,
        },
        OptimizedExpr::CharClass(ranges) => ranges
            .iter()
            .map(
                |(start, end)| match (single_char(start), single_char(end)) {
                    (Some(start), Some(end)) if start <= end => Some((start, end)),
                    _ => None,
                },
            )
            .collect(),
        OptimizedExpr::RestoreOnErr(inner) => qualifying_ranges(inner),
        _ => None,
    }
}

fn merge(mut ranges: Vec<(char, char)>) -> Vec<(char, char)> {
    ranges.sort_unstable();
    let mut merged: Vec<(char, char)> = Vec::with_capacity(ranges.len());
    for (start, end) in ranges {
        if let Some(last) = merged.last_mut() {
            if (start as u32) <= (last.1 as u32).saturating_add(1) {
                if end > last.1 {
                    last.1 = end;
                }
                continue;
            }
        }
        merged.push((start, end));
    }
    merged
}

fn to_string_ranges(ranges: &[(char, char)]) -> Vec<(String, String)> {
    ranges
        .iter()
        .map(|(start, end)| (start.to_string(), end.to_string()))
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::optimizer::OptimizedExpr::*;

    fn rule(ty: RuleType, expr: OptimizedExpr) -> OptimizedRule {
        OptimizedRule {
            name: "rule".to_owned(),
            ty,
            expr,
        }
    }

    fn run(expr: OptimizedExpr) -> OptimizedExpr {
        coalesce(rule(RuleType::Normal, expr), false).expr
    }

    fn s(c: &str) -> OptimizedExpr {
        Str(c.to_owned())
    }

    fn class(ranges: &[(&str, &str)]) -> Vec<(String, String)> {
        ranges
            .iter()
            .map(|(a, b)| (a.to_string(), b.to_string()))
            .collect()
    }

    #[test]
    fn adjacent_strings_become_range() {
        let expr = box_tree!(Choice(s("a"), Choice(s("b"), s("c"))));
        assert_eq!(run(expr), Range("a".to_owned(), "c".to_owned()));
    }

    #[test]
    fn duplicate_strings_become_str() {
        let expr = box_tree!(Choice(s("a"), s("a")));
        assert_eq!(run(expr), s("a"));
    }

    #[test]
    fn disjoint_without_reduction_is_unchanged() {
        let expr = box_tree!(Choice(s("a"), Choice(s("c"), s("e"))));
        assert_eq!(run(expr.clone()), expr);
    }

    #[test]
    fn merged_ranges_sorted() {
        let expr = box_tree!(Choice(
            s("z"),
            Choice(
                Range("0".to_owned(), "9".to_owned()),
                Choice(s("x"), s("y"))
            )
        ));
        assert_eq!(run(expr), CharClass(class(&[("0", "9"), ("x", "z")])));
    }

    #[test]
    fn insensitive_expands_cases() {
        let expr = box_tree!(Choice(
            Insens("a".to_owned()),
            Choice(Insens("b".to_owned()), Insens("c".to_owned()))
        ));
        assert_eq!(run(expr), CharClass(class(&[("A", "C"), ("a", "c")])));
    }

    #[test]
    fn insensitive_without_reduction_is_unchanged() {
        let expr = box_tree!(Choice(Insens("a".to_owned()), Insens("b".to_owned())));
        assert_eq!(run(expr.clone()), expr);
    }

    #[test]
    fn absorbs_char_class_and_strips_restore() {
        let existing = CharClass(class(&[("a", "c"), ("e", "z")]));
        let restored = box_tree!(RestoreOnErr(Str("d".to_owned())));
        let expr = Choice(Box::new(existing), Box::new(restored));
        assert_eq!(run(expr), Range("a".to_owned(), "z".to_owned()));
    }

    #[test]
    fn partial_run_of_three() {
        let expr = box_tree!(Choice(
            Ident("x".to_owned()),
            Choice(
                s("a"),
                Choice(s("b"), Choice(s("c"), Ident("y".to_owned())))
            )
        ));
        assert_eq!(
            run(expr),
            box_tree!(Choice(
                Ident("x".to_owned()),
                Choice(Range("a".to_owned(), "c".to_owned()), Ident("y".to_owned()))
            ))
        );
    }

    #[test]
    fn partial_run_of_two_is_unchanged() {
        let expr = box_tree!(Choice(
            s("a"),
            Choice(s("b"), Choice(Ident("x".to_owned()), Ident("y".to_owned())))
        ));
        assert_eq!(run(expr.clone()), expr);
    }

    #[test]
    fn negated_choice_before_any() {
        let expr = box_tree!(Seq(
            NegPred(Choice(s("b"), Choice(s("a"), s("\n")))),
            Ident("ANY".to_owned())
        ));
        assert_eq!(run(expr), NegCharClass(class(&[("\n", "\n"), ("a", "b")])));
    }

    #[test]
    fn negated_choice_before_any_in_sequence() {
        let expr = box_tree!(Seq(
            NegPred(Choice(s("a"), s("c"))),
            Seq(Ident("ANY".to_owned()), Ident("x".to_owned()))
        ));
        let negated = NegCharClass(class(&[("a", "a"), ("c", "c")]));
        assert_eq!(
            run(expr),
            Seq(Box::new(negated), Box::new(Ident("x".to_owned())))
        );
    }

    #[test]
    fn negated_choice_kept_with_implicit_skip() {
        let expr = box_tree!(Seq(NegPred(s("a")), Ident("ANY".to_owned())));
        assert_eq!(
            coalesce(rule(RuleType::Normal, expr.clone()), true).expr,
            expr
        );
        assert_eq!(
            coalesce(rule(RuleType::Atomic, expr), true).expr,
            NegCharClass(class(&[("a", "a")]))
        );
    }
}
