import Window from '../../src/window/Window.js';
import type Document from '../../src/nodes/document/Document.js';
import type Element from '../../src/nodes/element/Element.js';
import type IntersectionObserverEntry from '../../src/intersection-observer/IntersectionObserverEntry.js';
import { beforeEach, afterEach, describe, it, expect } from 'vitest';

interface IRectLike {
	x: number;
	y: number;
	width: number;
	height: number;
}

/**
 * Flushes queued microtasks and the macrotask used for delivery.
 *
 * @returns Promise.
 */
function flushAsync(): Promise<void> {
	return new Promise((resolve) => {
		setTimeout(resolve, 0);
	});
}

describe('IntersectionObserver', () => {
	let window: Window;
	let document: Document;

	beforeEach(() => {
		window = new Window({ width: 1000, height: 500 });
		document = window.document;
	});

	afterEach(async () => {
		await window.happyDOM.close();
	});

	describe('constructor', () => {
		it('Throws when the callback is missing or not a function.', () => {
			expect(() => new (<{ new (): unknown }>window.IntersectionObserver)()).toThrow(
				window.TypeError
			);
			expect(() => new window.IntersectionObserver(<never>null)).toThrow(window.TypeError);
			expect(() => new window.IntersectionObserver(<never>{})).toThrow(window.TypeError);
		});

		it('Throws when root is not an element, document, or null.', () => {
			expect(
				() =>
					new window.IntersectionObserver(() => {}, {
						root: <never>document.createTextNode('nope')
					})
			).toThrow(window.TypeError);
			expect(() => new window.IntersectionObserver(() => {}, { root: <never>1 })).toThrow(
				window.TypeError
			);
		});

		it('Throws a SyntaxError when rootMargin cannot be parsed.', () => {
			expect(() => new window.IntersectionObserver(() => {}, { rootMargin: '10' })).toThrow(
				window.DOMException
			);
			expect(() => new window.IntersectionObserver(() => {}, { rootMargin: '10em' })).toThrow(
				window.DOMException
			);
			expect(
				() => new window.IntersectionObserver(() => {}, { rootMargin: '1px 2px 3px 4px 5px' })
			).toThrow(window.DOMException);

			try {
				new window.IntersectionObserver(() => {}, { rootMargin: '10em' });
			} catch (error) {
				expect((<Error>error).name).toBe('SyntaxError');
			}
		});

		it('Throws a RangeError when a threshold is outside 0 to 1.', () => {
			expect(() => new window.IntersectionObserver(() => {}, { threshold: -0.01 })).toThrow(
				window.RangeError
			);
			expect(() => new window.IntersectionObserver(() => {}, { threshold: 1.01 })).toThrow(
				window.RangeError
			);
			expect(() => new window.IntersectionObserver(() => {}, { threshold: Number.NaN })).toThrow(
				window.RangeError
			);
			expect(() => new window.IntersectionObserver(() => {}, { threshold: [0, 2] })).toThrow(
				window.RangeError
			);
		});

		it('Exposes a normalized four-value rootMargin.', () => {
			expect(new window.IntersectionObserver(() => {}).rootMargin).toBe('0px 0px 0px 0px');
			expect(new window.IntersectionObserver(() => {}, { rootMargin: '10px' }).rootMargin).toBe(
				'10px 10px 10px 10px'
			);
			expect(
				new window.IntersectionObserver(() => {}, { rootMargin: '10px 20px' }).rootMargin
			).toBe('10px 20px 10px 20px');
			expect(
				new window.IntersectionObserver(() => {}, { rootMargin: '10px 20px 30px' }).rootMargin
			).toBe('10px 20px 30px 20px');
			expect(
				new window.IntersectionObserver(() => {}, { rootMargin: '1px 2px 3px 4px' }).rootMargin
			).toBe('1px 2px 3px 4px');
			expect(
				new window.IntersectionObserver(() => {}, { rootMargin: '  10%  0  -5px ' }).rootMargin
			).toBe('10% 0px -5px 0px');
			expect(new window.IntersectionObserver(() => {}, { rootMargin: '' }).rootMargin).toBe(
				'0px 0px 0px 0px'
			);
			expect(new window.IntersectionObserver(() => {}, { root: null }).root).toBe(null);
		});

		it('Exposes thresholds as a sorted unique list.', () => {
			expect(new window.IntersectionObserver(() => {}).thresholds).toEqual([0]);
			expect(new window.IntersectionObserver(() => {}, { threshold: [] }).thresholds).toEqual([0]);
			expect(new window.IntersectionObserver(() => {}, { threshold: 1 }).thresholds).toEqual([1]);
			expect(
				new window.IntersectionObserver(() => {}, { threshold: [1, 0.25, 0, 0.25, 0.5] }).thresholds
			).toEqual([0, 0.25, 0.5, 1]);
		});

		it('Accepts a document root.', () => {
			const observer = new window.IntersectionObserver(() => {}, { root: document });
			expect(observer.root).toBe(document);
		});
	});

	describe('observe()', () => {
		it('Throws when the argument is not an element.', () => {
			const observer = new window.IntersectionObserver(() => {});
			expect(() => observer.observe(<never>null)).toThrow(window.TypeError);
			expect(() => observer.observe(<never>document)).toThrow(window.TypeError);
			expect(() => observer.observe(<never>document.createTextNode('x'))).toThrow(window.TypeError);
		});

		it('Does not invoke the callback synchronously and queues an initial entry.', async () => {
			const target = mockRect(document.createElement('div'), {
				x: 0,
				y: 0,
				width: 100,
				height: 100
			});
			const entries: IntersectionObserverEntry[][] = [];
			const observer = new window.IntersectionObserver((records, instance) => {
				expect(instance).toBe(observer);
				entries.push(records);
			});

			observer.observe(target);
			expect(entries).toEqual([]);

			await Promise.resolve();
			const pending = observer.takeRecords();
			expect(pending).toHaveLength(1);
			expect(pending[0].target).toBe(target);
			expect(pending[0].isIntersecting).toBe(true);
			expect(pending[0].intersectionRatio).toBe(1);
			expect(rectOf(pending[0].boundingClientRect)).toEqual({
				x: 0,
				y: 0,
				width: 100,
				height: 100
			});
			expect(rectOf(pending[0].rootBounds)).toEqual({ x: 0, y: 0, width: 1000, height: 500 });

			await Promise.resolve();
			expect(entries).toEqual([]);
			observer.disconnect();
		});

		it('Delivers the initial entry asynchronously in observation order.', async () => {
			const first = mockRect(document.createElement('div'), { x: 0, y: 0, width: 10, height: 10 });
			const second = mockRect(document.createElement('div'), {
				x: 20,
				y: 0,
				width: 10,
				height: 10
			});
			const third = mockRect(document.createElement('div'), { x: 40, y: 0, width: 10, height: 10 });
			const seen: Element[] = [];
			const observer = new window.IntersectionObserver((records) => {
				seen.push(...records.map((record) => <Element>record.target));
			});

			observer.observe(first);
			observer.observe(second);
			observer.observe(third);
			observer.observe(second);

			await flushAsync();

			expect(seen).toEqual([first, second, third]);
			observer.disconnect();
		});

		it('Computes element-root intersections and pixel root margins.', async () => {
			const root = mockRect(document.createElement('div'), {
				x: 100,
				y: 100,
				width: 100,
				height: 100
			});
			const target = mockRect(document.createElement('div'), {
				x: 100,
				y: 100,
				width: 20,
				height: 20
			});
			const observer = new window.IntersectionObserver(() => {}, {
				root,
				rootMargin: '-10px'
			});
			observer.observe(target);

			await Promise.resolve();
			const [entry] = observer.takeRecords();
			expect(rectOf(entry.rootBounds)).toEqual({ x: 110, y: 110, width: 80, height: 80 });
			expect(entry.intersectionRatio).toBeCloseTo(0.25);
			expect(entry.isIntersecting).toBe(true);
			expect(rectOf(entry.intersectionRect)).toEqual({ x: 110, y: 110, width: 10, height: 10 });
			observer.disconnect();
		});

		it('Expands the viewport by pixel root margins.', async () => {
			const target = mockRect(document.createElement('div'), {
				x: -15,
				y: 0,
				width: 10,
				height: 10
			});
			const observer = new window.IntersectionObserver(() => {}, {
				rootMargin: '10px 0px 0px 0px'
			});
			observer.observe(target);
			await Promise.resolve();
			const [withoutHorizontal] = observer.takeRecords();
			expect(withoutHorizontal.isIntersecting).toBe(false);
			expect(withoutHorizontal.intersectionRatio).toBe(0);
			observer.disconnect();

			const expanded = new window.IntersectionObserver(() => {}, {
				rootMargin: '0px 0px 0px 10px'
			});
			expanded.observe(target);
			await Promise.resolve();
			const [entry] = expanded.takeRecords();
			expect(rectOf(entry.rootBounds)).toEqual({ x: -10, y: 0, width: 1010, height: 500 });
			expect(entry.isIntersecting).toBe(true);
			expect(entry.intersectionRatio).toBeCloseTo(0.5);
			expanded.disconnect();
		});

		it('Reports ratio 1 for a contained zero-area target and 0 otherwise.', async () => {
			const inside = mockRect(document.createElement('div'), { x: 10, y: 10, width: 0, height: 0 });
			const outside = mockRect(document.createElement('div'), {
				x: 5000,
				y: 10,
				width: 0,
				height: 0
			});
			const root = mockRect(document.createElement('div'), { x: 0, y: 0, width: 200, height: 200 });
			const records: IntersectionObserverEntry[] = [];
			const observer = new window.IntersectionObserver((entries) => records.push(...entries), {
				root
			});

			observer.observe(inside);
			observer.observe(outside);
			await flushAsync();

			expect(records.map((entry) => entry.intersectionRatio)).toEqual([1, 0]);
			expect(records.map((entry) => entry.isIntersecting)).toEqual([true, false]);
			observer.disconnect();
		});

		it('Queues a new entry when a target crosses a threshold and keeps the same slot silent.', async () => {
			let rect = { x: 0, y: 0, width: 100, height: 100 };
			const target = document.createElement('div');
			target.getBoundingClientRect = () =>
				new window.DOMRect(rect.x, rect.y, rect.width, rect.height);
			const ratios: number[] = [];
			const observer = new window.IntersectionObserver(
				(entries) => ratios.push(...entries.map((entry) => entry.intersectionRatio)),
				{ threshold: [0, 0.5, 1] }
			);

			observer.observe(target);
			await flushAsync();
			expect(ratios).toEqual([1]);

			rect = { x: 0, y: 0, width: 100, height: 80 };
			await flushAsync();
			expect(ratios).toEqual([1]);

			rect = { x: 950, y: 0, width: 100, height: 100 };
			await flushAsync();
			expect(ratios).toEqual([1, 0.5]);

			rect = { x: 2000, y: 0, width: 100, height: 100 };
			await flushAsync();
			expect(ratios).toEqual([1, 0.5, 0]);
			expect(ratios).toHaveLength(3);
			observer.disconnect();
		});
	});

	describe('unobserve()', () => {
		it('Stops future entries for that target.', async () => {
			let rect = { x: 0, y: 0, width: 100, height: 100 };
			const target = document.createElement('div');
			target.getBoundingClientRect = () =>
				new window.DOMRect(rect.x, rect.y, rect.width, rect.height);
			let calls = 0;
			const observer = new window.IntersectionObserver(() => {
				calls++;
			});

			observer.observe(target);
			await flushAsync();
			expect(calls).toBe(1);

			observer.unobserve(target);
			rect = { x: 5000, y: 0, width: 100, height: 100 };
			await flushAsync();
			await flushAsync();
			expect(calls).toBe(1);
		});

		it('Keeps an entry that was already queued.', async () => {
			const target = mockRect(document.createElement('div'), { x: 0, y: 0, width: 10, height: 10 });
			let calls = 0;
			const observer = new window.IntersectionObserver(() => {
				calls++;
			});

			observer.observe(target);
			await Promise.resolve();
			observer.unobserve(target);
			const pending = observer.takeRecords();
			expect(pending).toHaveLength(1);
			await flushAsync();
			expect(calls).toBe(0);
			observer.disconnect();
		});
	});

	describe('disconnect()', () => {
		it('Stops future delivery and clears pending records.', async () => {
			const target = mockRect(document.createElement('div'), { x: 0, y: 0, width: 10, height: 10 });
			let calls = 0;
			const observer = new window.IntersectionObserver(() => {
				calls++;
			});

			observer.observe(target);
			observer.disconnect();
			expect(observer.takeRecords()).toEqual([]);

			await flushAsync();
			expect(calls).toBe(0);

			observer.observe(target);
			await flushAsync();
			expect(calls).toBe(1);
			observer.disconnect();
		});
	});

	describe('takeRecords()', () => {
		it('Returns an empty array when nothing has been queued.', () => {
			const observer = new window.IntersectionObserver(() => {});
			expect(observer.takeRecords()).toEqual([]);
		});
	});
});

/**
 * Pins an element's bounding client rect.
 *
 * @param element Element.
 * @param rect Rectangle.
 * @returns Element.
 */
function mockRect(element: Element, rect: IRectLike): Element {
	const view = element.ownerDocument!.defaultView!;
	element.getBoundingClientRect = () => new view.DOMRect(rect.x, rect.y, rect.width, rect.height);
	return element;
}

/**
 * Reads the geometry fields used by assertions.
 *
 * @param rect Rectangle.
 * @returns Plain rectangle.
 */
function rectOf(
	rect: { x: number; y: number; width: number; height: number } | null
): IRectLike | null {
	if (!rect) {
		return null;
	}
	return { x: rect.x, y: rect.y, width: rect.width, height: rect.height };
}
