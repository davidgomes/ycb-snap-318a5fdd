import * as PropertySymbol from '../PropertySymbol.js';
import IntersectionObserverEntry from './IntersectionObserverEntry.js';
import type IIntersectionObserverInit from './IIntersectionObserverInit.js';
import type Element from '../nodes/element/Element.js';
import type Document from '../nodes/document/Document.js';
import type Node from '../nodes/node/Node.js';
import type ShadowRoot from '../nodes/shadow-root/ShadowRoot.js';
import type BrowserWindow from '../window/BrowserWindow.js';
import type IMutationListener from '../mutation-observer/IMutationListener.js';
import DOMRect from '../dom/DOMRect.js';
import DOMException from '../exception/DOMException.js';
import DOMExceptionNameEnum from '../exception/DOMExceptionNameEnum.js';
import NodeTypeEnum from '../nodes/node/NodeTypeEnum.js';

type IntersectionObserverCallback = (
	entries: IntersectionObserverEntry[],
	observer: IntersectionObserver
) => void;

interface IRootMarginValue {
	value: number;
	unit: 'px' | '%';
}

interface IObservationRegistration {
	previousThresholdIndex: number;
	previousIsIntersecting: boolean;
}

interface IEdges {
	top: number;
	right: number;
	bottom: number;
	left: number;
}

const ROOT_MARGIN_TOKEN_REGEXP = /^([+-]?(?:\d+\.?\d*|\.\d+)(?:e[+-]?\d+)?)(px|%)$/i;

/**
 * The IntersectionObserver interface of the Intersection Observer API provides a way to asynchronously observe changes in the intersection of a target element with an ancestor element or with a top-level document's viewport.
 *
 * Happy DOM has no rendering loop, so intersections are computed from getBoundingClientRect() and re-evaluated when targets are observed, on "scroll" and "resize" events and on DOM mutations.
 *
 * @see https://developer.mozilla.org/en-US/docs/Web/API/IntersectionObserver
 * @see https://w3c.github.io/IntersectionObserver/
 */
export default class IntersectionObserver {
	// Injected by WindowContextClassExtender
	protected declare [PropertySymbol.window]: BrowserWindow;
	#callback: IntersectionObserverCallback;
	#root: Element | Document | null;
	#rootMargin: IRootMarginValue[];
	#thresholds: ReadonlyArray<number>;
	#targets: Map<Element, IObservationRegistration> = new Map();
	#records: IntersectionObserverEntry[] = [];
	#window: BrowserWindow | null = null;
	#updateScheduled = false;
	#scheduleUpdateListener: () => void = this.#scheduleUpdate.bind(this);
	#mutationListener: IMutationListener | null = null;
	#observedDocument: Document | null = null;

	/**
	 * Constructor.
	 *
	 * @param callback Callback.
	 * @param [options] Options.
	 */
	constructor(callback: IntersectionObserverCallback, options?: IIntersectionObserverInit) {
		if (typeof callback !== 'function') {
			throw new TypeError(
				`Failed to construct 'IntersectionObserver': parameter 1 is not of type 'Function'.`
			);
		}

		if (options !== undefined && options !== null && typeof options !== 'object') {
			throw new TypeError(
				`Failed to construct 'IntersectionObserver': The provided value is not of type 'IntersectionObserverInit'.`
			);
		}

		const root = options?.root ?? null;

		if (root !== null && !this.#isElementOrDocument(root)) {
			throw new TypeError(
				`Failed to construct 'IntersectionObserver': Failed to read the 'root' property from 'IntersectionObserverInit': The provided value is not of type '(Document or Element)'.`
			);
		}

		this.#callback = callback;
		this.#root = root;
		this.#window = this[PropertySymbol.window] || root?.[PropertySymbol.window] || null;
		this.#rootMargin = this.#parseRootMargin(options?.rootMargin);
		this.#thresholds = this.#parseThresholds(options?.threshold);
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
	 * Returns root margin in the form "top right bottom left".
	 *
	 * @returns Root margin.
	 */
	public get rootMargin(): string {
		return this.#rootMargin.map((margin) => `${margin.value}${margin.unit}`).join(' ');
	}

	/**
	 * Returns thresholds sorted in increasing numeric order.
	 *
	 * @returns Thresholds.
	 */
	public get thresholds(): ReadonlyArray<number> {
		return this.#thresholds;
	}

	/**
	 * Starts observing a target element.
	 *
	 * @param target Target.
	 */
	public observe(target: Element): void {
		if (!this.#isElement(target)) {
			throw new TypeError(
				`Failed to execute 'observe' on 'IntersectionObserver': parameter 1 is not of type 'Element'.`
			);
		}

		if (this.#targets.has(target)) {
			return;
		}

		if (!this.#window) {
			this.#window = target[PropertySymbol.window] || null;
		}

		this.#targets.set(target, { previousThresholdIndex: -1, previousIsIntersecting: false });

		if (this.#targets.size === 1) {
			this.#startListening();
		}

		this.#scheduleUpdate();
	}

	/**
	 * Stops observing a target element.
	 *
	 * @param target Target.
	 */
	public unobserve(target: Element): void {
		if (!this.#isElement(target)) {
			throw new TypeError(
				`Failed to execute 'unobserve' on 'IntersectionObserver': parameter 1 is not of type 'Element'.`
			);
		}

		if (!this.#targets.delete(target)) {
			return;
		}

		this.#records = this.#records.filter((record) => record.target !== target);

		if (this.#targets.size === 0) {
			this.#stopListening();
		}
	}

	/**
	 * Stops observing all targets and discards pending records.
	 */
	public disconnect(): void {
		this.#targets.clear();
		this.#records = [];
		this.#stopListening();
	}

	/**
	 * Returns the pending IntersectionObserverEntry objects and empties the queue, so that they won't be delivered to the callback.
	 *
	 * @returns Records.
	 */
	public takeRecords(): IntersectionObserverEntry[] {
		this.#updateObservations();
		const records = this.#records;
		this.#records = [];
		return records;
	}

	/**
	 * Schedules an asynchronous update followed by delivery of queued records.
	 */
	#scheduleUpdate(): void {
		if (this.#updateScheduled || !this.#window) {
			return;
		}

		this.#updateScheduled = true;

		this.#window.queueMicrotask(() => {
			this.#updateScheduled = false;
			this.#updateObservations();

			if (this.#records.length === 0) {
				return;
			}

			const records = this.#records;
			this.#records = [];
			this.#callback.call(this, records, this);
		});
	}

	/**
	 * Runs the "update intersection observations" steps for all targets, in observation order.
	 *
	 * @see https://w3c.github.io/IntersectionObserver/#update-intersection-observations-algo
	 */
	#updateObservations(): void {
		if (this.#targets.size === 0) {
			return;
		}

		const window = this.#window;
		const time = window ? window.performance.now() : 0;
		const rootBounds = this.#getRootIntersectionRect();

		for (const [target, registration] of this.#targets) {
			let thresholdIndex = 0;
			let isIntersecting = false;
			let intersectionRatio = 0;
			let targetEdges: IEdges = { top: 0, right: 0, bottom: 0, left: 0 };
			let intersectionEdges: IEdges = { top: 0, right: 0, bottom: 0, left: 0 };

			if (this.#isTargetInRootScope(target)) {
				targetEdges = this.#getEdges(target.getBoundingClientRect());

				const top = Math.max(targetEdges.top, rootBounds.top);
				const right = Math.min(targetEdges.right, rootBounds.right);
				const bottom = Math.min(targetEdges.bottom, rootBounds.bottom);
				const left = Math.max(targetEdges.left, rootBounds.left);

				// Edge-adjacent rectangles are intersecting, even when the intersection has zero area.
				isIntersecting = right >= left && bottom >= top;

				if (isIntersecting) {
					intersectionEdges = { top, right, bottom, left };
				}

				const targetArea =
					(targetEdges.right - targetEdges.left) * (targetEdges.bottom - targetEdges.top);
				const intersectionArea =
					(intersectionEdges.right - intersectionEdges.left) *
					(intersectionEdges.bottom - intersectionEdges.top);

				if (targetArea > 0) {
					intersectionRatio = Math.min(1, intersectionArea / targetArea);
				} else {
					intersectionRatio = isIntersecting ? 1 : 0;
				}

				thresholdIndex = this.#thresholds.findIndex((threshold) => threshold > intersectionRatio);

				if (thresholdIndex === -1) {
					thresholdIndex = this.#thresholds.length;
				}
			}

			if (
				thresholdIndex !== registration.previousThresholdIndex ||
				isIntersecting !== registration.previousIsIntersecting
			) {
				this.#records.push(
					new IntersectionObserverEntry({
						time,
						rootBounds: this.#createDOMRect(rootBounds),
						boundingClientRect: this.#createDOMRect(targetEdges),
						intersectionRect: this.#createDOMRect(intersectionEdges),
						intersectionRatio,
						isIntersecting,
						target
					})
				);
			}

			registration.previousThresholdIndex = thresholdIndex;
			registration.previousIsIntersecting = isIntersecting;
		}
	}

	/**
	 * Returns the root intersection rectangle, expanded by the root margin.
	 *
	 * @see https://w3c.github.io/IntersectionObserver/#intersectionobserver-root-intersection-rectangle
	 * @returns Edges.
	 */
	#getRootIntersectionRect(): IEdges {
		let edges: IEdges;

		if (this.#root && this.#root[PropertySymbol.nodeType] === NodeTypeEnum.elementNode) {
			edges = this.#getEdges((<Element>this.#root).getBoundingClientRect());
		} else {
			const window = this.#root ? this.#root[PropertySymbol.window] : this.#window;
			edges = {
				top: 0,
				left: 0,
				right: window ? window.innerWidth : 0,
				bottom: window ? window.innerHeight : 0
			};
		}

		const width = edges.right - edges.left;
		const height = edges.bottom - edges.top;
		const [top, right, bottom, left] = this.#rootMargin.map((margin, index) =>
			margin.unit === '%' ? (margin.value / 100) * (index % 2 === 0 ? height : width) : margin.value
		);

		return {
			top: edges.top - top,
			right: edges.right + right,
			bottom: edges.bottom + bottom,
			left: edges.left - left
		};
	}

	/**
	 * Returns "true" if the target can intersect with the root.
	 *
	 * An explicit root element must be an ancestor of the target in the containing block chain, and an explicit root document must be the target's document.
	 *
	 * @param target Target.
	 * @returns "true" if in scope.
	 */
	#isTargetInRootScope(target: Element): boolean {
		const root = this.#root;

		if (!root) {
			return true;
		}

		if (root[PropertySymbol.nodeType] === NodeTypeEnum.documentNode) {
			return target[PropertySymbol.ownerDocument] === root;
		}

		let node: Node | null = target;

		while (node) {
			if (node[PropertySymbol.parentNode]) {
				node = node[PropertySymbol.parentNode];
			} else if (node[PropertySymbol.nodeType] === NodeTypeEnum.documentFragmentNode) {
				node = (<ShadowRoot>node).host || null;
			} else {
				node = null;
			}

			if (node === root) {
				return true;
			}
		}

		return false;
	}

	/**
	 * Returns the edges of a rect. Supports plain objects, as getBoundingClientRect() is commonly mocked.
	 *
	 * @param rect Rect.
	 * @returns Edges.
	 */
	#getEdges(rect: Partial<DOMRect> | null | undefined): IEdges {
		const x = this.#toFiniteNumber(rect?.left ?? rect?.x);
		const y = this.#toFiniteNumber(rect?.top ?? rect?.y);
		const width = this.#toFiniteNumber(rect?.width);
		const height = this.#toFiniteNumber(rect?.height);
		const right = rect?.right !== undefined ? this.#toFiniteNumber(rect.right) : x + width;
		const bottom = rect?.bottom !== undefined ? this.#toFiniteNumber(rect.bottom) : y + height;

		return {
			top: Math.min(y, bottom),
			right: Math.max(x, right),
			bottom: Math.max(y, bottom),
			left: Math.min(x, right)
		};
	}

	/**
	 * Creates a DOMRect from edges.
	 *
	 * @param edges Edges.
	 * @returns DOMRect.
	 */
	#createDOMRect(edges: IEdges): DOMRect {
		return new DOMRect(edges.left, edges.top, edges.right - edges.left, edges.bottom - edges.top);
	}

	/**
	 * Converts a value to a finite number, falling back to 0.
	 *
	 * @param value Value.
	 * @returns Number.
	 */
	#toFiniteNumber(value: unknown): number {
		const number = Number(value);
		return Number.isFinite(number) ? number : 0;
	}

	/**
	 * Starts listening for changes that may affect intersections.
	 */
	#startListening(): void {
		const window = this.#window;

		if (!window || window.closed) {
			return;
		}

		window.addEventListener('scroll', this.#scheduleUpdateListener, { capture: true });
		window.addEventListener('resize', this.#scheduleUpdateListener);

		this.#observedDocument = window.document;
		this.#mutationListener = {
			options: { childList: true, attributes: true, characterData: true, subtree: true },
			callback: new WeakRef(this.#scheduleUpdateListener)
		};
		this.#observedDocument[PropertySymbol.observeMutations](this.#mutationListener);
	}

	/**
	 * Stops listening for changes that may affect intersections.
	 */
	#stopListening(): void {
		if (this.#window) {
			this.#window.removeEventListener('scroll', this.#scheduleUpdateListener);
			this.#window.removeEventListener('resize', this.#scheduleUpdateListener);
		}

		if (this.#observedDocument && this.#mutationListener) {
			this.#observedDocument[PropertySymbol.unobserveMutations](this.#mutationListener);
		}

		this.#observedDocument = null;
		this.#mutationListener = null;
	}

	/**
	 * Parses root margin.
	 *
	 * @see https://w3c.github.io/IntersectionObserver/#parse-a-margin
	 * @param rootMargin Root margin.
	 * @returns Margins in the order top, right, bottom, left.
	 */
	#parseRootMargin(rootMargin: unknown): IRootMarginValue[] {
		const tokens = rootMargin === undefined ? [] : String(rootMargin).trim().split(/\s+/);
		const values: IRootMarginValue[] = [];

		if (tokens.length === 1 && tokens[0] === '') {
			tokens.pop();
		}

		if (tokens.length > 4) {
			throw this.#createRootMarginError();
		}

		for (const token of tokens) {
			const match = token.match(ROOT_MARGIN_TOKEN_REGEXP);

			if (!match) {
				throw this.#createRootMarginError();
			}

			values.push({
				value: Number(match[1]) || 0,
				unit: match[2] === '%' ? '%' : 'px'
			});
		}

		switch (values.length) {
			case 0:
				return [0, 1, 2, 3].map(() => ({ value: 0, unit: 'px' }));
			case 1:
				return [values[0], values[0], values[0], values[0]];
			case 2:
				return [values[0], values[1], values[0], values[1]];
			case 3:
				return [values[0], values[1], values[2], values[1]];
			default:
				return values;
		}
	}

	/**
	 * Creates the error thrown for an invalid root margin.
	 *
	 * @returns Error.
	 */
	#createRootMarginError(): DOMException {
		const DOMExceptionClass = this.#window?.DOMException || DOMException;
		return new DOMExceptionClass(
			`Failed to construct 'IntersectionObserver': rootMargin must be specified in pixels or percent.`,
			DOMExceptionNameEnum.syntaxError
		);
	}

	/**
	 * Parses thresholds.
	 *
	 * @param threshold Threshold.
	 * @returns Sorted unique thresholds.
	 */
	#parseThresholds(threshold: unknown): ReadonlyArray<number> {
		let values: unknown[];

		if (threshold === undefined) {
			values = [0];
		} else if (typeof threshold === 'object' && threshold !== null) {
			if (typeof (<Iterable<unknown>>threshold)[Symbol.iterator] !== 'function') {
				throw new TypeError(
					`Failed to construct 'IntersectionObserver': Failed to read the 'threshold' property from 'IntersectionObserverInit': The provided value cannot be converted to a sequence.`
				);
			}
			values = Array.from(<Iterable<unknown>>threshold);
		} else {
			values = [threshold];
		}

		const thresholds: number[] = [];

		for (const value of values) {
			const number = Number(value);

			if (!Number.isFinite(number)) {
				throw new TypeError(
					`Failed to construct 'IntersectionObserver': Failed to read the 'threshold' property from 'IntersectionObserverInit': The provided double value is non-finite.`
				);
			}

			if (number < 0 || number > 1) {
				throw new RangeError(
					`Failed to construct 'IntersectionObserver': Threshold values must be numbers between 0 and 1`
				);
			}

			// Normalizes -0 to 0.
			const normalized = number + 0;

			if (!thresholds.includes(normalized)) {
				thresholds.push(normalized);
			}
		}

		if (thresholds.length === 0) {
			thresholds.push(0);
		}

		return Object.freeze(thresholds.sort((a, b) => a - b));
	}

	/**
	 * Returns "true" if the value is an element.
	 *
	 * @param value Value.
	 * @returns "true" if element.
	 */
	#isElement(value: unknown): value is Element {
		return (
			typeof value === 'object' &&
			value !== null &&
			(<Node>value)[PropertySymbol.nodeType] === NodeTypeEnum.elementNode
		);
	}

	/**
	 * Returns "true" if the value is an element or a document.
	 *
	 * @param value Value.
	 * @returns "true" if element or document.
	 */
	#isElementOrDocument(value: unknown): value is Element | Document {
		return (
			this.#isElement(value) ||
			(typeof value === 'object' &&
				value !== null &&
				(<Node>value)[PropertySymbol.nodeType] === NodeTypeEnum.documentNode)
		);
	}
}
