import Window from '../../src/window/Window.js';
import type Document from '../../src/nodes/document/Document.js';
import type Element from '../../src/nodes/element/Element.js';
import * as PropertySymbol from '../../src/PropertySymbol.js';
import { beforeEach, describe, it, expect, vi } from 'vitest';

const nextMicrotask = async (): Promise<void> => {
	await Promise.resolve();
	await Promise.resolve();
};

describe('IntersectionObserver', () => {
	let window: Window;
	let document: Document;

	beforeEach(() => {
		window = new Window({ width: 100, height: 100 });
		document = window.document;
	});

	const setRect = (
		element: Element,
		rect: { x: number; y: number; width: number; height: number }
	): void => {
		element.getBoundingClientRect = () =>
			new window.DOMRect(rect.x, rect.y, rect.width, rect.height);
	};

	describe('constructor()', () => {
		it('Throws if the callback is not a function.', () => {
			expect(() => new window.IntersectionObserver(<any>null)).toThrow(window.TypeError);
			expect(() => new window.IntersectionObserver(<any>undefined)).toThrow(window.TypeError);
		});

		it('Throws if root is invalid.', () => {
			expect(
				() => new window.IntersectionObserver(() => {}, { root: <any>document.createTextNode('x') })
			).toThrow(window.TypeError);
			expect(() => new window.IntersectionObserver(() => {}, { root: <any>{} })).toThrow(
				window.TypeError
			);
		});

		it('Throws if rootMargin is invalid.', () => {
			expect(() => new window.IntersectionObserver(() => {}, { rootMargin: '10' })).toThrow(
				window.SyntaxError
			);
			expect(() => new window.IntersectionObserver(() => {}, { rootMargin: '10em' })).toThrow(
				window.SyntaxError
			);
			expect(
				() => new window.IntersectionObserver(() => {}, { rootMargin: '1px 2px 3px 4px 5px' })
			).toThrow(window.SyntaxError);
		});

		it('Throws if threshold is out of range.', () => {
			expect(() => new window.IntersectionObserver(() => {}, { threshold: -0.1 })).toThrow(
				window.RangeError
			);
			expect(() => new window.IntersectionObserver(() => {}, { threshold: 1.1 })).toThrow(
				window.RangeError
			);
			expect(() => new window.IntersectionObserver(() => {}, { threshold: [0, 2] })).toThrow(
				window.RangeError
			);
		});

		it('Throws if threshold is not a number.', () => {
			expect(() => new window.IntersectionObserver(() => {}, { threshold: <any>NaN })).toThrow(
				window.TypeError
			);
		});

		it('Accepts a document or element root.', () => {
			const root = document.createElement('div');
			expect(new window.IntersectionObserver(() => {}, { root }).root).toBe(root);
			expect(new window.IntersectionObserver(() => {}, { root: document }).root).toBe(document);
			expect(new window.IntersectionObserver(() => {}).root).toBe(null);
		});

		it('Normalizes rootMargin to four values.', () => {
			expect(new window.IntersectionObserver(() => {}).rootMargin).toBe('0px 0px 0px 0px');
			expect(new window.IntersectionObserver(() => {}, { rootMargin: '10px' }).rootMargin).toBe(
				'10px 10px 10px 10px'
			);
			expect(new window.IntersectionObserver(() => {}, { rootMargin: '10px 5%' }).rootMargin).toBe(
				'10px 5% 10px 5%'
			);
			expect(
				new window.IntersectionObserver(() => {}, { rootMargin: '1px 2px 3px' }).rootMargin
			).toBe('1px 2px 3px 2px');
			expect(
				new window.IntersectionObserver(() => {}, { rootMargin: '-4px 5% 6px 7px' }).rootMargin
			).toBe('-4px 5% 6px 7px');
		});

		it('Normalizes thresholds to sorted unique values.', () => {
			expect(new window.IntersectionObserver(() => {}).thresholds).toEqual([0]);
			expect(new window.IntersectionObserver(() => {}, { threshold: 0.5 }).thresholds).toEqual([
				0.5
			]);
			expect(
				new window.IntersectionObserver(() => {}, { threshold: [0.5, 0, 0.5, 1] }).thresholds
			).toEqual([0, 0.5, 1]);
			expect(new window.IntersectionObserver(() => {}, { threshold: [] }).thresholds).toEqual([0]);
		});
	});

	describe('observe()', () => {
		it('Throws for an invalid target.', () => {
			const observer = new window.IntersectionObserver(() => {});
			expect(() => observer.observe(<any>undefined)).toThrow(window.TypeError);
			expect(() => observer.observe(<any>document.createTextNode('x'))).toThrow(window.TypeError);
		});

		it('Does not invoke the callback synchronously.', () => {
			const callback = vi.fn();
			const observer = new window.IntersectionObserver(callback);
			const div = document.createElement('div');

			observer.observe(div);

			expect(callback).not.toHaveBeenCalled();
		});

		it('Queues an initial entry for each newly observed target asynchronously.', async () => {
			const callback = vi.fn();
			const observer = new window.IntersectionObserver(callback);
			const first = document.createElement('div');
			const second = document.createElement('div');

			setRect(first, { x: 0, y: 0, width: 10, height: 10 });
			setRect(second, { x: 0, y: 0, width: 10, height: 10 });

			observer.observe(first);
			observer.observe(second);

			expect(callback).not.toHaveBeenCalled();

			await nextMicrotask();

			expect(callback).toHaveBeenCalledTimes(1);
			const entries = callback.mock.calls[0][0];
			expect(entries.map((entry: any) => entry.target)).toEqual([first, second]);
			expect(callback.mock.calls[0][1]).toBe(observer);
		});

		it('Does not queue another initial entry when the same target is observed twice.', async () => {
			const callback = vi.fn();
			const observer = new window.IntersectionObserver(callback);
			const div = document.createElement('div');
			setRect(div, { x: 0, y: 0, width: 10, height: 10 });

			observer.observe(div);
			observer.observe(div);
			await nextMicrotask();

			expect(callback).toHaveBeenCalledTimes(1);
			expect(callback.mock.calls[0][0]).toHaveLength(1);
		});
	});

	describe('takeRecords()', () => {
		it('Returns queued records and clears the queue.', async () => {
			const callback = vi.fn();
			const observer = new window.IntersectionObserver(callback);
			const div = document.createElement('div');
			setRect(div, { x: 0, y: 0, width: 10, height: 10 });

			observer.observe(div);

			const records = observer.takeRecords();
			expect(records).toHaveLength(1);
			expect(records[0].target).toBe(div);

			await nextMicrotask();
			expect(callback).not.toHaveBeenCalled();
			expect(observer.takeRecords()).toEqual([]);
		});
	});

	describe('intersection calculations', () => {
		it('Calculates viewport intersections.', async () => {
			const callback = vi.fn();
			const observer = new window.IntersectionObserver(callback);
			const div = document.createElement('div');
			setRect(div, { x: 50, y: 0, width: 100, height: 100 });

			observer.observe(div);
			await nextMicrotask();

			const entry = callback.mock.calls[0][0][0];
			expect(entry.isIntersecting).toBe(true);
			expect(entry.intersectionRatio).toBe(0.5);
			expect(entry.intersectionRect.width).toBe(50);
			expect(entry.intersectionRect.height).toBe(100);
			expect(entry.rootBounds.width).toBe(100);
			expect(entry.rootBounds.height).toBe(100);
		});

		it('Calculates element root intersections and pixel root margins.', async () => {
			const callback = vi.fn();
			const root = document.createElement('div');
			const target = document.createElement('div');
			setRect(root, { x: 0, y: 0, width: 50, height: 50 });
			setRect(target, { x: 40, y: 0, width: 20, height: 20 });

			const observer = new window.IntersectionObserver(callback, {
				root,
				rootMargin: '0px 10px 0px 0px'
			});

			observer.observe(target);
			await nextMicrotask();

			const entry = callback.mock.calls[0][0][0];
			expect(entry.isIntersecting).toBe(true);
			expect(entry.intersectionRatio).toBe(1);
			expect(entry.rootBounds.width).toBe(60);
		});

		it('Uses offset box when getBoundingClientRect is empty.', async () => {
			const callback = vi.fn();
			const observer = new window.IntersectionObserver(callback);
			const div = document.createElement('div');
			div[PropertySymbol.offsetWidth] = 20;
			div[PropertySymbol.offsetHeight] = 20;
			div[PropertySymbol.offsetLeft] = 0;
			div[PropertySymbol.offsetTop] = 0;

			observer.observe(div);
			await nextMicrotask();

			const entry = callback.mock.calls[0][0][0];
			expect(entry.intersectionRatio).toBe(1);
			expect(entry.boundingClientRect.width).toBe(20);
		});

		it('Treats a contained zero-area target as fully intersecting.', async () => {
			const callback = vi.fn();
			const observer = new window.IntersectionObserver(callback);
			const inside = document.createElement('div');
			const outside = document.createElement('div');
			setRect(inside, { x: 10, y: 10, width: 0, height: 0 });
			setRect(outside, { x: 200, y: 200, width: 0, height: 0 });

			observer.observe(inside);
			observer.observe(outside);
			await nextMicrotask();

			const [insideEntry, outsideEntry] = callback.mock.calls[0][0];
			expect(insideEntry.intersectionRatio).toBe(1);
			expect(insideEntry.isIntersecting).toBe(true);
			expect(outsideEntry.intersectionRatio).toBe(0);
			expect(outsideEntry.isIntersecting).toBe(false);
		});

		it('Queues a new entry when a target crosses a threshold.', () => {
			const observer = new window.IntersectionObserver(() => {}, { threshold: [0, 0.5, 1] });
			const div = document.createElement('div');
			setRect(div, { x: 0, y: 0, width: 100, height: 100 });
			observer.observe(div);
			expect(observer.takeRecords()[0].intersectionRatio).toBe(1);

			setRect(div, { x: 0, y: 60, width: 100, height: 100 });
			const next = observer.takeRecords();
			expect(next).toHaveLength(1);
			expect(next[0].intersectionRatio).toBe(0.4);
		});
	});

	describe('unobserve()', () => {
		it('Stops future entries for the target.', async () => {
			const callback = vi.fn();
			const observer = new window.IntersectionObserver(callback, { threshold: [0, 1] });
			const div = document.createElement('div');
			setRect(div, { x: 0, y: 0, width: 100, height: 100 });

			observer.observe(div);
			observer.unobserve(div);

			await nextMicrotask();
			expect(callback).not.toHaveBeenCalled();

			setRect(div, { x: 200, y: 0, width: 100, height: 100 });
			expect(observer.takeRecords()).toEqual([]);
		});

		it('Throws for an invalid target.', () => {
			const observer = new window.IntersectionObserver(() => {});
			expect(() => observer.unobserve(<any>undefined)).toThrow(window.TypeError);
		});
	});

	describe('disconnect()', () => {
		it('Stops future delivery and clears pending records.', async () => {
			const callback = vi.fn();
			const observer = new window.IntersectionObserver(callback);
			const div = document.createElement('div');
			setRect(div, { x: 0, y: 0, width: 10, height: 10 });

			observer.observe(div);
			observer.disconnect();

			expect(observer.takeRecords()).toEqual([]);
			await nextMicrotask();
			expect(callback).not.toHaveBeenCalled();
		});
	});
});
