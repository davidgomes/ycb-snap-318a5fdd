// pest. The Elegant Parser
// Copyright (c) 2018 Dragoș Tiselice
//
// Licensed under the Apache License, Version 2.0
// <LICENSE-APACHE or http://www.apache.org/licenses/LICENSE-2.0> or the MIT
// license <LICENSE-MIT or http://opensource.org/licenses/MIT>, at your
// option. All files in the project carrying such notice may not be copied,
// modified, or distributed except according to those terms.

use crate::optimizer::*;

pub fn coalesce(rule: OptimizedRule) -> OptimizedRule {
    let OptimizedRule { name, ty, expr } = rule;
    OptimizedRule {
        name,
        ty,
        expr: walk(expr),
    }
}

fn walk(expr: OptimizedExpr) -> OptimizedExpr {
    let boxed = |e: Box<OptimizedExpr>| Box::new(walk(*e));
    match coalesce_expr(expr) {
        // Alternatives of a coalesced choice must not be regrouped by nested visits.
        choice @ OptimizedExpr::Choice(..) => {
            let mut alts = vec![];
            flatten_choice(choice, &mut alts);
            build_choice(alts.into_iter().map(walk).collect())
        }
        expr => walk_children_with(expr, &boxed),
    }
}

fn walk_children_with<F>(expr: OptimizedExpr, f: &F) -> OptimizedExpr
where
    F: Fn(Box<OptimizedExpr>) -> Box<OptimizedExpr>,
{
    match expr {
        OptimizedExpr::PosPred(e) => OptimizedExpr::PosPred(f(e)),
        OptimizedExpr::NegPred(e) => OptimizedExpr::NegPred(f(e)),
        OptimizedExpr::Seq(l, r) => OptimizedExpr::Seq(f(l), f(r)),
        OptimizedExpr::Choice(l, r) => OptimizedExpr::Choice(f(l), f(r)),
        OptimizedExpr::Opt(e) => OptimizedExpr::Opt(f(e)),
        OptimizedExpr::Rep(e) => OptimizedExpr::Rep(f(e)),
        #[cfg(feature = "grammar-extras")]
        OptimizedExpr::RepOnce(e) => OptimizedExpr::RepOnce(f(e)),
        OptimizedExpr::Push(e) => OptimizedExpr::Push(f(e)),
        #[cfg(feature = "grammar-extras")]
        OptimizedExpr::NodeTag(e, tag) => OptimizedExpr::NodeTag(f(e), tag),
        OptimizedExpr::RestoreOnErr(e) => OptimizedExpr::RestoreOnErr(f(e)),
        expr => expr,
    }
}

fn coalesce_expr(expr: OptimizedExpr) -> OptimizedExpr {
    match expr {
        OptimizedExpr::Choice(..) => coalesce_choice(expr),
        OptimizedExpr::Seq(lhs, rhs) => match (*lhs, *rhs) {
            (OptimizedExpr::NegPred(inner), OptimizedExpr::Ident(ref any)) if any == "ANY" => {
                match all_ranges(&inner) {
                    Some(ranges) => OptimizedExpr::NegCharClass(to_strings(merge(ranges))),
                    None => OptimizedExpr::Seq(
                        Box::new(OptimizedExpr::NegPred(inner)),
                        Box::new(OptimizedExpr::Ident("ANY".to_owned())),
                    ),
                }
            }
            (lhs, rhs) => OptimizedExpr::Seq(Box::new(lhs), Box::new(rhs)),
        },
        expr => expr,
    }
}

type Alt = (OptimizedExpr, Option<Vec<(char, char)>>);

fn flatten_choice(expr: OptimizedExpr, out: &mut Vec<OptimizedExpr>) {
    match expr {
        OptimizedExpr::Choice(lhs, rhs) => {
            flatten_choice(*lhs, out);
            flatten_choice(*rhs, out);
        }
        expr => out.push(expr),
    }
}

fn build_choice(mut alts: Vec<OptimizedExpr>) -> OptimizedExpr {
    let mut result = alts.pop().expect("empty choice");
    while let Some(alt) = alts.pop() {
        result = OptimizedExpr::Choice(Box::new(alt), Box::new(result));
    }
    result
}

fn all_ranges(expr: &OptimizedExpr) -> Option<Vec<(char, char)>> {
    let mut alts = vec![];
    flatten_choice(expr.clone(), &mut alts);
    let mut ranges = vec![];
    for alt in &alts {
        ranges.extend(alt_ranges(alt)?);
    }
    Some(ranges)
}

fn single_char(s: &str) -> Option<char> {
    let mut chars = s.chars();
    match (chars.next(), chars.next()) {
        (Some(c), None) => Some(c),
        _ => None,
    }
}

fn case_variants(c: char) -> Vec<char> {
    let mut out = vec![c];
    if c.is_alphabetic() {
        let lower: String = c.to_lowercase().collect();
        let upper: String = c.to_uppercase().collect();
        out.extend(single_char(&lower));
        out.extend(single_char(&upper));
    }
    out
}

fn alt_ranges(expr: &OptimizedExpr) -> Option<Vec<(char, char)>> {
    match expr {
        OptimizedExpr::Str(s) => single_char(s).map(|c| vec![(c, c)]),
        OptimizedExpr::Insens(s) => {
            single_char(s).map(|c| case_variants(c).into_iter().map(|v| (v, v)).collect())
        }
        OptimizedExpr::Range(start, end) => {
            let start = single_char(start)?;
            let end = single_char(end)?;
            Some(vec![(start, end)])
        }
        OptimizedExpr::CharClass(ranges) => ranges
            .iter()
            .map(|(s, e)| Some((single_char(s)?, single_char(e)?)))
            .collect(),
        OptimizedExpr::RestoreOnErr(inner) => alt_ranges(inner),
        _ => None,
    }
}

fn merge(mut ranges: Vec<(char, char)>) -> Vec<(char, char)> {
    ranges.retain(|(s, e)| s <= e);
    ranges.sort();
    let mut merged: Vec<(char, char)> = vec![];
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

fn to_strings(ranges: Vec<(char, char)>) -> Vec<(String, String)> {
    ranges
        .into_iter()
        .map(|(s, e)| (s.to_string(), e.to_string()))
        .collect()
}

fn to_expr(merged: Vec<(char, char)>) -> OptimizedExpr {
    if merged.len() == 1 {
        let (s, e) = merged[0];
        if s == e {
            OptimizedExpr::Str(s.to_string())
        } else {
            OptimizedExpr::Range(s.to_string(), e.to_string())
        }
    } else {
        OptimizedExpr::CharClass(to_strings(merged))
    }
}

/// Coalesces `alts` if merging yields fewer ranges than alternatives.
fn try_coalesce(alts: &[Alt]) -> Option<OptimizedExpr> {
    let ranges: Vec<(char, char)> = alts
        .iter()
        .flat_map(|(_, r)| r.clone().unwrap_or_default())
        .collect();
    let merged = merge(ranges);
    if merged.is_empty() || merged.len() >= alts.len() {
        return None;
    }
    Some(to_expr(merged))
}

fn coalesce_choice(expr: OptimizedExpr) -> OptimizedExpr {
    let mut flat = vec![];
    flatten_choice(expr, &mut flat);
    let alts: Vec<Alt> = flat
        .into_iter()
        .map(|alt| {
            let ranges = alt_ranges(&alt);
            (alt, ranges)
        })
        .collect();

    if alts.iter().all(|(_, r)| r.is_some()) {
        if let Some(result) = try_coalesce(&alts) {
            return result;
        }
        return build_choice(alts.into_iter().map(|(a, _)| a).collect());
    }

    let mut result = vec![];
    let mut i = 0;
    while i < alts.len() {
        if alts[i].1.is_none() {
            result.push(alts[i].0.clone());
            i += 1;
            continue;
        }
        let start = i;
        while i < alts.len() && alts[i].1.is_some() {
            i += 1;
        }
        let run = &alts[start..i];
        match (run.len() >= 3).then(|| try_coalesce(run)).flatten() {
            Some(expr) => result.push(expr),
            None => result.extend(run.iter().map(|(a, _)| a.clone())),
        }
    }
    build_choice(result)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn s(v: &str) -> OptimizedExpr {
        OptimizedExpr::Str(v.to_owned())
    }

    fn choice(alts: Vec<OptimizedExpr>) -> OptimizedExpr {
        build_choice(alts)
    }

    fn run(expr: OptimizedExpr) -> OptimizedExpr {
        coalesce(OptimizedRule {
            name: "r".to_owned(),
            ty: RuleType::Normal,
            expr,
        })
        .expr
    }

    #[test]
    fn collapses_to_range() {
        assert_eq!(
            run(choice(vec![s("a"), s("b"), s("c")])),
            OptimizedExpr::Range("a".to_owned(), "c".to_owned())
        );
    }

    #[test]
    fn char_class_and_insens() {
        assert_eq!(
            run(choice(vec![
                OptimizedExpr::Insens("x".to_owned()),
                s("y"),
                OptimizedExpr::RestoreOnErr(Box::new(s("0"))),
                OptimizedExpr::Range("1".to_owned(), "9".to_owned()),
            ])),
            OptimizedExpr::CharClass(vec![
                ("0".to_owned(), "9".to_owned()),
                ("X".to_owned(), "X".to_owned()),
                ("x".to_owned(), "y".to_owned()),
            ])
        );
    }

    #[test]
    fn no_gain_keeps_choice() {
        let expr = choice(vec![s("a"), s("c")]);
        assert_eq!(run(expr.clone()), expr);
    }

    #[test]
    fn partial_runs() {
        let id = OptimizedExpr::Ident("x".to_owned());
        assert_eq!(
            run(choice(vec![
                s("a"),
                s("b"),
                s("c"),
                id.clone(),
                s("d"),
                s("e")
            ])),
            choice(vec![
                OptimizedExpr::Range("a".to_owned(), "c".to_owned()),
                id,
                s("d"),
                s("e"),
            ])
        );
    }

    #[test]
    fn neg_char_class() {
        let expr = OptimizedExpr::Seq(
            Box::new(OptimizedExpr::NegPred(Box::new(choice(vec![
                s("b"),
                s("a"),
            ])))),
            Box::new(OptimizedExpr::Ident("ANY".to_owned())),
        );
        assert_eq!(
            run(expr),
            OptimizedExpr::NegCharClass(vec![("a".to_owned(), "b".to_owned())])
        );
    }
}
