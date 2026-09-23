import * as PropertySymbol from '../PropertySymbol.js';
import IntersectionObserverEntry from './IntersectionObserverEntry.js';
import type IIntersectionObserverInit from './IIntersectionObserverInit.js';
import type Element from '../nodes/element/Element.js';
import type Document from '../nodes/document/Document.js';
import type Node from '../nodes/node/Node.js';
import type BrowserWindow from '../window/BrowserWindow.js';
import NodeTypeEnum from '../nodes/node/NodeTypeEnum.js';
import DOMRect from '../dom/DOMRect.js';
import DOMException from '../exception/DOMException.js';
import DOMExceptionNameEnum from '../exception/DOMExceptionNameEnum.js';

const TIMER = {
	setInterval: globalThis.setInterval.bind(globalThis),
	clearInterval: globalThis.clearInterval.bind(globalThis)
};

/**
 * Geometry changes made without a scroll or resize event (e.g. a mocked getBoundingClientRect()) are picked up by polling.
 * The timer is not registered in the async task manager, so waitUntilComplete() is not blocked by it.
 */
const POLL_INTERVAL = 16;

const ROOT_MARGIN_REGEXP = /^(-?(?:\d+\.?\d*|\.\d+)(?:e[+-]?\d+)?)(px|%)$/i;

interface IMarginValue {
	value: number;
	unit: 'px' | '%';
}

interface IBox {
	top: number;
	right: number;
	bottom: number;
	left: number;
}

interface IObservationTarget {
	target: Element;
	previousThresholdIndex: number;
	previousIsIntersecting: boolean;
}

/**
 * The IntersectionObserver interface of the Intersection Observer API provides a way to asynchronously observe changes in the intersection of a target element with an ancestor element or with a top-level document's viewport.
 *
 * @see https://developer.mozilla.org/en-US/docs/Web/API/IntersectionObserver
 */
export default class IntersectionObserver {
	#callback: (entries: IntersectionObserverEntry[], observer: IntersectionObserver) => void;
	#root: Element | Document | null;
	#rootMargin: [IMarginValue, IMarginValue, IMarginValue, IMarginValue];
	#thresholds: number[];
	#observationTargets: IObservationTarget[] = [];
	#queuedEntries: IntersectionObserverEntry[] = [];
	#isUpdateScheduled = false;
	#pollInterval: NodeJS.Timeout | null = null;
	#windowListeners: Map<BrowserWindow, () => void> = new Map();

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

		if (
			root !== null &&
			(typeof root !== 'object' ||
				((<Node>root)[PropertySymbol.nodeType] !== NodeTypeEnum.elementNode &&
					(<Node>root)[PropertySymbol.nodeType] !== NodeTypeEnum.documentNode))
		) {
			throw new TypeError(
				`Failed to construct 'IntersectionObserver': Failed to read the 'root' property from 'IntersectionObserverInit': The provided value is not of type '(Document or Element)'.`
			);
		}

		this.#callback = callback;
		this.#root = root;
		this.#rootMargin = this.#parseRootMargin(
			options?.rootMargin === undefined ? '0px' : String(options.rootMargin)
		);
		this.#thresholds = this.#parseThresholds(
			options?.threshold === undefined ? 0 : options.threshold
		);
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
	 * Returns thresholds sorted in increasing order.
	 *
	 * @returns Thresholds.
	 */
	public get thresholds(): ReadonlyArray<number> {
		return Object.freeze(this.#thresholds.slice());
	}

	/**
	 * Starts observing.
	 *
	 * @param target Target.
	 */
	public observe(target: Element): void {
		if (
			!target ||
			typeof target !== 'object' ||
			(<Node>target)[PropertySymbol.nodeType] !== NodeTypeEnum.elementNode
		) {
			throw new TypeError(
				`Failed to execute 'observe' on 'IntersectionObserver': parameter 1 is not of type 'Element'.`
			);
		}

		if (this.#observationTargets.some((observation) => observation.target === target)) {
			return;
		}

		this.#observationTargets.push({
			target,
			previousThresholdIndex: -1,
			previousIsIntersecting: false
		});

		this.#listenToWindow(target[PropertySymbol.window]);
		this.#startPolling();
		this.#scheduleUpdate(target[PropertySymbol.window]);
	}

	/**
	 * Unobserves an element.
	 *
	 * @param target Target.
	 */
	public unobserve(target: Element): void {
		if (
			!target ||
			typeof target !== 'object' ||
			(<Node>target)[PropertySymbol.nodeType] !== NodeTypeEnum.elementNode
		) {
			throw new TypeError(
				`Failed to execute 'unobserve' on 'IntersectionObserver': parameter 1 is not of type 'Element'.`
			);
		}

		const index = this.#observationTargets.findIndex(
			(observation) => observation.target === target
		);

		if (index === -1) {
			return;
		}

		this.#observationTargets.splice(index, 1);
		this.#queuedEntries = this.#queuedEntries.filter((entry) => entry.target !== target);

		if (!this.#observationTargets.length) {
			this.#stopObserving();
		}
	}

	/**
	 * Disconnects.
	 */
	public disconnect(): void {
		this.#observationTargets = [];
		this.#queuedEntries = [];
		this.#stopObserving();
	}

	/**
	 * Returns an array of IntersectionObserverEntry objects for all observed targets.
	 *
	 * @returns Records.
	 */
	public takeRecords(): IntersectionObserverEntry[] {
		this.#updateObservations();
		const entries = this.#queuedEntries;
		this.#queuedEntries = [];
		return entries;
	}

	/**
	 * Parses root margin.
	 *
	 * @param rootMargin Root margin.
	 * @returns Root margin in the order top, right, bottom, left.
	 */
	#parseRootMargin(rootMargin: string): [IMarginValue, IMarginValue, IMarginValue, IMarginValue] {
		const tokens = rootMargin.trim().split(/\s+/).filter(Boolean);

		if (!tokens.length) {
			tokens.push('0px');
		}

		if (tokens.length > 4) {
			throw new DOMException(
				`Failed to construct 'IntersectionObserver': Extra text found at the end of rootMargin.`,
				DOMExceptionNameEnum.syntaxError
			);
		}

		const values: IMarginValue[] = tokens.map((token) => {
			const match = token.match(ROOT_MARGIN_REGEXP);
			if (!match) {
				throw new DOMException(
					`Failed to construct 'IntersectionObserver': rootMargin must be specified in pixels or percent.`,
					DOMExceptionNameEnum.syntaxError
				);
			}
			return {
				value: Number(match[1]) || 0,
				unit: <'px' | '%'>match[2].toLowerCase()
			};
		});

		const top = values[0];
		const right = values[1] ?? top;
		const bottom = values[2] ?? top;
		const left = values[3] ?? right;

		return [top, right, bottom, left];
	}

	/**
	 * Parses thresholds.
	 *
	 * @param threshold Threshold.
	 * @returns Sorted unique thresholds.
	 */
	#parseThresholds(threshold: number | number[]): number[] {
		const values = Array.isArray(threshold) ? threshold : [threshold];
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
			if (!thresholds.includes(number)) {
				thresholds.push(number);
			}
		}

		if (!thresholds.length) {
			thresholds.push(0);
		}

		return thresholds.sort((a, b) => a - b);
	}

	/**
	 * Schedules an update of all observations followed by delivery of queued entries.
	 *
	 * @param window Window.
	 */
	#scheduleUpdate(window: BrowserWindow): void {
		if (this.#isUpdateScheduled || !window || window.closed) {
			return;
		}
		this.#isUpdateScheduled = true;
		window.queueMicrotask(() => {
			this.#isUpdateScheduled = false;
			this.#updateObservations();
			this.#deliverEntries();
		});
	}

	/**
	 * Calls the callback with queued entries.
	 */
	#deliverEntries(): void {
		if (!this.#queuedEntries.length) {
			return;
		}
		const entries = this.#queuedEntries;
		this.#queuedEntries = [];
		this.#callback.call(this, entries, this);
	}

	/**
	 * Runs the intersection observation steps for all targets and queues entries for changed targets.
	 */
	#updateObservations(): void {
		for (const observation of this.#observationTargets) {
			const window = observation.target[PropertySymbol.window];

			if (!window || window.closed) {
				continue;
			}

			const boundingClientRect = observation.target.getBoundingClientRect();
			const targetBox = this.#getBox(boundingClientRect);
			const rootBox = this.#getRootBox(window);
			let intersectionBox: IBox | null = null;

			if (this.#isTargetInRootScope(observation.target) && rootBox) {
				const box = {
					top: Math.max(targetBox.top, rootBox.top),
					right: Math.min(targetBox.right, rootBox.right),
					bottom: Math.min(targetBox.bottom, rootBox.bottom),
					left: Math.max(targetBox.left, rootBox.left)
				};
				if (box.left <= box.right && box.top <= box.bottom) {
					intersectionBox = box;
				}
			}

			const isIntersecting = intersectionBox !== null;
			const targetArea = (targetBox.right - targetBox.left) * (targetBox.bottom - targetBox.top);
			let intersectionRatio: number;

			if (targetArea > 0) {
				const intersectionArea = intersectionBox
					? (intersectionBox.right - intersectionBox.left) *
						(intersectionBox.bottom - intersectionBox.top)
					: 0;
				intersectionRatio = Math.min(1, intersectionArea / targetArea);
			} else {
				intersectionRatio = isIntersecting ? 1 : 0;
			}

			const thresholds = this.#thresholds;
			let thresholdIndex = thresholds.findIndex((threshold) => threshold > intersectionRatio);

			if (thresholdIndex === -1) {
				thresholdIndex = thresholds.length;
			}

			if (
				thresholdIndex === observation.previousThresholdIndex &&
				isIntersecting === observation.previousIsIntersecting
			) {
				continue;
			}

			observation.previousThresholdIndex = thresholdIndex;
			observation.previousIsIntersecting = isIntersecting;

			this.#queuedEntries.push(
				new IntersectionObserverEntry({
					time: window.performance.now(),
					rootBounds: rootBox ? this.#getDOMRect(rootBox) : null,
					boundingClientRect: this.#getDOMRect(targetBox),
					intersectionRect: intersectionBox
						? this.#getDOMRect(intersectionBox)
						: new DOMRect(0, 0, 0, 0),
					isIntersecting,
					intersectionRatio,
					target: observation.target
				})
			);
		}
	}

	/**
	 * Returns true if the target can intersect with the root.
	 *
	 * @param target Target.
	 * @returns True if in scope.
	 */
	#isTargetInRootScope(target: Element): boolean {
		if (!target.isConnected) {
			return false;
		}

		const root = this.#root;

		if (root === null) {
			return true;
		}

		if (root[PropertySymbol.nodeType] === NodeTypeEnum.documentNode) {
			return target[PropertySymbol.ownerDocument] === root;
		}

		return root.isConnected && root !== target && root.contains(target);
	}

	/**
	 * Returns the root intersection rectangle with the root margin applied.
	 *
	 * @param targetWindow Window of the target.
	 * @returns Box.
	 */
	#getRootBox(targetWindow: BrowserWindow): IBox | null {
		const root = this.#root;
		let box: IBox;

		if (root === null || root[PropertySymbol.nodeType] === NodeTypeEnum.documentNode) {
			const window = root ? root[PropertySymbol.window] : targetWindow;
			if (!window) {
				return null;
			}
			box = { top: 0, left: 0, right: window.innerWidth, bottom: window.innerHeight };
		} else {
			box = this.#getBox((<Element>root).getBoundingClientRect());
		}

		const width = box.right - box.left;
		const height = box.bottom - box.top;
		const [top, right, bottom, left] = this.#rootMargin;
		const resolve = (margin: IMarginValue, size: number): number =>
			margin.unit === '%' ? (margin.value / 100) * size : margin.value;

		return {
			top: box.top - resolve(top, height),
			right: box.right + resolve(right, width),
			bottom: box.bottom + resolve(bottom, height),
			left: box.left - resolve(left, width)
		};
	}

	/**
	 * Returns a normalized box from a rectangle.
	 *
	 * @param rect Rectangle.
	 * @returns Box.
	 */
	#getBox(rect: Partial<DOMRect> | null | undefined): IBox {
		const x = Number(rect?.x ?? rect?.left ?? 0) || 0;
		const y = Number(rect?.y ?? rect?.top ?? 0) || 0;
		const width = Number(rect?.width ?? 0) || 0;
		const height = Number(rect?.height ?? 0) || 0;
		return {
			top: Math.min(y, y + height),
			right: Math.max(x, x + width),
			bottom: Math.max(y, y + height),
			left: Math.min(x, x + width)
		};
	}

	/**
	 * Returns a DOMRect from a box.
	 *
	 * @param box Box.
	 * @returns DOMRect.
	 */
	#getDOMRect(box: IBox): DOMRect {
		return new DOMRect(box.left, box.top, box.right - box.left, box.bottom - box.top);
	}

	/**
	 * Listens for scroll and resize events on a window.
	 *
	 * @param window Window.
	 */
	#listenToWindow(window: BrowserWindow): void {
		if (!window || this.#windowListeners.has(window)) {
			return;
		}
		const listener = (): void => this.#scheduleUpdate(window);
		window.addEventListener('scroll', listener, { capture: true });
		window.addEventListener('resize', listener);
		this.#windowListeners.set(window, listener);
	}

	/**
	 * Starts polling for geometry changes.
	 */
	#startPolling(): void {
		if (this.#pollInterval) {
			return;
		}
		const interval = TIMER.setInterval(() => {
			const observation = this.#observationTargets.find(
				(observation) =>
					observation.target[PropertySymbol.window] &&
					!observation.target[PropertySymbol.window].closed
			);
			if (!observation) {
				this.#stopObserving();
				return;
			}
			this.#scheduleUpdate(observation.target[PropertySymbol.window]);
		}, POLL_INTERVAL);
		interval.unref?.();
		this.#pollInterval = interval;
	}

	/**
	 * Stops polling and removes window listeners.
	 */
	#stopObserving(): void {
		if (this.#pollInterval) {
			TIMER.clearInterval(this.#pollInterval);
			this.#pollInterval = null;
		}
		for (const [window, listener] of this.#windowListeners) {
			window.removeEventListener('scroll', listener);
			window.removeEventListener('resize', listener);
		}
		this.#windowListeners.clear();
	}
}
