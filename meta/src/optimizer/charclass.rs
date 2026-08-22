use super::{OptimizedExpr, OptimizedRule};

pub fn coalesce(rule: OptimizedRule) -> OptimizedRule {
    OptimizedRule { expr: rule.expr.map_top_down(coalesce_expr), ..rule }
}

fn coalesce_expr(expr: OptimizedExpr) -> OptimizedExpr {
    match expr {
        OptimizedExpr::Choice(_, _) => coalesce_choice(expr),
        OptimizedExpr::NegPred(inner) => {
            if let OptimizedExpr::Seq(excluded, any) = *inner {
                if matches!(*any, OptimizedExpr::Ident(ref name) if name == "ANY") {
                    if let Some(ranges) = ranges_from_choice(*excluded) {
                        return OptimizedExpr::NegCharClass(ranges);
                    }
                }
                OptimizedExpr::NegPred(Box::new(OptimizedExpr::Seq(excluded, any)))
            } else {
                OptimizedExpr::NegPred(inner)
            }
        }
        other => other,
    }
}

fn coalesce_choice(expr: OptimizedExpr) -> OptimizedExpr {
    let mut alternatives = Vec::new();
    flatten(expr, &mut alternatives);
    let mut output = Vec::new();
    let mut i = 0;
    while i < alternatives.len() {
        if let Some((range, consumed)) = qualifying_run(&alternatives[i..]) {
            if consumed >= 3 && range.len() < consumed {
                output.push(simplify(range));
                i += consumed;
                continue;
            }
        }
        output.push(alternatives[i].clone());
        i += 1;
    }
    output.into_iter().rev().reduce(|rhs, lhs| OptimizedExpr::Choice(Box::new(lhs), Box::new(rhs))).unwrap()
}

fn flatten(expr: OptimizedExpr, out: &mut Vec<OptimizedExpr>) {
    match expr {
        OptimizedExpr::Choice(lhs, rhs) => { flatten(*lhs, out); flatten(*rhs, out); }
        other => out.push(other),
    }
}

fn qualifying_run(alts: &[OptimizedExpr]) -> Option<(Vec<(String, String)>, usize)> {
    let mut ranges = Vec::new();
    for (i, alt) in alts.iter().enumerate() {
        let Some(mut r) = ranges_for(alt) else { return if i == 0 { None } else { Some((merge(ranges), i)) }; };
        ranges.append(&mut r);
    }
    Some((merge(ranges), alts.len()))
}

fn ranges_from_choice(expr: OptimizedExpr) -> Option<Vec<(String, String)>> {
    let mut alts = Vec::new();
    flatten(expr, &mut alts);
    if alts.is_empty() { return None; }
    let mut ranges = Vec::new();
    for alt in alts { ranges.extend(ranges_for(&alt)?); }
    Some(merge(ranges))
}

fn ranges_for(expr: &OptimizedExpr) -> Option<Vec<(String, String)>> {
    match expr {
        OptimizedExpr::RestoreOnErr(inner) => ranges_for(inner),
        OptimizedExpr::Str(s) if s.chars().count() == 1 => {
            let c = s.chars().next().unwrap();
            Some(vec![(c.to_string(), c.to_string())])
        }
        OptimizedExpr::Insens(s) if s.chars().count() == 1 => {
            let c = s.chars().next().unwrap();
            let mut r = vec![(c.to_ascii_lowercase().to_string(), c.to_ascii_lowercase().to_string())];
            if c.is_ascii_alphabetic() { r.push((c.to_ascii_uppercase().to_string(), c.to_ascii_uppercase().to_string())); }
            Some(r)
        }
        OptimizedExpr::Range(s, e) => Some(vec![(s.clone(), e.clone())]),
        OptimizedExpr::CharClass(r) => Some(r.clone()),
        _ => None,
    }
}

fn merge(mut ranges: Vec<(String, String)>) -> Vec<(String, String)> {
    ranges.sort_by_key(|(s, _)| s.chars().next().unwrap() as u32);
    let mut result: Vec<(String, String)> = Vec::new();
    for (s, e) in ranges {
        let start = s.chars().next().unwrap();
        let end = e.chars().next().unwrap();
        if let Some((_, last_end)) = result.last_mut() {
            let last = last_end.chars().next().unwrap();
            if (start as u32) <= (last as u32).saturating_add(1) {
                if end > last { *last_end = end.to_string(); }
                continue;
            }
        }
        result.push((start.to_string(), end.to_string()));
    }
    result
}

fn simplify(ranges: Vec<(String, String)>) -> OptimizedExpr {
    if ranges.len() == 1 {
        let (s, e) = ranges.into_iter().next().unwrap();
        if s == e { OptimizedExpr::Str(s) } else { OptimizedExpr::Range(s, e) }
    } else {
        OptimizedExpr::CharClass(ranges)
    }
}
