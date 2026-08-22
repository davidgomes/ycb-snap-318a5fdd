import Yoga, {type Node as YogaNode} from 'yoga-layout';
import {type DOMElement, type DOMNode} from './dom.js';

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

const parseTrack = (value: string): Track => {
	const minmax = /^minmax\(\s*(\d+(?:\.\d+)?)\s*,\s*(\d+(?:\.\d+)?|[\d.]+fr)\s*\)$/i.exec(value);
	if (minmax) {
		const maximum = minmax[2].toLowerCase();
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

const parseTracks = (value: string | undefined): Track[] =>
	value?.trim().split(/\s+/).filter(Boolean).map(parseTrack) ?? [];

const parsePlacement = (
	value: number | string | undefined,
): Placement | undefined => {
	if (value === undefined) {
		return;
	}

	const parts = String(value)
		.split('/')
		.map(part => Number.parseInt(part.trim(), 10));
	const start = parts[0];
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

const fits = (
	cells: boolean[][],
	rowSpan: number,
	columnSpan: number,
	row: number,
	column: number,
): boolean => {
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
		cells[y] ??= [];
		for (let x = placement.column; x < placement.columnEnd; x++) {
			cells[y][x] = true;
		}
	}
};

const getSizes = (
	tracks: Track[],
	children: Array<{node: DOMNode; row: number; column: number; rowEnd: number; columnEnd: number}>,
	axis: 'width' | 'height',
	available: number,
	gap: number,
): number[] => {
	const sizes = tracks.map(track => track.minimum);
	for (const child of children) {
		const spanStart = axis === 'width' ? child.column : child.row;
		const spanEnd = axis === 'width' ? child.columnEnd : child.rowEnd;
		if (spanEnd - spanStart !== 1) {
			continue;
		}

		const node = child.node.yogaNode;
		if (node) {
			sizes[spanStart] = Math.max(sizes[spanStart] ?? 0, intrinsicSize(node, axis));
		}
	}

	const freeSpace = Math.max(0, available - gap * Math.max(0, tracks.length - 1) - sizes.reduce((sum, size) => sum + size, 0));
	const flexible = tracks.reduce((sum, track) => sum + (track.fraction ?? 0), 0);
	if (flexible > 0) {
		for (let index = 0; index < tracks.length; index++) {
			const track = tracks[index]!;
			if (track.fraction) {
				sizes[index] = Math.max(sizes[index]!, sizes[index]! + freeSpace * track.fraction / flexible);
			}
		}
	}

	for (let index = 0; index < tracks.length; index++) {
		const maximum = tracks[index]!.maximum;
		if (maximum !== undefined) {
			sizes[index] = Math.min(sizes[index]!, maximum);
		}
	}

	return sizes;
};

const layoutGrid = (node: DOMElement): void => {
	if (node.style.display !== 'grid' || !node.yogaNode) {
		return;
	}

	const columns = parseTracks(node.style.gridTemplateColumns);
	if (columns.length === 0) {
		return;
	}

	const rows = parseTracks(node.style.gridTemplateRows);
	const children = node.childNodes.filter(
		(child): child is DOMElement => Boolean(child.yogaNode),
	);
	const placements: Array<{node: DOMElement; row: number; column: number; rowEnd: number; columnEnd: number}> = [];
	const cells: boolean[][] = [];
	let cursor = 0;

	for (const child of children) {
		const columnPlacement = parsePlacement(child.style.gridColumn);
		const rowPlacement = parsePlacement(child.style.gridRow);
		const column = columnPlacement?.start ?? 0;
		const columnEnd = columnPlacement?.end ?? column + 1;
		let row = rowPlacement?.start;
		const rowEnd = rowPlacement?.end ?? (row ?? 0) + 1;

		if (row === undefined) {
			while (
				!fits(
					cells,
					rowEnd - (row ?? 0),
					columnEnd - column,
					Math.floor(cursor / columns.length),
					cursor % columns.length,
				)
			) {
				cursor++;
			}
			row = Math.floor(cursor / columns.length);
			cursor++;
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
	const columnSizes = getSizes(
		columns,
		placements,
		'width',
		node.yogaNode.getComputedWidth() - horizontalPadding,
		columnGap,
	);
	let rowSizes = getSizes(
		rows,
		placements,
		'height',
		node.yogaNode.getComputedHeight() - verticalPadding,
		rowGap,
	);

	if (node.style.height === undefined) {
		rowSizes = getSizes(rows, placements, 'height', Number.POSITIVE_INFINITY, rowGap);
		const height = rowSizes.reduce((sum, size) => sum + size, 0) +
			rowGap * Math.max(0, rowSizes.length - 1) + verticalPadding;
		node.yogaNode.setHeight(height);
	}

	const columnOffsets = columnSizes.map((_, index) =>
		columnSizes.slice(0, index).reduce((sum, size) => sum + size, 0) + columnGap * index,
	);
	const rowOffsets = rowSizes.map((_, index) =>
		rowSizes.slice(0, index).reduce((sum, size) => sum + size, 0) + rowGap * index,
	);

	for (const placement of placements) {
		const width = columnSizes.slice(placement.column, placement.columnEnd)
			.reduce((sum, size) => sum + size, 0) + columnGap * Math.max(0, placement.columnEnd - placement.column - 1);
		const height = rowSizes.slice(placement.row, placement.rowEnd)
			.reduce((sum, size) => sum + size, 0) + rowGap * Math.max(0, placement.rowEnd - placement.row - 1);
		const childNode = placement.node.yogaNode!;
		childNode.setPosition(Yoga.EDGE_LEFT, columnOffsets[placement.column]!);
		childNode.setPosition(Yoga.EDGE_TOP, rowOffsets[placement.row]!);
		childNode.setWidth(width);
		childNode.setHeight(height);
		childNode.calculateLayout(width, height, Yoga.DIRECTION_LTR);
		layoutGrid(placement.node);
	}
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
};
