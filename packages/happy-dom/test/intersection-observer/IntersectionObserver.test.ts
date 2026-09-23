import Window from '../../src/window/Window.js';
import type Document from '../../src/nodes/document/Document.js';
import type Element from '../../src/nodes/element/Element.js';
import DOMRect from '../../src/dom/DOMRect.js';
import DOMException from '../../src/exception/DOMException.js';
import type IntersectionObserverEntry from '../../src/intersection-observer/IntersectionObserverEntry.js';
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
		it('Throws if callback is not a function.', () => {
			expect(() => new window.IntersectionObserver(<any>null)).toThrow(TypeError);
			expect(() => new window.IntersectionObserver(<any>{})).toThrow(
				"Failed to construct 'IntersectionObserver': parameter 1 is not of type 'Function'."
			);
		});

		it('Throws if root is invalid.', () => {
			expect(() => new window.IntersectionObserver(() => {}, { root: <any>{} })).toThrow(TypeError);
			expect(
				() => new window.IntersectionObserver(() => {}, { root: <any>document.createTextNode('') })
			).toThrow(TypeError);
		});

		it('Throws a SyntaxError if rootMargin is invalid.', () => {
			for (const rootMargin of ['10', '10em', 'foo', '1px 2px 3px 4px 5px', '10px,20px']) {
				let error: Error | null = null;
				try {
					new window.IntersectionObserver(() => {}, { rootMargin });
				} catch (e) {
					error = <Error>e;
				}
				expect(error).toBeInstanceOf(DOMException);
				expect(error?.name).toBe('SyntaxError');
			}
		});

		it('Throws if threshold is invalid.', () => {
			expect(() => new window.IntersectionObserver(() => {}, { threshold: 1.5 })).toThrow(
				RangeError
			);
			expect(() => new window.IntersectionObserver(() => {}, { threshold: [0, -0.1] })).toThrow(
				RangeError
			);
			expect(() => new window.IntersectionObserver(() => {}, { threshold: NaN })).toThrow(
				TypeError
			);
		});

		it('Has default options.', () => {
			const observer = new window.IntersectionObserver(() => {});
			expect(observer.root).toBe(null);
			expect(observer.rootMargin).toBe('0px 0px 0px 0px');
			expect(observer.thresholds).toEqual([0]);
		});

		it('Normalizes rootMargin with shorthand expansion.', () => {
			const rootMargin = (value: string): string =>
				new window.IntersectionObserver(() => {}, { rootMargin: value }).rootMargin;
			expect(rootMargin('10px')).toBe('10px 10px 10px 10px');
			expect(rootMargin('10px 20%')).toBe('10px 20% 10px 20%');
			expect(rootMargin('1px 2px 3px')).toBe('1px 2px 3px 2px');
			expect(rootMargin(' 1px  2px 3px -4px ')).toBe('1px 2px 3px -4px');
			expect(rootMargin('1.5PX')).toBe('1.5px 1.5px 1.5px 1.5px');
		});

		it('Normalizes thresholds to sorted unique values.', () => {
			expect(new window.IntersectionObserver(() => {}, { threshold: 0.5 }).thresholds).toEqual([
				0.5
			]);
			expect(
				new window.IntersectionObserver(() => {}, { threshold: [1, 0.5, 0, 0.5] }).thresholds
			).toEqual([0, 0.5, 1]);
			expect(new window.IntersectionObserver(() => {}, { threshold: [] }).thresholds).toEqual([0]);
		});

		it('Exposes root.', () => {
			const root = document.createElement('div');
			expect(new window.IntersectionObserver(() => {}, { root }).root).toBe(root);
		});
	});

	describe('observe()', () => {
		it('Throws if target is not an element.', () => {
			const observer = new window.IntersectionObserver(() => {});
			expect(() => observer.observe(<any>null)).toThrow(
				"Failed to execute 'observe' on 'IntersectionObserver': parameter 1 is not of type 'Element'."
			);
			expect(() => observer.observe(<any>document.createTextNode(''))).toThrow(TypeError);
		});

		it('Delivers initial entries asynchronously in observation order.', async () => {
			const calls: IntersectionObserverEntry[][] = [];
			const first = createTarget(0, 0, 100, 100);
			const second = createTarget(0, 2000, 100, 100);
			const third = createTarget(950, 0, 100, 100);
			const observer = new window.IntersectionObserver((entries, instance) => {
				expect(instance).toBe(observer);
				calls.push(entries);
			});

			observer.observe(first);
			observer.observe(second);
			observer.observe(third);
			observer.observe(first);

			expect(calls.length).toBe(0);

			await tick();

			expect(calls.length).toBe(1);
			expect(calls[0].map((entry) => entry.target)).toEqual([first, second, third]);

			const [a, b, c] = calls[0];

			expect(a.isIntersecting).toBe(true);
			expect(a.intersectionRatio).toBe(1);
			expect(a.boundingClientRect).toEqual(new DOMRect(0, 0, 100, 100));
			expect(a.intersectionRect).toEqual(new DOMRect(0, 0, 100, 100));
			expect(a.rootBounds).toEqual(new DOMRect(0, 0, 1000, 800));
			expect(typeof a.time).toBe('number');

			expect(b.isIntersecting).toBe(false);
			expect(b.intersectionRatio).toBe(0);
			expect(b.intersectionRect).toEqual(new DOMRect(0, 0, 0, 0));

			expect(c.isIntersecting).toBe(true);
			expect(c.intersectionRatio).toBe(0.5);
			expect(c.intersectionRect).toEqual(new DOMRect(950, 0, 50, 100));

			await tick();
			expect(calls.length).toBe(1);
		});

		it('Reports disconnected targets as not intersecting.', async () => {
			const target = document.createElement('div');
			setRect(target, 0, 0, 100, 100);
			const entries: IntersectionObserverEntry[] = [];
			const observer = new window.IntersectionObserver((e) => entries.push(...e));
			observer.observe(target);
			await tick();
			expect(entries.length).toBe(1);
			expect(entries[0].isIntersecting).toBe(false);
		});

		it('Queues new entries when a threshold is crossed.', async () => {
			const target = createTarget(0, 900, 100, 100);
			const entries: IntersectionObserverEntry[] = [];
			const observer = new window.IntersectionObserver((e) => entries.push(...e), {
				threshold: [0, 0.5, 1]
			});

			observer.observe(target);
			await tick();
			expect(entries.length).toBe(1);
			expect(entries[0].isIntersecting).toBe(false);

			setRect(target, 0, 775, 100, 100);
			window.dispatchEvent(new window.Event('scroll'));
			await tick();
			expect(entries.length).toBe(2);
			expect(entries[1].isIntersecting).toBe(true);
			expect(entries[1].intersectionRatio).toBe(0.25);

			setRect(target, 0, 780, 100, 100);
			window.dispatchEvent(new window.Event('scroll'));
			await tick();
			expect(entries.length).toBe(2);

			setRect(target, 0, 740, 100, 100);
			window.dispatchEvent(new window.Event('scroll'));
			await tick();
			expect(entries.length).toBe(3);
			expect(entries[2].intersectionRatio).toBe(0.6);

			setRect(target, 0, 0, 100, 100);
			window.dispatchEvent(new window.Event('resize'));
			await tick();
			expect(entries.length).toBe(4);
			expect(entries[3].intersectionRatio).toBe(1);
		});

		it('Detects geometry changes without scroll or resize events.', async () => {
			const target = createTarget(0, 900, 100, 100);
			const entries: IntersectionObserverEntry[] = [];
			const observer = new window.IntersectionObserver((e) => entries.push(...e));

			observer.observe(target);
			await tick();
			expect(entries.length).toBe(1);

			setRect(target, 0, 0, 100, 100);
			await new Promise((resolve) => setTimeout(resolve, 50));
			expect(entries.length).toBe(2);
			expect(entries[1].isIntersecting).toBe(true);

			observer.disconnect();
		});

		it('Supports an element root.', async () => {
			const root = createTarget(100, 100, 200, 200);
			const inside = document.createElement('div');
			const partial = document.createElement('div');
			const outsideRoot = createTarget(0, 0, 50, 50);
			setRect(inside, 150, 150, 50, 50);
			setRect(partial, 250, 100, 100, 100);
			root.appendChild(inside);
			root.appendChild(partial);

			const entries: IntersectionObserverEntry[] = [];
			const observer = new window.IntersectionObserver((e) => entries.push(...e), { root });

			observer.observe(inside);
			observer.observe(partial);
			observer.observe(outsideRoot);
			await tick();

			expect(entries.map((entry) => entry.intersectionRatio)).toEqual([1, 0.5, 0]);
			expect(entries.map((entry) => entry.isIntersecting)).toEqual([true, true, false]);
			expect(entries[0].rootBounds).toEqual(new DOMRect(100, 100, 200, 200));
			expect(entries[1].intersectionRect).toEqual(new DOMRect(250, 100, 50, 100));
		});

		it('Applies pixel and percentage root margins.', async () => {
			const target = createTarget(0, 850, 100, 100);
			const entries: IntersectionObserverEntry[] = [];
			const observer = new window.IntersectionObserver((e) => entries.push(...e), {
				rootMargin: '0px 0px 100px 0px'
			});
			observer.observe(target);
			await tick();
			expect(entries[0].rootBounds).toEqual(new DOMRect(0, 0, 1000, 900));
			expect(entries[0].intersectionRatio).toBe(0.5);

			const negativeEntries: IntersectionObserverEntry[] = [];
			const negativeObserver = new window.IntersectionObserver((e) => negativeEntries.push(...e), {
				rootMargin: '-10%'
			});
			const topTarget = createTarget(0, 0, 100, 100);
			negativeObserver.observe(topTarget);
			await tick();
			expect(negativeEntries[0].rootBounds).toEqual(new DOMRect(100, 80, 800, 640));
			expect(negativeEntries[0].intersectionRect).toEqual(new DOMRect(100, 80, 0, 20));
			expect(negativeEntries[0].intersectionRatio).toBe(0);
			expect(negativeEntries[0].isIntersecting).toBe(true);
		});

		it('Handles zero-area targets.', async () => {
			const inside = createTarget(10, 10, 0, 0);
			const outside = createTarget(2000, 10, 0, 0);
			const entries: IntersectionObserverEntry[] = [];
			const observer = new window.IntersectionObserver((e) => entries.push(...e));
			observer.observe(inside);
			observer.observe(outside);
			await tick();
			expect(entries[0].intersectionRatio).toBe(1);
			expect(entries[0].isIntersecting).toBe(true);
			expect(entries[1].intersectionRatio).toBe(0);
			expect(entries[1].isIntersecting).toBe(false);
		});
	});

	describe('unobserve()', () => {
		it('Throws if target is not an element.', () => {
			const observer = new window.IntersectionObserver(() => {});
			expect(() => observer.unobserve(<any>undefined)).toThrow(TypeError);
		});

		it('Stops entries for the target.', async () => {
			const first = createTarget(0, 0, 100, 100);
			const second = createTarget(0, 0, 100, 100);
			const entries: IntersectionObserverEntry[] = [];
			const observer = new window.IntersectionObserver((e) => entries.push(...e));

			observer.observe(first);
			observer.observe(second);
			observer.unobserve(first);
			await tick();
			expect(entries.map((entry) => entry.target)).toEqual([second]);

			observer.observe(first);
			await tick();
			expect(entries.map((entry) => entry.target)).toEqual([second, first]);

			observer.unobserve(first);
			setRect(first, 0, 2000, 100, 100);
			window.dispatchEvent(new window.Event('scroll'));
			await tick();
			expect(entries.length).toBe(2);
		});
	});

	describe('disconnect()', () => {
		it('Clears pending records and stops delivery.', async () => {
			const target = createTarget(0, 0, 100, 100);
			let calls = 0;
			const observer = new window.IntersectionObserver(() => calls++);

			observer.observe(target);
			observer.disconnect();
			await tick();
			expect(calls).toBe(0);
			expect(observer.takeRecords()).toEqual([]);

			setRect(target, 0, 2000, 100, 100);
			window.dispatchEvent(new window.Event('scroll'));
			await tick();
			expect(calls).toBe(0);
		});
	});

	describe('takeRecords()', () => {
		it('Returns empty array when nothing is observed.', () => {
			const observer = new window.IntersectionObserver(() => {});
			expect(observer.takeRecords()).toEqual([]);
		});

		it('Returns pending records and prevents their delivery.', async () => {
			const target = createTarget(0, 0, 100, 100);
			let calls = 0;
			const observer = new window.IntersectionObserver(() => calls++);

			observer.observe(target);
			const records = observer.takeRecords();
			expect(records.length).toBe(1);
			expect(records[0].target).toBe(target);
			expect(observer.takeRecords()).toEqual([]);

			await tick();
			expect(calls).toBe(0);
		});
	});
});
