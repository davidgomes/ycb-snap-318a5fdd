use crate::optimizer::{OptimizedExpr, OptimizedRule};

type CharRange = (char, char);

pub fn coalesce(rule: OptimizedRule) -> OptimizedRule {
    let OptimizedRule { name, ty, expr } = rule;
    OptimizedRule {
        name,
        ty,
        expr: coalesce_expr(expr),
    }
}

fn coalesce_expr(expr: OptimizedExpr) -> OptimizedExpr {
    let expr = coalesce_at(expr);

    match expr {
        OptimizedExpr::PosPred(expr) => OptimizedExpr::PosPred(Box::new(coalesce_expr(*expr))),
        OptimizedExpr::NegPred(expr) => OptimizedExpr::NegPred(Box::new(coalesce_expr(*expr))),
        OptimizedExpr::Seq(lhs, rhs) => {
            OptimizedExpr::Seq(Box::new(coalesce_expr(*lhs)), Box::new(coalesce_expr(*rhs)))
        }
        OptimizedExpr::Choice(lhs, rhs) => {
            OptimizedExpr::Choice(Box::new(coalesce_expr(*lhs)), Box::new(coalesce_expr(*rhs)))
        }
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

fn coalesce_at(expr: OptimizedExpr) -> OptimizedExpr {
    match expr {
        OptimizedExpr::Choice(lhs, rhs) => coalesce_choice(*lhs, *rhs),
        OptimizedExpr::Seq(lhs, rhs) => match (*lhs, *rhs) {
            (OptimizedExpr::NegPred(predicate), OptimizedExpr::Ident(name)) if name == "ANY" => {
                let alternatives = choice_alternatives(*predicate);
                if let Some(ranges) = coalesce_alternatives(&alternatives) {
                    OptimizedExpr::NegCharClass(ranges)
                } else {
                    OptimizedExpr::Seq(
                        Box::new(OptimizedExpr::NegPred(Box::new(build_choice(alternatives)))),
                        Box::new(OptimizedExpr::Ident(name)),
                    )
                }
            }
            (lhs, rhs) => OptimizedExpr::Seq(Box::new(lhs), Box::new(rhs)),
        },
        expr => expr,
    }
}

fn coalesce_choice(lhs: OptimizedExpr, rhs: OptimizedExpr) -> OptimizedExpr {
    let mut alternatives = Vec::new();
    append_choice_alternatives(lhs, &mut alternatives);
    append_choice_alternatives(rhs, &mut alternatives);

    let mut coalesced = Vec::with_capacity(alternatives.len());
    let all_qualifying = alternatives
        .iter()
        .all(|alternative| ranges_for(alternative).is_some());
    let mut index = 0;
    while index < alternatives.len() {
        if ranges_for(&alternatives[index]).is_none() {
            coalesced.push(alternatives[index].clone());
            index += 1;
            continue;
        }

        let start = index;
        let mut ranges = Vec::new();
        while index < alternatives.len() {
            let Some(mut alternative_ranges) = ranges_for(&alternatives[index]) else {
                break;
            };
            ranges.append(&mut alternative_ranges);
            index += 1;
        }

        if (all_qualifying && index - start >= 2) || index - start >= 3 {
            if let Some(expr) = coalesced_expr(ranges, index - start) {
                coalesced.push(expr);
                continue;
            }
        }

        coalesced.extend(alternatives[start..index].iter().cloned());
    }

    build_choice(coalesced)
}

fn choice_alternatives(expr: OptimizedExpr) -> Vec<OptimizedExpr> {
    let mut alternatives = Vec::new();
    append_choice_alternatives(expr, &mut alternatives);
    alternatives
}

fn append_choice_alternatives(expr: OptimizedExpr, alternatives: &mut Vec<OptimizedExpr>) {
    match expr {
        OptimizedExpr::Choice(lhs, rhs) => {
            append_choice_alternatives(*lhs, alternatives);
            append_choice_alternatives(*rhs, alternatives);
        }
        expr => alternatives.push(expr),
    }
}

fn build_choice(mut alternatives: Vec<OptimizedExpr>) -> OptimizedExpr {
    let mut expr = alternatives.pop().expect("choice must have alternatives");
    while let Some(alternative) = alternatives.pop() {
        expr = OptimizedExpr::Choice(Box::new(alternative), Box::new(expr));
    }
    expr
}

fn coalesce_alternatives(alternatives: &[OptimizedExpr]) -> Option<Vec<(String, String)>> {
    if alternatives.len() < 2 {
        return None;
    }

    let mut ranges = Vec::new();
    for alternative in alternatives {
        ranges.extend(ranges_for(alternative)?);
    }
    coalesced_ranges(ranges, alternatives.len())
}

fn coalesced_expr(ranges: Vec<CharRange>, alternative_count: usize) -> Option<OptimizedExpr> {
    let ranges = merge_ranges(ranges);
    if ranges.len() >= alternative_count {
        return None;
    }

    Some(match ranges.as_slice() {
        [(start, end)] if start == end => OptimizedExpr::Str(start.to_string()),
        [(start, end)] => OptimizedExpr::Range(start.to_string(), end.to_string()),
        _ => OptimizedExpr::CharClass(
            ranges
                .into_iter()
                .map(|(start, end)| (start.to_string(), end.to_string()))
                .collect(),
        ),
    })
}

fn coalesced_ranges(
    ranges: Vec<CharRange>,
    alternative_count: usize,
) -> Option<Vec<(String, String)>> {
    let ranges = merge_ranges(ranges);
    if ranges.len() >= alternative_count {
        return None;
    }
    Some(
        ranges
            .into_iter()
            .map(|(start, end)| (start.to_string(), end.to_string()))
            .collect(),
    )
}

fn ranges_for(expr: &OptimizedExpr) -> Option<Vec<CharRange>> {
    match expr {
        OptimizedExpr::Str(string) => {
            let mut chars = string.chars();
            let character = chars.next()?;
            if chars.next().is_some() {
                None
            } else {
                Some(vec![(character, character)])
            }
        }
        OptimizedExpr::Insens(string) => {
            let mut chars = string.chars();
            let character = chars.next()?;
            if chars.next().is_some() {
                return None;
            }

            let mut ranges = vec![(character, character)];
            if character.is_ascii_alphabetic() {
                let lower = character.to_ascii_lowercase();
                let upper = character.to_ascii_uppercase();
                if lower != upper {
                    ranges.push((lower, lower));
                    ranges.push((upper, upper));
                }
            }
            Some(ranges)
        }
        OptimizedExpr::Range(start, end) => {
            let start = start.chars().next()?;
            let end = end.chars().next()?;
            if start > end {
                None
            } else {
                Some(vec![(start, end)])
            }
        }
        OptimizedExpr::CharClass(ranges) => ranges
            .iter()
            .map(|(start, end)| {
                let start = start.chars().next()?;
                let end = end.chars().next()?;
                (start <= end).then_some((start, end))
            })
            .collect(),
        OptimizedExpr::RestoreOnErr(expr) => ranges_for(expr),
        _ => None,
    }
}

fn merge_ranges(mut ranges: Vec<CharRange>) -> Vec<CharRange> {
    ranges.sort_unstable_by_key(|(start, _)| *start);

    let mut merged = Vec::with_capacity(ranges.len());
    for (start, end) in ranges {
        if let Some((_, current_end)) = merged.last_mut() {
            if (start as u32) <= (*current_end as u32) + 1 {
                if end > *current_end {
                    *current_end = end;
                }
                continue;
            }
        }
        merged.push((start, end));
    }
    merged
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::optimizer::OptimizedExpr::*;

    fn choice(alternatives: impl IntoIterator<Item = OptimizedExpr>) -> OptimizedExpr {
        build_choice(alternatives.into_iter().collect())
    }

    #[test]
    fn coalesces_ranges_and_single_characters() {
        let expr = choice([
            Str("a".to_owned()),
            Range("b".to_owned(), "d".to_owned()),
            Insens("e".to_owned()),
        ]);

        assert_eq!(
            coalesce_expr(expr),
            CharClass(vec![
                ("E".to_owned(), "E".to_owned()),
                ("a".to_owned(), "e".to_owned()),
            ])
        );
    }

    #[test]
    fn simplifies_a_single_merged_range() {
        assert_eq!(
            coalesce_expr(choice([
                Str("a".to_owned()),
                Str("b".to_owned()),
                Str("c".to_owned()),
            ])),
            Range("a".to_owned(), "c".to_owned())
        );
        assert_eq!(
            coalesce_expr(choice([
                Str("a".to_owned()),
                Str("a".to_owned()),
                Str("a".to_owned()),
            ])),
            Str("a".to_owned())
        );
    }

    #[test]
    fn coalesces_two_qualifying_alternatives_when_all_qualify() {
        assert_eq!(
            coalesce_expr(choice([Str("a".to_owned()), Str("b".to_owned())])),
            Range("a".to_owned(), "b".to_owned())
        );
    }

    #[test]
    fn only_coalesces_runs_of_three_qualifying_alternatives() {
        let expr = choice([
            Ident("rule".to_owned()),
            Str("a".to_owned()),
            Str("b".to_owned()),
            Str("c".to_owned()),
            Ident("other".to_owned()),
        ]);

        assert_eq!(
            coalesce_expr(expr),
            choice([
                Ident("rule".to_owned()),
                Range("a".to_owned(), "c".to_owned()),
                Ident("other".to_owned()),
            ])
        );
    }

    #[test]
    fn does_not_emit_when_range_count_does_not_shrink() {
        let expr = choice([
            Str("a".to_owned()),
            Str("c".to_owned()),
            Str("e".to_owned()),
        ]);

        assert_eq!(coalesce_expr(expr.clone()), expr);
    }

    #[test]
    fn strips_restore_on_err_from_coalesced_alternatives() {
        let expr = choice([
            RestoreOnErr(Box::new(Str("a".to_owned()))),
            Str("b".to_owned()),
            Str("c".to_owned()),
        ]);

        assert_eq!(coalesce_expr(expr), Range("a".to_owned(), "c".to_owned()));
    }

    #[test]
    fn coalesces_negative_class_before_any() {
        let expr = Seq(
            Box::new(NegPred(Box::new(choice([
                Str("a".to_owned()),
                Str("b".to_owned()),
                Str("c".to_owned()),
            ])))),
            Box::new(Ident("ANY".to_owned())),
        );

        assert_eq!(
            coalesce_expr(expr),
            NegCharClass(vec![("a".to_owned(), "c".to_owned())])
        );
    }
}
