import Window from '../../src/window/Window.js';
import type Document from '../../src/nodes/document/Document.js';
import type Element from '../../src/nodes/element/Element.js';
import DOMRect from '../../src/dom/DOMRect.js';
import IntersectionObserver from '../../src/intersection-observer/IntersectionObserver.js';
import IntersectionObserverEntry from '../../src/intersection-observer/IntersectionObserverEntry.js';
import DOMExceptionNameEnum from '../../src/exception/DOMExceptionNameEnum.js';
import { beforeEach, describe, it, expect, vi } from 'vitest';

type TCallback = (entries: IntersectionObserverEntry[], observer: IntersectionObserver) => void;

describe('IntersectionObserver', () => {
	let window: Window;
	let document: Document;

	beforeEach(() => {
		window = new Window({ width: 1024, height: 768 });
		document = window.document;
	});

	function setRect(element: Element, x: number, y: number, width: number, height: number): void {
		element.getBoundingClientRect = () => new DOMRect(x, y, width, height);
	}

	function createElement(
		id: string,
		x: number,
		y: number,
		width: number,
		height: number,
		parent: Element = document.body
	): Element {
		const element = document.createElement('div');
		element.id = id;
		setRect(element, x, y, width, height);
		parent.appendChild(element);
		return element;
	}

	function getError(callback: () => unknown): Error | null {
		try {
			callback();
		} catch (error) {
			return <Error>error;
		}
		return null;
	}

	function getTargetIDs(entries: IntersectionObserverEntry[]): string[] {
		return entries.map((entry) => (<Element>entry.target).id);
	}

	function dispatchScroll(): void {
		window.dispatchEvent(new window.Event('scroll'));
	}

	async function waitForDelivery(): Promise<void> {
		await new Promise((resolve) => setTimeout(resolve, 1));
	}

	describe('constructor()', () => {
		it('Uses default options.', () => {
			const observer = new window.IntersectionObserver(() => {});

			expect(observer.root).toBe(null);
			expect(observer.rootMargin).toBe('0px 0px 0px 0px');
			expect(observer.thresholds).toEqual([0]);
		});

		it('Supports an element, a document or null as root.', () => {
			const root = document.createElement('div');

			expect(new window.IntersectionObserver(() => {}, { root }).root).toBe(root);
			expect(new window.IntersectionObserver(() => {}, { root: document }).root).toBe(document);
			expect(new window.IntersectionObserver(() => {}, { root: null }).root).toBe(null);
		});

		it('Expands the root margin shorthand to four values.', () => {
			const rootMargins: { [rootMargin: string]: string } = {
				'10px': '10px 10px 10px 10px',
				'10px 20%': '10px 20% 10px 20%',
				'1px 2px 3px': '1px 2px 3px 2px',
				'-1px 2.5px 3% 4PX': '-1px 2.5px 3% 4px',
				' \n5px\t 6px ': '5px 6px 5px 6px',
				'': '0px 0px 0px 0px'
			};

			for (const rootMargin of Object.keys(rootMargins)) {
				expect(new window.IntersectionObserver(() => {}, { rootMargin }).rootMargin).toBe(
					rootMargins[rootMargin]
				);
			}
		});

		it('Normalizes thresholds to sorted unique values.', () => {
			const observer = new window.IntersectionObserver(() => {}, {
				threshold: [1, 0, 0.5, 0.25, 0.5]
			});

			expect(observer.thresholds).toEqual([0, 0.25, 0.5, 1]);
			expect(Object.isFrozen(observer.thresholds)).toBe(true);
			expect(new window.IntersectionObserver(() => {}, { threshold: 0.5 }).thresholds).toEqual([
				0.5
			]);
			expect(new window.IntersectionObserver(() => {}, { threshold: [] }).thresholds).toEqual([0]);
		});

		it('Throws a TypeError if the callback is missing or not a function.', () => {
			const missingError = getError(() => new (<any>window.IntersectionObserver)());
			const invalidError = getError(() => new window.IntersectionObserver(<any>'callback'));

			expect(missingError).toBeInstanceOf(window.TypeError);
			expect(missingError?.message).toBe(
				"Failed to construct 'IntersectionObserver': 1 argument required, but only 0 present."
			);
			expect(invalidError).toBeInstanceOf(window.TypeError);
			expect(invalidError?.message).toBe(
				"Failed to construct 'IntersectionObserver': parameter 1 is not of type 'Function'."
			);
		});

		it('Throws a TypeError if the options are not an object.', () => {
			const error = getError(() => new window.IntersectionObserver(() => {}, <any>'options'));

			expect(error).toBeInstanceOf(window.TypeError);
			expect(error?.message).toBe(
				"Failed to construct 'IntersectionObserver': The provided value is not of type 'IntersectionObserverInit'."
			);
		});

		it('Throws a TypeError if the root is not an element or a document.', () => {
			for (const root of ['body', {}, document.createTextNode('text')]) {
				const error = getError(() => new window.IntersectionObserver(() => {}, { root: <any>root }));

				expect(error).toBeInstanceOf(window.TypeError);
				expect(error?.message).toBe(
					"Failed to construct 'IntersectionObserver': Failed to read the 'root' property from 'IntersectionObserverInit': The provided value is not of type '(Document or Element)'."
				);
			}
		});

		it('Throws a SyntaxError if the root margin is invalid.', () => {
			for (const rootMargin of ['10', '10em', 'auto', '10px,20px', '10.px']) {
				const error = getError(() => new window.IntersectionObserver(() => {}, { rootMargin }));

				expect(error).toBeInstanceOf(window.DOMException);
				expect(error?.name).toBe(DOMExceptionNameEnum.syntaxError);
				expect(error?.message).toBe(
					"Failed to construct 'IntersectionObserver': rootMargin must be specified in pixels or percent."
				);
			}

			const error = getError(
				() => new window.IntersectionObserver(() => {}, { rootMargin: '1px 2px 3px 4px 5px' })
			);

			expect(error).toBeInstanceOf(window.DOMException);
			expect(error?.name).toBe(DOMExceptionNameEnum.syntaxError);
			expect(error?.message).toBe(
				"Failed to construct 'IntersectionObserver': Extra text found at the end of rootMargin."
			);
		});

		it('Throws a RangeError if a threshold is outside of the range 0 to 1.', () => {
			for (const threshold of [1.1, -0.1, [0, 2]]) {
				const error = getError(() => new window.IntersectionObserver(() => {}, { threshold }));

				expect(error).toBeInstanceOf(window.RangeError);
				expect(error?.message).toBe(
					"Failed to construct 'IntersectionObserver': Threshold values must be numbers between 0 and 1"
				);
			}
		});

		it('Throws a TypeError if a threshold is not a finite number.', () => {
			for (const threshold of [NaN, 'threshold', [0, Infinity]]) {
				const error = getError(
					() => new window.IntersectionObserver(() => {}, { threshold: <any>threshold })
				);

				expect(error).toBeInstanceOf(window.TypeError);
				expect(error?.message).toBe(
					"Failed to construct 'IntersectionObserver': Failed to read the 'threshold' property from 'IntersectionObserverInit': The provided double value is non-finite."
				);
			}
		});

		it('Throws a TypeError if constructed outside a Window context.', () => {
			expect(() => new IntersectionObserver(() => {})).toThrow(
				new TypeError(
					"Failed to construct 'IntersectionObserver': 'IntersectionObserver' was constructed outside a Window context."
				)
			);
		});
	});

	describe('observe()', () => {
		it('Throws a TypeError if the target is not an element.', () => {
			const observer = new window.IntersectionObserver(() => {});
			const missingError = getError(() => (<any>observer).observe());

			expect(missingError).toBeInstanceOf(window.TypeError);
			expect(missingError?.message).toBe(
				"Failed to execute 'observe' on 'IntersectionObserver': 1 argument required, but only 0 present."
			);

			for (const target of [undefined, null, {}, document, document.createTextNode('text')]) {
				const error = getError(() => observer.observe(<any>target));

				expect(error).toBeInstanceOf(window.TypeError);
				expect(error?.message).toBe(
					"Failed to execute 'observe' on 'IntersectionObserver': parameter 1 is not of type 'Element'."
				);
			}
		});

		it('Delivers an initial entry asynchronously.', async () => {
			const target = createElement('target', 10, 20, 100, 50);
			const callback = vi.fn<TCallback>();
			const observer = new window.IntersectionObserver(callback);

			observer.observe(target);

			expect(callback).not.toHaveBeenCalled();

			await waitForDelivery();

			expect(callback).toHaveBeenCalledTimes(1);

			const [entries, observerArgument] = callback.mock.calls[0];

			expect(observerArgument).toBe(observer);
			expect(callback.mock.contexts[0]).toBe(observer);
			expect(entries.length).toBe(1);
			expect(entries[0]).toBeInstanceOf(window.IntersectionObserverEntry);
			expect(entries[0].target).toBe(target);
			expect(entries[0].isIntersecting).toBe(true);
			expect(entries[0].intersectionRatio).toBe(1);
			expect(entries[0].boundingClientRect).toEqual(new DOMRect(10, 20, 100, 50));
			expect(entries[0].intersectionRect).toEqual(new DOMRect(10, 20, 100, 50));
			expect(entries[0].rootBounds).toEqual(new DOMRect(0, 0, 1024, 768));
			expect(entries[0].time).toBeGreaterThan(0);
		});

		it('Delivers the initial entries of all targets in observation order in one callback.', async () => {
			const a = createElement('a', 0, 0, 10, 10);
			const b = createElement('b', 0, 0, 10, 10);
			const c = createElement('c', 0, 0, 10, 10);
			const callback = vi.fn<TCallback>();
			const observer = new window.IntersectionObserver(callback);

			observer.observe(c);
			observer.observe(a);
			observer.observe(b);

			await waitForDelivery();

			expect(callback).toHaveBeenCalledTimes(1);
			expect(getTargetIDs(callback.mock.calls[0][0])).toEqual(['c', 'a', 'b']);
		});

		it('Ignores targets that are already observed.', async () => {
			const target = createElement('target', 0, 0, 10, 10);
			const callback = vi.fn<TCallback>();
			const observer = new window.IntersectionObserver(callback);

			observer.observe(target);
			observer.observe(target);

			await waitForDelivery();

			expect(callback).toHaveBeenCalledTimes(1);
			expect(callback.mock.calls[0][0].length).toBe(1);
		});

		it('Delivers an initial entry for targets that are not intersecting.', async () => {
			const target = createElement('target', 2000, 0, 100, 100);
			const callback = vi.fn<TCallback>();
			const observer = new window.IntersectionObserver(callback);

			observer.observe(target);

			await waitForDelivery();

			const entry = callback.mock.calls[0][0][0];

			expect(entry.isIntersecting).toBe(false);
			expect(entry.intersectionRatio).toBe(0);
			expect(entry.intersectionRect).toEqual(new DOMRect(0, 0, 0, 0));
			expect(entry.boundingClientRect).toEqual(new DOMRect(2000, 0, 100, 100));
		});
	});

	describe('Intersection calculation', () => {
		it('Calculates the intersection with the viewport.', () => {
			const target = createElement('target', 1000, 700, 48, 136);
			const observer = new window.IntersectionObserver(() => {});

			observer.observe(target);

			const [entry] = observer.takeRecords();

			expect(entry.isIntersecting).toBe(true);
			expect(entry.intersectionRatio).toBe(0.25);
			expect(entry.intersectionRect).toEqual(new DOMRect(1000, 700, 24, 68));
			expect(entry.rootBounds).toEqual(new DOMRect(0, 0, 1024, 768));
		});

		it('Calculates the intersection with a root element.', () => {
			const root = createElement('root', 100, 100, 200, 200);
			const partial = createElement('partial', 250, 150, 100, 100, root);
			const outside = createElement('outside', 400, 100, 50, 50, root);
			const observer = new window.IntersectionObserver(() => {}, { root });

			observer.observe(partial);
			observer.observe(outside);

			const [partialEntry, outsideEntry] = observer.takeRecords();

			expect(partialEntry.isIntersecting).toBe(true);
			expect(partialEntry.intersectionRatio).toBe(0.5);
			expect(partialEntry.intersectionRect).toEqual(new DOMRect(250, 150, 50, 100));
			expect(partialEntry.rootBounds).toEqual(new DOMRect(100, 100, 200, 200));
			expect(outsideEntry.isIntersecting).toBe(false);
			expect(outsideEntry.intersectionRatio).toBe(0);
		});

		it('Uses the viewport of the document for a document root.', () => {
			const target = createElement('target', 0, 0, 10, 10);
			const observer = new window.IntersectionObserver(() => {}, { root: document });

			observer.observe(target);

			expect(observer.takeRecords()[0].rootBounds).toEqual(new DOMRect(0, 0, 1024, 768));
		});

		it('Applies root margins in pixels.', () => {
			const target = createElement('target', 0, 780, 100, 100);
			const observer = new window.IntersectionObserver(() => {}, {
				rootMargin: '10px 20px 30px 40px'
			});

			observer.observe(target);

			const [entry] = observer.takeRecords();

			expect(entry.rootBounds).toEqual(new DOMRect(-40, -10, 1084, 808));
			expect(entry.isIntersecting).toBe(true);
			expect(entry.intersectionRatio).toBe(0.18);
			expect(entry.intersectionRect).toEqual(new DOMRect(0, 780, 100, 18));
		});

		it('Applies negative root margins.', () => {
			const root = createElement('root', 0, 0, 400, 400);
			const target = createElement('target', 50, 50, 100, 100, root);
			const observer = new window.IntersectionObserver(() => {}, { root, rootMargin: '-100px' });

			observer.observe(target);

			const [entry] = observer.takeRecords();

			expect(entry.rootBounds).toEqual(new DOMRect(100, 100, 200, 200));
			expect(entry.intersectionRatio).toBe(0.25);
			expect(entry.intersectionRect).toEqual(new DOMRect(100, 100, 50, 50));
		});

		it('Never intersects when negative root margins invert the root.', () => {
			const root = createElement('root', 0, 0, 100, 100);
			const target = createElement('target', 0, 0, 100, 100, root);
			const observer = new window.IntersectionObserver(() => {}, { root, rootMargin: '-60px' });

			observer.observe(target);

			const [entry] = observer.takeRecords();

			expect(entry.isIntersecting).toBe(false);
			expect(entry.rootBounds).toEqual(new DOMRect(60, 60, 0, 0));
		});

		it('Resolves percentage root margins against the height for top and bottom and the width for right and left.', () => {
			const root = createElement('root', 0, 0, 200, 100);
			const target = createElement('target', 0, 0, 10, 10, root);
			const observer = new window.IntersectionObserver(() => {}, { root, rootMargin: '10% 25%' });

			observer.observe(target);

			expect(observer.takeRecords()[0].rootBounds).toEqual(new DOMRect(-50, -10, 300, 120));
		});

		it('Reports a ratio of 1 for zero-area targets contained in the root and 0 otherwise.', () => {
			const inside = createElement('inside', 10, 10, 0, 0);
			const edge = createElement('edge', 1024, 768, 0, 0);
			const line = createElement('line', 100, 700, 0, 100);
			const outside = createElement('outside', 2000, 10, 0, 0);
			const observer = new window.IntersectionObserver(() => {}, { threshold: [0, 0.5, 1] });

			observer.observe(inside);
			observer.observe(edge);
			observer.observe(line);
			observer.observe(outside);

			const entries = observer.takeRecords();

			expect(entries.map((entry) => entry.isIntersecting)).toEqual([true, true, true, false]);
			expect(entries.map((entry) => entry.intersectionRatio)).toEqual([1, 1, 1, 0]);
		});

		it('Reports edge-adjacent targets as intersecting with a ratio of 0.', () => {
			const target = createElement('target', 0, 768, 100, 100);
			const observer = new window.IntersectionObserver(() => {});

			observer.observe(target);

			const [entry] = observer.takeRecords();

			expect(entry.isIntersecting).toBe(true);
			expect(entry.intersectionRatio).toBe(0);
			expect(entry.intersectionRect).toEqual(new DOMRect(0, 768, 100, 0));
		});

		it('Supports mocked bounding client rects with only some of the properties.', () => {
			const target = document.createElement('div');
			const observer = new window.IntersectionObserver(() => {});

			target.getBoundingClientRect = () =>
				<DOMRect>(<unknown>{ top: 700, left: 0, width: 100, height: 136 });
			observer.observe(target);

			const [entry] = observer.takeRecords();

			expect(entry.boundingClientRect).toEqual(new DOMRect(0, 700, 100, 136));
			expect(entry.intersectionRatio).toBe(0.5);
		});
	});

	describe('Threshold crossing', () => {
		it('Delivers a new entry each time a target crosses a threshold.', async () => {
			const target = createElement('target', 0, 0, 100, 100);
			const entries: IntersectionObserverEntry[] = [];
			const observer = new window.IntersectionObserver(
				(newEntries) => entries.push(...newEntries),
				{ threshold: [0, 0.5, 1] }
			);

			observer.observe(target);
			await waitForDelivery();

			for (const y of [718, 743, 800, 0]) {
				setRect(target, 0, y, 100, 100);
				dispatchScroll();
				await waitForDelivery();
			}

			expect(entries.map((entry) => entry.intersectionRatio)).toEqual([1, 0.5, 0.25, 0, 1]);
			expect(entries.map((entry) => entry.isIntersecting)).toEqual([true, true, true, false, true]);
		});

		it('Does not deliver entries when no threshold is crossed.', async () => {
			const target = createElement('target', 0, 700, 100, 100);
			const entries: IntersectionObserverEntry[] = [];
			const observer = new window.IntersectionObserver(
				(newEntries) => entries.push(...newEntries),
				{ threshold: 0.5 }
			);

			observer.observe(target);
			await waitForDelivery();

			for (const y of [680, 740, 900]) {
				setRect(target, 0, y, 100, 100);
				dispatchScroll();
				await waitForDelivery();
			}

			// 68% -> 88% stays above 0.5, 28% crosses below it and leaving the viewport stays below it.
			expect(entries.map((entry) => entry.intersectionRatio)).toEqual([0.68, 0.28]);
			expect(entries.map((entry) => entry.isIntersecting)).toEqual([true, true]);
		});

		it('Updates on scroll events dispatched on elements and the document.', async () => {
			const target = createElement('target', 0, 0, 100, 100);
			const callback = vi.fn<TCallback>();
			const observer = new window.IntersectionObserver(callback);

			observer.observe(target);
			await waitForDelivery();

			setRect(target, 0, 1000, 100, 100);
			document.body.dispatchEvent(new window.Event('scroll'));
			await waitForDelivery();

			setRect(target, 0, 0, 100, 100);
			document.dispatchEvent(new window.Event('scroll'));
			await waitForDelivery();

			expect(callback).toHaveBeenCalledTimes(3);
			expect(callback.mock.calls[1][0][0].isIntersecting).toBe(false);
			expect(callback.mock.calls[2][0][0].isIntersecting).toBe(true);
		});

		it('Updates on scroll events dispatched on a root element that is not connected to the document.', async () => {
			const root = document.createElement('div');
			const target = createElement('target', 0, 0, 50, 50, root);
			const callback = vi.fn<TCallback>();
			const observer = new window.IntersectionObserver(callback, { root, threshold: 1 });

			setRect(root, 0, 0, 100, 100);
			observer.observe(target);
			await waitForDelivery();

			setRect(target, 75, 0, 50, 50);
			root.dispatchEvent(new window.Event('scroll'));
			await waitForDelivery();

			expect(callback).toHaveBeenCalledTimes(2);
			expect(callback.mock.calls[1][0][0].intersectionRatio).toBe(0.5);
		});

		it('Updates on resize events.', async () => {
			const target = createElement('target', 600, 0, 100, 100);
			const callback = vi.fn<TCallback>();
			const observer = new window.IntersectionObserver(callback);

			observer.observe(target);
			await waitForDelivery();

			window.happyDOM.setViewport({ width: 500, height: 500 });
			await waitForDelivery();

			expect(callback).toHaveBeenCalledTimes(2);

			const entry = callback.mock.calls[1][0][0];

			expect(entry.isIntersecting).toBe(false);
			expect(entry.rootBounds).toEqual(new DOMRect(0, 0, 500, 500));
		});

		it('Delivers changes from multiple events in one callback.', async () => {
			const a = createElement('a', 0, 0, 100, 100);
			const b = createElement('b', 0, 0, 100, 100);
			const callback = vi.fn<TCallback>();
			const observer = new window.IntersectionObserver(callback);

			observer.observe(a);
			observer.observe(b);
			await waitForDelivery();

			setRect(b, 0, 1000, 100, 100);
			dispatchScroll();
			setRect(a, 0, 1000, 100, 100);
			dispatchScroll();
			dispatchScroll();
			await waitForDelivery();

			expect(callback).toHaveBeenCalledTimes(2);
			expect(getTargetIDs(callback.mock.calls[1][0])).toEqual(['a', 'b']);
		});
	});

	describe('unobserve()', () => {
		it('Throws a TypeError if the target is not an element.', () => {
			const observer = new window.IntersectionObserver(() => {});
			const missingError = getError(() => (<any>observer).unobserve());
			const invalidError = getError(() => observer.unobserve(<any>{}));

			expect(missingError).toBeInstanceOf(window.TypeError);
			expect(missingError?.message).toBe(
				"Failed to execute 'unobserve' on 'IntersectionObserver': 1 argument required, but only 0 present."
			);
			expect(invalidError).toBeInstanceOf(window.TypeError);
			expect(invalidError?.message).toBe(
				"Failed to execute 'unobserve' on 'IntersectionObserver': parameter 1 is not of type 'Element'."
			);
		});

		it('Ignores targets that are not observed.', () => {
			const observer = new window.IntersectionObserver(() => {});

			expect(() => observer.unobserve(document.createElement('div'))).not.toThrow();
		});

		it('Stops delivering entries for the target.', async () => {
			const a = createElement('a', 0, 0, 100, 100);
			const b = createElement('b', 0, 0, 100, 100);
			const callback = vi.fn<TCallback>();
			const observer = new window.IntersectionObserver(callback);

			observer.observe(a);
			observer.observe(b);
			observer.unobserve(a);
			await waitForDelivery();

			expect(callback).toHaveBeenCalledTimes(1);
			expect(getTargetIDs(callback.mock.calls[0][0])).toEqual(['b']);

			observer.unobserve(b);
			setRect(a, 0, 1000, 100, 100);
			setRect(b, 0, 1000, 100, 100);
			dispatchScroll();
			await waitForDelivery();

			expect(callback).toHaveBeenCalledTimes(1);
			expect(observer.takeRecords()).toEqual([]);
		});

		it('Delivers a new initial entry when the target is observed again.', async () => {
			const target = createElement('target', 0, 0, 100, 100);
			const callback = vi.fn<TCallback>();
			const observer = new window.IntersectionObserver(callback);

			observer.observe(target);
			await waitForDelivery();

			observer.unobserve(target);
			observer.observe(target);
			await waitForDelivery();

			expect(callback).toHaveBeenCalledTimes(2);
			expect(callback.mock.calls[1][0][0].target).toBe(target);
		});
	});

	describe('disconnect()', () => {
		it('Clears pending records and stops delivering entries.', async () => {
			const target = createElement('target', 0, 0, 100, 100);
			const callback = vi.fn<TCallback>();
			const observer = new window.IntersectionObserver(callback);

			observer.observe(target);
			observer.disconnect();

			expect(observer.takeRecords()).toEqual([]);

			await waitForDelivery();

			setRect(target, 0, 1000, 100, 100);
			dispatchScroll();
			await waitForDelivery();

			expect(callback).not.toHaveBeenCalled();
		});

		it('Stops delivering entries after an initial delivery.', async () => {
			const target = createElement('target', 0, 0, 100, 100);
			const callback = vi.fn<TCallback>();
			const observer = new window.IntersectionObserver(callback);

			observer.observe(target);
			await waitForDelivery();

			observer.disconnect();
			setRect(target, 0, 1000, 100, 100);
			dispatchScroll();
			await waitForDelivery();

			expect(callback).toHaveBeenCalledTimes(1);
		});

		it('Can observe targets again after disconnecting.', async () => {
			const target = createElement('target', 0, 0, 100, 100);
			const callback = vi.fn<TCallback>();
			const observer = new window.IntersectionObserver(callback);

			observer.observe(target);
			observer.disconnect();
			observer.observe(target);
			await waitForDelivery();

			expect(callback).toHaveBeenCalledTimes(1);
			expect(getTargetIDs(callback.mock.calls[0][0])).toEqual(['target']);
		});
	});

	describe('takeRecords()', () => {
		it('Returns an empty array when there are no targets.', () => {
			const observer = new window.IntersectionObserver(() => {});

			expect(observer.takeRecords()).toEqual([]);
		});

		it('Returns pending entries, which are then not delivered to the callback.', async () => {
			const a = createElement('a', 0, 0, 100, 100);
			const b = createElement('b', 0, 0, 100, 100);
			const callback = vi.fn<TCallback>();
			const observer = new window.IntersectionObserver(callback);

			observer.observe(a);
			observer.observe(b);

			expect(getTargetIDs(observer.takeRecords())).toEqual(['a', 'b']);

			await waitForDelivery();

			expect(callback).not.toHaveBeenCalled();
			expect(observer.takeRecords()).toEqual([]);
		});

		it('Returns entries for thresholds crossed since the last delivery.', async () => {
			const target = createElement('target', 0, 0, 100, 100);
			const callback = vi.fn<TCallback>();
			const observer = new window.IntersectionObserver(callback);

			observer.observe(target);
			await waitForDelivery();

			setRect(target, 0, 1000, 100, 100);

			const records = observer.takeRecords();

			expect(records.length).toBe(1);
			expect(records[0].isIntersecting).toBe(false);

			dispatchScroll();
			await waitForDelivery();

			expect(callback).toHaveBeenCalledTimes(1);
		});
	});
});
