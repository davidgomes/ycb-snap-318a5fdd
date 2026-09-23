//! Finds the elements whose stylesheet matching depends on the structure of the document.
use std::{cell::RefCell, collections::HashSet, convert::Infallible};

use lightningcss::{
    printer::PrinterOptions,
    rules::CssRuleList,
    selector::{Combinator, Component, Selector},
    traits::ToCss,
    visit_types,
    visitor::{Visit, VisitTypes, Visitor},
};
use oxvg_ast::{
    element::{Element, HashableElement},
    is_element,
    selectors::{SelectElement, Selector as MatchingSelector},
};
use parcel_selectors::parser::NthType;

#[derive(Default)]
/// Elements participating in a full match of a structure-sensitive selector, as found in the
/// document before any rewrite takes place.
///
/// A selector is structure-sensitive when it has a combinator or a positional pseudo-class
/// (e.g. `g > path`, `.a + .b`, `path:first-child`).
pub struct StructuralSelectors<'input, 'arena> {
    /// Targets and anchors of matching selectors, which must keep their own identity and
    /// attributes.
    anchors: HashSet<HashableElement<'input, 'arena>>,
    /// Elements whose match depends on the siblings preceding them
    preceding_dependent: HashSet<HashableElement<'input, 'arena>>,
    /// Elements whose match depends on the siblings following them
    following_dependent: HashSet<HashableElement<'input, 'arena>>,
    /// Whether a selector could not be evaluated, in which case every element is protected
    unknown: bool,
}

impl<'input, 'arena> StructuralSelectors<'input, 'arena> {
    /// Collects the implicated elements of each structure-sensitive selector in the stylesheets.
    pub fn new(
        root: &Element<'input, 'arena>,
        stylesheets: &[RefCell<CssRuleList<'input>>],
    ) -> Self {
        let mut collector = Collector {
            root,
            result: Self::default(),
        };
        for stylesheet in stylesheets {
            let Ok(()) = stylesheet.borrow_mut().0.visit(&mut collector);
        }
        collector.result
    }

    /// Whether the element is the target or an anchor of a structure-sensitive selector.
    pub fn is_anchor(&self, element: &Element<'input, 'arena>) -> bool {
        self.unknown
            || self
                .anchors
                .contains(&HashableElement::new(element.clone()))
    }

    /// Whether the element's match depends on the siblings preceding it.
    pub fn depends_on_preceding(&self, element: &Element<'input, 'arena>) -> bool {
        self.unknown
            || self
                .preceding_dependent
                .contains(&HashableElement::new(element.clone()))
    }

    /// Whether the element's match depends on the siblings following it.
    pub fn depends_on_following(&self, element: &Element<'input, 'arena>) -> bool {
        self.unknown
            || self
                .following_dependent
                .contains(&HashableElement::new(element.clone()))
    }

    fn add_selector(&mut self, root: &Element<'input, 'arena>, selector: &Selector<'input>) {
        let complex = Complex::new(selector);
        if !complex.is_structure_sensitive() {
            return;
        }
        let Some(prefixes) = complex.matching_prefixes() else {
            log::debug!("cannot evaluate selector {selector:?}, protecting document");
            self.unknown = true;
            return;
        };

        let (target_selector, prefixes) = prefixes
            .split_last()
            .expect("complex selector has at least one compound");
        let mut frontier: Vec<_> = std::iter::once(root.clone())
            .chain(root.breadth_first())
            .filter(|element| {
                is_element!(element)
                    && target_selector.matches_naive(&SelectElement::new(element.clone()))
            })
            .collect();
        let (target_compound, compounds) = complex
            .compounds
            .split_last()
            .expect("complex selector has at least one compound");
        self.record(&frontier, target_compound);

        for ((compound, combinator), selector) in compounds
            .iter()
            .zip(&complex.combinators)
            .zip(prefixes)
            .rev()
        {
            #[allow(clippy::mutable_key_type)]
            let mut seen = HashSet::new();
            let mut anchors = Vec::new();
            for element in &frontier {
                for candidate in related(element, *combinator) {
                    if selector.matches_naive(&SelectElement::new(candidate.clone()))
                        && seen.insert(HashableElement::new(candidate.clone()))
                    {
                        anchors.push(candidate);
                    }
                }
            }
            self.record(&anchors, compound);
            frontier = anchors;
        }
    }

    fn record(&mut self, elements: &[Element<'input, 'arena>], compound: &Compound<'input>) {
        for element in elements {
            self.anchors.insert(HashableElement::new(element.clone()));
            if compound.depends_on_preceding {
                self.preceding_dependent
                    .insert(HashableElement::new(element.clone()));
            }
            if compound.depends_on_following {
                self.following_dependent
                    .insert(HashableElement::new(element.clone()));
            }
            if compound.is_opaque {
                self.record_neighbourhood(element);
            }
        }
    }

    /// For relationships that cannot be evaluated, protect every element that may take part in
    /// them: ancestors, siblings, and their descendants.
    fn record_neighbourhood(&mut self, element: &Element<'input, 'arena>) {
        let element = HashableElement::new(element.clone());
        self.preceding_dependent.insert(element.clone());
        self.following_dependent.insert(element.clone());
        let mut ancestor = element.parent_element();
        if let Some(parent) = &ancestor {
            self.anchors
                .extend(parent.breadth_first().map(HashableElement::new));
        } else {
            self.anchors
                .extend(element.breadth_first().map(HashableElement::new));
        }
        while let Some(current) = ancestor {
            ancestor = current.parent_element();
            self.anchors.insert(HashableElement::new(current));
        }
    }
}

struct Collector<'a, 'input, 'arena> {
    root: &'a Element<'input, 'arena>,
    result: StructuralSelectors<'input, 'arena>,
}

impl<'input> Visitor<'input> for Collector<'_, 'input, '_> {
    type Error = Infallible;

    fn visit_types(&self) -> VisitTypes {
        visit_types!(SELECTORS)
    }

    fn visit_selector(&mut self, selector: &mut Selector<'input>) -> Result<(), Self::Error> {
        self.result.add_selector(self.root, selector);
        Ok(())
    }
}

/// A compound selector reduced to the components that can be evaluated against the document.
///
/// Components that cannot be evaluated are treated as matching any element, so matches are
/// a superset of the real matches.
#[derive(Default)]
struct Compound<'input> {
    components: Vec<Component<'input>>,
    depends_on_preceding: bool,
    depends_on_following: bool,
    /// Whether the compound contains relationships to other elements which cannot be evaluated
    is_opaque: bool,
}

/// A complex selector in parse order, where `combinators[i]` sits between `compounds[i]` and
/// `compounds[i + 1]`.
struct Complex<'input> {
    compounds: Vec<Compound<'input>>,
    combinators: Vec<Combinator>,
}

impl<'input> Complex<'input> {
    fn new(selector: &Selector<'input>) -> Self {
        let mut compounds = vec![Compound::default()];
        let mut combinators = vec![];
        let mut iter = selector.iter();
        loop {
            let compound = compounds
                .last_mut()
                .expect("compounds should never be empty");
            for component in &mut iter {
                compound.push(component);
            }
            match iter.next_sequence() {
                None => break,
                Some(Combinator::PseudoElement | Combinator::SlotAssignment | Combinator::Part) => {
                }
                Some(combinator) => {
                    combinators.push(match combinator {
                        Combinator::Deep | Combinator::DeepDescendant => Combinator::Descendant,
                        combinator => combinator,
                    });
                    compounds.push(Compound::default());
                }
            }
        }
        compounds.reverse();
        combinators.reverse();
        Self {
            compounds,
            combinators,
        }
    }

    fn is_structure_sensitive(&self) -> bool {
        !self.combinators.is_empty()
            || self.compounds.iter().any(|compound| {
                compound.depends_on_preceding || compound.depends_on_following || compound.is_opaque
            })
    }

    /// Returns a selector for each compound, matching the compound along with every compound
    /// and combinator to it's left.
    fn matching_prefixes(&self) -> Option<Vec<MatchingSelector>> {
        let mut prefix = String::new();
        let mut result = Vec::with_capacity(self.compounds.len());
        for (index, compound) in self.compounds.iter().enumerate() {
            if index > 0 {
                prefix.push_str(match self.combinators[index - 1] {
                    Combinator::Child => " > ",
                    Combinator::NextSibling => " + ",
                    Combinator::LaterSibling => " ~ ",
                    _ => " ",
                });
            }
            if compound.components.is_empty() {
                prefix.push('*');
            } else {
                let selector = Selector::from(compound.components.clone());
                prefix.push_str(&selector.to_css_string(PrinterOptions::default()).ok()?);
            }
            result.push(MatchingSelector::new(&prefix).ok()?);
        }
        Some(result)
    }
}

impl<'input> Compound<'input> {
    fn push(&mut self, component: &Component<'input>) {
        match component {
            Component::Nth(data) => {
                self.add_position(data.ty);
                self.components.push(component.clone());
            }
            Component::NthOf(data) => {
                self.add_position(data.nth_data().ty);
                self.is_opaque = true;
            }
            Component::Negation(selectors) => {
                // A negated relationship may start matching when structure changes, so every
                // element possibly affected by it must be included
                let mut nested = Compound::default();
                let is_evaluable = nested.add_nested(selectors);
                let is_structural =
                    nested.depends_on_preceding || nested.depends_on_following || nested.is_opaque;
                self.depends_on_preceding |= nested.depends_on_preceding;
                self.depends_on_following |= nested.depends_on_following;
                self.is_opaque |= nested.is_opaque;
                if is_evaluable && !is_structural {
                    self.components.push(component.clone());
                }
            }
            Component::Is(selectors)
            | Component::Where(selectors)
            | Component::Any(_, selectors) => match &**selectors {
                [selector] if !selector.has_combinator() => {
                    selector.iter().for_each(|component| self.push(component));
                }
                _ => {
                    self.add_nested(selectors);
                }
            },
            Component::Has(_) => self.is_opaque = true,
            Component::LocalName(_)
            | Component::ExplicitUniversalType
            | Component::ID(_)
            | Component::Class(_)
            | Component::AttributeInNoNamespaceExists { .. }
            | Component::AttributeInNoNamespace { .. }
            | Component::Root
            | Component::Empty => self.components.push(component.clone()),
            _ => {}
        }
    }

    fn add_position(&mut self, ty: NthType) {
        match ty {
            NthType::Child | NthType::OfType | NthType::Col => self.depends_on_preceding = true,
            NthType::LastChild | NthType::LastOfType | NthType::LastCol => {
                self.depends_on_following = true;
            }
            NthType::OnlyChild | NthType::OnlyOfType => {
                self.depends_on_preceding = true;
                self.depends_on_following = true;
            }
        }
    }

    /// Includes the structural dependencies of selectors nested in a pseudo-class, returning
    /// whether every nested component can be evaluated.
    fn add_nested(&mut self, selectors: &[Selector<'input>]) -> bool {
        let mut is_evaluable = true;
        for selector in selectors {
            let complex = Complex::new(selector);
            if !complex.combinators.is_empty() {
                self.is_opaque = true;
            }
            for compound in &complex.compounds {
                self.depends_on_preceding |= compound.depends_on_preceding;
                self.depends_on_following |= compound.depends_on_following;
                self.is_opaque |= compound.is_opaque;
            }
            is_evaluable &= selector
                .iter_raw_match_order()
                .all(|component| match component {
                    Component::Combinator(combinator) => combinator.is_tree_combinator(),
                    Component::Nth(_)
                    | Component::LocalName(_)
                    | Component::ExplicitUniversalType
                    | Component::ID(_)
                    | Component::Class(_)
                    | Component::AttributeInNoNamespaceExists { .. }
                    | Component::AttributeInNoNamespace { .. }
                    | Component::Root
                    | Component::Empty => true,
                    Component::Negation(selectors) => Compound::default().add_nested(selectors),
                    _ => false,
                });
        }
        is_evaluable
    }
}

/// Elements that may sit to the left of `combinator` when `element` sits to it's right.
fn related<'input, 'arena>(
    element: &Element<'input, 'arena>,
    combinator: Combinator,
) -> Vec<Element<'input, 'arena>> {
    let mut result = Vec::new();
    match combinator {
        Combinator::Child => result.extend(element.parent_element()),
        Combinator::NextSibling => result.extend(element.previous_element_sibling()),
        Combinator::LaterSibling => {
            let mut sibling = element.previous_element_sibling();
            while let Some(current) = sibling {
                sibling = current.previous_element_sibling();
                result.push(current);
            }
        }
        _ => {
            let mut ancestor = element.parent_element();
            while let Some(current) = ancestor {
                ancestor = current.parent_element();
                result.push(current);
            }
        }
    }
    result
}
