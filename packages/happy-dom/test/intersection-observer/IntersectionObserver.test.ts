import Window from '../../src/window/Window.js';
import type Document from '../../src/nodes/document/Document.js';
import type Element from '../../src/nodes/element/Element.js';
import type IntersectionObserver from '../../src/intersection-observer/IntersectionObserver.js';
import type IntersectionObserverEntry from '../../src/intersection-observer/IntersectionObserverEntry.js';
import { beforeEach, describe, it, expect } from 'vitest';

/**
 * Flushes the asynchronous observer callback.
 *
 * @returns Promise.
 */
function flush(): Promise<void> {
	return new Promise((resolve) => setTimeout(resolve, 1));
}

describe('IntersectionObserver', () => {
	let window: Window;
	let document: Document;

	beforeEach(() => {
		window = new Window();
		document = window.document;
		window.innerWidth = 100;
		window.innerHeight = 100;
	});

	/**
	 * Pins an element rectangle for deterministic intersection math.
	 *
	 * @param element Element.
	 * @param x X.
	 * @param y Y.
	 * @param width Width.
	 * @param height Height.
	 */
	function mockRect(element: Element, x: number, y: number, width: number, height: number): void {
		element.getBoundingClientRect = () => new window.DOMRect(x, y, width, height);
	}

	describe('constructor', () => {
		it('Normalizes root, rootMargin, and thresholds.', () => {
			const root = document.createElement('div');
			const observer = new window.IntersectionObserver(() => {}, {
				root,
				rootMargin: '  1px   2%  3px  ',
				threshold: [1, 0, 0.5, 0.5]
			});

			expect(observer.root).toBe(root);
			expect(observer.rootMargin).toBe('1px 2% 3px 2%');
			expect(observer.thresholds).toEqual([0, 0.5, 1]);
		});

		it('Uses viewport defaults when options are omitted.', () => {
			const observer = new window.IntersectionObserver(() => {});

			expect(observer.root).toBe(null);
			expect(observer.rootMargin).toBe('0px 0px 0px 0px');
			expect(observer.thresholds).toEqual([0]);
		});

		it('Expands rootMargin shorthand for one, two, and four values.', () => {
			expect(new window.IntersectionObserver(() => {}, { rootMargin: '10px' }).rootMargin).toBe(
				'10px 10px 10px 10px'
			);
			expect(new window.IntersectionObserver(() => {}, { rootMargin: '1px 2px' }).rootMargin).toBe(
				'1px 2px 1px 2px'
			);
			expect(
				new window.IntersectionObserver(() => {}, { rootMargin: '1px 2% 3px 4px' }).rootMargin
			).toBe('1px 2% 3px 4px');
			expect(
				new window.IntersectionObserver(() => {}, { rootMargin: '1px 2px 3px' }).rootMargin
			).toBe('1px 2px 3px 2px');
			expect(new window.IntersectionObserver(() => {}, { rootMargin: '-5px' }).rootMargin).toBe(
				'-5px -5px -5px -5px'
			);
			expect(new window.IntersectionObserver(() => {}, { rootMargin: '10PX 20%' }).rootMargin).toBe(
				'10px 20% 10px 20%'
			);
		});

		it('Treats an empty threshold list as 0 and keeps a single number.', () => {
			expect(new window.IntersectionObserver(() => {}, { threshold: [] }).thresholds).toEqual([0]);
			expect(new window.IntersectionObserver(() => {}, { threshold: 0.25 }).thresholds).toEqual([
				0.25
			]);
		});

		it('Throws when the callback is not a function.', () => {
			expect(() => new window.IntersectionObserver(<any>null)).toThrow(window.TypeError);
			expect(() => new window.IntersectionObserver(<any>undefined)).toThrow(/not a function/);
		});

		it('Throws when root is not an element, document, or null.', () => {
			expect(
				() =>
					new window.IntersectionObserver(() => {}, {
						root: <Element>(<any>document.createTextNode('x'))
					})
			).toThrow(window.TypeError);
			expect(
				() =>
					new window.IntersectionObserver(() => {}, {
						root: <Element>(<any>{})
					})
			).toThrow(/root/);
		});

		it('Accepts a document root.', () => {
			const observer = new window.IntersectionObserver(() => {}, { root: document });
			expect(observer.root).toBe(document);
		});

		it('Throws when rootMargin is not px or percent.', () => {
			expect(() => new window.IntersectionObserver(() => {}, { rootMargin: '10' })).toThrow(
				window.SyntaxError
			);
			expect(() => new window.IntersectionObserver(() => {}, { rootMargin: '2em' })).toThrow(
				window.SyntaxError
			);
			expect(() => new window.IntersectionObserver(() => {}, { rootMargin: 'auto' })).toThrow(
				window.SyntaxError
			);
			expect(
				() => new window.IntersectionObserver(() => {}, { rootMargin: '1px 1px 1px 1px 1px' })
			).toThrow(window.SyntaxError);
		});

		it('Throws when a threshold is out of range or not a number.', () => {
			expect(() => new window.IntersectionObserver(() => {}, { threshold: 1.1 })).toThrow(
				window.RangeError
			);
			expect(() => new window.IntersectionObserver(() => {}, { threshold: [-0.1] })).toThrow(
				window.RangeError
			);
			expect(
				() => new window.IntersectionObserver(() => {}, { threshold: <number[]>(<any>['foo']) })
			).toThrow(window.TypeError);
			expect(() => new window.IntersectionObserver(() => {}, { threshold: Number.NaN })).toThrow(
				window.TypeError
			);
			expect(
				() => new window.IntersectionObserver(() => {}, { threshold: Number.POSITIVE_INFINITY })
			).toThrow(window.TypeError);
		});
	});

	describe('observe()', () => {
		it('Does not invoke the callback synchronously and queues an initial entry.', async () => {
			const div = document.createElement('div');
			mockRect(div, 0, 0, 50, 50);
			let called = false;
			const observer = new window.IntersectionObserver(() => {
				called = true;
			});

			observer.observe(div);

			expect(called).toBe(false);
			const records = observer.takeRecords();
			expect(records).toHaveLength(1);
			expect(records[0].target).toBe(div);
			expect(records[0].isIntersecting).toBe(true);
			expect(records[0].intersectionRatio).toBe(1);

			await flush();
			expect(called).toBe(false);
		});

		it('Delivers the initial entry asynchronously.', async () => {
			const div = document.createElement('div');
			mockRect(div, 0, 0, 40, 40);
			const entries: IntersectionObserverEntry[][] = [];
			let observerRef: IntersectionObserver | null = null;
			const observer = new window.IntersectionObserver((records, instance) => {
				entries.push(records);
				observerRef = instance;
			});

			observer.observe(div);
			await flush();

			expect(entries).toHaveLength(1);
			expect(entries[0]).toHaveLength(1);
			expect(entries[0][0].target).toBe(div);
			expect(entries[0][0].intersectionRatio).toBe(1);
			expect(entries[0][0].boundingClientRect.width).toBe(40);
			expect(entries[0][0].rootBounds?.width).toBe(100);
			expect(observerRef).toBe(observer);
			expect(typeof entries[0][0].time).toBe('number');
		});

		it('Preserves observation order in one callback.', async () => {
			const first = document.createElement('div');
			const second = document.createElement('div');
			const third = document.createElement('div');
			mockRect(first, 0, 0, 10, 10);
			mockRect(second, 0, 0, 10, 10);
			mockRect(third, 0, 0, 10, 10);
			const seen: Element[] = [];
			const observer = new window.IntersectionObserver((records) => {
				seen.push(...records.map((record) => <Element>record.target));
			});

			observer.observe(first);
			observer.observe(second);
			observer.observe(third);
			await flush();

			expect(seen[0]).toBe(first);
			expect(seen[1]).toBe(second);
			expect(seen[2]).toBe(third);
			expect(seen).toHaveLength(3);
		});

		it('Does not queue a second initial entry when observe() is repeated.', async () => {
			const div = document.createElement('div');
			mockRect(div, 0, 0, 10, 10);
			const seen: IntersectionObserverEntry[] = [];
			const observer = new window.IntersectionObserver((records) => {
				seen.push(...records);
			});

			observer.observe(div);
			observer.observe(div);
			await flush();

			expect(seen).toHaveLength(1);
		});

		it('Throws when the target is not an element.', () => {
			const observer = new window.IntersectionObserver(() => {});

			expect(() => observer.observe(<Element>(<any>null))).toThrow(window.TypeError);
			expect(() => observer.observe(<Element>(<any>document.createTextNode('x')))).toThrow(
				window.TypeError
			);
			expect(() => observer.observe(<Element>(<any>document))).toThrow(window.TypeError);
		});

		it('Calculates viewport intersection and pixel root margins.', async () => {
			const div = document.createElement('div');
			mockRect(div, 90, 0, 20, 10);
			const entries: IntersectionObserverEntry[] = [];
			const observer = new window.IntersectionObserver(
				(records) => {
					entries.push(...records);
				},
				{ rootMargin: '10px', threshold: [0, 0.5, 1] }
			);

			observer.observe(div);
			await flush();

			expect(entries).toHaveLength(1);
			expect(entries[0].rootBounds?.x).toBe(-10);
			expect(entries[0].rootBounds?.y).toBe(-10);
			expect(entries[0].rootBounds?.width).toBe(120);
			expect(entries[0].rootBounds?.height).toBe(120);
			expect(entries[0].intersectionRect?.width).toBe(20);
			expect(entries[0].intersectionRect?.height).toBe(10);
			expect(entries[0].intersectionRatio).toBe(1);
			expect(entries[0].isIntersecting).toBe(true);
		});

		it('Calculates a partial viewport intersection without margin.', async () => {
			const div = document.createElement('div');
			mockRect(div, 90, 0, 20, 10);
			const entries: IntersectionObserverEntry[] = [];
			const observer = new window.IntersectionObserver((records) => {
				entries.push(...records);
			});

			observer.observe(div);
			await flush();

			expect(entries[0].intersectionRect?.x).toBe(90);
			expect(entries[0].intersectionRect?.width).toBe(10);
			expect(entries[0].intersectionRect?.height).toBe(10);
			expect(entries[0].intersectionRatio).toBe(0.5);
			expect(entries[0].isIntersecting).toBe(true);
		});

		it('Calculates element-root intersection with a pixel margin.', async () => {
			const root = document.createElement('div');
			const target = document.createElement('div');
			mockRect(root, 0, 0, 100, 100);
			mockRect(target, 50, 50, 100, 100);
			const entries: IntersectionObserverEntry[] = [];
			const observer = new window.IntersectionObserver(
				(records) => {
					entries.push(...records);
				},
				{ root, rootMargin: '0px 25px', threshold: 0 }
			);

			observer.observe(target);
			await flush();

			expect(entries[0].rootBounds?.x).toBe(-25);
			expect(entries[0].rootBounds?.width).toBe(150);
			expect(entries[0].rootBounds?.height).toBe(100);
			expect(entries[0].intersectionRect?.width).toBe(75);
			expect(entries[0].intersectionRect?.height).toBe(50);
			expect(entries[0].intersectionRatio).toBe(0.375);
		});

		it('Reports ratio 1 for a zero-area target contained by the root and 0 otherwise.', async () => {
			const inside = document.createElement('div');
			const edge = document.createElement('div');
			const outside = document.createElement('div');
			const line = document.createElement('div');
			mockRect(inside, 10, 10, 0, 0);
			mockRect(edge, 100, 100, 0, 0);
			mockRect(outside, 100.1, 50, 0, 0);
			mockRect(line, 90, 0, 50, 0);
			const ratios: number[] = [];
			const observer = new window.IntersectionObserver((records) => {
				ratios.push(...records.map((record) => record.intersectionRatio));
			});

			observer.observe(inside);
			observer.observe(edge);
			observer.observe(outside);
			observer.observe(line);
			await flush();

			expect(ratios).toEqual([1, 1, 0, 0]);
		});

		it('Reports a non-intersecting positive-area target as ratio 0.', async () => {
			const div = document.createElement('div');
			mockRect(div, 200, 200, 10, 10);
			const entries: IntersectionObserverEntry[] = [];
			const observer = new window.IntersectionObserver((records) => {
				entries.push(...records);
			});

			observer.observe(div);
			await flush();

			expect(entries[0].isIntersecting).toBe(false);
			expect(entries[0].intersectionRatio).toBe(0);
			expect(entries[0].intersectionRect?.width).toBe(0);
			expect(entries[0].intersectionRect?.height).toBe(0);
		});

		it('Applies percentage root margins against the root dimensions.', async () => {
			const div = document.createElement('div');
			mockRect(div, 0, 0, 10, 10);
			const entries: IntersectionObserverEntry[] = [];
			const observer = new window.IntersectionObserver(
				(records) => {
					entries.push(...records);
				},
				{ rootMargin: '10% 20%' }
			);

			observer.observe(div);
			await flush();

			expect(entries[0].rootBounds?.x).toBe(-20);
			expect(entries[0].rootBounds?.y).toBe(-10);
			expect(entries[0].rootBounds?.width).toBe(140);
			expect(entries[0].rootBounds?.height).toBe(120);
		});

		it('Queues a new entry when a target crosses a threshold.', async () => {
			const div = document.createElement('div');
			mockRect(div, 0, 0, 100, 100);
			const ratios: number[] = [];
			const observer = new window.IntersectionObserver(
				(records) => {
					ratios.push(...records.map((record) => record.intersectionRatio));
				},
				{ threshold: [0, 0.5, 1] }
			);

			observer.observe(div);
			await flush();
			expect(ratios).toEqual([1]);

			mockRect(div, 0, 60, 100, 100);
			window.dispatchEvent(new window.Event('resize'));
			await flush();
			expect(ratios).toEqual([1, 0.4]);

			mockRect(div, 0, 70, 100, 100);
			window.dispatchEvent(new window.Event('resize'));
			await flush();
			expect(ratios).toEqual([1, 0.4]);
		});

		it('Keeps the initial entry when a threshold is crossed before delivery.', async () => {
			const div = document.createElement('div');
			mockRect(div, 0, 0, 100, 100);
			const ratios: number[] = [];
			const observer = new window.IntersectionObserver(
				(records) => {
					ratios.push(...records.map((record) => record.intersectionRatio));
				},
				{ threshold: [0, 0.5, 1] }
			);

			observer.observe(div);
			mockRect(div, 0, 60, 100, 100);
			window.dispatchEvent(new window.Event('resize'));
			await flush();

			expect(ratios).toEqual([1, 0.4]);
		});

		it('Delivers a later initial entry and an earlier crossing in observation order.', async () => {
			const first = document.createElement('div');
			const second = document.createElement('div');
			document.body.appendChild(first);
			document.body.appendChild(second);
			mockRect(first, 0, 0, 100, 100);
			mockRect(second, 0, 0, 10, 10);
			const seen: Element[] = [];
			const observer = new window.IntersectionObserver(
				(records) => {
					seen.push(...records.map((record) => <Element>record.target));
				},
				{ threshold: [0, 0.5, 1] }
			);

			observer.observe(first);
			await flush();
			seen.length = 0;

			observer.observe(second);
			mockRect(first, 0, 60, 100, 100);
			first.dispatchEvent(new window.Event('scroll'));
			await flush();

			expect(seen[0]).toBe(first);
			expect(seen[1]).toBe(second);
			expect(seen).toHaveLength(2);
		});

		it('Invokes a non-arrow callback with the observer as this.', async () => {
			const div = document.createElement('div');
			mockRect(div, 0, 0, 10, 10);
			let context: IntersectionObserver | null = null;

			function callback(this: IntersectionObserver): void {
				context = this;
			}

			const observer = new window.IntersectionObserver(callback);
			observer.observe(div);
			await flush();

			expect(context).toBe(observer);
		});
	});

	describe('unobserve()', () => {
		it('Stops future entries for that target and drops its pending record.', async () => {
			const first = document.createElement('div');
			const second = document.createElement('div');
			mockRect(first, 0, 0, 20, 20);
			mockRect(second, 0, 0, 20, 20);
			const seen: Element[] = [];
			const observer = new window.IntersectionObserver((records) => {
				seen.push(...records.map((record) => <Element>record.target));
			});

			observer.observe(first);
			observer.observe(second);
			observer.unobserve(first);

			const pending = observer.takeRecords();
			expect(pending).toHaveLength(1);
			expect(pending[0].target).toBe(second);
			expect(observer.takeRecords()).toEqual([]);

			await flush();
			expect(seen).toEqual([]);

			mockRect(first, 200, 200, 20, 20);
			mockRect(second, 200, 200, 10, 10);
			window.dispatchEvent(new window.Event('scroll'));
			await flush();

			expect(seen).toHaveLength(1);
			expect(seen[0]).toBe(second);
		});

		it('Throws when the target is not an element.', () => {
			const observer = new window.IntersectionObserver(() => {});
			expect(() => observer.unobserve(<Element>(<any>null))).toThrow(window.TypeError);
		});
	});

	describe('disconnect()', () => {
		it('Stops future delivery and clears pending records.', async () => {
			const div = document.createElement('div');
			mockRect(div, 0, 0, 20, 20);
			let called = false;
			const observer = new window.IntersectionObserver(() => {
				called = true;
			});

			observer.observe(div);
			observer.disconnect();

			expect(observer.takeRecords()).toEqual([]);
			await flush();
			expect(called).toBe(false);

			mockRect(div, 0, 0, 10, 10);
			window.dispatchEvent(new window.Event('resize'));
			await flush();
			expect(called).toBe(false);
		});
	});

	describe('takeRecords()', () => {
		it('Returns an empty array when nothing is queued.', () => {
			const observer = new window.IntersectionObserver(() => {});
			expect(observer.takeRecords()).toEqual([]);
		});
	});
});
