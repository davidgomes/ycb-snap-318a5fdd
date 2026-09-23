import type BrowserWindow from '../window/BrowserWindow.js';
import type Document from '../nodes/document/Document.js';
import type MutationObserver from '../mutation-observer/MutationObserver.js';
import Element from '../nodes/element/Element.js';
import * as PropertySymbol from '../PropertySymbol.js';
import NodeTypeEnum from '../nodes/node/NodeTypeEnum.js';
import IntersectionObserverEntry from './IntersectionObserverEntry.js';
import type IIntersectionObserverInit from './IIntersectionObserverInit.js';

interface IMarginSide {
	value: number;
	unit: 'px' | '%';
}

interface IBox {
	x: number;
	y: number;
	width: number;
	height: number;
}

interface IObservation {
	target: Element;
	// -1 means the target has not produced an entry yet.
	lastThresholdIndex: number;
	lastIsIntersecting: boolean;
}

interface ISnapshot {
	entry: IntersectionObserverEntry;
	thresholdIndex: number;
	isIntersecting: boolean;
}

const ROOT_MARGIN_TOKEN = /^([+-]?(?:\d+\.?\d*|\.\d+))(px|%)$/i;

/**
 * The IntersectionObserver interface of the Intersection Observer API provides a way to asynchronously observe changes in the intersection of a target element with an ancestor element or with a top-level document's viewport.
 *
 * @see https://developer.mozilla.org/en-US/docs/Web/API/IntersectionObserver
 */
export default class IntersectionObserver {
	// Injected by WindowContextClassExtender
	protected declare [PropertySymbol.window]: BrowserWindow;

	#window: BrowserWindow;
	#callback: (entries: IntersectionObserverEntry[], observer: IntersectionObserver) => void;
	#root: Element | Document | null;
	#rootMargin: string;
	#margins: [IMarginSide, IMarginSide, IMarginSide, IMarginSide];
	#thresholds: readonly number[];
	#observations: IObservation[] = [];
	#records: IntersectionObserverEntry[] = [];
	#mutationObserver: MutationObserver | null = null;
	#listening = false;
	#deliveryScheduled = false;
	#updating = false;
	#destroyed = false;
	#generation = 0;

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
		if (!this[PropertySymbol.window]) {
			throw new TypeError(
				`Failed to construct 'IntersectionObserver': 'IntersectionObserver' was constructed outside a Window context.`
			);
		}

		this.#window = this[PropertySymbol.window];

		if (typeof callback !== 'function') {
			throw new this.#window.TypeError(
				`Failed to construct 'IntersectionObserver': The callback provided as parameter 1 is not a function.`
			);
		}

		if (options !== undefined && options !== null && typeof options !== 'object') {
			throw new this.#window.TypeError(
				`Failed to construct 'IntersectionObserver': The provided value is not of type 'IntersectionObserverInit'.`
			);
		}

		const init = options ?? {};
		const root = init.root === undefined ? null : init.root;

		if (root !== null && !isElement(root) && !isDocument(root)) {
			throw new this.#window.TypeError(
				`Failed to construct 'IntersectionObserver': The provided root is not an Element, Document, or null.`
			);
		}

		const parsedMargin = parseRootMargin(this.#window, init.rootMargin);
		const thresholds = parseThresholds(this.#window, init.threshold);

		this.#callback = callback;
		this.#root = root;
		this.#rootMargin = parsedMargin.serialized;
		this.#margins = parsedMargin.margins;
		this.#thresholds = Object.freeze(thresholds);

		if (!this.#window[PropertySymbol.intersectionObservers].includes(this)) {
			this.#window[PropertySymbol.intersectionObservers].push(this);
		}
	}

	/**
	 * Returns the intersection root.
	 *
	 * @returns Root element, document, or null for the viewport.
	 */
	public get root(): Element | Document | null {
		return this.#root;
	}

	/**
	 * Returns the normalized root margin in four-value form (top right bottom left).
	 *
	 * @returns Root margin.
	 */
	public get rootMargin(): string {
		return this.#rootMargin;
	}

	/**
	 * Returns the sorted unique thresholds.
	 *
	 * @returns Thresholds.
	 */
	public get thresholds(): readonly number[] {
		return this.#thresholds;
	}

	/**
	 * Starts observing a target.
	 *
	 * The initial entry is queued immediately. The callback runs asynchronously.
	 *
	 * @param target Target.
	 */
	public observe(target: Element): void {
		if (this.#destroyed) {
			return;
		}

		if (!isElement(target)) {
			throw new this.#window.TypeError(
				`Failed to execute 'observe' on 'IntersectionObserver': parameter 1 is not of type 'Element'.`
			);
		}

		if (this.#observations.some((item) => item.target === target)) {
			return;
		}

		const observation: IObservation = {
			target,
			lastThresholdIndex: -1,
			lastIsIntersecting: false
		};

		this.#observations.push(observation);
		this.#ensureListening();
		this.#queueObservation(observation);
	}

	/**
	 * Stops observing a target and drops pending entries for it.
	 *
	 * @param target Target.
	 */
	public unobserve(target: Element): void {
		if (this.#destroyed) {
			return;
		}

		if (!isElement(target)) {
			throw new this.#window.TypeError(
				`Failed to execute 'unobserve' on 'IntersectionObserver': parameter 1 is not of type 'Element'.`
			);
		}

		const index = this.#observations.findIndex((item) => item.target === target);

		if (index === -1) {
			return;
		}

		this.#observations.splice(index, 1);
		this.#records = this.#records.filter((record) => record.target !== target);

		if (this.#observations.length === 0) {
			this.#stopListening();
		}
	}

	/**
	 * Stops observing every target and clears pending records.
	 */
	public disconnect(): void {
		this.#generation++;
		this.#observations = [];
		this.#records = [];
		this.#deliveryScheduled = false;
		this.#stopListening();
	}

	/**
	 * Returns queued entries and clears the queue.
	 *
	 * @returns Records.
	 */
	public takeRecords(): IntersectionObserverEntry[] {
		const records = this.#records;
		this.#records = [];
		return records;
	}

	/**
	 * Destroys the observer when the window is closed.
	 */
	public [PropertySymbol.destroy](): void {
		if (this.#destroyed) {
			return;
		}

		this.#destroyed = true;
		this.disconnect();

		const observers = this.#window[PropertySymbol.intersectionObservers];
		const index = observers.indexOf(this);

		if (index !== -1) {
			observers.splice(index, 1);
		}
	}

	/**
	 * Measures one target and appends an entry when its threshold bucket changes.
	 *
	 * Pending entries are kept so an initial observation and a later crossing in the same
	 * delivery cycle are both reported.
	 *
	 * @param observation Observation.
	 */
	#queueObservation(observation: IObservation): void {
		const snapshot = this.#createSnapshot(observation.target);
		const thresholdChanged =
			observation.lastThresholdIndex !== snapshot.thresholdIndex ||
			observation.lastIsIntersecting !== snapshot.isIntersecting;

		if (!thresholdChanged) {
			return;
		}

		observation.lastThresholdIndex = snapshot.thresholdIndex;
		observation.lastIsIntersecting = snapshot.isIntersecting;
		this.#records.push(snapshot.entry);
		this.#scheduleDelivery();
	}

	/**
	 * Builds an entry from the current root and target rectangles.
	 *
	 * @param target Target.
	 * @returns Snapshot.
	 */
	#createSnapshot(target: Element): ISnapshot {
		const targetBox = readElementBox(target);
		const rootBox = expandBox(readRootBox(this.#root, this.#window), this.#margins);
		const targetArea = targetBox.width * targetBox.height;
		let intersectionBox: IBox = { x: 0, y: 0, width: 0, height: 0 };
		let ratio = 0;

		if (targetArea === 0) {
			if (containsBox(rootBox, targetBox)) {
				ratio = 1;
				intersectionBox = targetBox;
			}
		} else {
			const hit = intersectBox(rootBox, targetBox);
			if (hit) {
				ratio = clampRatio((hit.width * hit.height) / targetArea);
				intersectionBox = hit;
			}
		}

		const isIntersecting = ratio > 0;
		const DOMRect = this.#window.DOMRect;
		const entry = new IntersectionObserverEntry({
			time: this.#window.performance.now(),
			target,
			boundingClientRect: new DOMRect(targetBox.x, targetBox.y, targetBox.width, targetBox.height),
			intersectionRect: new DOMRect(
				intersectionBox.x,
				intersectionBox.y,
				intersectionBox.width,
				intersectionBox.height
			),
			rootBounds: new DOMRect(rootBox.x, rootBox.y, rootBox.width, rootBox.height),
			isIntersecting,
			intersectionRatio: ratio
		});

		return {
			entry,
			isIntersecting,
			thresholdIndex: thresholdIndexForRatio(ratio, this.#thresholds)
		};
	}

	/**
	 * Delivers queued entries in a microtask, in observation order.
	 */
	#scheduleDelivery(): void {
		if (this.#deliveryScheduled || this.#destroyed) {
			return;
		}

		this.#deliveryScheduled = true;
		const generation = this.#generation;

		this.#window.queueMicrotask(() => {
			if (this.#destroyed || generation !== this.#generation) {
				return;
			}

			this.#deliveryScheduled = false;

			const records = this.takeRecords();

			if (records.length === 0) {
				return;
			}

			const order = new Map<Element, number>();

			for (let index = 0; index < this.#observations.length; index++) {
				order.set(this.#observations[index].target, index);
			}

			records.sort((left, right) => {
				return (order.get(<Element>left.target) ?? 0) - (order.get(<Element>right.target) ?? 0);
			});

			this.#callback.call(this, records, this);
		});
	}

	/**
	 * Subscribes to scroll, resize, and DOM mutations that can change intersection.
	 */
	#ensureListening(): void {
		if (this.#listening || this.#destroyed) {
			return;
		}

		this.#listening = true;
		this.#window.addEventListener('scroll', this.#onLayoutChange, true);
		this.#window.addEventListener('resize', this.#onLayoutChange, true);

		const document = this.#window.document;

		if (document) {
			this.#mutationObserver = new this.#window.MutationObserver(() => {
				this.#onLayoutChange();
			});
			this.#mutationObserver.observe(document, {
				attributes: true,
				childList: true,
				subtree: true
			});
		}
	}

	/**
	 * Removes layout listeners.
	 */
	#stopListening(): void {
		if (!this.#listening) {
			return;
		}

		this.#listening = false;
		this.#window.removeEventListener('scroll', this.#onLayoutChange);
		this.#window.removeEventListener('resize', this.#onLayoutChange);

		if (this.#mutationObserver) {
			this.#mutationObserver.disconnect();
			this.#mutationObserver = null;
		}
	}

	/**
	 * Recomputes every target after a layout-affecting event.
	 */
	#onLayoutChange = (): void => {
		if (this.#destroyed || this.#updating) {
			return;
		}

		this.#updating = true;

		try {
			for (const observation of this.#observations) {
				this.#queueObservation(observation);
			}
		} finally {
			this.#updating = false;
		}
	};
}

/**
 * Returns true when the value is an element.
 *
 * @param value Value.
 * @returns True when the value is an element.
 */
function isElement(value: unknown): value is Element {
	return (
		!!value &&
		typeof value === 'object' &&
		(<Element>value)[PropertySymbol.nodeType] === NodeTypeEnum.elementNode
	);
}

/**
 * Returns true when the value is a document.
 *
 * @param value Value.
 * @returns True when the value is a document.
 */
function isDocument(value: unknown): value is Document {
	return (
		!!value &&
		typeof value === 'object' &&
		(<Document>value)[PropertySymbol.nodeType] === NodeTypeEnum.documentNode
	);
}

/**
 * Parses rootMargin into four sides and a normalized string.
 *
 * @param window Window.
 * @param rootMargin Raw root margin.
 * @returns Parsed margin.
 */
function parseRootMargin(
	window: BrowserWindow,
	rootMargin: string | undefined
): {
	serialized: string;
	margins: [IMarginSide, IMarginSide, IMarginSide, IMarginSide];
} {
	if (rootMargin === undefined) {
		const zero: IMarginSide = { value: 0, unit: 'px' };
		return {
			serialized: '0px 0px 0px 0px',
			margins: [zero, zero, zero, zero]
		};
	}

	if (typeof rootMargin !== 'string') {
		throw new window.SyntaxError(
			`Failed to construct 'IntersectionObserver': rootMargin must be specified in pixels or percent.`
		);
	}

	const tokens = rootMargin.trim() === '' ? [] : rootMargin.trim().split(/[ \t\n\r\f]+/);

	if (tokens.length > 4) {
		throw new window.SyntaxError(
			`Failed to construct 'IntersectionObserver': rootMargin must be specified in pixels or percent.`
		);
	}

	const sides: IMarginSide[] = [];

	for (const token of tokens) {
		const match = ROOT_MARGIN_TOKEN.exec(token);

		if (!match) {
			throw new window.SyntaxError(
				`Failed to construct 'IntersectionObserver': rootMargin must be specified in pixels or percent.`
			);
		}

		const value = Number(match[1]);

		if (!Number.isFinite(value)) {
			throw new window.SyntaxError(
				`Failed to construct 'IntersectionObserver': rootMargin must be specified in pixels or percent.`
			);
		}

		sides.push({
			value: Object.is(value, -0) ? 0 : value,
			unit: <'px' | '%'>match[2].toLowerCase()
		});
	}

	let top: IMarginSide;
	let right: IMarginSide;
	let bottom: IMarginSide;
	let left: IMarginSide;
	const zero: IMarginSide = { value: 0, unit: 'px' };

	switch (sides.length) {
		case 0:
			top = right = bottom = left = zero;
			break;
		case 1:
			top = right = bottom = left = sides[0];
			break;
		case 2:
			top = bottom = sides[0];
			right = left = sides[1];
			break;
		case 3:
			top = sides[0];
			right = left = sides[1];
			bottom = sides[2];
			break;
		default:
			top = sides[0];
			right = sides[1];
			bottom = sides[2];
			left = sides[3];
			break;
	}

	const margins: [IMarginSide, IMarginSide, IMarginSide, IMarginSide] = [top, right, bottom, left];

	return {
		margins,
		serialized: margins.map(formatMarginSide).join(' ')
	};
}

/**
 * Serializes one margin side.
 *
 * @param side Side.
 * @returns CSS length.
 */
function formatMarginSide(side: IMarginSide): string {
	return `${formatMarginNumber(side.value)}${side.unit}`;
}

/**
 * Serializes a margin number without a trailing decimal.
 *
 * @param value Value.
 * @returns Number text.
 */
function formatMarginNumber(value: number): string {
	if (!Number.isFinite(value) || Object.is(value, -0) || value === 0) {
		return '0';
	}

	const text = String(value);

	if (!text.includes('e') && !text.includes('E')) {
		return text;
	}

	return value.toFixed(6).replace(/\.?0+$/, '');
}

/**
 * Normalizes thresholds to a sorted unique list.
 *
 * @param window Window.
 * @param threshold Threshold option.
 * @returns Thresholds.
 */
function parseThresholds(
	window: BrowserWindow,
	threshold: number | number[] | undefined
): number[] {
	if (threshold === undefined) {
		return [0];
	}

	const list = Array.isArray(threshold) ? threshold : [threshold];

	if (list.length === 0) {
		return [0];
	}

	const values: number[] = [];

	for (const item of list) {
		if (typeof item !== 'number') {
			throw new window.TypeError(
				`Failed to construct 'IntersectionObserver': The provided threshold is not a number or a list of numbers.`
			);
		}

		if (!Number.isFinite(item)) {
			throw new window.TypeError(
				`Failed to construct 'IntersectionObserver': The provided double value is non-finite.`
			);
		}

		if (item < 0 || item > 1) {
			throw new window.RangeError(
				`Failed to construct 'IntersectionObserver': Threshold values must be between 0 and 1.`
			);
		}

		values.push(item);
	}

	values.sort((left, right) => left - right);

	const unique: number[] = [];

	for (const value of values) {
		if (unique.length === 0 || unique[unique.length - 1] !== value) {
			unique.push(value);
		}
	}

	return unique;
}

/**
 * Returns the index of the first threshold greater than the ratio.
 *
 * @param ratio Intersection ratio.
 * @param thresholds Thresholds.
 * @returns Threshold index.
 */
function thresholdIndexForRatio(ratio: number, thresholds: readonly number[]): number {
	for (let index = 0; index < thresholds.length; index++) {
		if (thresholds[index] > ratio) {
			return index;
		}
	}

	return thresholds.length;
}

/**
 * Reads a finite number.
 *
 * @param value Value.
 * @returns Finite number or 0.
 */
function toFinite(value: unknown): number {
	const number = typeof value === 'number' ? value : Number(value);
	return Number.isFinite(number) ? number : 0;
}

/**
 * Reads an element box.
 *
 * Mocked getBoundingClientRect() wins. Otherwise a non-zero offset box is used so geometry stays deterministic without a layout engine.
 *
 * @param element Element.
 * @returns Box.
 */
function readElementBox(element: Element): IBox {
	const rect = element.getBoundingClientRect();
	const box = {
		x: toFinite(rect?.x),
		y: toFinite(rect?.y),
		width: toFinite(rect?.width),
		height: toFinite(rect?.height)
	};
	const hasRect = box.x !== 0 || box.y !== 0 || box.width !== 0 || box.height !== 0;

	if (!hasRect && element.getBoundingClientRect === Element.prototype.getBoundingClientRect) {
		const withOffset = <
			Element & {
				offsetLeft?: number;
				offsetTop?: number;
				offsetWidth?: number;
				offsetHeight?: number;
			}
		>element;
		const offsetBox = {
			x: toFinite(withOffset.offsetLeft),
			y: toFinite(withOffset.offsetTop),
			width: toFinite(withOffset.offsetWidth),
			height: toFinite(withOffset.offsetHeight)
		};

		if (offsetBox.x !== 0 || offsetBox.y !== 0 || offsetBox.width !== 0 || offsetBox.height !== 0) {
			return normalizeBox(offsetBox);
		}
	}

	return normalizeBox(box);
}

/**
 * Reads the viewport or element root box before margins are applied.
 *
 * @param root Root.
 * @param window Window.
 * @returns Box.
 */
function readRootBox(root: Element | Document | null, window: BrowserWindow): IBox {
	if (root === null) {
		return {
			x: 0,
			y: 0,
			width: toFinite(window.innerWidth),
			height: toFinite(window.innerHeight)
		};
	}

	if (isDocument(root)) {
		const rootWindow = root[PropertySymbol.window] ?? window;
		return {
			x: 0,
			y: 0,
			width: toFinite(rootWindow.innerWidth),
			height: toFinite(rootWindow.innerHeight)
		};
	}

	return readElementBox(root);
}

/**
 * Expands a root box by the parsed margin.
 *
 * @param box Root box.
 * @param margins Margins in top, right, bottom, left order.
 * @returns Expanded box. Width or height may be negative when margins collapse the root.
 */
function expandBox(box: IBox, margins: [IMarginSide, IMarginSide, IMarginSide, IMarginSide]): IBox {
	const top = resolveMargin(margins[0], box.height);
	const right = resolveMargin(margins[1], box.width);
	const bottom = resolveMargin(margins[2], box.height);
	const left = resolveMargin(margins[3], box.width);

	return {
		x: box.x - left,
		y: box.y - top,
		width: box.width + left + right,
		height: box.height + top + bottom
	};
}

/**
 * Resolves one margin length against a root dimension.
 *
 * @param side Margin side.
 * @param size Root width or height.
 * @returns Pixel length.
 */
function resolveMargin(side: IMarginSide, size: number): number {
	if (side.unit === '%') {
		return (size * side.value) / 100;
	}

	return side.value;
}

/**
 * Normalizes a box so width and height are non-negative.
 *
 * @param box Box.
 * @returns Normalized box.
 */
function normalizeBox(box: IBox): IBox {
	const left = Math.min(box.x, box.x + box.width);
	const right = Math.max(box.x, box.x + box.width);
	const top = Math.min(box.y, box.y + box.height);
	const bottom = Math.max(box.y, box.y + box.height);

	return {
		x: left,
		y: top,
		width: right - left,
		height: bottom - top
	};
}

/**
 * Returns true when the inner box lies entirely inside the outer box.
 *
 * @param outer Outer box.
 * @param inner Inner box.
 * @returns True when contained.
 */
function containsBox(outer: IBox, inner: IBox): boolean {
	if (outer.width < 0 || outer.height < 0) {
		return false;
	}

	const outerRight = outer.x + outer.width;
	const outerBottom = outer.y + outer.height;
	const innerRight = inner.x + inner.width;
	const innerBottom = inner.y + inner.height;

	return (
		inner.x >= outer.x &&
		innerRight <= outerRight &&
		inner.y >= outer.y &&
		innerBottom <= outerBottom
	);
}

/**
 * Returns the overlap of two boxes, or null when the overlap has no area.
 *
 * @param root Root box.
 * @param target Target box.
 * @returns Intersection box.
 */
function intersectBox(root: IBox, target: IBox): IBox | null {
	if (root.width < 0 || root.height < 0 || target.width < 0 || target.height < 0) {
		return null;
	}

	const left = Math.max(root.x, target.x);
	const top = Math.max(root.y, target.y);
	const right = Math.min(root.x + root.width, target.x + target.width);
	const bottom = Math.min(root.y + root.height, target.y + target.height);
	const width = right - left;
	const height = bottom - top;

	if (width <= 0 || height <= 0) {
		return null;
	}

	return { x: left, y: top, width, height };
}

/**
 * Clamps a ratio to the range 0..1.
 *
 * @param ratio Ratio.
 * @returns Clamped ratio.
 */
function clampRatio(ratio: number): number {
	if (!Number.isFinite(ratio) || ratio <= 0) {
		return 0;
	}

	if (ratio >= 1) {
		return 1;
	}

	return ratio;
}
