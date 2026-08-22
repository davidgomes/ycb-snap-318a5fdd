import * as PropertySymbol from '../PropertySymbol.js';
import DOMRect from '../dom/DOMRect.js';
import NodeTypeEnum from '../nodes/node/NodeTypeEnum.js';
import type Element from '../nodes/element/Element.js';
import type HTMLElement from '../nodes/html-element/HTMLElement.js';
import type Document from '../nodes/document/Document.js';
import type BrowserWindow from '../window/BrowserWindow.js';
import IntersectionObserverEntry from './IntersectionObserverEntry.js';
import type IIntersectionObserverInit from './IIntersectionObserverInit.js';

interface IRect {
	x: number;
	y: number;
	width: number;
	height: number;
	top: number;
	right: number;
	bottom: number;
	left: number;
}

interface IRootMargin {
	top: { value: number; unit: 'px' | '%' };
	right: { value: number; unit: 'px' | '%' };
	bottom: { value: number; unit: 'px' | '%' };
	left: { value: number; unit: 'px' | '%' };
	serialized: string;
}

interface IObservation {
	target: Element;
	previousThresholdIndex: number;
}

const ROOT_MARGIN_VALUE = /^([+-]?(?:\d+\.?\d*|\.\d+))(px|%)$/;

/**
 * The IntersectionObserver interface of the Intersection Observer API provides a way to asynchronously observe changes in the intersection of a target element with an ancestor element or with a top-level document's viewport.
 *
 * @see https://developer.mozilla.org/en-US/docs/Web/API/IntersectionObserver
 */
export default class IntersectionObserver {
	// Injected by WindowContextClassExtender
	protected declare [PropertySymbol.window]: BrowserWindow;

	#callback: (entries: IntersectionObserverEntry[], observer: IntersectionObserver) => void;
	#root: Element | Document | null;
	#rootMargin: IRootMargin;
	#thresholds: number[];
	#observations: IObservation[] = [];
	#queued: IntersectionObserverEntry[] = [];
	#deliveryScheduled = false;

	/**
	 * Constructor.
	 *
	 * @param callback Callback.
	 * @param options Options.
	 */
	constructor(
		callback: (entries: IntersectionObserverEntry[], observer: IntersectionObserver) => void,
		options?: IIntersectionObserverInit
	) {
		const window = this[PropertySymbol.window];

		if (typeof callback !== 'function') {
			throw new (window?.TypeError ?? TypeError)(
				`Failed to construct 'IntersectionObserver': The callback provided as parameter 1 is not a function.`
			);
		}

		const init = options ?? {};
		const root = init.root ?? null;

		if (root !== null && !this.#isValidRoot(root)) {
			throw new (window?.TypeError ?? TypeError)(
				`Failed to construct 'IntersectionObserver': Failed to read the 'root' property from 'IntersectionObserverInit': The provided value is not of type '(Element or Document or null)'.`
			);
		}

		this.#callback = callback;
		this.#root = root;
		this.#rootMargin = this.#parseRootMargin(init.rootMargin ?? '0px');
		this.#thresholds = this.#parseThresholds(init.threshold);
	}

	/**
	 * Intersection root, or null for the implicit viewport.
	 *
	 * @returns Root.
	 */
	public get root(): Element | Document | null {
		return this.#root;
	}

	/**
	 * Normalized four-value root margin string (top right bottom left).
	 *
	 * @returns Root margin.
	 */
	public get rootMargin(): string {
		return this.#rootMargin.serialized;
	}

	/**
	 * Sorted unique thresholds.
	 *
	 * @returns Thresholds.
	 */
	public get thresholds(): number[] {
		return this.#thresholds.slice();
	}

	/**
	 * Starts observing a target.
	 *
	 * @param target Target.
	 */
	public observe(target: Element): void {
		const window = this.#getWindow(target);

		if (target === undefined || target === null || !this.#isElement(target)) {
			throw new (window?.TypeError ?? TypeError)(
				`Failed to execute 'observe' on 'IntersectionObserver': parameter 1 is not of type 'Element'.`
			);
		}

		for (const observation of this.#observations) {
			if (observation.target === target) {
				return;
			}
		}

		const observation: IObservation = {
			target,
			previousThresholdIndex: -1
		};

		this.#observations.push(observation);
		this.#queueIfThresholdChanged(observation);
		this.#scheduleDelivery();
	}

	/**
	 * Disconnects all targets and clears pending records.
	 */
	public disconnect(): void {
		this.#observations = [];
		this.#queued = [];
		this.#deliveryScheduled = false;
	}

	/**
	 * Unobserves an element.
	 *
	 * @param target Target.
	 */
	public unobserve(target: Element): void {
		const window = this.#getWindow(target);

		if (target === undefined || target === null || !this.#isElement(target)) {
			throw new (window?.TypeError ?? TypeError)(
				`Failed to execute 'unobserve' on 'IntersectionObserver': parameter 1 is not of type 'Element'.`
			);
		}

		const index = this.#observations.findIndex((observation) => observation.target === target);

		if (index === -1) {
			return;
		}

		this.#observations.splice(index, 1);
		this.#queued = this.#queued.filter((entry) => entry.target !== target);
	}

	/**
	 * Returns queued records and empties the queue.
	 *
	 * @returns Records.
	 */
	public takeRecords(): IntersectionObserverEntry[] {
		this.#checkObservations();
		const records = this.#queued;
		this.#queued = [];
		return records;
	}

	/**
	 * Returns the window associated with this observer or a related node.
	 *
	 * @param node Optional node.
	 * @returns Window.
	 */
	#getWindow(node?: Element | Document | null): BrowserWindow | undefined {
		return (
			this[PropertySymbol.window] ??
			(<Element | undefined>node)?.[PropertySymbol.window] ??
			undefined
		);
	}

	/**
	 * Returns true if the value is an Element.
	 *
	 * @param value Value.
	 * @returns True if element.
	 */
	#isElement(value: unknown): value is Element {
		return (
			typeof value === 'object' &&
			value !== null &&
			(<Element>value)[PropertySymbol.nodeType] === NodeTypeEnum.elementNode
		);
	}

	/**
	 * Returns true if the value is a valid observer root.
	 *
	 * @param value Value.
	 * @returns True if valid root.
	 */
	#isValidRoot(value: unknown): value is Element | Document {
		if (typeof value !== 'object' || value === null) {
			return false;
		}

		const nodeType = (<Element>value)[PropertySymbol.nodeType];
		return nodeType === NodeTypeEnum.elementNode || nodeType === NodeTypeEnum.documentNode;
	}

	/**
	 * Parses rootMargin using CSS margin shorthand.
	 *
	 * @param rootMargin Root margin.
	 * @returns Parsed margin.
	 */
	#parseRootMargin(rootMargin: string): IRootMargin {
		const window = this[PropertySymbol.window];
		const tokens = String(rootMargin).trim().split(/\s+/).filter(Boolean);

		if (tokens.length < 1 || tokens.length > 4) {
			throw new (window?.SyntaxError ?? SyntaxError)(
				`Failed to construct 'IntersectionObserver': rootMargin must be specified in pixels or percent.`
			);
		}

		const parsed = tokens.map((token) => {
			const match = token.match(ROOT_MARGIN_VALUE);
			if (!match) {
				throw new (window?.SyntaxError ?? SyntaxError)(
					`Failed to construct 'IntersectionObserver': rootMargin must be specified in pixels or percent.`
				);
			}
			return { value: Number(match[1]), unit: <'px' | '%'>match[2] };
		});

		const top = parsed[0];
		const right = parsed[1] ?? top;
		const bottom = parsed[2] ?? top;
		const left = parsed[3] ?? right;

		const serialize = (part: { value: number; unit: 'px' | '%' }): string =>
			`${part.value}${part.unit}`;

		return {
			top,
			right,
			bottom,
			left,
			serialized: `${serialize(top)} ${serialize(right)} ${serialize(bottom)} ${serialize(left)}`
		};
	}

	/**
	 * Parses and normalizes thresholds.
	 *
	 * @param threshold Threshold option.
	 * @returns Sorted unique thresholds.
	 */
	#parseThresholds(threshold: number | number[] | undefined): number[] {
		const window = this[PropertySymbol.window];
		const values =
			threshold === undefined ? [0] : Array.isArray(threshold) ? threshold : [threshold];
		const normalized: number[] = [];

		if (values.length === 0) {
			return [0];
		}

		for (const value of values) {
			const number = Number(value);
			if (!Number.isFinite(number)) {
				throw new (window?.TypeError ?? TypeError)(
					`Failed to construct 'IntersectionObserver': Threshold values must be numbers between 0 and 1.`
				);
			}
			if (number < 0 || number > 1) {
				throw new (window?.RangeError ?? RangeError)(
					`Failed to construct 'IntersectionObserver': Threshold values must be between 0 and 1.`
				);
			}
			normalized.push(number);
		}

		normalized.sort((a, b) => a - b);

		const unique: number[] = [];
		for (const value of normalized) {
			if (unique.length === 0 || unique[unique.length - 1] !== value) {
				unique.push(value);
			}
		}

		return unique;
	}

	/**
	 * Schedules asynchronous callback delivery.
	 */
	#scheduleDelivery(): void {
		if (this.#deliveryScheduled || this.#queued.length === 0) {
			return;
		}

		this.#deliveryScheduled = true;

		const window = this.#getWindow(<Element | null>this.#root) ?? this.#getWindow();
		const schedule = window?.queueMicrotask?.bind(window) ?? queueMicrotask;

		schedule(() => {
			this.#deliveryScheduled = false;

			if (this.#queued.length === 0) {
				return;
			}

			const records = this.#queued;
			this.#queued = [];
			this.#callback(records, this);
		});
	}

	/**
	 * Recomputes intersections for all observed targets.
	 */
	#checkObservations(): void {
		for (const observation of this.#observations) {
			this.#queueIfThresholdChanged(observation);
		}
	}

	/**
	 * Queues an entry when the threshold index changes.
	 *
	 * @param observation Observation.
	 */
	#queueIfThresholdChanged(observation: IObservation): void {
		const entry = this.#createEntry(observation.target);
		const thresholdIndex = this.#thresholdIndex(entry.intersectionRatio);

		if (thresholdIndex === observation.previousThresholdIndex) {
			return;
		}

		observation.previousThresholdIndex = thresholdIndex;
		this.#queued.push(entry);
	}

	/**
	 * Returns the highest threshold index that the ratio has crossed.
	 *
	 * @param ratio Intersection ratio.
	 * @returns Threshold index.
	 */
	#thresholdIndex(ratio: number): number {
		let index = -1;
		for (let i = 0; i < this.#thresholds.length; i++) {
			if (ratio >= this.#thresholds[i]) {
				index = i;
			}
		}
		return index;
	}

	/**
	 * Creates an intersection entry for a target.
	 *
	 * @param target Target.
	 * @returns Entry.
	 */
	#createEntry(target: Element): IntersectionObserverEntry {
		const window = this.#getWindow(target);
		const boundingClientRect = this.#readRect(target);
		const rootBounds = this.#getExpandedRootBounds(window);
		const intersection = this.#intersect(boundingClientRect, rootBounds);
		const targetArea =
			Math.max(0, boundingClientRect.width) * Math.max(0, boundingClientRect.height);
		const intersectionArea = intersection.width * intersection.height;
		const zeroAreaTarget = targetArea === 0;
		const contained = this.#isContained(boundingClientRect, rootBounds);

		let intersectionRatio: number;
		if (zeroAreaTarget) {
			intersectionRatio = contained ? 1 : 0;
		} else {
			intersectionRatio = intersectionArea / targetArea;
		}

		const isIntersecting = zeroAreaTarget ? contained : intersectionArea > 0;

		return new IntersectionObserverEntry({
			boundingClientRect: this.#toDOMRect(boundingClientRect),
			intersectionRatio,
			intersectionRect: this.#toDOMRect(intersection),
			isIntersecting,
			rootBounds: this.#toDOMRect(rootBounds),
			target,
			time: window?.performance?.now?.() ?? 0
		});
	}

	/**
	 * Returns expanded intersection root bounds.
	 *
	 * @param window Window.
	 * @returns Root bounds.
	 */
	#getExpandedRootBounds(window?: BrowserWindow): IRect {
		const rootRect = this.#getRootRect(window);
		const width = rootRect.width;
		const height = rootRect.height;
		const top = this.#marginToPixels(this.#rootMargin.top, height);
		const right = this.#marginToPixels(this.#rootMargin.right, width);
		const bottom = this.#marginToPixels(this.#rootMargin.bottom, height);
		const left = this.#marginToPixels(this.#rootMargin.left, width);

		return this.#normalizeRect({
			x: rootRect.x - left,
			y: rootRect.y - top,
			width: rootRect.width + left + right,
			height: rootRect.height + top + bottom
		});
	}

	/**
	 * Returns unexpanded root rect.
	 *
	 * @param window Window.
	 * @returns Root rect.
	 */
	#getRootRect(window?: BrowserWindow): IRect {
		if (this.#root && this.#isElement(this.#root)) {
			return this.#readRect(this.#root);
		}

		const width = window?.innerWidth ?? 0;
		const height = window?.innerHeight ?? 0;

		return this.#normalizeRect({
			x: 0,
			y: 0,
			width,
			height
		});
	}

	/**
	 * Reads an element's box.
	 *
	 * @param element Element.
	 * @returns Rect.
	 */
	#readRect(element: Element): IRect {
		const bounding = element.getBoundingClientRect();
		const fromBounding = this.#normalizeRect(bounding);

		if (
			fromBounding.width !== 0 ||
			fromBounding.height !== 0 ||
			fromBounding.x !== 0 ||
			fromBounding.y !== 0
		) {
			return fromBounding;
		}

		const htmlElement = <HTMLElement>element;
		const offsetWidth = htmlElement.offsetWidth ?? 0;
		const offsetHeight = htmlElement.offsetHeight ?? 0;
		const offsetLeft = htmlElement.offsetLeft ?? 0;
		const offsetTop = htmlElement.offsetTop ?? 0;

		if (offsetWidth || offsetHeight || offsetLeft || offsetTop) {
			return this.#normalizeRect({
				x: offsetLeft,
				y: offsetTop,
				width: offsetWidth,
				height: offsetHeight
			});
		}

		return fromBounding;
	}

	/**
	 * Converts a margin token to pixels.
	 *
	 * @param margin Margin.
	 * @param margin.value
	 * @param reference Size used for percent units.
	 * @param margin.unit
	 * @returns Pixels.
	 */
	#marginToPixels(margin: { value: number; unit: 'px' | '%' }, reference: number): number {
		if (margin.unit === '%') {
			return (margin.value / 100) * reference;
		}
		return margin.value;
	}

	/**
	 * Intersects two rects.
	 *
	 * @param a First rect.
	 * @param b Second rect.
	 * @returns Intersection rect.
	 */
	#intersect(a: IRect, b: IRect): IRect {
		const left = Math.max(a.left, b.left);
		const top = Math.max(a.top, b.top);
		const right = Math.min(a.right, b.right);
		const bottom = Math.min(a.bottom, b.bottom);
		const width = Math.max(0, right - left);
		const height = Math.max(0, bottom - top);

		return this.#normalizeRect({
			x: width === 0 && height === 0 ? 0 : left,
			y: width === 0 && height === 0 ? 0 : top,
			width,
			height
		});
	}

	/**
	 * Returns true if a (possibly zero-area) target is contained by root.
	 *
	 * @param target Target rect.
	 * @param root Root rect.
	 * @returns True if contained.
	 */
	#isContained(target: IRect, root: IRect): boolean {
		return (
			target.left >= root.left &&
			target.top >= root.top &&
			target.right <= root.right &&
			target.bottom <= root.bottom
		);
	}

	/**
	 * Normalizes a rect-like object.
	 *
	 * @param rect Rect.
	 * @param rect.x
	 * @param rect.y
	 * @param rect.width
	 * @param rect.height
	 * @param rect.top
	 * @param rect.right
	 * @param rect.bottom
	 * @param rect.left
	 * @returns Normalized rect.
	 */
	#normalizeRect(rect: {
		x?: number;
		y?: number;
		width?: number;
		height?: number;
		top?: number;
		right?: number;
		bottom?: number;
		left?: number;
	}): IRect {
		const width = Number(rect.width) || 0;
		const height = Number(rect.height) || 0;
		const x =
			rect.x !== undefined && rect.x !== null
				? Number(rect.x)
				: rect.left !== undefined && rect.left !== null
					? Number(rect.left)
					: 0;
		const y =
			rect.y !== undefined && rect.y !== null
				? Number(rect.y)
				: rect.top !== undefined && rect.top !== null
					? Number(rect.top)
					: 0;

		return {
			x,
			y,
			width,
			height,
			top: Math.min(y, y + height),
			right: Math.max(x, x + width),
			bottom: Math.max(y, y + height),
			left: Math.min(x, x + width)
		};
	}

	/**
	 * Creates a DOMRect snapshot.
	 *
	 * @param rect Rect.
	 * @returns DOMRect.
	 */
	#toDOMRect(rect: IRect): DOMRect {
		return new DOMRect(rect.x, rect.y, rect.width, rect.height);
	}
}
