//! Elements implicated by structure-sensitive CSS selectors.
//!
//! A rewrite may drop or retag an element only when that element is not part of
//! a selector relationship that already holds. The relationship is read from
//! the tree before the rewrite: the matched subject, and any anchor whose link
//! to elements outside its own subtree the selector depends on.

use std::{cell::RefCell, collections::HashSet};

use lightningcss::{
    rules::{CssRule, CssRuleList},
    selector::{Combinator, Component, Selector},
    stylesheet::PrinterOptions,
};
use oxvg_serialize::ToValue as _;

use crate::{
    element::Element,
    get_attribute,
    node::{AllocationID, Type},
};

/// Elements a structure-sensitive selector depends on.
#[derive(Debug, Default)]
pub struct Implication {
    /// Flattening or removing these elements changes an existing structural match.
    pub structural: HashSet<AllocationID>,
    /// Changing these elements' local names changes an existing or prospective match.
    pub type_sensitive: HashSet<AllocationID>,
}

/// Collects implicated elements from stylesheets already parsed for `root`.
pub fn collect<'input, 'arena>(
    styles: &[RefCell<CssRuleList<'input>>],
    root: &Element<'input, 'arena>,
) -> Implication {
    let mut implication = Implication::default();
    for style in styles {
        let rules = style.borrow();
        walk_rules(&rules, &mut |selector| {
            if !selector_is_structure_sensitive(selector) {
                return;
            }
            implicate_selector(selector, root, &mut implication);
            implicate_negated_relationships(selector, root, &mut implication);
            implicate_prospective_paths(selector, root, &mut implication);
        });
    }
    implication
}

/// Whether `selector` depends on document structure rather than only on one element.
pub fn selector_is_structure_sensitive(selector: &Selector<'_>) -> bool {
    selector.iter_raw_match_order().any(component_is_structural)
}

fn component_is_structural(component: &Component<'_>) -> bool {
    match component {
        Component::Combinator(combinator) if combinator.is_tree_combinator() => true,
        Component::Nth(data) => {
            let mut label = String::new();
            let _ = data.write_start(&mut label, data.is_function());
            !label.contains("col")
        }
        Component::NthOf(_) | Component::Empty | Component::Root | Component::Has(_) => true,
        Component::Is(list) | Component::Where(list) | Component::Negation(list) => {
            list.iter().any(selector_is_structure_sensitive)
        }
        Component::Any(_, list) => list.iter().any(selector_is_structure_sensitive),
        _ => false,
    }
}

fn walk_rules<'input>(rules: &CssRuleList<'input>, visit: &mut impl FnMut(&Selector<'input>)) {
    for rule in &rules.0 {
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

fn implicate_selector<'input, 'arena>(
    selector: &Selector<'input>,
    root: &Element<'input, 'arena>,
    out: &mut Implication,
) {
    let chain = Chain::parse(selector);
    if !chain.should_mark() {
        return;
    }
    for_each_element(root, |element| {
        let mut walk = Walk {
            out,
            mark: true,
            ignore_subject_path: false,
        };
        walk.record(element, &chain);
    });
}

fn implicate_negated_relationships<'input, 'arena>(
    selector: &Selector<'input>,
    root: &Element<'input, 'arena>,
    out: &mut Implication,
) {
    for component in selector.iter_raw_match_order() {
        match component {
            Component::Negation(list) => {
                for inner in list.iter() {
                    if has_multi_element_relationship(inner) {
                        implicate_selector(inner, root, out);
                    }
                    implicate_negated_relationships(inner, root, out);
                }
            }
            Component::Is(list)
            | Component::Where(list)
            | Component::Has(list)
            | Component::Any(_, list) => {
                for inner in list.iter() {
                    implicate_negated_relationships(inner, root, out);
                }
            }
            Component::NthOf(nth_of) => {
                for inner in nth_of.selectors() {
                    implicate_negated_relationships(inner, root, out);
                }
            }
            _ => {}
        }
    }
}

fn implicate_prospective_paths<'input, 'arena>(
    selector: &Selector<'input>,
    root: &Element<'input, 'arena>,
    out: &mut Implication,
) {
    let chain = Chain::parse(selector);
    if !chain.subject_type_is("path") {
        return;
    }
    for_each_element(root, |element| {
        if element.local_name().as_str() == "path" {
            return;
        }
        let mut walk = Walk {
            out,
            mark: false,
            ignore_subject_path: true,
        };
        if walk.record(element, &chain) {
            out.type_sensitive.insert(element.id());
        }
    });
}

fn has_multi_element_relationship(selector: &Selector<'_>) -> bool {
    selector.iter_raw_match_order().any(|component| {
        matches!(component, Component::Combinator(combinator) if combinator.is_tree_combinator())
            || matches!(component, Component::Has(_))
    }) || selector
        .iter_raw_match_order()
        .any(|component| match component {
            Component::Is(list) | Component::Where(list) | Component::Negation(list) => {
                list.iter().any(has_multi_element_relationship)
            }
            Component::Any(_, list) => list.iter().any(has_multi_element_relationship),
            _ => false,
        })
}

fn for_each_element<'input, 'arena>(
    root: &Element<'input, 'arena>,
    mut visit: impl FnMut(&Element<'input, 'arena>),
) {
    if root.node_type() == Type::Element {
        visit(root);
    }
    for element in root.breadth_first() {
        visit(&element);
    }
}

struct Walk<'a> {
    out: &'a mut Implication,
    mark: bool,
    ignore_subject_path: bool,
}

impl Walk<'_> {
    fn record<'input, 'arena>(&mut self, element: &Element<'input, 'arena>, chain: &Chain) -> bool {
        self.record_from(element, chain, 0, None)
    }

    fn record_from<'input, 'arena>(
        &mut self,
        element: &Element<'input, 'arena>,
        chain: &Chain,
        index: usize,
        scope: Option<&Element<'input, 'arena>>,
    ) -> bool {
        let Some(compound) = chain.compounds.get(index) else {
            return false;
        };
        if !self.compound_matches(element, compound, index == 0, scope) {
            return false;
        }
        let left_matches = if index + 1 >= chain.compounds.len() {
            true
        } else {
            self.combinator_matches(element, chain, index, scope)
        };
        if !left_matches {
            return false;
        }
        if self.mark {
            self.mark_match(element, compound, scope);
        }
        true
    }

    fn combinator_matches<'input, 'arena>(
        &mut self,
        element: &Element<'input, 'arena>,
        chain: &Chain,
        index: usize,
        scope: Option<&Element<'input, 'arena>>,
    ) -> bool {
        let Some(combinator) = chain.combinators.get(index).copied() else {
            return false;
        };
        let next = index + 1;
        match combinator {
            Combinator::Child => element
                .parent_element()
                .is_some_and(|parent| self.record_from(&parent, chain, next, scope)),
            Combinator::Descendant | Combinator::Deep | Combinator::DeepDescendant => {
                let mut matched = false;
                let mut parent = element.parent_element();
                while let Some(ancestor) = parent {
                    if self.record_from(&ancestor, chain, next, scope) {
                        matched = true;
                        if !self.mark {
                            break;
                        }
                    }
                    parent = ancestor.parent_element();
                }
                matched
            }
            Combinator::NextSibling => element
                .previous_element_sibling()
                .is_some_and(|sibling| self.record_from(&sibling, chain, next, scope)),
            Combinator::LaterSibling => {
                let mut matched = false;
                let mut sibling = element.previous_element_sibling();
                while let Some(previous) = sibling {
                    if self.record_from(&previous, chain, next, scope) {
                        matched = true;
                        if !self.mark {
                            break;
                        }
                    }
                    sibling = previous.previous_element_sibling();
                }
                matched
            }
            _ => false,
        }
    }

    fn compound_matches<'input, 'arena>(
        &mut self,
        element: &Element<'input, 'arena>,
        compound: &Compound,
        is_subject: bool,
        scope: Option<&Element<'input, 'arena>>,
    ) -> bool {
        compound.preds.iter().all(|pred| match pred {
            Pred::Type(name) => {
                (self.ignore_subject_path && is_subject && name == "path")
                    || element.local_name().as_str() == name
            }
            Pred::Id(id) => element_has_id(element, id),
            Pred::Class(class) => element.has_class(class),
            Pred::AttrExists(name) => attribute_string(element, name).is_some(),
            Pred::Empty => css_empty(element),
            Pred::Root => element.is_root(),
            Pred::Nth { nth, of } => nth_matches(element, nth, of),
            Pred::Not(chains) => !chains
                .iter()
                .any(|chain| self.matches_chain(element, chain, scope)),
            Pred::Is(chains) => chains
                .iter()
                .any(|chain| self.matches_chain(element, chain, scope)),
            Pred::Has(chains) => chains
                .iter()
                .any(|chain| self.relative_matches(element, chain)),
            Pred::Scope => scope.is_some_and(|anchor| anchor.id() == element.id()),
            Pred::True => true,
        })
    }

    fn matches_chain<'input, 'arena>(
        &mut self,
        element: &Element<'input, 'arena>,
        chain: &Chain,
        scope: Option<&Element<'input, 'arena>>,
    ) -> bool {
        let mark = self.mark;
        self.mark = false;
        let matched = self.record_from(element, chain, 0, scope);
        self.mark = mark;
        matched
    }

    fn relative_matches<'input, 'arena>(
        &mut self,
        anchor: &Element<'input, 'arena>,
        chain: &Chain,
    ) -> bool {
        if let Some(rest) = chain.without_scope() {
            let combinator = chain.combinators.last().copied();
            return match combinator {
                Some(Combinator::Child) => anchor
                    .children_iter()
                    .any(|child| self.matches_chain(&child, &rest, Some(anchor))),
                Some(Combinator::NextSibling) => anchor
                    .next_element_sibling()
                    .is_some_and(|sibling| self.matches_chain(&sibling, &rest, Some(anchor))),
                Some(Combinator::LaterSibling) => following_siblings(anchor)
                    .any(|sibling| self.matches_chain(&sibling, &rest, Some(anchor))),
                Some(Combinator::Descendant | Combinator::Deep | Combinator::DeepDescendant)
                | None => anchor
                    .breadth_first()
                    .any(|descendant| self.matches_chain(&descendant, &rest, Some(anchor))),
                _ => false,
            };
        }
        anchor
            .breadth_first()
            .any(|descendant| self.matches_chain(&descendant, chain, Some(anchor)))
    }

    fn mark_match<'input, 'arena>(
        &mut self,
        element: &Element<'input, 'arena>,
        compound: &Compound,
        scope: Option<&Element<'input, 'arena>>,
    ) {
        self.out.structural.insert(element.id());
        if compound.of_type || compound_type_matters(element, compound) {
            self.out.type_sensitive.insert(element.id());
        }
        if compound.preceding || compound.following {
            mark_sibling_axis(element, compound, self.out);
        }
        for pred in &compound.preds {
            match pred {
                Pred::Is(chains) => {
                    for chain in chains {
                        if chain.should_mark() && self.matches_chain(element, chain, scope) {
                            let mark = self.mark;
                            self.mark = true;
                            self.record_from(element, chain, 0, scope);
                            self.mark = mark;
                        }
                    }
                }
                Pred::Has(chains) => {
                    for chain in chains {
                        self.record_relative(element, chain);
                    }
                }
                _ => {}
            }
        }
    }

    fn record_relative<'input, 'arena>(&mut self, anchor: &Element<'input, 'arena>, chain: &Chain) {
        if let Some(rest) = chain.without_scope() {
            let combinator = chain.combinators.last().copied();
            match combinator {
                Some(Combinator::Child) => {
                    for child in anchor.children_iter() {
                        self.record_from(&child, &rest, 0, Some(anchor));
                    }
                }
                Some(Combinator::NextSibling) => {
                    if let Some(sibling) = anchor.next_element_sibling() {
                        self.record_from(&sibling, &rest, 0, Some(anchor));
                    }
                }
                Some(Combinator::LaterSibling) => {
                    for sibling in following_siblings(anchor) {
                        self.record_from(&sibling, &rest, 0, Some(anchor));
                    }
                }
                Some(Combinator::Descendant | Combinator::Deep | Combinator::DeepDescendant)
                | None => {
                    for descendant in anchor.breadth_first() {
                        self.record_from(&descendant, &rest, 0, Some(anchor));
                    }
                }
                _ => {}
            }
            return;
        }
        for descendant in anchor.breadth_first() {
            self.record_from(&descendant, chain, 0, Some(anchor));
        }
    }
}

fn mark_sibling_axis<'input, 'arena>(
    element: &Element<'input, 'arena>,
    compound: &Compound,
    out: &mut Implication,
) {
    let Some(parent) = element.parent_element() else {
        return;
    };
    out.structural.insert(parent.id());
    let name = element.local_name().as_str().to_string();
    for sibling in parent.children_iter() {
        if sibling.id() == element.id() {
            continue;
        }
        let before = sibling_is_before(&sibling, element);
        if before && !compound.preceding {
            continue;
        }
        if !before && !compound.following {
            continue;
        }
        if compound.of_type && !sibling_affects_type(&sibling, &name) {
            continue;
        }
        out.structural.insert(sibling.id());
        if compound.of_type && sibling.local_name().as_str() == name {
            out.type_sensitive.insert(sibling.id());
        }
    }
}

fn sibling_affects_type<'input, 'arena>(sibling: &Element<'input, 'arena>, name: &str) -> bool {
    sibling.local_name().as_str() == name
        || sibling
            .breadth_first()
            .any(|descendant| descendant.local_name().as_str() == name)
}

fn sibling_is_before<'input, 'arena>(
    sibling: &Element<'input, 'arena>,
    element: &Element<'input, 'arena>,
) -> bool {
    let mut previous = element.previous_element_sibling();
    while let Some(current) = previous {
        if current.id() == sibling.id() {
            return true;
        }
        previous = current.previous_element_sibling();
    }
    false
}

fn following_siblings<'input, 'arena>(
    element: &Element<'input, 'arena>,
) -> impl Iterator<Item = Element<'input, 'arena>> {
    let mut current = element.next_element_sibling();
    std::iter::from_fn(move || {
        let sibling = current.clone()?;
        current = sibling.next_element_sibling();
        Some(sibling)
    })
}

fn nth_matches<'input, 'arena>(
    element: &Element<'input, 'arena>,
    data: &NthData,
    of: &[Chain],
) -> bool {
    if data.label.contains("col") {
        return true;
    }
    let Some(parent) = element.parent_element() else {
        return false;
    };
    let matched: Vec<_> = parent
        .children_iter()
        .filter(|sibling| nth_filter(sibling, element, data, of))
        .collect();
    let Some(position) = matched
        .iter()
        .position(|sibling| sibling.id() == element.id())
    else {
        return false;
    };
    if data.label.contains("only") {
        return matched.len() == 1;
    }
    let index = if data.ends_at_last() {
        matched.len() - position
    } else {
        position + 1
    };
    an_plus_b(data.a, data.b, i32::try_from(index).unwrap_or(i32::MAX))
}

fn nth_filter<'input, 'arena>(
    sibling: &Element<'input, 'arena>,
    subject: &Element<'input, 'arena>,
    data: &NthData,
    of: &[Chain],
) -> bool {
    if !of.is_empty() {
        return of.iter().any(|chain| {
            let mut walk = Walk {
                out: &mut Implication::default(),
                mark: false,
                ignore_subject_path: false,
            };
            walk.record(sibling, chain)
        });
    }
    if data.of_type() {
        return sibling.local_name().as_str() == subject.local_name().as_str();
    }
    true
}

fn an_plus_b(a: i32, b: i32, index: i32) -> bool {
    if a == 0 {
        return index == b;
    }
    if (index - b) % a != 0 {
        return false;
    }
    (index - b) / a >= 0
}

fn compound_type_matters<'input, 'arena>(
    element: &Element<'input, 'arena>,
    compound: &Compound,
) -> bool {
    compound.preds.iter().any(|pred| match pred {
        Pred::Type(_) => true,
        Pred::Is(chains) => chains.iter().any(|chain| {
            chain.subject_has_type()
                && Walk {
                    out: &mut Implication::default(),
                    mark: false,
                    ignore_subject_path: false,
                }
                .record(element, chain)
        }),
        _ => false,
    })
}

fn element_has_id(element: &Element<'_, '_>, expected: &str) -> bool {
    get_attribute!(element, Id).is_some_and(|id| id.0.as_str() == expected)
}

fn attribute_string(element: &Element<'_, '_>, name: &str) -> Option<String> {
    let name = name.into();
    let attr = element.get_attribute_local(&name)?;
    attr.to_value_string(PrinterOptions::default()).ok()
}

fn css_empty(element: &Element<'_, '_>) -> bool {
    element.child_nodes_iter().all(|child| {
        child.node_type() == Type::Text
            && child
                .text_content()
                .is_none_or(|text| text.trim().is_empty())
    })
}

#[derive(Clone)]
struct NthData {
    a: i32,
    b: i32,
    label: String,
}

impl NthData {
    fn ends_at_last(&self) -> bool {
        self.label.contains("last")
    }

    fn of_type(&self) -> bool {
        self.label.contains("of-type")
    }
}

struct Chain {
    compounds: Vec<Compound>,
    combinators: Vec<Combinator>,
}

struct Compound {
    preds: Vec<Pred>,
    preceding: bool,
    following: bool,
    of_type: bool,
}

enum Pred {
    Type(String),
    Id(String),
    Class(String),
    AttrExists(String),
    Empty,
    Root,
    Nth { nth: NthData, of: Vec<Chain> },
    Not(Vec<Chain>),
    Is(Vec<Chain>),
    Has(Vec<Chain>),
    Scope,
    True,
}

impl Chain {
    fn parse(selector: &Selector<'_>) -> Self {
        let mut iter = selector.iter();
        let mut compounds = Vec::new();
        let mut combinators = Vec::new();
        loop {
            let mut preds = Vec::new();
            for component in iter.by_ref() {
                if let Some(pred) = Pred::parse(component) {
                    preds.push(pred);
                }
            }
            let compound = Compound::from_preds(preds);
            let combinator = iter.next_sequence();
            if compounds.is_empty() && combinator == Some(Combinator::PseudoElement) {
                continue;
            }
            compounds.push(compound);
            match combinator {
                Some(combinator) => combinators.push(combinator),
                None => break,
            }
        }
        Self {
            compounds,
            combinators,
        }
    }

    fn should_mark(&self) -> bool {
        self.combinators.iter().any(Combinator::is_tree_combinator)
            || self.compounds.first().is_some_and(Compound::marks_self)
    }

    fn subject_has_type(&self) -> bool {
        self.compounds.first().is_some_and(|compound| {
            compound
                .preds
                .iter()
                .any(|pred| matches!(pred, Pred::Type(_)))
        })
    }

    fn subject_type_is(&self, name: &str) -> bool {
        self.compounds.first().is_some_and(|compound| {
            compound
                .preds
                .iter()
                .any(|pred| matches!(pred, Pred::Type(ty) if ty == name))
        })
    }

    fn without_scope(&self) -> Option<Self> {
        let last = self.compounds.last()?;
        if !last.preds.iter().any(|pred| matches!(pred, Pred::Scope)) {
            return None;
        }
        if self.compounds.len() < 2 {
            return None;
        }
        Some(Self {
            compounds: self.compounds[..self.compounds.len() - 1].to_vec(),
            combinators: self.combinators[..self.combinators.len().saturating_sub(1)].to_vec(),
        })
    }
}

impl Compound {
    fn from_preds(preds: Vec<Pred>) -> Self {
        let (preceding, following, of_type) = sibling_axes(&preds);
        Self {
            preds,
            preceding,
            following,
            of_type,
        }
    }

    fn marks_self(&self) -> bool {
        self.preceding
            || self.following
            || self
                .preds
                .iter()
                .any(|pred| matches!(pred, Pred::Empty | Pred::Root | Pred::Has(_)))
    }
}

impl Clone for Chain {
    fn clone(&self) -> Self {
        Self {
            compounds: self.compounds.clone(),
            combinators: self.combinators.clone(),
        }
    }
}

impl Clone for Compound {
    fn clone(&self) -> Self {
        Self {
            preds: self.preds.clone(),
            preceding: self.preceding,
            following: self.following,
            of_type: self.of_type,
        }
    }
}

impl Clone for Pred {
    fn clone(&self) -> Self {
        match self {
            Self::Type(value) => Self::Type(value.clone()),
            Self::Id(value) => Self::Id(value.clone()),
            Self::Class(value) => Self::Class(value.clone()),
            Self::AttrExists(value) => Self::AttrExists(value.clone()),
            Self::Empty => Self::Empty,
            Self::Root => Self::Root,
            Self::Nth { nth, of } => Self::Nth {
                nth: nth.clone(),
                of: of.clone(),
            },
            Self::Not(chains) => Self::Not(chains.clone()),
            Self::Is(chains) => Self::Is(chains.clone()),
            Self::Has(chains) => Self::Has(chains.clone()),
            Self::Scope => Self::Scope,
            Self::True => Self::True,
        }
    }
}

fn sibling_axes(preds: &[Pred]) -> (bool, bool, bool) {
    let mut preceding = false;
    let mut following = false;
    let mut of_type = false;
    for pred in preds {
        match pred {
            Pred::Nth { nth, .. } => {
                apply_nth_axis(nth, &mut preceding, &mut following, &mut of_type);
            }
            Pred::Not(chains) | Pred::Is(chains) => {
                for chain in chains {
                    if !chain.combinators.is_empty() {
                        continue;
                    }
                    if let Some(inner) = chain.compounds.first() {
                        preceding |= inner.preceding;
                        following |= inner.following;
                        of_type |= inner.of_type;
                    }
                }
            }
            _ => {}
        }
    }
    (preceding, following, of_type)
}

fn apply_nth_axis(data: &NthData, preceding: &mut bool, following: &mut bool, of_type: &mut bool) {
    if data.label.contains("col") {
        return;
    }
    if data.label.contains("only") {
        *preceding = true;
        *following = true;
    } else if data.ends_at_last() {
        *following = true;
    } else {
        *preceding = true;
    }
    if data.of_type() {
        *of_type = true;
    }
}

impl Pred {
    fn parse(component: &Component<'_>) -> Option<Self> {
        Some(match component {
            Component::LocalName(name) => Self::Type(name.name.as_ref().to_string()),
            Component::ID(id) => Self::Id(id.as_ref().to_string()),
            Component::Class(class) => Self::Class(class.as_ref().to_string()),
            Component::AttributeInNoNamespaceExists { local_name, .. }
            | Component::AttributeInNoNamespace { local_name, .. } => {
                Self::AttrExists(local_name.as_ref().to_string())
            }
            Component::Empty => Self::Empty,
            Component::Root => Self::Root,
            Component::Scope => Self::Scope,
            Component::Nth(data) => {
                let mut label = String::new();
                let _ = data.write_start(&mut label, data.is_function());
                Self::Nth {
                    nth: NthData {
                        a: data.a,
                        b: data.b,
                        label,
                    },
                    of: Vec::new(),
                }
            }
            Component::NthOf(data) => {
                let nth = data.nth_data();
                let mut label = String::new();
                let _ = nth.write_start(&mut label, nth.is_function());
                Self::Nth {
                    nth: NthData {
                        a: nth.a,
                        b: nth.b,
                        label,
                    },
                    of: data.selectors().iter().map(Chain::parse).collect(),
                }
            }
            Component::Negation(list) => Self::Not(list.iter().map(Chain::parse).collect()),
            Component::Is(list) | Component::Where(list) | Component::Any(_, list) => {
                Self::Is(list.iter().map(Chain::parse).collect())
            }
            Component::Has(list) => Self::Has(list.iter().map(Chain::parse).collect()),
            Component::NonTSPseudoClass(_)
            | Component::ExplicitUniversalType
            | Component::ExplicitAnyNamespace
            | Component::ExplicitNoNamespace
            | Component::DefaultNamespace(_)
            | Component::Namespace(_, _)
            | Component::AttributeOther(_)
            | Component::Nesting => Self::True,
            _ => return None,
        })
    }
}
