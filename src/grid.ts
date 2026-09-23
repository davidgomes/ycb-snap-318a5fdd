import Yoga, {type Node as YogaNode} from 'yoga-layout';
import {type DOMElement} from './dom.js';

type TrackBound = {type: 'fixed'; value: number} | {type: 'fr'; value: number};

type Track =
	| {type: 'fixed'; value: number}
	| {type: 'fr'; value: number}
	| {type: 'auto'}
	| {type: 'minmax'; min: number; max: TrackBound};

type Placement = {
	column: number;
	columnSpan: number;
	row: number;
	rowSpan: number;
};

type Length = number | 'auto' | `${number}%` | undefined;

type LineRange = {start: number; span: number} | undefined;

const parseBound = (token: string): TrackBound => {
	if (token.endsWith('fr')) {
		return {type: 'fr', value: Number.parseFloat(token) || 0};
	}

	return {type: 'fixed', value: Number.parseFloat(token) || 0};
};

export const parseTracks = (template: string | undefined): Track[] => {
	if (!template) {
		return [];
	}

	const tokens = template.trim().match(/minmax\([^)]*\)|\S+/g) ?? [];

	return tokens.map((token): Track => {
		const minmax = /^minmax\(\s*([^,]+?)\s*,\s*([^)]+?)\s*\)$/.exec(token);
		if (minmax) {
			return {
				type: 'minmax',
				min: Number.parseFloat(minmax[1]!) || 0,
				max: parseBound(minmax[2]!),
			};
		}

		if (token === 'auto') {
			return {type: 'auto'};
		}

		return parseBound(token);
	});
};

const parseLine = (value: number | string | undefined): LineRange => {
	if (value === undefined) {
		return undefined;
	}

	const [startPart, endPart] = String(value).split('/');
	const start = Math.max(1, Number.parseInt(startPart!, 10) || 1);

	if (endPart === undefined) {
		return {start: start - 1, span: 1};
	}

	const end = Number.parseInt(endPart, 10);
	const span = Number.isNaN(end) ? 1 : Math.max(1, end - start);

	return {start: start - 1, span};
};

const placeItems = (
	items: DOMElement[],
	columnCount: number,
): {placements: Placement[]; columnCount: number; rowCount: number} => {
	const ranges = items.map(item => ({
		column: parseLine(item.style.gridColumn),
		row: parseLine(item.style.gridRow),
	}));

	for (const {column} of ranges) {
		if (column) {
			columnCount = Math.max(columnCount, column.start + column.span);
		}
	}

	columnCount = Math.max(columnCount, 1);

	const occupied = new Set<string>();
	const isFree = (placement: Placement): boolean => {
		for (let r = placement.row; r < placement.row + placement.rowSpan; r++) {
			for (
				let c = placement.column;
				c < placement.column + placement.columnSpan;
				c++
			) {
				if (occupied.has(`${r}:${c}`)) {
					return false;
				}
			}
		}

		return true;
	};

	const occupy = (placement: Placement): void => {
		for (let r = placement.row; r < placement.row + placement.rowSpan; r++) {
			for (
				let c = placement.column;
				c < placement.column + placement.columnSpan;
				c++
			) {
				occupied.add(`${r}:${c}`);
			}
		}
	};

	const placements: Array<Placement | undefined> = ranges.map(() => undefined);

	// Fully explicit items are placed first so auto-placed items flow around them.
	for (const [index, {column, row}] of ranges.entries()) {
		if (column && row) {
			const placement = {
				column: column.start,
				columnSpan: column.span,
				row: row.start,
				rowSpan: row.span,
			};
			occupy(placement);
			placements[index] = placement;
		}
	}

	for (const [index, {column, row}] of ranges.entries()) {
		if (placements[index]) {
			continue;
		}

		const columnSpan = Math.min(column?.span ?? 1, columnCount);
		const rowSpan = row?.span ?? 1;
		let found: Placement | undefined;

		for (let r = row?.start ?? 0; !found; r++) {
			const columnStarts = column
				? [column.start]
				: Array.from({length: columnCount - columnSpan + 1}, (_, c) => c);

			for (const c of columnStarts) {
				const candidate = {column: c, columnSpan, row: r, rowSpan};
				if (isFree(candidate)) {
					found = candidate;
					break;
				}
			}

			if (row && !found) {
				// Row is fixed; stack onto the next free column-less slot by ignoring overlap.
				found = {
					column: column?.start ?? 0,
					columnSpan,
					row: row.start,
					rowSpan,
				};
			}
		}

		occupy(found);
		placements[index] = found;
	}

	let rowCount = 0;
	for (const placement of placements) {
		rowCount = Math.max(rowCount, placement!.row + placement!.rowSpan);
	}

	return {placements: placements as Placement[], columnCount, rowCount};
};

const distribute = (amount: number, weights: number[]): number[] => {
	const total = weights.reduce((sum, weight) => sum + weight, 0);
	if (total <= 0 || amount <= 0) {
		return weights.map(() => 0);
	}

	const exact = weights.map(weight => (amount * weight) / total);
	const result = exact.map(value => Math.floor(value));
	let remainder = Math.round(amount - result.reduce((sum, v) => sum + v, 0));

	const order = exact
		.map((value, index) => ({index, fraction: value - Math.floor(value)}))
		.filter(({index}) => weights[index]! > 0)
		.sort((a, b) => b.fraction - a.fraction || a.index - b.index);

	for (const {index} of order) {
		if (remainder <= 0) {
			break;
		}

		result[index]!++;
		remainder--;
	}

	return result;
};

// `available` is undefined when the container size along this axis is indefinite.
export const sizeTracks = (
	tracks: Track[],
	contentSizes: number[],
	available: number | undefined,
	gap: number,
): number[] => {
	const isFr = (track: Track): boolean =>
		track.type === 'fr' || (track.type === 'minmax' && track.max.type === 'fr');

	const sizes = tracks.map((track, index): number => {
		const content = contentSizes[index] ?? 0;
		if (track.type === 'fixed') {
			return track.value;
		}

		if (track.type === 'auto') {
			return content;
		}

		if (track.type === 'fr') {
			return available === undefined ? content : 0;
		}

		if (available === undefined) {
			return track.max.type === 'fixed'
				? Math.max(track.min, Math.min(content, track.max.value))
				: Math.max(track.min, content);
		}

		return track.min;
	});

	if (available === undefined) {
		return sizes;
	}

	const gaps = gap * Math.max(0, tracks.length - 1);
	let free = available - gaps - sizes.reduce((sum, size) => sum + size, 0);

	// Grow minmax tracks with a fixed maximum toward that maximum.
	const growable = tracks.map((track, index) =>
		track.type === 'minmax' && track.max.type === 'fixed'
			? Math.max(0, track.max.value - sizes[index]!)
			: 0,
	);
	const totalGrowable = growable.reduce((sum, value) => sum + value, 0);
	if (free > 0 && totalGrowable > 0) {
		const grow = free >= totalGrowable ? growable : distribute(free, growable);
		for (const [index, value] of grow.entries()) {
			sizes[index]! += value;
			free -= value;
		}
	}

	const frWeights = tracks.map(track => {
		if (track.type === 'fr') {
			return track.value;
		}

		if (track.type === 'minmax' && track.max.type === 'fr') {
			return track.max.value;
		}

		return 0;
	});

	if (tracks.some(track => isFr(track))) {
		for (const [index, value] of distribute(free, frWeights).entries()) {
			sizes[index]! += value;
		}
	} else if (free > 0) {
		const autoWeights = tracks.map(track => (track.type === 'auto' ? 1 : 0));
		for (const [index, value] of distribute(free, autoWeights).entries()) {
			sizes[index]! += value;
		}
	}

	return sizes;
};

type GridManaged = {
	internal_gridItem?: boolean;
	internal_gridContainer?: boolean;
};

const isGrid = (node: DOMElement): boolean => node.style.display === 'grid';

const gridItems = (node: DOMElement): DOMElement[] =>
	node.childNodes.filter(
		(child): child is DOMElement =>
			child.nodeName !== '#text' &&
			child.yogaNode !== undefined &&
			child.style.display !== 'none',
	);

const restoreItem = (child: DOMElement): void => {
	const yoga = child.yogaNode!;
	const {style} = child;
	yoga.setPositionType(
		style.position === 'absolute'
			? Yoga.POSITION_TYPE_ABSOLUTE
			: style.position === 'static'
				? Yoga.POSITION_TYPE_STATIC
				: Yoga.POSITION_TYPE_RELATIVE,
	);
	yoga.setPosition(Yoga.EDGE_LEFT, style.left as Exclude<Length, 'auto'>);
	yoga.setPosition(Yoga.EDGE_TOP, style.top as Exclude<Length, 'auto'>);
	yoga.setWidth(style.width as Length);
	yoga.setHeight(style.height as Length);
	delete (child as GridManaged).internal_gridItem;
};

const restoreNonGridNodes = (node: DOMElement): void => {
	if (!isGrid(node)) {
		if ((node as GridManaged).internal_gridContainer) {
			node.yogaNode?.setHeight(node.style.height as Length);
			delete (node as GridManaged).internal_gridContainer;
		}

		for (const child of node.childNodes) {
			if (
				child.nodeName !== '#text' &&
				(child as GridManaged).internal_gridItem
			) {
				restoreItem(child);
			}
		}
	}

	for (const child of node.childNodes) {
		if (child.nodeName !== '#text') {
			restoreNonGridNodes(child);
		}
	}
};

const collectGrids = (node: DOMElement, result: DOMElement[]): void => {
	if (node.yogaNode?.getDisplay() === Yoga.DISPLAY_NONE) {
		return;
	}

	if (isGrid(node) && node.yogaNode) {
		result.push(node);
	}

	for (const child of node.childNodes) {
		if (child.nodeName !== '#text') {
			collectGrids(child, result);
		}
	}
};

const horizontalMargin = (yoga: YogaNode): number =>
	yoga.getComputedMargin(Yoga.EDGE_LEFT) +
	yoga.getComputedMargin(Yoga.EDGE_RIGHT);

const verticalMargin = (yoga: YogaNode): number =>
	yoga.getComputedMargin(Yoga.EDGE_TOP) +
	yoga.getComputedMargin(Yoga.EDGE_BOTTOM);

const layoutGrid = (container: DOMElement, recalculate: () => void): void => {
	const yoga = container.yogaNode!;
	const {style} = container;
	const items = gridItems(container);
	(container as GridManaged).internal_gridContainer = true;

	const columnGap = style.columnGap ?? style.gap ?? 0;
	const rowGap = style.rowGap ?? style.gap ?? 0;
	const columnTemplate = parseTracks(style.gridTemplateColumns);
	const rowTemplate = parseTracks(style.gridTemplateRows);

	const {placements, columnCount, rowCount} = placeItems(
		items,
		columnTemplate.length,
	);

	const columns: Track[] = Array.from(
		{length: columnCount},
		(_, index) => columnTemplate[index] ?? {type: 'auto'},
	);
	const rows: Track[] = Array.from(
		{length: Math.max(rowCount, rowTemplate.length)},
		(_, index) => rowTemplate[index] ?? {type: 'auto'},
	);

	const paddingLeft =
		yoga.getComputedBorder(Yoga.EDGE_LEFT) +
		yoga.getComputedPadding(Yoga.EDGE_LEFT);
	const paddingTop =
		yoga.getComputedBorder(Yoga.EDGE_TOP) +
		yoga.getComputedPadding(Yoga.EDGE_TOP);
	const horizontalChrome =
		paddingLeft +
		yoga.getComputedBorder(Yoga.EDGE_RIGHT) +
		yoga.getComputedPadding(Yoga.EDGE_RIGHT);
	const verticalChrome =
		paddingTop +
		yoga.getComputedBorder(Yoga.EDGE_BOTTOM) +
		yoga.getComputedPadding(Yoga.EDGE_BOTTOM);

	for (const item of items) {
		(item as GridManaged).internal_gridItem = true;
		item.yogaNode!.setPositionType(Yoga.POSITION_TYPE_ABSOLUTE);
		item.yogaNode!.setWidth(item.style.width as Length);
		item.yogaNode!.setHeight(item.style.height as Length);
	}

	if (style.height === undefined) {
		yoga.setHeight(undefined);
	}

	recalculate();

	const availableWidth = Math.max(
		0,
		yoga.getComputedWidth() - horizontalChrome,
	);
	const availableHeight =
		style.height === undefined
			? undefined
			: Math.max(0, yoga.getComputedHeight() - verticalChrome);

	const columnContent = columns.map(() => 0);
	for (const [index, item] of items.entries()) {
		const placement = placements[index]!;
		if (placement.columnSpan === 1) {
			const itemYoga = item.yogaNode!;
			columnContent[placement.column] = Math.max(
				columnContent[placement.column]!,
				itemYoga.getComputedWidth() + horizontalMargin(itemYoga),
			);
		}
	}

	const columnSizes = sizeTracks(
		columns,
		columnContent,
		availableWidth,
		columnGap,
	);

	const areaSize = (
		sizes: number[],
		start: number,
		span: number,
		gap: number,
	): number =>
		sizes.slice(start, start + span).reduce((sum, size) => sum + size, 0) +
		gap * (span - 1);

	const offset = (sizes: number[], index: number, gap: number): number =>
		sizes.slice(0, index).reduce((sum, size) => sum + size, 0) + gap * index;

	for (const [index, item] of items.entries()) {
		const placement = placements[index]!;
		const itemYoga = item.yogaNode!;
		if (item.style.width === undefined) {
			itemYoga.setWidth(
				Math.max(
					0,
					areaSize(
						columnSizes,
						placement.column,
						placement.columnSpan,
						columnGap,
					) - horizontalMargin(itemYoga),
				),
			);
		}
	}

	recalculate();

	const rowContent = rows.map(() => 0);
	for (const [index, item] of items.entries()) {
		const placement = placements[index]!;
		if (placement.rowSpan === 1) {
			const itemYoga = item.yogaNode!;
			rowContent[placement.row] = Math.max(
				rowContent[placement.row]!,
				itemYoga.getComputedHeight() + verticalMargin(itemYoga),
			);
		}
	}

	const rowSizes = sizeTracks(rows, rowContent, availableHeight, rowGap);

	for (const [index, item] of items.entries()) {
		const placement = placements[index]!;
		const itemYoga = item.yogaNode!;
		itemYoga.setPosition(
			Yoga.EDGE_LEFT,
			paddingLeft + offset(columnSizes, placement.column, columnGap),
		);
		itemYoga.setPosition(
			Yoga.EDGE_TOP,
			paddingTop + offset(rowSizes, placement.row, rowGap),
		);
		if (item.style.height === undefined) {
			itemYoga.setHeight(
				Math.max(
					0,
					areaSize(rowSizes, placement.row, placement.rowSpan, rowGap) -
						verticalMargin(itemYoga),
				),
			);
		}
	}

	if (style.height === undefined) {
		yoga.setHeight(
			verticalChrome + areaSize(rowSizes, 0, rowSizes.length, rowGap),
		);
	}

	recalculate();
};

/**
Yoga has no CSS grid support, so grid containers are laid out on top of Yoga's result: their children become absolutely positioned and sized according to the computed tracks.
*/
const calculateGridLayout = (
	rootNode: DOMElement,
	recalculate: () => void,
): void => {
	restoreNonGridNodes(rootNode);

	const grids: DOMElement[] = [];
	collectGrids(rootNode, grids);

	if (grids.length === 0) {
		recalculate();
		return;
	}

	recalculate();

	// Outer grids are laid out first; a second pass settles outer tracks that depend on nested grids.
	const passes = grids.length > 1 ? 2 : 1;
	for (let pass = 0; pass < passes; pass++) {
		for (const grid of grids) {
			layoutGrid(grid, recalculate);
		}
	}
};

export default calculateGridLayout;
