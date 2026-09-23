import * as PropertySymbol from '../PropertySymbol.js';
import IntersectionObserverEntry from './IntersectionObserverEntry.js';
import type IIntersectionObserverInit from './IIntersectionObserverInit.js';
import type Element from '../nodes/element/Element.js';
import type Document from '../nodes/document/Document.js';
import type BrowserWindow from '../window/BrowserWindow.js';
import DOMRect from '../dom/DOMRect.js';
import NodeTypeEnum from '../nodes/node/NodeTypeEnum.js';
import DOMExceptionNameEnum from '../exception/DOMExceptionNameEnum.js';

type TIntersectionObserverCallback = (
	entries: IntersectionObserverEntry[],
	observer: IntersectionObserver
) => void;

interface IRootMarginValue {
	value: number;
	unit: 'px' | '%';
}

interface IRectEdges {
	top: number;
	right: number;
	bottom: number;
	left: number;
}

const ROOT_MARGIN_VALUE_REGEXP = /^([+-]?(?:\d+(?:\.\d+)?|\.\d+)(?:e[+-]?\d+)?)(px|%)$/i;
const CSS_WHITESPACE_REGEXP = /[ \t\n\r\f]+/;

/**
 * The IntersectionObserver interface of the Intersection Observer API provides a way to asynchronously observe changes in the intersection of a target element with an ancestor element or with a top-level document's viewport.
 *
 * Happy DOM has no layout engine, so intersections are calculated from what getBoundingClientRect() returns for the targets and the root element (or from the window viewport when the root is not an element).
 *
 * Intersections are updated asynchronously after targets are observed and after "scroll" and "resize" events, and synchronously when calling takeRecords().
 *
 * @see https://developer.mozilla.org/en-US/docs/Web/API/IntersectionObserver
 */
export default class IntersectionObserver {
	// Injected by WindowContextClassExtender
	protected declare [PropertySymbol.window]: BrowserWindow;
	#callback: TIntersectionObserverCallback;
	#root: Element | Document | null;
	#rootMargin: IRootMarginValue[];
	#thresholds: ReadonlyArray<number>;
	// Previous threshold index of each target (-1 before the first update), in the order that the targets were observed.
	#targets: Map<Element, number> = new Map();
	#isUpdateQueued = false;
	#updateListener = (): void => this.#queueUpdate();

	/**
	 * Constructor.
	 *
	 * @param callback Callback.
	 * @param [options] Options.
	 */
	constructor(callback: TIntersectionObserverCallback, options?: IIntersectionObserverInit) {
		const window = this[PropertySymbol.window];

		if (!window) {
			throw new TypeError(
				`Failed to construct '${this.constructor.name}': '${this.constructor.name}' was constructed outside a Window context.`
			);
		}

		if (arguments.length === 0) {
			throw new window.TypeError(
				"Failed to construct 'IntersectionObserver': 1 argument required, but only 0 present."
			);
		}

		if (typeof callback !== 'function') {
			throw new window.TypeError(
				"Failed to construct 'IntersectionObserver': parameter 1 is not of type 'Function'."
			);
		}

		if (options !== undefined && options !== null && typeof options !== 'object') {
			throw new window.TypeError(
				"Failed to construct 'IntersectionObserver': The provided value is not of type 'IntersectionObserverInit'."
			);
		}

		const root = options?.root ?? null;

		if (
			root !== null &&
			root[PropertySymbol.nodeType] !== NodeTypeEnum.elementNode &&
			root[PropertySymbol.nodeType] !== NodeTypeEnum.documentNode
		) {
			throw new window.TypeError(
				"Failed to construct 'IntersectionObserver': Failed to read the 'root' property from 'IntersectionObserverInit': The provided value is not of type '(Document or Element)'."
			);
		}

		this.#callback = callback;
		this.#root = root;
		this.#rootMargin = this.#parseRootMargin(
			options?.rootMargin !== undefined ? String(options.rootMargin) : '0px'
		);
		this.#thresholds = this.#parseThresholds(options?.threshold ?? 0);
	}

	/**
	 * Returns root.
	 *
	 * @returns Root.
	 */
	public get root(): Element | Document | null {
		return this.#root;
	}

	/**
	 * Returns root margin.
	 *
	 * @returns Root margin in the form "top right bottom left".
	 */
	public get rootMargin(): string {
		return this.#rootMargin.map(({ value, unit }) => `${value}${unit}`).join(' ');
	}

	/**
	 * Returns thresholds.
	 *
	 * @returns Thresholds in ascending order.
	 */
	public get thresholds(): ReadonlyArray<number> {
		return this.#thresholds;
	}

	/**
	 * Starts observing.
	 *
	 * @param target Target.
	 */
	public observe(target: Element): void {
		this.#validateTarget('observe', target, arguments.length);

		if (this.#targets.has(target)) {
			return;
		}

		if (!this.#targets.size) {
			this.#addUpdateListeners();
		}

		this.#targets.set(target, -1);
		this.#queueUpdate();
	}

	/**
	 * Disconnects.
	 */
	public disconnect(): void {
		if (!this.#targets.size) {
			return;
		}

		this.#targets.clear();
		this.#removeUpdateListeners();
	}

	/**
	 * Unobserves an element.
	 *
	 * @param target Target.
	 */
	public unobserve(target: Element): void {
		this.#validateTarget('unobserve', target, arguments.length);

		if (this.#targets.delete(target) && !this.#targets.size) {
			this.#removeUpdateListeners();
		}
	}

	/**
	 * Returns an array of IntersectionObserverEntry objects for the intersection changes that have not yet been delivered to the callback.
	 *
	 * @returns Records.
	 */
	public takeRecords(): IntersectionObserverEntry[] {
		return this.#updateObservations();
	}

	/**
	 * Queues an update of the intersections, which delivers the changed entries to the callback.
	 */
	#queueUpdate(): void {
		if (this.#isUpdateQueued) {
			return;
		}

		this.#isUpdateQueued = true;

		this[PropertySymbol.window].queueMicrotask(() => {
			this.#isUpdateQueued = false;

			const entries = this.#updateObservations();

			if (entries.length) {
				this.#callback.call(this, entries, this);
			}
		});
	}

	/**
	 * Updates the intersections of all targets.
	 *
	 * @returns Entries for the targets that have crossed a threshold since the previous update.
	 */
	#updateObservations(): IntersectionObserverEntry[] {
		const entries: IntersectionObserverEntry[] = [];

		if (!this.#targets.size) {
			return entries;
		}

		const time = this[PropertySymbol.window].performance.now();
		const root = this.#getRootIntersectionEdges();

		for (const [target, previousThresholdIndex] of this.#targets) {
			const boundingClientRect = this.#getBoundingClientRect(target);
			const left = Math.max(boundingClientRect.left, root.left);
			const top = Math.max(boundingClientRect.top, root.top);
			const right = Math.min(boundingClientRect.right, root.right);
			const bottom = Math.min(boundingClientRect.bottom, root.bottom);
			// Edge-adjacent rects are intersecting even though the intersection has no area.
			const isIntersecting = left <= right && top <= bottom;
			const intersectionRect = isIntersecting
				? new DOMRect(left, top, right - left, bottom - top)
				: new DOMRect();
			const targetArea = boundingClientRect.width * boundingClientRect.height;
			const intersectionRatio =
				targetArea > 0
					? (intersectionRect.width * intersectionRect.height) / targetArea
					: isIntersecting
						? 1
						: 0;
			// As in browsers, being below the lowest threshold shares index 0 with not intersecting.
			const thresholdIndex = isIntersecting
				? this.#thresholds.filter((threshold) => threshold <= intersectionRatio).length
				: 0;

			if (thresholdIndex !== previousThresholdIndex) {
				this.#targets.set(target, thresholdIndex);
				entries.push(
					new IntersectionObserverEntry({
						time,
						rootBounds: new DOMRect(
							root.left,
							root.top,
							Math.max(0, root.right - root.left),
							Math.max(0, root.bottom - root.top)
						),
						boundingClientRect,
						intersectionRect,
						intersectionRatio,
						isIntersecting,
						target
					})
				);
			}
		}

		return entries;
	}

	/**
	 * Returns the edges of the root intersection rectangle, which is the root expanded by the root margin.
	 *
	 * The edges are not normalized, so that a root that has been inverted by negative margins never intersects.
	 *
	 * @returns Edges.
	 */
	#getRootIntersectionEdges(): IRectEdges {
		const root = this.#root;
		let rect: DOMRect;

		if (root && root[PropertySymbol.nodeType] === NodeTypeEnum.elementNode) {
			rect = this.#getBoundingClientRect(<Element>root);
		} else {
			const window = (<Document | null>root)?.defaultView ?? this[PropertySymbol.window];
			rect = new DOMRect(0, 0, window.innerWidth, window.innerHeight);
		}

		// Browsers resolve percentages for top and bottom against the height, and for right and left against the width.
		const [top, right, bottom, left] = this.#rootMargin.map(({ value, unit }, index) =>
			unit === '%' ? (value * (index % 2 === 0 ? rect.height : rect.width)) / 100 : value
		);

		return {
			top: rect.top - top,
			right: rect.right + right,
			bottom: rect.bottom + bottom,
			left: rect.left - left
		};
	}

	/**
	 * Returns the bounding client rect of an element.
	 *
	 * Mocked implementations of getBoundingClientRect() often return plain objects with only some of the DOMRect properties, so missing edges are derived from the other properties.
	 *
	 * @param element Element.
	 * @returns Bounding client rect.
	 */
	#getBoundingClientRect(element: Element): DOMRect {
		const rect = <Partial<DOMRect>>element.getBoundingClientRect();
		const left = rect.left ?? rect.x ?? 0;
		const top = rect.top ?? rect.y ?? 0;
		const right = rect.right ?? left + (rect.width ?? 0);
		const bottom = rect.bottom ?? top + (rect.height ?? 0);

		return new DOMRect(left, top, right - left, bottom - top);
	}

	/**
	 * Parses a root margin, similar to the CSS "margin" shorthand property.
	 *
	 * @param rootMargin Root margin.
	 * @returns Top, right, bottom and left margins.
	 */
	#parseRootMargin(rootMargin: string): IRootMarginValue[] {
		const window = this[PropertySymbol.window];
		const values: IRootMarginValue[] = [];

		for (const token of rootMargin.split(CSS_WHITESPACE_REGEXP)) {
			if (!token) {
				continue;
			}

			if (values.length === 4) {
				throw new window.DOMException(
					"Failed to construct 'IntersectionObserver': Extra text found at the end of rootMargin.",
					DOMExceptionNameEnum.syntaxError
				);
			}

			const match = token.match(ROOT_MARGIN_VALUE_REGEXP);

			if (!match) {
				throw new window.DOMException(
					"Failed to construct 'IntersectionObserver': rootMargin must be specified in pixels or percent.",
					DOMExceptionNameEnum.syntaxError
				);
			}

			values.push({ value: Number(match[1]), unit: match[2] === '%' ? '%' : 'px' });
		}

		const [top = { value: 0, unit: 'px' }, right = top, bottom = top, left = right] = values;

		return [top, right, bottom, left];
	}

	/**
	 * Parses thresholds.
	 *
	 * @param threshold Threshold or list of thresholds.
	 * @returns Unique thresholds in ascending order.
	 */
	#parseThresholds(threshold: unknown): ReadonlyArray<number> {
		const window = this[PropertySymbol.window];
		const values =
			threshold !== null && typeof threshold === 'object' && Symbol.iterator in threshold
				? Array.from(<Iterable<unknown>>threshold)
				: [threshold];
		const thresholds: number[] = [];

		for (const value of values) {
			const number = Number(value);

			if (!Number.isFinite(number)) {
				throw new window.TypeError(
					"Failed to construct 'IntersectionObserver': Failed to read the 'threshold' property from 'IntersectionObserverInit': The provided double value is non-finite."
				);
			}

			if (number < 0 || number > 1) {
				throw new window.RangeError(
					"Failed to construct 'IntersectionObserver': Threshold values must be numbers between 0 and 1"
				);
			}

			if (!thresholds.includes(number)) {
				thresholds.push(number);
			}
		}

		if (!thresholds.length) {
			thresholds.push(0);
		}

		return Object.freeze(thresholds.sort((a, b) => a - b));
	}

	/**
	 * Validates a target passed to observe() or unobserve().
	 *
	 * @param methodName Method name.
	 * @param target Target.
	 * @param argumentCount Number of arguments passed to the method.
	 */
	#validateTarget(methodName: string, target: Element, argumentCount: number): void {
		const window = this[PropertySymbol.window];

		if (argumentCount === 0) {
			throw new window.TypeError(
				`Failed to execute '${methodName}' on 'IntersectionObserver': 1 argument required, but only 0 present.`
			);
		}

		if (!target || target[PropertySymbol.nodeType] !== NodeTypeEnum.elementNode) {
			throw new window.TypeError(
				`Failed to execute '${methodName}' on 'IntersectionObserver': parameter 1 is not of type 'Element'.`
			);
		}
	}

	/**
	 * Adds listeners for events that may change intersections.
	 */
	#addUpdateListeners(): void {
		const window = this[PropertySymbol.window];

		// Scroll events don't bubble, so they are captured to include scrolling of any element.
		window.addEventListener('scroll', this.#updateListener, true);
		window.addEventListener('resize', this.#updateListener);
		this.#root?.addEventListener('scroll', this.#updateListener, true);
	}

	/**
	 * Removes listeners for events that may change intersections.
	 */
	#removeUpdateListeners(): void {
		const window = this[PropertySymbol.window];

		window.removeEventListener('scroll', this.#updateListener);
		window.removeEventListener('resize', this.#updateListener);
		this.#root?.removeEventListener('scroll', this.#updateListener);
	}
}
