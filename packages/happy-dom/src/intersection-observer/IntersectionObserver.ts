import type Element from '../nodes/element/Element.js';
import type BrowserWindow from '../window/BrowserWindow.js';
import * as PropertySymbol from '../PropertySymbol.js';
import NodeTypeEnum from '../nodes/node/NodeTypeEnum.js';
import DOMRect from '../dom/DOMRect.js';
import IntersectionObserverEntry from './IntersectionObserverEntry.js';
import type IIntersectionObserverInit from './IIntersectionObserverInit.js';
import { subscribeIntersectionObserver } from './IntersectionObserverRegistry.js';

interface IRootMarginEdge {
	value: number;
	unit: 'px' | '%';
}

interface IParsedRootMargin {
	top: IRootMarginEdge;
	right: IRootMarginEdge;
	bottom: IRootMarginEdge;
	left: IRootMarginEdge;
	normalized: string;
}

interface IBox {
	left: number;
	top: number;
	right: number;
	bottom: number;
}

interface IObservation {
	target: Element;
	lastThresholdIndex: number | null;
}

const DEFAULT_MARGIN: IRootMarginEdge = { value: 0, unit: 'px' };
const ROOT_MARGIN_PATTERN = /^(-?(?:\d+|\d*\.\d+))(px|%)$/;

/**
 * The IntersectionObserver interface of the Intersection Observer API provides a way to asynchronously observe changes in the intersection of a target element with an ancestor element or with a top-level document's viewport.
 *
 * @see https://developer.mozilla.org/en-US/docs/Web/API/IntersectionObserver
 */
export default class IntersectionObserver {
	// Injected by WindowContextClassExtender
	protected declare [PropertySymbol.window]: BrowserWindow;

	#callback: (entries: IntersectionObserverEntry[], observer: IntersectionObserver) => void;
	#root: Element | null;
	#rootMargin: string;
	#margins: IParsedRootMargin;
	#thresholds: number[];
	#observations: IObservation[] = [];
	#pending: IntersectionObserverEntry[] = [];
	#scheduled = false;
	#unsubscribe: (() => void) | null = null;

	/**
	 * Constructor.
	 *
	 * @param callback Callback.
	 * @param options Options.
	 */
	constructor(
		callback: (entries: IntersectionObserverEntry[], observer: IntersectionObserver) => void,
		options?: IIntersectionObserverInit | null
	) {
		const typeError = this.#typeError();

		if (typeof callback !== 'function') {
			throw new typeError(
				"Failed to construct 'IntersectionObserver': The callback provided as parameter 1 is not a function."
			);
		}

		const init = options ?? {};
		if (typeof init !== 'object') {
			throw new typeError(
				"Failed to construct 'IntersectionObserver': The provided value is not of type 'IntersectionObserverInit'."
			);
		}

		this.#callback = callback;
		this.#root = this.#parseRoot(init.root);
		this.#margins = this.#parseRootMargin(init.rootMargin);
		this.#rootMargin = this.#margins.normalized;
		this.#thresholds = this.#parseThresholds(init.threshold);
	}

	/**
	 * Returns the root element, or null when the viewport is the root.
	 *
	 * @returns Root.
	 */
	public get root(): Element | null {
		return this.#root;
	}

	/**
	 * Returns the normalized root margin (top right bottom left).
	 *
	 * @returns Root margin.
	 */
	public get rootMargin(): string {
		return this.#rootMargin;
	}

	/**
	 * Returns the normalized, sorted, unique thresholds.
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
		if (!this.#isElement(target)) {
			throw new this.#typeError()(
				"Failed to execute 'observe' on 'IntersectionObserver': parameter 1 is not of type 'Element'."
			);
		}

		if (this.#observations.some((observation) => observation.target === target)) {
			return;
		}

		const observation: IObservation = {
			target,
			lastThresholdIndex: null
		};
		this.#observations.push(observation);
		this.#queueObservation(observation);
		this.#ensureSubscribed();
		this.#schedule();
	}

	/**
	 * Stops observing a target.
	 *
	 * @param target Target.
	 */
	public unobserve(target: Element): void {
		if (!this.#isElement(target)) {
			throw new this.#typeError()(
				"Failed to execute 'unobserve' on 'IntersectionObserver': parameter 1 is not of type 'Element'."
			);
		}

		const index = this.#observations.findIndex((observation) => observation.target === target);
		if (index === -1) {
			return;
		}

		this.#observations.splice(index, 1);
		this.#pending = this.#pending.filter((entry) => entry.target !== target);

		if (this.#observations.length === 0) {
			this.#unsubscribeFromDocument();
		}
	}

	/**
	 * Stops observing every target and drops records that have not been delivered.
	 */
	public disconnect(): void {
		this.#observations = [];
		this.#pending = [];
		this.#unsubscribeFromDocument();
	}

	/**
	 * Returns queued entries and clears them so they are not delivered to the callback.
	 *
	 * @returns Records.
	 */
	public takeRecords(): IntersectionObserverEntry[] {
		const records = this.#pending;
		this.#pending = [];
		return records;
	}

	/**
	 * Recomputes every target and queues entries whose threshold index changed.
	 */
	#refresh(): void {
		if (this.#observations.length === 0) {
			return;
		}

		let queued = false;
		for (const observation of this.#observations) {
			if (this.#queueObservation(observation)) {
				queued = true;
			}
		}

		if (queued) {
			this.#schedule();
		}
	}

	/**
	 * Queues an entry when the target is new or its threshold index changed.
	 *
	 * @param observation Observation.
	 * @returns True when an entry was queued.
	 */
	#queueObservation(observation: IObservation): boolean {
		const entry = this.#createEntry(observation.target);
		const thresholdIndex = this.#thresholdIndex(entry.intersectionRatio, entry.isIntersecting);

		if (
			observation.lastThresholdIndex !== null &&
			observation.lastThresholdIndex === thresholdIndex
		) {
			return false;
		}

		observation.lastThresholdIndex = thresholdIndex;
		this.#pending.push(entry);
		return true;
	}

	/**
	 * Schedules asynchronous delivery of pending records.
	 */
	#schedule(): void {
		if (this.#scheduled) {
			return;
		}

		this.#scheduled = true;
		const deliver = (): void => {
			this.#scheduled = false;
			if (this.#pending.length === 0 || this.#observations.length === 0) {
				if (this.#observations.length === 0) {
					this.#pending = [];
				}
				return;
			}

			const order = new Map<Element, number>();
			for (let i = 0, max = this.#observations.length; i < max; i++) {
				order.set(this.#observations[i].target, i);
			}

			const records = this.#pending;
			this.#pending = [];
			records.sort((left, right) => {
				return (order.get(<Element>left.target) ?? 0) - (order.get(<Element>right.target) ?? 0);
			});
			this.#callback(records, this);
		};

		const window = this[PropertySymbol.window];
		if (window) {
			window.queueMicrotask(deliver);
		} else {
			queueMicrotask(deliver);
		}
	}

	/**
	 * Builds an intersection entry from current geometry.
	 *
	 * @param target Target.
	 * @returns Entry.
	 */
	#createEntry(target: Element): IntersectionObserverEntry {
		const targetBox = this.#elementBox(target);
		const rootBox = this.#rootBox();
		const targetArea = this.#area(targetBox);
		const intersection = this.#intersect(targetBox, rootBox);
		let ratio = 0;
		let isIntersecting = false;

		if (targetArea === 0) {
			isIntersecting = this.#contains(rootBox, targetBox);
			ratio = isIntersecting ? 1 : 0;
		} else if (intersection && this.#area(intersection) > 0) {
			ratio = Math.min(1, this.#area(intersection) / targetArea);
			isIntersecting = ratio > 0;
		}

		const DOMRectConstructor = this[PropertySymbol.window]?.DOMRect ?? DOMRect;
		const intersectionBox = isIntersecting ? (intersection ?? targetBox) : null;

		return new IntersectionObserverEntry({
			time: this.#now(),
			target,
			boundingClientRect: this.#toDOMRect(DOMRectConstructor, targetBox),
			intersectionRect: this.#toDOMRect(DOMRectConstructor, intersectionBox),
			intersectionRatio: ratio,
			isIntersecting,
			rootBounds: this.#toDOMRect(DOMRectConstructor, rootBox)
		});
	}

	/**
	 * Returns the expanded root rectangle.
	 *
	 * @returns Root box.
	 */
	#rootBox(): IBox {
		const base = this.#root ? this.#elementBox(this.#root) : this.#viewportBox();
		const width = Math.max(0, base.right - base.left);
		const height = Math.max(0, base.bottom - base.top);
		const top = this.#marginLength(this.#margins.top, height);
		const right = this.#marginLength(this.#margins.right, width);
		const bottom = this.#marginLength(this.#margins.bottom, height);
		const left = this.#marginLength(this.#margins.left, width);

		return {
			left: base.left - left,
			top: base.top - top,
			right: base.right + right,
			bottom: base.bottom + bottom
		};
	}

	/**
	 * Returns the viewport rectangle.
	 *
	 * @returns Viewport box.
	 */
	#viewportBox(): IBox {
		const window = this[PropertySymbol.window];
		const width = window ? window.innerWidth : 1024;
		const height = window ? window.innerHeight : 768;
		return { left: 0, top: 0, right: width, bottom: height };
	}

	/**
	 * Reads an element's border box.
	 *
	 * @param element Element.
	 * @returns Box.
	 */
	#elementBox(element: Element): IBox {
		const rect = element.getBoundingClientRect();
		const left = rect.left;
		const top = rect.top;
		const right = rect.right;
		const bottom = rect.bottom;

		if (
			[left, top, right, bottom].every((value) => typeof value === 'number' && !Number.isNaN(value))
		) {
			return { left, top, right, bottom };
		}

		const x = rect.x ?? 0;
		const y = rect.y ?? 0;
		const width = rect.width ?? 0;
		const height = rect.height ?? 0;
		return {
			left: Math.min(x, x + width),
			top: Math.min(y, y + height),
			right: Math.max(x, x + width),
			bottom: Math.max(y, y + height)
		};
	}

	/**
	 * Converts a margin edge to pixels.
	 *
	 * @param edge Edge.
	 * @param size Root size on the corresponding axis.
	 * @returns Pixels.
	 */
	#marginLength(edge: IRootMarginEdge, size: number): number {
		if (edge.unit === '%') {
			return (size * edge.value) / 100;
		}
		return edge.value;
	}

	/**
	 * Returns the intersection of two boxes, including zero-area contact.
	 *
	 * @param first First box.
	 * @param second Second box.
	 * @returns Intersection, or null when the boxes are disjoint.
	 */
	#intersect(first: IBox, second: IBox): IBox | null {
		const left = Math.max(first.left, second.left);
		const top = Math.max(first.top, second.top);
		const right = Math.min(first.right, second.right);
		const bottom = Math.min(first.bottom, second.bottom);

		if (right < left || bottom < top) {
			return null;
		}

		return { left, top, right, bottom };
	}

	/**
	 * Returns true when the inner box lies entirely inside the outer box.
	 *
	 * @param outer Outer box.
	 * @param inner Inner box.
	 * @returns Contained.
	 */
	#contains(outer: IBox, inner: IBox): boolean {
		if (outer.right < outer.left || outer.bottom < outer.top) {
			return false;
		}
		return (
			inner.left >= outer.left &&
			inner.right <= outer.right &&
			inner.top >= outer.top &&
			inner.bottom <= outer.bottom
		);
	}

	/**
	 * Returns the area of a box.
	 *
	 * @param box Box.
	 * @returns Area.
	 */
	#area(box: IBox): number {
		return Math.max(0, box.right - box.left) * Math.max(0, box.bottom - box.top);
	}

	/**
	 * Creates a DOMRect for a box. An empty rect is used when the box is null.
	 *
	 * @param DOMRectConstructor DOMRect constructor.
	 * @param box Box.
	 * @returns DOMRect.
	 */
	#toDOMRect(DOMRectConstructor: typeof DOMRect, box: IBox | null): DOMRect {
		if (!box) {
			return new DOMRectConstructor(0, 0, 0, 0);
		}
		return new DOMRectConstructor(box.left, box.top, box.right - box.left, box.bottom - box.top);
	}

	/**
	 * Maps a ratio to the greatest threshold index that has been reached.
	 * A non-intersecting target is below every threshold, including 0.
	 *
	 * @param ratio Intersection ratio.
	 * @param isIntersecting Whether the target intersects the root.
	 * @returns Threshold index, or -1 when no threshold has been reached.
	 */
	#thresholdIndex(ratio: number, isIntersecting: boolean): number {
		if (!isIntersecting) {
			return -1;
		}

		let index = -1;
		for (let i = 0, max = this.#thresholds.length; i < max; i++) {
			if (ratio >= this.#thresholds[i]) {
				index = i;
			}
		}
		return index;
	}

	/**
	 * Returns a timestamp.
	 *
	 * @returns Time.
	 */
	#now(): number {
		const window = this[PropertySymbol.window];
		if (window?.performance) {
			return window.performance.now();
		}
		return 0;
	}

	/**
	 * Subscribes to document connection changes while targets are observed.
	 */
	#ensureSubscribed(): void {
		if (this.#unsubscribe) {
			return;
		}
		this.#unsubscribe = subscribeIntersectionObserver(() => {
			this.#refresh();
		});
	}

	/**
	 * Stops listening for document connection changes.
	 */
	#unsubscribeFromDocument(): void {
		if (this.#unsubscribe) {
			this.#unsubscribe();
			this.#unsubscribe = null;
		}
	}

	/**
	 * Parses the root option.
	 *
	 * @param root Root.
	 * @returns Root element or null.
	 */
	#parseRoot(root: Element | null | undefined): Element | null {
		if (root === undefined || root === null) {
			return null;
		}

		if (!this.#isElement(root)) {
			throw new this.#typeError()(
				"Failed to construct 'IntersectionObserver': root must be an Element or null."
			);
		}

		return root;
	}

	/**
	 * Parses a CSS root margin shorthand into four edges.
	 *
	 * @param rootMargin Root margin.
	 * @returns Parsed margin.
	 */
	#parseRootMargin(rootMargin: string | undefined): IParsedRootMargin {
		if (rootMargin === undefined) {
			return {
				top: DEFAULT_MARGIN,
				right: DEFAULT_MARGIN,
				bottom: DEFAULT_MARGIN,
				left: DEFAULT_MARGIN,
				normalized: '0px 0px 0px 0px'
			};
		}

		if (typeof rootMargin !== 'string') {
			throw new this.#typeError()(
				"Failed to construct 'IntersectionObserver': rootMargin must be specified in pixels or percent."
			);
		}

		const parts = rootMargin.trim().split(/\s+/);
		if (rootMargin.trim() === '' || parts.length < 1 || parts.length > 4) {
			throw new this.#typeError()(
				"Failed to construct 'IntersectionObserver': rootMargin must be specified in pixels or percent."
			);
		}

		const edges = parts.map((part) => this.#parseRootMarginEdge(part));
		const top = edges[0];
		const right = edges[1] ?? edges[0];
		const bottom = edges[2] ?? edges[0];
		const left = edges[3] ?? edges[1] ?? edges[0];

		return {
			top,
			right,
			bottom,
			left,
			normalized: [top, right, bottom, left].map((edge) => this.#formatEdge(edge)).join(' ')
		};
	}

	/**
	 * Parses one root margin edge.
	 *
	 * @param part Edge token.
	 * @returns Edge.
	 */
	#parseRootMarginEdge(part: string): IRootMarginEdge {
		const match = ROOT_MARGIN_PATTERN.exec(part);
		if (!match) {
			throw new this.#typeError()(
				"Failed to construct 'IntersectionObserver': rootMargin must be specified in pixels or percent."
			);
		}

		return {
			value: Number(match[1]),
			unit: <'px' | '%'>match[2]
		};
	}

	/**
	 * Formats an edge for the normalized rootMargin string.
	 *
	 * @param edge Edge.
	 * @returns Serialized edge.
	 */
	#formatEdge(edge: IRootMarginEdge): string {
		const value = Object.is(edge.value, -0) ? 0 : edge.value;
		return `${value}${edge.unit}`;
	}

	/**
	 * Normalizes thresholds to a sorted list of unique values in the range 0..1.
	 *
	 * @param threshold Threshold option.
	 * @returns Thresholds.
	 */
	#parseThresholds(threshold: number | number[] | undefined): number[] {
		if (threshold === undefined) {
			return [0];
		}

		const values = Array.isArray(threshold) ? threshold : [threshold];
		if (values.length === 0) {
			throw new this.#typeError()(
				"Failed to construct 'IntersectionObserver': Threshold values must be between 0 and 1."
			);
		}

		for (const value of values) {
			if (typeof value !== 'number' || Number.isNaN(value) || value < 0 || value > 1) {
				throw new this.#typeError()(
					"Failed to construct 'IntersectionObserver': Threshold values must be between 0 and 1."
				);
			}
		}

		return [...new Set(values)].sort((left, right) => left - right);
	}

	/**
	 * Returns true when the value is an element.
	 *
	 * @param value Value.
	 * @returns Is element.
	 */
	#isElement(value: unknown): value is Element {
		return !!value && (<Element>value)[PropertySymbol.nodeType] === NodeTypeEnum.elementNode;
	}

	/**
	 * Returns the TypeError constructor for this realm.
	 *
	 * @returns TypeError.
	 */
	#typeError(): typeof TypeError {
		return this[PropertySymbol.window]?.TypeError ?? TypeError;
	}
}
