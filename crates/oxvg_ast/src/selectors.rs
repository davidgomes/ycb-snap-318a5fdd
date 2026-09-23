//! Types used for selecting elements with css selectors.
use std::{
    collections::HashSet,
    hash::{DefaultHasher, Hash as _, Hasher},
    marker::PhantomData,
    ops::Deref,
};

use cssparser::ToCss;
use lightningcss::printer::PrinterOptions;
use oxvg_collections::{
    atom::Atom,
    attribute::{Attr, AttrId},
    element::ElementId,
    name::{self, Prefix, QualName},
};
use oxvg_serialize::ToValue as _;
use precomputed_hash::PrecomputedHash;
use selectors::{
    context::SelectorCaches,
    matching,
    parser::{
        Combinator, Component, NthSelectorData, NthType, ParseRelative, SelectorParseErrorKind,
    },
    SelectorList,
};

use crate::{
    element::{self, Element},
    get_attribute, is_attribute, is_element, node,
};

type A<'input> = Atom<'input>;
type P<'input> = Prefix<'input>;
type LN<'input> = Atom<'input>;
type NS<'input> = Atom<'input>;

#[derive(Debug, Clone)]
/// Specifies parser types
pub struct SelectorImpl {
    atom: PhantomData<A<'static>>,
    prefix: PhantomData<P<'static>>,
    name: PhantomData<LN<'static>>,
    namespace: PhantomData<NS<'static>>,
}

#[derive(Eq, PartialEq, Debug, Clone, Default)]
/// A value
pub struct CssAtom(pub A<'static>);
impl<'a> From<&'a str> for CssAtom {
    fn from(value: &'a str) -> Self {
        Self(value.to_string().into())
    }
}

#[derive(Eq, PartialEq, Clone, Default)]
/// A local name or prefix
pub struct CssName(pub A<'static>);
impl<'a> From<&'a str> for CssName {
    fn from(value: &'a str) -> Self {
        Self(value.to_string().into())
    }
}
impl Deref for CssName {
    type Target = [u8];

    fn deref(&self) -> &Self::Target {
        self.0.as_bytes()
    }
}

#[derive(Eq, PartialEq, Clone, Default)]
/// A namespace url
pub struct CssNamespace(pub NS<'static>);

#[derive(Eq, PartialEq, Clone)]
/// The type for a pseudo-class.
pub enum PseudoClass {
    /// :any-link
    AnyLink(
        PhantomData<A<'static>>,
        PhantomData<P<'static>>,
        PhantomData<LN<'static>>,
        PhantomData<NS<'static>>,
    ),
    /// :link
    Link(
        PhantomData<A<'static>>,
        PhantomData<P<'static>>,
        PhantomData<LN<'static>>,
        PhantomData<NS<'static>>,
    ),
}

#[derive(Eq, PartialEq, Clone)]
/// The type for a pseudo-element.
pub struct PseudoElement {
    atom: PhantomData<A<'static>>,
    prefix: PhantomData<P<'static>>,
    name: PhantomData<LN<'static>>,
    namespace: PhantomData<NS<'static>>,
}

impl ToCss for CssAtom {
    fn to_css<W>(&self, dest: &mut W) -> std::fmt::Result
    where
        W: std::fmt::Write,
    {
        cssparser::serialize_string(self.0.as_ref(), dest)
    }
}

impl AsRef<str> for CssAtom {
    fn as_ref(&self) -> &str {
        self.0.as_ref()
    }
}

impl ToCss for CssName {
    fn to_css<W>(&self, dest: &mut W) -> std::fmt::Result
    where
        W: std::fmt::Write,
    {
        cssparser::serialize_string(&self.0, dest)
    }
}

impl ToCss for PseudoClass {
    fn to_css<W>(&self, dest: &mut W) -> std::fmt::Result
    where
        W: std::fmt::Write,
    {
        dest.write_str(&self.to_css_string())
    }

    fn to_css_string(&self) -> String {
        match self {
            Self::Link(..) => ":link",
            Self::AnyLink(..) => ":any-link",
        }
        .into()
    }
}

impl PrecomputedHash for CssName {
    #[allow(clippy::cast_possible_truncation)] // fine for hash
    fn precomputed_hash(&self) -> u32 {
        let mut output = DefaultHasher::default();
        self.0.hash(&mut output);
        output.finish() as u32
    }
}

impl PrecomputedHash for CssNamespace {
    #[allow(clippy::cast_possible_truncation)] // fine for hash
    fn precomputed_hash(&self) -> u32 {
        let mut output = DefaultHasher::default();
        self.0.hash(&mut output);
        output.finish() as u32
    }
}

impl selectors::parser::NonTSPseudoClass for PseudoClass {
    type Impl = SelectorImpl;

    fn is_active_or_hover(&self) -> bool {
        false
    }

    fn is_user_action_state(&self) -> bool {
        false
    }

    fn visit<V>(&self, _visitor: &mut V) -> bool
    where
        V: selectors::visitor::SelectorVisitor<Impl = Self::Impl>,
    {
        false
    }
}

impl ToCss for PseudoElement {
    fn to_css<W>(&self, dest: &mut W) -> std::fmt::Result
    where
        W: std::fmt::Write,
    {
        dest.write_str(&self.to_css_string())
    }

    fn to_css_string(&self) -> String {
        String::default()
    }
}

impl selectors::parser::PseudoElement for PseudoElement {
    type Impl = SelectorImpl;
}

impl selectors::SelectorImpl for SelectorImpl {
    type AttrValue = CssAtom;
    type Identifier = CssName;
    type LocalName = CssName;
    type NamespacePrefix = CssName;
    type NamespaceUrl = CssNamespace;
    type BorrowedNamespaceUrl = CssNamespace;
    type BorrowedLocalName = CssName;

    type NonTSPseudoClass = PseudoClass;
    type PseudoElement = PseudoElement;

    type ExtraMatchingData<'a> = ();
}

/// An iterator for the elements matching a given selector.
#[allow(clippy::type_complexity)]
pub struct Select<'input, 'arena> {
    inner: element::Iterator<'input, 'arena>,
    scope: Option<Element<'input, 'arena>>,
    selector: Selector,
    selector_caches: SelectorCaches,
}

#[derive(Debug)]
/// A parsed selector.
pub struct Selector(selectors::parser::SelectorList<SelectorImpl>);

bitflags! {
    /// What a selector relies on for it to match an element, as reported by
    /// [`Selector::dependencies`].
    ///
    /// Changes to the document that keep each of these intact won't change whether the
    /// selector matches.
    #[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
    pub struct Dependency: u16 {
        /// The element is the subject of the selector, or the anchor matched by one of the
        /// compounds to the subject's left.
        const MATCHED = 1 << 0;
        /// The element's attributes, such as for `.a`, `#a`, or `[a]`.
        const ATTRIBUTES = 1 << 1;
        /// The element's parent, such as for the `b` of `a > b`, or for `:root`.
        const PARENT = 1 << 2;
        /// An ancestor of the element, such as for the `b` of `a b`.
        const ANCESTOR = 1 << 3;
        /// A preceding sibling of the element, such as for the `b` of `a + b` or `a ~ b`.
        const PREVIOUS_SIBLING = 1 << 4;
        /// Every preceding sibling of the element, such as for `b:not(a + b)`.
        const PREVIOUS_SIBLINGS = 1 << 5;
        /// The element's following siblings and their descendants, such as for `a:has(~ b)`.
        const NEXT_SIBLINGS = 1 << 6;
        /// How many sibling elements precede the element, such as for `:first-child`.
        const INDEX = 1 << 7;
        /// How many sibling elements of the same type precede the element, such as for
        /// `:first-of-type`.
        const INDEX_OF_TYPE = 1 << 8;
        /// How many sibling elements follow the element, such as for `:last-child`.
        const INDEX_FROM_END = 1 << 9;
        /// How many sibling elements of the same type follow the element, such as for
        /// `:last-of-type`.
        const INDEX_OF_TYPE_FROM_END = 1 << 10;
        /// The element's child nodes, such as for `:empty`.
        const CHILDREN = 1 << 11;
        /// The element's descendants, such as for `:has(a)`.
        const DESCENDANTS = 1 << 12;
    }
}

impl Dependency {
    /// Returns whether anything besides the element's own name and attributes is relied on.
    pub fn is_structural(self) -> bool {
        !(Self::MATCHED | Self::ATTRIBUTES).contains(self)
    }
}

/// A parser for selectors.
pub struct Parser;

impl<'input, 'arena> Select<'input, 'arena> {
    /// Creates an iterator over the elements matching the selector.
    ///
    /// # Errors
    /// If the selector fails to parse
    pub fn new<'a>(
        element: &'a Element<'input, 'arena>,
        selector: &'a str,
    ) -> Result<
        Select<'input, 'arena>,
        cssparser::ParseError<'a, selectors::parser::SelectorParseErrorKind<'a>>,
    > {
        Ok(Self::new_with_selector(element, Selector::new(selector)?))
    }

    /// Creates an iterator over the elements matching the selector, using the given selector.
    #[allow(clippy::type_complexity)]
    pub fn new_with_selector(
        element: &Element<'input, 'arena>,
        selector: Selector,
    ) -> Select<'input, 'arena> {
        Select {
            inner: element.breadth_first(),
            scope: Some(element.clone()),
            selector,
            selector_caches: SelectorCaches::default(),
        }
    }
}

impl<'input, 'arena> Iterator for Select<'input, 'arena> {
    type Item = Element<'input, 'arena>;

    fn next(&mut self) -> Option<Self::Item> {
        self.inner.find(|element| {
            Element::parent_element(element).is_some()
                && self.selector.matches_with_scope_and_cache(
                    &SelectElement {
                        element: element.clone(),
                    },
                    self.scope.clone(),
                    &mut self.selector_caches,
                )
        })
    }
}

impl Selector {
    /// # Errors
    /// If the selector fails to parse
    pub fn new(
        selector: &str,
    ) -> Result<Selector, cssparser::ParseError<'_, SelectorParseErrorKind<'_>>> {
        let parser_input = &mut cssparser::ParserInput::new(selector);
        let parser = &mut cssparser::Parser::new(parser_input);

        let list = SelectorList::parse(&Parser, parser, ParseRelative::No)?;
        Ok(Selector(list))
    }
}

impl<'input, 'arena> Selector {
    /// Returns whether the selector matches an element.
    pub fn matches_with_scope_and_cache(
        &self,
        element: &SelectElement<'input, 'arena>,
        scope: Option<Element<'input, 'arena>>,
        selector_caches: &mut SelectorCaches,
    ) -> bool {
        let mut context = matching::MatchingContext::new(
            matching::MatchingMode::Normal,
            None,
            selector_caches,
            matching::QuirksMode::NoQuirks,
            matching::NeedsSelectorFlags::No,
            matching::MatchingForInvalidation::No,
        );
        context.scope_element = scope.map(|e| selectors::Element::opaque(&SelectElement::new(e)));
        matching::matches_selector_list(&self.0, element, &mut context)
    }

    /// Returns whether the selector matches an element.
    pub fn matches_naive(&self, element: &SelectElement<'input, 'arena>) -> bool {
        self.matches_with_scope_and_cache(element, None, &mut SelectorCaches::default())
    }

    /// Returns whether the selector matches an element, reporting each element the match relies
    /// on to `dependency` along with how it's relied on.
    ///
    /// For each complex selector in the list that matches, the element and the nearest anchor
    /// matching each compound to its left are reported with [`Dependency::MATCHED`]. Elements
    /// that could otherwise match within a negation, such as the parent for `b:not(a > b)`, are
    /// reported with what the negation relies on.
    pub fn dependencies(
        &self,
        element: &Element<'input, 'arena>,
        mut dependency: impl FnMut(&Element<'input, 'arena>, Dependency),
    ) -> bool {
        let selector_caches = &mut SelectorCaches::default();
        let mut context = matching::MatchingContext::new(
            matching::MatchingMode::Normal,
            None,
            selector_caches,
            matching::QuirksMode::NoQuirks,
            matching::NeedsSelectorFlags::No,
            matching::MatchingForInvalidation::No,
        );
        let element = SelectElement::new(element.clone());
        let mut is_match = false;
        for selector in self.0.slice() {
            if matching::matches_selector(selector, 0, None, &element, &mut context) {
                is_match = true;
                match_dependencies(selector, &element, &mut context, &mut dependency);
            }
        }
        is_match
    }
}

fn match_dependencies<'input, 'arena>(
    selector: &selectors::parser::Selector<SelectorImpl>,
    element: &SelectElement<'input, 'arena>,
    context: &mut matching::MatchingContext<SelectorImpl>,
    dependency: &mut dyn FnMut(&Element<'input, 'arena>, Dependency),
) {
    let components = selector.iter_raw_match_order().as_slice();
    let mut element = element.clone();
    let mut offset = 0;
    loop {
        let end = components[offset..]
            .iter()
            .position(Component::is_combinator)
            .map_or(components.len(), |index| offset + index);
        let combinator = (end < components.len()).then(|| selector.combinator_at_match_order(end));
        let flags = Dependency::MATCHED
            | compound_dependencies(&components[offset..end], &element, dependency)
            | combinator.map_or(Dependency::empty(), |c| combinator_dependency(c, true));
        dependency(&element.element, flags);

        let Some(combinator) = combinator else {
            return;
        };
        offset = end + 1;
        // Whether the compounds to the left of a combinator match never depends on the elements
        // to its right, so the nearest anchor is enough to satisfy the rest of the selector.
        let Some(anchor) = related_elements(&element, combinator)
            .find(|anchor| matching::matches_selector(selector, offset, None, anchor, context))
        else {
            return;
        };
        element = anchor;
    }
}

/// Reports what a selector relies on for each element it could match against, for when there's
/// no match to take anchors from, such as within `:not()`.
fn candidate_dependencies<'input, 'arena>(
    selector: &selectors::parser::Selector<SelectorImpl>,
    element: &SelectElement<'input, 'arena>,
    dependency: &mut dyn FnMut(&Element<'input, 'arena>, Dependency),
) {
    let mut iter = selector.iter();
    let mut candidates = vec![element.clone()];
    loop {
        let compound: Vec<_> = iter.by_ref().collect();
        let combinator = iter.next_sequence();
        for candidate in &candidates {
            let flags = compound_dependencies(compound.iter().copied(), candidate, dependency)
                | combinator.map_or(Dependency::empty(), |c| combinator_dependency(c, false));
            if !flags.is_empty() {
                dependency(&candidate.element, flags);
            }
        }

        let Some(combinator) = combinator else {
            return;
        };
        let mut seen = HashSet::new();
        candidates = candidates
            .iter()
            .flat_map(|candidate| related_elements(candidate, combinator))
            .filter(|candidate| seen.insert(candidate.element.id()))
            .collect();
    }
}

fn compound_dependencies<'a, 'input, 'arena>(
    compound: impl IntoIterator<Item = &'a Component<SelectorImpl>>,
    element: &SelectElement<'input, 'arena>,
    dependency: &mut dyn FnMut(&Element<'input, 'arena>, Dependency),
) -> Dependency {
    compound
        .into_iter()
        .fold(Dependency::empty(), |flags, component| {
            flags
                | match component {
                    Component::LocalName(_)
                    | Component::ExplicitUniversalType
                    | Component::ExplicitAnyNamespace
                    | Component::ExplicitNoNamespace
                    | Component::DefaultNamespace(_)
                    | Component::Namespace(..) => Dependency::empty(),
                    Component::ID(_)
                    | Component::Class(_)
                    | Component::AttributeInNoNamespaceExists { .. }
                    | Component::AttributeInNoNamespace { .. }
                    | Component::AttributeOther(_)
                    | Component::NonTSPseudoClass(_) => Dependency::ATTRIBUTES,
                    Component::Root | Component::Scope | Component::ImplicitScope => {
                        Dependency::PARENT
                    }
                    Component::Empty => Dependency::CHILDREN,
                    Component::Nth(nth) => nth_dependency(nth),
                    Component::Negation(list) | Component::Is(list) | Component::Where(list) => {
                        for selector in list.slice() {
                            candidate_dependencies(selector, element, dependency);
                        }
                        Dependency::empty()
                    }
                    _ => Dependency::all(),
                }
        })
}

fn nth_dependency(nth: &NthSelectorData) -> Dependency {
    match nth.ty {
        NthType::Child => Dependency::INDEX,
        NthType::LastChild => Dependency::INDEX_FROM_END,
        NthType::OnlyChild => Dependency::INDEX | Dependency::INDEX_FROM_END,
        NthType::OfType => Dependency::INDEX_OF_TYPE,
        NthType::LastOfType => Dependency::INDEX_OF_TYPE_FROM_END,
        NthType::OnlyOfType => Dependency::INDEX_OF_TYPE | Dependency::INDEX_OF_TYPE_FROM_END,
    }
}

/// Returns what the element to the right of `combinator` relies on, where `is_anchored` is
/// whether the element to its left is reported as the one it relates to.
fn combinator_dependency(combinator: Combinator, is_anchored: bool) -> Dependency {
    match combinator {
        Combinator::Child => Dependency::PARENT,
        Combinator::Descendant => Dependency::ANCESTOR,
        Combinator::NextSibling | Combinator::LaterSibling if is_anchored => {
            Dependency::PREVIOUS_SIBLING
        }
        Combinator::NextSibling | Combinator::LaterSibling => Dependency::PREVIOUS_SIBLINGS,
        Combinator::PseudoElement | Combinator::SlotAssignment | Combinator::Part => {
            Dependency::all()
        }
    }
}

/// Returns the elements that the compound to the left of `combinator` may match, nearest first.
fn related_elements<'input, 'arena>(
    element: &SelectElement<'input, 'arena>,
    combinator: Combinator,
) -> impl Iterator<Item = SelectElement<'input, 'arena>> {
    type Step<'input, 'arena> =
        fn(&SelectElement<'input, 'arena>) -> Option<SelectElement<'input, 'arena>>;
    let parent: Step = <SelectElement as selectors::Element>::parent_element;
    let previous: Step = <SelectElement as selectors::Element>::prev_sibling_element;
    let (step, is_repeated): (Step, bool) = match combinator {
        Combinator::Child => (parent, false),
        Combinator::Descendant => (parent, true),
        Combinator::NextSibling => (previous, false),
        Combinator::LaterSibling => (previous, true),
        Combinator::PseudoElement | Combinator::SlotAssignment | Combinator::Part => {
            (|_| None, false)
        }
    };
    std::iter::successors(step(element), move |element| {
        if is_repeated {
            step(element)
        } else {
            None
        }
    })
}

impl<'i> selectors::parser::Parser<'i> for Parser {
    type Impl = SelectorImpl;
    type Error = SelectorParseErrorKind<'i>;
}

#[derive(Clone)]
/// A wrapper for [`element::Element`] implementing [`selectors::Element`]
pub struct SelectElement<'input, 'arena> {
    element: Element<'input, 'arena>,
}

impl<'input, 'arena> SelectElement<'input, 'arena> {
    /// Creates a selectable element using the given element
    pub fn new(element: Element<'input, 'arena>) -> Self {
        Self { element }
    }
}

impl std::fmt::Debug for SelectElement<'_, '_> {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        if !is_element!(self.element) {
            std::fmt::Debug::fmt(&self.element.node_type(), f)?;
            return Ok(());
        }
        f.debug_struct("SelectElement")
            .field("name", self.element.qual_name())
            .field("attr length", &self.element.attributes().len())
            .finish()
    }
}

impl selectors::Element for SelectElement<'_, '_> {
    type Impl = SelectorImpl;

    fn opaque(&self) -> selectors::OpaqueElement {
        selectors::OpaqueElement::new(self)
    }

    fn parent_element(&self) -> Option<Self> {
        self.element.parent_element().map(Self::new)
    }

    fn parent_node_is_shadow_root(&self) -> bool {
        false
    }

    fn containing_shadow_host(&self) -> Option<Self> {
        None
    }

    fn is_pseudo_element(&self) -> bool {
        false
    }

    fn prev_sibling_element(&self) -> Option<Self> {
        self.element.previous_element_sibling().map(Self::new)
    }

    fn next_sibling_element(&self) -> Option<Self> {
        self.element.next_element_sibling().map(Self::new)
    }

    fn first_element_child(&self) -> Option<Self> {
        self.element.first_element_child().map(Self::new)
    }

    fn is_html_element_in_html_document(&self) -> bool {
        true
    }

    fn has_local_name(
        &self,
        local_name: &<Self::Impl as selectors::SelectorImpl>::BorrowedLocalName,
    ) -> bool {
        if self.element.node_type() == node::Type::Document {
            false
        } else {
            *self.element.local_name() == local_name.0
        }
    }

    fn has_namespace(
        &self,
        ns: &<Self::Impl as selectors::SelectorImpl>::BorrowedNamespaceUrl,
    ) -> bool {
        *self.element.prefix().ns().uri() == ns.0
    }

    fn is_same_type(&self, other: &Self) -> bool {
        let name = self.element.qual_name();
        let other_name = other.element.qual_name();

        name.local_name() == other.element.local_name() && name.prefix() == other_name.prefix()
    }

    fn attr_matches(
        &self,
        ns: &selectors::attr::NamespaceConstraint<
            &<Self::Impl as selectors::SelectorImpl>::NamespaceUrl,
        >,
        local_name: &<Self::Impl as selectors::SelectorImpl>::LocalName,
        operation: &selectors::attr::AttrSelectorOperation<
            &<Self::Impl as selectors::SelectorImpl>::AttrValue,
        >,
    ) -> bool {
        use selectors::attr::NamespaceConstraint;

        let value = match ns {
            NamespaceConstraint::Any => self.element.get_attribute_local(&local_name.0),
            NamespaceConstraint::Specific(ns) if ns.0.is_empty() => {
                self.element.get_attribute_local(&local_name.0)
            }
            NamespaceConstraint::Specific(ns) => self
                .element
                .get_attribute_ns(&name::NS::new(ns.0.clone()), &local_name.0),
        };
        let Some(value) = value else {
            return false;
        };
        let Ok(value) = value.to_value_string(PrinterOptions::default()) else {
            return false;
        };
        operation.eval_str(&value)
    }

    fn match_non_ts_pseudo_class(
        &self,
        pc: &<Self::Impl as selectors::SelectorImpl>::NonTSPseudoClass,
        _context: &mut matching::MatchingContext<Self::Impl>,
    ) -> bool {
        match pc {
            PseudoClass::Link(..) | PseudoClass::AnyLink(..) => self.is_link(),
        }
    }

    fn match_pseudo_element(
        &self,
        _pe: &<Self::Impl as selectors::SelectorImpl>::PseudoElement,
        _context: &mut matching::MatchingContext<Self::Impl>,
    ) -> bool {
        false
    }

    fn apply_selector_flags(&self, flags: matching::ElementSelectorFlags) {
        let self_flags = flags.for_self();
        self.element.set_selector_flags(self_flags);

        let Some(parent) = self.element.parent_element() else {
            return;
        };
        let parent_flags = flags.for_parent();
        parent.set_selector_flags(parent_flags);
    }

    fn is_link(&self) -> bool {
        if self.element.node_type() == node::Type::Document {
            return false;
        }
        (match self.element.qual_name() {
            ElementId::A => true,
            ElementId::Unknown(QualName { local, .. }) => matches!(local.as_str(), "area" | "link"),
            _ => false,
        }) && self.element.has_attribute(&AttrId::Href)
    }

    fn is_html_slot_element(&self) -> bool {
        false
    }

    fn has_id(
        &self,
        id: &<Self::Impl as selectors::SelectorImpl>::Identifier,
        case_sensitivity: selectors::attr::CaseSensitivity,
    ) -> bool {
        if self.element.node_type() == node::Type::Document {
            return false;
        }
        let Some(self_id) = get_attribute!(self.element, Id) else {
            return false;
        };
        case_sensitivity.eq(id.0.as_bytes(), self_id.as_bytes())
    }

    fn has_class(
        &self,
        name: &<Self::Impl as selectors::SelectorImpl>::Identifier,
        case_sensitivity: selectors::attr::CaseSensitivity,
    ) -> bool {
        if self.element.node_type() == node::Type::Document {
            return false;
        }

        let Some(attr) = get_attribute!(self.element, Class) else {
            return false;
        };
        attr.iter().any(|c| case_sensitivity.eq(name, c.as_bytes()))
    }

    fn imported_part(
        &self,
        _name: &<Self::Impl as selectors::SelectorImpl>::Identifier,
    ) -> Option<<Self::Impl as selectors::SelectorImpl>::Identifier> {
        None
    }

    fn is_part(&self, _name: &<Self::Impl as selectors::SelectorImpl>::Identifier) -> bool {
        false
    }

    fn is_empty(&self) -> bool {
        !self.element.has_child_nodes()
            || self.element.child_nodes_iter().all(|child| {
                child.node_type() == node::Type::Text
                    && child
                        .text_content()
                        .is_none_or(|string| string.trim().is_empty())
            })
    }

    fn is_root(&self) -> bool {
        self.element.is_root()
    }

    fn has_custom_state(
        &self,
        _name: &<Self::Impl as selectors::SelectorImpl>::Identifier,
    ) -> bool {
        false
    }

    #[allow(clippy::cast_possible_truncation)]
    fn add_element_unique_hashes(&self, filter: &mut selectors::bloom::BloomFilter) -> bool {
        let mut f = |hash: u32| filter.insert_hash(hash & selectors::bloom::BLOOM_HASH_MASK);

        let local_name_hash = &mut DefaultHasher::default();
        self.element.local_name().hash(local_name_hash);
        f(local_name_hash.finish() as u32);

        let prefix_hash = &mut DefaultHasher::default();
        self.element.prefix().hash(prefix_hash);
        f(prefix_hash.finish() as u32);

        if let Some(id) = self.element.get_attribute(&AttrId::Id) {
            if let Attr::Id(id) = &*id {
                let id_hash = &mut DefaultHasher::default();
                id.hash(id_hash);
                f(prefix_hash.finish() as u32);
            }
        }

        self.element.class_list().for_each(|class| {
            let class_hash = &mut DefaultHasher::default();
            class.hash(class_hash);
            f(class_hash.finish() as u32);
        });

        for attr in self.element.attributes() {
            let name = attr.name();
            if is_attribute!(name, Class | Id | Style) {
                continue;
            }

            let name_hash = &mut DefaultHasher::default();
            name.hash(name_hash);
            f(name_hash.finish() as u32);
        }
        true
    }
}
