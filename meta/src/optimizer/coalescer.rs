// pest. The Elegant Parser
// Copyright (c) 2018 Dragoș Tiselice
//
// Licensed under the Apache License, Version 2.0
// <LICENSE-APACHE or http://www.apache.org/licenses/LICENSE-2.0> or the MIT
// license <LICENSE-MIT or http://opensource.org/licenses/MIT>, at your
// option. All files in the project carrying such notice may not be copied,
// modified, or distributed except according to those terms.

use crate::optimizer::{OptimizedExpr, OptimizedRule};

/// Final optimizer pass. Collapses choice chains of character alternatives
/// into character classes, top-down.
pub fn coalesce(rule: OptimizedRule) -> OptimizedRule {
    let OptimizedRule { name, ty, expr } = rule;
    OptimizedRule {
        name,
        ty,
        expr: coalesce_expr(expr),
    }
}

fn coalesce_expr(expr: OptimizedExpr) -> OptimizedExpr {
    match expr {
        OptimizedExpr::Choice(lhs, rhs) => coalesce_choice(OptimizedExpr::Choice(lhs, rhs)),
        OptimizedExpr::Seq(lhs, rhs) => {
            if let Some(negated) = negated_class(&lhs, &rhs) {
                return negated;
            }
            OptimizedExpr::Seq(Box::new(coalesce_expr(*lhs)), Box::new(coalesce_expr(*rhs)))
        }
        OptimizedExpr::PosPred(expr) => OptimizedExpr::PosPred(Box::new(coalesce_expr(*expr))),
        OptimizedExpr::NegPred(expr) => OptimizedExpr::NegPred(Box::new(coalesce_expr(*expr))),
        OptimizedExpr::Opt(expr) => OptimizedExpr::Opt(Box::new(coalesce_expr(*expr))),
        OptimizedExpr::Rep(expr) => OptimizedExpr::Rep(Box::new(coalesce_expr(*expr))),
        #[cfg(feature = "grammar-extras")]
        OptimizedExpr::RepOnce(expr) => OptimizedExpr::RepOnce(Box::new(coalesce_expr(*expr))),
        OptimizedExpr::Push(expr) => OptimizedExpr::Push(Box::new(coalesce_expr(*expr))),
        #[cfg(feature = "grammar-extras")]
        OptimizedExpr::NodeTag(expr, tag) => {
            OptimizedExpr::NodeTag(Box::new(coalesce_expr(*expr)), tag)
        }
        OptimizedExpr::RestoreOnErr(expr) => {
            OptimizedExpr::RestoreOnErr(Box::new(coalesce_expr(*expr)))
        }
        expr => expr,
    }
}

fn coalesce_choice(expr: OptimizedExpr) -> OptimizedExpr {
    let alternatives = flatten_choice(expr);
    let qualified: Vec<Option<Vec<(char, char)>>> = alternatives.iter().map(char_ranges).collect();
    let all_qualify = qualified.iter().all(|ranges| ranges.is_some());

    if all_qualify {
        let collected = qualified.into_iter().flatten().flatten().collect();
        if let Some(coalesced) = emit_class(collected, alternatives.len()) {
            return coalesced;
        }
        return rebuild_choice(
            alternatives
                .into_iter()
                .map(coalesce_expr)
                .collect::<Vec<_>>(),
        );
    }

    let mut pieces = Vec::new();
    let mut index = 0;
    while index < alternatives.len() {
        if qualified[index].is_some() {
            let start = index;
            while index < alternatives.len() && qualified[index].is_some() {
                index += 1;
            }
            let run_len = index - start;
            if run_len >= 3 {
                let collected = qualified[start..index]
                    .iter()
                    .flatten()
                    .flatten()
                    .copied()
                    .collect();
                if let Some(coalesced) = emit_class(collected, run_len) {
                    pieces.push(coalesced);
                    continue;
                }
            }
            pieces.extend(
                alternatives[start..index]
                    .iter()
                    .cloned()
                    .map(coalesce_expr),
            );
        } else {
            pieces.push(coalesce_expr(alternatives[index].clone()));
            index += 1;
        }
    }
    rebuild_choice(pieces)
}

fn negated_class(lhs: &OptimizedExpr, rhs: &OptimizedExpr) -> Option<OptimizedExpr> {
    let OptimizedExpr::Ident(name) = rhs else {
        return None;
    };
    if name != "ANY" {
        return None;
    }
    let OptimizedExpr::NegPred(inner) = lhs else {
        return None;
    };

    let alternatives = flatten_choice((**inner).clone());
    let mut collected = Vec::new();
    for alternative in &alternatives {
        collected.extend(char_ranges(alternative)?);
    }
    let merged = merge_ranges(collected);
    if merged.is_empty() {
        return None;
    }
    Some(OptimizedExpr::NegCharClass(ranges_to_strings(merged)))
}

fn flatten_choice(expr: OptimizedExpr) -> Vec<OptimizedExpr> {
    match expr {
        OptimizedExpr::Choice(lhs, rhs) => {
            let mut alternatives = flatten_choice(*lhs);
            alternatives.extend(flatten_choice(*rhs));
            alternatives
        }
        other => vec![other],
    }
}

fn rebuild_choice(alternatives: Vec<OptimizedExpr>) -> OptimizedExpr {
    let mut iter = alternatives.into_iter().rev();
    let mut expr = iter.next().expect("choice has an alternative");
    for alternative in iter {
        expr = OptimizedExpr::Choice(Box::new(alternative), Box::new(expr));
    }
    expr
}

/// Ranges contributed by one qualifying choice alternative.
fn char_ranges(expr: &OptimizedExpr) -> Option<Vec<(char, char)>> {
    let expr = match expr {
        OptimizedExpr::RestoreOnErr(inner) => inner.as_ref(),
        other => other,
    };
    match expr {
        OptimizedExpr::Str(string) => singleton(string),
        OptimizedExpr::Insens(string) => {
            let mut chars = string.chars();
            let ch = chars.next()?;
            if chars.next().is_some() {
                return None;
            }
            Some(case_expand(ch))
        }
        OptimizedExpr::Range(start, end) => {
            let start = start.chars().next()?;
            let end = end.chars().next()?;
            Some(vec![normalize(start, end)])
        }
        OptimizedExpr::CharClass(ranges) => {
            let mut collected = Vec::with_capacity(ranges.len());
            for (start, end) in ranges {
                let start = start.chars().next()?;
                let end = end.chars().next()?;
                collected.push(normalize(start, end));
            }
            Some(collected)
        }
        _ => None,
    }
}

fn singleton(string: &str) -> Option<Vec<(char, char)>> {
    let mut chars = string.chars();
    let ch = chars.next()?;
    if chars.next().is_some() {
        None
    } else {
        Some(vec![(ch, ch)])
    }
}

fn case_expand(ch: char) -> Vec<(char, char)> {
    if !ch.is_alphabetic() {
        return vec![(ch, ch)];
    }
    let upper = single_case_map(ch, true);
    let lower = single_case_map(ch, false);
    let mut ranges = Vec::new();
    if let Some(upper) = upper {
        ranges.push((upper, upper));
    }
    if let Some(lower) = lower {
        if ranges.iter().all(|(start, _)| *start != lower) {
            ranges.push((lower, lower));
        }
    }
    if ranges.is_empty() {
        ranges.push((ch, ch));
    }
    ranges
}

fn single_case_map(ch: char, upper: bool) -> Option<char> {
    let mapped: Vec<char> = if upper {
        ch.to_uppercase().collect()
    } else {
        ch.to_lowercase().collect()
    };
    if mapped.len() == 1 {
        Some(mapped[0])
    } else {
        None
    }
}

fn normalize(start: char, end: char) -> (char, char) {
    if start <= end {
        (start, end)
    } else {
        (end, start)
    }
}

fn merge_ranges(mut ranges: Vec<(char, char)>) -> Vec<(char, char)> {
    if ranges.is_empty() {
        return ranges;
    }
    ranges.sort_by_key(|(start, _)| *start as u32);
    let mut merged = Vec::new();
    let (mut current_start, mut current_end) = ranges[0];
    for (start, end) in ranges.into_iter().skip(1) {
        let overlaps_or_adjacent = start <= current_end
            || (current_end != char::MAX && start as u32 == current_end as u32 + 1);
        if overlaps_or_adjacent {
            if end > current_end {
                current_end = end;
            }
        } else {
            merged.push((current_start, current_end));
            current_start = start;
            current_end = end;
        }
    }
    merged.push((current_start, current_end));
    merged
}

fn emit_class(ranges: Vec<(char, char)>, alternative_count: usize) -> Option<OptimizedExpr> {
    let merged = merge_ranges(ranges);
    if merged.is_empty() || merged.len() >= alternative_count {
        return None;
    }
    if merged.len() == 1 {
        let (start, end) = merged[0];
        if start == end {
            Some(OptimizedExpr::Str(start.to_string()))
        } else {
            Some(OptimizedExpr::Range(start.to_string(), end.to_string()))
        }
    } else {
        Some(OptimizedExpr::CharClass(ranges_to_strings(merged)))
    }
}

fn ranges_to_strings(ranges: Vec<(char, char)>) -> Vec<(String, String)> {
    ranges
        .into_iter()
        .map(|(start, end)| (start.to_string(), end.to_string()))
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn choice(alts: Vec<OptimizedExpr>) -> OptimizedExpr {
        let mut iter = alts.into_iter().rev();
        let mut expr = iter.next().unwrap();
        for alternative in iter {
            expr = OptimizedExpr::Choice(Box::new(alternative), Box::new(expr));
        }
        expr
    }

    fn rule(expr: OptimizedExpr) -> OptimizedRule {
        OptimizedRule {
            name: "rule".to_owned(),
            ty: crate::ast::RuleType::Atomic,
            expr,
        }
    }

    #[test]
    fn coalesces_adjacent_chars_into_range() {
        let expr = choice(vec![
            OptimizedExpr::Str("a".to_owned()),
            OptimizedExpr::Str("b".to_owned()),
            OptimizedExpr::Str("c".to_owned()),
        ]);
        let coalesced = coalesce(rule(expr));
        assert_eq!(
            coalesced.expr,
            OptimizedExpr::Range("a".to_owned(), "c".to_owned())
        );
    }

    #[test]
    fn equal_endpoints_become_str() {
        let expr = choice(vec![
            OptimizedExpr::Str("a".to_owned()),
            OptimizedExpr::Str("a".to_owned()),
        ]);
        assert_eq!(
            coalesce(rule(expr)).expr,
            OptimizedExpr::Str("a".to_owned())
        );
    }

    #[test]
    fn keeps_choice_when_merge_does_not_shrink() {
        let expr = choice(vec![
            OptimizedExpr::Str("a".to_owned()),
            OptimizedExpr::Str("c".to_owned()),
            OptimizedExpr::Str("e".to_owned()),
        ]);
        let coalesced = coalesce(rule(expr.clone()));
        assert_eq!(coalesced.expr, expr);
    }

    #[test]
    fn partial_runs_need_three() {
        let expr = choice(vec![
            OptimizedExpr::Str("a".to_owned()),
            OptimizedExpr::Str("b".to_owned()),
            OptimizedExpr::Ident("digit".to_owned()),
            OptimizedExpr::Str("x".to_owned()),
            OptimizedExpr::Str("y".to_owned()),
            OptimizedExpr::Str("z".to_owned()),
        ]);
        let coalesced = coalesce(rule(expr));
        assert_eq!(
            coalesced.expr,
            choice(vec![
                OptimizedExpr::Str("a".to_owned()),
                OptimizedExpr::Str("b".to_owned()),
                OptimizedExpr::Ident("digit".to_owned()),
                OptimizedExpr::Range("x".to_owned(), "z".to_owned()),
            ])
        );
    }

    #[test]
    fn insens_letters_cover_both_cases_and_sort() {
        let expr = choice(vec![
            OptimizedExpr::Insens("c".to_owned()),
            OptimizedExpr::Insens("a".to_owned()),
            OptimizedExpr::Insens("b".to_owned()),
        ]);
        assert_eq!(
            coalesce(rule(expr)).expr,
            OptimizedExpr::CharClass(vec![
                ("A".to_owned(), "C".to_owned()),
                ("a".to_owned(), "c".to_owned()),
            ])
        );
    }

    #[test]
    fn overlapping_and_adjacent_ranges_merge() {
        let expr = choice(vec![
            OptimizedExpr::Range("a".to_owned(), "c".to_owned()),
            OptimizedExpr::Range("b".to_owned(), "e".to_owned()),
            OptimizedExpr::Str("f".to_owned()),
            OptimizedExpr::Range("0".to_owned(), "9".to_owned()),
        ]);
        assert_eq!(
            coalesce(rule(expr)).expr,
            OptimizedExpr::CharClass(vec![
                ("0".to_owned(), "9".to_owned()),
                ("a".to_owned(), "f".to_owned()),
            ])
        );
    }

    #[test]
    fn strips_restore_on_err_and_absorbs_char_class() {
        let expr = choice(vec![
            OptimizedExpr::RestoreOnErr(Box::new(OptimizedExpr::Str("a".to_owned()))),
            OptimizedExpr::CharClass(vec![
                ("c".to_owned(), "c".to_owned()),
                ("e".to_owned(), "g".to_owned()),
            ]),
            OptimizedExpr::Str("b".to_owned()),
            OptimizedExpr::Range("d".to_owned(), "d".to_owned()),
        ]);
        assert_eq!(
            coalesce(rule(expr)).expr,
            OptimizedExpr::Range("a".to_owned(), "g".to_owned())
        );
    }

    #[test]
    fn negated_predicate_followed_by_any() {
        let expr = OptimizedExpr::Seq(
            Box::new(OptimizedExpr::NegPred(Box::new(choice(vec![
                OptimizedExpr::Str("a".to_owned()),
                OptimizedExpr::Range("c".to_owned(), "e".to_owned()),
                OptimizedExpr::Str("b".to_owned()),
            ])))),
            Box::new(OptimizedExpr::Ident("ANY".to_owned())),
        );
        assert_eq!(
            coalesce(rule(expr)).expr,
            OptimizedExpr::NegCharClass(vec![("a".to_owned(), "e".to_owned())])
        );
    }
}
