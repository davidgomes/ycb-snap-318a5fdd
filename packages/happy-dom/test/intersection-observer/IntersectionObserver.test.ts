import Window from '../../src/window/Window.js';
import type Document from '../../src/nodes/document/Document.js';
import type Element from '../../src/nodes/element/Element.js';
import type IntersectionObserverEntry from '../../src/intersection-observer/IntersectionObserverEntry.js';
import { beforeEach, afterEach, describe, it, expect } from 'vitest';

describe('IntersectionObserver', () => {
	let window: Window;
	let document: Document;

	beforeEach(() => {
		window = new Window();
		document = window.document;
	});

	afterEach(async () => {
		await window.happyDOM.close();
	});

	describe('constructor', () => {
		it('Exposes the viewport root, a normalized root margin, and default thresholds.', () => {
			const observer = new window.IntersectionObserver(() => {});

			expect(observer.root).toBeNull();
			expect(observer.rootMargin).toBe('0px 0px 0px 0px');
			expect(observer.thresholds).toEqual([0]);
		});

		it('Keeps an element root and a document root.', () => {
			const root = document.createElement('div');
			const elementObserver = new window.IntersectionObserver(() => {}, { root });
			const documentObserver = new window.IntersectionObserver(() => {}, { root: document });

			expect(elementObserver.root).toBe(root);
			expect(documentObserver.root).toBe(document);
		});

		it('Expands rootMargin shorthand into four values and accepts px and percent.', () => {
			expect(margin('10px')).toBe('10px 10px 10px 10px');
			expect(margin('10px 20px')).toBe('10px 20px 10px 20px');
			expect(margin('10px 20px 30px')).toBe('10px 20px 30px 20px');
			expect(margin('10px 20% 30px 40px')).toBe('10px 20% 30px 40px');
			expect(margin('  -5px   10%  ')).toBe('-5px 10% -5px 10%');
			expect(margin('10PX 0%')).toBe('10px 0% 10px 0%');
			expect(margin('')).toBe('0px 0px 0px 0px');
		});

		it('Sorts and deduplicates thresholds.', () => {
			const observer = new window.IntersectionObserver(() => {}, {
				threshold: [1, 0, 0.5, 0.5, 0]
			});

			expect(observer.thresholds).toEqual([0, 0.5, 1]);
			expect(Object.isFrozen(observer.thresholds)).toBe(true);
		});

		it('Accepts a single threshold number.', () => {
			const observer = new window.IntersectionObserver(() => {}, { threshold: 1 });

			expect(observer.thresholds).toEqual([1]);
		});

		it('Throws when the callback is not a function.', () => {
			expect(() => new window.IntersectionObserver(<never>null)).toThrow(window.TypeError);
			expect(() => new window.IntersectionObserver(<never>'callback')).toThrow(window.TypeError);
		});

		it('Throws when root is not an element, document, or null.', () => {
			expect(
				() =>
					new window.IntersectionObserver(() => {}, {
						root: <never>document.createTextNode('nope')
					})
			).toThrow(window.TypeError);
			expect(() => new window.IntersectionObserver(() => {}, { root: <never>window })).toThrow(
				window.TypeError
			);
		});

		it('Throws when rootMargin is not px or percent.', () => {
			expect(() => new window.IntersectionObserver(() => {}, { rootMargin: '10' })).toThrow(
				window.DOMException
			);
			expect(() => new window.IntersectionObserver(() => {}, { rootMargin: '10em' })).toThrow(
				window.DOMException
			);
			expect(
				() => new window.IntersectionObserver(() => {}, { rootMargin: '10px 20px 30px 40px 50px' })
			).toThrow(window.DOMException);

			try {
				new window.IntersectionObserver(() => {}, { rootMargin: '10em' });
			} catch (error) {
				expect((<Error>error).name).toBe('SyntaxError');
			}
		});

		it('Throws when a threshold is out of range or not a number.', () => {
			expect(() => new window.IntersectionObserver(() => {}, { threshold: -0.1 })).toThrow(
				window.RangeError
			);
			expect(() => new window.IntersectionObserver(() => {}, { threshold: 1.1 })).toThrow(
				window.RangeError
			);
			expect(() => new window.IntersectionObserver(() => {}, { threshold: Number.NaN })).toThrow(
				window.RangeError
			);
			expect(
				() => new window.IntersectionObserver(() => {}, { threshold: <never>['0.5'] })
			).toThrow(window.TypeError);
		});
	});

	describe('observe()', () => {
		it('Does not invoke the callback synchronously and delivers the initial entry asynchronously.', async () => {
			const target = document.createElement('div');
			const entries: IntersectionObserverEntry[][] = [];
			const observer = new window.IntersectionObserver((records) => {
				entries.push(records);
			});

			setRect(target, 0, 0, 100, 100);
			observer.observe(target);

			expect(entries).toEqual([]);

			await window.happyDOM.waitUntilComplete();

			expect(entries).toHaveLength(1);
			expect(entries[0]).toHaveLength(1);
			expect(entries[0][0].target).toBe(target);
			expect(entries[0][0].isIntersecting).toBe(true);
			expect(entries[0][0].intersectionRatio).toBe(1);
			expect(entries[0][0].boundingClientRect.width).toBe(100);
		});

		it('Queues an initial entry for every newly observed target, in observation order.', async () => {
			const first = document.createElement('div');
			const second = document.createElement('div');
			const third = document.createElement('div');
			let records: IntersectionObserverEntry[] = [];
			const observer = new window.IntersectionObserver((entries, instance) => {
				records = entries;
				expect(instance).toBe(observer);
			});

			setRect(first, 0, 0, 10, 10);
			setRect(second, 20, 0, 10, 10);
			setRect(third, 40, 0, 10, 10);
			observer.observe(first);
			observer.observe(second);
			observer.observe(third);
			observer.observe(first);

			expect(observer.takeRecords().map((entry) => entry.target)).toEqual([first, second, third]);

			await window.happyDOM.waitUntilComplete();

			expect(records).toEqual([]);
		});

		it('Delivers targets observed in the same turn in one callback, in order.', async () => {
			const first = document.createElement('div');
			const second = document.createElement('div');
			const seen: Element[] = [];
			const observer = new window.IntersectionObserver((entries) => {
				seen.push(...entries.map((entry) => <Element>entry.target));
			});

			setRect(first, 0, 0, 10, 10);
			setRect(second, 0, 20, 10, 10);
			observer.observe(first);
			observer.observe(second);

			await window.happyDOM.waitUntilComplete();

			expect(seen).toEqual([first, second]);
		});

		it('Throws when the argument is not an element.', () => {
			const observer = new window.IntersectionObserver(() => {});

			expect(() => observer.observe(<never>null)).toThrow(window.TypeError);
			expect(() => observer.observe(<never>document)).toThrow(window.TypeError);
			expect(() => observer.observe(<never>document.createTextNode('text'))).toThrow(
				window.TypeError
			);
		});
	});

	describe('takeRecords()', () => {
		it('Returns pending records and prevents the callback from receiving them.', async () => {
			const target = document.createElement('div');
			let called = false;
			const observer = new window.IntersectionObserver(() => {
				called = true;
			});

			setRect(target, 0, 0, 20, 20);
			observer.observe(target);

			const records = observer.takeRecords();

			expect(records).toHaveLength(1);
			expect(records[0].target).toBe(target);
			expect(observer.takeRecords()).toEqual([]);

			await window.happyDOM.waitUntilComplete();

			expect(called).toBe(false);
		});
	});

	describe('unobserve()', () => {
		it('Stops future entries for that target.', async () => {
			const kept = document.createElement('div');
			const dropped = document.createElement('div');
			const seen: Element[] = [];
			const observer = new window.IntersectionObserver((entries) => {
				seen.push(...entries.map((entry) => <Element>entry.target));
			});
			const keptBox = { x: 3000, y: 0, width: 100, height: 100 };
			const droppedBox = { x: 3000, y: 0, width: 100, height: 100 };

			document.body.appendChild(kept);
			document.body.appendChild(dropped);
			setRectBox(kept, keptBox);
			setRectBox(dropped, droppedBox);
			observer.observe(kept);
			observer.observe(dropped);
			observer.unobserve(dropped);

			expect(observer.takeRecords().map((entry) => entry.target)).toEqual([kept]);

			await window.happyDOM.waitUntilComplete();

			keptBox.x = 0;
			droppedBox.x = 0;
			kept.setAttribute('data-move', '1');

			await window.happyDOM.waitUntilComplete();

			expect(seen).toEqual([kept]);
		});

		it('Throws when the argument is not an element.', () => {
			const observer = new window.IntersectionObserver(() => {});

			expect(() => observer.unobserve(<never>null)).toThrow(window.TypeError);
		});
	});

	describe('disconnect()', () => {
		it('Stops future delivery and clears pending records.', async () => {
			const target = document.createElement('div');
			let called = false;
			const observer = new window.IntersectionObserver(() => {
				called = true;
			});

			setRect(target, 0, 0, 30, 30);
			observer.observe(target);
			observer.disconnect();

			expect(observer.takeRecords()).toEqual([]);

			await window.happyDOM.waitUntilComplete();

			expect(called).toBe(false);

			target.setAttribute('data-move', '1');
			await window.happyDOM.waitUntilComplete();

			expect(called).toBe(false);
		});

		it('Can observe again after disconnect and queues a new initial entry.', async () => {
			const target = document.createElement('div');
			const ratios: number[] = [];
			const observer = new window.IntersectionObserver((entries) => {
				ratios.push(entries[0].intersectionRatio);
			});

			setRect(target, 4000, 0, 10, 10);
			observer.observe(target);
			observer.disconnect();
			setRect(target, 0, 0, 10, 10);
			observer.observe(target);

			await window.happyDOM.waitUntilComplete();

			expect(ratios).toEqual([1]);
		});
	});

	describe('intersection geometry', () => {
		it('Intersects a target with the viewport.', async () => {
			const target = document.createElement('div');
			const entry = await observeEntry(target, { x: 1000, y: 0, width: 48, height: 20 });

			expect(entry.rootBounds?.x).toBe(0);
			expect(entry.rootBounds?.y).toBe(0);
			expect(entry.rootBounds?.width).toBe(window.innerWidth);
			expect(entry.rootBounds?.height).toBe(window.innerHeight);
			expect(entry.intersectionRect.width).toBe(24);
			expect(entry.intersectionRect.height).toBe(20);
			expect(entry.intersectionRatio).toBe(0.5);
			expect(entry.isIntersecting).toBe(true);
		});

		it('Intersects a target with an element root.', async () => {
			const root = document.createElement('div');
			const target = document.createElement('div');

			setRect(root, 10, 20, 100, 80);
			setRect(target, 60, 40, 100, 40);

			const entry = await observeEntry(target, null, { root });

			expect(entry.rootBounds?.x).toBe(10);
			expect(entry.rootBounds?.width).toBe(100);
			expect(entry.intersectionRect.x).toBe(60);
			expect(entry.intersectionRect.y).toBe(40);
			expect(entry.intersectionRect.width).toBe(50);
			expect(entry.intersectionRect.height).toBe(40);
			expect(entry.intersectionRatio).toBe(0.5);
		});

		it('Applies root margins in pixels.', async () => {
			const root = document.createElement('div');
			const target = document.createElement('div');

			setRect(root, 0, 0, 100, 100);
			setRect(target, 100, 0, 20, 20);

			const outside = await observeEntry(target, null, { root, rootMargin: '0px' });
			const inside = await observeEntry(target, null, { root, rootMargin: '10px' });

			expect(outside.intersectionRatio).toBe(0);
			expect(outside.isIntersecting).toBe(false);
			expect(outside.intersectionRect.width).toBe(0);
			expect(inside.rootBounds?.x).toBe(-10);
			expect(inside.rootBounds?.width).toBe(120);
			expect(inside.rootBounds?.height).toBe(120);
			expect(inside.intersectionRect.width).toBe(10);
			expect(inside.intersectionRect.height).toBe(20);
			expect(inside.intersectionRatio).toBe(0.5);
		});

		it('Resolves percentage root margins against the root width.', async () => {
			const root = document.createElement('div');
			const target = document.createElement('div');

			setRect(root, 0, 0, 200, 100);
			setRect(target, -10, 0, 10, 10);

			const entry = await observeEntry(target, null, { root, rootMargin: '10%' });

			expect(entry.rootBounds?.x).toBe(-20);
			expect(entry.rootBounds?.y).toBe(-20);
			expect(entry.rootBounds?.width).toBe(240);
			expect(entry.rootBounds?.height).toBe(140);
			expect(entry.intersectionRatio).toBe(1);
			expect(entry.isIntersecting).toBe(true);
		});

		it('Reports ratio 1 for a zero-area target inside the root and 0 when it is outside.', async () => {
			const inside = document.createElement('div');
			const outside = document.createElement('div');
			const line = document.createElement('div');

			const insideEntry = await observeEntry(inside, { x: 10, y: 10, width: 0, height: 0 });
			const outsideEntry = await observeEntry(outside, { x: 5000, y: 10, width: 0, height: 0 });
			const lineEntry = await observeEntry(line, { x: 0, y: 5000, width: 40, height: 0 });

			expect(insideEntry.intersectionRatio).toBe(1);
			expect(insideEntry.isIntersecting).toBe(true);
			expect(outsideEntry.intersectionRatio).toBe(0);
			expect(outsideEntry.isIntersecting).toBe(false);
			expect(lineEntry.intersectionRatio).toBe(0);
			expect(lineEntry.isIntersecting).toBe(false);
		});

		it('Uses inline style geometry when no client rect is provided.', async () => {
			const root = document.createElement('div');
			const target = document.createElement('div');

			root.style.position = 'absolute';
			root.style.left = '0px';
			root.style.top = '0px';
			root.style.width = '200px';
			root.style.height = '100px';
			target.style.position = 'absolute';
			target.style.left = '150px';
			target.style.top = '0px';
			target.style.width = '100px';
			target.style.height = '100px';
			root.appendChild(target);

			const entry = await observeEntry(target, null, { root });

			expect(entry.boundingClientRect.x).toBe(150);
			expect(entry.boundingClientRect.width).toBe(100);
			expect(entry.intersectionRect.width).toBe(50);
			expect(entry.intersectionRatio).toBe(0.5);
		});

		it('Queues a new entry when a target crosses a threshold.', async () => {
			const target = document.createElement('div');
			const box = { x: 4000, y: 0, width: 100, height: 100 };
			const ratios: number[] = [];
			const observer = new window.IntersectionObserver(
				(entries) => {
					ratios.push(entries[0].intersectionRatio);
				},
				{ threshold: [0, 0.5, 1] }
			);

			document.body.appendChild(target);
			setRectBox(target, box);
			observer.observe(target);
			await window.happyDOM.waitUntilComplete();

			box.x = 0;
			target.setAttribute('data-move', 'in');
			await window.happyDOM.waitUntilComplete();

			box.x = window.innerWidth - 50;
			target.setAttribute('data-move', 'half');
			await window.happyDOM.waitUntilComplete();

			expect(ratios).toEqual([0, 1, 0.5]);
		});
	});

	/**
	 * Reads the normalized root margin.
	 *
	 * @param rootMargin Root margin.
	 * @returns Normalized root margin.
	 */
	function margin(rootMargin: string): string {
		return new window.IntersectionObserver(() => {}, { rootMargin }).rootMargin;
	}

	/**
	 * Assigns a fixed client rectangle.
	 *
	 * @param element Element.
	 * @param x X.
	 * @param y Y.
	 * @param width Width.
	 * @param height Height.
	 */
	function setRect(element: Element, x: number, y: number, width: number, height: number): void {
		setRectBox(element, { x, y, width, height });
	}

	/**
	 * Assigns a mutable client rectangle.
	 *
	 * @param element Element.
	 * @param rect Rectangle.
	 * @param rect.x X.
	 * @param rect.y Y.
	 * @param rect.width Width.
	 * @param rect.height Height.
	 */
	function setRectBox(
		element: Element,
		rect: { x: number; y: number; width: number; height: number }
	): void {
		element.getBoundingClientRect = () =>
			new window.DOMRect(rect.x, rect.y, rect.width, rect.height);
	}

	/**
	 * Observes a target and returns its initial entry.
	 *
	 * @param target Target.
	 * @param rect Rectangle, or null to keep the element's own geometry.
	 * @param [options] Observer options.
	 * @returns Initial entry.
	 */
	async function observeEntry(
		target: Element,
		rect: { x: number; y: number; width: number; height: number } | null,
		options?: ConstructorParameters<typeof window.IntersectionObserver>[1]
	): Promise<IntersectionObserverEntry> {
		if (rect) {
			setRectBox(target, rect);
		}

		let entry: IntersectionObserverEntry | null = null;
		const observer = new window.IntersectionObserver((entries) => {
			entry = entries[0];
		}, options);

		observer.observe(target);
		await window.happyDOM.waitUntilComplete();

		if (!entry) {
			throw new Error('Expected an intersection entry.');
		}

		return entry;
	}
});
