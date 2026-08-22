import Yoga from 'yoga-layout';
import {type DOMElement, type DOMNode} from './dom.js';
import {type Styles} from './styles.js';

type FixedTrack = {
	readonly type: 'fixed';
	readonly value: number;
};

type FrTrack = {
	readonly type: 'fr';
	readonly value: number;
};

type AutoTrack = {
	readonly type: 'auto';
};

type MinmaxTrack = {
	readonly type: 'minmax';
	readonly min: number;
	readonly max: FixedTrack | FrTrack;
};

export type Track = FixedTrack | FrTrack | AutoTrack | MinmaxTrack;

type LineRange = {
	start: number;
	end: number;
};

type GridItem = {
	node: DOMElement;
	column: LineRange;
	row: LineRange;
	intrinsicWidth: number;
	intrinsicHeight: number;
};

const trackListPattern =
	/minmax\(\s*(\d+(?:\.\d+)?)\s*,\s*(\d+(?:\.\d+)?fr|\d+(?:\.\d+)?)\s*\)|(\d+(?:\.\d+)?fr)|(\d+(?:\.\d+)?)|(auto)/gi;

export const parseTrackList = (value: string | undefined): Track[] => {
	if (!value) {
		return [];
	}

	const tracks: Track[] = [];

	for (const match of value.matchAll(trackListPattern)) {
		if (match[1] !== undefined && match[2] !== undefined) {
			const min = Number(match[1]);
			const maxRaw = match[2];

			tracks.push({
				type: 'minmax',
				min,
				max: maxRaw.endsWith('fr')
					? {type: 'fr', value: Number.parseFloat(maxRaw)}
					: {type: 'fixed', value: Number(maxRaw)},
			});
			continue;
		}

		if (match[3] !== undefined) {
			tracks.push({type: 'fr', value: Number.parseFloat(match[3])});
			continue;
		}

		if (match[4] !== undefined) {
			tracks.push({type: 'fixed', value: Number(match[4])});
			continue;
		}

		if (match[5] !== undefined) {
			tracks.push({type: 'auto'});
		}
	}

	return tracks;
};

export const parseGridLine = (
	value: number | string | undefined,
): LineRange | undefined => {
	if (value === undefined) {
		return undefined;
	}

	if (typeof value === 'number' && Number.isFinite(value)) {
		const start = Math.max(1, Math.trunc(value));
		return {start, end: start + 1};
	}

	const parts = String(value)
		.split('/')
		.map(part => part.trim())
		.filter(Boolean);

	if (parts.length === 0) {
		return undefined;
	}

	const start = Number(parts[0]);
	if (!Number.isFinite(start)) {
		return undefined;
	}

	const normalizedStart = Math.max(1, Math.trunc(start));

	if (parts.length === 1) {
		return {start: normalizedStart, end: normalizedStart + 1};
	}

	const end = Number(parts[1]);
	if (!Number.isFinite(end)) {
		return {start: normalizedStart, end: normalizedStart + 1};
	}

	const normalizedEnd = Math.trunc(end);
	if (normalizedEnd <= normalizedStart) {
		return {start: normalizedStart, end: normalizedStart + 1};
	}

	return {start: normalizedStart, end: normalizedEnd};
};

const isVisibleGridItem = (node: DOMNode): node is DOMElement => {
	if (!node.yogaNode || node.style.display === 'none') {
		return false;
	}

	return node.yogaNode.getDisplay() !== Yoga.DISPLAY_NONE;
};

const getGaps = (style: Styles): {column: number; row: number} => ({
	column: style.columnGap ?? style.gap ?? 0,
	row: style.rowGap ?? style.gap ?? 0,
});

const sum = (values: number[]): number =>
	values.reduce((total, value) => total + value, 0);

const hasExplicitDimension = (
	style: Styles,
	property: 'width' | 'height',
): boolean => property in style && style[property] !== undefined;

const resetPositionType = (node: DOMElement): void => {
	const yogaNode = node.yogaNode;
	if (!yogaNode) {
		return;
	}

	if (node.style.position === 'absolute') {
		yogaNode.setPositionType(Yoga.POSITION_TYPE_ABSOLUTE);
	} else if (node.style.position === 'static') {
		yogaNode.setPositionType(Yoga.POSITION_TYPE_STATIC);
	} else {
		yogaNode.setPositionType(Yoga.POSITION_TYPE_RELATIVE);
	}

	yogaNode.setPositionAuto(Yoga.EDGE_LEFT);
	yogaNode.setPositionAuto(Yoga.EDGE_TOP);
	yogaNode.setPositionAuto(Yoga.EDGE_RIGHT);
	yogaNode.setPositionAuto(Yoga.EDGE_BOTTOM);
};

const resetItemDimensions = (node: DOMElement): void => {
	const yogaNode = node.yogaNode;
	if (!yogaNode) {
		return;
	}

	node.gridItemLayout = undefined;

	if (typeof node.style.width === 'number') {
		yogaNode.setWidth(node.style.width);
	} else if (typeof node.style.width === 'string') {
		yogaNode.setWidthPercent(Number.parseInt(node.style.width, 10));
	} else {
		yogaNode.setWidthAuto();
	}

	if (typeof node.style.height === 'number') {
		yogaNode.setHeight(node.style.height);
	} else if (typeof node.style.height === 'string') {
		yogaNode.setHeightPercent(Number.parseInt(node.style.height, 10));
	} else {
		yogaNode.setHeightAuto();
	}

	resetPositionType(node);
};

const resetContainerDimensions = (node: DOMElement): void => {
	const yogaNode = node.yogaNode;
	if (!yogaNode) {
		return;
	}

	if (typeof node.style.width === 'number') {
		yogaNode.setWidth(node.style.width);
	} else if (typeof node.style.width === 'string') {
		yogaNode.setWidthPercent(Number.parseInt(node.style.width, 10));
	} else {
		yogaNode.setWidthAuto();
	}

	if (typeof node.style.height === 'number') {
		yogaNode.setHeight(node.style.height);
	} else if (typeof node.style.height === 'string') {
		yogaNode.setHeightPercent(Number.parseInt(node.style.height, 10));
	} else {
		yogaNode.setHeightAuto();
	}

	if (typeof node.style.minWidth === 'number') {
		yogaNode.setMinWidth(node.style.minWidth);
	} else if (typeof node.style.minWidth === 'string') {
		yogaNode.setMinWidthPercent(Number.parseInt(node.style.minWidth, 10));
	} else {
		yogaNode.setMinWidth(undefined);
	}

	if (typeof node.style.minHeight === 'number') {
		yogaNode.setMinHeight(node.style.minHeight);
	} else if (typeof node.style.minHeight === 'string') {
		yogaNode.setMinHeightPercent(Number.parseInt(node.style.minHeight, 10));
	} else {
		yogaNode.setMinHeight(undefined);
	}
};

const measureNode = (node: DOMElement): {width: number; height: number} => {
	const yogaNode = node.yogaNode;
	if (!yogaNode) {
		return {width: 0, height: 0};
	}

	yogaNode.calculateLayout(undefined, undefined, Yoga.DIRECTION_LTR);

	return {
		width: Math.max(0, Math.round(yogaNode.getComputedWidth())),
		height: Math.max(0, Math.round(yogaNode.getComputedHeight())),
	};
};

const boxEdges = (
	node: DOMElement,
	axis: 'horizontal' | 'vertical',
): number => {
	const yogaNode = node.yogaNode;
	if (!yogaNode) {
		return 0;
	}

	if (axis === 'horizontal') {
		return (
			yogaNode.getComputedPadding(Yoga.EDGE_LEFT) +
			yogaNode.getComputedPadding(Yoga.EDGE_RIGHT) +
			yogaNode.getComputedBorder(Yoga.EDGE_LEFT) +
			yogaNode.getComputedBorder(Yoga.EDGE_RIGHT)
		);
	}

	return (
		yogaNode.getComputedPadding(Yoga.EDGE_TOP) +
		yogaNode.getComputedPadding(Yoga.EDGE_BOTTOM) +
		yogaNode.getComputedBorder(Yoga.EDGE_TOP) +
		yogaNode.getComputedBorder(Yoga.EDGE_BOTTOM)
	);
};

const styleBoxEdges = (
	style: Styles,
	axis: 'horizontal' | 'vertical',
): number => {
	const border =
		style.borderStyle === undefined
			? 0
			: axis === 'horizontal'
				? (style.borderLeft === false ? 0 : 1) +
					(style.borderRight === false ? 0 : 1)
				: (style.borderTop === false ? 0 : 1) +
					(style.borderBottom === false ? 0 : 1);

	if (axis === 'horizontal') {
		return (
			(style.paddingLeft ?? style.paddingX ?? style.padding ?? 0) +
			(style.paddingRight ?? style.paddingX ?? style.padding ?? 0) +
			border
		);
	}

	return (
		(style.paddingTop ?? style.paddingY ?? style.padding ?? 0) +
		(style.paddingBottom ?? style.paddingY ?? style.padding ?? 0) +
		border
	);
};

const occupy = (
	occupied: Set<string>,
	rowStart: number,
	rowEnd: number,
	columnStart: number,
	columnEnd: number,
): void => {
	for (let row = rowStart; row < rowEnd; row++) {
		for (let column = columnStart; column < columnEnd; column++) {
			occupied.add(`${row},${column}`);
		}
	}
};

const isOccupied = (
	occupied: Set<string>,
	rowStart: number,
	rowEnd: number,
	columnStart: number,
	columnEnd: number,
): boolean => {
	for (let row = rowStart; row < rowEnd; row++) {
		for (let column = columnStart; column < columnEnd; column++) {
			if (occupied.has(`${row},${column}`)) {
				return true;
			}
		}
	}

	return false;
};

const findOpenCell = (
	occupied: Set<string>,
	columnCount: number,
	columnSpan: number,
	rowSpan: number,
	startRow: number,
	startColumn: number,
	fixedColumn: boolean,
): {rowStart: number; columnStart: number} => {
	let rowStart = startRow;
	let columnStart = startColumn;

	while (true) {
		if (fixedColumn) {
			if (
				!isOccupied(
					occupied,
					rowStart,
					rowStart + rowSpan,
					columnStart,
					columnStart + columnSpan,
				)
			) {
				return {rowStart, columnStart};
			}

			rowStart++;
			continue;
		}

		if (columnStart + columnSpan > columnCount) {
			columnStart = 0;
			rowStart++;
			continue;
		}

		if (
			!isOccupied(
				occupied,
				rowStart,
				rowStart + rowSpan,
				columnStart,
				columnStart + columnSpan,
			)
		) {
			return {rowStart, columnStart};
		}

		columnStart++;
	}
};

const placeItems = (
	nodes: DOMElement[],
	explicitColumnCount: number,
	explicitRowCount: number,
): {
	items: Array<Omit<GridItem, 'intrinsicWidth' | 'intrinsicHeight'>>;
	columnCount: number;
	rowCount: number;
} => {
	const occupied = new Set<string>();
	const items: Array<Omit<GridItem, 'intrinsicWidth' | 'intrinsicHeight'>> =
		[];

	let columnCount = Math.max(1, explicitColumnCount);
	let rowCount = explicitRowCount;

	const resolved = nodes.map(node => ({
		node,
		column: parseGridLine(node.style.gridColumn),
		row: parseGridLine(node.style.gridRow),
	}));

	for (const item of resolved) {
		if (item.column) {
			columnCount = Math.max(columnCount, item.column.end - 1);
		}

		if (item.row) {
			rowCount = Math.max(rowCount, item.row.end - 1);
		}
	}

	for (const item of resolved) {
		if (item.column && item.row) {
			occupy(
				occupied,
				item.row.start - 1,
				item.row.end - 1,
				item.column.start - 1,
				item.column.end - 1,
			);
			items.push({node: item.node, column: item.column, row: item.row});
		}
	}

	for (const item of resolved) {
		if (item.column && item.row) {
			continue;
		}

		const columnSpan = item.column ? item.column.end - item.column.start : 1;
		const rowSpan = item.row ? item.row.end - item.row.start : 1;

		if (item.row && !item.column) {
			const rowStart = item.row.start - 1;
			const found = findOpenCell(
				occupied,
				columnCount,
				columnSpan,
				rowSpan,
				rowStart,
				0,
				false,
			);
			const columnStart =
				found.rowStart === rowStart ? found.columnStart : 0;
			const nextColumnCount = Math.max(
				columnCount,
				columnStart + columnSpan,
			);
			columnCount = nextColumnCount;
			const column = {
				start: columnStart + 1,
				end: columnStart + 1 + columnSpan,
			};
			occupy(
				occupied,
				rowStart,
				rowStart + rowSpan,
				column.start - 1,
				column.end - 1,
			);
			items.push({node: item.node, column, row: item.row});
			continue;
		}

		const startColumn = item.column ? item.column.start - 1 : 0;
		const startRow = item.row ? item.row.start - 1 : 0;
		const found = findOpenCell(
			occupied,
			columnCount,
			columnSpan,
			rowSpan,
			startRow,
			startColumn,
			Boolean(item.column),
		);
		const column = item.column ?? {
			start: found.columnStart + 1,
			end: found.columnStart + 1 + columnSpan,
		};
		const row = item.row ?? {
			start: found.rowStart + 1,
			end: found.rowStart + 1 + rowSpan,
		};

		occupy(
			occupied,
			row.start - 1,
			row.end - 1,
			column.start - 1,
			column.end - 1,
		);
		rowCount = Math.max(rowCount, row.end - 1);
		columnCount = Math.max(columnCount, column.end - 1);
		items.push({node: item.node, column, row});
	}

	return {items, columnCount, rowCount: Math.max(rowCount, 0)};
};

const distributeFr = (remaining: number, weights: number[]): number[] => {
	const extras = weights.map(() => 0);
	const totalWeight = sum(weights);

	if (remaining <= 0 || totalWeight <= 0) {
		return extras;
	}

	const raw = weights.map(weight => (weight / totalWeight) * remaining);
	const floors = raw.map(value => Math.floor(value));
	let leftover = remaining - sum(floors);

	const order = raw
		.map((value, index) => ({index, fraction: value - (floors[index] ?? 0)}))
		.sort((a, b) => b.fraction - a.fraction || a.index - b.index);

	for (const [index, value] of floors.entries()) {
		extras[index] = value;
	}

	for (const entry of order) {
		if (leftover <= 0) {
			break;
		}

		extras[entry.index] = (extras[entry.index] ?? 0) + 1;
		leftover--;
	}

	return extras;
};

const trackFrWeight = (track: Track): number => {
	if (track.type === 'fr') {
		return track.value;
	}

	if (track.type === 'minmax' && track.max.type === 'fr') {
		return track.max.value;
	}

	return 0;
};

const trackFixedMax = (track: Track): number | undefined => {
	if (track.type === 'fixed') {
		return track.value;
	}

	if (track.type === 'minmax' && track.max.type === 'fixed') {
		return track.max.value;
	}

	return undefined;
};

const sizeTracks = (
	tracks: Track[],
	items: GridItem[],
	axis: 'column' | 'row',
	available: number | undefined,
	gap: number,
): number[] => {
	const sizes = tracks.map(track => {
		if (track.type === 'fixed') {
			return track.value;
		}

		if (track.type === 'minmax') {
			return track.min;
		}

		return 0;
	});

	const contributions = tracks.map(() => 0);

	for (const item of items) {
		const range = axis === 'column' ? item.column : item.row;
		const start = range.start - 1;
		const end = range.end - 1;
		const itemSize =
			axis === 'column' ? item.intrinsicWidth : item.intrinsicHeight;

		if (end - start === 1 && start >= 0 && start < tracks.length) {
			contributions[start] = Math.max(contributions[start] ?? 0, itemSize);
			continue;
		}

		const spannedGaps = Math.max(0, end - start - 1) * gap;
		const current = sum(sizes.slice(start, end)) + spannedGaps;

		if (current >= itemSize) {
			continue;
		}

		const extra = itemSize - current;
		const growable: number[] = [];

		for (let index = start; index < end; index++) {
			const track = tracks[index];
			if (!track) {
				continue;
			}

			if (
				track.type === 'auto' ||
				track.type === 'fr' ||
				(track.type === 'minmax' && track.max.type === 'fr')
			) {
				growable.push(index);
			}
		}

		if (growable.length === 0) {
			const last = end - 1;
			if (last >= 0 && last < sizes.length) {
				sizes[last] = (sizes[last] ?? 0) + extra;
			}

			continue;
		}

		const share = Math.floor(extra / growable.length);
		let leftover = extra - share * growable.length;
		for (const index of growable) {
			sizes[index] = (sizes[index] ?? 0) + share + (leftover > 0 ? 1 : 0);
			if (leftover > 0) {
				leftover--;
			}
		}
	}

	for (const [index, track] of tracks.entries()) {
		const content = contributions[index] ?? 0;

		if (track.type === 'auto') {
			sizes[index] = Math.max(sizes[index] ?? 0, content);
			continue;
		}

		if (track.type === 'minmax' && track.max.type === 'fixed') {
			sizes[index] = Math.min(track.max.value, Math.max(track.min, content));
			continue;
		}

		if (track.type === 'minmax') {
			sizes[index] = Math.max(track.min, sizes[index] ?? 0, content);
		}
	}

	const gapTotal = tracks.length > 1 ? (tracks.length - 1) * gap : 0;
	const remaining =
		available === undefined
			? 0
			: Math.max(0, available - (sum(sizes) + gapTotal));
	const weights = tracks.map(track => trackFrWeight(track));

	if (remaining > 0 && weights.some(weight => weight > 0)) {
		const extras = distributeFr(remaining, weights);
		for (const [index, extra] of extras.entries()) {
			sizes[index] = (sizes[index] ?? 0) + extra;
			const max = trackFixedMax(tracks[index]!);
			if (max !== undefined) {
				sizes[index] = Math.min(sizes[index]!, max);
			}
		}
	}

	return sizes.map(size => Math.max(0, Math.round(size)));
};

const expandTracks = (tracks: Track[], count: number): Track[] => {
	const expanded = [...tracks];
	while (expanded.length < count) {
		expanded.push({type: 'auto'});
	}

	return expanded;
};

const trackOffsets = (sizes: number[], gap: number): number[] => {
	const offsets = [0];
	for (const [index, size] of sizes.entries()) {
		offsets.push(offsets[index]! + size + (index < sizes.length - 1 ? gap : 0));
	}

	return offsets;
};

const collectGridItems = (node: DOMElement): DOMElement[] =>
	node.childNodes.filter(child => isVisibleGridItem(child));

const prepareGridContainer = (node: DOMElement): void => {
	const yogaNode = node.yogaNode;
	if (!yogaNode || node.style.display !== 'grid') {
		return;
	}

	resetContainerDimensions(node);

	const children = collectGridItems(node);
	for (const child of children) {
		resetItemDimensions(child);
	}

	const columnTracks = parseTrackList(node.style.gridTemplateColumns);
	const rowTracks = parseTrackList(node.style.gridTemplateRows);
	const {items: placed, columnCount, rowCount} = placeItems(
		children,
		columnTracks.length,
		rowTracks.length,
	);

	const columns = expandTracks(columnTracks, columnCount);
	const rows = expandTracks(rowTracks, rowCount);
	const gaps = getGaps(node.style);

	const items: GridItem[] = placed.map(item => {
		const measured = measureNode(item.node);
		return {
			...item,
			intrinsicWidth: measured.width,
			intrinsicHeight: measured.height,
		};
	});

	for (const child of children) {
		child.yogaNode?.setPositionType(Yoga.POSITION_TYPE_ABSOLUTE);
	}

	const columnSizes = sizeTracks(
		columns,
		items,
		'column',
		undefined,
		gaps.column,
	);
	const rowSizes = sizeTracks(rows, items, 'row', undefined, gaps.row);

	const intrinsicWidth =
		sum(columnSizes) +
		(columns.length > 1 ? (columns.length - 1) * gaps.column : 0) +
		styleBoxEdges(node.style, 'horizontal');
	const intrinsicHeight =
		sum(rowSizes) +
		(rows.length > 1 ? (rows.length - 1) * gaps.row : 0) +
		styleBoxEdges(node.style, 'vertical');

	if (!hasExplicitDimension(node.style, 'width')) {
		const minWidth = node.style.minWidth;
		yogaNode.setMinWidth(
			typeof minWidth === 'number'
				? Math.max(minWidth, intrinsicWidth)
				: intrinsicWidth,
		);
	}

	if (!hasExplicitDimension(node.style, 'height')) {
		const minHeight = node.style.minHeight;
		yogaNode.setMinHeight(
			typeof minHeight === 'number'
				? Math.max(minHeight, intrinsicHeight)
				: intrinsicHeight,
		);
	}
};

const setItemSize = (
	node: DOMElement,
	cellWidth: number,
	cellHeight: number,
): void => {
	const yogaNode = node.yogaNode;
	if (!yogaNode) {
		return;
	}

	if (typeof node.style.width === 'number') {
		yogaNode.setWidth(node.style.width);
	} else if (typeof node.style.width === 'string') {
		const percent = Number.parseInt(node.style.width, 10);
		yogaNode.setWidth(
			Number.isFinite(percent) ? (cellWidth * percent) / 100 : cellWidth,
		);
	} else {
		yogaNode.setWidth(cellWidth);
	}

	if (typeof node.style.height === 'number') {
		yogaNode.setHeight(node.style.height);
	} else if (typeof node.style.height === 'string') {
		const percent = Number.parseInt(node.style.height, 10);
		yogaNode.setHeight(
			Number.isFinite(percent) ? (cellHeight * percent) / 100 : cellHeight,
		);
	} else {
		yogaNode.setHeight(cellHeight);
	}

	yogaNode.calculateLayout(cellWidth, cellHeight, Yoga.DIRECTION_LTR);
};

const finalizeGridContainer = (node: DOMElement): void => {
	const yogaNode = node.yogaNode;
	if (!yogaNode || node.style.display !== 'grid') {
		return;
	}

	const children = collectGridItems(node);
	const columnTracks = parseTrackList(node.style.gridTemplateColumns);
	const rowTracks = parseTrackList(node.style.gridTemplateRows);
	const {items: placed, columnCount, rowCount} = placeItems(
		children,
		columnTracks.length,
		rowTracks.length,
	);

	const columns = expandTracks(columnTracks, columnCount);
	const rows = expandTracks(rowTracks, rowCount);
	const gaps = getGaps(node.style);

	const items: GridItem[] = placed.map(item => {
		const measured = measureNode(item.node);
		return {
			...item,
			intrinsicWidth: measured.width,
			intrinsicHeight: measured.height,
		};
	});

	const contentWidth = Math.max(
		0,
		Math.round(yogaNode.getComputedWidth() - boxEdges(node, 'horizontal')),
	);
	const contentHeight = Math.max(
		0,
		Math.round(yogaNode.getComputedHeight() - boxEdges(node, 'vertical')),
	);

	const columnSizes = sizeTracks(
		columns,
		items,
		'column',
		contentWidth,
		gaps.column,
	);
	const rowSizes = sizeTracks(rows, items, 'row', contentHeight, gaps.row);
	const columnStarts = trackOffsets(columnSizes, gaps.column);
	const rowStarts = trackOffsets(rowSizes, gaps.row);

	const contentLeft =
		yogaNode.getComputedBorder(Yoga.EDGE_LEFT) +
		yogaNode.getComputedPadding(Yoga.EDGE_LEFT);
	const contentTop =
		yogaNode.getComputedBorder(Yoga.EDGE_TOP) +
		yogaNode.getComputedPadding(Yoga.EDGE_TOP);

	for (const item of items) {
		const columnStart = item.column.start - 1;
		const columnEnd = item.column.end - 1;
		const rowStart = item.row.start - 1;
		const rowEnd = item.row.end - 1;

		item.node.gridItemLayout = {
			left: Math.round(contentLeft + (columnStarts[columnStart] ?? 0)),
			top: Math.round(contentTop + (rowStarts[rowStart] ?? 0)),
		};

		setItemSize(
			item.node,
			Math.max(0, (columnStarts[columnEnd] ?? 0) - (columnStarts[columnStart] ?? 0)),
			Math.max(0, (rowStarts[rowEnd] ?? 0) - (rowStarts[rowStart] ?? 0)),
		);
	}
};

export const prepareGridLayout = (node: DOMElement): void => {
	if (node.style.display !== 'grid') {
		for (const child of node.childNodes) {
			if (child.yogaNode && child.gridItemLayout) {
				resetItemDimensions(child as DOMElement);
			}
		}
	}

	for (const child of node.childNodes) {
		if (child.yogaNode || child.nodeName !== '#text') {
			prepareGridLayout(child as DOMElement);
		}
	}

	if (node.style.display === 'grid') {
		prepareGridContainer(node);
	}
};

export const finalizeGridLayout = (node: DOMElement): void => {
	if (node.style.display === 'grid') {
		finalizeGridContainer(node);
	}

	for (const child of node.childNodes) {
		if (child.yogaNode || child.nodeName !== '#text') {
			finalizeGridLayout(child as DOMElement);
		}
	}
};
