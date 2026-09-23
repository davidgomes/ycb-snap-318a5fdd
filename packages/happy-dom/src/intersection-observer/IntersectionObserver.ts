import type Document from '../nodes/document/Document.js';
import type Element from '../nodes/element/Element.js';
import type Node from '../nodes/node/Node.js';
import DOMRect from '../dom/DOMRect.js';
import DOMException from '../exception/DOMException.js';
import DOMExceptionNameEnum from '../exception/DOMExceptionNameEnum.js';
import * as PropertySymbol from '../PropertySymbol.js';
import type BrowserWindow from '../window/BrowserWindow.js';
import type IIntersectionObserverInit from './IIntersectionObserverInit.js';
import IntersectionObserverEntry from './IntersectionObserverEntry.js';

interface IMarginComponent {
	value: number;
	unit: 'px' | '%';
}

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

interface ITargetRegistration {
	target: Element;
	previousThresholdIndex: number;
	previousIsIntersecting: boolean;
}

/**
 * Samples observed targets after the current task so threshold crossings are noticed without a render loop.
 */
class IntersectionObserverScheduler {
	#window: BrowserWindow;
	#observers: Set<IntersectionObserver> = new Set();
	#timer: ReturnType<typeof setTimeout> | null = null;

	/**
	 * @param window Window.
	 */
	constructor(window: BrowserWindow) {
		this.#window = window;
	}

	/**
	 * Tracks an observer until it has no targets.
	 *
	 * @param observer Observer.
	 */
	public watch(observer: IntersectionObserver): void {
		this.#observers.add(observer);
		this.#arm();
	}

	/**
	 * Stops tracking an observer.
	 *
	 * @param observer Observer.
	 */
	public unwatch(observer: IntersectionObserver): void {
		this.#observers.delete(observer);
		if (this.#observers.size === 0) {
			this.#disarm();
		}
	}

	/**
	 * Recomputes every tracked observer.
	 */
	public run(): void {
		if ((<BrowserWindow>this.#window).closed) {
			this.#disarm();
			return;
		}

		for (const observer of [...this.#observers]) {
			if (!this.#observers.has(observer)) {
				continue;
			}
			observer[PropertySymbol.updateIntersectionObservations]();
		}
	}

	/**
	 * Arms the next sample.
	 */
	#arm(): void {
		if (
			this.#timer !== null ||
			this.#observers.size === 0 ||
			(<BrowserWindow>this.#window).closed
		) {
			return;
		}

		const timer = setTimeout(() => {
			this.#timer = null;
			if ((<BrowserWindow>this.#window).closed || this.#observers.size === 0) {
				return;
			}
			this.run();
			this.#arm();
		}, 0);

		if (typeof timer.unref === 'function') {
			timer.unref();
		}

		this.#timer = timer;
	}

	/**
	 * Stops sampling.
	 */
	#disarm(): void {
		if (this.#timer !== null) {
			clearTimeout(this.#timer);
			this.#timer = null;
		}
	}
}

const schedulers = new WeakMap<BrowserWindow, IntersectionObserverScheduler>();

/**
 * Returns the scheduler for a window.
 *
 * @param window Window.
 * @returns Scheduler.
 */
function getScheduler(window: BrowserWindow): IntersectionObserverScheduler {
	let scheduler = schedulers.get(window);
	if (!scheduler) {
		scheduler = new IntersectionObserverScheduler(window);
		schedulers.set(window, scheduler);
	}
	return scheduler;
}

/**
 * The IntersectionObserver interface of the Intersection Observer API provides a way to asynchronously observe changes in the intersection of a target element with an ancestor element or with a top-level document's viewport.
 *
 * @see https://developer.mozilla.org/en-US/docs/Web/API/IntersectionObserver
 */
export default class IntersectionObserver {
	// Injected by WindowContextClassExtender
	protected declare [PropertySymbol.window]: BrowserWindow;
	#callback: (entries: IntersectionObserverEntry[], observer: IntersectionObserver) => void;
	#root: Element | Document | null = null;
	#rootMargin: string = '0px 0px 0px 0px';
	#margins: [IMarginComponent, IMarginComponent, IMarginComponent, IMarginComponent] = [
		{ value: 0, unit: 'px' },
		{ value: 0, unit: 'px' },
		{ value: 0, unit: 'px' },
		{ value: 0, unit: 'px' }
	];
	#thresholds: readonly number[] = Object.freeze([0]);
	#targets: ITargetRegistration[] = [];
	#queued: IntersectionObserverEntry[] = [];
	#generation = 0;
	#updateScheduled = false;
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
		if (!this[PropertySymbol.window]) {
			throw new TypeError(
				`Failed to construct 'IntersectionObserver': 'IntersectionObserver' was constructed outside a Window context.`
			);
		}

		if (arguments.length < 1) {
			throw new this[PropertySymbol.window].TypeError(
				"Failed to construct 'IntersectionObserver': 1 argument required, but only 0 present."
			);
		}

		if (typeof callback !== 'function') {
			throw new this[PropertySymbol.window].TypeError(
				"Failed to construct 'IntersectionObserver': The callback provided as parameter 1 is not a function."
			);
		}

		const init = this.#normalizeInit(options);
		this.#callback = callback;
		this.#root = this.#normalizeRoot(init.root);
		const parsedMargin = this.#parseRootMargin(init.rootMargin);
		this.#margins = parsedMargin.components;
		this.#rootMargin = parsedMargin.serialized;
		this.#thresholds = Object.freeze(this.#normalizeThresholds(init.threshold));
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
	 * Returns the normalized root margin.
	 *
	 * @returns Root margin in four-value form.
	 */
	public get rootMargin(): string {
		return this.#rootMargin;
	}

	/**
	 * Returns the normalized thresholds.
	 *
	 * @returns Thresholds sorted in ascending order.
	 */
	public get thresholds(): readonly number[] {
		return this.#thresholds;
	}

	/**
	 * Starts observing a target.
	 *
	 * @param target Target.
	 */
	public observe(target: Element): void {
		this.#assertElement(target, 'observe');

		if (this.#targets.some((registration) => registration.target === target)) {
			return;
		}

		this.#targets.push({
			target,
			previousThresholdIndex: -1,
			previousIsIntersecting: false
		});

		getScheduler(this[PropertySymbol.window]).watch(this);
		this.#scheduleUpdate();
	}

	/**
	 * Stops observing a target. Already queued entries for the target are kept.
	 *
	 * @param target Target.
	 */
	public unobserve(target: Element): void {
		this.#assertElement(target, 'unobserve');

		const index = this.#targets.findIndex((registration) => registration.target === target);
		if (index === -1) {
			return;
		}

		this.#targets.splice(index, 1);

		if (this.#targets.length === 0) {
			getScheduler(this[PropertySymbol.window]).unwatch(this);
		}
	}

	/**
	 * Stops observing every target and drops entries that have not been delivered.
	 */
	public disconnect(): void {
		this.#generation++;
		this.#updateScheduled = false;
		this.#deliveryScheduled = false;
		this.#queued = [];
		this.#targets = [];
		getScheduler(this[PropertySymbol.window]).unwatch(this);
	}

	/**
	 * Returns queued entries and clears the queue so they are not delivered to the callback.
	 *
	 * @returns Records.
	 */
	public takeRecords(): IntersectionObserverEntry[] {
		const records = this.#queued;
		this.#queued = [];
		return records;
	}

	/**
	 * Recomputes intersections for the current targets and queues entries for threshold crossings.
	 */
	public [PropertySymbol.updateIntersectionObservations](): void {
		if (this[PropertySymbol.window].closed) {
			return;
		}

		const generation = this.#generation;
		for (const registration of this.#targets.slice()) {
			if (generation !== this.#generation) {
				return;
			}
			if (!this.#targets.includes(registration)) {
				continue;
			}
			this.#measure(registration);
		}

		if (generation === this.#generation) {
			this.#scheduleDelivery();
		}
	}

	/**
	 * Validates constructor options.
	 *
	 * @param options Options.
	 * @returns Init dictionary.
	 */
	#normalizeInit(options?: IIntersectionObserverInit): IIntersectionObserverInit {
		if (options === undefined || options === null) {
			return {};
		}

		if (typeof options !== 'object') {
			throw new this[PropertySymbol.window].TypeError(
				"Failed to construct 'IntersectionObserver': The provided value is not of type 'IntersectionObserverInit'."
			);
		}

		return options;
	}

	/**
	 * Validates the root option.
	 *
	 * @param root Root.
	 * @returns Root.
	 */
	#normalizeRoot(root: IIntersectionObserverInit['root']): Element | Document | null {
		if (root === undefined || root === null) {
			return null;
		}

		const nodeType = (<Node>(<unknown>root)).nodeType;
		if (nodeType === 1 || nodeType === 9) {
			return root;
		}

		throw new this[PropertySymbol.window].TypeError(
			"Failed to construct 'IntersectionObserver': The provided value is not of type '(Document or Element)'."
		);
	}

	/**
	 * Parses a root margin string into four components.
	 *
	 * @param rootMargin Root margin.
	 * @returns Parsed margin.
	 */
	#parseRootMargin(rootMargin: string | undefined): {
		components: [IMarginComponent, IMarginComponent, IMarginComponent, IMarginComponent];
		serialized: string;
	} {
		if (rootMargin === undefined) {
			rootMargin = '0px';
		}

		if (typeof rootMargin !== 'string') {
			throw this.#syntaxError(
				"Failed to construct 'IntersectionObserver': rootMargin must be specified in pixels or percent."
			);
		}

		const trimmed = rootMargin.trim();
		const tokens = trimmed === '' ? [] : trimmed.split(/\s+/);

		if (tokens.length > 4) {
			throw this.#syntaxError(
				"Failed to construct 'IntersectionObserver': Extra text found at the end of rootMargin."
			);
		}

		const parsed = tokens.map((token) => this.#parseMarginToken(token));
		if (parsed.length === 0) {
			parsed.push({ value: 0, unit: 'px' });
		}

		const top = parsed[0];
		const right = parsed[1] ?? top;
		const bottom = parsed[2] ?? top;
		const left = parsed[3] ?? right;
		const components: [IMarginComponent, IMarginComponent, IMarginComponent, IMarginComponent] = [
			top,
			parsed.length === 1 ? top : right,
			parsed.length === 1 ? top : parsed.length === 2 ? top : bottom,
			parsed.length === 1 ? top : left
		];

		return {
			components,
			serialized: components.map((component) => formatMargin(component)).join(' ')
		};
	}

	/**
	 * Parses one margin token.
	 *
	 * @param token Token.
	 * @returns Margin component.
	 */
	#parseMarginToken(token: string): IMarginComponent {
		const match = /^([+-]?(?:\d+(?:\.\d+)?|\.\d+))(px|%)$/i.exec(token);
		if (match) {
			return {
				value: normalizeSignedZero(Number(match[1])),
				unit: <'px' | '%'>match[2].toLowerCase()
			};
		}

		if (/^[+-]?(?:0+(?:\.\d*)?|\.\d+)$/.test(token) && Number(token) === 0) {
			return { value: 0, unit: 'px' };
		}

		throw this.#syntaxError(
			"Failed to construct 'IntersectionObserver': rootMargin must be specified in pixels or percent."
		);
	}

	/**
	 * Normalizes threshold values to a sorted unique list.
	 *
	 * @param threshold Threshold.
	 * @returns Thresholds.
	 */
	#normalizeThresholds(threshold: IIntersectionObserverInit['threshold']): number[] {
		let values: unknown[];
		if (threshold === undefined) {
			values = [0];
		} else if (Array.isArray(threshold)) {
			values = threshold.length === 0 ? [0] : [...threshold];
		} else if (typeof threshold === 'number') {
			values = [threshold];
		} else {
			throw new this[PropertySymbol.window].RangeError(
				"Failed to construct 'IntersectionObserver': Threshold values must be numbers between 0 and 1"
			);
		}

		const thresholds: number[] = [];
		for (const value of values) {
			if (typeof value !== 'number' || !Number.isFinite(value) || value < 0 || value > 1) {
				throw new this[PropertySymbol.window].RangeError(
					"Failed to construct 'IntersectionObserver': Threshold values must be numbers between 0 and 1"
				);
			}
			const normalized = normalizeSignedZero(value);
			if (!thresholds.includes(normalized)) {
				thresholds.push(normalized);
			}
		}

		thresholds.sort((left, right) => left - right);
		return thresholds;
	}

	/**
	 * Queues an asynchronous intersection update.
	 */
	#scheduleUpdate(): void {
		if (this.#updateScheduled || this[PropertySymbol.window].closed) {
			return;
		}

		this.#updateScheduled = true;
		const generation = this.#generation;
		this[PropertySymbol.window].queueMicrotask(() => {
			this.#updateScheduled = false;
			if (generation !== this.#generation || this[PropertySymbol.window].closed) {
				return;
			}
			this[PropertySymbol.updateIntersectionObservations]();
		});
	}

	/**
	 * Delivers queued entries on a later microtask.
	 */
	#scheduleDelivery(): void {
		if (
			this.#deliveryScheduled ||
			this.#queued.length === 0 ||
			this[PropertySymbol.window].closed
		) {
			return;
		}

		this.#deliveryScheduled = true;
		const generation = this.#generation;
		this[PropertySymbol.window].queueMicrotask(() => {
			if (generation !== this.#generation) {
				return;
			}
			this.#deliveryScheduled = false;
			const records = this.#queued;
			this.#queued = [];
			if (records.length === 0) {
				return;
			}
			this.#callback.call(this, records, this);
		});
	}

	/**
	 * Measures one target and queues an entry when its threshold slot changes.
	 *
	 * @param registration Registration.
	 */
	#measure(registration: ITargetRegistration): void {
		const time = this[PropertySymbol.window].performance.now();
		const rootBounds = this.#rootBounds();
		const boundingClientRect = readRect((<Element>registration.target).getBoundingClientRect());
		const intersection = intersectRects(boundingClientRect, rootBounds);
		const isIntersecting = intersection !== null;
		const targetArea = rectArea(boundingClientRect);
		const intersectionArea = intersection ? rectArea(intersection) : 0;
		let intersectionRatio = 0;

		if (targetArea > 0) {
			intersectionRatio = intersectionArea / targetArea;
		} else if (isIntersecting) {
			intersectionRatio = 1;
		}

		if (intersectionRatio < 0) {
			intersectionRatio = 0;
		} else if (intersectionRatio > 1) {
			intersectionRatio = 1;
		}

		const thresholdIndex = indexForRatio(intersectionRatio, this.#thresholds);
		const changed =
			thresholdIndex !== registration.previousThresholdIndex ||
			isIntersecting !== registration.previousIsIntersecting;

		registration.previousThresholdIndex = thresholdIndex;
		registration.previousIsIntersecting = isIntersecting;

		if (!changed) {
			return;
		}

		const DOMRectConstructor = this[PropertySymbol.window].DOMRect || DOMRect;
		const intersectionRect = intersection ?? {
			x: 0,
			y: 0,
			width: 0,
			height: 0
		};

		this.#queued.push(
			new IntersectionObserverEntry({
				time,
				target: registration.target,
				isIntersecting,
				intersectionRatio,
				boundingClientRect: new DOMRectConstructor(
					boundingClientRect.x,
					boundingClientRect.y,
					boundingClientRect.width,
					boundingClientRect.height
				),
				intersectionRect: new DOMRectConstructor(
					intersectionRect.x,
					intersectionRect.y,
					intersectionRect.width,
					intersectionRect.height
				),
				rootBounds: new DOMRectConstructor(
					rootBounds.x,
					rootBounds.y,
					rootBounds.width,
					rootBounds.height
				)
			})
		);
	}

	/**
	 * Returns the root intersection rectangle after applying rootMargin.
	 * Percentages resolve against the undilated root width.
	 *
	 * @returns Root bounds.
	 */
	#rootBounds(): IRect {
		const base =
			this.#root === null || (<Node>(<unknown>this.#root)).nodeType === 9
				? viewportRect(this[PropertySymbol.window])
				: readRect((<Element>this.#root).getBoundingClientRect());
		const basis = base.right - base.left;
		const top = resolveMargin(this.#margins[0], basis);
		const right = resolveMargin(this.#margins[1], basis);
		const bottom = resolveMargin(this.#margins[2], basis);
		const left = resolveMargin(this.#margins[3], basis);
		return rectFromEdges(
			base.left - left,
			base.top - top,
			base.right + right,
			base.bottom + bottom
		);
	}

	/**
	 * Throws when a method argument is not an element.
	 *
	 * @param target Target.
	 * @param method Method name.
	 */
	#assertElement(target: Element, method: 'observe' | 'unobserve'): void {
		if (!(<Node>(<unknown>target)) || (<Node>(<unknown>target)).nodeType !== 1) {
			throw new this[PropertySymbol.window].TypeError(
				`Failed to execute '${method}' on 'IntersectionObserver': parameter 1 is not of type 'Element'.`
			);
		}
	}

	/**
	 * Returns a SyntaxError DOMException.
	 *
	 * @param message Message.
	 * @returns Exception.
	 */
	#syntaxError(message: string): DOMException {
		const DOMExceptionConstructor = this[PropertySymbol.window].DOMException || DOMException;
		return new DOMExceptionConstructor(message, DOMExceptionNameEnum.syntaxError);
	}
}

/**
 * Normalizes -0 to 0.
 *
 * @param value Value.
 * @returns Normalized number.
 */
function normalizeSignedZero(value: number): number {
	return Object.is(value, -0) ? 0 : value;
}

/**
 * Serializes one margin component.
 *
 * @param component Component.
 * @returns CSS component.
 */
function formatMargin(component: IMarginComponent): string {
	const value = normalizeSignedZero(component.value);
	const text = Object.is(value, Math.round(value)) ? String(Math.round(value)) : String(value);
	return `${text}${component.unit}`;
}

/**
 * Resolves a margin against the root width.
 *
 * @param component Component.
 * @param width Root width.
 * @returns Pixel offset.
 */
function resolveMargin(component: IMarginComponent, width: number): number {
	if (component.unit === '%') {
		return (component.value / 100) * width;
	}
	return component.value;
}

/**
 * Reads a finite number.
 *
 * @param value Value.
 * @returns Finite number.
 */
function finiteNumber(value: number | undefined): number {
	return typeof value === 'number' && Number.isFinite(value) ? value : 0;
}

/**
 * Reads a rectangle from a DOMRect-like object.
 *
 * @param source Source.
 * @returns Rectangle.
 */
function readRect(source: Partial<IRect> | null | undefined): IRect {
	const x = finiteNumber(source?.x);
	const y = finiteNumber(source?.y);
	const width = finiteNumber(source?.width);
	const height = finiteNumber(source?.height);
	const left = source?.left === undefined ? Math.min(x, x + width) : finiteNumber(source.left);
	const right = source?.right === undefined ? Math.max(x, x + width) : finiteNumber(source.right);
	const top = source?.top === undefined ? Math.min(y, y + height) : finiteNumber(source.top);
	const bottom =
		source?.bottom === undefined ? Math.max(y, y + height) : finiteNumber(source.bottom);
	return {
		x,
		y,
		width,
		height,
		left: Math.min(left, right),
		right: Math.max(left, right),
		top: Math.min(top, bottom),
		bottom: Math.max(top, bottom)
	};
}

/**
 * Builds a rectangle from edges.
 *
 * @param left Left.
 * @param top Top.
 * @param right Right.
 * @param bottom Bottom.
 * @returns Rectangle.
 */
function rectFromEdges(left: number, top: number, right: number, bottom: number): IRect {
	return {
		x: left,
		y: top,
		width: right - left,
		height: bottom - top,
		left,
		right,
		top,
		bottom
	};
}

/**
 * Returns the viewport rectangle.
 *
 * @param window Window.
 * @returns Viewport rectangle.
 */
function viewportRect(window: BrowserWindow): IRect {
	return rectFromEdges(0, 0, finiteNumber(window.innerWidth), finiteNumber(window.innerHeight));
}

/**
 * Returns the positive area of a rectangle.
 *
 * @param rect Rectangle.
 * @returns Area.
 */
function rectArea(rect: IRect): number {
	return Math.max(0, rect.right - rect.left) * Math.max(0, rect.bottom - rect.top);
}

/**
 * Intersects two rectangles. Edge contact counts as an intersection.
 *
 * @param target Target rectangle.
 * @param root Root rectangle.
 * @returns Intersection, or null when the rectangles are disjoint.
 */
function intersectRects(target: IRect, root: IRect): IRect | null {
	if (
		target.left > root.right ||
		target.right < root.left ||
		target.top > root.bottom ||
		target.bottom < root.top
	) {
		return null;
	}

	const left = Math.max(target.left, root.left);
	const right = Math.min(target.right, root.right);
	const top = Math.max(target.top, root.top);
	const bottom = Math.min(target.bottom, root.bottom);
	return rectFromEdges(left, top, right, bottom);
}

/**
 * Returns the spec threshold index for a ratio.
 *
 * @param ratio Intersection ratio.
 * @param thresholds Thresholds.
 * @returns Threshold index.
 */
function indexForRatio(ratio: number, thresholds: readonly number[]): number {
	for (let index = 0; index < thresholds.length; index++) {
		if (thresholds[index] > ratio) {
			return index;
		}
	}
	return thresholds.length;
}
