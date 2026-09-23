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
