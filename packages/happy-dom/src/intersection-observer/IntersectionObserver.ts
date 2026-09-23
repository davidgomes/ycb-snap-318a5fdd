import * as PropertySymbol from '../PropertySymbol.js';
import type BrowserWindow from '../window/BrowserWindow.js';
import type Document from '../nodes/document/Document.js';
import type Element from '../nodes/element/Element.js';
import type DOMRect from '../dom/DOMRect.js';
import DOMExceptionNameEnum from '../exception/DOMExceptionNameEnum.js';
import NodeTypeEnum from '../nodes/node/NodeTypeEnum.js';
import type IMutationListener from '../mutation-observer/IMutationListener.js';
import type IntersectionObserverEntry from './IntersectionObserverEntry.js';
import type IIntersectionObserverInit from './IIntersectionObserverInit.js';
import IntersectionObserverGeometry from './IntersectionObserverGeometry.js';
import type { IIntersectionRect, IParsedRootMargin } from './IntersectionObserverGeometry.js';

interface IObservation {
	target: Element;
	previousThresholdIndex: number;
}

interface IComputedIntersection {
	targetRect: IIntersectionRect;
	rootBounds: IIntersectionRect;
	intersectionRect: IIntersectionRect;
	ratio: number;
	thresholdIndex: number;
	isIntersecting: boolean;
}

interface IObserverBridge {
	update(): boolean;
	deliver(): void;
	hasPending(): boolean;
}

/**
 * Schedules intersection updates and callback delivery for one window.
 */
class IntersectionObserverScheduler {
	#window: BrowserWindow;
	#bridges: IObserverBridge[] = [];
	#notifyScheduled = false;
	#updateScheduled = false;
	#listening = false;
	#elementCounts: Map<Element, number> = new Map();
	#onDocumentChange: () => void;
	#documentListener: IMutationListener;
	#elementListener: IMutationListener;

	/**
	 * Constructor.
	 *
	 * @param window Window.
	 */
	constructor(window: BrowserWindow) {
		this.#window = window;
		this.#onDocumentChange = (): void => {
			this.scheduleUpdate();
		};
		this.#documentListener = {
			options: {
				subtree: true,
				childList: true,
				attributes: true,
				characterData: true
			},
			callback: new WeakRef(this.#onDocumentChange)
		};
		this.#elementListener = {
			options: {
				subtree: false,
				childList: true,
				attributes: true,
				characterData: true
			},
			callback: new WeakRef(this.#onDocumentChange)
		};
	}

	/**
	 * Tracks an observer bridge.
	 *
	 * @param bridge Bridge.
	 */
	public register(bridge: IObserverBridge): void {
		if (!this.#bridges.includes(bridge)) {
			this.#bridges.push(bridge);
		}

		this.#startListening();
	}

	/**
	 * Stops tracking an observer bridge.
	 *
	 * @param bridge Bridge.
	 */
	public unregister(bridge: IObserverBridge): void {
		const index = this.#bridges.indexOf(bridge);

		if (index !== -1) {
			this.#bridges.splice(index, 1);
		}

		if (this.#bridges.length === 0) {
			this.#stopListening();
		}
	}

	/**
	 * Watches an element for geometry changes.
	 *
	 * @param element Element.
	 */
	public watch(element: Element): void {
		const count = this.#elementCounts.get(element) ?? 0;
		this.#elementCounts.set(element, count + 1);

		if (count === 0) {
			element[PropertySymbol.observeMutations](this.#elementListener);
		}
	}

	/**
	 * Stops watching an element.
	 *
	 * @param element Element.
	 */
	public unwatch(element: Element): void {
		const count = this.#elementCounts.get(element) ?? 0;

		if (count <= 1) {
			this.#elementCounts.delete(element);
			element[PropertySymbol.unobserveMutations](this.#elementListener);
			return;
		}

		this.#elementCounts.set(element, count - 1);
	}

	/**
	 * Delivers queued entries on a future microtask.
	 */
	public scheduleNotify(): void {
		if (this.#notifyScheduled || this.#window.closed) {
			return;
		}

		this.#notifyScheduled = true;
		this.#window.queueMicrotask(() => {
			this.#notifyScheduled = false;

			if (this.#window.closed) {
				return;
			}

			for (const bridge of this.#bridges.slice()) {
				if (bridge.hasPending()) {
					bridge.deliver();
				}
			}
		});
	}

	/**
	 * Recomputes intersections on a future microtask.
	 */
	public scheduleUpdate(): void {
		if (this.#updateScheduled || this.#window.closed) {
			return;
		}

		this.#updateScheduled = true;
		this.#window.queueMicrotask(() => {
			this.#updateScheduled = false;

			if (this.#window.closed) {
				return;
			}

			let queued = false;

			for (const bridge of this.#bridges.slice()) {
				if (bridge.update()) {
					queued = true;
				}
			}

			if (queued) {
				this.scheduleNotify();
			}
		});
	}

	/**
	 * Listens for DOM, scroll, and resize changes.
	 */
	#startListening(): void {
		if (this.#listening || this.#window.closed) {
			return;
		}

		this.#listening = true;
		this.#window.addEventListener('scroll', this.#onDocumentChange, true);
		this.#window.addEventListener('resize', this.#onDocumentChange);
		this.#window.document.addEventListener('scroll', this.#onDocumentChange, true);
		this.#window.document[PropertySymbol.observeMutations](this.#documentListener);
	}

	/**
	 * Removes window listeners.
	 */
	#stopListening(): void {
		if (!this.#listening) {
			return;
		}

		this.#listening = false;
		this.#window.removeEventListener('scroll', this.#onDocumentChange);
		this.#window.removeEventListener('resize', this.#onDocumentChange);
		this.#window.document.removeEventListener('scroll', this.#onDocumentChange);
		this.#window.document[PropertySymbol.unobserveMutations](this.#documentListener);
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
 * Returns true when the value is an element.
 *
 * @param value Value.
 * @returns Whether the value is an element.
 */
function isElementNode(value: unknown): value is Element {
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
 * @returns Whether the value is a document.
 */
function isDocumentNode(value: unknown): value is Document {
	return (
		!!value &&
		typeof value === 'object' &&
		(<Document>value)[PropertySymbol.nodeType] === NodeTypeEnum.documentNode
	);
}

/**
 * The IntersectionObserver interface provides a way to asynchronously observe changes in the intersection of a target element with an ancestor element or with the viewport.
 *
 * @see https://developer.mozilla.org/en-US/docs/Web/API/IntersectionObserver
 */
export default class IntersectionObserver {
	// Injected by WindowContextClassExtender
	protected declare [PropertySymbol.window]: BrowserWindow;
	#callback: (entries: IntersectionObserverEntry[], observer: IntersectionObserver) => void;
	#root: Element | Document | null = null;
	#rootElement: Element | null = null;
	#rootDocument: Document | null = null;
	#rootMargin: string;
	#margins: IParsedRootMargin['margins'];
	#thresholds: readonly number[];
	#observations: IObservation[] = [];
	#queued: IntersectionObserverEntry[] = [];
	#scheduler: IntersectionObserverScheduler;
	#bridge: IObserverBridge;
	#rootWatched = false;
	#destroyed = false;
	#epoch = 0;

	/**
	 * Constructor.
	 *
	 * @param callback Callback.
	 * @param [options] Options.
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

		const window = this[PropertySymbol.window];

		if (typeof callback !== 'function') {
			throw new window.TypeError(
				"Failed to construct 'IntersectionObserver': The callback provided as parameter 1 is not a function."
			);
		}

		if (
			options !== undefined &&
			options !== null &&
			(typeof options !== 'object' || Array.isArray(options))
		) {
			throw new window.TypeError(
				"Failed to construct 'IntersectionObserver': The provided value is not of type 'IntersectionObserverInit'."
			);
		}

		const init = options || {};
		const root = init.root;

		if (root !== undefined && root !== null && !isElementNode(root) && !isDocumentNode(root)) {
			throw new window.TypeError(
				"Failed to construct 'IntersectionObserver': The provided root is not an Element, Document, or null."
			);
		}

		if (init.rootMargin !== undefined && typeof init.rootMargin !== 'string') {
			throw new window.TypeError(
				"Failed to construct 'IntersectionObserver': rootMargin must be a string."
			);
		}

		const parsedMargin = IntersectionObserverGeometry.parseRootMargin(init.rootMargin ?? '0px');

		if (!parsedMargin) {
			throw new window.DOMException(
				"Failed to construct 'IntersectionObserver': rootMargin must be specified in pixels or percent.",
				DOMExceptionNameEnum.syntaxError
			);
		}

		let thresholds: number[];

		try {
			thresholds = IntersectionObserverGeometry.parseThresholds(init.threshold);
		} catch (error) {
			const message = error instanceof Error ? error.message : String(error);

			if (error instanceof RangeError) {
				throw new window.RangeError(message);
			}

			throw new window.TypeError(message);
		}

		this.#callback = callback;
		this.#root = root === undefined ? null : root;
		this.#rootElement = isElementNode(root) ? root : null;
		this.#rootDocument = isDocumentNode(root) ? root : null;
		this.#rootMargin = parsedMargin.serialized;
		this.#margins = parsedMargin.margins;
		this.#thresholds = Object.freeze(thresholds);
		this.#scheduler = getScheduler(window);
		this.#bridge = {
			update: (): boolean => this.#updateObservations(),
			deliver: (): void => this.#deliver(),
			hasPending: (): boolean => this.#queued.length > 0
		};
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
	 * Returns the root margin in four-value form.
	 *
	 * @returns Root margin.
	 */
	public get rootMargin(): string {
		return this.#rootMargin;
	}

	/**
	 * Returns the normalized thresholds.
	 *
	 * @returns Thresholds.
	 */
	public get thresholds(): readonly number[] {
		return this.#thresholds;
	}

	/**
	 * Starts observing a target.
	 * The initial entry is queued immediately and delivered asynchronously.
	 *
	 * @param target Target.
	 */
	public observe(target: Element): void {
		this.#assertElement(target, 'observe');

		if (
			this.#destroyed ||
			this.#observations.some((observation) => observation.target === target)
		) {
			return;
		}

		const observation: IObservation = {
			target,
			previousThresholdIndex: -1
		};

		this.#observations.push(observation);
		this.#scheduler.watch(target);
		this.#watchRoot();
		this.#scheduler.register(this.#bridge);

		const observers = this[PropertySymbol.window][PropertySymbol.intersectionObservers];

		if (!observers.includes(this)) {
			observers.push(this);
		}
		this.#recordObservation(observation, this.#getWindow().performance.now());
		this.#scheduler.scheduleNotify();
	}

	/**
	 * Stops observing a target and drops pending entries for it.
	 *
	 * @param target Target.
	 */
	public unobserve(target: Element): void {
		this.#assertElement(target, 'unobserve');

		const index = this.#observations.findIndex((observation) => observation.target === target);

		if (index === -1) {
			return;
		}

		this.#observations.splice(index, 1);
		this.#queued = this.#queued.filter((entry) => entry.target !== target);
		this.#scheduler.unwatch(target);

		if (this.#observations.length === 0) {
			this.#unwatchRoot();
			this.#scheduler.unregister(this.#bridge);
		}
	}

	/**
	 * Stops observing every target and clears pending records.
	 */
	public disconnect(): void {
		this.#epoch++;

		for (const observation of this.#observations) {
			this.#scheduler.unwatch(observation.target);
		}

		this.#observations = [];
		this.#queued = [];
		this.#unwatchRoot();
		this.#scheduler.unregister(this.#bridge);

		const observers = this[PropertySymbol.window][PropertySymbol.intersectionObservers];
		const index = observers.indexOf(this);

		if (index !== -1) {
			observers.splice(index, 1);
		}
	}

	/**
	 * Returns queued entries and clears the queue so they are not delivered.
	 *
	 * @returns Records.
	 */
	public takeRecords(): IntersectionObserverEntry[] {
		const entries = this.#queued;
		this.#queued = [];
		return entries;
	}

	/**
	 * Disconnects the observer when the window is destroyed.
	 */
	public [PropertySymbol.destroy](): void {
		if (this.#destroyed) {
			return;
		}

		this.#destroyed = true;
		this.disconnect();
	}

	/**
	 * Recomputes every target and queues entries whose threshold slot changed.
	 *
	 * @returns Whether any entry was queued.
	 */
	#updateObservations(): boolean {
		if (this.#destroyed) {
			return false;
		}

		const epoch = this.#epoch;
		const time = this.#getWindow().performance.now();
		let queued = false;

		for (const observation of this.#observations.slice()) {
			if (this.#epoch !== epoch) {
				return queued;
			}

			const before = this.#queued.length;
			this.#recordObservation(observation, time);

			if (this.#queued.length !== before) {
				queued = true;
			}
		}

		return queued;
	}

	/**
	 * Queues an entry when the target's threshold slot changes.
	 *
	 * @param observation Observation.
	 * @param time Time.
	 */
	#recordObservation(observation: IObservation, time: number): void {
		const computed = this.#compute(observation.target);

		if (computed.thresholdIndex === observation.previousThresholdIndex) {
			return;
		}

		observation.previousThresholdIndex = computed.thresholdIndex;
		this.#queued.push(this.#createEntry(observation.target, computed, time));
	}

	/**
	 * Computes the intersection of a target against the root.
	 *
	 * @param target Target.
	 * @returns Intersection.
	 */
	#compute(target: Element): IComputedIntersection {
		const window = this.#getWindow();
		const viewport = IntersectionObserverGeometry.viewportRect(
			window.innerWidth,
			window.innerHeight
		);
		const targetRect = IntersectionObserverGeometry.getElementRect(target, viewport);
		let rootRect = viewport;

		if (this.#rootElement) {
			rootRect = IntersectionObserverGeometry.getElementRect(this.#rootElement, viewport);
		} else if (this.#rootDocument?.defaultView) {
			rootRect = IntersectionObserverGeometry.viewportRect(
				this.#rootDocument.defaultView.innerWidth,
				this.#rootDocument.defaultView.innerHeight
			);
		}

		const rootBounds = IntersectionObserverGeometry.applyRootMargin(rootRect, this.#margins);
		const overlap = IntersectionObserverGeometry.intersection(targetRect, rootBounds);
		const targetArea = IntersectionObserverGeometry.area(targetRect);
		const overlapArea = IntersectionObserverGeometry.area(overlap);
		let ratio = 0;
		let isIntersecting = false;
		let intersectionRect = { x: 0, y: 0, width: 0, height: 0 };

		if (targetArea === 0) {
			if (IntersectionObserverGeometry.contains(rootBounds, targetRect)) {
				ratio = 1;
				isIntersecting = true;
				intersectionRect = overlap;
			}
		} else if (overlapArea > 0) {
			ratio = overlapArea / targetArea;

			if (!Number.isFinite(ratio) || ratio < 0) {
				ratio = 0;
			} else if (ratio > 1) {
				ratio = 1;
			}

			isIntersecting = ratio > 0;
			intersectionRect = overlap;
		}

		const thresholdIndex = IntersectionObserverGeometry.thresholdIndex(
			ratio,
			this.#thresholds,
			isIntersecting
		);

		return {
			targetRect,
			rootBounds,
			intersectionRect,
			ratio,
			thresholdIndex,
			isIntersecting
		};
	}

	/**
	 * Creates an intersection entry.
	 *
	 * @param target Target.
	 * @param computed Computed intersection.
	 * @param time Time.
	 * @returns Entry.
	 */
	#createEntry(
		target: Element,
		computed: IComputedIntersection,
		time: number
	): IntersectionObserverEntry {
		const window = this.#getWindow();
		const Entry = window.IntersectionObserverEntry;

		return new Entry({
			time,
			rootBounds: this.#toDOMRect(computed.rootBounds),
			boundingClientRect: this.#toDOMRect(computed.targetRect),
			intersectionRect: this.#toDOMRect(computed.intersectionRect),
			isIntersecting: computed.isIntersecting,
			intersectionRatio: computed.ratio,
			target
		});
	}

	/**
	 * Converts a rectangle into a DOMRect.
	 *
	 * @param rect Rectangle.
	 * @returns DOM rect.
	 */
	#toDOMRect(rect: IIntersectionRect): DOMRect {
		const normalized = IntersectionObserverGeometry.normalize(rect);
		const DOMRect = this.#getWindow().DOMRect;

		return new DOMRect(normalized.x, normalized.y, normalized.width, normalized.height);
	}

	/**
	 * Delivers queued entries to the callback.
	 */
	#deliver(): void {
		if (this.#destroyed || this.#queued.length === 0) {
			return;
		}

		const entries = this.#queued;
		this.#queued = [];

		try {
			this.#callback.call(this, entries, this);
		} catch (error) {
			this.#getWindow()[PropertySymbol.dispatchError](<Error>error);
		}
	}

	/**
	 * Watches the element root once per connected observation list.
	 */
	#watchRoot(): void {
		if (this.#rootWatched || !this.#rootElement) {
			return;
		}

		this.#rootWatched = true;
		this.#scheduler.watch(this.#rootElement);
	}

	/**
	 * Stops watching the element root.
	 */
	#unwatchRoot(): void {
		if (!this.#rootWatched || !this.#rootElement) {
			this.#rootWatched = false;
			return;
		}

		this.#rootWatched = false;
		this.#scheduler.unwatch(this.#rootElement);
	}

	/**
	 * Returns the owning window.
	 *
	 * @returns Window.
	 */
	#getWindow(): BrowserWindow {
		return this[PropertySymbol.window];
	}

	/**
	 * Throws when the value is not an element.
	 *
	 * @param target Target.
	 * @param method Method name.
	 */
	#assertElement(target: Element, method: 'observe' | 'unobserve'): void {
		if (!isElementNode(target)) {
			throw new this[PropertySymbol.window].TypeError(
				`Failed to execute '${method}' on 'IntersectionObserver': parameter 1 is not of type 'Element'.`
			);
		}
	}
}
