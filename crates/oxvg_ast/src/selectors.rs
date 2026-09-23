//! Types used for selecting elements with css selectors.
use std::{
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
        Combinator, Component, NthSelectorData, ParseRelative, RelativeSelector,
        RelativeSelectorMatchHint, SelectorParseErrorKind,
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
        // Identity is the arena node, so anchors stay valid for the whole match.
        selectors::OpaqueElement::new(self.element.0)
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

type ComplexSelector = selectors::parser::Selector<SelectorImpl>;

#[derive(Clone, Copy)]
struct NthLock {
    from_start: bool,
    from_end: bool,
    of_type: bool,
}

/// Marks elements whose structure a stylesheet selector depends on.
///
/// Flags are derived from selectors that fully match the current tree. A later
/// rewrite can then refuse to flatten, move, or rename only those elements.
pub fn protect_structural_selectors<'input, 'arena>(root: &Element<'input, 'arena>) {
    for element in std::iter::once(root.clone()).chain(root.breadth_first()) {
        element.clear_structural_guard();
    }
    let sheets: Vec<_> = crate::style::root(root).collect();
    if sheets.is_empty() {
        return;
    }
    let mut selectors = Vec::new();
    for sheet in &sheets {
        collect_selectors(&sheet.borrow(), &mut selectors);
    }
    let elements: Vec<_> = std::iter::once(root.clone())
        .chain(root.breadth_first())
        .filter(|element| element.node_type() == node::Type::Element)
        .collect();
    for css in selectors {
        let Ok(parsed) = Selector::new(&css) else {
            continue;
        };
        for complex in parsed.0.slice() {
            if !selector_is_structural(complex) {
                continue;
            }
            for element in &elements {
                if element_matches(complex, element) {
                    mark_chain(complex, element, None, 0);
                }
            }
        }
    }
}

fn collect_selectors(rules: &lightningcss::rules::CssRuleList<'_>, out: &mut Vec<String>) {
    use lightningcss::rules::CssRule;
    for rule in &rules.0 {
        match rule {
            CssRule::Style(style) => {
                push_selector_list(&style.selectors, out);
                collect_selectors(&style.rules, out);
            }
            CssRule::Nesting(nesting) => {
                push_selector_list(&nesting.style.selectors, out);
                collect_selectors(&nesting.style.rules, out);
            }
            CssRule::Media(media) => collect_selectors(&media.rules, out),
            CssRule::Supports(supports) => collect_selectors(&supports.rules, out),
            CssRule::Container(container) => collect_selectors(&container.rules, out),
            CssRule::LayerBlock(layer) => collect_selectors(&layer.rules, out),
            CssRule::StartingStyle(starting) => collect_selectors(&starting.rules, out),
            CssRule::MozDocument(document) => collect_selectors(&document.rules, out),
            CssRule::Scope(scope) => collect_selectors(&scope.rules, out),
            _ => {}
        }
    }
}

fn push_selector_list(list: &lightningcss::selector::SelectorList<'_>, out: &mut Vec<String>) {
    use lightningcss::traits::ToCss;
    for selector in &list.0 {
        if let Ok(css) = selector.to_css_string(PrinterOptions::default()) {
            if !css.is_empty() {
                out.push(css);
            }
        }
    }
}

fn element_matches(selector: &ComplexSelector, element: &Element) -> bool {
    matches_from(selector, 0, element)
}

fn matches_from(selector: &ComplexSelector, offset: usize, element: &Element) -> bool {
    let mut caches = SelectorCaches::default();
    let mut context = matching::MatchingContext::new(
        matching::MatchingMode::Normal,
        None,
        &mut caches,
        matching::QuirksMode::NoQuirks,
        matching::NeedsSelectorFlags::No,
        matching::MatchingForInvalidation::No,
    );
    matching::matches_selector(
        selector,
        offset,
        None,
        &SelectElement::new(element.clone()),
        &mut context,
    )
}

fn selector_is_structural(selector: &ComplexSelector) -> bool {
    selector.iter_raw_match_order().any(component_is_structural)
}

/// Whether `css` is a selector whose match depends on document structure.
///
/// A plain `rect` is not structural. `g > rect`, `:nth-child`, and `:has` are.
pub fn is_structural_selector(css: &str) -> bool {
    let Ok(parsed) = Selector::new(css) else {
        return false;
    };
    parsed.0.slice().iter().any(selector_is_structural)
}

/// Elements that currently match each structure-sensitive selector, in tree order.
///
/// An empty result means the document has no structure-sensitive selector.
pub fn structural_match_fingerprint<'input, 'arena>(
    root: &Element<'input, 'arena>,
) -> Vec<Vec<usize>> {
    let selectors = structural_selectors(root);
    if selectors.is_empty() {
        return Vec::new();
    }
    let elements = document_elements(root);
    selectors
        .iter()
        .map(|selector| {
            elements
                .iter()
                .filter(|element| element_matches(selector, element))
                .map(|element| element.0.id())
                .collect()
        })
        .collect()
}

fn structural_selectors<'input, 'arena>(root: &Element<'input, 'arena>) -> Vec<ComplexSelector> {
    let sheets: Vec<_> = crate::style::root(root).collect();
    let mut css_selectors = Vec::new();
    for sheet in &sheets {
        collect_selectors(&sheet.borrow(), &mut css_selectors);
    }
    let mut structural = Vec::new();
    for css in css_selectors {
        let Ok(parsed) = Selector::new(&css) else {
            continue;
        };
        for complex in parsed.0.slice() {
            if selector_is_structural(complex) {
                structural.push(complex.clone());
            }
        }
    }
    structural
}

fn document_elements<'input, 'arena>(
    root: &Element<'input, 'arena>,
) -> Vec<Element<'input, 'arena>> {
    std::iter::once(root.clone())
        .chain(root.breadth_first())
        .filter(|element| element.node_type() == node::Type::Element)
        .collect()
}

struct DomNodeSnap<'input, 'arena> {
    node: node::Ref<'input, 'arena>,
    parent: Option<node::Ref<'input, 'arena>>,
    next_sibling: Option<node::Ref<'input, 'arena>>,
    previous_sibling: Option<node::Ref<'input, 'arena>>,
    first_child: Option<node::Ref<'input, 'arena>>,
    last_child: Option<node::Ref<'input, 'arena>>,
    attrs: Option<Vec<Attr<'input>>>,
}

/// Links and attributes for every node under `root`, so a simulated rewrite can be undone.
pub struct DomSnapshot<'input, 'arena> {
    nodes: Vec<DomNodeSnap<'input, 'arena>>,
}

/// Captures the tree under `root`.
pub fn snapshot_dom<'input, 'arena>(root: &Element<'input, 'arena>) -> DomSnapshot<'input, 'arena> {
    let mut nodes = Vec::new();
    collect_nodes(root.0, &mut nodes);
    DomSnapshot {
        nodes: nodes
            .into_iter()
            .map(|node| DomNodeSnap {
                parent: node.parent.get(),
                next_sibling: node.next_sibling.get(),
                previous_sibling: node.previous_sibling.get(),
                first_child: node.first_child.get(),
                last_child: node.last_child.get(),
                attrs: (node.node_type() == node::Type::Element).then(|| {
                    element::Element::new(node)
                        .unwrap()
                        .attributes()
                        .as_slice()
                        .to_vec()
                }),
                node,
            })
            .collect(),
    }
}

/// Restores a tree captured by [`snapshot_dom`].
pub fn restore_dom<'input, 'arena>(snapshot: &DomSnapshot<'input, 'arena>) {
    for snap in &snapshot.nodes {
        snap.node.parent.set(snap.parent);
        snap.node.next_sibling.set(snap.next_sibling);
        snap.node.previous_sibling.set(snap.previous_sibling);
        snap.node.first_child.set(snap.first_child);
        snap.node.last_child.set(snap.last_child);
        if let Some(attrs) = &snap.attrs {
            if snap.node.node_type() == node::Type::Element {
                if let Some(element) = Element::new(snap.node) {
                    element.attributes().0.replace(attrs.clone());
                }
            }
        }
    }
}

fn collect_nodes<'input, 'arena>(
    node: node::Ref<'input, 'arena>,
    out: &mut Vec<node::Ref<'input, 'arena>>,
) {
    out.push(node);
    for child in node.child_nodes_iter() {
        collect_nodes(child, out);
    }
}

fn component_is_structural(component: &Component<SelectorImpl>) -> bool {
    match component {
        Component::Nth(_) | Component::NthOf(_) | Component::Empty | Component::Has(_) => true,
        Component::Combinator(
            Combinator::Child
            | Combinator::Descendant
            | Combinator::NextSibling
            | Combinator::LaterSibling,
        ) => true,
        Component::Is(list) | Component::Where(list) | Component::Negation(list) => {
            list.slice().iter().any(selector_is_structural)
        }
        _ => false,
    }
}

fn mark_chain<'input, 'arena>(
    selector: &ComplexSelector,
    element: &Element<'input, 'arena>,
    known_anchor: Option<&Element<'input, 'arena>>,
    depth: u8,
) {
    if depth > 24 {
        return;
    }
    let mut iter = selector.iter();
    let mut current = element.clone();
    let mut consumed = 0usize;
    loop {
        current.add_structural_guard(element::STRUCTURAL_KEEP);
        let mut rename = false;
        let mut nth: Option<NthLock> = None;
        let mut empty = false;
        let mut negated_nonempty = false;
        let mut pending_has = Vec::new();
        let mut pending_is = Vec::new();
        while let Some(component) = iter.next() {
            consumed += 1;
            classify_component(
                component,
                &current,
                &mut rename,
                &mut nth,
                &mut empty,
                &mut negated_nonempty,
                &mut pending_has,
                &mut pending_is,
            );
        }
        if rename {
            current.add_structural_guard(element::STRUCTURAL_NO_RENAME);
        }
        if let Some(lock) = nth {
            lock_positional(&current, lock);
        }
        if empty {
            current.add_structural_guard(element::STRUCTURAL_NO_APPEND);
        }
        if negated_nonempty {
            if let Some(child) = current.first_element_child() {
                child.add_structural_guard(element::STRUCTURAL_KEEP_SLOT);
            }
        }
        for relative in &pending_has {
            mark_has(&current, relative, depth);
        }
        for nested in &pending_is {
            mark_chain(nested, &current, None, depth + 1);
        }
        let Some(combinator) = iter.next_sequence() else {
            break;
        };
        consumed += 1;
        let Some(next) = resolve_anchor(&current, combinator, selector, consumed, known_anchor)
        else {
            break;
        };
        current = next;
    }
}

fn classify_component(
    component: &Component<SelectorImpl>,
    element: &Element,
    rename: &mut bool,
    nth: &mut Option<NthLock>,
    empty: &mut bool,
    negated_nonempty: &mut bool,
    pending_has: &mut Vec<RelativeSelector<SelectorImpl>>,
    pending_is: &mut Vec<ComplexSelector>,
) {
    match component {
        Component::LocalName(_) => *rename = true,
        Component::Nth(data) => merge_nth(nth, lock_from_nth(data)),
        Component::NthOf(data) => {
            let mut lock = lock_from_nth(data.nth_data());
            if !data.selectors().is_empty() {
                // `:nth-child(n of S)` counts every sibling matching S, not a tag name.
                lock.of_type = false;
            }
            merge_nth(nth, lock);
        }
        Component::Empty => *empty = true,
        Component::Has(relatives) => pending_has.extend(relatives.iter().cloned()),
        Component::Is(list) | Component::Where(list) => {
            let mut structural = Vec::new();
            let mut plain = false;
            for selector in list.slice() {
                if !element_matches(selector, element) {
                    continue;
                }
                if selector_is_structural(selector) {
                    structural.push(selector.clone());
                } else {
                    plain = true;
                }
            }
            // A non-structural branch already matches, so the structural one is not load-bearing.
            if !plain {
                pending_is.append(&mut structural);
            }
        }
        Component::Negation(list) => {
            for selector in list.slice() {
                if selector_is_structural(selector) {
                    lock_negated(selector, element, negated_nonempty);
                }
            }
        }
        _ => {}
    }
}

fn merge_nth(slot: &mut Option<NthLock>, extra: NthLock) {
    match slot {
        Some(current) => {
            current.from_start |= extra.from_start;
            current.from_end |= extra.from_end;
            current.of_type |= extra.of_type;
        }
        None => *slot = Some(extra),
    }
}

fn lock_from_nth(data: &NthSelectorData) -> NthLock {
    let only = data.ty.is_only();
    NthLock {
        from_start: only || !data.ty.is_from_end(),
        from_end: only || data.ty.is_from_end(),
        of_type: data.ty.is_of_type(),
    }
}

fn lock_negated(selector: &ComplexSelector, element: &Element, negated_nonempty: &mut bool) {
    let mut iter = selector.iter();
    let mut nth = None;
    let mut saw_empty = false;
    let mut consumed = 0usize;
    for component in iter.by_ref() {
        consumed += 1;
        match component {
            Component::Nth(data) => merge_nth(&mut nth, lock_from_nth(data)),
            Component::NthOf(data) => {
                let mut lock = lock_from_nth(data.nth_data());
                if !data.selectors().is_empty() {
                    lock.of_type = false;
                }
                merge_nth(&mut nth, lock);
            }
            Component::Empty => saw_empty = true,
            _ => {}
        }
    }
    if let Some(combinator) = iter.next_sequence() {
        consumed += 1;
        if !left_side_matches(element, combinator, selector, consumed) {
            return;
        }
    }
    if let Some(lock) = nth {
        lock_positional(element, lock);
    }
    if saw_empty {
        *negated_nonempty = true;
    }
}

fn left_side_matches(
    element: &Element,
    combinator: Combinator,
    selector: &ComplexSelector,
    offset: usize,
) -> bool {
    match combinator {
        Combinator::Child | Combinator::Descendant => {
            let mut parent = element.parent_element();
            while let Some(candidate) = parent {
                if matches_from(selector, offset, &candidate) {
                    return true;
                }
                if combinator == Combinator::Child {
                    return false;
                }
                parent = candidate.parent_element();
            }
            false
        }
        Combinator::NextSibling => element
            .previous_element_sibling()
            .is_some_and(|sibling| matches_from(selector, offset, &sibling)),
        Combinator::LaterSibling => {
            let mut sibling = element.previous_element_sibling();
            while let Some(candidate) = sibling {
                if matches_from(selector, offset, &candidate) {
                    return true;
                }
                sibling = candidate.previous_element_sibling();
            }
            false
        }
        _ => false,
    }
}

fn lock_positional(element: &Element, lock: NthLock) {
    let Some(parent) = element.parent_element() else {
        return;
    };
    parent.add_structural_guard(element::STRUCTURAL_NO_FLATTEN);
    let siblings: Vec<_> = parent.children_iter().collect();
    let Some(pos) = siblings.iter().position(|sibling| sibling.id_eq(element)) else {
        return;
    };
    for (index, sibling) in siblings.iter().enumerate() {
        if index == pos {
            continue;
        }
        let before = index < pos;
        let after = index > pos;
        if before && !lock.from_start {
            continue;
        }
        if after && !lock.from_end {
            continue;
        }
        if lock.of_type {
            if same_local(sibling, element) {
                sibling.add_structural_guard(
                    element::STRUCTURAL_KEEP
                        | element::STRUCTURAL_NO_FLATTEN
                        | element::STRUCTURAL_NO_RENAME,
                );
            } else if contains_local(sibling, element.local_name()) {
                sibling.add_structural_guard(element::STRUCTURAL_NO_FLATTEN);
            }
            sibling.add_structural_guard(element::STRUCTURAL_NO_INSERT_BEFORE);
        } else {
            sibling.add_structural_guard(
                element::STRUCTURAL_SIBLING_SLOT | element::STRUCTURAL_NO_INSERT_BEFORE,
            );
        }
    }
    if lock.of_type {
        element.add_structural_guard(element::STRUCTURAL_NO_RENAME);
    }
    if lock.from_start || lock.from_end {
        element.add_structural_guard(element::STRUCTURAL_NO_INSERT_BEFORE);
    }
    if lock.from_end {
        parent.add_structural_guard(element::STRUCTURAL_NO_APPEND);
    }
}

fn same_local(left: &Element, right: &Element) -> bool {
    left.local_name() == right.local_name()
}

fn contains_local(element: &Element, local: &Atom<'_>) -> bool {
    element
        .breadth_first()
        .any(|descendant| descendant.local_name() == local)
}

fn resolve_anchor<'input, 'arena>(
    element: &Element<'input, 'arena>,
    combinator: Combinator,
    selector: &ComplexSelector,
    offset: usize,
    known_anchor: Option<&Element<'input, 'arena>>,
) -> Option<Element<'input, 'arena>> {
    if next_is_relative_anchor(selector, offset) {
        let anchor = known_anchor?;
        let linked = match combinator {
            Combinator::Child => element
                .parent_element()
                .is_some_and(|parent| parent.id_eq(anchor)),
            Combinator::Descendant => is_ancestor(anchor, element.clone()),
            Combinator::NextSibling => element
                .previous_element_sibling()
                .is_some_and(|sibling| sibling.id_eq(anchor)),
            Combinator::LaterSibling => {
                previous_siblings(element).any(|sibling| sibling.id_eq(anchor))
            }
            _ => false,
        };
        if !linked {
            return None;
        }
        if combinator == Combinator::NextSibling {
            element.add_structural_guard(element::STRUCTURAL_NO_INSERT_BEFORE);
        }
        anchor.add_structural_guard(element::STRUCTURAL_KEEP | element::STRUCTURAL_NO_FLATTEN);
        return None;
    }
    match combinator {
        Combinator::Child => {
            let parent = element.parent_element()?;
            if !matches_from(selector, offset, &parent) {
                return None;
            }
            parent.add_structural_guard(element::STRUCTURAL_NO_FLATTEN);
            Some(parent)
        }
        Combinator::Descendant => {
            // Keep the outermost ancestor that completes the relationship.
            // Intermediate ancestors that also match are not required, so they
            // stay eligible for collapse.
            let mut parent = element.parent_element()?;
            let mut matched = None;
            loop {
                if matches_from(selector, offset, &parent) {
                    matched = Some(parent.clone());
                }
                parent = match parent.parent_element() {
                    Some(next) => next,
                    None => break,
                };
            }
            let matched = matched?;
            matched.add_structural_guard(element::STRUCTURAL_NO_FLATTEN);
            Some(matched)
        }
        Combinator::NextSibling => {
            let previous = element.previous_element_sibling()?;
            if !matches_from(selector, offset, &previous) {
                return None;
            }
            element.add_structural_guard(element::STRUCTURAL_NO_INSERT_BEFORE);
            Some(previous)
        }
        Combinator::LaterSibling => {
            // The furthest previous sibling that completes `~` is enough.
            // A nearer match is only a piece of the same selector, not the
            // relationship the rule depends on.
            let mut sibling = element.previous_element_sibling()?;
            let mut matched = None;
            loop {
                if matches_from(selector, offset, &sibling) {
                    matched = Some(sibling.clone());
                }
                sibling = match sibling.previous_element_sibling() {
                    Some(next) => next,
                    None => break,
                };
            }
            matched
        }
        _ => None,
    }
}

fn next_is_relative_anchor(selector: &ComplexSelector, offset: usize) -> bool {
    matches!(
        selector.iter_from(offset).next(),
        Some(Component::RelativeSelectorAnchor)
    )
}

fn is_ancestor(ancestor: &Element, mut node: Element) -> bool {
    while let Some(parent) = node.parent_element() {
        if parent.id_eq(ancestor) {
            return true;
        }
        node = parent;
    }
    false
}

fn previous_siblings<'input, 'arena>(
    element: &Element<'input, 'arena>,
) -> impl Iterator<Item = Element<'input, 'arena>> {
    let mut sibling = element.previous_element_sibling();
    std::iter::from_fn(move || {
        let current = sibling.clone()?;
        sibling = current.previous_element_sibling();
        Some(current)
    })
}

fn mark_has(anchor: &Element, relative: &RelativeSelector<SelectorImpl>, depth: u8) {
    let anchor_element = SelectElement::new(anchor.clone());
    let anchor_opaque = selectors::Element::opaque(&anchor_element);
    for candidate in has_candidates(anchor, relative.match_hint) {
        if relative_matches(relative, anchor_opaque, &candidate) {
            mark_chain(&relative.selector, &candidate, Some(anchor), depth + 1);
            break;
        }
    }
}

fn relative_matches(
    relative: &RelativeSelector<SelectorImpl>,
    anchor: selectors::OpaqueElement,
    candidate: &Element,
) -> bool {
    let mut caches = SelectorCaches::default();
    let mut context = matching::MatchingContext::new(
        matching::MatchingMode::Normal,
        None,
        &mut caches,
        matching::QuirksMode::NoQuirks,
        matching::NeedsSelectorFlags::No,
        matching::MatchingForInvalidation::No,
    );
    context.nest_for_relative_selector(anchor, |context| {
        matching::matches_selector(
            &relative.selector,
            0,
            None,
            &SelectElement::new(candidate.clone()),
            context,
        )
    })
}

fn has_candidates<'input, 'arena>(
    anchor: &Element<'input, 'arena>,
    hint: RelativeSelectorMatchHint,
) -> Vec<Element<'input, 'arena>> {
    match hint {
        RelativeSelectorMatchHint::InChild => anchor.children_iter().collect(),
        RelativeSelectorMatchHint::InSubtree => anchor.breadth_first().collect(),
        RelativeSelectorMatchHint::InNextSibling => {
            anchor.next_element_sibling().into_iter().collect()
        }
        RelativeSelectorMatchHint::InNextSiblingSubtree => anchor
            .next_element_sibling()
            .map(|sibling| std::iter::once(sibling.clone()).chain(sibling.breadth_first()))
            .into_iter()
            .flatten()
            .collect(),
        RelativeSelectorMatchHint::InSibling => following_siblings(anchor).collect(),
        RelativeSelectorMatchHint::InSiblingSubtree => following_siblings(anchor)
            .flat_map(|sibling| std::iter::once(sibling.clone()).chain(sibling.breadth_first()))
            .collect(),
    }
}

fn following_siblings<'input, 'arena>(
    element: &Element<'input, 'arena>,
) -> impl Iterator<Item = Element<'input, 'arena>> {
    let mut sibling = element.next_element_sibling();
    std::iter::from_fn(move || {
        let current = sibling.clone()?;
        sibling = current.next_element_sibling();
        Some(current)
    })
}
