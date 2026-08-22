import Yoga, {type Node as YogaNode} from 'yoga-layout';
import {type DOMElement} from './dom.js';

type Track = {
	minimum: number;
	maximum?: number;
	fraction?: number;
	auto: boolean;
};

type Placement = {
	start: number;
	end: number;
};

type GridChildPlacement = {
	node: DOMElement;
	row: number;
	column: number;
	rowEnd: number;
	columnEnd: number;
};

type SizeOptions = {
	tracks: Track[];
	children: GridChildPlacement[];
	axis: 'width' | 'height';
	available: number;
	gap: number;
};

const sum = (values: number[]): number => {
	let total = 0;
	for (const value of values) {
		total += value;
	}

	return total;
};

const parseTrack = (value: string): Track => {
	const minmax =
		/^minmax\(\s*(\d+(?:\.\d+)?)\s*,\s*(\d+(?:\.\d+)?|[\d.]+fr)\s*\)$/i.exec(
			value,
		);
	if (minmax) {
		const maximum = minmax[2]!.toLowerCase();
		return {
			minimum: Number(minmax[1]),
			maximum: maximum.endsWith('fr') ? undefined : Number(maximum),
			fraction: maximum.endsWith('fr') ? Number.parseFloat(maximum) : undefined,
			auto: false,
		};
	}

	if (value.toLowerCase() === 'auto') {
		return {minimum: 0, auto: true};
	}

	if (value.toLowerCase().endsWith('fr')) {
		return {
			minimum: 0,
			fraction: Number.parseFloat(value),
			auto: false,
		};
	}

	return {minimum: Number(value), maximum: Number(value), auto: false};
};

const parseTracks = (value: string | undefined): Track[] => {
	if (!value?.trim()) {
		return [];
	}

	const tokens = value.trim().match(/minmax\([^)]+\)|\S+/gi) ?? [];
	return tokens.map(token => parseTrack(token));
};

const parsePlacement = (
	value: number | string | undefined,
): Placement | undefined => {
	if (value === undefined) {
		return;
	}

	const parts = String(value)
		.split('/')
		.map(part => Number.parseInt(part.trim(), 10));
	const start = parts[0]!;
	if (!Number.isInteger(start) || start < 1) {
		return;
	}

	const end = parts[1] ?? start + 1;
	return Number.isInteger(end) && end > start
		? {start: start - 1, end: end - 1}
		: undefined;
};

const intrinsicSize = (node: YogaNode, axis: 'width' | 'height'): number =>
	axis === 'width' ? node.getComputedWidth() : node.getComputedHeight();

const occupied = (cells: boolean[][], row: number, column: number): boolean =>
	cells[row]?.[column] ?? false;

const fits = (options: {
	cells: boolean[][];
	rowSpan: number;
	columnSpan: number;
	row: number;
	column: number;
}): boolean => {
	const {cells, rowSpan, columnSpan, row, column} = options;

	for (let y = row; y < row + rowSpan; y++) {
		for (let x = column; x < column + columnSpan; x++) {
			if (occupied(cells, y, x)) {
				return false;
			}
		}
	}

	return true;
};

const occupy = (
	cells: boolean[][],
	placement: {row: number; column: number; rowEnd: number; columnEnd: number},
): void => {
	for (let y = placement.row; y < placement.rowEnd; y++) {
		let row = cells[y];
		if (!row) {
			row = [];
			cells[y] = row;
		}

		for (let x = placement.column; x < placement.columnEnd; x++) {
			row[x] = true;
		}
	}
};

const getSizes = (options: SizeOptions): number[] => {
	const {tracks, children, axis, available, gap} = options;
	const sizes = tracks.map(track => track.minimum);

	for (const child of children) {
		const spanStart = axis === 'width' ? child.column : child.row;
		const spanEnd = axis === 'width' ? child.columnEnd : child.rowEnd;
		if (spanEnd - spanStart !== 1) {
			continue;
		}

		const node = child.node.yogaNode;
		if (node) {
			sizes[spanStart] = Math.max(
				sizes[spanStart] ?? 0,
				tracks[spanStart]!.auto
					? Math.max(1, intrinsicSize(node, axis))
					: intrinsicSize(node, axis),
			);
		}
	}

	const freeSpace = Math.max(
		0,
		available - gap * Math.max(0, tracks.length - 1) - sum(sizes),
	);
	let flexible = 0;
	for (const track of tracks) {
		flexible += track.fraction ?? 0;
	}

	if (flexible > 0) {
		for (const [index, track] of tracks.entries()) {
			if (track.fraction) {
				sizes[index] = Math.max(
					sizes[index]!,
					sizes[index]! + (freeSpace * track.fraction) / flexible,
				);
			}
		}
	}

	for (const [index, {maximum}] of tracks.entries()) {
		if (maximum !== undefined) {
			sizes[index] = Math.min(sizes[index]!, maximum);
		}
	}

	return sizes;
};

const getTrackOffsets = (sizes: number[], gap: number): number[] =>
	sizes.map((_, index) => sum(sizes.slice(0, index)) + gap * index);

const getSpanSize = (
	sizes: number[],
	start: number,
	end: number,
	gap: number,
): number => sum(sizes.slice(start, end)) + gap * Math.max(0, end - start - 1);

const layoutGrid = (node: DOMElement): void => {
	if (node.style.display !== 'grid' || !node.yogaNode) {
		return;
	}

	const columns = parseTracks(node.style.gridTemplateColumns);
	if (columns.length === 0) {
		return;
	}

	const rows = parseTracks(node.style.gridTemplateRows);
	const children = node.childNodes.filter((child): child is DOMElement =>
		Boolean(child.yogaNode),
	);
	const placements: GridChildPlacement[] = [];
	const cells: boolean[][] = [];
	let cursor = 0;

	for (const child of children) {
		const columnPlacement = parsePlacement(child.style.gridColumn);
		const rowPlacement = parsePlacement(child.style.gridRow);
		let column = columnPlacement?.start;
		let columnEnd = columnPlacement?.end;
		let row = rowPlacement?.start;
		let rowEnd = rowPlacement?.end;

		if (row === undefined && column === undefined) {
			while (true) {
				const autoRow = Math.floor(cursor / columns.length);
				const autoColumn = cursor % columns.length;
				const autoColumnEnd = columnEnd ?? autoColumn + 1;
				const autoRowEnd = rowEnd ?? autoRow + 1;

				if (
					fits({
						cells,
						rowSpan: autoRowEnd - autoRow,
						columnSpan: autoColumnEnd - autoColumn,
						row: autoRow,
						column: autoColumn,
					})
				) {
					row = autoRow;
					column = autoColumn;
					columnEnd = autoColumnEnd;
					rowEnd = autoRowEnd;
					cursor++;
					break;
				}

				cursor++;
			}
		} else {
			column ??= 0;
			columnEnd ??= column + 1;
			row ??= 0;
			rowEnd ??= row + 1;
		}

		const placement = {node: child, row, column, rowEnd, columnEnd};
		placements.push(placement);
		occupy(cells, placement);
	}

	const rowCount = Math.max(
		rows.length,
		...placements.map(placement => placement.rowEnd),
		0,
	);
	while (rows.length < rowCount) {
		rows.push({minimum: 0, auto: true});
	}

	const horizontalPadding =
		node.yogaNode.getComputedPadding(Yoga.EDGE_LEFT) +
		node.yogaNode.getComputedPadding(Yoga.EDGE_RIGHT) +
		node.yogaNode.getComputedBorder(Yoga.EDGE_LEFT) +
		node.yogaNode.getComputedBorder(Yoga.EDGE_RIGHT);
	const verticalPadding =
		node.yogaNode.getComputedPadding(Yoga.EDGE_TOP) +
		node.yogaNode.getComputedPadding(Yoga.EDGE_BOTTOM) +
		node.yogaNode.getComputedBorder(Yoga.EDGE_TOP) +
		node.yogaNode.getComputedBorder(Yoga.EDGE_BOTTOM);
	const columnGap = node.style.columnGap ?? node.style.gap ?? 0;
	const rowGap = node.style.rowGap ?? node.style.gap ?? 0;
	const columnSizes = getSizes({
		tracks: columns,
		children: placements,
		axis: 'width',
		available: node.yogaNode.getComputedWidth() - horizontalPadding,
		gap: columnGap,
	});
	let rowSizes = getSizes({
		tracks: rows,
		children: placements,
		axis: 'height',
		available: node.yogaNode.getComputedHeight() - verticalPadding,
		gap: rowGap,
	});

	if (node.style.height === undefined) {
		rowSizes = getSizes({
			tracks: rows,
			children: placements,
			axis: 'height',
			available: Number.POSITIVE_INFINITY,
			gap: rowGap,
		});
		const height =
			sum(rowSizes) +
			rowGap * Math.max(0, rowSizes.length - 1) +
			verticalPadding;
		node.yogaNode.setHeight(height);
	}

	const columnOffsets = getTrackOffsets(columnSizes, columnGap);
	const rowOffsets = getTrackOffsets(rowSizes, rowGap);

	for (const placement of placements) {
		const width = getSpanSize(
			columnSizes,
			placement.column,
			placement.columnEnd,
			columnGap,
		);
		const height = getSpanSize(
			rowSizes,
			placement.row,
			placement.rowEnd,
			rowGap,
		);
		const childNode = placement.node.yogaNode!;
		const columnOffset = columnOffsets[placement.column] ?? 0;
		const rowOffset = rowOffsets[placement.row] ?? 0;
		childNode.setPositionType(Yoga.POSITION_TYPE_ABSOLUTE);
		childNode.setWidth(width);
		childNode.setHeight(height);
		childNode.calculateLayout(width, height, Yoga.DIRECTION_LTR);
		childNode.setPosition(Yoga.EDGE_LEFT, columnOffset);
		childNode.setPosition(Yoga.EDGE_TOP, rowOffset);
		placement.node.internal_grid = {
			x: columnOffset,
			y: rowOffset,
			width,
			height,
		};
	}

	node.yogaNode.calculateLayout(
		node.yogaNode.getComputedWidth(),
		node.yogaNode.getComputedHeight(),
		Yoga.DIRECTION_LTR,
	);
	for (const placement of placements) {
		layoutGrid(placement.node);
	}

	const gridHeight =
		sum(rowSizes) + rowGap * Math.max(0, rowSizes.length - 1) + verticalPadding;
	if (node.style.height === undefined) {
		node.yogaNode.setHeight(gridHeight);
	}

	node.internal_grid = {
		x: node.internal_grid?.x ?? 0,
		y: node.internal_grid?.y ?? 0,
		width: node.yogaNode.getComputedWidth(),
		height:
			node.style.height === undefined
				? gridHeight
				: node.yogaNode.getComputedHeight(),
	};
};

export const applyGridLayouts = (root: DOMElement): void => {
	const visit = (node: DOMElement): void => {
		layoutGrid(node);
		for (const child of node.childNodes) {
			if (child.nodeName !== '#text') {
				visit(child);
			}
		}
	};

	visit(root);
	if (root.yogaNode) {
		root.yogaNode.calculateLayout(
			root.yogaNode.getComputedWidth(),
			root.yogaNode.getComputedHeight(),
			Yoga.DIRECTION_LTR,
		);
		let height = root.yogaNode.getComputedHeight();
		for (const child of root.childNodes) {
			if (child.nodeName === '#text') {
				continue;
			}

			const grid = child.internal_grid;
			height = Math.max(height, (grid?.y ?? 0) + (grid?.height ?? 0));
		}

		root.internal_grid = {
			x: 0,
			y: 0,
			width: root.yogaNode.getComputedWidth(),
			height,
		};
	}
};
