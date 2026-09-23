import Window from '../../src/window/Window.js';
import type Document from '../../src/nodes/document/Document.js';
import type Element from '../../src/nodes/element/Element.js';
import DOMRect from '../../src/dom/DOMRect.js';
import IntersectionObserver from '../../src/intersection-observer/IntersectionObserver.js';
import type IntersectionObserverEntry from '../../src/intersection-observer/IntersectionObserverEntry.js';
import Event from '../../src/event/Event.js';
import { beforeEach, afterEach, describe, it, expect } from 'vitest';

describe('IntersectionObserver', () => {
	let window: Window;
	let document: Document;

	const setRect = (element: Element, x: number, y: number, width: number, height: number): void => {
		element.getBoundingClientRect = () => new DOMRect(x, y, width, height);
	};

	const createTarget = (x: number, y: number, width: number, height: number): Element => {
		const element = document.createElement('div');
		setRect(element, x, y, width, height);
		document.body.appendChild(element);
		return element;
	};

	const tick = (): Promise<void> => new Promise((resolve) => setTimeout(resolve, 0));

	beforeEach(() => {
		window = new Window({ width: 1000, height: 800 });
		document = window.document;
	});

	afterEach(async () => {
		await window.happyDOM.close();
	});

	describe('constructor()', () => {
		it('Sets default options.', () => {
			const observer = new window.IntersectionObserver(() => {});

			expect(observer.root).toBe(null);
			expect(observer.rootMargin).toBe('0px 0px 0px 0px');
			expect(observer.thresholds).toEqual([0]);
		});

		it('Supports a root element or document.', () => {
			const root = document.createElement('div');

			expect(new window.IntersectionObserver(() => {}, { root }).root).toBe(root);
			expect(new window.IntersectionObserver(() => {}, { root: document }).root).toBe(document);
			expect(new window.IntersectionObserver(() => {}, { root: null }).root).toBe(null);
		});

		it('Expands and normalizes "rootMargin".', () => {
			const getRootMargin = (rootMargin: string): string =>
				new window.IntersectionObserver(() => {}, { rootMargin }).rootMargin;

			expect(getRootMargin('10px')).toBe('10px 10px 10px 10px');
			expect(getRootMargin('10px 20%')).toBe('10px 20% 10px 20%');
			expect(getRootMargin('  1px   2px 3px ')).toBe('1px 2px 3px 2px');
			expect(getRootMargin('1px -2px 3.5% 4PX')).toBe('1px -2px 3.5% 4px');
			expect(getRootMargin('')).toBe('0px 0px 0px 0px');
		});

		it('Normalizes "threshold" to sorted unique values.', () => {
			const getThresholds = (threshold: number | number[]): ReadonlyArray<number> =>
				new window.IntersectionObserver(() => {}, { threshold }).thresholds;

			expect(getThresholds(0.5)).toEqual([0.5]);
			expect(getThresholds([1, 0, 0.5, 0.5, 0.25])).toEqual([0, 0.25, 0.5, 1]);
			expect(getThresholds([])).toEqual([0]);
		});

		it('Throws an error for an invalid callback.', () => {
			for (const callback of [undefined, null, {}, 'callback']) {
				expect(() => new window.IntersectionObserver(<any>callback)).toThrow(
					new TypeError(
						`Failed to construct 'IntersectionObserver': parameter 1 is not of type 'Function'.`
					)
				);
			}
		});

		it('Throws an error for an invalid root.', () => {
			for (const root of [{}, 'root', 1, document.createTextNode('text')]) {
				expect(() => new window.IntersectionObserver(() => {}, { root: <any>root })).toThrow(
					TypeError
				);
			}
		});

		it('Throws an error for an invalid "rootMargin".', () => {
			for (const rootMargin of ['10', '10em', 'auto', '1px 2px 3px 4px 5px', '10 px', 'px']) {
				let error: Error | null = null;
				try {
					new window.IntersectionObserver(() => {}, { rootMargin });
				} catch (e) {
					error = <Error>e;
				}
				expect(error).toBeInstanceOf(window.DOMException);
				expect(error?.name).toBe('SyntaxError');
				expect(error?.message).toBe(
					`Failed to construct 'IntersectionObserver': rootMargin must be specified in pixels or percent.`
				);
			}
		});

		it('Throws an error for an invalid "threshold".', () => {
			for (const threshold of [-0.1, 1.1, [0, 2]]) {
				expect(() => new window.IntersectionObserver(() => {}, { threshold })).toThrow(
					new RangeError(
						`Failed to construct 'IntersectionObserver': Threshold values must be numbers between 0 and 1`
					)
				);
			}

			for (const threshold of [NaN, Infinity, [0, NaN], <any>'abc']) {
				expect(() => new window.IntersectionObserver(() => {}, { threshold })).toThrow(TypeError);
			}
		});
	});

	describe('observe()', () => {
		it('Throws an error for an invalid target.', () => {
			const observer = new window.IntersectionObserver(() => {});

			for (const target of [undefined, null, {}, document, document.createTextNode('text')]) {
				expect(() => observer.observe(<any>target)).toThrow(
					new TypeError(
						`Failed to execute 'observe' on 'IntersectionObserver': parameter 1 is not of type 'Element'.`
					)
				);
			}
		});

		it('Delivers an initial entry asynchronously.', async () => {
			const target = createTarget(0, 0, 100, 100);
			const calls: Array<{ entries: IntersectionObserverEntry[]; observer: IntersectionObserver }> =
				[];
			const observer = new window.IntersectionObserver((entries, observer) =>
				calls.push({ entries, observer })
			);

			observer.observe(target);

			expect(calls.length).toBe(0);

			await tick();

			expect(calls.length).toBe(1);
			expect(calls[0].observer).toBe(observer);
			expect(calls[0].entries.length).toBe(1);

			const entry = calls[0].entries[0];

			expect(entry).toBeInstanceOf(window.IntersectionObserverEntry);
			expect(entry.target).toBe(target);
			expect(entry.isIntersecting).toBe(true);
			expect(entry.intersectionRatio).toBe(1);
			expect(entry.boundingClientRect).toEqual(new DOMRect(0, 0, 100, 100));
			expect(entry.intersectionRect).toEqual(new DOMRect(0, 0, 100, 100));
			expect(entry.rootBounds).toEqual(new DOMRect(0, 0, 1000, 800));
			expect(typeof entry.time).toBe('number');
		});

		it('Delivers entries for targets in observation order in a single callback.', async () => {
			const targets = [
				createTarget(0, 0, 10, 10),
				createTarget(0, 2000, 10, 10),
				createTarget(0, 20, 10, 10)
			];
			const calls: IntersectionObserverEntry[][] = [];
			const observer = new window.IntersectionObserver((entries) => calls.push(entries));

			observer.observe(targets[1]);
			observer.observe(targets[0]);
			observer.observe(targets[2]);

			await tick();

			expect(calls.length).toBe(1);
			expect(calls[0].map((entry) => entry.target)).toEqual([targets[1], targets[0], targets[2]]);
			expect(calls[0].map((entry) => entry.isIntersecting)).toEqual([false, true, true]);
		});

		it('Ignores a target that is already observed.', async () => {
			const target = createTarget(0, 0, 10, 10);
			const calls: IntersectionObserverEntry[][] = [];
			const observer = new window.IntersectionObserver((entries) => calls.push(entries));

			observer.observe(target);
			observer.observe(target);
			await tick();
			observer.observe(target);
			await tick();

			expect(calls.length).toBe(1);
			expect(calls[0].length).toBe(1);
		});

		it('Works when constructed outside of a window context.', async () => {
			const target = createTarget(0, 0, 10, 10);
			const calls: IntersectionObserverEntry[][] = [];
			const observer = new IntersectionObserver((entries) => calls.push(entries));

			observer.observe(target);
			await tick();

			expect(calls.length).toBe(1);
			expect(calls[0][0].rootBounds).toEqual(new DOMRect(0, 0, 1000, 800));
		});
	});

	describe('Intersection calculation', () => {
		const getEntry = async (
			target: Element,
			options?: ConstructorParameters<typeof IntersectionObserver>[1]
		): Promise<IntersectionObserverEntry> => {
			const observer = new window.IntersectionObserver(() => {}, options);
			observer.observe(target);
			const records = observer.takeRecords();
			observer.disconnect();
			expect(records.length).toBe(1);
			return records[0];
		};

		it('Calculates a partial intersection with the viewport.', async () => {
			const entry = await getEntry(createTarget(900, 700, 200, 200));

			expect(entry.isIntersecting).toBe(true);
			expect(entry.intersectionRatio).toBe(0.25);
			expect(entry.intersectionRect).toEqual(new DOMRect(900, 700, 100, 100));
		});

		it('Reports no intersection for a target outside of the viewport.', async () => {
			const entry = await getEntry(createTarget(0, 900, 100, 100));

			expect(entry.isIntersecting).toBe(false);
			expect(entry.intersectionRatio).toBe(0);
			expect(entry.intersectionRect).toEqual(new DOMRect(0, 0, 0, 0));
			expect(entry.boundingClientRect).toEqual(new DOMRect(0, 900, 100, 100));
		});

		it('Treats an edge-adjacent target as intersecting with a ratio of 0.', async () => {
			const entry = await getEntry(createTarget(0, 800, 100, 100));

			expect(entry.isIntersecting).toBe(true);
			expect(entry.intersectionRatio).toBe(0);
		});

		it('Calculates intersection with a root element.', async () => {
			const root = document.createElement('div');
			const target = document.createElement('div');
			root.appendChild(target);
			document.body.appendChild(root);
			setRect(root, 100, 100, 200, 200);
			setRect(target, 250, 100, 100, 100);

			const entry = await getEntry(target, { root });

			expect(entry.isIntersecting).toBe(true);
			expect(entry.intersectionRatio).toBe(0.5);
			expect(entry.rootBounds).toEqual(new DOMRect(100, 100, 200, 200));
			expect(entry.intersectionRect).toEqual(new DOMRect(250, 100, 50, 100));
		});

		it('Uses the viewport for a document root.', async () => {
			const entry = await getEntry(createTarget(0, 0, 10, 10), { root: document });

			expect(entry.isIntersecting).toBe(true);
			expect(entry.rootBounds).toEqual(new DOMRect(0, 0, 1000, 800));
		});

		it('Reports no intersection for a target that is not a descendant of the root element.', async () => {
			const root = document.createElement('div');
			document.body.appendChild(root);
			setRect(root, 0, 0, 1000, 1000);

			const entry = await getEntry(createTarget(0, 0, 100, 100), { root });

			expect(entry.isIntersecting).toBe(false);
			expect(entry.intersectionRatio).toBe(0);
			expect(entry.boundingClientRect).toEqual(new DOMRect(0, 0, 0, 0));
		});

		it('Supports a root element in a shadow tree ancestor chain.', async () => {
			const root = document.createElement('div');
			const host = document.createElement('div');
			const target = document.createElement('div');
			root.appendChild(host);
			host.attachShadow({ mode: 'open' }).appendChild(target);
			document.body.appendChild(root);
			setRect(root, 0, 0, 100, 100);
			setRect(target, 0, 0, 50, 50);

			const entry = await getEntry(target, { root });

			expect(entry.isIntersecting).toBe(true);
			expect(entry.intersectionRatio).toBe(1);
		});

		it('Expands the root by a pixel root margin.', async () => {
			const target = createTarget(0, 800, 100, 100);

			const entry = await getEntry(target, { rootMargin: '50px' });

			expect(entry.rootBounds).toEqual(new DOMRect(-50, -50, 1100, 900));
			expect(entry.isIntersecting).toBe(true);
			expect(entry.intersectionRatio).toBe(0.5);

			const shrunk = await getEntry(createTarget(0, 0, 100, 100), {
				rootMargin: '-50px 0px 0px 0px'
			});

			expect(shrunk.rootBounds).toEqual(new DOMRect(0, 50, 1000, 750));
			expect(shrunk.intersectionRatio).toBe(0.5);
		});

		it('Resolves percentage root margins against the root size.', async () => {
			const root = document.createElement('div');
			const target = document.createElement('div');
			root.appendChild(target);
			document.body.appendChild(root);
			setRect(root, 0, 0, 200, 100);
			setRect(target, 0, 0, 10, 10);

			const entry = await getEntry(target, { root, rootMargin: '10% 50%' });

			expect(entry.rootBounds).toEqual(new DOMRect(-100, -10, 400, 120));
		});

		it('Reports a ratio of 1 for zero-area targets inside the root and 0 outside.', async () => {
			const inside = await getEntry(createTarget(50, 50, 0, 0));

			expect(inside.isIntersecting).toBe(true);
			expect(inside.intersectionRatio).toBe(1);

			const line = await getEntry(createTarget(50, 50, 0, 100));

			expect(line.isIntersecting).toBe(true);
			expect(line.intersectionRatio).toBe(1);

			const outside = await getEntry(createTarget(2000, 2000, 0, 0));

			expect(outside.isIntersecting).toBe(false);
			expect(outside.intersectionRatio).toBe(0);
		});

		it('Supports plain object rects.', async () => {
			const target = document.createElement('div');
			document.body.appendChild(target);
			target.getBoundingClientRect = () =>
				<DOMRect>(<unknown>{ top: 700, left: 0, width: 100, height: 200 });

			const entry = await getEntry(target);

			expect(entry.intersectionRatio).toBe(0.5);
			expect(entry.boundingClientRect).toEqual(new DOMRect(0, 700, 100, 200));
		});
	});

	describe('Threshold crossing', () => {
		it('Queues new entries only when a threshold is crossed.', async () => {
			const target = createTarget(0, 1000, 100, 100);
			const calls: IntersectionObserverEntry[][] = [];
			const observer = new window.IntersectionObserver((entries) => calls.push(entries), {
				threshold: [0.5, 1]
			});

			observer.observe(target);
			await tick();

			expect(calls.length).toBe(1);
			expect(calls[0][0].isIntersecting).toBe(false);

			// 25% visible: intersecting, but below the 0.5 threshold.
			setRect(target, 0, 775, 100, 100);
			window.dispatchEvent(new Event('scroll'));
			await tick();

			expect(calls.length).toBe(2);
			expect(calls[1][0].isIntersecting).toBe(true);
			expect(calls[1][0].intersectionRatio).toBe(0.25);

			// 40% visible: still below the 0.5 threshold.
			setRect(target, 0, 760, 100, 100);
			window.dispatchEvent(new Event('scroll'));
			await tick();

			expect(calls.length).toBe(2);

			// 60% visible: crosses 0.5.
			setRect(target, 0, 740, 100, 100);
			window.dispatchEvent(new Event('scroll'));
			await tick();

			expect(calls.length).toBe(3);
			expect(calls[2][0].intersectionRatio).toBeCloseTo(0.6);

			// Fully visible: crosses 1.
			setRect(target, 0, 0, 100, 100);
			window.dispatchEvent(new Event('scroll'));
			await tick();

			expect(calls.length).toBe(4);
			expect(calls[3][0].intersectionRatio).toBe(1);
		});

		it('Re-evaluates on element scroll, window resize and DOM mutations.', async () => {
			const target = createTarget(0, 900, 100, 100);
			const calls: IntersectionObserverEntry[][] = [];
			const observer = new window.IntersectionObserver((entries) => calls.push(entries));

			observer.observe(target);
			await tick();

			setRect(target, 0, 0, 100, 100);
			target.dispatchEvent(new Event('scroll'));
			await tick();

			expect(calls.length).toBe(2);
			expect(calls[1][0].isIntersecting).toBe(true);

			window.happyDOM.setViewport({ width: 50, height: 50 });
			setRect(target, 60, 0, 100, 100);
			window.dispatchEvent(new Event('resize'));
			await tick();

			expect(calls.length).toBe(3);
			expect(calls[2][0].isIntersecting).toBe(false);

			setRect(target, 0, 0, 100, 100);
			target.setAttribute('class', 'visible');
			await tick();

			expect(calls.length).toBe(4);
			expect(calls[3][0].isIntersecting).toBe(true);
		});
	});

	describe('unobserve()', () => {
		it('Throws an error for an invalid target.', () => {
			const observer = new window.IntersectionObserver(() => {});

			expect(() => observer.unobserve(<any>null)).toThrow(TypeError);
		});

		it('Stops future entries for the target.', async () => {
			const target1 = createTarget(0, 900, 10, 10);
			const target2 = createTarget(0, 900, 10, 10);
			const calls: IntersectionObserverEntry[][] = [];
			const observer = new window.IntersectionObserver((entries) => calls.push(entries));

			observer.observe(target1);
			observer.observe(target2);
			await tick();

			expect(calls.length).toBe(1);
			expect(calls[0].length).toBe(2);

			observer.unobserve(target1);
			setRect(target1, 0, 0, 10, 10);
			setRect(target2, 0, 0, 10, 10);
			window.dispatchEvent(new Event('scroll'));
			await tick();

			expect(calls.length).toBe(2);
			expect(calls[1].map((entry) => entry.target)).toEqual([target2]);
		});

		it('Discards pending entries for the target.', async () => {
			const target1 = createTarget(0, 0, 10, 10);
			const target2 = createTarget(0, 0, 10, 10);
			const calls: IntersectionObserverEntry[][] = [];
			const observer = new window.IntersectionObserver((entries) => calls.push(entries));

			observer.observe(target1);
			observer.observe(target2);
			observer.unobserve(target1);
			await tick();

			expect(calls.length).toBe(1);
			expect(calls[0].map((entry) => entry.target)).toEqual([target2]);
		});
	});

	describe('disconnect()', () => {
		it('Stops delivery and clears pending records.', async () => {
			const target = createTarget(0, 0, 10, 10);
			const calls: IntersectionObserverEntry[][] = [];
			const observer = new window.IntersectionObserver((entries) => calls.push(entries));

			observer.observe(target);
			observer.disconnect();
			await tick();

			expect(calls.length).toBe(0);
			expect(observer.takeRecords()).toEqual([]);

			setRect(target, 0, 900, 10, 10);
			window.dispatchEvent(new Event('scroll'));
			await tick();

			expect(calls.length).toBe(0);
		});

		it('Allows the observer to be reused.', async () => {
			const target = createTarget(0, 0, 10, 10);
			const calls: IntersectionObserverEntry[][] = [];
			const observer = new window.IntersectionObserver((entries) => calls.push(entries));

			observer.observe(target);
			await tick();
			observer.disconnect();
			observer.observe(target);
			await tick();

			expect(calls.length).toBe(2);
			expect(calls[1][0].target).toBe(target);
		});
	});

	describe('takeRecords()', () => {
		it('Returns an empty array when nothing is observed.', () => {
			const observer = new window.IntersectionObserver(() => {});

			expect(observer.takeRecords()).toEqual([]);
		});

		it('Returns pending entries, which are then not delivered to the callback.', async () => {
			const target1 = createTarget(0, 0, 10, 10);
			const target2 = createTarget(0, 900, 10, 10);
			const calls: IntersectionObserverEntry[][] = [];
			const observer = new window.IntersectionObserver((entries) => calls.push(entries));

			observer.observe(target1);
			observer.observe(target2);

			const records = observer.takeRecords();

			expect(records.map((entry) => entry.target)).toEqual([target1, target2]);
			expect(observer.takeRecords()).toEqual([]);

			await tick();

			expect(calls.length).toBe(0);
		});
	});
});
