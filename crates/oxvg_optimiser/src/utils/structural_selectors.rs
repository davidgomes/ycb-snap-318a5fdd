// `HashableElement` hashes by allocation id, which is unaffected by interior mutability
#![allow(clippy::mutable_key_type)]
use std::{cell::RefCell, collections::HashSet};

use lightningcss::{
    printer::PrinterOptions,
    rules::{CssRule, CssRuleList},
    selector::{Component, Selector},
    traits::ToCss as _,
};
use oxvg_ast::{
    element::{Element, HashableElement},
    selectors::{SelectElement, Selector as ElementSelector},
};
use parcel_selectors::parser::Combinator;

#[derive(Default)]
/// The parts of a document that stylesheet selectors depend on structurally, i.e. through
/// combinators or structural pseudo-classes.
///
/// Only elements taking part in a complete match of a structure-sensitive selector are recorded,
/// so that rewrites elsewhere in the document remain possible.
pub struct StructuralDependencies<'input, 'arena> {
    /// Elements which are either the target of a structure-sensitive selector or an anchor
    /// that the target's match is relative to.
    elements: HashSet<HashableElement<'input, 'arena>>,
    /// Elements whose sequence of children affects matching, such as the parent of an
    /// element matched by `:nth-child`.
    ordered_parents: HashSet<HashableElement<'input, 'arena>>,
}

#[derive(Default, Clone, Copy)]
struct CompoundDependencies {
    /// The matched element's position among its siblings affects matching
    position: bool,
    /// The matched element's ancestors affect matching, beyond what the outer selector expresses
    ancestors: bool,
    /// The matched element's descendants affect matching
    subtree: bool,
}

impl CompoundDependencies {
    fn is_structural(self) -> bool {
        self.position || self.ancestors || self.subtree
    }

    fn union(self, other: Self) -> Self {
        Self {
            position: self.position || other.position,
            ancestors: self.ancestors || other.ancestors,
            subtree: self.subtree || other.subtree,
        }
    }
}

struct ComplexSelector {
    /// The compounds of the selector from right to left, starting with the subject
    compounds: Vec<CompoundDependencies>,
    /// The combinator to the left of each compound
    combinators: Vec<Combinator>,
    /// For each compound, a selector made of that compound and everything to its left
    prefixes: Vec<ElementSelector>,
    has_structural_component: bool,
}

impl<'input, 'arena> StructuralDependencies<'input, 'arena> {
    /// Gathers the structural dependencies of the stylesheets over the current document.
    ///
    /// This should be done before rewriting the document, as rewrites may remove the
    /// structure that a selector depends on.
    pub fn new(root: &Element<'input, 'arena>, styles: &[RefCell<CssRuleList<'input>>]) -> Self {
        let mut result = Self::default();
        let mut selectors = vec![];
        for style in styles {
            let style = style.borrow();
            let mut style_selectors = vec![];
            collect_selectors(&style.0, &mut style_selectors);
            selectors.extend(
                style_selectors
                    .into_iter()
                    .filter_map(ComplexSelector::new)
                    .filter(ComplexSelector::is_structural),
            );
        }
        if selectors.is_empty() {
            return result;
        }

        for element in root.breadth_first() {
            for selector in &selectors {
                let select_element = SelectElement::new(element.clone());
                if selector.prefixes[0].matches_naive(&select_element) {
                    let mut visited = HashSet::new();
                    result.insert_match(selector, &element, 0, &mut visited);
                }
            }
        }
        result
    }

    /// Whether the element is the target or anchor of a structure-sensitive selector
    pub fn is_implicated(&self, element: &Element<'input, 'arena>) -> bool {
        self.elements
            .contains(&HashableElement::new(element.clone()))
    }

    /// Whether the order and parentage of the element's children affects matching
    pub fn is_ordered_parent(&self, element: &Element<'input, 'arena>) -> bool {
        self.ordered_parents
            .contains(&HashableElement::new(element.clone()))
    }

    fn insert_match(
        &mut self,
        selector: &ComplexSelector,
        element: &Element<'input, 'arena>,
        index: usize,
        visited: &mut HashSet<(HashableElement<'input, 'arena>, usize)>,
    ) {
        if !visited.insert((HashableElement::new(element.clone()), index)) {
            return;
        }
        self.elements.insert(HashableElement::new(element.clone()));

        let dependencies = selector.compounds[index];
        if dependencies.position {
            if let Some(parent) = Element::parent_element(element) {
                self.ordered_parents.insert(HashableElement::new(parent));
            }
        }
        if dependencies.ancestors {
            let mut ancestor = Element::parent_element(element);
            while let Some(current) = ancestor {
                ancestor = Element::parent_element(&current);
                self.elements.insert(HashableElement::new(current));
            }
        }
        if dependencies.subtree {
            self.ordered_parents
                .insert(HashableElement::new(element.clone()));
            self.elements
                .extend(element.breadth_first().map(HashableElement::new));
        }

        let Some(combinator) = selector.combinators.get(index) else {
            return;
        };
        let next = index + 1;
        for anchor in related_elements(element, *combinator) {
            if selector.prefixes[next].matches_naive(&SelectElement::new(anchor.clone())) {
                self.insert_match(selector, &anchor, next, visited);
            }
        }
    }
}

impl ComplexSelector {
    fn new(selector: &Selector) -> Option<Self> {
        let mut compounds = vec![];
        let mut combinators = vec![];
        let mut iter = selector.iter();
        loop {
            compounds.push(iter.by_ref().cloned().collect::<Vec<_>>());
            match iter.next_sequence() {
                Some(combinator) => combinators.push(combinator),
                None => break,
            }
        }

        let mut prefixes = Vec::with_capacity(compounds.len());
        for index in 0..compounds.len() {
            let mut parse_order = vec![];
            for left in (index..compounds.len()).rev() {
                parse_order.extend(compounds[left].iter().cloned());
                if left > index {
                    parse_order.push(Component::Combinator(combinators[left - 1]));
                }
            }
            let prefix = Selector::from(parse_order)
                .to_css_string(PrinterOptions::default())
                .ok()?;
            let Ok(prefix) = ElementSelector::new(&prefix) else {
                log::debug!("cannot determine structural dependencies of {prefix}");
                return None;
            };
            prefixes.push(prefix);
        }

        let compounds: Vec<_> = compounds
            .iter()
            .map(|compound| compound_dependencies(compound))
            .collect();
        let has_structural_component = selector.iter_raw_match_order().any(|component| {
            matches!(
                component,
                Component::Root | Component::Empty | Component::Scope | Component::Nesting
            )
        });
        Some(Self {
            compounds,
            combinators,
            prefixes,
            has_structural_component,
        })
    }

    fn is_structural(&self) -> bool {
        !self.combinators.is_empty()
            || self.has_structural_component
            || self
                .compounds
                .iter()
                .any(|compound| compound.is_structural())
    }
}

fn collect_selectors<'a, 'input>(
    rules: &'a [CssRule<'input>],
    output: &mut Vec<&'a Selector<'input>>,
) {
    for rule in rules {
        match rule {
            CssRule::Style(style) => output.extend(style.selectors.0.iter()),
            CssRule::Media(media) => collect_selectors(&media.rules.0, output),
            CssRule::Container(container) => collect_selectors(&container.rules.0, output),
            CssRule::Supports(supports) => collect_selectors(&supports.rules.0, output),
            CssRule::LayerBlock(layer) => collect_selectors(&layer.rules.0, output),
            _ => {}
        }
    }
}

fn compound_dependencies(compound: &[Component]) -> CompoundDependencies {
    compound
        .iter()
        .map(component_dependencies)
        .fold(CompoundDependencies::default(), CompoundDependencies::union)
}

fn component_dependencies(component: &Component) -> CompoundDependencies {
    match component {
        Component::Nth(_) => CompoundDependencies {
            position: true,
            ..CompoundDependencies::default()
        },
        Component::NthOf(nth_of) => CompoundDependencies {
            position: true,
            ..nested_dependencies(nth_of.selectors())
        },
        Component::Has(_) => CompoundDependencies {
            position: true,
            ancestors: false,
            subtree: true,
        },
        Component::Negation(selectors)
        | Component::Is(selectors)
        | Component::Where(selectors)
        | Component::Any(_, selectors) => nested_dependencies(selectors),
        Component::Host(Some(selector)) | Component::Slotted(selector) => {
            nested_dependencies(std::slice::from_ref(selector))
        }
        _ => CompoundDependencies::default(),
    }
}

fn nested_dependencies(selectors: &[Selector]) -> CompoundDependencies {
    selectors
        .iter()
        .map(|selector| {
            let mut iter = selector.iter();
            let mut dependencies = CompoundDependencies::default();
            loop {
                dependencies = dependencies.union(compound_dependencies(
                    &iter.by_ref().cloned().collect::<Vec<_>>(),
                ));
                if iter.next_sequence().is_none() {
                    break;
                }
                dependencies.ancestors = true;
                dependencies.position = true;
            }
            dependencies
        })
        .fold(CompoundDependencies::default(), CompoundDependencies::union)
}

fn related_elements<'input, 'arena>(
    element: &Element<'input, 'arena>,
    combinator: Combinator,
) -> Vec<Element<'input, 'arena>> {
    match combinator {
        Combinator::Child => Element::parent_element(element).into_iter().collect(),
        Combinator::Descendant | Combinator::DeepDescendant | Combinator::Deep => {
            std::iter::successors(Element::parent_element(element), Element::parent_element)
                .collect()
        }
        Combinator::NextSibling => element.previous_element_sibling().into_iter().collect(),
        Combinator::LaterSibling => {
            std::iter::successors(element.previous_element_sibling(), |sibling| {
                sibling.previous_element_sibling()
            })
            .collect()
        }
        Combinator::PseudoElement | Combinator::SlotAssignment | Combinator::Part => {
            vec![element.clone()]
        }
    }
}
