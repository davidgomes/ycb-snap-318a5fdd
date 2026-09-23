use oxvg_ast::{
    element::Element,
    has_attribute, has_computed_style, is_element,
    style::ComputedStyles,
    visitor::{Context, PrepareOutcome, Visitor},
};
use oxvg_collections::element::ElementCategory;
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
/// Removes container elements with no functional children or meaningful attributes.
///
/// # Correctness
///
/// This job shouldn't visually change the document. Removing whitespace may have
/// an effect on `inline` or `inline-block` elements.
///
/// # Errors
///
/// Never.
///
/// If this job produces an error or panic, please raise an [issue](https://github.com/noahbald/oxvg/issues)
pub struct RemoveEmptyContainers(pub bool);

impl<'input, 'arena> Visitor<'input, 'arena> for RemoveEmptyContainers {
    type Error = JobsError<'input>;

    fn prepare(
        &self,
        document: &Element<'input, 'arena>,
        context: &mut Context<'input, 'arena, '_>,
    ) -> Result<PrepareOutcome, Self::Error> {
        if !self.0 {
            return Ok(PrepareOutcome::skip);
        }
        context.query_has_stylesheet(document);
        context.query_has_script(document);
        seal_removals_that_change_matches(document, context);
        Ok(PrepareOutcome::none)
    }

    fn exit_element(
        &self,
        element: &Element<'input, 'arena>,
        context: &mut Context<'input, 'arena, '_>,
    ) -> Result<(), Self::Error> {
        if container_is_removable(element, context)? {
            element.remove();
        }
        Ok(())
    }
}

fn container_is_removable<'input, 'arena>(
    element: &Element<'input, 'arena>,
    context: &Context<'input, 'arena, '_>,
) -> Result<bool, JobsError<'input>> {
    let name = element.qual_name();

    if !name.categories().contains(ElementCategory::Container) || !element.is_empty() {
        return Ok(false);
    }
    if is_element!(element, Svg) {
        return Ok(false);
    } else if is_element!(element, Pattern) {
        if !element.attributes().is_empty() {
            return Ok(false);
        }
    } else if is_element!(element, Mask) {
        if has_attribute!(element, Id) {
            return Ok(false);
        }
    } else if element
        .parent_element()
        .is_some_and(|e| is_element!(e, Switch))
    {
        return Ok(false);
    }
    if is_element!(element, G) {
        let computed_styles = ComputedStyles::default()
            .with_all(element, &context.query_has_stylesheet_result)
            .map_err(JobsError::ComputedStylesError)?;
        if has_computed_style!(computed_styles, Filter) {
            return Ok(false);
        }
    }
    Ok(true)
}

/// Keeps an empty container when removing it would make a structure-sensitive
/// selector match an element it does not match now.
fn seal_removals_that_change_matches<'input, 'arena>(
    root: &Element<'input, 'arena>,
    context: &Context<'input, 'arena, '_>,
) {
    use oxvg_ast::selectors::{restore_dom, snapshot_dom, structural_match_fingerprint};

    let original = structural_match_fingerprint(root);
    if original.is_empty() {
        return;
    }
    let limit = std::iter::once(root.clone())
        .chain(root.breadth_first())
        .count();
    for _ in 0..limit {
        let snapshot = snapshot_dom(root);
        let mut touched = Vec::new();
        simulate_removal_walk(root, context, &mut touched);
        let changed = structural_match_fingerprint(root) != original;
        restore_dom(&snapshot);
        if !changed {
            return;
        }
        let Some(candidate) = first_sufficient_removal_pin(root, context, &original, &touched)
        else {
            return;
        };
        candidate.block_structural_removal();
    }
}

fn first_sufficient_removal_pin<'input, 'arena>(
    root: &Element<'input, 'arena>,
    context: &Context<'input, 'arena, '_>,
    original: &[Vec<usize>],
    touched: &[Element<'input, 'arena>],
) -> Option<Element<'input, 'arena>> {
    use oxvg_ast::selectors::{restore_dom, snapshot_dom, structural_match_fingerprint};

    for candidate in touched {
        if candidate.structural_removal_blocked() {
            continue;
        }
        candidate.block_structural_removal();
        let snapshot = snapshot_dom(root);
        let mut ignored = Vec::new();
        simulate_removal_walk(root, context, &mut ignored);
        let preserves = structural_match_fingerprint(root) == original;
        restore_dom(&snapshot);
        if preserves {
            return Some(candidate.clone());
        }
        candidate.unblock_structural_removal();
    }
    touched
        .iter()
        .find(|element| !element.structural_removal_blocked())
        .cloned()
}

fn simulate_removal_walk<'input, 'arena>(
    element: &Element<'input, 'arena>,
    context: &Context<'input, 'arena, '_>,
    touched: &mut Vec<Element<'input, 'arena>>,
) {
    // Collect first. A `<style>` child's next sibling can point at itself, and
    // removal rewrites sibling links while this walk is in progress.
    let children: Vec<_> = element.child_nodes_iter().collect();
    for node in children {
        let Some(child) = Element::new(node) else {
            continue;
        };
        simulate_removal_walk(&child, context, touched);
        if container_is_removable(&child, context).unwrap_or(false) {
            let parent = child.parent.get();
            child.remove();
            if parent.is_some() && child.parent.get().is_none() {
                touched.push(child);
            }
        }
    }
}

impl Default for RemoveEmptyContainers {
    fn default() -> Self {
        Self(true)
    }
}

#[test]
#[allow(clippy::too_many_lines)]
fn remove_empty_containers() -> anyhow::Result<()> {
    use crate::test_config;

    insta::assert_snapshot!(test_config(
        r#"{ "removeEmptyContainers": true }"#,
        Some(
            r#"<svg xmlns="http://www.w3.org/2000/svg">
    <!-- remove empty containers -->
    <pattern/>
    <g>
        <marker>
            <a/>
        </marker>
    </g>
    <path d="..."/>
</svg>"#
        ),
    )?);

    insta::assert_snapshot!(test_config(
        r#"{ "removeEmptyContainers": true }"#,
        Some(
            r#"<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink">
    <!-- preserve non-empty containers -->
    <defs>
        <pattern id="a">
            <rect/>
        </pattern>
        <pattern xlink:href="url(#a)" id="b"/>
    </defs>
    <g>
        <marker>
            <a/>
        </marker>
        <path d="..."/>
    </g>
</svg>"#
        ),
    )?);

    insta::assert_snapshot!(test_config(
        r#"{ "removeEmptyContainers": true }"#,
        Some(
            r#"<svg xmlns="http://www.w3.org/2000/svg" xmlns:x="http://www.w3.org/1999/xlink">
    <!-- preserve non-empty containers -->
    <defs>
        <pattern id="a">
            <rect/>
        </pattern>
        <pattern x:href="url(#a)" id="b"/>
    </defs>
    <g>
        <marker>
            <a/>
        </marker>
        <path d="..."/>
    </g>
</svg>"#
        ),
    )?);

    insta::assert_snapshot!(test_config(
        r#"{ "removeEmptyContainers": true }"#,
        Some(
            r#"<svg>
    <!-- preserve non-empty containers -->
    <defs>
        <filter id="feTileFilter" filterUnits="userSpaceOnUse" primitiveUnits="userSpaceOnUse" x="115" y="40" width="250" height="250">
            <feFlood x="115" y="40" width="54" height="19" flood-color="lime"/>
            <feOffset x="115" y="40" width="50" height="25" dx="6" dy="6" result="offset"/>
            <feTile/>
        </filter>
    </defs>
    <g filter="url(#feTileFilter)"/>
</svg>"#
        ),
    )?);

    insta::assert_snapshot!(test_config(
        r#"{ "removeEmptyContainers": true }"#,
        Some(
            r#"<svg width="480" height="360" xmlns="http://www.w3.org/2000/svg">
    <!-- preserve id'd mask -->
    <mask id="testMask" />
    <rect x="100" y="100" width="250" height="150" fill="green" />
    <rect x="100" y="100" width="250" height="150" fill="red" mask="url(#testMask)" />
</svg>"#
        ),
    )?);

    insta::assert_snapshot!(test_config(
        r#"{ "removeEmptyContainers": true }"#,
        Some(
            r#"<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 462 352">
    <!-- preserve children of `switch` -->
    <switch>
        <g requiredFeatures="http://www.w3.org/TR/SVG11/feature#Extensibility"/>
        <a transform="translate(0,-5)" href="https://www.diagrams.net/doc/faq/svg-export-text-problems" target="_blank">
            <text text-anchor="middle" font-size="10px" x="50%" y="100%">Viewer does not support full SVG 1.1</text>
        </a>
    </switch>
</svg>"#
        ),
    )?);

    insta::assert_snapshot!(test_config(
        r#"{ "removeEmptyContainers": true }"#,
        Some(
            r##"<svg viewBox="0 0 50 50" xmlns="http://www.w3.org/2000/svg">
    <!-- preserve filtered `g`s -->
    <filter id="a" x="0" y="0" width="50" height="50" filterUnits="userSpaceOnUse">
        <feFlood flood-color="#aaa"/>
    </filter>
    <mask id="b" x="0" y="0" width="50" height="50">
        <g style="filter: url(#a)"/>
    </mask>
    <text x="16" y="16" style="mask: url(#b)">•ᴗ•</text>
</svg>"##
        ),
    )?);

    Ok(())
}

#[test]
fn structural_selectors_block_only_implicated_containers() -> anyhow::Result<()> {
    use crate::test_config;

    let matched = test_config(
        r#"{ "removeEmptyContainers": true }"#,
        Some(
            r#"<svg xmlns="http://www.w3.org/2000/svg">
    <style>#a:empty { display: none }</style>
    <g id="a"></g>
    <g id="b"></g>
</svg>"#,
        ),
    )?;
    assert!(
        matched.contains("id=\"a\""),
        "empty group implicated by :empty should stay:\n{matched}"
    );
    assert!(
        !matched.contains("id=\"b\""),
        "unrelated empty group should be removed:\n{matched}"
    );

    // The `<style>` element is the svg's first child, so the empty group has to
    // sit with the rect under another parent. Removing it would make the rect
    // `:nth-child(1)`.
    let created = test_config(
        r#"{ "removeEmptyContainers": true }"#,
        Some(
            r#"<svg xmlns="http://www.w3.org/2000/svg">
    <style>rect:nth-child(1) { fill: red }</style>
    <g>
        <g id="slot"></g>
        <rect id="shape" width="1" height="1"/>
    </g>
</svg>"#,
        ),
    )?;
    assert!(
        created.contains("id=\"slot\""),
        "removing the slot would make rect:nth-child(1) match:\n{created}"
    );
    assert!(created.contains("id=\"shape\""), "{created}");

    Ok(())
}
