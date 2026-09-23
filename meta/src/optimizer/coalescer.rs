// pest. The Elegant Parser
// Copyright (c) 2018 Dragoș Tiselice
//
// Licensed under the Apache License, Version 2.0
// <LICENSE-APACHE or http://www.apache.org/licenses/LICENSE-2.0> or the MIT
// license <LICENSE-MIT or http://opensource.org/licenses/MIT>, at your
// option. All files in the project carrying such notice may not be copied,
// modified, or distributed except according to those terms.

use crate::optimizer::{OptimizedExpr, OptimizedRule};

/// Final optimizer pass. Choice chains of character alternatives collapse into
/// character classes, top-down, after every other pass.
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
        OptimizedExpr::Choice(..) => coalesce_choice(expr),
        OptimizedExpr::Seq(..) => coalesce_seq(expr),
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
        expr => expr,
    }
}

/// Flatten the whole choice chain before rewriting it. A nested `Choice` is part
/// of that same chain, so it must not be coalesced again as its own chain.
fn coalesce_choice(expr: OptimizedExpr) -> OptimizedExpr {
    let mut leaves = Vec::new();
    collect_choice_refs(&expr, &mut leaves);
    if let Some(alts) = coalesced_leaves(&leaves) {
        let alts = alts.into_iter().map(coalesce_expr).collect();
        rebuild_choice(alts)
    } else {
        map_choice_leaves(expr, &mut coalesce_expr)
    }
}

fn coalesce_seq(expr: OptimizedExpr) -> OptimizedExpr {
    let mut items = Vec::new();
    collect_seq_refs(&expr, &mut items);
    if let Some(items) = collapsed_neg_items(&items) {
        let items = items.into_iter().map(coalesce_expr).collect();
        rebuild_seq(items)
    } else {
        map_seq_leaves(expr, &mut coalesce_expr)
    }
}

fn collect_choice_refs<'a>(expr: &'a OptimizedExpr, out: &mut Vec<&'a OptimizedExpr>) {
    match expr {
        OptimizedExpr::Choice(lhs, rhs) => {
            collect_choice_refs(lhs, out);
            collect_choice_refs(rhs, out);
        }
        other => out.push(other),
    }
}

fn collect_seq_refs<'a>(expr: &'a OptimizedExpr, out: &mut Vec<&'a OptimizedExpr>) {
    match expr {
        OptimizedExpr::Seq(lhs, rhs) => {
            collect_seq_refs(lhs, out);
            collect_seq_refs(rhs, out);
        }
        other => out.push(other),
    }
}

fn map_choice_leaves<F>(expr: OptimizedExpr, f: &mut F) -> OptimizedExpr
where
    F: FnMut(OptimizedExpr) -> OptimizedExpr,
{
    match expr {
        OptimizedExpr::Choice(lhs, rhs) => OptimizedExpr::Choice(
            Box::new(map_choice_leaves(*lhs, f)),
            Box::new(map_choice_leaves(*rhs, f)),
        ),
        other => f(other),
    }
}

fn map_seq_leaves<F>(expr: OptimizedExpr, f: &mut F) -> OptimizedExpr
where
    F: FnMut(OptimizedExpr) -> OptimizedExpr,
{
    match expr {
        OptimizedExpr::Seq(lhs, rhs) => OptimizedExpr::Seq(
            Box::new(map_seq_leaves(*lhs, f)),
            Box::new(map_seq_leaves(*rhs, f)),
        ),
        other => f(other),
    }
}

fn rebuild_choice(mut alts: Vec<OptimizedExpr>) -> OptimizedExpr {
    let mut expr = alts.pop().expect("choice chain is empty");
    while let Some(alt) = alts.pop() {
        expr = OptimizedExpr::Choice(Box::new(alt), Box::new(expr));
    }
    expr
}

fn rebuild_seq(mut items: Vec<OptimizedExpr>) -> OptimizedExpr {
    let mut expr = items.pop().expect("sequence is empty");
    while let Some(item) = items.pop() {
        expr = OptimizedExpr::Seq(Box::new(item), Box::new(expr));
    }
    expr
}

/// `None` when no alternative was replaced.
fn coalesced_leaves(alts: &[&OptimizedExpr]) -> Option<Vec<OptimizedExpr>> {
    let quals: Vec<Option<Vec<(char, char)>>> =
        alts.iter().copied().map(qualifying_ranges).collect();
    let all_qualify = quals.iter().all(|ranges| ranges.is_some());

    if all_qualify {
        return try_merge(alts.len(), &quals).map(|expr| vec![expr]);
    }

    let mut changed = false;
    let mut out = Vec::new();
    let mut i = 0;
    while i < alts.len() {
        if quals[i].is_some() {
            let start = i;
            while i < alts.len() && quals[i].is_some() {
                i += 1;
            }
            if i - start >= 3 {
                if let Some(expr) = try_merge(i - start, &quals[start..i]) {
                    out.push(expr);
                    changed = true;
                    continue;
                }
            }
            out.extend(alts[start..i].iter().map(|expr| (*expr).clone()));
        } else {
            out.push(alts[i].clone());
            i += 1;
        }
    }

    if changed {
        Some(out)
    } else {
        None
    }
}

fn try_merge(alt_count: usize, quals: &[Option<Vec<(char, char)>>]) -> Option<OptimizedExpr> {
    let mut ranges = Vec::new();
    for qual in quals {
        ranges.extend(qual.as_ref()?.iter().copied());
    }
    let merged = merge_ranges(ranges);
    if merged.len() < alt_count {
        Some(expr_from_ranges(merged))
    } else {
        None
    }
}

fn expr_from_ranges(ranges: Vec<(char, char)>) -> OptimizedExpr {
    match ranges.as_slice() {
        [(start, end)] if start == end => OptimizedExpr::Str(start.to_string()),
        [(start, end)] => OptimizedExpr::Range(start.to_string(), end.to_string()),
        _ => OptimizedExpr::CharClass(ranges_to_strings(ranges)),
    }
}

fn ranges_to_strings(ranges: Vec<(char, char)>) -> Vec<(String, String)> {
    ranges
        .into_iter()
        .map(|(start, end)| (start.to_string(), end.to_string()))
        .collect()
}

/// `None` when no negated class was collapsed.
fn collapsed_neg_items(items: &[&OptimizedExpr]) -> Option<Vec<OptimizedExpr>> {
    let mut changed = false;
    let mut out = Vec::new();
    let mut i = 0;
    while i < items.len() {
        if i + 1 < items.len() && is_any(items[i + 1]) {
            if let Some(ranges) = negated_class_ranges(items[i]) {
                out.push(OptimizedExpr::NegCharClass(ranges_to_strings(ranges)));
                changed = true;
                i += 2;
                continue;
            }
        }
        out.push(items[i].clone());
        i += 1;
    }
    if changed {
        Some(out)
    } else {
        None
    }
}

fn negated_class_ranges(expr: &OptimizedExpr) -> Option<Vec<(char, char)>> {
    let expr = unwrap_restore(expr);
    let OptimizedExpr::NegPred(inner) = expr else {
        return None;
    };
    let inner = unwrap_restore(inner);
    let mut alts = Vec::new();
    collect_choice_refs(inner, &mut alts);
    if alts.is_empty() {
        return None;
    }
    let mut ranges = Vec::new();
    for alt in alts {
        ranges.extend(qualifying_ranges(alt)?);
    }
    Some(merge_ranges(ranges))
}

fn is_any(expr: &OptimizedExpr) -> bool {
    matches!(unwrap_restore(expr), OptimizedExpr::Ident(name) if name == "ANY")
}

fn qualifying_ranges(expr: &OptimizedExpr) -> Option<Vec<(char, char)>> {
    match unwrap_restore(expr) {
        OptimizedExpr::Str(string) => {
            let ch = single_char(string)?;
            Some(vec![(ch, ch)])
        }
        OptimizedExpr::Insens(string) => {
            let ch = single_char(string)?;
            Some(expand_insens(ch))
        }
        OptimizedExpr::Range(start, end) => {
            let start = single_char(start)?;
            let end = single_char(end)?;
            if start > end {
                Some(Vec::new())
            } else {
                Some(vec![(start, end)])
            }
        }
        OptimizedExpr::CharClass(ranges) => {
            let mut out = Vec::with_capacity(ranges.len());
            for (start, end) in ranges {
                let start = single_char(start)?;
                let end = single_char(end)?;
                if start <= end {
                    out.push((start, end));
                }
            }
            Some(out)
        }
        _ => None,
    }
}

fn unwrap_restore(expr: &OptimizedExpr) -> &OptimizedExpr {
    let mut expr = expr;
    while let OptimizedExpr::RestoreOnErr(inner) = expr {
        expr = inner;
    }
    expr
}

fn single_char(string: &str) -> Option<char> {
    let mut chars = string.chars();
    match (chars.next(), chars.next()) {
        (Some(ch), None) => Some(ch),
        _ => None,
    }
}

/// ASCII case-insensitive letters cover both cases. Other characters stay as written.
fn expand_insens(ch: char) -> Vec<(char, char)> {
    let lower = ch.to_ascii_lowercase();
    let upper = ch.to_ascii_uppercase();
    if lower == upper {
        vec![(ch, ch)]
    } else if upper < lower {
        vec![(upper, upper), (lower, lower)]
    } else {
        vec![(lower, lower), (upper, upper)]
    }
}

fn merge_ranges(mut ranges: Vec<(char, char)>) -> Vec<(char, char)> {
    ranges.retain(|(start, end)| start <= end);
    if ranges.is_empty() {
        return ranges;
    }
    ranges.sort_by_key(|(start, _)| *start);
    let mut merged = Vec::new();
    for (start, end) in ranges {
        if let Some((_, prev_end)) = merged.last_mut() {
            if touches(*prev_end, start) {
                if end > *prev_end {
                    *prev_end = end;
                }
                continue;
            }
        }
        merged.push((start, end));
    }
    merged
}

fn touches(prev_end: char, start: char) -> bool {
    match char_succ(prev_end) {
        Some(next) => start <= next,
        None => true,
    }
}

fn char_succ(ch: char) -> Option<char> {
    let next = (ch as u32).checked_add(1)?;
    let next = if (0xD800..0xE000).contains(&next) {
        0xE000
    } else {
        next
    };
    char::from_u32(next)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::ast::{Expr, Rule, RuleType};
    use crate::optimizer::{optimize, OptimizedExpr};

    fn choice(alts: Vec<OptimizedExpr>) -> OptimizedExpr {
        let mut alts = alts.into_iter().rev();
        let mut expr = alts.next().unwrap();
        for alt in alts {
            expr = OptimizedExpr::Choice(Box::new(alt), Box::new(expr));
        }
        expr
    }

    fn seq(items: Vec<OptimizedExpr>) -> OptimizedExpr {
        let mut items = items.into_iter().rev();
        let mut expr = items.next().unwrap();
        for item in items {
            expr = OptimizedExpr::Seq(Box::new(item), Box::new(expr));
        }
        expr
    }

    fn s(text: &str) -> OptimizedExpr {
        OptimizedExpr::Str(text.to_owned())
    }

    fn ins(text: &str) -> OptimizedExpr {
        OptimizedExpr::Insens(text.to_owned())
    }

    fn range(start: char, end: char) -> OptimizedExpr {
        OptimizedExpr::Range(start.to_string(), end.to_string())
    }

    fn class(ranges: &[(char, char)]) -> OptimizedExpr {
        OptimizedExpr::CharClass(
            ranges
                .iter()
                .map(|(start, end)| (start.to_string(), end.to_string()))
                .collect(),
        )
    }

    fn neg_class(ranges: &[(char, char)]) -> OptimizedExpr {
        OptimizedExpr::NegCharClass(
            ranges
                .iter()
                .map(|(start, end)| (start.to_string(), end.to_string()))
                .collect(),
        )
    }

    fn rule(expr: OptimizedExpr) -> OptimizedRule {
        OptimizedRule {
            name: "rule".to_owned(),
            ty: RuleType::Atomic,
            expr,
        }
    }

    fn coalesced(expr: OptimizedExpr) -> OptimizedExpr {
        coalesce(rule(expr)).expr
    }

    #[test]
    fn adjacent_chars_become_range() {
        assert_eq!(
            coalesced(choice(vec![s("a"), s("b"), s("c")])),
            range('a', 'c')
        );
    }

    #[test]
    fn two_adjacent_chars_become_range() {
        assert_eq!(coalesced(choice(vec![s("a"), s("b")])), range('a', 'b'));
    }

    #[test]
    fn identical_chars_become_str() {
        assert_eq!(coalesced(choice(vec![s("a"), s("a")])), s("a"));
    }

    #[test]
    fn disjoint_pair_stays_choice() {
        let expr = choice(vec![s("a"), s("c")]);
        assert_eq!(coalesced(expr.clone()), expr);
    }

    #[test]
    fn disjoint_triple_stays_choice_when_merge_does_not_shrink() {
        let expr = choice(vec![s("a"), s("c"), s("e")]);
        assert_eq!(coalesced(expr.clone()), expr);
    }

    #[test]
    fn overlapping_and_adjacent_ranges_merge_and_sort() {
        assert_eq!(
            coalesced(choice(vec![
                range('e', 'g'),
                range('a', 'c'),
                range('b', 'f')
            ])),
            range('a', 'g')
        );
    }

    #[test]
    fn partial_run_of_three_coalesces_and_shorter_run_does_not() {
        let expr = choice(vec![
            OptimizedExpr::Ident("foo".to_owned()),
            s("a"),
            s("b"),
            OptimizedExpr::Ident("bar".to_owned()),
            s("d"),
            s("e"),
            s("g"),
        ]);
        assert_eq!(
            coalesced(expr),
            choice(vec![
                OptimizedExpr::Ident("foo".to_owned()),
                s("a"),
                s("b"),
                OptimizedExpr::Ident("bar".to_owned()),
                class(&[('d', 'e'), ('g', 'g')]),
            ])
        );
    }

    #[test]
    fn trailing_pair_in_a_mixed_chain_is_not_coalesced() {
        let expr = choice(vec![OptimizedExpr::Ident("foo".to_owned()), s("a"), s("b")]);
        assert_eq!(coalesced(expr.clone()), expr);
    }

    #[test]
    fn leading_pair_in_a_mixed_chain_is_not_coalesced() {
        let expr = choice(vec![s("a"), s("b"), OptimizedExpr::Ident("foo".to_owned())]);
        assert_eq!(coalesced(expr.clone()), expr);
    }

    #[test]
    fn insens_letters_expand_to_both_cases() {
        assert_eq!(
            coalesced(choice(vec![ins("b"), ins("a"), ins("c")])),
            class(&[('A', 'C'), ('a', 'c')])
        );
    }

    #[test]
    fn insens_pair_that_does_not_shrink_stays() {
        let expr = choice(vec![ins("a"), ins("b")]);
        assert_eq!(coalesced(expr.clone()), expr);
    }

    #[test]
    fn insens_non_letter_merges_with_neighbors() {
        assert_eq!(
            coalesced(choice(vec![ins("1"), s("1"), s("2")])),
            range('1', '2')
        );
    }

    #[test]
    fn multi_char_string_does_not_qualify() {
        let expr = choice(vec![s("aa"), s("bb"), s("cc")]);
        assert_eq!(coalesced(expr.clone()), expr);
    }

    #[test]
    fn existing_char_class_ranges_are_absorbed() {
        assert_eq!(
            coalesced(choice(vec![class(&[('a', 'c'), ('e', 'g')]), s("d")])),
            range('a', 'g')
        );
    }

    #[test]
    fn char_class_that_does_not_shrink_stays() {
        let expr = choice(vec![class(&[('a', 'a'), ('c', 'c')]), s("e")]);
        assert_eq!(coalesced(expr.clone()), expr);
    }

    #[test]
    fn restore_on_err_is_stripped_when_coalesced() {
        let expr = choice(vec![
            OptimizedExpr::RestoreOnErr(Box::new(s("a"))),
            OptimizedExpr::RestoreOnErr(Box::new(s("b"))),
            s("c"),
        ]);
        assert_eq!(coalesced(expr), range('a', 'c'));
    }

    #[test]
    fn restore_on_err_stays_when_the_run_is_not_coalesced() {
        let expr = choice(vec![OptimizedExpr::RestoreOnErr(Box::new(s("a"))), s("c")]);
        assert_eq!(coalesced(expr.clone()), expr);
    }

    #[test]
    fn negated_predicate_followed_by_any_becomes_neg_class() {
        let expr = seq(vec![
            OptimizedExpr::NegPred(Box::new(choice(vec![s("c"), s("a"), s("b")]))),
            OptimizedExpr::Ident("ANY".to_owned()),
        ]);
        assert_eq!(coalesced(expr), neg_class(&[('a', 'c')]));
    }

    #[test]
    fn negated_insens_expands_excluded_ranges() {
        let expr = seq(vec![
            OptimizedExpr::NegPred(Box::new(ins("a"))),
            OptimizedExpr::Ident("ANY".to_owned()),
        ]);
        assert_eq!(coalesced(expr), neg_class(&[('A', 'A'), ('a', 'a')]));
    }

    #[test]
    fn negated_class_keeps_a_single_excluded_character() {
        let expr = seq(vec![
            OptimizedExpr::NegPred(Box::new(s("\n"))),
            OptimizedExpr::Ident("ANY".to_owned()),
        ]);
        assert_eq!(coalesced(expr), neg_class(&[('\n', '\n')]));
    }

    #[test]
    fn negated_predicate_without_any_does_not_collapse() {
        let expr = OptimizedExpr::NegPred(Box::new(choice(vec![s("a"), s("b"), s("c")])));
        assert_eq!(
            coalesced(expr),
            OptimizedExpr::NegPred(Box::new(range('a', 'c')))
        );
    }

    #[test]
    fn mixed_negated_choice_is_not_a_neg_class() {
        let expr = seq(vec![
            OptimizedExpr::NegPred(Box::new(choice(vec![
                s("a"),
                OptimizedExpr::Ident("foo".to_owned()),
                s("b"),
            ]))),
            OptimizedExpr::Ident("ANY".to_owned()),
        ]);
        assert_eq!(coalesced(expr.clone()), expr);
    }

    #[test]
    fn neg_class_in_a_longer_sequence() {
        let expr = seq(vec![
            OptimizedExpr::NegPred(Box::new(s("a"))),
            OptimizedExpr::Ident("ANY".to_owned()),
            s("tail"),
        ]);
        assert_eq!(
            coalesced(expr),
            seq(vec![neg_class(&[('a', 'a')]), s("tail")])
        );
    }

    #[test]
    fn surrogate_gap_is_adjacent() {
        assert_eq!(
            coalesced(choice(vec![s("\u{D7FF}"), s("\u{E000}")])),
            range('\u{D7FF}', '\u{E000}')
        );
    }

    #[test]
    fn nested_choice_inside_repetition() {
        let expr = OptimizedExpr::Rep(Box::new(choice(vec![s("a"), s("b"), s("c")])));
        assert_eq!(
            coalesced(expr),
            OptimizedExpr::Rep(Box::new(range('a', 'c')))
        );
    }

    #[test]
    fn optimize_coalesces_after_restore() {
        let rules = vec![Rule {
            name: "rule".to_owned(),
            ty: RuleType::Normal,
            expr: Expr::Choice(
                Box::new(Expr::Push(Box::new(Expr::Str("x".to_owned())))),
                Box::new(Expr::Choice(
                    Box::new(Expr::Str("a".to_owned())),
                    Box::new(Expr::Choice(
                        Box::new(Expr::Str("b".to_owned())),
                        Box::new(Expr::Str("c".to_owned())),
                    )),
                )),
            ),
        }];
        let optimized = optimize(rules);
        assert_eq!(
            optimized[0].expr,
            OptimizedExpr::Choice(
                Box::new(OptimizedExpr::RestoreOnErr(Box::new(OptimizedExpr::Push(
                    Box::new(s("x"))
                )))),
                Box::new(range('a', 'c')),
            )
        );
    }
}
