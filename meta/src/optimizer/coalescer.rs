use crate::optimizer::{OptimizedExpr, OptimizedRule};

pub fn coalesce(rule: OptimizedRule) -> OptimizedRule {
    let OptimizedRule { name, ty, expr } = rule;
    OptimizedRule {
        name,
        ty,
        expr: expr.map_top_down(coalesce_expr),
    }
}

fn coalesce_expr(expr: OptimizedExpr) -> OptimizedExpr {
    match expr {
        OptimizedExpr::Choice(_, _) => coalesce_choice(expr),
        OptimizedExpr::Seq(lhs, rhs) => {
            if let Some(ranges) = negated_char_class_ranges(&lhs, &rhs) {
                OptimizedExpr::NegCharClass(ranges)
            } else {
                OptimizedExpr::Seq(lhs, rhs)
            }
        }
        expr => expr,
    }
}

fn coalesce_choice(expr: OptimizedExpr) -> OptimizedExpr {
    let original = expr.clone();
    let mut alternatives = Vec::new();
    flatten_choices(expr, &mut alternatives);
    let qualifications = alternatives.iter().map(char_ranges).collect::<Vec<_>>();
    let all_qualify = qualifications.iter().all(Option::is_some);
    let mut result = Vec::with_capacity(alternatives.len());
    let mut changed = false;
    let mut index = 0;

    while index < alternatives.len() {
        if qualifications[index].is_some() {
            let mut end = index + 1;
            while end < alternatives.len() && qualifications[end].is_some() {
                end += 1;
            }

            let run_len = end - index;
            if run_len >= 3 || (all_qualify && run_len == alternatives.len()) {
                if let Some(coalesced) = coalesce_alternatives(&alternatives[index..end], false) {
                    result.push(coalesced);
                    changed = true;
                    index = end;
                    continue;
                }
            }
        }

        result.push(alternatives[index].clone());
        index += 1;
    }

    if changed {
        build_choice(result)
    } else {
        original
    }
}

fn flatten_choices(expr: OptimizedExpr, alternatives: &mut Vec<OptimizedExpr>) {
    match expr {
        OptimizedExpr::Choice(lhs, rhs) => {
            flatten_choices(*lhs, alternatives);
            flatten_choices(*rhs, alternatives);
        }
        expr => alternatives.push(expr),
    }
}

fn char_ranges(expr: &OptimizedExpr) -> Option<Vec<(char, char)>> {
    match expr {
        OptimizedExpr::Str(string) => {
            let mut chars = string.chars();
            let character = chars.next()?;
            if chars.next().is_none() {
                Some(vec![(character, character)])
            } else {
                None
            }
        }
        OptimizedExpr::Insens(string) => {
            let mut chars = string.chars();
            let character = chars.next()?;
            if chars.next().is_some() {
                return None;
            }

            if character.is_ascii_alphabetic() {
                Some(vec![
                    (
                        character.to_ascii_lowercase(),
                        character.to_ascii_lowercase(),
                    ),
                    (
                        character.to_ascii_uppercase(),
                        character.to_ascii_uppercase(),
                    ),
                ])
            } else {
                Some(vec![(character, character)])
            }
        }
        OptimizedExpr::Range(start, end) => {
            let mut starts = start.chars();
            let mut ends = end.chars();
            let start = starts.next()?;
            let end = ends.next()?;
            if starts.next().is_none() && ends.next().is_none() {
                Some(vec![(start, end)])
            } else {
                None
            }
        }
        OptimizedExpr::CharClass(ranges) => ranges
            .iter()
            .map(|(start, end)| {
                let mut starts = start.chars();
                let mut ends = end.chars();
                let start = starts.next()?;
                let end = ends.next()?;
                if starts.next().is_none() && ends.next().is_none() {
                    Some((start, end))
                } else {
                    None
                }
            })
            .collect(),
        OptimizedExpr::RestoreOnErr(expr) => char_ranges(expr),
        _ => None,
    }
}

fn coalesce_alternatives(alternatives: &[OptimizedExpr], negated: bool) -> Option<OptimizedExpr> {
    let ranges = alternatives
        .iter()
        .map(char_ranges)
        .collect::<Option<Vec<_>>>()?
        .into_iter()
        .flatten()
        .collect::<Vec<_>>();
    let ranges = merge_ranges(ranges);

    if ranges.len() >= alternatives.len() {
        return None;
    }

    let ranges = ranges
        .into_iter()
        .map(|(start, end)| (start.to_string(), end.to_string()))
        .collect::<Vec<_>>();

    if negated {
        Some(OptimizedExpr::NegCharClass(ranges))
    } else if ranges.len() == 1 {
        let (start, end) = ranges.into_iter().next().unwrap();
        if start == end {
            Some(OptimizedExpr::Str(start))
        } else {
            Some(OptimizedExpr::Range(start, end))
        }
    } else {
        Some(OptimizedExpr::CharClass(ranges))
    }
}

fn merge_ranges(mut ranges: Vec<(char, char)>) -> Vec<(char, char)> {
    ranges.sort_by_key(|(start, _)| *start);
    let mut merged = Vec::with_capacity(ranges.len());

    for (start, end) in ranges {
        if let Some((_, previous_end)) = merged.last_mut() {
            let adjacent = (*previous_end as u32)
                .checked_add(1)
                .map_or(false, |next| start as u32 <= next);
            if start <= *previous_end || adjacent {
                if end > *previous_end {
                    *previous_end = end;
                }
                continue;
            }
        }
        merged.push((start, end));
    }

    merged
}

fn negated_char_class_ranges(
    lhs: &OptimizedExpr,
    rhs: &OptimizedExpr,
) -> Option<Vec<(String, String)>> {
    if !matches!(rhs, OptimizedExpr::Ident(name) if name == "ANY") {
        return None;
    }
    let OptimizedExpr::NegPred(expr) = lhs else {
        return None;
    };

    let mut alternatives = Vec::new();
    flatten_choices((**expr).clone(), &mut alternatives);
    coalesce_alternatives(&alternatives, true).and_then(|expr| match expr {
        OptimizedExpr::NegCharClass(ranges) => Some(ranges),
        _ => None,
    })
}

fn build_choice(mut alternatives: Vec<OptimizedExpr>) -> OptimizedExpr {
    let last = alternatives.pop().unwrap();
    alternatives.into_iter().rev().fold(last, |rhs, lhs| {
        OptimizedExpr::Choice(Box::new(lhs), Box::new(rhs))
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::ast::RuleType;

    fn rule(expr: OptimizedExpr) -> OptimizedRule {
        OptimizedRule {
            name: "rule".to_owned(),
            ty: RuleType::Atomic,
            expr,
        }
    }

    #[test]
    fn coalesces_ranges_and_case_insensitive_characters() {
        let expr = OptimizedExpr::Choice(
            Box::new(OptimizedExpr::Insens("b".to_owned())),
            Box::new(OptimizedExpr::Choice(
                Box::new(OptimizedExpr::Range("c".to_owned(), "d".to_owned())),
                Box::new(OptimizedExpr::Choice(
                    Box::new(OptimizedExpr::Str("A".to_owned())),
                    Box::new(OptimizedExpr::Str("D".to_owned())),
                )),
            )),
        );

        assert_eq!(
            coalesce(rule(expr)).expr,
            OptimizedExpr::CharClass(vec![
                ("A".to_owned(), "B".to_owned()),
                ("b".to_owned(), "d".to_owned()),
            ])
        );
    }

    #[test]
    fn preserves_short_qualifying_runs_when_partially_qualified() {
        let expr = OptimizedExpr::Choice(
            Box::new(OptimizedExpr::Str("a".to_owned())),
            Box::new(OptimizedExpr::Choice(
                Box::new(OptimizedExpr::Str("b".to_owned())),
                Box::new(OptimizedExpr::Ident("other".to_owned())),
            )),
        );

        assert_eq!(coalesce(rule(expr.clone())).expr, expr);
    }

    #[test]
    fn coalesces_negated_choice_followed_by_any() {
        let expr = OptimizedExpr::Seq(
            Box::new(OptimizedExpr::NegPred(Box::new(OptimizedExpr::Choice(
                Box::new(OptimizedExpr::Str("a".to_owned())),
                Box::new(OptimizedExpr::Str("b".to_owned())),
            )))),
            Box::new(OptimizedExpr::Ident("ANY".to_owned())),
        );

        assert_eq!(
            coalesce(rule(expr)).expr,
            OptimizedExpr::NegCharClass(vec![("a".to_owned(), "b".to_owned())])
        );
    }
}
