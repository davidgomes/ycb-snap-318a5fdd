import Element from '../nodes/element/Element.js';
import type HTMLElement from '../nodes/html-element/HTMLElement.js';

/**
 * Axis-aligned rectangle in viewport coordinates.
 */
export interface IIntersectionRect {
	x: number;
	y: number;
	width: number;
	height: number;
}

/**
 * One root-margin component.
 */
export interface IRootMarginLength {
	value: number;
	unit: 'px' | '%';
}

/**
 * Parsed root margin.
 */
export interface IParsedRootMargin {
	margins: [IRootMarginLength, IRootMarginLength, IRootMarginLength, IRootMarginLength];
	serialized: string;
}

const LENGTH_PATTERN = /^([+-]?(?:\d+|\d*\.\d+))(px|%)$/i;

/**
 * Deterministic intersection geometry used by IntersectionObserver.
 *
 * Rectangles come from an overridden getBoundingClientRect(), otherwise from inline
 * px/% sizing and offset box properties. Root-margin percentages resolve against the
 * undilated root width.
 */
export default class IntersectionObserverGeometry {
	/**
	 * Parses a root margin string into four CSS shorthand lengths.
	 *
	 * @param rootMargin Root margin.
	 * @returns Parsed margin, or null when the string is invalid.
	 */
	public static parseRootMargin(rootMargin: string): IParsedRootMargin | null {
		const tokens = rootMargin.trim() === '' ? [] : rootMargin.trim().split(/\s+/);

		if (tokens.length > 4) {
			return null;
		}

		const parsed: IRootMarginLength[] = [];

		for (const token of tokens) {
			const match = token.match(LENGTH_PATTERN);

			if (!match) {
				return null;
			}

			const value = Number(match[1]);

			if (!Number.isFinite(value)) {
				return null;
			}

			parsed.push({
				value,
				unit: <'px' | '%'>match[2].toLowerCase()
			});
		}

		if (parsed.length === 0) {
			parsed.push({ value: 0, unit: 'px' });
		}

		if (parsed.length === 1) {
			parsed.push(parsed[0], parsed[0], parsed[0]);
		} else if (parsed.length === 2) {
			parsed.push(parsed[0], parsed[1]);
		} else if (parsed.length === 3) {
			parsed.push(parsed[1]);
		}

		const margins: IParsedRootMargin['margins'] = [parsed[0], parsed[1], parsed[2], parsed[3]];

		return {
			margins,
			serialized: margins
				.map((margin) => `${this.formatNumber(margin.value)}${margin.unit}`)
				.join(' ')
		};
	}

	/**
	 * Normalizes a threshold value or list into a sorted unique list.
	 * An empty list becomes `[0]`.
	 *
	 * @param threshold Threshold input.
	 * @returns Normalized thresholds.
	 * @throws TypeError When the value is not a number or list of numbers.
	 * @throws RangeError When a value is non-finite or outside [0, 1].
	 */
	public static parseThresholds(threshold: number | number[] | undefined): number[] {
		let values: number[];

		if (threshold === undefined) {
			values = [0];
		} else if (typeof threshold === 'number') {
			values = [threshold];
		} else if (Array.isArray(threshold)) {
			values = [];

			for (const value of threshold) {
				if (typeof value !== 'number') {
					throw new TypeError(
						"Failed to construct 'IntersectionObserver': The threshold must be a number or a list of numbers."
					);
				}

				values.push(value);
			}
		} else {
			throw new TypeError(
				"Failed to construct 'IntersectionObserver': The threshold must be a number or a list of numbers."
			);
		}

		for (const value of values) {
			if (!Number.isFinite(value)) {
				throw new RangeError(
					"Failed to construct 'IntersectionObserver': The provided double value is non-finite."
				);
			}

			if (value < 0 || value > 1) {
				throw new RangeError(
					"Failed to construct 'IntersectionObserver': Threshold values must be numbers between 0 and 1."
				);
			}
		}

		values.sort((left, right) => left - right);

		const unique: number[] = [];

		for (const value of values) {
			if (unique.length === 0 || unique[unique.length - 1] !== value) {
				unique.push(value);
			}
		}

		if (unique.length === 0) {
			unique.push(0);
		}

		return unique;
	}

	/**
	 * Returns the viewport rectangle anchored at the origin.
	 *
	 * @param width Viewport width.
	 * @param height Viewport height.
	 * @returns Viewport rectangle.
	 */
	public static viewportRect(width: number, height: number): IIntersectionRect {
		return {
			x: 0,
			y: 0,
			width: Number.isFinite(width) && width > 0 ? width : 0,
			height: Number.isFinite(height) && height > 0 ? height : 0
		};
	}

	/**
	 * Returns the border box of an element.
	 *
	 * An overridden getBoundingClientRect() wins. Otherwise inline width, height, and
	 * offsets are used so results stay deterministic without a layout engine.
	 *
	 * @param element Element.
	 * @param viewport Viewport rectangle used for fixed boxes and percentage bases.
	 * @param [seen] Elements currently being resolved.
	 * @returns Element rectangle.
	 */
	public static getElementRect(
		element: Element,
		viewport: IIntersectionRect,
		seen: Set<Element> = new Set()
	): IIntersectionRect {
		if (seen.has(element)) {
			return { x: 0, y: 0, width: 0, height: 0 };
		}

		seen.add(element);

		if (element.getBoundingClientRect !== Element.prototype.getBoundingClientRect) {
			return this.fromClientRect(element.getBoundingClientRect());
		}

		const styled = this.readStyledRect(element, viewport, seen);

		if (styled) {
			return styled;
		}

		const offset = this.readOffsetRect(element);

		if (offset) {
			return offset;
		}

		return this.fromClientRect(element.getBoundingClientRect());
	}

	/**
	 * Expands a root rectangle by the root margin.
	 * Percentages are resolved against the undilated root width.
	 *
	 * @param rect Root rectangle.
	 * @param margins Four margin components, top right bottom left.
	 * @returns Dilated root rectangle.
	 */
	public static applyRootMargin(
		rect: IIntersectionRect,
		margins: IParsedRootMargin['margins']
	): IIntersectionRect {
		const normalized = this.normalize(rect);
		const width = normalized.width;
		const edges = margins.map((margin) =>
			margin.unit === '%' ? (width * margin.value) / 100 : margin.value
		);
		const top = edges[0];
		const right = edges[1];
		const bottom = edges[2];
		const left = edges[3];

		return {
			x: normalized.x - left,
			y: normalized.y - top,
			width: normalized.width + left + right,
			height: normalized.height + top + bottom
		};
	}

	/**
	 * Returns true when the inner rectangle lies entirely inside the outer rectangle.
	 * Edges count as inside, so a zero-area target on the root boundary is contained.
	 *
	 * @param outer Outer rectangle.
	 * @param inner Inner rectangle.
	 * @returns Whether the inner rectangle is contained.
	 */
	public static contains(outer: IIntersectionRect, inner: IIntersectionRect): boolean {
		const container = this.normalize(outer);
		const target = this.normalize(inner);

		return (
			target.x >= container.x &&
			target.y >= container.y &&
			target.x + target.width <= container.x + container.width &&
			target.y + target.height <= container.y + container.height
		);
	}

	/**
	 * Returns the overlap of two rectangles, or a zero rectangle when they are separated.
	 *
	 * @param first First rectangle.
	 * @param second Second rectangle.
	 * @returns Intersection rectangle.
	 */
	public static intersection(
		first: IIntersectionRect,
		second: IIntersectionRect
	): IIntersectionRect {
		const a = this.normalize(first);
		const b = this.normalize(second);
		const x = Math.max(a.x, b.x);
		const y = Math.max(a.y, b.y);
		const right = Math.min(a.x + a.width, b.x + b.width);
		const bottom = Math.min(a.y + a.height, b.y + b.height);

		if (right < x || bottom < y) {
			return { x: 0, y: 0, width: 0, height: 0 };
		}

		return { x, y, width: right - x, height: bottom - y };
	}

	/**
	 * Returns the area of a rectangle.
	 *
	 * @param rect Rectangle.
	 * @returns Area.
	 */
	public static area(rect: IIntersectionRect): number {
		const normalized = this.normalize(rect);
		return normalized.width * normalized.height;
	}

	/**
	 * Returns the threshold slot for a ratio.
	 *
	 * The slot is the number of thresholds less than or equal to the ratio. A target that
	 * does not intersect uses slot 0, which is less than every ratio that has crossed the
	 * lowest threshold.
	 *
	 * @param ratio Intersection ratio.
	 * @param thresholds Sorted thresholds.
	 * @param intersecting False when the target misses the root.
	 * @returns Threshold index.
	 */
	public static thresholdIndex(
		ratio: number,
		thresholds: readonly number[],
		intersecting: boolean
	): number {
		if (!intersecting) {
			return 0;
		}

		let index = 0;

		while (index < thresholds.length && thresholds[index] <= ratio) {
			index++;
		}

		return index;
	}

	/**
	 * Normalizes a client rectangle into a non-negative box.
	 *
	 * @param rect Client rectangle.
	 * @returns Normalized rectangle.
	 */
	public static fromClientRect(
		rect: { x?: number; y?: number; width?: number; height?: number } | null | undefined
	): IIntersectionRect {
		if (!rect) {
			return { x: 0, y: 0, width: 0, height: 0 };
		}

		const x = Number(rect.x);
		const y = Number(rect.y);
		const width = Number(rect.width);
		const height = Number(rect.height);

		return this.normalize({
			x: Number.isFinite(x) ? x : 0,
			y: Number.isFinite(y) ? y : 0,
			width: Number.isFinite(width) ? width : 0,
			height: Number.isFinite(height) ? height : 0
		});
	}

	/**
	 * Converts a rectangle so x/y is the top-left and width/height are non-negative.
	 *
	 * @param rect Rectangle.
	 * @returns Normalized rectangle.
	 */
	public static normalize(rect: IIntersectionRect): IIntersectionRect {
		const left = Math.min(rect.x, rect.x + rect.width);
		const right = Math.max(rect.x, rect.x + rect.width);
		const top = Math.min(rect.y, rect.y + rect.height);
		const bottom = Math.max(rect.y, rect.y + rect.height);

		return {
			x: left,
			y: top,
			width: right - left,
			height: bottom - top
		};
	}

	/**
	 * Reads an inline-style box.
	 *
	 * @param element Element.
	 * @param viewport Viewport rectangle.
	 * @param seen Elements currently being resolved.
	 * @returns Styled rectangle, or null when no geometry was set.
	 */
	private static readStyledRect(
		element: Element,
		viewport: IIntersectionRect,
		seen: Set<Element>
	): IIntersectionRect | null {
		if (!('style' in element)) {
			return null;
		}

		const style = (<HTMLElement>element).style;

		if (!style) {
			return null;
		}

		const widthText = style.width || '';
		const heightText = style.height || '';
		const leftText = style.left || '';
		const topText = style.top || '';
		const rightText = style.right || '';
		const bottomText = style.bottom || '';

		if (!widthText && !heightText && !leftText && !topText && !rightText && !bottomText) {
			return null;
		}

		const position = style.position || '';
		const containing = this.containingRect(element, viewport, seen, position);
		const percentWidth = containing.width > 0 ? containing.width : viewport.width;
		const percentHeight = containing.height > 0 ? containing.height : viewport.height;
		const width = this.parseLength(widthText, percentWidth);
		const height = this.parseLength(heightText, percentHeight);
		const left = this.parseLength(leftText, percentWidth);
		const top = this.parseLength(topText, percentHeight);
		const right = this.parseLength(rightText, percentWidth);
		const bottom = this.parseLength(bottomText, percentHeight);
		const positioned =
			position === 'absolute' ||
			position === 'fixed' ||
			position === 'relative' ||
			position === 'sticky';
		const resolvedWidth = width ?? 0;
		const resolvedHeight = height ?? 0;
		let x = containing.x;
		let y = containing.y;

		if (positioned && left !== null) {
			x = containing.x + left;
		} else if (positioned && right !== null) {
			x = containing.x + containing.width - right - resolvedWidth;
		}

		if (positioned && top !== null) {
			y = containing.y + top;
		} else if (positioned && bottom !== null) {
			y = containing.y + containing.height - bottom - resolvedHeight;
		}

		return {
			x,
			y,
			width: resolvedWidth,
			height: resolvedHeight
		};
	}

	/**
	 * Returns the rectangle offsets are resolved against.
	 *
	 * @param element Element.
	 * @param viewport Viewport rectangle.
	 * @param seen Elements currently being resolved.
	 * @param position CSS position.
	 * @returns Containing rectangle.
	 */
	private static containingRect(
		element: Element,
		viewport: IIntersectionRect,
		seen: Set<Element>,
		position: string
	): IIntersectionRect {
		if (position === 'fixed' || !element.parentElement) {
			return viewport;
		}

		if (position === 'absolute') {
			let parent = element.parentElement;

			while (parent) {
				const parentPosition = 'style' in parent ? (<HTMLElement>parent).style?.position || '' : '';

				if (
					parentPosition === 'absolute' ||
					parentPosition === 'relative' ||
					parentPosition === 'fixed' ||
					parentPosition === 'sticky'
				) {
					return this.getElementRect(parent, viewport, seen);
				}

				if (!parent.parentElement) {
					break;
				}

				parent = parent.parentElement;
			}

			return viewport;
		}

		return this.getElementRect(element.parentElement, viewport, seen);
	}

	/**
	 * Reads the offset box when it has been given a non-zero geometry.
	 *
	 * @param element Element.
	 * @returns Offset rectangle, or null when every offset is zero.
	 */
	private static readOffsetRect(element: Element): IIntersectionRect | null {
		if (!('offsetWidth' in element)) {
			return null;
		}

		const box = <HTMLElement>element;
		const width = box.offsetWidth || 0;
		const height = box.offsetHeight || 0;
		const x = box.offsetLeft || 0;
		const y = box.offsetTop || 0;

		if (!width && !height && !x && !y) {
			return null;
		}

		return { x, y, width, height };
	}

	/**
	 * Parses a px or % length.
	 *
	 * @param value CSS value.
	 * @param percentBase Base length for percentages.
	 * @returns Parsed length, or null when the value is empty or unsupported.
	 */
	private static parseLength(value: string, percentBase: number): number | null {
		if (!value) {
			return null;
		}

		const match = value.trim().match(LENGTH_PATTERN);

		if (!match) {
			return null;
		}

		const amount = Number(match[1]);

		if (!Number.isFinite(amount)) {
			return null;
		}

		if (match[2].toLowerCase() === '%') {
			return (percentBase * amount) / 100;
		}

		return amount;
	}

	/**
	 * Formats a margin number without an unnecessary trailing decimal.
	 *
	 * @param value Number.
	 * @returns Serialized number.
	 */
	private static formatNumber(value: number): string {
		if (!Number.isFinite(value) || value === 0 || Object.is(value, -0)) {
			return '0';
		}

		const text = value.toFixed(6).replace(/\.?0+$/, '');
		return text === '-0' || text === '' ? '0' : text;
	}
}
