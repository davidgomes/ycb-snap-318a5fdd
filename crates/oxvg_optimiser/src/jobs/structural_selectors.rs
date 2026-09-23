//! Elements a structure-sensitive selector actually depends on.
//!
//! A rewrite may flatten or remove a container only when that container is not
//! part of a selector relationship that already matches. That relationship is
//! read from the tree before the rewrite: the matched subject, and each
//! ancestor or sibling the full selector needs. An element that only shares a
//! tag or class with part of the selector is not implicated.

use std::{collections::HashSet, fmt::Write as _};

use lightningcss::{
    rules::{CssRule, CssRuleList},
    selector::{Combinator, Component, Selector as CssSelector},
};
use oxvg_ast::{
    element::Element,
    selectors::{SelectElement, Selector},
    visitor::Context,
};
use parcel_selectors::parser::NthType;

/// Records every element implicated by a structure-sensitive selector.
pub fn record<'input, 'arena>(
    root: &Element<'input, 'arena>,
    context: &Context<'input, 'arena, '_>,
) {
    let mut walker = Walker::default();
    for sheet in &context.query_has_stylesheet_result {
        let sheet = sheet.borrow();
        walk_rules(&sheet, &mut |selector| {
            if !selector_is_structural(selector) {
                return;
            }
            if let Some(chain) = split_selector(selector) {
                walker.protect_chain(root, context, &chain, None);
            }
            for nested in nested_structural(selector) {
                if let Some(chain) = split_selector(&nested) {
                    walker.protect_chain(root, context, &chain, None);
                }
            }
        });
    }
}

#[derive(Default)]
struct Walker {
    seen: HashSet<(usize, u64)>,
    chain_id: u64,
}

struct Chain {
    compounds: Vec<Compound>,
    combinators: Vec<Combinator>,
}

struct Compound {
    css: String,
    child_indexed: bool,
    has_relative: Vec<(Search, Chain)>,
}

#[derive(Clone, Copy)]
struct Step<'a, 'input, 'arena> {
    chain: &'a Chain,
    index: usize,
    boundary: Option<&'a Element<'input, 'arena>>,
    chain_id: u64,
    depth: u8,
}

#[derive(Clone, Copy)]
enum Search {
    Descendants,
    Children,
    NextSibling,
    LaterSiblings,
}

impl Chain {
    fn css(&self) -> String {
        self.prefix(self.compounds.len() - 1)
    }

    fn prefix(&self, end: usize) -> String {
        let mut css = String::new();
        for index in 0..=end {
            if index > 0 {
                css.push_str(combinator_css(self.combinators[index - 1]));
            }
            css.push_str(&self.compounds[index].css);
        }
        css
    }
}

impl Walker {
    fn protect_chain<'input, 'arena>(
        &mut self,
        root: &Element<'input, 'arena>,
        context: &Context<'input, 'arena, '_>,
        chain: &Chain,
        boundary: Option<&Element<'input, 'arena>>,
    ) {
        let Some(selector) = parse_selector(&chain.css()) else {
            return;
        };
        self.chain_id = self.chain_id.wrapping_add(1);
        let chain_id = self.chain_id;
        for element in tree_elements(root) {
            if boundary.is_some_and(|limit| !within_boundary(&element, Some(limit))) {
                continue;
            }
            if !matches_selector(&selector, &element) {
                continue;
            }
            self.mark_from(
                root,
                context,
                &element,
                Step {
                    chain,
                    index: chain.compounds.len() - 1,
                    boundary,
                    chain_id,
                    depth: 0,
                },
            );
        }
    }

    fn mark_from<'input, 'arena>(
        &mut self,
        root: &Element<'input, 'arena>,
        context: &Context<'input, 'arena, '_>,
        element: &Element<'input, 'arena>,
        step: Step<'_, 'input, 'arena>,
    ) {
        let seen_key = (
            element.id(),
            step.chain_id
                .wrapping_mul(64)
                .wrapping_add(u64::from(step.depth))
                .wrapping_mul(32)
                .wrapping_add(u64::try_from(step.index).unwrap_or(u64::MAX)),
        );
        if step.depth > 16 || !self.seen.insert(seen_key) {
            return;
        }

        let compound = &step.chain.compounds[step.index];
        context.mark_structural_anchor(element);
        // Child-indexed pseudos are defined by the parent's child list. Flattening
        // that parent moves the subject onto a different sibling axis.
        if compound.child_indexed {
            if let Some(parent) = element.parent_element() {
                if within_boundary(&parent, step.boundary) {
                    context.mark_structural_anchor(&parent);
                }
            }
        }
        for (search, relative) in &compound.has_relative {
            self.mark_has(root, context, element, *search, relative, step.depth);
        }
        if step.index == 0 {
            return;
        }
        self.mark_combinator(root, context, element, step);
    }

    fn mark_combinator<'input, 'arena>(
        &mut self,
        root: &Element<'input, 'arena>,
        context: &Context<'input, 'arena, '_>,
        element: &Element<'input, 'arena>,
        step: Step<'_, 'input, 'arena>,
    ) {
        let Some(left) = parse_selector(&step.chain.prefix(step.index - 1)) else {
            return;
        };
        let next = Step {
            index: step.index - 1,
            ..step
        };
        let mark = |walker: &mut Self, candidate: &Element<'input, 'arena>| {
            if matches_selector(&left, candidate) {
                walker.mark_from(root, context, candidate, next);
            }
        };
        match step.chain.combinators[step.index - 1] {
            Combinator::Child => {
                if let Some(parent) = element.parent_element() {
                    if within_boundary(&parent, step.boundary) {
                        mark(self, &parent);
                    }
                }
            }
            Combinator::Descendant | Combinator::Deep | Combinator::DeepDescendant => {
                let mut ancestor = element.parent_element();
                while let Some(candidate) = ancestor {
                    if !within_boundary(&candidate, step.boundary) {
                        break;
                    }
                    mark(self, &candidate);
                    ancestor = candidate.parent_element();
                }
            }
            Combinator::NextSibling => {
                if let Some(previous) = element.previous_element_sibling() {
                    mark(self, &previous);
                }
            }
            Combinator::LaterSibling => {
                let mut previous = element.previous_element_sibling();
                while let Some(candidate) = previous {
                    mark(self, &candidate);
                    previous = candidate.previous_element_sibling();
                }
            }
            Combinator::PseudoElement | Combinator::Part | Combinator::SlotAssignment => {}
        }
    }

    fn mark_has<'input, 'arena>(
        &mut self,
        root: &Element<'input, 'arena>,
        context: &Context<'input, 'arena, '_>,
        anchor: &Element<'input, 'arena>,
        search: Search,
        relative: &Chain,
        depth: u8,
    ) {
        let Some(selector) = parse_selector(&relative.css()) else {
            return;
        };
        self.chain_id = self.chain_id.wrapping_add(1);
        let chain_id = self.chain_id;
        for candidate in candidates(anchor, search) {
            if !matches_selector(&selector, &candidate) {
                continue;
            }
            self.mark_from(
                root,
                context,
                &candidate,
                Step {
                    chain: relative,
                    index: relative.compounds.len() - 1,
                    boundary: Some(anchor),
                    chain_id,
                    depth: depth + 1,
                },
            );
        }
    }
}

fn matches_selector(selector: &Selector, element: &Element<'_, '_>) -> bool {
    selector.matches_naive(&SelectElement::new(element.clone()))
}

fn within_boundary(element: &Element, boundary: Option<&Element>) -> bool {
    boundary.is_none_or(|limit| limit.id() != element.id())
}

fn parse_selector(css: &str) -> Option<Selector> {
    Selector::new(css).ok()
}

fn walk_rules<'input>(list: &CssRuleList<'input>, visit: &mut impl FnMut(&CssSelector<'input>)) {
    for rule in &list.0 {
        match rule {
            CssRule::Style(style) => {
                for selector in &style.selectors.0 {
                    visit(selector);
                }
                walk_rules(&style.rules, visit);
            }
            CssRule::Media(media) => walk_rules(&media.rules, visit),
            CssRule::Supports(supports) => walk_rules(&supports.rules, visit),
            CssRule::Container(container) => walk_rules(&container.rules, visit),
            CssRule::LayerBlock(layer) => walk_rules(&layer.rules, visit),
            CssRule::StartingStyle(starting) => walk_rules(&starting.rules, visit),
            CssRule::Scope(scope) => walk_rules(&scope.rules, visit),
            CssRule::MozDocument(document) => walk_rules(&document.rules, visit),
            CssRule::Nesting(nesting) => {
                for selector in &nesting.style.selectors.0 {
                    visit(selector);
                }
                walk_rules(&nesting.style.rules, visit);
            }
            _ => {}
        }
    }
}

fn selector_is_structural(selector: &CssSelector) -> bool {
    selector
        .iter_raw_match_order()
        .any(|component| component_is_structural(component))
}

fn component_is_structural(component: &Component) -> bool {
    match component {
        Component::Combinator(combinator) => structural_combinator(*combinator),
        Component::Nth(_) | Component::NthOf(_) | Component::Empty | Component::Has(_) => true,
        Component::Is(list)
        | Component::Where(list)
        | Component::Negation(list)
        | Component::Any(_, list) => list.iter().any(selector_is_structural),
        _ => false,
    }
}

fn structural_combinator(combinator: Combinator) -> bool {
    matches!(
        combinator,
        Combinator::Child
            | Combinator::Descendant
            | Combinator::NextSibling
            | Combinator::LaterSibling
            | Combinator::Deep
            | Combinator::DeepDescendant
    )
}

fn nested_structural<'a>(selector: &CssSelector<'a>) -> Vec<CssSelector<'a>> {
    let mut nested = Vec::new();
    for component in selector.iter_raw_match_order() {
        collect_nested(component, &mut nested);
    }
    nested
}

fn collect_nested<'a>(component: &Component<'a>, out: &mut Vec<CssSelector<'a>>) {
    let (Component::Is(list)
    | Component::Where(list)
    | Component::Negation(list)
    | Component::Any(_, list)) = component
    else {
        return;
    };
    for selector in list {
        if selector_is_structural(selector) {
            out.push(selector.clone());
        }
        for inner in selector.iter_raw_match_order() {
            collect_nested(inner, out);
        }
    }
}

fn split_selector(selector: &CssSelector) -> Option<Chain> {
    let raw: Vec<_> = selector.iter_raw_match_order().cloned().collect();
    chain_from_match_order(&raw)
}

fn split_relative(selector: &CssSelector) -> Option<(Search, Chain)> {
    let mut raw: Vec<_> = selector.iter_raw_match_order().cloned().collect();
    let mut search = Search::Descendants;
    if matches!(raw.last(), Some(Component::Scope | Component::Nesting)) {
        raw.pop();
        if let Some(Component::Combinator(combinator)) = raw.last().cloned() {
            if let Some(found) = search_from(combinator) {
                search = found;
                raw.pop();
            }
        }
    }
    Some((search, chain_from_match_order(&raw)?))
}

fn search_from(combinator: Combinator) -> Option<Search> {
    Some(match combinator {
        Combinator::Child => Search::Children,
        Combinator::Descendant | Combinator::Deep | Combinator::DeepDescendant => {
            Search::Descendants
        }
        Combinator::NextSibling => Search::NextSibling,
        Combinator::LaterSibling => Search::LaterSiblings,
        _ => return None,
    })
}

/// `raw` is in match order: the subject compound is first, and simple selectors
/// inside a compound stay in parse order.
fn chain_from_match_order(raw: &[Component]) -> Option<Chain> {
    let mut parts = Vec::new();
    let mut combinators = Vec::new();
    let mut current = Vec::new();
    for component in raw {
        if let Component::Combinator(combinator) = component {
            if structural_combinator(*combinator) {
                parts.push(std::mem::take(&mut current));
                combinators.push(*combinator);
                continue;
            }
        }
        current.push(component.clone());
    }
    parts.push(current);
    parts.reverse();
    combinators.reverse();
    if parts.is_empty() || parts.iter().any(Vec::is_empty) {
        return None;
    }
    Some(Chain {
        compounds: parts.iter().map(|part| compound_from(part)).collect(),
        combinators,
    })
}

fn compound_from(components: &[Component]) -> Compound {
    let mut css = String::new();
    let mut has_relative = Vec::new();
    for component in components {
        collect_has(component, &mut has_relative);
        if let Component::Has(list) = component {
            css.push_str(&has_css(list));
            continue;
        }
        // `Component`'s `Debug` is its CSS serialization (via `ToCss` in parcel_selectors).
        // The cssparser trait in this crate is a different version, so it cannot be called directly.
        let _ = write!(css, "{component:?}");
    }
    Compound {
        child_indexed: components.iter().any(component_is_child_indexed),
        css,
        has_relative,
    }
}

fn component_is_child_indexed(component: &Component) -> bool {
    match component {
        Component::Nth(data) => nth_is_child_indexed(data.ty),
        Component::NthOf(data) => nth_is_child_indexed(data.nth_data().ty),
        Component::Is(list)
        | Component::Where(list)
        | Component::Negation(list)
        | Component::Any(_, list) => list.iter().any(|selector| {
            selector
                .iter_raw_match_order()
                .any(|component| component_is_child_indexed(component))
        }),
        _ => false,
    }
}

fn nth_is_child_indexed(kind: NthType) -> bool {
    !matches!(kind, NthType::Col | NthType::LastCol)
}

fn collect_has(component: &Component, out: &mut Vec<(Search, Chain)>) {
    match component {
        Component::Has(list) => {
            for selector in list {
                if let Some(relative) = split_relative(selector) {
                    out.push(relative);
                }
            }
        }
        Component::Is(list) | Component::Where(list) | Component::Any(_, list) => {
            for selector in list {
                for component in selector.iter_raw_match_order() {
                    collect_has(component, out);
                }
            }
        }
        _ => {}
    }
}

fn has_css(selectors: &[CssSelector]) -> String {
    let mut parts = Vec::new();
    for selector in selectors {
        let Some((search, chain)) = split_relative(selector) else {
            continue;
        };
        let prefix = match search {
            Search::Descendants => "",
            Search::Children => "> ",
            Search::NextSibling => "+ ",
            Search::LaterSiblings => "~ ",
        };
        parts.push(format!("{prefix}{}", chain.css()));
    }
    format!(":has({})", parts.join(", "))
}

fn combinator_css(combinator: Combinator) -> &'static str {
    match combinator {
        Combinator::Child => " > ",
        Combinator::Descendant => " ",
        Combinator::NextSibling => " + ",
        Combinator::LaterSibling => " ~ ",
        Combinator::DeepDescendant => " >>> ",
        Combinator::Deep => " /deep/ ",
        Combinator::PseudoElement | Combinator::Part | Combinator::SlotAssignment => "",
    }
}

fn tree_elements<'input, 'arena>(root: &Element<'input, 'arena>) -> Vec<Element<'input, 'arena>> {
    let mut elements = Vec::new();
    collect_elements(root, &mut elements);
    elements
}

fn collect_elements<'input, 'arena>(
    element: &Element<'input, 'arena>,
    elements: &mut Vec<Element<'input, 'arena>>,
) {
    elements.push(element.clone());
    for child in element.children_iter() {
        collect_elements(&child, elements);
    }
}

fn candidates<'input, 'arena>(
    anchor: &Element<'input, 'arena>,
    search: Search,
) -> Vec<Element<'input, 'arena>> {
    match search {
        Search::Children => anchor.children_iter().collect(),
        Search::Descendants => {
            let mut elements = Vec::new();
            for child in anchor.children_iter() {
                collect_elements(&child, &mut elements);
            }
            elements
        }
        Search::NextSibling => anchor.next_element_sibling().into_iter().collect(),
        Search::LaterSiblings => {
            let mut siblings = Vec::new();
            let mut next = anchor.next_element_sibling();
            while let Some(sibling) = next {
                siblings.push(sibling.clone());
                next = sibling.next_element_sibling();
            }
            siblings
        }
    }
}
