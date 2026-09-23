use std::{cell::RefCell, collections::HashMap};

use lightningcss::{
    printer::PrinterOptions,
    rules::{CssRule, CssRuleList},
    selector::{Component, Selector as CssSelector},
    traits::ToCss as _,
};
use oxvg_ast::{
    element::Element,
    node::AllocationID,
    selectors::{Dependency, Selector},
};
use parcel_selectors::parser::Combinator;

#[derive(Debug, Default)]
/// What each element implicated by a structure-sensitive selector is relied on for, such as the
/// target and anchors of a selector with combinators or pseudo-classes like `:first-child`.
///
/// Only selectors that match are considered, and only the elements that make up the match are
/// implicated. These should be gathered before rewriting the document, as rewrites may erase the
/// structure a selector relies on.
pub struct StructuralDependencies {
    dependencies: HashMap<AllocationID, Dependency>,
    /// Whether a selector couldn't be analysed, so any element may be implicated
    is_unknown: bool,
}

impl StructuralDependencies {
    pub fn new<'input>(
        root: &Element<'input, '_>,
        stylesheets: &[RefCell<CssRuleList<'input>>],
    ) -> Self {
        let mut result = Self::default();
        for stylesheet in stylesheets {
            result.add_rules(root, &stylesheet.borrow().0);
        }
        result
    }

    fn add_rules<'input>(&mut self, root: &Element<'input, '_>, rules: &[CssRule<'input>]) {
        for rule in rules {
            match rule {
                CssRule::Style(style) => {
                    for selector in &style.selectors.0 {
                        self.add_selector(root, selector);
                    }
                    // Nested rules are relative to the selectors of their parent
                    if !style.rules.0.is_empty() {
                        self.is_unknown = true;
                    }
                }
                CssRule::Media(rule) => self.add_rules(root, &rule.rules.0),
                CssRule::Supports(rule) => self.add_rules(root, &rule.rules.0),
                CssRule::Container(rule) => self.add_rules(root, &rule.rules.0),
                CssRule::LayerBlock(rule) => self.add_rules(root, &rule.rules.0),
                CssRule::StartingStyle(rule) => self.add_rules(root, &rule.rules.0),
                CssRule::Scope(_) | CssRule::Nesting(_) => self.is_unknown = true,
                _ => {}
            }
        }
    }

    fn add_selector<'input>(&mut self, root: &Element<'input, '_>, selector: &CssSelector<'input>) {
        if self.is_unknown || is_element_local(selector) {
            return;
        }
        let Some(selector) = parse_selector(selector) else {
            log::debug!("unknown structural dependencies: cannot match {selector:?}");
            self.is_unknown = true;
            return;
        };

        let mut dependencies = Vec::new();
        for element in root.breadth_first() {
            if !selector.dependencies(&element, |element, dependency| {
                dependencies.push((element.id(), dependency));
            }) {
                continue;
            }
            // Whether a match relies on structure depends on the selector, not the element
            if !dependencies
                .iter()
                .any(|(_, dependency)| dependency.is_structural())
            {
                return;
            }
            for (id, dependency) in dependencies.drain(..) {
                *self.dependencies.entry(id).or_default() |= dependency;
            }
        }
    }

    fn get(&self, element: &Element) -> Dependency {
        self.dependencies
            .get(&element.id())
            .copied()
            .unwrap_or_default()
    }

    /// Returns whether changing the attributes of the element may change what a
    /// structure-sensitive selector matches.
    pub fn prevents_attribute_change(&self, element: &Element) -> bool {
        self.is_unknown || self.get(element).intersects(Dependency::ATTRIBUTES)
    }

    /// Returns whether replacing the element with its children may change what a
    /// structure-sensitive selector matches.
    pub fn prevents_flatten(&self, element: &Element) -> bool {
        if self.is_unknown {
            return true;
        }
        if self.dependencies.is_empty() {
            return false;
        }
        if !self.get(element).is_empty() {
            return true;
        }
        let Some(parent) = element.parent_element() else {
            return true;
        };
        if self.get(&parent).intersects(Dependency::CHILDREN) {
            return true;
        }
        let mut ancestor = Some(parent.clone());
        while let Some(current) = ancestor {
            let parent = current.parent_element();
            if self.get(&current).intersects(Dependency::DESCENDANTS)
                || parent.as_ref().is_some_and(|parent| {
                    parent
                        .children_iter()
                        .take_while(|sibling| *sibling != current)
                        .any(|sibling| self.get(&sibling).intersects(Dependency::NEXT_SIBLINGS))
                })
            {
                return true;
            }
            ancestor = parent;
        }

        let siblings = parent.children();
        let Some(index) = siblings.iter().position(|sibling| sibling == element) else {
            return true;
        };
        let preceding = &siblings[..index];
        let following = &siblings[index + 1..];
        let children = element.children();

        let is_child_moved = |child: &Element| {
            let dependency = self.get(child);
            dependency.intersects(Dependency::PARENT)
                || (!preceding.is_empty()
                    && dependency.intersects(Dependency::PREVIOUS_SIBLINGS | Dependency::INDEX))
                || (!following.is_empty()
                    && dependency.intersects(Dependency::NEXT_SIBLINGS | Dependency::INDEX_FROM_END))
                || (dependency.contains(Dependency::INDEX_OF_TYPE)
                    && preceding.iter().any(|sibling| is_same_type(sibling, child)))
                || (dependency.contains(Dependency::INDEX_OF_TYPE_FROM_END)
                    && following.iter().any(|sibling| is_same_type(sibling, child)))
        };
        let is_count_changed = children.len() != 1;
        let is_count_of_type_changed = |sibling: &Element| {
            children
                .iter()
                .filter(|child| is_same_type(child, sibling))
                .count()
                != usize::from(is_same_type(element, sibling))
        };
        let is_preceding_sibling_shifted = |sibling: &Element| {
            let dependency = self.get(sibling);
            dependency.intersects(Dependency::NEXT_SIBLINGS)
                || (is_count_changed && dependency.intersects(Dependency::INDEX_FROM_END))
                || (dependency.contains(Dependency::INDEX_OF_TYPE_FROM_END)
                    && is_count_of_type_changed(sibling))
        };
        let is_following_sibling_shifted = |sibling: &Element| {
            let dependency = self.get(sibling);
            dependency.intersects(Dependency::PREVIOUS_SIBLINGS)
                || (is_count_changed && dependency.intersects(Dependency::INDEX))
                || (dependency.contains(Dependency::INDEX_OF_TYPE)
                    && is_count_of_type_changed(sibling))
        };

        children.iter().any(is_child_moved)
            || preceding.iter().any(is_preceding_sibling_shifted)
            || following.iter().any(is_following_sibling_shifted)
    }
}

fn is_same_type(a: &Element, b: &Element) -> bool {
    a.local_name() == b.local_name() && a.prefix() == b.prefix()
}

/// Returns whether the selector only matches against an element's own name, attributes, and
/// state, which never relies on the document's structure.
fn is_element_local(selector: &CssSelector) -> bool {
    selector.iter_raw_match_order().all(|component| {
        matches!(
            component,
            Component::LocalName(_)
                | Component::ID(_)
                | Component::Class(_)
                | Component::AttributeInNoNamespaceExists { .. }
                | Component::AttributeInNoNamespace { .. }
                | Component::AttributeOther(_)
                | Component::ExplicitUniversalType
                | Component::ExplicitAnyNamespace
                | Component::ExplicitNoNamespace
                | Component::DefaultNamespace(_)
                | Component::Namespace(..)
                | Component::NonTSPseudoClass(_)
                | Component::PseudoElement(_)
                | Component::Combinator(Combinator::PseudoElement)
        )
    })
}

/// Converts a stylesheet's selector to one that can be matched against the document.
///
/// Dynamic pseudo-classes (e.g. `:hover`) and pseudo-elements don't depend on the document's
/// structure, so they're dropped when unsupported to match any element they may apply to.
fn parse_selector(selector: &CssSelector) -> Option<Selector> {
    let css = selector.to_css_string(PrinterOptions::default()).ok()?;
    if let Ok(selector) = Selector::new(&css) {
        return Some(selector);
    }

    let mut components = Vec::new();
    let mut is_compound_empty = true;
    for component in selector.iter_raw_parse_order_from(0) {
        match component {
            Component::NonTSPseudoClass(_)
            | Component::PseudoElement(_)
            | Component::Combinator(Combinator::PseudoElement) => continue,
            Component::Combinator(_) => {
                if is_compound_empty {
                    components.push(Component::ExplicitUniversalType);
                }
                is_compound_empty = true;
            }
            _ => is_compound_empty = false,
        }
        components.push(component.clone());
    }
    if is_compound_empty {
        components.push(Component::ExplicitUniversalType);
    }
    let css = CssSelector::from(components)
        .to_css_string(PrinterOptions::default())
        .ok()?;
    Selector::new(&css).ok()
}
