//! Elements implicated by structure-sensitive CSS selectors.
//!
//! A rewrite may flatten or move a container. That has to be decided from the
//! selector anchors that exist beforehand: afterwards the relationship those
//! selectors match is gone, so it can no longer be recovered from the tree.

use std::{cell::RefCell, collections::HashSet};

use lightningcss::{
    printer::PrinterOptions,
    rules::{self, CssRuleList},
    traits::ToCss,
};
use selectors::{
    context::SelectorCaches,
    matching::{self, MatchingMode, NeedsSelectorFlags, QuirksMode},
    parser::{Combinator, Component, NthType, Selector as ComplexSelector},
    Element as SelectorElement,
};

use crate::{
    element::Element,
    node::AllocationID,
    selectors::{SelectElement, Selector, SelectorImpl},
};

/// Allocation ids of elements that participate in a full structure-sensitive match.
pub fn implicated_elements<'input>(
    root: &Element<'input, '_>,
    styles: &[RefCell<CssRuleList<'input>>],
) -> HashSet<AllocationID> {
    let mut implicated = HashSet::new();
    let mut selectors = Vec::new();
    for css in styles {
        for rule in &css.borrow().0 {
            collect_selectors(rule, &mut selectors);
        }
    }
    for css in selectors {
        let Ok(parsed) = Selector::new(&css) else {
            continue;
        };
        mark_selector_list(root, &parsed, &mut implicated);
    }
    implicated
}

/// Whether a parsed selector depends on document structure, not only on one compound.
pub fn is_structure_sensitive(selector: &Selector) -> bool {
    selector.complexes().iter().any(complex_is_structural)
}

fn collect_selectors(rule: &rules::CssRule<'_>, out: &mut Vec<String>) {
    match rule {
        rules::CssRule::Style(style) => {
            for selector in &style.selectors.0 {
                if let Ok(css) = selector.to_css_string(PrinterOptions::default()) {
                    out.push(css);
                }
            }
        }
        rules::CssRule::Container(rules::container::ContainerRule { rules, .. })
        | rules::CssRule::Media(rules::media::MediaRule { rules, .. })
        | rules::CssRule::Supports(rules::supports::SupportsRule { rules, .. }) => {
            for rule in &rules.0 {
                collect_selectors(rule, out);
            }
        }
        _ => {}
    }
}

fn mark_selector_list(
    root: &Element<'_, '_>,
    selector: &Selector,
    implicated: &mut HashSet<AllocationID>,
) {
    for complex in selector.complexes() {
        if !complex_is_structural(complex) {
            continue;
        }
        let mut elements = vec![root.clone()];
        elements.extend(root.breadth_first());
        for element in elements {
            if !complex_matches(complex, &element) {
                continue;
            }
            mark_relationship(complex, &element, implicated);
        }
    }
}

fn mark_relationship(
    complex: &ComplexSelector<SelectorImpl>,
    subject: &Element<'_, '_>,
    implicated: &mut HashSet<AllocationID>,
) {
    let sequences = split_sequences(complex);
    if sequences.is_empty() {
        return;
    }
    let mut node = subject.clone();
    mark_compound_subject(&node, &sequences[0].components, implicated);

    for index in 0..sequences.len().saturating_sub(1) {
        let Some(combinator) = sequences[index].combinator_to_left else {
            break;
        };
        let left = &sequences[index + 1].components;
        let Some(anchor) = find_anchor(&node, combinator, left) else {
            break;
        };
        if matches!(
            combinator,
            Combinator::NextSibling | Combinator::LaterSibling
        ) {
            if let Some(parent) = node.parent_element() {
                implicated.insert(parent.id());
            }
        }
        mark_compound_subject(&anchor, left, implicated);
        node = anchor;
    }
}

fn mark_compound_subject(
    element: &Element<'_, '_>,
    components: &[Component<SelectorImpl>],
    implicated: &mut HashSet<AllocationID>,
) {
    implicated.insert(element.id());
    if !compound_is_index_sensitive(components) {
        return;
    }
    let Some(parent) = element.parent_element() else {
        return;
    };
    implicated.insert(parent.id());
    for child in parent.children_iter() {
        implicated.insert(child.id());
    }
}

struct Sequence {
    components: Vec<Component<SelectorImpl>>,
    combinator_to_left: Option<Combinator>,
}

fn split_sequences(complex: &ComplexSelector<SelectorImpl>) -> Vec<Sequence> {
    let mut sequences = Vec::new();
    let mut iter = complex.iter();
    loop {
        let components = iter.by_ref().cloned().collect();
        let combinator_to_left = iter.next_sequence();
        let done = combinator_to_left.is_none();
        sequences.push(Sequence {
            components,
            combinator_to_left,
        });
        if done {
            break;
        }
    }
    sequences
}

fn find_anchor<'input, 'arena>(
    node: &Element<'input, 'arena>,
    combinator: Combinator,
    compound: &[Component<SelectorImpl>],
) -> Option<Element<'input, 'arena>> {
    match combinator {
        Combinator::Child => {
            let parent = node.parent_element()?;
            compound_matches(&parent, compound).then_some(parent)
        }
        Combinator::Descendant => {
            let mut parent = node.parent_element();
            while let Some(candidate) = parent {
                if compound_matches(&candidate, compound) {
                    return Some(candidate);
                }
                parent = candidate.parent_element();
            }
            None
        }
        Combinator::NextSibling => {
            let previous = node.previous_element_sibling()?;
            compound_matches(&previous, compound).then_some(previous)
        }
        Combinator::LaterSibling => {
            let mut previous = node.previous_element_sibling();
            while let Some(candidate) = previous {
                if compound_matches(&candidate, compound) {
                    return Some(candidate);
                }
                previous = candidate.previous_element_sibling();
            }
            None
        }
        Combinator::PseudoElement | Combinator::SlotAssignment | Combinator::Part => None,
    }
}

fn complex_matches(complex: &ComplexSelector<SelectorImpl>, element: &Element<'_, '_>) -> bool {
    let mut caches = SelectorCaches::default();
    let mut context = matching::MatchingContext::new(
        MatchingMode::Normal,
        None,
        &mut caches,
        QuirksMode::NoQuirks,
        NeedsSelectorFlags::No,
        matching::MatchingForInvalidation::No,
    );
    matching::matches_selector(
        complex,
        0,
        None,
        &SelectElement::new(element.clone()),
        &mut context,
    )
}

fn complex_is_structural(complex: &ComplexSelector<SelectorImpl>) -> bool {
    let mut iter = complex.iter();
    iter.by_ref().any(component_is_structural) || iter.next_sequence().is_some()
}

fn component_is_structural(component: &Component<SelectorImpl>) -> bool {
    match component {
        Component::Nth(_)
        | Component::NthOf(_)
        | Component::Empty
        | Component::Has(_)
        | Component::Combinator(_) => true,
        Component::Negation(list) | Component::Is(list) | Component::Where(list) => {
            list.slice().iter().any(complex_is_structural)
        }
        _ => false,
    }
}

fn compound_is_index_sensitive(components: &[Component<SelectorImpl>]) -> bool {
    components.iter().any(component_is_index_sensitive)
}

fn component_is_index_sensitive(component: &Component<SelectorImpl>) -> bool {
    match component {
        Component::Nth(nth) => {
            nth.ty == NthType::Child
                || nth.ty == NthType::LastChild
                || nth.ty == NthType::OfType
                || nth.ty == NthType::LastOfType
        }
        Component::NthOf(_) => true,
        Component::Negation(list) | Component::Is(list) | Component::Where(list) => {
            list.slice().iter().any(|selector| {
                split_sequences(selector).iter().any(|sequence| {
                    sequence.combinator_to_left.is_none()
                        && compound_is_index_sensitive(&sequence.components)
                })
            })
        }
        _ => false,
    }
}

fn compound_matches(element: &Element<'_, '_>, components: &[Component<SelectorImpl>]) -> bool {
    let selected = SelectElement::new(element.clone());
    components.iter().all(|component| match component {
        Component::LocalName(name) => selected.has_local_name(&name.lower_name),
        Component::ID(id) => selected.has_id(id, selectors::attr::CaseSensitivity::CaseSensitive),
        Component::Class(class) => {
            selected.has_class(class, selectors::attr::CaseSensitivity::CaseSensitive)
        }
        Component::Empty => selected.is_empty(),
        Component::Root => selected.is_root(),
        Component::AttributeInNoNamespaceExists {
            local_name,
            local_name_lower,
        } => {
            selected.attr_matches(
                &selectors::attr::NamespaceConstraint::Specific(
                    &crate::selectors::CssNamespace::default(),
                ),
                local_name_lower,
                &selectors::attr::AttrSelectorOperation::Exists,
            ) || selected.attr_matches(
                &selectors::attr::NamespaceConstraint::Any,
                local_name,
                &selectors::attr::AttrSelectorOperation::Exists,
            )
        }
        Component::Is(list) | Component::Where(list) => list.slice().iter().any(|selector| {
            let sequences = split_sequences(selector);
            (sequences.len() == 1 && compound_matches(element, &sequences[0].components))
                || complex_matches(selector, element)
        }),
        Component::Negation(list) => list
            .slice()
            .iter()
            .all(|selector| !complex_matches(selector, element)),
        _ => true,
    })
}
