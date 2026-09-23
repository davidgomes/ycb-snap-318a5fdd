import Yoga from 'yoga-layout';
import {type DOMElement} from './dom.js';
import applyStyles, {type Styles} from './styles.js';

type FixedTrack = {readonly type: 'fixed'; readonly value: number};
type FlexTrack = {readonly type: 'fr'; readonly value: number};

export type GridTrack =
	| FixedTrack
	| FlexTrack
	| {readonly type: 'auto'}
	| {
			readonly type: 'minmax';
			readonly min: number;
			readonly max: FixedTrack | FlexTrack;
	  };

export type GridLine = {readonly start: number; readonly span: number};

export type GridArea = {
	readonly rowStart: number;
	readonly rowSpan: number;
	readonly columnStart: number;
	readonly columnSpan: number;
};

type TrackItem = {
	readonly start: number;
	readonly span: number;
	readonly size: number;
};

const autoTrack: GridTrack = {type: 'auto'};
const numberPattern = /^\d*\.?\d+$/;
const flexPattern = /^(\d*\.?\d+)fr$/;
const minmaxPattern = /^minmax\(([^,()]+),([^,()]+)\)$/;
const linePattern = /^\d+$/;

const parseFixedTrack = (token: string): FixedTrack | undefined =>
	numberPattern.test(token) ? {type: 'fixed', value: Number(token)} : undefined;

const parseFlexTrack = (token: string): FlexTrack | undefined => {
	const match = flexPattern.exec(token);
	return match ? {type: 'fr', value: Number(match[1])} : undefined;
};

const parseTrack = (token: string): GridTrack | undefined => {
	if (token === 'auto') {
		return autoTrack;
	}

	const minmax = minmaxPattern.exec(token);

	if (minmax) {
		const min = parseFixedTrack(minmax[1]!);
		const max = parseFixedTrack(minmax[2]!) ?? parseFlexTrack(minmax[2]!);
		return min && max ? {type: 'minmax', min: min.value, max} : undefined;
	}

	return parseFixedTrack(token) ?? parseFlexTrack(token);
};

/**
Parse a space-separated track list like `'10 auto 1fr minmax(5, 2fr)'`. Like in CSS, an invalid list is ignored entirely.
*/
export const parseTrackList = (value: string | undefined): GridTrack[] => {
	const normalized = value
		?.trim()
		.toLowerCase()
		.replaceAll(/\(\s*/g, '(')
		.replaceAll(/\s*,\s*/g, ',')
		.replaceAll(/\s*\)/g, ')');

	if (!normalized) {
		return [];
	}

	const tracks: GridTrack[] = [];

	for (const token of normalized.split(/\s+/)) {
		const track = parseTrack(token);

		if (!track) {
			return [];
		}

		tracks.push(track);
	}

	return tracks;
};

const parseLine = (value: string): number | undefined => {
	const trimmed = value.trim();

	if (!linePattern.test(trimmed)) {
		return undefined;
	}

	const line = Number(trimmed);
	return line >= 1 ? line : undefined;
};

/**
Parse a `gridColumn` or `gridRow` value into a 0-based start track and a span. Returns `undefined` for auto placement.
*/
export const parseGridLine = (
	value: number | string | undefined,
): GridLine | undefined => {
	if (value === undefined) {
		return undefined;
	}

	const parts = String(value).split('/');

	if (parts.length > 2) {
		return undefined;
	}

	const start = parseLine(parts[0]!);

	if (start === undefined) {
		return undefined;
	}

	const end = parts.length === 2 ? parseLine(parts[1]!) : start + 1;

	if (end === undefined) {
		return undefined;
	}

	if (start === end) {
		return {start: start - 1, span: 1};
	}

	return {start: Math.min(start, end) - 1, span: Math.abs(end - start)};
};

/**
Place items on the grid following the CSS grid auto-placement algorithm with `grid-auto-flow: row`.
*/
export const placeGridItems = (
	requests: ReadonlyArray<{
		readonly row: GridLine | undefined;
		readonly column: GridLine | undefined;
	}>,
	explicitColumnCount: number,
	explicitRowCount: number,
): {areas: GridArea[]; columnCount: number; rowCount: number} => {
	const occupied = new Set<string>();
	const areas: GridArea[] = Array.from({length: requests.length});
	let columnCount = Math.max(explicitColumnCount, 1);

	const isFree = (area: GridArea): boolean => {
		for (let row = area.rowStart; row < area.rowStart + area.rowSpan; row++) {
			for (
				let column = area.columnStart;
				column < area.columnStart + area.columnSpan;
				column++
			) {
				if (occupied.has(`${row},${column}`)) {
					return false;
				}
			}
		}

		return true;
	};

	const place = (index: number, area: GridArea): void => {
		for (let row = area.rowStart; row < area.rowStart + area.rowSpan; row++) {
			for (
				let column = area.columnStart;
				column < area.columnStart + area.columnSpan;
				column++
			) {
				occupied.add(`${row},${column}`);
			}
		}

		areas[index] = area;
		columnCount = Math.max(columnCount, area.columnStart + area.columnSpan);
	};

	for (const [index, {row, column}] of requests.entries()) {
		if (row && column) {
			place(index, {
				rowStart: row.start,
				rowSpan: row.span,
				columnStart: column.start,
				columnSpan: column.span,
			});
		}
	}

	const rowCursors = new Map<number, number>();

	for (const [index, {row, column}] of requests.entries()) {
		if (!row || column) {
			continue;
		}

		let columnStart = rowCursors.get(row.start) ?? 0;

		while (
			!isFree({
				rowStart: row.start,
				rowSpan: row.span,
				columnStart,
				columnSpan: 1,
			})
		) {
			columnStart++;
		}

		place(index, {
			rowStart: row.start,
			rowSpan: row.span,
			columnStart,
			columnSpan: 1,
		});
		rowCursors.set(row.start, columnStart + 1);
	}

	for (const {row, column} of requests) {
		if (!row && column) {
			columnCount = Math.max(columnCount, column.start + column.span);
		}
	}

	let cursorRow = 0;
	let cursorColumn = 0;

	for (const [index, {row, column}] of requests.entries()) {
		if (row) {
			continue;
		}

		if (column) {
			if (column.start < cursorColumn) {
				cursorRow++;
			}

			cursorColumn = column.start;

			while (
				!isFree({
					rowStart: cursorRow,
					rowSpan: 1,
					columnStart: column.start,
					columnSpan: column.span,
				})
			) {
				cursorRow++;
			}

			place(index, {
				rowStart: cursorRow,
				rowSpan: 1,
				columnStart: column.start,
				columnSpan: column.span,
			});
			continue;
		}

		while (
			cursorColumn >= columnCount ||
			!isFree({
				rowStart: cursorRow,
				rowSpan: 1,
				columnStart: cursorColumn,
				columnSpan: 1,
			})
		) {
			if (cursorColumn >= columnCount) {
				cursorRow++;
				cursorColumn = 0;
			} else {
				cursorColumn++;
			}
		}

		place(index, {
			rowStart: cursorRow,
			rowSpan: 1,
			columnStart: cursorColumn,
			columnSpan: 1,
		});
		cursorColumn++;
	}

	let rowCount = explicitRowCount;

	for (const area of areas) {
		rowCount = Math.max(rowCount, area.rowStart + area.rowSpan);
	}

	return {areas, columnCount, rowCount};
};

const getFlexFactor = (track: GridTrack): number => {
	if (track.type === 'fr') {
		return track.value;
	}

	if (track.type === 'minmax' && track.max.type === 'fr') {
		return track.max.value;
	}

	return 0;
};

const isFlexibleTrack = (track: GridTrack): boolean =>
	track.type === 'fr' || (track.type === 'minmax' && track.max.type === 'fr');

const sum = (values: readonly number[]): number =>
	values.reduce((total, value) => total + value, 0);

/**
Resolve the size of each track. When `available` is `undefined`, the grid is sized to its content.
*/
export const resolveTrackSizes = (
	tracks: readonly GridTrack[],
	items: readonly TrackItem[],
	available: number | undefined,
	gap: number,
): number[] => {
	const sizes = tracks.map(track => {
		if (track.type === 'fixed') {
			return track.value;
		}

		return track.type === 'minmax' ? track.min : 0;
	});

	const contentSizes = tracks.map(() => 0);

	for (const item of items) {
		if (item.span === 1) {
			contentSizes[item.start] = Math.max(contentSizes[item.start]!, item.size);
		}
	}

	for (const [index, track] of tracks.entries()) {
		if (track.type === 'auto') {
			sizes[index] = contentSizes[index]!;
		}
	}

	const spanningItems = items
		.filter(item => item.span > 1)
		.sort((a, b) => a.span - b.span);

	for (const item of spanningItems) {
		const indices = Array.from({length: item.span}, (_, i) => item.start + i);

		if (indices.some(index => isFlexibleTrack(tracks[index]!))) {
			continue;
		}

		const autoIndices = indices.filter(index => tracks[index]!.type === 'auto');

		const extra =
			item.size -
			sum(indices.map(index => sizes[index]!)) -
			gap * (item.span - 1);

		if (autoIndices.length === 0 || extra <= 0) {
			continue;
		}

		for (const index of autoIndices) {
			sizes[index]! += extra / autoIndices.length;
		}
	}

	const flexibleIndices = [...tracks.keys()].filter(index =>
		isFlexibleTrack(tracks[index]!),
	);

	let growableIndices = [...tracks.keys()].filter(index => {
		const track = tracks[index]!;
		return (
			track.type === 'minmax' &&
			track.max.type === 'fixed' &&
			track.max.value > sizes[index]!
		);
	});

	const getLimit = (index: number): number => {
		const track = tracks[index]!;
		return track.type === 'minmax' ? track.max.value : 0;
	};

	if (available === undefined) {
		for (const index of growableIndices) {
			sizes[index] = getLimit(index);
		}

		let fractionSize = 0;

		for (const index of flexibleIndices) {
			const factor = getFlexFactor(tracks[index]!);

			if (factor > 0) {
				fractionSize = Math.max(
					fractionSize,
					Math.max(sizes[index]!, contentSizes[index]!) / factor,
				);
			}
		}

		for (const index of flexibleIndices) {
			sizes[index] = Math.max(
				sizes[index]!,
				fractionSize * getFlexFactor(tracks[index]!),
			);
		}

		return sizes;
	}

	let freeSpace = available - sum(sizes) - gap * Math.max(tracks.length - 1, 0);

	// Bounded `minmax()` tracks grow equally toward their maximum before `fr` tracks receive any space
	while (freeSpace > 0 && growableIndices.length > 0) {
		const share = freeSpace / growableIndices.length;
		const unsaturated: number[] = [];

		for (const index of growableIndices) {
			const growth = Math.min(share, getLimit(index) - sizes[index]!);
			sizes[index]! += growth;
			freeSpace -= growth;

			if (sizes[index]! < getLimit(index)) {
				unsaturated.push(index);
			}
		}

		if (unsaturated.length === growableIndices.length) {
			break;
		}

		growableIndices = unsaturated;
	}

	const totalFactor = sum(
		flexibleIndices.map(index => getFlexFactor(tracks[index]!)),
	);

	if (freeSpace > 0 && totalFactor > 0) {
		// Like in CSS, factors summing to less than 1 only take that fraction of the free space
		const fractionSize = freeSpace / Math.max(totalFactor, 1);

		for (const index of flexibleIndices) {
			sizes[index]! += fractionSize * getFlexFactor(tracks[index]!);
		}
	}

	return sizes;
};

const getTrackOffsets = (
	sizes: readonly number[],
	gap: number,
): Array<{start: number; end: number}> => {
	let cursor = 0;

	return sizes.map(size => {
		const start = cursor;
		cursor += size;
		const end = cursor;
		cursor += gap;

		return {start: Math.round(start), end: Math.round(end)};
	});
};

const fillTracks = (
	tracks: readonly GridTrack[],
	count: number,
): GridTrack[] => [
	...tracks,
	...Array.from<GridTrack>({length: Math.max(count - tracks.length, 0)}).fill(
		autoTrack,
	),
];

const isGridContainer = (node: DOMElement): boolean =>
	node.style.display === 'grid' && node.yogaNode !== undefined;

const isGridItemCandidate = (node: DOMElement): boolean =>
	node.yogaNode !== undefined &&
	node.style.position !== 'absolute' &&
	node.yogaNode.getDisplay() !== Yoga.DISPLAY_NONE;

const restoreGridItem = (node: DOMElement): void => {
	const {style} = node;
	node.internal_isGridItem = false;

	applyStyles(node.yogaNode!, {
		position: style.position,
		top: style.top,
		left: style.left,
		width: style.width,
		height: style.height,
	});
};

const restoreGridContainer = (node: DOMElement): void => {
	const {style} = node;
	node.internal_isGridContainer = false;

	applyStyles(node.yogaNode!, {
		minWidth: style.minWidth,
		minHeight: style.minHeight,
	});
};

const prepareGridItem = (node: DOMElement): void => {
	const yogaNode = node.yogaNode!;
	node.internal_isGridItem = true;

	yogaNode.setPositionType(Yoga.POSITION_TYPE_ABSOLUTE);
	yogaNode.setPosition(Yoga.EDGE_LEFT, 0);
	yogaNode.setPosition(Yoga.EDGE_TOP, 0);
	applyStyles(yogaNode, {width: node.style.width, height: node.style.height});
};

const collectGridContainers = (
	node: DOMElement,
	containers: DOMElement[],
): void => {
	if (isGridContainer(node)) {
		containers.push(node);
		return;
	}

	for (const child of node.childNodes) {
		if (child.nodeName !== '#text') {
			collectGridContainers(child, containers);
		}
	}
};

const getGap = (style: Styles, key: 'columnGap' | 'rowGap'): number =>
	style[key] ?? style.gap ?? 0;

const layoutGridContainer = (
	container: DOMElement,
	computeLayout: () => void,
): void => {
	const yogaNode = container.yogaNode!;

	if (yogaNode.getDisplay() === Yoga.DISPLAY_NONE) {
		return;
	}

	const {style} = container;
	container.internal_isGridContainer = true;
	applyStyles(yogaNode, {minWidth: style.minWidth, minHeight: style.minHeight});

	const items: DOMElement[] = [];
	const nestedContainers: DOMElement[] = [];

	for (const child of container.childNodes) {
		if (child.nodeName === '#text') {
			continue;
		}

		if (isGridItemCandidate(child)) {
			prepareGridItem(child);
			items.push(child);
		} else if (child.internal_isGridItem) {
			restoreGridItem(child);
		}

		collectGridContainers(child, nestedContainers);
	}

	const layoutNestedContainers = (): void => {
		for (const nestedContainer of nestedContainers) {
			layoutGridContainer(nestedContainer, computeLayout);
		}
	};

	computeLayout();

	const paddingLeft = yogaNode.getComputedPadding(Yoga.EDGE_LEFT);
	const paddingTop = yogaNode.getComputedPadding(Yoga.EDGE_TOP);

	const horizontalInset =
		paddingLeft +
		yogaNode.getComputedPadding(Yoga.EDGE_RIGHT) +
		yogaNode.getComputedBorder(Yoga.EDGE_LEFT) +
		yogaNode.getComputedBorder(Yoga.EDGE_RIGHT);

	const verticalInset =
		paddingTop +
		yogaNode.getComputedPadding(Yoga.EDGE_BOTTOM) +
		yogaNode.getComputedBorder(Yoga.EDGE_TOP) +
		yogaNode.getComputedBorder(Yoga.EDGE_BOTTOM);

	const outerWidth = yogaNode.getComputedWidth();
	const outerHeight = yogaNode.getComputedHeight();

	// Children are absolutely positioned, so a content-sized container has no content box yet
	const contentWidth = outerWidth - horizontalInset;
	const contentHeight = outerHeight - verticalInset;
	const availableWidth = contentWidth > 0 ? contentWidth : undefined;
	const availableHeight = contentHeight > 0 ? contentHeight : undefined;

	const explicitColumns = parseTrackList(style.gridTemplateColumns);
	const explicitRows = parseTrackList(style.gridTemplateRows);

	const {areas, columnCount, rowCount} = placeGridItems(
		items.map(item => ({
			row: parseGridLine(item.style.gridRow),
			column: parseGridLine(item.style.gridColumn),
		})),
		explicitColumns.length,
		explicitRows.length,
	);

	const columnTracks = fillTracks(explicitColumns, columnCount);
	const rowTracks = fillTracks(explicitRows, rowCount);
	const columnGap = getGap(style, 'columnGap');
	const rowGap = getGap(style, 'rowGap');

	const needsItemWidths = columnTracks.some(
		track =>
			track.type === 'auto' ||
			(availableWidth === undefined && isFlexibleTrack(track)),
	);

	if (needsItemWidths && nestedContainers.length > 0) {
		layoutNestedContainers();
	}

	const columns = getTrackOffsets(
		resolveTrackSizes(
			columnTracks,
			items.map((item, index) => {
				const itemNode = item.yogaNode!;

				return {
					start: areas[index]!.columnStart,
					span: areas[index]!.columnSpan,
					size: needsItemWidths
						? itemNode.getComputedWidth() +
							itemNode.getComputedMargin(Yoga.EDGE_LEFT) +
							itemNode.getComputedMargin(Yoga.EDGE_RIGHT)
						: 0,
				};
			}),
			availableWidth,
			columnGap,
		),
		columnGap,
	);

	for (const [index, item] of items.entries()) {
		const area = areas[index]!;
		const itemNode = item.yogaNode!;
		const {start} = columns[area.columnStart]!;
		const {end} = columns[area.columnStart + area.columnSpan - 1]!;

		itemNode.setPosition(Yoga.EDGE_LEFT, paddingLeft + start);

		if (item.style.width === undefined) {
			itemNode.setWidth(
				Math.max(
					0,
					end -
						start -
						itemNode.getComputedMargin(Yoga.EDGE_LEFT) -
						itemNode.getComputedMargin(Yoga.EDGE_RIGHT),
				),
			);
		}
	}

	computeLayout();
	layoutNestedContainers();

	const rows = getTrackOffsets(
		resolveTrackSizes(
			rowTracks,
			items.map((item, index) => {
				const itemNode = item.yogaNode!;

				return {
					start: areas[index]!.rowStart,
					span: areas[index]!.rowSpan,
					size:
						itemNode.getComputedHeight() +
						itemNode.getComputedMargin(Yoga.EDGE_TOP) +
						itemNode.getComputedMargin(Yoga.EDGE_BOTTOM),
				};
			}),
			availableHeight,
			rowGap,
		),
		rowGap,
	);

	for (const [index, item] of items.entries()) {
		const area = areas[index]!;
		const itemNode = item.yogaNode!;
		const {start} = rows[area.rowStart]!;
		const {end} = rows[area.rowStart + area.rowSpan - 1]!;

		itemNode.setPosition(Yoga.EDGE_TOP, paddingTop + start);

		if (item.style.height === undefined) {
			itemNode.setHeight(
				Math.max(
					0,
					end -
						start -
						itemNode.getComputedMargin(Yoga.EDGE_TOP) -
						itemNode.getComputedMargin(Yoga.EDGE_BOTTOM),
				),
			);
		}
	}

	const gridWidth = (columns.at(-1)?.end ?? 0) + horizontalInset;
	const gridHeight = (rows.at(-1)?.end ?? 0) + verticalInset;

	if (style.width === undefined && gridWidth > outerWidth) {
		yogaNode.setMinWidth(gridWidth);
	}

	if (style.height === undefined && gridHeight > outerHeight) {
		yogaNode.setMinHeight(gridHeight);
	}

	computeLayout();
};

const prepareTree = (
	node: DOMElement,
	containers: DOMElement[],
	isInsideGrid: boolean,
): void => {
	const isGrid = isGridContainer(node);

	if (node.internal_isGridContainer && !isGrid) {
		restoreGridContainer(node);
	}

	if (isGrid && !isInsideGrid) {
		containers.push(node);
	}

	for (const child of node.childNodes) {
		if (child.nodeName === '#text') {
			continue;
		}

		if (child.internal_isGridItem && !isGrid) {
			restoreGridItem(child);
		}

		prepareTree(child, containers, isInsideGrid || isGrid);
	}
};

/**
Calculate the layout of the whole tree. Yoga has no grid support, so grid items are absolutely positioned inside their container after their tracks are resolved.
*/
const calculateLayout = (rootNode: DOMElement): void => {
	const yogaNode = rootNode.yogaNode!;
	const containers: DOMElement[] = [];

	const computeLayout = (): void => {
		yogaNode.calculateLayout(undefined, undefined, Yoga.DIRECTION_LTR);
	};

	prepareTree(rootNode, containers, false);
	computeLayout();

	for (const container of containers) {
		layoutGridContainer(container, computeLayout);
	}
};

export default calculateLayout;
