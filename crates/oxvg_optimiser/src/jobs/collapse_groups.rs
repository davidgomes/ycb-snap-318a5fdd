use std::mem;

use lightningcss::{properties::PropertyId, vendor_prefix::VendorPrefix};
use oxvg_ast::{
    element::Element,
    get_attribute, has_attribute, is_element,
    visitor::{Context, PrepareOutcome, Visitor},
};
use oxvg_collections::{
    atom::Atom,
    attribute::{inheritable::Inheritable, Attr},
    content_type::ContentType,
    element::ElementCategory,
};
#[cfg(feature = "serde")]
use serde::{Deserialize, Serialize};

#[cfg(feature = "wasm")]
use tsify::Tsify;

use crate::error::JobsError;

#[cfg_attr(feature = "wasm", derive(Tsify))]
#[cfg_attr(feature = "napi", napi(object))]
#[cfg_attr(feature = "serde", derive(Serialize, Deserialize))]
#[derive(Debug, Clone)]
#[cfg_attr(feature = "serde", serde(transparent))]
/// Filters `<g>` elements that have no effect.
///
/// For removing empty groups, see [`super::RemoveEmptyContainers`].
///
/// # Correctness
///
/// This job should never visually change the document.
///
/// # Errors
///
/// Never.
///
/// If this job produces an error or panic, please raise an [issue](https://github.com/noahbald/oxvg/issues)
pub struct CollapseGroups(pub bool);

impl<'input, 'arena> Visitor<'input, 'arena> for CollapseGroups {
    type Error = JobsError<'input>;

    fn prepare(
        &self,
        document: &Element<'input, 'arena>,
        _context: &mut Context<'input, 'arena, '_>,
    ) -> Result<PrepareOutcome, Self::Error> {
        if !self.0 {
            return Ok(PrepareOutcome::skip);
        }
        // Positive marks are already on the tree. Also refuse a collapse that
        // would make a structure-sensitive selector start matching.
        seal_collapses_that_change_matches(document);
        Ok(PrepareOutcome::none)
    }

    fn exit_element(
        &self,
        element: &Element<'input, 'arena>,
        _context: &mut Context<'input, 'arena, '_>,
    ) -> Result<(), Self::Error> {
        let Some(parent) = Element::parent_element(element) else {
            return Ok(());
        };

        if element.is_root() || is_element!(parent, Switch) {
            return Ok(());
        }
        if !is_element!(element, G) || !element.has_child_elements() {
            return Ok(());
        }

        move_attributes_to_child(element);
        flatten_when_all_attributes_moved(element);
        Ok(())
    }
}

impl Default for CollapseGroups {
    fn default() -> Self {
        Self(true)
    }
}

fn move_attributes_to_child(element: &Element) {
    log::debug!("collapse_groups: move_attributes_to_child");
    // Flattening is what this move prepares for. Doing it on an element whose
    // structure a selector depends on would retarget that selector.
    if element.structural_flatten_blocked() {
        return;
    }

    let mut children = element.children_iter();
    let Some(first_child) = children.next() else {
        log::debug!("collapse_groups: not moving attrs: no children");
        return;
    };
    if children.next().is_some() {
        log::debug!("collapse_groups: not moving attrs: many children");
        return;
    }

    let attrs = element.attributes();
    if attrs.is_empty() {
        log::debug!("collapse_groups: not moving attrs: no attrs to move");
        return;
    }

    if is_group_identifiable(element, &first_child) {
        log::debug!("collapse_groups: not moving attrs: identifiable");
        return;
    } else if is_position_visually_unstable(element, &first_child) {
        log::debug!("collapse_groups: not moving attrs: visually unstable");
        return;
    } else if is_node_with_filter(element) {
        log::debug!("collapse_groups: not moving attrs: filter");
        return;
    }

    let mut removals = Vec::default();
    let first_child_attrs = first_child.attributes();
    for mut attr in attrs.into_iter_mut() {
        let name = attr.name().clone();
        let child_attr = first_child_attrs.get_named_item_mut(&name);
        if has_animated_attr(&first_child, name.local_name()) {
            log::debug!("collapse_groups: canelled moves: has animated_attr");
            return;
        }

        removals.push(name);
        let Some(mut child_attr) = child_attr else {
            log::debug!("collapse_groups: moved {attr:?}: same as parent",);
            first_child_attrs.set_named_item(attr.clone());
            continue;
        };

        if let Attr::Transform(Inheritable::Defined(value)) = &mut *attr {
            let Attr::Transform(Inheritable::Defined(child_value)) = &mut *child_attr else {
                continue;
            };
            log::debug!("collapse_groups: moved transform: is transform");
            value.0.extend(mem::take(&mut child_value.0));
            mem::swap(&mut value.0, &mut child_value.0);
        } else if let ContentType::Inheritable(inheritable) = child_attr.value() {
            if Inheritable::Inherited == inheritable {
                log::debug!("collapse_groups: moved {attr:?}: is explicit inherit");
                *child_attr = attr.clone();
            }
        } else if *attr != *child_attr {
            log::debug!("collapse_groups: removing {attr:?}: inheritable attr is not inherited");
            removals.pop();
            break;
        }
    }

    for attr in removals {
        element.remove_attribute(&attr);
    }
}

/// Pins the smallest set of groups whose collapse would change which elements
/// match a structure-sensitive selector.
fn seal_collapses_that_change_matches<'input, 'arena>(root: &Element<'input, 'arena>) {
    use oxvg_ast::selectors::{restore_dom, snapshot_dom, structural_match_fingerprint};

    let original = structural_match_fingerprint(root);
    if original.is_empty() {
        return;
    }
    // Each pass pins one group. The bound is the number of elements.
    let limit = std::iter::once(root.clone())
        .chain(root.breadth_first())
        .count();
    for _ in 0..limit {
        let snapshot = snapshot_dom(root);
        let mut touched = Vec::new();
        simulate_collapse_walk(root, &mut touched);
        let changed = structural_match_fingerprint(root) != original;
        restore_dom(&snapshot);
        if !changed {
            return;
        }
        let Some(candidate) = first_sufficient_pin(root, &original, &touched) else {
            return;
        };
        candidate.block_structural_flatten();
    }
}

/// Returns the earliest collapsed group that, held in place, preserves matches.
fn first_sufficient_pin<'input, 'arena>(
    root: &Element<'input, 'arena>,
    original: &[Vec<usize>],
    touched: &[Element<'input, 'arena>],
) -> Option<Element<'input, 'arena>> {
    use oxvg_ast::selectors::{restore_dom, snapshot_dom, structural_match_fingerprint};

    for candidate in touched {
        if candidate.structural_flatten_blocked() {
            continue;
        }
        candidate.block_structural_flatten();
        let snapshot = snapshot_dom(root);
        let mut ignored = Vec::new();
        simulate_collapse_walk(root, &mut ignored);
        let preserves = structural_match_fingerprint(root) == original;
        restore_dom(&snapshot);
        if preserves {
            return Some(candidate.clone());
        }
        candidate.unblock_structural_flatten();
    }
    touched
        .iter()
        .find(|element| !element.structural_flatten_blocked())
        .cloned()
}

fn simulate_collapse_walk<'input, 'arena>(
    element: &Element<'input, 'arena>,
    touched: &mut Vec<Element<'input, 'arena>>,
) {
    // Collect first. A `<style>` child's next sibling can point at itself, and
    // collapsing rewrites sibling links while this walk is in progress.
    let children: Vec<_> = element.child_nodes_iter().collect();
    for node in children {
        let Some(child) = Element::new(node) else {
            continue;
        };
        simulate_collapse_walk(&child, touched);
        if collapse_changes_element(&child) {
            touched.push(child);
        }
    }
}

fn collapse_changes_element(element: &Element) -> bool {
    let Some(parent) = element.parent_element() else {
        return false;
    };
    // Same gate as `CollapseGroups::exit_element`. Other elements, including
    // `<style>`, are not candidates for this job.
    if element.is_root() || is_element!(parent, Switch) {
        return false;
    }
    if !is_element!(element, G) || !element.has_child_elements() {
        return false;
    }
    let before_len = element.attributes().len();
    let parent_before = element.parent.get();
    move_attributes_to_child(element);
    flatten_when_all_attributes_moved(element);
    let flattened = parent_before.is_some() && element.parent.get().is_none();
    flattened || element.attributes().len() != before_len
}

fn flatten_when_all_attributes_moved(element: &Element) {
    if !element.attributes().is_empty() {
        log::debug!("skipping flatten: has attributes");
        return;
    }

    {
        if element.breadth_first().any(|child| {
            child
                .qual_name()
                .categories()
                .contains(ElementCategory::Animation)
        }) {
            log::debug!("skipping flatten: has animating child");
            return;
        }
    }

    element.flatten();
}

fn has_animated_attr<'input>(element: &Element<'input, '_>, local_name: &Atom<'input>) -> bool {
    for child in std::iter::once(element.clone()).chain(element.breadth_first()) {
        if child
            .qual_name()
            .categories()
            .intersects(ElementCategory::Animation)
            && get_attribute!(child, AttributeName).is_some_and(|attr| &*attr == local_name)
        {
            return true;
        }
    }
    false
}

fn is_group_identifiable<'input, 'arena>(
    node: &Element<'input, 'arena>,
    child: &Element<'input, 'arena>,
) -> bool {
    has_attribute!(child, Id) && (!has_attribute!(node, Class) || !has_attribute!(child, Class))
}

fn is_position_visually_unstable<'input, 'arena>(
    node: &Element<'input, 'arena>,
    child: &Element<'input, 'arena>,
) -> bool {
    let is_node_clipping = has_attribute!(node, ClipPath | Mask);
    let is_child_transformed_group = is_element!(child, G) && has_attribute!(child, Transform);
    is_node_clipping || is_child_transformed_group
}

fn is_node_with_filter(node: &Element) -> bool {
    has_attribute!(node, Filter)
        || get_attribute!(node, Style)
            .is_some_and(|style| style.get(&PropertyId::Filter(VendorPrefix::None)).is_some())
}

#[test]
#[allow(clippy::too_many_lines)]
fn collapse_groups() -> anyhow::Result<()> {
    use crate::test_config;

    insta::assert_snapshot!(test_config(
        r#"{ "collapseGroups": true }"#,
        Some(
            r#"<svg xmlns="http://www.w3.org/2000/svg">
    <!-- Should remove both useless `g`s -->
    <g>
        <g>
            <path d="..."/>
        </g>
    </g>
</svg>"#
        )
    )?);

    insta::assert_snapshot!(test_config(
        r#"{ "collapseGroups": true }"#,
        Some(
            r#"<svg xmlns="http://www.w3.org/2000/svg">
    <!-- Should pass all inheritable attributes to children -->
    <g>
        <g attr1="val1">
            <path d="..."/>
        </g>
    </g>
    <g attr1="val1">
        <g attr2="val2">
            <path d="..."/>
        </g>
    </g>
    <g attr1="val1">
        <g>
            <path d="..."/>
        </g>
        <path d="..."/>
    </g>
    <g attr1="val1">
        <g attr2="val2">
            <path d="..."/>
        </g>
        <path d="..."/>
    </g>
</svg>"#
        )
    )?);

    insta::assert_snapshot!(test_config(
        r#"{ "collapseGroups": true }"#,
        Some(
            r#"<svg xmlns="http://www.w3.org/2000/svg">
    <!-- Should remove inheritable overridden attributes -->
    <g attr1="val1">
        <g fill="red">
            <path fill="green" d="..."/>
        </g>
        <path d="..."/>
    </g>
</svg>"#
        )
    )?);

    insta::assert_snapshot!(test_config(
        r#"{ "collapseGroups": true }"#,
        Some(
            r#"<svg xmlns="http://www.w3.org/2000/svg">
    <!-- Should remove group with equal attribute values to child -->
    <g attr1="val1">
        <g attr2="val2">
            <path attr2="val2" d="..."/>
        </g>
        <g attr2="val2">
            <path attr2="val3" d="..."/>
        </g>
    </g>
</svg>"#
        )
    )?);

    insta::assert_snapshot!(test_config(
        r#"{ "collapseGroups": true }"#,
        Some(
            r#"<svg xmlns="http://www.w3.org/2000/svg">
    <!-- Should join transform attributes into `transform="rotate(45) scale(2)"` -->
    <g attr1="val1">
        <g transform="rotate(45)">
            <path transform="scale(2)" d="..."/>
        </g>
        <path d="..."/>
    </g>
</svg>"#
        )
    )?);

    insta::assert_snapshot!(test_config(
        r#"{ "collapseGroups": true }"#,
        Some(
            r#"<svg xmlns="http://www.w3.org/2000/svg">
    <!-- Should preserve groups with `clip-path` -->
    <clipPath id="a">
       <path d="..."/>
    </clipPath>
    <clipPath id="b">
       <path d="..."/>
    </clipPath>
    <g transform="matrix(0 -1.25 -1.25 0 100 100)" clip-path="url(#a)">
        <g transform="scale(.2)">
            <path d="..."/>
            <path d="..."/>
        </g>
    </g>
    <g transform="matrix(0 -1.25 -1.25 0 100 100)" clip-path="url(#a)">
        <g transform="scale(.2)">
            <g>
                <g clip-path="url(#b)">
                    <path d="..."/>
                    <path d="..."/>
                </g>
            </g>
        </g>
    </g>
</svg>"#
        )
    )?);

    insta::assert_snapshot!(test_config(
        r#"{ "collapseGroups": true }"#,
        Some(
            r#"<svg xmlns="http://www.w3.org/2000/svg">
    <!-- Should preserve groups with `clip-path` and `mask` -->
    <clipPath id="a">
       <path d="..."/>
    </clipPath>
    <path d="..."/>
    <g clip-path="url(#a)">
        <path d="..." transform="scale(.2)"/>
    </g>
    <g mask="url(#a)">
        <path d="..." transform="scale(.2)"/>
    </g>
</svg>"#
        )
    )?);

    insta::assert_snapshot!(test_config(
        r#"{ "collapseGroups": true }"#,
        Some(
            r##"<svg xmlns="http://www.w3.org/2000/svg">
    <!-- Should preserve groups with `id` or animation children -->
    <g stroke="#000">
        <g id="star">
            <path id="bar" d="..."/>
        </g>
    </g>
    <g>
        <animate id="frame0" attributeName="visibility" values="visible" dur="33ms" begin="0s;frame27.end"/>
        <path d="..." fill="#272727"/>
        <path d="..." fill="#404040"/>
        <path d="..." fill="#2d2d2d"/>
    </g>
    <g transform="rotate(-90 25 0)">
        <circle stroke-dasharray="110" r="20" stroke="#10cfbd" fill="none" stroke-width="3" stroke-linecap="round">
            <animate attributeName="stroke-dashoffset" values="360;140" dur="2.2s" keyTimes="0;1" calcMode="spline" fill="freeze" keySplines="0.41,0.314,0.8,0.54" repeatCount="indefinite" begin="0"/>
            <animateTransform attributeName="transform" type="rotate" values="0;274;360" keyTimes="0;0.74;1" calcMode="linear" dur="2.2s" repeatCount="indefinite" begin="0"/>
        </circle>
    </g>
</svg>"##
        )
    )?);

    insta::assert_snapshot!(test_config(
        r#"{ "collapseGroups": true }"#,
        Some(
            r#"<svg xmlns="http://www.w3.org/2000/svg">
    <!-- Should preserve groups with classes -->
    <style>
        .n{display:none}
        .i{display:inline}
    </style>
    <g id="a">
        <g class="i"/>
    </g>
    <g id="b" class="n">
        <g class="i"/>
    </g>
</svg>"#
        )
    )?);

    insta::assert_snapshot!(test_config(
        r#"{ "collapseGroups": true }"#,
        Some(
            r#"<svg xmlns="http://www.w3.org/2000/svg">
    <!-- Should preserve children of `<switch>` -->
    <switch>
        <g id="a">
            <g class="i"/>
        </g>
        <g id="b" class="n">
            <g class="i"/>
        </g>
        <g>
            <g/>
        </g>
    </switch>
</svg>"#
        )
    )?);

    insta::assert_snapshot!(test_config(
        r#"{ "collapseGroups": true }"#,
        Some(
            r#"<svg xmlns="http://www.w3.org/2000/svg">
    <!-- Should replace inheritable value -->
	<g color="red">
		<g color="inherit" fill="none" stroke="none">
			<circle cx="130" cy="80" r="60" fill="currentColor"/>
			<circle cx="350" cy="80" r="60" stroke="currentColor" stroke-width="4"/>
		</g>
	</g>
</svg>"#
        )
    )?);

    insta::assert_snapshot!(test_config(
        r#"{ "collapseGroups": true }"#,
        Some(
            r#"<svg xmlns="http://www.w3.org/2000/svg">
    <!-- Should remove useless group -->
    <g filter="url(#...)">
        <g>
            <path d="..."/>
        </g>
    </g>
</svg>"#
        )
    )?);

    insta::assert_snapshot!(test_config(
        r#"{ "collapseGroups": true }"#,
        Some(
            r#"<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 88 88">
  <!-- Should preserve group if some attrs cannot be moved -->
  <filter id="a">
    <feGaussianBlur stdDeviation="1"/>
  </filter>
  <g transform="matrix(0.6875,0,0,0.6875,20.34375,66.34375)" style="filter:url(#a)">
    <path d="M 33.346591,-83.471591 L -10.744318,-36.471591 L -10.49989,-32.5" style="fill-opacity:1"/>
  </g>
</svg>"#
        )
    )?);

    insta::assert_snapshot!(test_config(
        r#"{ "collapseGroups": true }"#,
        Some(
            r#"<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100">
    <!-- Should preserve group if parent has `filter` -->
    <clipPath id="a">
        <circle cx="25" cy="15" r="10"/>
    </clipPath>
    <filter id="b">
        <feColorMatrix type="saturate"/>
    </filter>
    <g filter="url(#b)">
        <g clip-path="url(#a)">
            <circle cx="30" cy="10" r="10" fill="yellow" id="c1"/>
        </g>
    </g>
    <g style="filter:url(#b)">
        <g clip-path="url(#a)">
            <circle cx="20" cy="10" r="10" fill="blue" id="c2"/>
        </g>
    </g>
    <circle cx="25" cy="15" r="10" stroke="black" stroke-width=".1" fill="none"/>
</svg>"#
        )
    )?);

    Ok(())
}

#[test]
fn structural_selectors_block_only_implicated_groups() -> anyhow::Result<()> {
    use crate::test_config;

    // `g > rect` needs the direct parent. The outer group and an unrelated
    // group are not part of that relationship, so they still collapse.
    let child = test_config(
        r#"{ "collapseGroups": true }"#,
        Some(
            r#"<svg xmlns="http://www.w3.org/2000/svg">
    <style>g &gt; rect { fill: red }</style>
    <g>
        <g><rect id="target" width="1" height="1"/></g>
        <circle id="sib" r="1"/>
    </g>
    <g><circle id="free" r="1"/></g>
</svg>"#,
        ),
    )?;
    assert!(
        child.contains("<g>\n        <rect id=\"target\""),
        "direct parent should stay:\n{child}"
    );
    assert!(
        child.contains("<circle id=\"sib\""),
        "sibling of the inner group is outside that relationship:\n{child}"
    );
    assert!(
        !child.contains("<g>\n        <circle id=\"sib\""),
        "outer group should collapse:\n{child}"
    );
    assert!(
        !child.contains("<g>\n        <circle id=\"free\"") && child.contains("<circle id=\"free\""),
        "unrelated group should collapse:\n{child}"
    );

    // `g rect` is satisfied by the outermost ancestor. The inner group can go.
    let descendant = test_config(
        r#"{ "collapseGroups": true }"#,
        Some(
            r#"<svg xmlns="http://www.w3.org/2000/svg">
    <style>g rect { fill: red }</style>
    <g>
        <g><rect id="target" width="1" height="1"/></g>
        <circle id="sib" r="1"/>
    </g>
    <g><circle id="free" r="1"/></g>
</svg>"#,
        ),
    )?;
    assert!(
        descendant.contains("<g>\n        <rect id=\"target\"")
            && descendant.contains("<circle id=\"sib\""),
        "outermost ancestor should keep both children:\n{descendant}"
    );
    assert!(
        !descendant.contains("<g>\n            <rect"),
        "inner group should collapse:\n{descendant}"
    );
    assert!(
        !descendant.contains("<g>\n        <circle id=\"free\"")
            && descendant.contains("<circle id=\"free\""),
        "unrelated group should collapse:\n{descendant}"
    );

    // Nothing matches `svg > rect` yet. Collapsing every group around the rect
    // would make it match, so one implicated group stays. The other group goes.
    let near_miss = test_config(
        r#"{ "collapseGroups": true }"#,
        Some(
            r#"<svg xmlns="http://www.w3.org/2000/svg">
    <style>svg &gt; rect { fill: red }</style>
    <g>
        <g><rect id="target" width="1" height="1"/></g>
    </g>
    <g><circle id="free" r="1"/></g>
</svg>"#,
        ),
    )?;
    assert!(
        near_miss.contains("<g") && near_miss.contains("<rect id=\"target\""),
        "{near_miss}"
    );
    assert!(
        !near_miss.contains("<svg xmlns=\"http://www.w3.org/2000/svg\">\n    <style>\n        svg>rect{fill:red}\n    </style>\n    <rect"),
        "collapsing every group made svg > rect match:\n{near_miss}"
    );
    assert!(
        near_miss.contains("<circle id=\"free\"") && !near_miss.contains("<g>\n        <circle id=\"free\""),
        "unrelated group should collapse:\n{near_miss}"
    );

    // The plain group only shares the `g` piece. The group that parents `.special` stays.
    let nearby_piece = test_config(
        r#"{ "collapseGroups": true }"#,
        Some(
            r#"<svg xmlns="http://www.w3.org/2000/svg">
    <style>g &gt; .special { fill: red }</style>
    <g><rect id="plain" width="1" height="1"/></g>
    <g><rect id="holder" class="special" width="1" height="1"/></g>
</svg>"#,
        ),
    )?;
    assert!(
        !nearby_piece.contains("<g>\n        <rect id=\"plain\""),
        "plain group should collapse:\n{nearby_piece}"
    );
    assert!(
        nearby_piece.contains("<g>\n        <rect id=\"holder\" class=\"special\""),
        "holder completes g > .special:\n{nearby_piece}"
    );

    // `:nth-child` depends on the parent and the preceding sibling slot.
    // The inner group can collapse without changing which element is 2nd.
    let nth = test_config(
        r#"{ "collapseGroups": true }"#,
        Some(
            r#"<svg xmlns="http://www.w3.org/2000/svg">
    <style>rect:nth-child(2) { fill: red }</style>
    <g>
        <g><circle id="first" r="1"/></g>
        <rect id="second" width="1" height="1"/>
    </g>
    <g><circle id="other" r="2"/></g>
</svg>"#,
        ),
    )?;
    assert!(
        nth.contains("<g>\n        <circle id=\"first\"") && nth.contains("<rect id=\"second\""),
        "parent and position should stay:\n{nth}"
    );
    assert!(
        !nth.contains("<g>\n            <circle id=\"first\""),
        "inner group is not the nth anchor:\n{nth}"
    );
    assert!(
        !nth.contains("<g>\n        <circle id=\"other\"") && nth.contains("<circle id=\"other\""),
        "unrelated group should collapse:\n{nth}"
    );

    // The anchor is the group next to the rect, not the group inside it.
    let has_anchor = test_config(
        r#"{ "collapseGroups": true }"#,
        Some(
            r#"<svg xmlns="http://www.w3.org/2000/svg">
    <style>g:has(+ rect) { fill: red }</style>
    <g class="anchor">
        <g><circle id="inner" r="1"/></g>
    </g>
    <rect id="next" width="1" height="1"/>
    <g><circle id="other" r="2"/></g>
</svg>"#,
        ),
    )?;
    assert!(
        has_anchor.contains("<g class=\"anchor\">"),
        "anchor group should stay:\n{has_anchor}"
    );
    assert!(
        has_anchor.contains("<circle id=\"inner\"") && !has_anchor.contains("<g>\n            <circle id=\"inner\""),
        "inner group should collapse:\n{has_anchor}"
    );
    assert!(
        has_anchor.contains("<rect id=\"next\"")
            && !has_anchor.contains("<g>\n        <circle id=\"other\"")
            && has_anchor.contains("<circle id=\"other\""),
        "unrelated group should collapse:\n{has_anchor}"
    );

    // Collapsing the inner group would make `g.foo > rect` start matching.
    let donated_class = test_config(
        r#"{ "collapseGroups": true }"#,
        Some(
            r#"<svg xmlns="http://www.w3.org/2000/svg">
    <style>g.foo &gt; rect { fill: red }</style>
    <g class="foo">
        <g><rect id="inner" width="1" height="1"/></g>
        <circle id="sib" r="1"/>
    </g>
    <g><circle id="other" r="2"/></g>
</svg>"#,
        ),
    )?;
    assert!(
        donated_class.contains("<g class=\"foo\">"),
        "class should stay on the group:\n{donated_class}"
    );
    assert!(
        donated_class.contains("<g>\n            <rect id=\"inner\""),
        "inner group keeps g.foo from matching the rect:\n{donated_class}"
    );
    assert!(
        !donated_class.contains("<g>\n        <circle id=\"other\"")
            && donated_class.contains("<circle id=\"other\""),
        "unrelated group should collapse:\n{donated_class}"
    );

    Ok(())
}
