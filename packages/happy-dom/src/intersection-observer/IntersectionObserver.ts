import IntersectionObserverEntry from './IntersectionObserverEntry.js';
import type IIntersectionObserverInit from './IIntersectionObserverInit.js';
import Element from '../nodes/element/Element.js';
import Document from '../nodes/document/Document.js';
import DOMRect from '../dom/DOMRect.js';
import DOMException from '../exception/DOMException.js';
import DOMExceptionNameEnum from '../exception/DOMExceptionNameEnum.js';
import type BrowserWindow from '../window/BrowserWindow.js';

type IMargin = { value: number; unit: 'px' | '%' };

interface IObservation {
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
	#rootMargin: IMargin[];
	#thresholds: number[];
	#observations: IObservation[] = [];
	#records: IntersectionObserverEntry[] = [];
	#scheduled = false;
	#window: BrowserWindow | null = null;
	#listener = (): void => {
		this.#updateObservations();
	};

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
				"Failed to construct 'IntersectionObserver': parameter 1 is not of type 'Function'."
			);
		}
		const init = options || {};
		const root = init.root ?? null;
		if (root !== null && !(root instanceof Element) && !(root instanceof Document)) {
			throw new TypeError(
				"Failed to construct 'IntersectionObserver': Failed to read the 'root' property from 'IntersectionObserverInit': The provided value is not of type '(Document or Element)'."
			);
		}
		this.#callback = callback;
		this.#root = root;
		this.#rootMargin = IntersectionObserver.#parseRootMargin(
			init.rootMargin === undefined ? '0px' : String(init.rootMargin)
		);
		this.#thresholds = IntersectionObserver.#parseThresholds(
			init.threshold === undefined ? 0 : init.threshold
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
	 * Returns root margin.
	 *
	 * @returns Root margin.
	 */
	public get rootMargin(): string {
		return this.#rootMargin.map((margin) => `${margin.value}${margin.unit}`).join(' ');
	}

	/**
	 * Returns thresholds.
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
		if (!(target instanceof Element)) {
			throw new TypeError(
				"Failed to execute 'observe' on 'IntersectionObserver': parameter 1 is not of type 'Element'."
			);
		}
		if (this.#observations.some((observation) => observation.target === target)) {
			return;
		}
		if (!this.#window) {
			const window = <BrowserWindow | null>(target.ownerDocument?.defaultView ?? null);
			if (window) {
				this.#window = window;
				window.addEventListener('scroll', this.#listener, { capture: true });
				window.addEventListener('resize', this.#listener);
			}
		}
		this.#observations.push({ target, previousThresholdIndex: -1, previousIsIntersecting: false });
		this.#scheduleUpdate();
	}

	/**
	 * Disconnects.
	 */
	public disconnect(): void {
		this.#observations = [];
		this.#records = [];
		this.#removeListeners();
	}

	/**
	 * Unobserves an element.
	 *
	 * @param target Target.
	 */
	public unobserve(target: Element): void {
		if (!(target instanceof Element)) {
			throw new TypeError(
				"Failed to execute 'unobserve' on 'IntersectionObserver': parameter 1 is not of type 'Element'."
			);
		}
		this.#observations = this.#observations.filter((observation) => observation.target !== target);
		this.#records = this.#records.filter((record) => record.target !== target);
		if (!this.#observations.length) {
			this.#removeListeners();
		}
	}

	/**
	 * Returns an array of IntersectionObserverEntry objects for all observed targets.
	 *
	 * @returns Records.
	 */
	public takeRecords(): IntersectionObserverEntry[] {
		const records = this.#records;
		this.#records = [];
		return records;
	}

	/**
	 * Schedules an asynchronous update and delivery.
	 */
	#scheduleUpdate(): void {
		if (this.#scheduled) {
			return;
		}
		this.#scheduled = true;
		const run = (): void => {
			this.#scheduled = false;
			this.#updateObservations();
		};
		if (this.#window) {
			this.#window.setTimeout(run, 0);
		} else {
			setTimeout(run, 0);
		}
	}

	/**
	 * Computes intersections, queues entries and delivers them.
	 */
	#updateObservations(): void {
		for (const observation of this.#observations) {
			const entry = this.#computeEntry(observation.target);
			const thresholdIndex = entry.isIntersecting
				? this.#thresholds.filter((threshold) => threshold <= entry.intersectionRatio).length
				: 0;
			if (
				observation.previousThresholdIndex !== thresholdIndex ||
				observation.previousIsIntersecting !== entry.isIntersecting
			) {
				observation.previousThresholdIndex = thresholdIndex;
				observation.previousIsIntersecting = entry.isIntersecting;
				this.#records.push(entry);
			}
		}
		if (this.#records.length) {
			const records = this.takeRecords();
			this.#callback.call(this, records, this);
		}
	}

	/**
	 * Computes an entry for a target.
	 *
	 * @param target Target.
	 * @returns Entry.
	 */
	#computeEntry(target: Element): IntersectionObserverEntry {
		const window = this.#window;
		const targetRect = target.getBoundingClientRect();
		let rootRect: { left: number; top: number; right: number; bottom: number };

		if (this.#root instanceof Element) {
			const rect = this.#root.getBoundingClientRect();
			rootRect = { left: rect.left, top: rect.top, right: rect.right, bottom: rect.bottom };
		} else {
			rootRect = {
				left: 0,
				top: 0,
				right: window ? window.innerWidth : 0,
				bottom: window ? window.innerHeight : 0
			};
		}

		const width = rootRect.right - rootRect.left;
		const height = rootRect.bottom - rootRect.top;
		const resolve = (margin: IMargin, base: number): number =>
			margin.unit === '%' ? (margin.value / 100) * base : margin.value;
		rootRect = {
			top: rootRect.top - resolve(this.#rootMargin[0], height),
			right: rootRect.right + resolve(this.#rootMargin[1], width),
			bottom: rootRect.bottom + resolve(this.#rootMargin[2], height),
			left: rootRect.left - resolve(this.#rootMargin[3], width)
		};

		const left = Math.max(targetRect.left, rootRect.left);
		const top = Math.max(targetRect.top, rootRect.top);
		const right = Math.min(targetRect.right, rootRect.right);
		const bottom = Math.min(targetRect.bottom, rootRect.bottom);

		const isInRoot =
			target.isConnected &&
			(this.#root === null ||
				(this.#root !== target && this.#root.contains(target)) ||
				(this.#root instanceof Document && target.ownerDocument === this.#root));
		const isIntersecting = isInRoot && right >= left && bottom >= top;

		const targetArea = targetRect.width * targetRect.height;
		let intersectionRatio = 0;
		let intersectionRect = new DOMRect(0, 0, 0, 0);
		if (isIntersecting) {
			intersectionRect = new DOMRect(left, top, right - left, bottom - top);
			intersectionRatio =
				targetArea > 0
					? Math.min(1, (intersectionRect.width * intersectionRect.height) / targetArea)
					: 1;
		}

		return new IntersectionObserverEntry({
			boundingClientRect: targetRect,
			intersectionRatio,
			intersectionRect,
			isIntersecting,
			rootBounds: new DOMRect(
				rootRect.left,
				rootRect.top,
				rootRect.right - rootRect.left,
				rootRect.bottom - rootRect.top
			),
			target,
			time: window ? window.performance.now() : Date.now()
		});
	}

	/**
	 * Removes window listeners.
	 */
	#removeListeners(): void {
		if (this.#window) {
			this.#window.removeEventListener('scroll', this.#listener);
			this.#window.removeEventListener('resize', this.#listener);
			this.#window = null;
		}
	}

	/**
	 * Parses root margin.
	 *
	 * @param rootMargin Root margin.
	 * @returns Margins (top, right, bottom, left).
	 */
	static #parseRootMargin(rootMargin: string): IMargin[] {
		const parts = rootMargin.trim().split(/\s+/).filter(Boolean);
		const error = (): DOMException =>
			new DOMException(
				"Failed to construct 'IntersectionObserver': rootMargin must be specified in pixels or percent.",
				DOMExceptionNameEnum.syntaxError
			);
		if (!parts.length || parts.length > 4) {
			throw error();
		}
		const margins = parts.map((part): IMargin => {
			const match = part.match(/^([-+]?(?:\d+\.?\d*|\.\d+)(?:e[-+]?\d+)?)(px|%)?$/i);
			if (!match || (!match[2] && parseFloat(match[1]) !== 0)) {
				throw error();
			}
			return {
				value: parseFloat(match[1]),
				unit: match[2] === '%' ? '%' : 'px'
			};
		});
		const [top, right = top, bottom = top, left = right] = margins;
		return [top, right, bottom, left];
	}

	/**
	 * Parses thresholds.
	 *
	 * @param threshold Threshold.
	 * @returns Sorted unique thresholds.
	 */
	static #parseThresholds(threshold: number | number[]): number[] {
		const list = (Array.isArray(threshold) ? threshold : [threshold]).map(Number);
		for (const value of list) {
			if (isNaN(value) || value < 0 || value > 1) {
				throw new RangeError(
					"Failed to construct 'IntersectionObserver': Threshold values must be numbers between 0 and 1"
				);
			}
		}
		const result = Array.from(new Set(list)).sort((a, b) => a - b);
		return result.length ? result : [0];
	}
}
