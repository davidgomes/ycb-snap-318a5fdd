// pest. The Elegant Parser
// Copyright (c) 2018 Dragoș Tiselice
//
// Licensed under the Apache License, Version 2.0
// <LICENSE-APACHE or http://www.apache.org/licenses/LICENSE-2.0> or the MIT
// license <LICENSE-MIT or http://opensource.org/licenses/MIT>, at your
// option. All files in the project carrying such notice may not be copied,
// modified, or distributed except according to those terms.

//! Collapse choice chains of character predicates into `CharClass` / `NegCharClass`.

use super::OptimizedExpr;

pub fn coalesce(rule: super::OptimizedRule) -> super::OptimizedRule {
    let super::OptimizedRule { name, ty, expr } = rule;
    super::OptimizedRule {
        name,
        ty,
        expr: map_coalesce(expr),
    }
}

/// Top-down walk. Choice spines are flattened and coalesced before their
/// alternatives are walked, so a chain is judged as a whole.
fn map_coalesce(expr: OptimizedExpr) -> OptimizedExpr {
    match expr {
        OptimizedExpr::Choice(lhs, rhs) => {
            let mut alts = Vec::new();
            flatten_choice(OptimizedExpr::Choice(lhs, rhs), &mut alts);
            let alts = coalesce_alts(alts);
            let alts: Vec<_> = alts.into_iter().map(map_coalesce).collect();
            rebuild_choice(alts)
        }
        OptimizedExpr::Seq(lhs, rhs) => {
            if let Some(expr) = try_negated_class(&lhs, &rhs) {
                return map_coalesce(expr);
            }
            OptimizedExpr::Seq(Box::new(map_coalesce(*lhs)), Box::new(map_coalesce(*rhs)))
        }
        OptimizedExpr::PosPred(expr) => OptimizedExpr::PosPred(Box::new(map_coalesce(*expr))),
        OptimizedExpr::NegPred(expr) => OptimizedExpr::NegPred(Box::new(map_coalesce(*expr))),
        OptimizedExpr::Opt(expr) => OptimizedExpr::Opt(Box::new(map_coalesce(*expr))),
        OptimizedExpr::Rep(expr) => OptimizedExpr::Rep(Box::new(map_coalesce(*expr))),
        OptimizedExpr::Push(expr) => OptimizedExpr::Push(Box::new(map_coalesce(*expr))),
        OptimizedExpr::RestoreOnErr(expr) => {
            OptimizedExpr::RestoreOnErr(Box::new(map_coalesce(*expr)))
        }
        #[cfg(feature = "grammar-extras")]
        OptimizedExpr::RepOnce(expr) => OptimizedExpr::RepOnce(Box::new(map_coalesce(*expr))),
        #[cfg(feature = "grammar-extras")]
        OptimizedExpr::NodeTag(expr, tag) => {
            OptimizedExpr::NodeTag(Box::new(map_coalesce(*expr)), tag)
        }
        other => other,
    }
}

fn flatten_choice(expr: OptimizedExpr, out: &mut Vec<OptimizedExpr>) {
    match expr {
        OptimizedExpr::Choice(lhs, rhs) => {
            flatten_choice(*lhs, out);
            flatten_choice(*rhs, out);
        }
        other => out.push(other),
    }
}

fn rebuild_choice(alts: Vec<OptimizedExpr>) -> OptimizedExpr {
    let mut iter = alts.into_iter().rev();
    let mut acc = iter.next().expect("choice alternative");
    for alt in iter {
        acc = OptimizedExpr::Choice(Box::new(alt), Box::new(acc));
    }
    acc
}

fn coalesce_alts(alts: Vec<OptimizedExpr>) -> Vec<OptimizedExpr> {
    if alts.len() < 2 {
        return alts;
    }

    let quals: Vec<Option<Vec<(char, char)>>> = alts.iter().map(qualifying_ranges).collect();
    if quals.iter().all(|q| q.is_some()) {
        let ranges = collect_ranges(&quals);
        if let Some(expr) = emit_positive(&ranges, alts.len()) {
            return vec![expr];
        }
        return alts;
    }

    let mut out = Vec::new();
    let mut i = 0;
    while i < alts.len() {
        if quals[i].is_some() {
            let start = i;
            i += 1;
            while i < alts.len() && quals[i].is_some() {
                i += 1;
            }
            let run_len = i - start;
            if run_len >= 3 {
                let ranges = collect_ranges(&quals[start..i]);
                if let Some(expr) = emit_positive(&ranges, run_len) {
                    out.push(expr);
                    continue;
                }
            }
            out.extend(alts[start..i].iter().cloned());
        } else {
            out.push(alts[i].clone());
            i += 1;
        }
    }
    out
}

fn collect_ranges(quals: &[Option<Vec<(char, char)>>]) -> Vec<(char, char)> {
    let mut ranges = Vec::new();
    for qual in quals.iter().flatten() {
        ranges.extend(qual.iter().copied());
    }
    ranges
}

/// `None` when merging does not strictly reduce the alternative count.
fn emit_positive(ranges: &[(char, char)], alt_count: usize) -> Option<OptimizedExpr> {
    let merged = merge_ranges(ranges);
    if merged.is_empty() || merged.len() >= alt_count {
        return None;
    }
    Some(positive_expr(merged))
}

fn positive_expr(merged: Vec<(char, char)>) -> OptimizedExpr {
    if merged.len() == 1 {
        let (start, end) = merged[0];
        if start == end {
            OptimizedExpr::Str(start.to_string())
        } else {
            OptimizedExpr::Range(start.to_string(), end.to_string())
        }
    } else {
        OptimizedExpr::CharClass(ranges_to_strings(merged))
    }
}

fn try_negated_class(lhs: &OptimizedExpr, rhs: &OptimizedExpr) -> Option<OptimizedExpr> {
    let inner = match lhs {
        OptimizedExpr::NegPred(inner) => inner.as_ref(),
        _ => return None,
    };

    let tail = match rhs {
        OptimizedExpr::Ident(name) if name == "ANY" => None,
        OptimizedExpr::Seq(head, rest)
            if matches!(head.as_ref(), OptimizedExpr::Ident(name) if name == "ANY") =>
        {
            Some(rest.as_ref())
        }
        _ => return None,
    };

    let mut alts = Vec::new();
    flatten_choice(inner.clone(), &mut alts);
    if alts.is_empty() {
        return None;
    }

    let mut ranges = Vec::new();
    for alt in &alts {
        ranges.extend(qualifying_ranges(alt)?);
    }
    let merged = merge_ranges(&ranges);
    if merged.is_empty() {
        return None;
    }

    let neg = OptimizedExpr::NegCharClass(ranges_to_strings(merged));
    Some(match tail {
        Some(rest) => OptimizedExpr::Seq(Box::new(neg), Box::new(rest.clone())),
        None => neg,
    })
}

/// Ranges contributed by a qualifying choice alternative.
///
/// `RestoreOnErr` is transparent: the wrapper is not part of the coalesced result.
fn qualifying_ranges(expr: &OptimizedExpr) -> Option<Vec<(char, char)>> {
    let inner = match expr {
        OptimizedExpr::RestoreOnErr(inner) => inner.as_ref(),
        other => other,
    };

    match inner {
        OptimizedExpr::Str(string) => single_char(string).map(|c| vec![(c, c)]),
        OptimizedExpr::Insens(string) => single_char(string).map(expand_insens),
        OptimizedExpr::Range(start, end) => {
            let start = single_char(start)?;
            let end = single_char(end)?;
            Some(vec![(start, end)])
        }
        OptimizedExpr::CharClass(ranges) => {
            let mut out = Vec::with_capacity(ranges.len());
            for (start, end) in ranges {
                out.push((single_char(start)?, single_char(end)?));
            }
            Some(out)
        }
        _ => None,
    }
}

fn single_char(string: &str) -> Option<char> {
    let mut chars = string.chars();
    let c = chars.next()?;
    if chars.next().is_none() {
        Some(c)
    } else {
        None
    }
}

/// ASCII letters match either case. Other characters stay a single code point,
/// matching `eq_ignore_ascii_case`.
fn expand_insens(c: char) -> Vec<(char, char)> {
    if c.is_ascii_alphabetic() {
        let lower = c.to_ascii_lowercase();
        let upper = c.to_ascii_uppercase();
        // ASCII letters always have distinct cases, and uppercase sorts first.
        vec![(upper, upper), (lower, lower)]
    } else {
        vec![(c, c)]
    }
}

fn merge_ranges(ranges: &[(char, char)]) -> Vec<(char, char)> {
    let mut ranges: Vec<(char, char)> = ranges.iter().copied().filter(|(s, e)| s <= e).collect();
    if ranges.is_empty() {
        return ranges;
    }
    ranges.sort_by(|a, b| a.0.cmp(&b.0).then(a.1.cmp(&b.1)));

    let mut merged = Vec::new();
    let (mut cur_start, mut cur_end) = ranges[0];
    for (start, end) in ranges.into_iter().skip(1) {
        if start <= adjacent_end(cur_end) {
            if end > cur_end {
                cur_end = end;
            }
        } else {
            merged.push((cur_start, cur_end));
            cur_start = start;
            cur_end = end;
        }
    }
    merged.push((cur_start, cur_end));
    merged
}

/// Inclusive end of the code point that still touches `c`, skipping surrogates.
fn adjacent_end(c: char) -> char {
    if c == char::MAX {
        return c;
    }
    let mut next = c as u32 + 1;
    if next == 0xD800 {
        next = 0xE000;
    }
    char::from_u32(next).unwrap_or(c)
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
    use crate::ast::{Expr, Rule, RuleType};
    use crate::optimizer::{optimize, OptimizedExpr};

    fn optimized(expr: Expr) -> OptimizedExpr {
        optimize(vec![Rule {
            name: "rule".to_owned(),
            ty: RuleType::Silent,
            expr,
        }])
        .pop()
        .unwrap()
        .expr
    }

    fn choice_strs(chars: &[&str]) -> Expr {
        let mut iter = chars.iter().rev();
        let mut acc = Expr::Str((*iter.next().unwrap()).to_owned());
        for c in iter {
            acc = Expr::Choice(Box::new(Expr::Str((*c).to_owned())), Box::new(acc));
        }
        acc
    }

    #[test]
    fn contiguous_chars_become_range() {
        let expr = optimized(choice_strs(&["a", "b", "c", "d"]));
        assert_eq!(expr, OptimizedExpr::Range("a".to_owned(), "d".to_owned()));
    }

    #[test]
    fn two_adjacent_chars_become_range() {
        let expr = optimized(choice_strs(&["a", "b"]));
        assert_eq!(expr, OptimizedExpr::Range("a".to_owned(), "b".to_owned()));
    }

    #[test]
    fn duplicates_collapse_to_str() {
        let expr = optimized(choice_strs(&["a", "a", "a"]));
        assert_eq!(expr, OptimizedExpr::Str("a".to_owned()));
    }

    #[test]
    fn disjoint_choice_is_char_class_sorted() {
        // "c" | "a" | "b" | "e" merges to 'a'..'c' and 'e', sorted by start.
        let expr = optimized(choice_strs(&["c", "a", "b", "e"]));
        assert_eq!(
            expr,
            OptimizedExpr::CharClass(vec![
                ("a".to_owned(), "c".to_owned()),
                ("e".to_owned(), "e".to_owned()),
            ])
        );
    }

    #[test]
    fn no_coalesce_when_range_count_does_not_drop() {
        let expr = optimized(choice_strs(&["a", "c", "e"]));
        assert!(matches!(expr, OptimizedExpr::Choice(_, _)));
    }

    #[test]
    fn overlapping_ranges_merge() {
        let expr = optimized(Expr::Choice(
            Box::new(Expr::Range("a".to_owned(), "d".to_owned())),
            Box::new(Expr::Choice(
                Box::new(Expr::Range("c".to_owned(), "f".to_owned())),
                Box::new(Expr::Str("b".to_owned())),
            )),
        ));
        assert_eq!(expr, OptimizedExpr::Range("a".to_owned(), "f".to_owned()));
    }

    #[test]
    fn adjacent_ranges_merge() {
        let expr = optimized(Expr::Choice(
            Box::new(Expr::Range("a".to_owned(), "c".to_owned())),
            Box::new(Expr::Range("d".to_owned(), "f".to_owned())),
        ));
        assert_eq!(expr, OptimizedExpr::Range("a".to_owned(), "f".to_owned()));
    }

    #[test]
    fn surrogate_gap_is_adjacent() {
        let expr = optimized(choice_strs(&["\u{D7FF}", "\u{E000}"]));
        assert_eq!(
            expr,
            OptimizedExpr::Range("\u{D7FF}".to_owned(), "\u{E000}".to_owned())
        );
    }

    #[test]
    fn insens_letter_expands_both_cases() {
        let expr = optimized(Expr::Choice(
            Box::new(Expr::Insens("a".to_owned())),
            Box::new(Expr::Choice(
                Box::new(Expr::Str("b".to_owned())),
                Box::new(Expr::Str("c".to_owned())),
            )),
        ));
        assert_eq!(
            expr,
            OptimizedExpr::CharClass(vec![
                ("A".to_owned(), "A".to_owned()),
                ("a".to_owned(), "c".to_owned()),
            ])
        );
    }

    #[test]
    fn insens_non_letter_stays_one_code_point() {
        let expr = optimized(Expr::Choice(
            Box::new(Expr::Insens("!".to_owned())),
            Box::new(Expr::Choice(
                Box::new(Expr::Str("\"".to_owned())),
                Box::new(Expr::Str("#".to_owned())),
            )),
        ));
        assert_eq!(expr, OptimizedExpr::Range("!".to_owned(), "#".to_owned()));
    }

    #[test]
    fn partial_run_needs_three() {
        let expr = optimized(Expr::Choice(
            Box::new(Expr::Str("a".to_owned())),
            Box::new(Expr::Choice(
                Box::new(Expr::Str("b".to_owned())),
                Box::new(Expr::Choice(
                    Box::new(Expr::Ident("foo".to_owned())),
                    Box::new(Expr::Choice(
                        Box::new(Expr::Str("c".to_owned())),
                        Box::new(Expr::Choice(
                            Box::new(Expr::Str("d".to_owned())),
                            Box::new(Expr::Str("e".to_owned())),
                        )),
                    )),
                )),
            )),
        ));
        assert_eq!(
            expr,
            OptimizedExpr::Choice(
                Box::new(OptimizedExpr::Str("a".to_owned())),
                Box::new(OptimizedExpr::Choice(
                    Box::new(OptimizedExpr::Str("b".to_owned())),
                    Box::new(OptimizedExpr::Choice(
                        Box::new(OptimizedExpr::Ident("foo".to_owned())),
                        Box::new(OptimizedExpr::Range("c".to_owned(), "e".to_owned())),
                    )),
                )),
            )
        );
    }

    #[test]
    fn multi_char_string_does_not_qualify() {
        let expr = optimized(choice_strs(&["a", "b", "cd"]));
        assert!(matches!(expr, OptimizedExpr::Choice(_, _)));
    }

    #[test]
    fn negated_choice_followed_by_any() {
        let expr = optimized(Expr::Seq(
            Box::new(Expr::NegPred(Box::new(choice_strs(&["a", "c", "d"])))),
            Box::new(Expr::Ident("ANY".to_owned())),
        ));
        assert_eq!(
            expr,
            OptimizedExpr::NegCharClass(vec![
                ("a".to_owned(), "a".to_owned()),
                ("c".to_owned(), "d".to_owned()),
            ])
        );
    }

    #[test]
    fn negated_class_keeps_following_sequence() {
        let expr = optimized(Expr::Seq(
            Box::new(Expr::NegPred(Box::new(choice_strs(&["a", "b"])))),
            Box::new(Expr::Seq(
                Box::new(Expr::Ident("ANY".to_owned())),
                Box::new(Expr::Str("x".to_owned())),
            )),
        ));
        assert_eq!(
            expr,
            OptimizedExpr::Seq(
                Box::new(OptimizedExpr::NegCharClass(vec![(
                    "a".to_owned(),
                    "b".to_owned()
                )])),
                Box::new(OptimizedExpr::Str("x".to_owned())),
            )
        );
    }

    #[test]
    fn negated_mixed_choice_is_not_a_class() {
        let expr = optimized(Expr::Seq(
            Box::new(Expr::NegPred(Box::new(Expr::Choice(
                Box::new(Expr::Str("a".to_owned())),
                Box::new(Expr::Ident("foo".to_owned())),
            )))),
            Box::new(Expr::Ident("ANY".to_owned())),
        ));
        assert!(matches!(
            expr,
            OptimizedExpr::Seq(ref lhs, _) if matches!(lhs.as_ref(), OptimizedExpr::NegPred(_))
        ));
    }

    #[test]
    fn restore_on_err_wrapper_is_stripped() {
        let expr = OptimizedExpr::Choice(
            Box::new(OptimizedExpr::RestoreOnErr(Box::new(OptimizedExpr::Str(
                "a".to_owned(),
            )))),
            Box::new(OptimizedExpr::Choice(
                Box::new(OptimizedExpr::RestoreOnErr(Box::new(OptimizedExpr::Str(
                    "c".to_owned(),
                )))),
                Box::new(OptimizedExpr::RestoreOnErr(Box::new(OptimizedExpr::Str(
                    "b".to_owned(),
                )))),
            )),
        );
        assert_eq!(
            map_coalesce(expr),
            OptimizedExpr::Range("a".to_owned(), "c".to_owned())
        );
    }

    #[test]
    fn existing_char_class_ranges_are_absorbed() {
        let expr = OptimizedExpr::Choice(
            Box::new(OptimizedExpr::CharClass(vec![
                ("a".to_owned(), "c".to_owned()),
                ("e".to_owned(), "e".to_owned()),
            ])),
            Box::new(OptimizedExpr::Choice(
                Box::new(OptimizedExpr::Str("d".to_owned())),
                Box::new(OptimizedExpr::Str("f".to_owned())),
            )),
        );
        assert_eq!(
            map_coalesce(expr),
            OptimizedExpr::Range("a".to_owned(), "f".to_owned())
        );
    }

    #[test]
    fn insens_that_does_not_reduce_count_stays_choice() {
        // ^"a" contributes 'A' and 'a'; "c" contributes 'c'. 3 ranges, 2 alts.
        let expr = optimized(Expr::Choice(
            Box::new(Expr::Insens("a".to_owned())),
            Box::new(Expr::Str("c".to_owned())),
        ));
        assert!(matches!(expr, OptimizedExpr::Choice(_, _)));
    }
}
