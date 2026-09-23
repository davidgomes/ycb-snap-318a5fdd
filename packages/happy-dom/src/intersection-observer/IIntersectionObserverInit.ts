import type Document from '../nodes/document/Document.js';
import type Element from '../nodes/element/Element.js';

/**
 * Options for constructing an IntersectionObserver.
 */
export default interface IIntersectionObserverInit {
	/**
	 * Ancestor element or document used as the intersection root.
	 * `null` selects the viewport.
	 */
	root?: Element | Document | null;
	/**
	 * Offset applied to the root rectangle, using CSS margin shorthand.
	 * One to four values, each `px` or `%`.
	 */
	rootMargin?: string;
	/**
	 * A threshold or list of thresholds in the range [0, 1].
	 */
	threshold?: number | number[];
}
