// pest. The Elegant Parser
// Copyright (c) 2018 Dragoș Tiselice
//
// Licensed under the Apache License, Version 2.0
// <LICENSE-APACHE or http://www.apache.org/licenses/LICENSE-2.0> or the MIT
// license <LICENSE-MIT or http://opensource.org/licenses/MIT>, at your
// option. All files in the project carrying such notice may not be copied,
// modified, or distributed except according to those terms.

use crate::optimizer::*;

const MIN_PARTIAL_RUN: usize = 3;

pub fn coalesce(rule: OptimizedRule) -> OptimizedRule {
    let OptimizedRule { name, ty, expr } = rule;
    let expr = expr.map_top_down(coalesce_expr);
    OptimizedRule { name, ty, expr }
}

fn coalesce_expr(expr: OptimizedExpr) -> OptimizedExpr {
    match expr {
        OptimizedExpr::Choice(..) => coalesce_choice(expr),
        OptimizedExpr::Seq(lhs, rhs) => coalesce_negated(*lhs, *rhs),
        expr => expr,
    }
}

fn coalesce_choice(expr: OptimizedExpr) -> OptimizedExpr {
    let mut alternatives = vec![];
    let mut current = expr;
    while let OptimizedExpr::Choice(lhs, rhs) = current {
        alternatives.push(*lhs);
        current = *rhs;
    }
    alternatives.push(current);

    let classified: Vec<_> = alternatives
        .into_iter()
        .map(|alternative| {
            let ranges = char_ranges(&alternative);
            (alternative, ranges)
        })
        .collect();

    if classified.iter().all(|(_, ranges)| ranges.is_some()) {
        let run = classified
            .into_iter()
            .map(|(alternative, ranges)| (alternative, ranges.unwrap_or_default()))
            .collect();
        return rebuild_choice(coalesce_run(run));
    }

    let mut result = vec![];
    let mut run = vec![];
    for (alternative, ranges) in classified {
        match ranges {
            Some(ranges) => run.push((alternative, ranges)),
            None => {
                result.extend(coalesce_partial_run(std::mem::take(&mut run)));
                result.push(alternative);
            }
        }
    }
    result.extend(coalesce_partial_run(run));

    rebuild_choice(result)
}

fn coalesce_partial_run(run: Vec<(OptimizedExpr, Vec<(char, char)>)>) -> Vec<OptimizedExpr> {
    if run.len() >= MIN_PARTIAL_RUN {
        coalesce_run(run)
    } else {
        run.into_iter()
            .map(|(alternative, _)| alternative)
            .collect()
    }
}

fn coalesce_run(run: Vec<(OptimizedExpr, Vec<(char, char)>)>) -> Vec<OptimizedExpr> {
    let merged = merge_ranges(run.iter().flat_map(|(_, ranges)| ranges.iter().copied()));

    if merged.len() >= run.len() {
        return run
            .into_iter()
            .map(|(alternative, _)| alternative)
            .collect();
    }

    let coalesced = match merged.as_slice() {
        [(start, end)] if start == end => OptimizedExpr::Str(start.to_string()),
        [(start, end)] => OptimizedExpr::Range(start.to_string(), end.to_string()),
        _ => OptimizedExpr::CharClass(ranges_to_strings(merged)),
    };

    vec![coalesced]
}

fn rebuild_choice(alternatives: Vec<OptimizedExpr>) -> OptimizedExpr {
    alternatives
        .into_iter()
        .rev()
        .reduce(|rhs, lhs| OptimizedExpr::Choice(Box::new(lhs), Box::new(rhs)))
        .expect("choice must have at least one alternative")
}

fn coalesce_negated(lhs: OptimizedExpr, rhs: OptimizedExpr) -> OptimizedExpr {
    let excluded = match lhs {
        OptimizedExpr::NegPred(ref expr) => negated_ranges(expr),
        _ => None,
    };

    if let Some(excluded) = excluded {
        let class = || OptimizedExpr::NegCharClass(ranges_to_strings(merge_ranges(excluded)));
        match rhs {
            OptimizedExpr::Ident(ref ident) if ident == "ANY" => return class(),
            OptimizedExpr::Seq(any, rest) => {
                if matches!(*any, OptimizedExpr::Ident(ref ident) if ident == "ANY") {
                    return OptimizedExpr::Seq(Box::new(class()), rest);
                }
                return OptimizedExpr::Seq(Box::new(lhs), Box::new(OptimizedExpr::Seq(any, rest)));
            }
            _ => {}
        }
    }

    OptimizedExpr::Seq(Box::new(lhs), Box::new(rhs))
}

fn negated_ranges(expr: &OptimizedExpr) -> Option<Vec<(char, char)>> {
    let mut ranges = vec![];
    let mut current = expr;
    while let OptimizedExpr::Choice(lhs, rhs) = current {
        ranges.extend(char_ranges(lhs)?);
        current = rhs;
    }
    ranges.extend(char_ranges(current)?);
    Some(ranges)
}

/// Returns the character ranges matched by `expr` if it always matches exactly one character.
fn char_ranges(expr: &OptimizedExpr) -> Option<Vec<(char, char)>> {
    match expr {
        OptimizedExpr::Str(string) => single_char(string).map(|c| vec![(c, c)]),
        OptimizedExpr::Insens(string) => {
            // `match_insensitive` only folds ASCII case.
            let c = single_char(string)?;
            if c.is_ascii_alphabetic() {
                let lower = c.to_ascii_lowercase();
                let upper = c.to_ascii_uppercase();
                Some(vec![(upper, upper), (lower, lower)])
            } else {
                Some(vec![(c, c)])
            }
        }
        OptimizedExpr::Range(start, end) => {
            let start = single_char(start)?;
            let end = single_char(end)?;
            (start <= end).then(|| vec![(start, end)])
        }
        OptimizedExpr::CharClass(ranges) => ranges
            .iter()
            .map(|(start, end)| Some((single_char(start)?, single_char(end)?)))
            .collect(),
        OptimizedExpr::RestoreOnErr(expr) => char_ranges(expr),
        _ => None,
    }
}

fn single_char(string: &str) -> Option<char> {
    let mut chars = string.chars();
    match (chars.next(), chars.next()) {
        (Some(c), None) => Some(c),
        _ => None,
    }
}

fn merge_ranges(ranges: impl IntoIterator<Item = (char, char)>) -> Vec<(char, char)> {
    let mut ranges: Vec<_> = ranges.into_iter().collect();
    ranges.sort_unstable();

    let mut merged: Vec<(char, char)> = Vec::with_capacity(ranges.len());
    for (start, end) in ranges {
        match merged.last_mut() {
            Some(last) if start as u32 <= last.1 as u32 + 1 => last.1 = last.1.max(end),
            _ => merged.push((start, end)),
        }
    }
    merged
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
    use crate::optimizer::OptimizedExpr::*;

    fn coalesce_expr_tree(expr: OptimizedExpr) -> OptimizedExpr {
        coalesce(OptimizedRule {
            name: "rule".to_owned(),
            ty: RuleType::Atomic,
            expr,
        })
        .expr
    }

    fn s(string: &str) -> OptimizedExpr {
        Str(string.to_owned())
    }

    fn class(ranges: &[(&str, &str)]) -> Vec<(String, String)> {
        ranges
            .iter()
            .map(|(start, end)| (start.to_string(), end.to_string()))
            .collect()
    }

    #[test]
    fn single_chars_into_range() {
        let expr = box_tree!(Choice(
            Str("b".to_owned()),
            Choice(Str("a".to_owned()), Str("c".to_owned()))
        ));
        assert_eq!(
            coalesce_expr_tree(expr),
            Range("a".to_owned(), "c".to_owned())
        );
    }

    #[test]
    fn identical_chars_into_str() {
        let expr = box_tree!(Choice(
            Str("a".to_owned()),
            Range("a".to_owned(), "a".to_owned())
        ));
        assert_eq!(coalesce_expr_tree(expr), s("a"));
    }

    #[test]
    fn disjoint_ranges_into_char_class() {
        let expr = box_tree!(Choice(
            Range("x".to_owned(), "z".to_owned()),
            Choice(
                Str("a".to_owned()),
                Choice(Str("b".to_owned()), Range("0".to_owned(), "9".to_owned()))
            )
        ));
        assert_eq!(
            coalesce_expr_tree(expr),
            CharClass(class(&[("0", "9"), ("a", "b"), ("x", "z")]))
        );
    }

    #[test]
    fn overlapping_and_adjacent_ranges_merge() {
        let expr = box_tree!(Choice(
            Range("a".to_owned(), "f".to_owned()),
            Choice(
                Range("d".to_owned(), "k".to_owned()),
                Range("l".to_owned(), "m".to_owned())
            )
        ));
        assert_eq!(
            coalesce_expr_tree(expr),
            Range("a".to_owned(), "m".to_owned())
        );
    }

    #[test]
    fn no_reduction_is_left_alone() {
        let expr = box_tree!(Choice(
            Str("a".to_owned()),
            Choice(Str("c".to_owned()), Str("e".to_owned()))
        ));
        assert_eq!(coalesce_expr_tree(expr.clone()), expr);
    }

    #[test]
    fn insensitive_expands_both_cases() {
        let expr = box_tree!(Choice(
            Insens("a".to_owned()),
            Choice(Insens("b".to_owned()), Insens("c".to_owned()))
        ));
        assert_eq!(
            coalesce_expr_tree(expr),
            CharClass(class(&[("A", "C"), ("a", "c")]))
        );
    }

    #[test]
    fn insensitive_non_alphabetic_is_single_char() {
        let expr = box_tree!(Choice(
            Insens("1".to_owned()),
            Choice(Str("2".to_owned()), Str("3".to_owned()))
        ));
        assert_eq!(
            coalesce_expr_tree(expr),
            Range("1".to_owned(), "3".to_owned())
        );
    }

    #[test]
    fn multi_char_strings_do_not_qualify() {
        let expr = box_tree!(Choice(Str("ab".to_owned()), Str("c".to_owned())));
        assert_eq!(coalesce_expr_tree(expr.clone()), expr);
    }

    #[test]
    fn existing_char_class_is_absorbed() {
        let expr = Choice(
            Box::new(CharClass(class(&[("a", "c"), ("x", "z")]))),
            Box::new(Choice(Box::new(s("d")), Box::new(s("w")))),
        );
        assert_eq!(
            coalesce_expr_tree(expr),
            CharClass(class(&[("a", "d"), ("w", "z")]))
        );
    }

    #[test]
    fn restore_on_err_is_stripped() {
        let expr = box_tree!(Choice(
            RestoreOnErr(Str("a".to_owned())),
            Choice(Str("b".to_owned()), RestoreOnErr(Str("c".to_owned())))
        ));
        assert_eq!(
            coalesce_expr_tree(expr),
            Range("a".to_owned(), "c".to_owned())
        );
    }

    #[test]
    fn partial_run_of_three_is_coalesced() {
        let expr = box_tree!(Choice(
            Ident("x".to_owned()),
            Choice(
                Str("a".to_owned()),
                Choice(
                    Str("b".to_owned()),
                    Choice(Str("c".to_owned()), Ident("y".to_owned()))
                )
            )
        ));
        assert_eq!(
            coalesce_expr_tree(expr),
            box_tree!(Choice(
                Ident("x".to_owned()),
                Choice(Range("a".to_owned(), "c".to_owned()), Ident("y".to_owned()))
            ))
        );
    }

    #[test]
    fn partial_run_of_two_is_left_alone() {
        let expr = box_tree!(Choice(
            Str("a".to_owned()),
            Choice(Str("b".to_owned()), Ident("x".to_owned()))
        ));
        assert_eq!(coalesce_expr_tree(expr.clone()), expr);
    }

    #[test]
    fn negated_choice_before_any() {
        let expr = box_tree!(Seq(
            NegPred(Choice(
                Str("b".to_owned()),
                Choice(Str("a".to_owned()), Insens("z".to_owned()))
            )),
            Ident("ANY".to_owned())
        ));
        assert_eq!(
            coalesce_expr_tree(expr),
            NegCharClass(class(&[("Z", "Z"), ("a", "b"), ("z", "z")]))
        );
    }

    #[test]
    fn negated_choice_before_any_in_longer_sequence() {
        let expr = box_tree!(Rep(Seq(
            NegPred(Choice(Str("a".to_owned()), Str("b".to_owned()))),
            Seq(Ident("ANY".to_owned()), Ident("x".to_owned()))
        )));
        assert_eq!(
            coalesce_expr_tree(expr),
            Rep(Box::new(Seq(
                Box::new(NegCharClass(class(&[("a", "b")]))),
                Box::new(Ident("x".to_owned()))
            )))
        );
    }

    #[test]
    fn negated_non_qualifying_is_left_alone() {
        let expr = box_tree!(Seq(
            NegPred(Choice(Str("a".to_owned()), Ident("x".to_owned()))),
            Ident("ANY".to_owned())
        ));
        assert_eq!(coalesce_expr_tree(expr.clone()), expr);
    }
}
