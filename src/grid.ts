import Yoga, {
	type MeasureFunction,
	type MeasureMode,
	type Node as YogaNode,
} from 'yoga-layout';
import {type DOMElement} from './dom.js';
import {type Styles} from './styles.js';

// Grid containers are leaves with a measure function in Yoga, and each of their children is a separate Yoga root. Ink places the children into grid cells itself.

type TrackSize =
	| {readonly type: 'auto'}
	| {readonly type: 'fixed'; readonly min: number; readonly max: number}
	| {readonly type: 'flex'; readonly min: number; readonly factor: number};

type Placement = {
	readonly start: number;
	readonly span: number;
};

type GridArea = {
	readonly column: Placement;
	readonly row: Placement;
};

type GridItem = {
	readonly node: DOMElement;
	readonly yogaNode: YogaNode;
	readonly area: GridArea;
};

type SizingItem = Placement & {
	readonly getSize: () => number;
};

type Track = {
	readonly start: number;
	readonly size: number;
};

type AvailableSpace = {
	readonly size: number;
	readonly mode: MeasureMode;
};

const numberPattern = /^\d*\.?\d+$/;
const fractionPattern = /^(\d*\.?\d+)fr$/;
const minmaxPattern = /^minmax\(\s*([^\s,]+)\s*,\s*([^\s,]+)\s*\)$/;
const trackListPattern = /minmax\([^)]*\)|\S+/g;

const parseTrackSize = (value: string): TrackSize | undefined => {
	if (value === 'auto') {
		return {type: 'auto'};
	}

	if (numberPattern.test(value)) {
		const size = Number(value);
		return {type: 'fixed', min: size, max: size};
	}

	const fraction = fractionPattern.exec(value);

	if (fraction) {
		return {type: 'flex', min: 0, factor: Number(fraction[1])};
	}

	const [, minimum = '', maximum = ''] = minmaxPattern.exec(value) ?? [];

	if (!numberPattern.test(minimum)) {
		return undefined;
	}

	const min = Number(minimum);

	if (numberPattern.test(maximum)) {
		return {type: 'fixed', min, max: Math.max(min, Number(maximum))};
	}

	const maximumFraction = fractionPattern.exec(maximum);

	if (maximumFraction) {
		return {type: 'flex', min, factor: Number(maximumFraction[1])};
	}

	return undefined;
};

const parseTrackList = (template: string | undefined): TrackSize[] => {
	const tracks: TrackSize[] = [];

	for (const value of template?.match(trackListPattern) ?? []) {
		const track = parseTrackSize(value);

		// Like in CSS, a template with an invalid track size is ignored entirely
		if (!track) {
			return [];
		}

		tracks.push(track);
	}

	return tracks;
};

const parseGridLine = (value: string): number | undefined => {
	const line = Number(value.trim());
	return Number.isInteger(line) && line >= 1 ? line - 1 : undefined;
};

const parsePlacement = (value: Styles['gridColumn']): Placement | undefined => {
	if (value === undefined) {
		return undefined;
	}

	const [startValue = '', endValue, ...rest] = String(value).split('/');
	const start = parseGridLine(startValue);

	if (start === undefined || rest.length > 0) {
		return undefined;
	}

	if (endValue === undefined) {
		return {start, span: 1};
	}

	const end = parseGridLine(endValue);

	if (end === undefined) {
		return undefined;
	}

	// Like in CSS, reversed lines are swapped and identical lines span a single track
	return end === start
		? {start, span: 1}
		: {start: Math.min(start, end), span: Math.abs(end - start)};
};

const getCellArea = (row: number, column: number): GridArea => ({
	row: {start: row, span: 1},
	column: {start: column, span: 1},
});

const getTrackIndexes = ({start, span}: Placement): number[] =>
	Array.from({length: span}, (_, index) => start + index);

const getCells = ({column, row}: GridArea): string[] =>
	getTrackIndexes(row).flatMap(rowIndex =>
		getTrackIndexes(column).map(columnIndex => `${rowIndex}:${columnIndex}`),
	);

// Implements the CSS grid item placement algorithm with `grid-auto-flow: row`
const placeItems = (
	placements: ReadonlyArray<{column?: Placement; row?: Placement}>,
	explicitColumnCount: number,
): GridArea[] => {
	const areas: Array<GridArea | undefined> = [];
	const occupiedCells = new Set<string>();

	const isFree = (area: GridArea): boolean =>
		getCells(area).every(cell => !occupiedCells.has(cell));

	const place = (index: number, area: GridArea): void => {
		areas[index] = area;

		for (const cell of getCells(area)) {
			occupiedCells.add(cell);
		}
	};

	for (const [index, {column, row}] of placements.entries()) {
		if (column && row) {
			place(index, {column, row});
		}
	}

	const rowCursors = new Map<number, number>();

	for (const [index, {column, row}] of placements.entries()) {
		if (column !== undefined || row === undefined) {
			continue;
		}

		let columnStart = rowCursors.get(row.start) ?? 0;

		while (!isFree({column: {start: columnStart, span: 1}, row})) {
			columnStart++;
		}

		place(index, {column: {start: columnStart, span: 1}, row});
		rowCursors.set(row.start, columnStart + 1);
	}

	let columnCount = explicitColumnCount;

	for (const [index, {column}] of placements.entries()) {
		const placedColumn = areas[index]?.column ?? column;

		columnCount = Math.max(
			columnCount,
			placedColumn ? placedColumn.start + placedColumn.span : 1,
		);
	}

	let cursorRow = 0;
	let cursorColumn = 0;

	for (const [index, {column}] of placements.entries()) {
		if (areas[index]) {
			continue;
		}

		if (column) {
			if (column.start < cursorColumn) {
				cursorRow++;
			}

			cursorColumn = column.start;

			while (!isFree({column, row: {start: cursorRow, span: 1}})) {
				cursorRow++;
			}

			place(index, {column, row: {start: cursorRow, span: 1}});
			continue;
		}

		while (!isFree(getCellArea(cursorRow, cursorColumn))) {
			cursorColumn++;

			if (cursorColumn === columnCount) {
				cursorColumn = 0;
				cursorRow++;
			}
		}

		place(index, getCellArea(cursorRow, cursorColumn));
	}

	return areas as GridArea[];
};

const sum = (values: number[]): number =>
	values.reduce((total, value) => total + value, 0);

const sizeTracks = (
	tracks: readonly TrackSize[],
	items: readonly SizingItem[],
	gap: number,
	availableSize: number | undefined,
): number[] => {
	const sizes = tracks.map(track => (track.type === 'auto' ? 0 : track.min));
	const trackIndexes = tracks.map((_, index) => index);
	const getFactor = (index: number): number => {
		const track = tracks[index]!;
		return track.type === 'flex' ? track.factor : 0;
	};

	const getSpannedSize = (item: SizingItem): number =>
		sum(getTrackIndexes(item).map(index => sizes[index]!)) +
		gap * (item.span - 1);
	const getFreeSpace = (size: number): number =>
		size - sum(sizes) - gap * Math.max(0, tracks.length - 1);
	const spansFlexibleTrack = (item: SizingItem): boolean =>
		getTrackIndexes(item).some(index => tracks[index]!.type === 'flex');

	// Grow `auto` tracks to fit their items, handling items spanning fewer tracks first
	const intrinsicItems = items
		.filter(
			item =>
				!spansFlexibleTrack(item) &&
				getTrackIndexes(item).some(index => tracks[index]!.type === 'auto'),
		)
		.sort((a, b) => a.span - b.span);

	for (const item of intrinsicItems) {
		const autoIndexes = getTrackIndexes(item).filter(
			index => tracks[index]!.type === 'auto',
		);
		const extraSize = item.getSize() - getSpannedSize(item);

		if (extraSize > 0) {
			for (const index of autoIndexes) {
				sizes[index]! += extraSize / autoIndexes.length;
			}
		}
	}

	// Grow `minmax()` tracks with a fixed maximum towards it, sharing the free space equally.
	// Without a definite size, they grow all the way to their maximum.
	const maxSizes = tracks.map((track, index) =>
		track.type === 'fixed' ? Math.max(track.max, sizes[index]!) : sizes[index]!,
	);

	let growableIndexes = trackIndexes.filter(
		index => maxSizes[index]! > sizes[index]!,
	);
	let freeSpace =
		availableSize === undefined
			? Number.POSITIVE_INFINITY
			: getFreeSpace(availableSize);

	while (freeSpace > 0 && growableIndexes.length > 0) {
		const share = freeSpace / growableIndexes.length;

		for (const index of growableIndexes) {
			const growth = Math.min(share, maxSizes[index]! - sizes[index]!);
			sizes[index]! += growth;
			freeSpace -= growth;
		}

		growableIndexes = growableIndexes.filter(
			index => maxSizes[index]! > sizes[index]!,
		);
	}

	// Distribute the space remaining after all minimums proportionally to `fr` tracks
	const flexibleIndexes = trackIndexes.filter(index => getFactor(index) > 0);
	const totalFactor = sum(flexibleIndexes.map(index => getFactor(index)));

	if (totalFactor === 0) {
		return sizes;
	}

	let fraction = 0;

	if (availableSize === undefined) {
		// Without a definite size, `fr` tracks grow just enough to fit their items
		for (const item of items) {
			const factor = sum(getTrackIndexes(item).map(index => getFactor(index)));

			if (factor > 0) {
				fraction = Math.max(
					fraction,
					(item.getSize() - getSpannedSize(item)) / factor,
				);
			}
		}
	} else {
		fraction = Math.max(0, getFreeSpace(availableSize)) / totalFactor;
	}

	for (const index of flexibleIndexes) {
		sizes[index]! += fraction * getFactor(index);
	}

	return sizes;
};

// Round track edges rather than sizes, so that rounding errors don't accumulate
const getTracks = (sizes: readonly number[], gap: number): Track[] => {
	const tracks: Track[] = [];
	let position = 0;

	for (const size of sizes) {
		const start = Math.round(position);
		tracks.push({start, size: Math.round(position + size) - start});
		position += size + gap;
	}

	return tracks;
};

const getTotalSize = (tracks: readonly Track[]): number => {
	const lastTrack = tracks.at(-1);
	return lastTrack ? lastTrack.start + lastTrack.size : 0;
};

const getSpannedTrack = (
	tracks: readonly Track[],
	{start, span}: Placement,
) => {
	const firstTrack = tracks[start]!;
	const lastTrack = tracks[start + span - 1]!;

	return {
		start: firstTrack.start,
		size: lastTrack.start + lastTrack.size - firstTrack.start,
	};
};

const sizeAxis = (
	trackSizes: readonly TrackSize[],
	items: readonly SizingItem[],
	gap: number,
	available: AvailableSpace,
): Track[] => {
	if (available.mode === Yoga.MEASURE_MODE_EXACTLY) {
		return getTracks(
			sizeTracks(trackSizes, items, gap, Math.max(0, available.size)),
			gap,
		);
	}

	const tracks = getTracks(sizeTracks(trackSizes, items, gap, undefined), gap);

	if (
		available.mode === Yoga.MEASURE_MODE_AT_MOST &&
		getTotalSize(tracks) > available.size
	) {
		return getTracks(
			sizeTracks(trackSizes, items, gap, Math.max(0, available.size)),
			gap,
		);
	}

	return tracks;
};

const withAutoTracks = (
	tracks: readonly TrackSize[],
	count: number,
): TrackSize[] => [
	...tracks,
	...Array.from(
		{length: Math.max(0, count - tracks.length)},
		(): TrackSize => ({type: 'auto'}),
	),
];

const memoize = (getValue: () => number): (() => number) => {
	let value: number | undefined;

	return () => {
		value ??= getValue();
		return value;
	};
};

const getGridItems = (node: DOMElement) => {
	const children: Array<{node: DOMElement; yogaNode: YogaNode}> = [];

	for (const childNode of node.childNodes) {
		if (
			childNode.nodeName !== '#text' &&
			childNode.yogaNode &&
			childNode.yogaNode.getDisplay() !== Yoga.DISPLAY_NONE
		) {
			children.push({node: childNode, yogaNode: childNode.yogaNode});
		}
	}

	return children;
};

const computeGridLayout = (
	node: DOMElement,
	availableWidth: AvailableSpace,
	availableHeight: AvailableSpace,
) => {
	const {style} = node;
	const columnGap = style.columnGap ?? style.gap ?? 0;
	const rowGap = style.rowGap ?? style.gap ?? 0;
	const explicitColumns = parseTrackList(style.gridTemplateColumns);
	const explicitRows = parseTrackList(style.gridTemplateRows);
	const children = getGridItems(node);

	const areas = placeItems(
		children.map(child => ({
			column: parsePlacement(child.node.style.gridColumn),
			row: parsePlacement(child.node.style.gridRow),
		})),
		explicitColumns.length,
	);

	const items: GridItem[] = children.map((child, index) => ({
		...child,
		area: areas[index]!,
	}));

	const columnCount = Math.max(
		explicitColumns.length,
		...areas.map(({column}) => column.start + column.span),
	);

	const rowCount = Math.max(
		explicitRows.length,
		...areas.map(({row}) => row.start + row.span),
	);

	const columns = sizeAxis(
		withAutoTracks(explicitColumns, columnCount),
		items.map(({yogaNode, area}) => ({
			...area.column,
			getSize: memoize(() => {
				yogaNode.calculateLayout(undefined, undefined, Yoga.DIRECTION_LTR);

				return (
					yogaNode.getComputedWidth() +
					yogaNode.getComputedMargin(Yoga.EDGE_LEFT) +
					yogaNode.getComputedMargin(Yoga.EDGE_RIGHT)
				);
			}),
		})),
		columnGap,
		availableWidth,
	);

	const rows = sizeAxis(
		withAutoTracks(explicitRows, rowCount),
		items.map(({yogaNode, area}) => ({
			...area.row,
			getSize: memoize(() => {
				yogaNode.calculateLayout(
					getSpannedTrack(columns, area.column).size,
					undefined,
					Yoga.DIRECTION_LTR,
				);

				return (
					yogaNode.getComputedHeight() +
					yogaNode.getComputedMargin(Yoga.EDGE_TOP) +
					yogaNode.getComputedMargin(Yoga.EDGE_BOTTOM)
				);
			}),
		})),
		rowGap,
		availableHeight,
	);

	return {items, columns, rows};
};

export const isGridContainer = (node: DOMElement): boolean =>
	node.nodeName === 'ink-box' && node.style.display === 'grid';

export const createGridMeasureFunc =
	(node: DOMElement): MeasureFunction =>
	(width, widthMode, height, heightMode) => {
		const {columns, rows} = computeGridLayout(
			node,
			{size: width, mode: widthMode},
			{size: height, mode: heightMode},
		);

		return {width: getTotalSize(columns), height: getTotalSize(rows)};
	};

const layoutGrid = (node: DOMElement, yogaNode: YogaNode): void => {
	const left =
		yogaNode.getComputedBorder(Yoga.EDGE_LEFT) +
		yogaNode.getComputedPadding(Yoga.EDGE_LEFT);
	const top =
		yogaNode.getComputedBorder(Yoga.EDGE_TOP) +
		yogaNode.getComputedPadding(Yoga.EDGE_TOP);
	const width =
		yogaNode.getComputedWidth() -
		left -
		yogaNode.getComputedBorder(Yoga.EDGE_RIGHT) -
		yogaNode.getComputedPadding(Yoga.EDGE_RIGHT);
	const height =
		yogaNode.getComputedHeight() -
		top -
		yogaNode.getComputedBorder(Yoga.EDGE_BOTTOM) -
		yogaNode.getComputedPadding(Yoga.EDGE_BOTTOM);

	const {items, columns, rows} = computeGridLayout(
		node,
		{size: width, mode: Yoga.MEASURE_MODE_EXACTLY},
		{size: height, mode: Yoga.MEASURE_MODE_EXACTLY},
	);

	for (const item of items) {
		const column = getSpannedTrack(columns, item.area.column);
		const row = getSpannedTrack(rows, item.area.row);

		// Items stretch to fill their grid area, unless they have their own size
		item.yogaNode.calculateLayout(column.size, row.size, Yoga.DIRECTION_LTR);
		item.node.internal_gridOffset = {
			x: left + column.start,
			y: top + row.start,
		};
	}

	// Hidden items still need a layout pass, so that Yoga reports when they change again
	for (const childNode of node.childNodes) {
		if (childNode.yogaNode?.getDisplay() === Yoga.DISPLAY_NONE) {
			childNode.yogaNode.calculateLayout(
				undefined,
				undefined,
				Yoga.DIRECTION_LTR,
			);
		}
	}
};

/**
Position children of grid containers in their grid cells. Must run after every Yoga layout pass of the root node.
*/
export const calculateGridLayout = (node: DOMElement): void => {
	const {yogaNode} = node;

	if (yogaNode?.getDisplay() === Yoga.DISPLAY_NONE) {
		return;
	}

	if (yogaNode && isGridContainer(node)) {
		layoutGrid(node, yogaNode);
	}

	for (const childNode of node.childNodes) {
		if (childNode.nodeName !== '#text') {
			calculateGridLayout(childNode);
		}
	}
};
