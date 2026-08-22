import Yoga, {type Node as YogaNode} from 'yoga-layout';
import {type DOMElement} from './dom.js';

type Track = {
	minimum: number;
	maximum?: number;
	fraction?: number;
	auto: boolean;
};

type Placement = {
	node: DOMElement;
	row: number;
	column: number;
	rowEnd: number;
	columnEnd: number;
};

const numberPattern = '(?:\\d+(?:\\.\\d*)?|\\.\\d+)';
const minmaxPattern = new RegExp(
	`^minmax\\(\\s*(${numberPattern})\\s*,\\s*(${numberPattern})(fr)?\\s*\\)$`,
	'i',
);

const parseTrack = (value: string): Track | undefined => {
	const normalized = value.toLowerCase();
	const minmax = minmaxPattern.exec(value);

	if (minmax) {
		const minimum = Number(minmax[1]);
		return minmax[3]
			? {
					minimum,
					fraction: Number(minmax[2]),
					auto: false,
				}
			: {
					minimum,
					maximum: Number(minmax[2]),
					auto: false,
				};
	}

	if (normalized === 'auto') {
		return {minimum: 0, auto: true};
	}

	if (normalized.endsWith('fr')) {
		const fraction = Number.parseFloat(normalized);
		return fraction > 0 ? {minimum: 0, fraction, auto: false} : undefined;
	}

	const size = Number(value);
	return Number.isFinite(size) && size >= 0
		? {minimum: size, maximum: size, auto: false}
		: undefined;
};

const parseTracks = (value: string | undefined): Track[] => {
	if (!value) {
		return [];
	}

	const values = value.trim().match(/minmax\([^)]*\)|\S+/gi) ?? [];
	return values
		.map(value => parseTrack(value))
		.filter((track): track is Track => track !== undefined);
};

const parsePlacement = (
	value: number | string | undefined,
): {start: number; end: number} | undefined => {
	if (value === undefined) {
		return;
	}

	const parts = String(value)
		.trim()
		.split('/')
		.map(part => Number(part.trim()));

	if (parts.length > 2 || !Number.isInteger(parts[0]) || parts[0]! < 1) {
		return;
	}

	const start = parts[0]! - 1;
	const end = parts[1] === undefined ? start + 1 : parts[1] - 1;

	return Number.isInteger(end) && end > start ? {start, end} : undefined;
};

const isHidden = (node: DOMElement): boolean =>
	node.style.display === 'none' ||
	node.yogaNode?.getDisplay() === Yoga.DISPLAY_NONE;

const isOccupied = (cells: boolean[][], row: number, column: number): boolean =>
	cells[row]?.[column] ?? false;

const fits = (
	cells: boolean[][],
	row: number,
	column: number,
	rowSpan: number,
	columnSpan: number,
): boolean => {
	for (let currentRow = row; currentRow < row + rowSpan; currentRow++) {
		for (
			let currentColumn = column;
			currentColumn < column + columnSpan;
			currentColumn++
		) {
			if (isOccupied(cells, currentRow, currentColumn)) {
				return false;
			}
		}
	}

	return true;
};

const occupy = (cells: boolean[][], placement: Placement): void => {
	const {row, rowEnd, column, columnEnd} = placement;
	for (let currentRow = row; currentRow < rowEnd; currentRow++) {
		cells[currentRow] ??= [];

		for (
			let currentColumn = column;
			currentColumn < columnEnd;
			currentColumn++
		) {
			cells[currentRow]![currentColumn] = true;
		}
	}
};

const placeChild = (
	child: DOMElement,
	row: number,
	column: number,
	rowEnd: number,
	columnEnd: number,
	cells: boolean[][],
): Placement => {
	const placement = {node: child, row, column, rowEnd, columnEnd};
	occupy(cells, placement);
	return placement;
};

const findPlacement = (
	cells: boolean[][],
	row: number | undefined,
	column: number | undefined,
	rowSpan: number,
	columnSpan: number,
	columnCount: number,
): {row: number; column: number} => {
	const firstRow = row ?? 0;
	const firstColumn = column ?? 0;
	const lastRow = row === undefined ? Number.POSITIVE_INFINITY : row + 1;

	for (let currentRow = firstRow; currentRow < lastRow; currentRow++) {
		const columnStart = row === undefined ? firstColumn : 0;
		const columnLimit = column === undefined ? columnCount : column + 1;

		for (
			let currentColumn = columnStart;
			currentColumn < columnLimit;
			currentColumn++
		) {
			if (fits(cells, currentRow, currentColumn, rowSpan, columnSpan)) {
				return {row: currentRow, column: currentColumn};
			}
		}
	}

	return {
		row: firstRow,
		column: firstColumn,
	};
};

const intrinsicSize = (node: YogaNode, axis: 'width' | 'height'): number =>
	axis === 'width' ? node.getComputedWidth() : node.getComputedHeight();

const getTrackSizes = (
	tracks: Track[],
	placements: Placement[],
	axis: 'width' | 'height',
	available: number,
	gap: number,
): number[] => {
	const sizes = tracks.map(track => track.minimum);

	for (const placement of placements) {
		const start = axis === 'width' ? placement.column : placement.row;
		const end = axis === 'width' ? placement.columnEnd : placement.rowEnd;

		if (!placement.node.yogaNode) {
			continue;
		}

		const intrinsic = intrinsicSize(placement.node.yogaNode, axis);
		const span = end - start;
		if (span === 1 && tracks[start]?.auto) {
			sizes[start] = Math.max(sizes[start] ?? 0, intrinsic);
		}

		if (span > 1) {
			const currentSize =
				sizes.slice(start, end).reduce((sum, size) => sum + size, 0) +
				gap * (span - 1);
			const autoTracks = tracks
				.slice(start, end)
				.map((track, index) => (track.auto ? start + index : -1))
				.filter(index => index >= 0);
			const extraSize = Math.max(0, intrinsic - currentSize);

			for (const index of autoTracks) {
				sizes[index]! += extraSize / autoTracks.length;
			}
		}
	}

	const gapSize = gap * Math.max(0, tracks.length - 1);
	let freeSpace = Number.isFinite(available)
		? Math.max(
				0,
				available - gapSize - sizes.reduce((sum, size) => sum + size, 0),
			)
		: Number.POSITIVE_INFINITY;

	// A fixed maximum in minmax() can grow up to that maximum before flexible
	// maxima receive the remaining space.
	for (const [index, track] of tracks.entries()) {
		if (freeSpace === 0 || track.maximum === undefined) {
			continue;
		}

		const growth = Math.min(
			freeSpace,
			Math.max(0, track.maximum - sizes[index]!),
		);
		sizes[index]! += growth;
		freeSpace -= growth;
	}

	const totalFraction = tracks.reduce(
		(sum, track) => sum + (track.fraction ?? 0),
		0,
	);

	if (totalFraction > 0 && Number.isFinite(freeSpace) && freeSpace > 0) {
		for (const [index, track] of tracks.entries()) {
			if (track.fraction) {
				sizes[index]! += (freeSpace * track.fraction) / totalFraction;
			}
		}
	}

	const totalSize = sizes.reduce((sum, size) => sum + size, 0);
	const roundedSizes = sizes.map(size => Math.floor(size));
	let remaining =
		Math.round(totalSize) - roundedSizes.reduce((sum, size) => sum + size, 0);

	const order = sizes
		.map((size, index) => ({index, fraction: size - Math.floor(size)}))
		.sort((a, b) => b.fraction - a.fraction);

	for (const {index} of order) {
		if (remaining <= 0) {
			break;
		}

		roundedSizes[index]!++;
		remaining--;
	}

	return roundedSizes;
};

const getGridChildren = (node: DOMElement): DOMElement[] =>
	node.childNodes.filter(
		(child): child is DOMElement =>
			child.nodeName !== '#text' && Boolean(child.yogaNode) && !isHidden(child),
	);

const getContentOffset = (node: DOMElement, edge: number): number =>
	(node.yogaNode?.getComputedPadding(edge) ?? 0) +
	(node.yogaNode?.getComputedBorder(edge) ?? 0);

const layoutGrid = (node: DOMElement): void => {
	if (node.style.display !== 'grid' || !node.yogaNode) {
		return;
	}

	const columns = parseTracks(node.style.gridTemplateColumns);
	if (columns.length === 0) {
		return;
	}

	const rows = parseTracks(node.style.gridTemplateRows);
	const children = getGridChildren(node);
	const cells: boolean[][] = [];
	const placements: Placement[] = [];
	const pending = children.map(child => ({
		child,
		column: parsePlacement(child.style.gridColumn),
		row: parsePlacement(child.style.gridRow),
	}));

	const place = (
		item: (typeof pending)[number],
		cursor: {row: number; column: number},
	): void => {
		const rowSpan = item.row ? item.row.end - item.row.start : 1;
		const columnSpan = item.column ? item.column.end - item.column.start : 1;
		const placement =
			item.row && item.column
				? {
						row: item.row.start,
						column: item.column.start,
					}
				: findPlacement(
						cells,
						item.row?.start,
						item.column?.start,
						rowSpan,
						columnSpan,
						columns.length,
					);

		const result = placeChild(
			item.child,
			placement.row,
			placement.column,
			placement.row + rowSpan,
			placement.column + columnSpan,
			cells,
		);
		placements.push(result);

		if (!item.row && !item.column) {
			cursor.row = result.row;
			cursor.column = result.column + columnSpan;
			if (cursor.column >= columns.length) {
				cursor.row++;
				cursor.column = 0;
			}
		}
	};

	const cursor = {row: 0, column: 0};
	for (const item of pending.filter(item => item.row && item.column)) {
		place(item, cursor);
	}

	for (const item of pending.filter(item => !(item.row && item.column))) {
		place(item, cursor);
	}

	const rowCount = Math.max(
		rows.length,
		...placements.map(placement => placement.rowEnd),
		0,
	);
	while (rows.length < rowCount) {
		rows.push({minimum: 0, auto: true});
	}

	const width = node.yogaNode.getComputedWidth();
	const height = node.yogaNode.getComputedHeight();
	const horizontalPadding =
		getContentOffset(node, Yoga.EDGE_LEFT) +
		getContentOffset(node, Yoga.EDGE_RIGHT);
	const verticalPadding =
		getContentOffset(node, Yoga.EDGE_TOP) +
		getContentOffset(node, Yoga.EDGE_BOTTOM);
	const columnGap = node.style.columnGap ?? node.style.gap ?? 0;
	const rowGap = node.style.rowGap ?? node.style.gap ?? 0;
	const contentWidth = Math.max(0, width - horizontalPadding);
	const columnSizes = getTrackSizes(
		columns,
		placements,
		'width',
		contentWidth,
		columnGap,
	);
	const columnOffsets = columnSizes.map(
		(_, index) =>
			getContentOffset(node, Yoga.EDGE_LEFT) +
			columnSizes.slice(0, index).reduce((sum, size) => sum + size, 0) +
			columnGap * index,
	);

	// Measure children after their grid columns are known so wrapped text
	// contributes its actual height to auto rows.
	for (const placement of placements) {
		const childWidth =
			columnSizes
				.slice(placement.column, placement.columnEnd)
				.reduce((sum, size) => sum + size, 0) +
			columnGap * Math.max(0, placement.columnEnd - placement.column - 1);
		placement.node.yogaNode!.setPositionType(Yoga.POSITION_TYPE_ABSOLUTE);
		placement.node.yogaNode!.setWidth(childWidth);
		placement.node.yogaNode!.calculateLayout(
			childWidth,
			undefined,
			Yoga.DIRECTION_LTR,
		);
	}

	const rowSizes = getTrackSizes(
		rows,
		placements,
		'height',
		node.style.height === undefined
			? Number.POSITIVE_INFINITY
			: Math.max(0, height - verticalPadding),
		rowGap,
	);
	const rowHeight =
		rowSizes.reduce((sum, size) => sum + size, 0) +
		rowGap * Math.max(0, rowSizes.length - 1) +
		verticalPadding;

	if (node.style.height === undefined) {
		node.yogaNode.setHeight(rowHeight);
	}

	const rowOffsets = rowSizes.map(
		(_, index) =>
			getContentOffset(node, Yoga.EDGE_TOP) +
			rowSizes.slice(0, index).reduce((sum, size) => sum + size, 0) +
			rowGap * index,
	);

	for (const placement of placements) {
		const childWidth =
			columnSizes
				.slice(placement.column, placement.columnEnd)
				.reduce((sum, size) => sum + size, 0) +
			columnGap * Math.max(0, placement.columnEnd - placement.column - 1);
		const childHeight =
			rowSizes
				.slice(placement.row, placement.rowEnd)
				.reduce((sum, size) => sum + size, 0) +
			rowGap * Math.max(0, placement.rowEnd - placement.row - 1);
		const childNode = placement.node.yogaNode!;
		childNode.setWidth(childWidth);
		childNode.setHeight(childHeight);
		childNode.setPosition(Yoga.EDGE_LEFT, columnOffsets[placement.column]);
		childNode.setPosition(Yoga.EDGE_TOP, rowOffsets[placement.row]);
		childNode.calculateLayout(childWidth, childHeight, Yoga.DIRECTION_LTR);
		placement.node.internal_grid = {
			x: columnOffsets[placement.column]!,
			y: rowOffsets[placement.row]!,
			width: childWidth,
			height: childHeight,
		};
	}

	node.yogaNode.calculateLayout(
		width,
		node.yogaNode.getComputedHeight(),
		Yoga.DIRECTION_LTR,
	);
	for (const placement of placements) {
		layoutTree(placement.node);
	}

	node.internal_grid = {
		x: node.internal_grid?.x ?? node.yogaNode.getComputedLeft(),
		y: node.internal_grid?.y ?? node.yogaNode.getComputedTop(),
		width,
		height:
			node.style.height === undefined
				? rowHeight
				: node.yogaNode.getComputedHeight(),
	};
};

const clearGridLayouts = (node: DOMElement): void => {
	node.internal_grid = undefined;
	for (const child of node.childNodes) {
		if (child.nodeName !== '#text') {
			clearGridLayouts(child);
		}
	}
};

const layoutTree = (node: DOMElement): void => {
	if (node.style.display === 'grid') {
		layoutGrid(node);
		return;
	}

	for (const child of node.childNodes) {
		if (child.nodeName !== '#text') {
			layoutTree(child);
		}
	}
};

export const applyGridLayouts = (root: DOMElement): void => {
	clearGridLayouts(root);
	layoutTree(root);

	if (!root.yogaNode) {
		return;
	}

	const width = root.yogaNode.getComputedWidth();
	const height = Math.max(
		root.yogaNode.getComputedHeight(),
		...root.childNodes
			.filter((child): child is DOMElement => child.nodeName !== '#text')
			.map(child => {
				const grid = child.internal_grid;
				return grid
					? grid.y + grid.height
					: child.yogaNode!.getComputedTop() +
							child.yogaNode!.getComputedHeight();
			}),
		0,
	);

	root.internal_grid = {x: 0, y: 0, width, height};
};
