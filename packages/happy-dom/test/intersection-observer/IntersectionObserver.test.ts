import Window from '../../src/window/Window.js';
import type Document from '../../src/nodes/document/Document.js';
import type Element from '../../src/nodes/element/Element.js';
import DOMRect from '../../src/dom/DOMRect.js';
import { beforeEach, describe, it, expect } from 'vitest';

/**
 * Flushes the intersection observer microtask.
 *
 * @returns Promise.
 */
function flush(): Promise<void> {
	return new Promise((resolve) => {
		queueMicrotask(resolve);
	});
}

/**
 * Stubs an element's border box.
 *
 * @param element Element.
 * @param x X.
 * @param y Y.
 * @param width Width.
 * @param height Height.
 */
function setRect(element: Element, x: number, y: number, width: number, height: number): void {
	element.getBoundingClientRect = () => new DOMRect(x, y, width, height);
}

describe('IntersectionObserver', () => {
	let window: Window;
	let document: Document;

	beforeEach(() => {
		window = new Window();
		document = window.document;
	});

	describe('constructor', () => {
		it('Throws when the callback is not a function.', () => {
			expect(() => new window.IntersectionObserver(<never>null)).toThrow(window.TypeError);
			expect(() => new window.IntersectionObserver(<never>undefined)).toThrow(window.TypeError);
		});

		it('Throws when root is not an element or null.', () => {
			expect(
				() =>
					new window.IntersectionObserver(() => {}, {
						root: <never>document.createTextNode('x')
					})
			).toThrow(TypeError);
			expect(() => new window.IntersectionObserver(() => {}, { root: <never>document })).toThrow(
				TypeError
			);
		});

		it('Throws when rootMargin cannot be parsed as pixels or percent.', () => {
			expect(() => new window.IntersectionObserver(() => {}, { rootMargin: '10' })).toThrow(
				TypeError
			);
			expect(() => new window.IntersectionObserver(() => {}, { rootMargin: '10em' })).toThrow(
				TypeError
			);
			expect(
				() => new window.IntersectionObserver(() => {}, { rootMargin: '1px 2px 3px 4px 5px' })
			).toThrow(TypeError);
		});

		it('Throws when a threshold is outside 0..1.', () => {
			expect(() => new window.IntersectionObserver(() => {}, { threshold: 1.5 })).toThrow(
				TypeError
			);
			expect(() => new window.IntersectionObserver(() => {}, { threshold: -0.1 })).toThrow(
				TypeError
			);
			expect(
				() => new window.IntersectionObserver(() => {}, { threshold: [0, Number.NaN] })
			).toThrow(TypeError);
			expect(() => new window.IntersectionObserver(() => {}, { threshold: [] })).toThrow(TypeError);
		});

		it('Exposes a normalized rootMargin and sorted unique thresholds.', () => {
			const root = document.createElement('div');
			const observer = new window.IntersectionObserver(() => {}, {
				root,
				rootMargin: '10px 20% 30px',
				threshold: [1, 0, 0.5, 0]
			});

			expect(observer.root).toBe(root);
			expect(observer.rootMargin).toBe('10px 20% 30px 20%');
			expect(observer.thresholds).toEqual([0, 0.5, 1]);
			expect(new window.IntersectionObserver(() => {}).rootMargin).toBe('0px 0px 0px 0px');
			expect(new window.IntersectionObserver(() => {}).thresholds).toEqual([0]);
			expect(new window.IntersectionObserver(() => {}, { root: null }).root).toBeNull();
		});
	});

	describe('observe()', () => {
		it('Throws when the target is not an element.', () => {
			const observer = new window.IntersectionObserver(() => {});
			expect(() => observer.observe(<never>null)).toThrow(TypeError);
			expect(() => observer.observe(<never>document.createTextNode('x'))).toThrow(TypeError);
		});

		it('Does not invoke the callback synchronously and queues an initial entry.', async () => {
			const target = document.createElement('div');
			document.body.appendChild(target);
			const entries: Element[] = [];
			let sync = false;
			const observer = new window.IntersectionObserver((records) => {
				sync = true;
				entries.push(...records.map((record) => <Element>record.target));
			});

			observer.observe(target);
			expect(sync).toBe(false);
			expect(observer.takeRecords()).toHaveLength(1);

			observer.observe(target);
			await flush();
			expect(entries).toEqual([]);
		});

		it('Delivers initial entries in observation order.', async () => {
			const first = document.createElement('div');
			const second = document.createElement('span');
			const seen: Element[] = [];
			const observer = new window.IntersectionObserver((records) => {
				seen.push(...records.map((record) => <Element>record.target));
			});

			observer.observe(first);
			observer.observe(second);
			await flush();

			expect(seen).toEqual([first, second]);
		});

		it('Reports a zero-area target as fully intersecting when it is contained by the viewport.', async () => {
			const target = document.createElement('div');
			setRect(target, 0, 0, 0, 0);
			let ratio = -1;
			let intersecting = false;
			const observer = new window.IntersectionObserver((records) => {
				ratio = records[0].intersectionRatio;
				intersecting = records[0].isIntersecting;
			});

			observer.observe(target);
			await flush();

			expect(ratio).toBe(1);
			expect(intersecting).toBe(true);
		});

		it('Reports a zero-area target as not intersecting when it lies outside the root.', async () => {
			const target = document.createElement('div');
			setRect(target, 5000, 5000, 0, 0);
			let ratio = -1;
			const observer = new window.IntersectionObserver((records) => {
				ratio = records[0].intersectionRatio;
			});

			observer.observe(target);
			await flush();

			expect(ratio).toBe(0);
		});

		it('Applies pixel root margins and element roots.', async () => {
			const root = document.createElement('div');
			const target = document.createElement('div');
			setRect(root, 0, 0, 100, 100);
			setRect(target, 100, 0, 50, 50);
			const ratios: number[] = [];
			const clipped = new window.IntersectionObserver(
				(records) => {
					ratios.push(records[0].intersectionRatio);
				},
				{ root, rootMargin: '0px' }
			);
			const expanded = new window.IntersectionObserver(
				(records) => {
					ratios.push(records[0].intersectionRatio);
				},
				{ root, rootMargin: '0px 50px 0px 0px' }
			);

			clipped.observe(target);
			expanded.observe(target);
			await flush();

			expect(ratios).toEqual([0, 1]);
			expect(expanded.rootMargin).toBe('0px 50px 0px 0px');
		});

		it('Queues a new entry when a target crosses a threshold.', async () => {
			const target = document.createElement('div');
			document.body.appendChild(target);
			setRect(target, 0, 0, 0, 0);
			const ratios: number[] = [];
			const observer = new window.IntersectionObserver(
				(records) => {
					ratios.push(records[0].intersectionRatio);
				},
				{ threshold: [0, 1] }
			);

			observer.observe(target);
			await flush();
			setRect(target, 5000, 5000, 0, 0);
			document.body.removeChild(target);
			await flush();

			expect(ratios).toEqual([1, 0]);
		});
	});

	describe('unobserve()', () => {
		it('Stops future entries for the target.', async () => {
			const target = document.createElement('div');
			let calls = 0;
			const observer = new window.IntersectionObserver(() => {
				calls++;
			});

			observer.observe(target);
			observer.unobserve(target);
			await flush();

			expect(calls).toBe(0);
			expect(observer.takeRecords()).toEqual([]);
		});
	});

	describe('disconnect()', () => {
		it('Clears pending records and prevents delivery.', async () => {
			const target = document.createElement('div');
			let calls = 0;
			const observer = new window.IntersectionObserver(() => {
				calls++;
			});

			observer.observe(target);
			expect(observer.takeRecords()).toHaveLength(1);
			observer.disconnect();
			expect(observer.takeRecords()).toEqual([]);
			await flush();
			expect(calls).toBe(0);
		});
	});

	describe('takeRecords()', () => {
		it('Returns queued entries and prevents the callback from seeing them.', async () => {
			const first = document.createElement('div');
			const second = document.createElement('div');
			let calls = 0;
			const observer = new window.IntersectionObserver(() => {
				calls++;
			});

			observer.observe(first);
			observer.observe(second);
			const records = observer.takeRecords();

			expect(records.map((record) => record.target)).toEqual([first, second]);
			await flush();
			expect(calls).toBe(0);
			expect(observer.takeRecords()).toEqual([]);
		});
	});
});
