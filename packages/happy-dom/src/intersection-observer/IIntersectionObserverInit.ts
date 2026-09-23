import type Element from '../nodes/element/Element.js';
import type Document from '../nodes/document/Document.js';

export default interface IIntersectionObserverInit {
	/**
	 * A specific ancestor of the target element against which the intersection is to be calculated. The viewport is used when null.
	 */
	root?: Element | Document | null;
	/**
	 * Margin around the root, using CSS margin shorthand syntax with "px" or "%" values (e.g. "10px 20%").
	 */
	rootMargin?: string;
	/**
	 * A list of thresholds, sorted in increasing numeric order, where each threshold is a ratio of intersection area to bounding box area of the target.
	 */
	threshold?: number | number[];
}
