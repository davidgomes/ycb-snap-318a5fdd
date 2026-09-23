// pest. The Elegant Parser
// Copyright (c) 2018 Dragoș Tiselice
//
// Licensed under the Apache License, Version 2.0
// <LICENSE-APACHE or http://www.apache.org/licenses/LICENSE-2.0> or the MIT
// license <LICENSE-MIT or http://opensource.org/licenses/MIT>, at your
// option. All files in the project carrying such notice may not be copied,
// modified, or distributed except according to those terms.

use super::{OptimizedExpr, OptimizedRule};

/// Final optimizer pass. Choice chains of character alternatives are collapsed
/// top-down into ranges and character classes.
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
        OptimizedExpr::Choice(lhs, rhs) => coalesce_choice(*lhs, *rhs),
        OptimizedExpr::Seq(lhs, rhs) => {
            if let Some(rewritten) = negated_any(lhs.as_ref(), rhs.as_ref()) {
                coalesce_expr(rewritten)
            } else {
                OptimizedExpr::Seq(Box::new(coalesce_expr(*lhs)), Box::new(coalesce_expr(*rhs)))
            }
        }
        OptimizedExpr::PosPred(expr) => OptimizedExpr::PosPred(Box::new(coalesce_expr(*expr))),
        OptimizedExpr::NegPred(expr) => OptimizedExpr::NegPred(Box::new(coalesce_expr(*expr))),
        OptimizedExpr::Rep(expr) => OptimizedExpr::Rep(Box::new(coalesce_expr(*expr))),
        OptimizedExpr::Opt(expr) => OptimizedExpr::Opt(Box::new(coalesce_expr(*expr))),
        OptimizedExpr::Push(expr) => OptimizedExpr::Push(Box::new(coalesce_expr(*expr))),
        OptimizedExpr::RestoreOnErr(expr) => {
            OptimizedExpr::RestoreOnErr(Box::new(coalesce_expr(*expr)))
        }
        #[cfg(feature = "grammar-extras")]
        OptimizedExpr::RepOnce(expr) => OptimizedExpr::RepOnce(Box::new(coalesce_expr(*expr))),
        #[cfg(feature = "grammar-extras")]
        OptimizedExpr::NodeTag(expr, tag) => {
            OptimizedExpr::NodeTag(Box::new(coalesce_expr(*expr)), tag)
        }
        other => other,
    }
}

/// Flatten the whole choice chain once. A qualifying suffix must not be
/// revisited as its own fully-qualifying choice, or runs shorter than three
/// next to a non-qualifying alternative would collapse anyway.
fn coalesce_choice(lhs: OptimizedExpr, rhs: OptimizedExpr) -> OptimizedExpr {
    let expr = OptimizedExpr::Choice(Box::new(lhs), Box::new(rhs));
    let mut alts = Vec::new();
    flatten_alts(&expr, &mut alts);
    if let Some(pieces) = coalesce_pieces(&alts) {
        distribute(rebuild(pieces))
    } else {
        match expr {
            OptimizedExpr::Choice(lhs, rhs) => {
                OptimizedExpr::Choice(Box::new(walk_alt(*lhs)), Box::new(walk_alt(*rhs)))
            }
            other => other,
        }
    }
}

fn walk_alt(expr: OptimizedExpr) -> OptimizedExpr {
    match expr {
        OptimizedExpr::Choice(lhs, rhs) => {
            OptimizedExpr::Choice(Box::new(walk_alt(*lhs)), Box::new(walk_alt(*rhs)))
        }
        other => coalesce_expr(other),
    }
}

fn distribute(expr: OptimizedExpr) -> OptimizedExpr {
    match expr {
        OptimizedExpr::Choice(lhs, rhs) => {
            OptimizedExpr::Choice(Box::new(distribute(*lhs)), Box::new(distribute(*rhs)))
        }
        other => coalesce_expr(other),
    }
}

fn flatten_alts(expr: &OptimizedExpr, out: &mut Vec<OptimizedExpr>) {
    match expr {
        OptimizedExpr::Choice(lhs, rhs) => {
            flatten_alts(lhs, out);
            flatten_alts(rhs, out);
        }
        other => out.push(other.clone()),
    }
}

fn rebuild(alts: Vec<OptimizedExpr>) -> OptimizedExpr {
    let mut iter = alts.into_iter().rev();
    let mut expr = iter.next().expect("choice alternative");
    for alt in iter {
        expr = OptimizedExpr::Choice(Box::new(alt), Box::new(expr));
    }
    expr
}

fn coalesce_pieces(alts: &[OptimizedExpr]) -> Option<Vec<OptimizedExpr>> {
    if alts.iter().all(|alt| qualifying_ranges(alt).is_some()) {
        let merged = merge_alts(alts);
        return emit_positive(&merged, alts.len()).map(|expr| vec![expr]);
    }

    let mut changed = false;
    let mut output = Vec::new();
    let mut index = 0;
    while index < alts.len() {
        if qualifying_ranges(&alts[index]).is_some() {
            let start = index;
            index += 1;
            while index < alts.len() && qualifying_ranges(&alts[index]).is_some() {
                index += 1;
            }
            let run = &alts[start..index];
            if run.len() >= 3 {
                let merged = merge_alts(run);
                if let Some(expr) = emit_positive(&merged, run.len()) {
                    output.push(expr);
                    changed = true;
                    continue;
                }
            }
            output.extend(run.iter().cloned());
        } else {
            output.push(alts[index].clone());
            index += 1;
        }
    }

    if changed {
        Some(output)
    } else {
        None
    }
}

fn merge_alts(alts: &[OptimizedExpr]) -> Vec<(char, char)> {
    let mut ranges = Vec::new();
    for alt in alts {
        if let Some(mut alt_ranges) = qualifying_ranges(alt) {
            ranges.append(&mut alt_ranges);
        }
    }
    merge_ranges(ranges)
}

fn emit_positive(merged: &[(char, char)], alt_count: usize) -> Option<OptimizedExpr> {
    if merged.is_empty() || merged.len() >= alt_count {
        return None;
    }
    Some(if merged.len() == 1 {
        let (start, end) = merged[0];
        if start == end {
            OptimizedExpr::Str(start.to_string())
        } else {
            OptimizedExpr::Range(start.to_string(), end.to_string())
        }
    } else {
        OptimizedExpr::CharClass(to_string_ranges(merged))
    })
}

/// `!qualifying ~ ANY` (ANY may be the head of a longer sequence) becomes
/// `NegCharClass`. The excluded ranges stay a class even when only one range
/// remains; positive coalescing is what simplifies a single range to `Str` or
/// `Range`.
fn negated_any(lhs: &OptimizedExpr, rhs: &OptimizedExpr) -> Option<OptimizedExpr> {
    let inner = match lhs {
        OptimizedExpr::NegPred(inner) => peel_restore(inner),
        _ => return None,
    };
    let rest = any_tail(rhs)?;

    let mut alts = Vec::new();
    if let OptimizedExpr::Choice(_, _) = inner {
        flatten_alts(inner, &mut alts);
    } else {
        alts.push(inner.clone());
    }
    if alts.is_empty() || alts.iter().any(|alt| qualifying_ranges(alt).is_none()) {
        return None;
    }

    let merged = merge_alts(&alts);
    let neg = OptimizedExpr::NegCharClass(to_string_ranges(&merged));
    Some(match rest {
        Some(rest) => OptimizedExpr::Seq(Box::new(neg), Box::new(rest)),
        None => neg,
    })
}

fn any_tail(expr: &OptimizedExpr) -> Option<Option<OptimizedExpr>> {
    match expr {
        OptimizedExpr::Ident(name) if name == "ANY" => Some(None),
        OptimizedExpr::Seq(head, tail) => match head.as_ref() {
            OptimizedExpr::Ident(name) if name == "ANY" => Some(Some((**tail).clone())),
            _ => None,
        },
        _ => None,
    }
}

fn qualifying_ranges(expr: &OptimizedExpr) -> Option<Vec<(char, char)>> {
    let expr = peel_restore(expr);
    match expr {
        OptimizedExpr::Str(string) => single_char(string).map(|c| vec![(c, c)]),
        OptimizedExpr::Insens(string) => single_char(string).map(expand_insens),
        OptimizedExpr::Range(start, end) => Some(vec![(single_char(start)?, single_char(end)?)]),
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

fn peel_restore(expr: &OptimizedExpr) -> &OptimizedExpr {
    let mut expr = expr;
    while let OptimizedExpr::RestoreOnErr(inner) = expr {
        expr = inner;
    }
    expr
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

fn expand_insens(c: char) -> Vec<(char, char)> {
    if c.is_ascii_alphabetic() {
        let lower = c.to_ascii_lowercase();
        let upper = c.to_ascii_uppercase();
        if lower == upper {
            vec![(c, c)]
        } else {
            vec![(upper, upper), (lower, lower)]
        }
    } else {
        vec![(c, c)]
    }
}

fn merge_ranges(mut ranges: Vec<(char, char)>) -> Vec<(char, char)> {
    if ranges.is_empty() {
        return ranges;
    }
    ranges.sort_by_key(|range| (range.0, range.1));
    let mut merged: Vec<(char, char)> = Vec::new();
    for (start, end) in ranges {
        if let Some(last) = merged.last_mut() {
            if overlaps_or_adjacent(last.1, start) {
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

fn overlaps_or_adjacent(end: char, start: char) -> bool {
    if start <= end {
        return true;
    }
    match next_scalar(end) {
        Some(next) => start <= next,
        None => false,
    }
}

fn next_scalar(c: char) -> Option<char> {
    let mut code = c as u32 + 1;
    if code == 0xD800 {
        code = 0xE000;
    }
    char::from_u32(code)
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
    use crate::ast::{Expr, Rule, RuleType};
    use crate::optimizer::optimize;
    use crate::optimizer::OptimizedExpr::*;

    fn expr_of(rules: Vec<Rule>) -> OptimizedExpr {
        optimize(rules).pop().unwrap().expr
    }

    fn choice_of(alts: Vec<Expr>) -> Expr {
        let mut iter = alts.into_iter().rev();
        let mut expr = iter.next().unwrap();
        for alt in iter {
            expr = Expr::Choice(Box::new(alt), Box::new(expr));
        }
        expr
    }

    fn rule(expr: Expr) -> Vec<Rule> {
        vec![Rule {
            name: "rule".to_owned(),
            ty: RuleType::Normal,
            expr,
        }]
    }

    fn ch(c: char) -> Expr {
        Expr::Str(c.to_string())
    }

    fn direct(expr: OptimizedExpr) -> OptimizedExpr {
        coalesce(OptimizedRule {
            name: "rule".to_owned(),
            ty: RuleType::Atomic,
            expr,
        })
        .expr
    }

    #[test]
    fn adjacent_chars_become_range() {
        let optimized = expr_of(rule(choice_of(vec![ch('a'), ch('b'), ch('c')])));
        assert_eq!(optimized, Range("a".to_owned(), "c".to_owned()));
    }

    #[test]
    fn two_adjacent_chars_become_range() {
        let optimized = expr_of(rule(choice_of(vec![ch('a'), ch('b')])));
        assert_eq!(optimized, Range("a".to_owned(), "b".to_owned()));
    }

    #[test]
    fn identical_chars_become_str() {
        let optimized = expr_of(rule(choice_of(vec![ch('a'), ch('a'), ch('a')])));
        assert_eq!(optimized, Str("a".to_owned()));
    }

    #[test]
    fn disjoint_chars_stay_choice_when_not_fewer() {
        let optimized = expr_of(rule(choice_of(vec![ch('a'), ch('c'), ch('e')])));
        assert_eq!(
            optimized,
            box_tree!(Choice(
                Str("a".to_owned()),
                Choice(Str("c".to_owned()), Str("e".to_owned()))
            ))
        );
    }

    #[test]
    fn clusters_become_sorted_char_class() {
        let optimized = expr_of(rule(choice_of(vec![ch('d'), ch('e'), ch('a'), ch('b')])));
        assert_eq!(
            optimized,
            CharClass(vec![
                ("a".to_owned(), "b".to_owned()),
                ("d".to_owned(), "e".to_owned())
            ])
        );
    }

    #[test]
    fn overlapping_ranges_merge_and_sort() {
        let optimized = expr_of(rule(choice_of(vec![
            Expr::Range("a".to_owned(), "m".to_owned()),
            Expr::Range("c".to_owned(), "z".to_owned()),
            ch('0'),
        ])));
        assert_eq!(
            optimized,
            CharClass(vec![
                ("0".to_owned(), "0".to_owned()),
                ("a".to_owned(), "z".to_owned())
            ])
        );
    }

    #[test]
    fn insens_letters_expand_to_both_cases() {
        let optimized = expr_of(rule(choice_of(vec![
            Expr::Insens("a".to_owned()),
            Expr::Insens("b".to_owned()),
            Expr::Insens("c".to_owned()),
        ])));
        assert_eq!(
            optimized,
            CharClass(vec![
                ("A".to_owned(), "C".to_owned()),
                ("a".to_owned(), "c".to_owned())
            ])
        );
    }

    #[test]
    fn insens_non_letters_do_not_expand() {
        let optimized = expr_of(rule(choice_of(vec![
            Expr::Insens("1".to_owned()),
            ch('1'),
            ch('2'),
        ])));
        assert_eq!(optimized, Range("1".to_owned(), "2".to_owned()));
    }

    #[test]
    fn unicode_insens_stays_single_case() {
        let optimized = expr_of(rule(choice_of(vec![
            Expr::Insens("é".to_owned()),
            Expr::Insens("é".to_owned()),
            Expr::Insens("é".to_owned()),
        ])));
        assert_eq!(optimized, Str("é".to_owned()));
    }

    #[test]
    fn partial_run_of_two_is_left_intact() {
        let optimized = expr_of(rule(choice_of(vec![
            Expr::Ident("foo".to_owned()),
            ch('a'),
            ch('b'),
        ])));
        assert_eq!(
            optimized,
            box_tree!(Choice(
                Ident("foo".to_owned()),
                Choice(Str("a".to_owned()), Str("b".to_owned()))
            ))
        );
    }

    #[test]
    fn partial_run_of_three_coalesces() {
        let optimized = expr_of(rule(choice_of(vec![
            Expr::Ident("foo".to_owned()),
            ch('a'),
            ch('b'),
            ch('c'),
            Expr::Ident("bar".to_owned()),
        ])));
        assert_eq!(
            optimized,
            box_tree!(Choice(
                Ident("foo".to_owned()),
                Choice(
                    Range("a".to_owned(), "c".to_owned()),
                    Ident("bar".to_owned())
                )
            ))
        );
    }

    #[test]
    fn partial_disjoint_run_is_not_emitted() {
        let optimized = expr_of(rule(choice_of(vec![
            Expr::Ident("foo".to_owned()),
            ch('a'),
            ch('c'),
            ch('e'),
        ])));
        assert_eq!(
            optimized,
            box_tree!(Choice(
                Ident("foo".to_owned()),
                Choice(
                    Str("a".to_owned()),
                    Choice(Str("c".to_owned()), Str("e".to_owned()))
                )
            ))
        );
    }

    #[test]
    fn multi_char_strings_do_not_qualify() {
        let optimized = expr_of(rule(choice_of(vec![
            Expr::Str("ab".to_owned()),
            Expr::Str("cd".to_owned()),
            Expr::Str("ef".to_owned()),
        ])));
        assert_eq!(
            optimized,
            box_tree!(Choice(
                Str("ab".to_owned()),
                Choice(Str("cd".to_owned()), Str("ef".to_owned()))
            ))
        );
    }

    #[test]
    fn restore_on_err_is_stripped_when_coalesced() {
        let optimized = direct(box_tree!(Choice(
            RestoreOnErr(Str("a".to_owned())),
            Choice(
                RestoreOnErr(Str("b".to_owned())),
                RestoreOnErr(Str("c".to_owned()))
            )
        )));
        assert_eq!(optimized, Range("a".to_owned(), "c".to_owned()));
    }

    #[test]
    fn restore_on_err_survives_short_partial_run() {
        let original = box_tree!(Choice(
            Ident("foo".to_owned()),
            Choice(
                RestoreOnErr(Str("a".to_owned())),
                RestoreOnErr(Str("b".to_owned()))
            )
        ));
        assert_eq!(direct(original.clone()), original);
    }

    #[test]
    fn existing_char_class_ranges_are_absorbed() {
        let optimized = direct(box_tree!(Choice(
            CharClass(vec![
                ("a".to_owned(), "b".to_owned()),
                ("d".to_owned(), "e".to_owned())
            ]),
            Choice(Str("c".to_owned()), Str("f".to_owned()))
        )));
        assert_eq!(optimized, Range("a".to_owned(), "f".to_owned()));
    }

    #[test]
    fn surrogate_gap_ranges_are_adjacent() {
        let optimized = direct(box_tree!(Choice(
            Range("\u{D7FF}".to_owned(), "\u{D7FF}".to_owned()),
            Choice(
                Range("\u{E000}".to_owned(), "\u{E000}".to_owned()),
                Str("a".to_owned())
            )
        )));
        assert_eq!(
            optimized,
            CharClass(vec![
                ("a".to_owned(), "a".to_owned()),
                ("\u{D7FF}".to_owned(), "\u{E000}".to_owned())
            ])
        );
    }

    #[test]
    fn left_nested_non_qualifying_choice_keeps_shape() {
        let original = box_tree!(Choice(
            Choice(Ident("x".to_owned()), Ident("y".to_owned())),
            Ident("z".to_owned())
        ));
        assert_eq!(direct(original.clone()), original);
    }

    #[test]
    fn negated_choice_followed_by_any_becomes_neg_class() {
        let optimized = expr_of(rule(Expr::Seq(
            Box::new(Expr::NegPred(Box::new(choice_of(vec![
                ch('a'),
                ch('b'),
                ch('c'),
            ])))),
            Box::new(Expr::Ident("ANY".to_owned())),
        )));
        assert_eq!(
            optimized,
            NegCharClass(vec![("a".to_owned(), "c".to_owned())])
        );
    }

    #[test]
    fn negated_disjoint_choice_still_becomes_neg_class() {
        let optimized = expr_of(rule(Expr::Seq(
            Box::new(Expr::NegPred(Box::new(choice_of(vec![ch('"'), ch('\\')])))),
            Box::new(Expr::Ident("ANY".to_owned())),
        )));
        assert_eq!(
            optimized,
            NegCharClass(vec![
                ("\"".to_owned(), "\"".to_owned()),
                ("\\".to_owned(), "\\".to_owned())
            ])
        );
    }

    #[test]
    fn negated_single_char_followed_by_any() {
        let optimized = expr_of(rule(Expr::Seq(
            Box::new(Expr::NegPred(Box::new(ch('\'')))),
            Box::new(Expr::Ident("ANY".to_owned())),
        )));
        assert_eq!(
            optimized,
            NegCharClass(vec![("'".to_owned(), "'".to_owned())])
        );
    }

    #[test]
    fn negated_any_keeps_the_trailing_sequence() {
        let optimized = expr_of(rule(Expr::Seq(
            Box::new(Expr::NegPred(Box::new(choice_of(vec![ch('a'), ch('b')])))),
            Box::new(Expr::Seq(
                Box::new(Expr::Ident("ANY".to_owned())),
                Box::new(Expr::Str("x".to_owned())),
            )),
        )));
        assert_eq!(
            optimized,
            box_tree!(Seq(
                NegCharClass(vec![("a".to_owned(), "b".to_owned())]),
                Str("x".to_owned())
            ))
        );
    }

    #[test]
    fn mixed_negation_is_not_a_neg_class() {
        let optimized = expr_of(rule(Expr::Seq(
            Box::new(Expr::NegPred(Box::new(choice_of(vec![
                ch('a'),
                Expr::Ident("foo".to_owned()),
                ch('b'),
            ])))),
            Box::new(Expr::Ident("ANY".to_owned())),
        )));
        assert_eq!(
            optimized,
            box_tree!(Seq(
                NegPred(Choice(
                    Str("a".to_owned()),
                    Choice(Ident("foo".to_owned()), Str("b".to_owned()))
                )),
                Ident("ANY".to_owned())
            ))
        );
    }

    #[test]
    fn nested_choices_coalesce_independently() {
        let optimized = expr_of(rule(Expr::Seq(
            Box::new(choice_of(vec![ch('a'), ch('b'), ch('c')])),
            Box::new(choice_of(vec![ch('d'), ch('e'), ch('f')])),
        )));
        assert_eq!(
            optimized,
            box_tree!(Seq(
                Range("a".to_owned(), "c".to_owned()),
                Range("d".to_owned(), "f".to_owned())
            ))
        );
    }

    #[test]
    fn insens_inside_negation_expands() {
        let optimized = expr_of(rule(Expr::Seq(
            Box::new(Expr::NegPred(Box::new(choice_of(vec![
                Expr::Insens("a".to_owned()),
                ch('b'),
                ch('c'),
            ])))),
            Box::new(Expr::Ident("ANY".to_owned())),
        )));
        assert_eq!(
            optimized,
            NegCharClass(vec![
                ("A".to_owned(), "A".to_owned()),
                ("a".to_owned(), "c".to_owned())
            ])
        );
    }

    #[test]
    fn push_branch_stays_restored_beside_coalesced_chars() {
        let optimized = expr_of(rule(choice_of(vec![
            Expr::Push(Box::new(ch('z'))),
            ch('a'),
            ch('b'),
            ch('c'),
        ])));
        assert_eq!(
            optimized,
            box_tree!(Choice(
                RestoreOnErr(Push(Str("z".to_owned()))),
                Range("a".to_owned(), "c".to_owned())
            ))
        );
    }
}
