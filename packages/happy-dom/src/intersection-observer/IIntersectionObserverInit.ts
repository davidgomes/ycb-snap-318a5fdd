import type Document from '../nodes/document/Document.js';
import type Element from '../nodes/element/Element.js';

/**
 * Options for constructing an IntersectionObserver.
 */
export default interface IIntersectionObserverInit {
	/**
	 * Ancestor element used as the intersection root.
	 * Null or omitted uses the document viewport.
	 */
	root?: Element | Document | null;
	/**
	 * Margin around the root. Accepts 1-4 CSS shorthand values in px or %.
	 */
	rootMargin?: string;
	/**
	 * One ratio, or a list of ratios, between 0 and 1.
	 */
	threshold?: number | number[];
}
