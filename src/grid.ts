import Yoga, {type Node as YogaNode} from 'yoga-layout';
import {type DOMElement} from './dom.js';
import applyStyles from './styles.js';

type FixedTrack = {type: 'fixed'; value: number};
type FlexTrack = {type: 'fr'; value: number};
type Track =
	| FixedTrack
	| FlexTrack
	| {type: 'auto'}
	| {type: 'minmax'; min: number; max: FixedTrack | FlexTrack};

type LineRange = {start: number; span: number};

type TrackItem = LineRange & {size: number};

type TrackOffset = {start: number; end: number};

const numberPattern = /^\d+(?:\.\d+)?$/;
const flexPattern = /^(\d+(?:\.\d+)?)fr$/;
const minmaxPattern = /^minmax\(([^,]+),([^)]+)\)$/;

const parseFixedOrFlex = (
	value: string,
): FixedTrack | FlexTrack | undefined => {
	if (numberPattern.test(value)) {
		return {type: 'fixed', value: Number(value)};
	}

	const flexMatch = flexPattern.exec(value);
	if (flexMatch) {
		return {type: 'fr', value: Number(flexMatch[1])};
	}

	return undefined;
};

const parseTrack = (value: string): Track => {
	if (value === 'auto') {
		return {type: 'auto'};
	}

	const minmaxMatch = minmaxPattern.exec(value);
	if (minmaxMatch) {
		const min = minmaxMatch[1]!.trim();
		const max = parseFixedOrFlex(minmaxMatch[2]!.trim());

		if (numberPattern.test(min) && max) {
			return {type: 'minmax', min: Number(min), max};
		}

		return {type: 'auto'};
	}

	return parseFixedOrFlex(value) ?? {type: 'auto'};
};

export const parseTrackList = (value: string | undefined): Track[] => {
	if (!value) {
		return [];
	}

	const tokens: string[] = [];
	let current = '';
	let depth = 0;

	for (const character of value.trim()) {
		if (character === '(') {
			depth++;
		} else if (character === ')') {
			depth = Math.max(0, depth - 1);
		}

		if (/\s/.test(character) && depth === 0) {
			if (current) {
				tokens.push(current);
				current = '';
			}

			continue;
		}

		current += character;
	}

	if (current) {
		tokens.push(current);
	}

	return tokens.map(token => parseTrack(token.replaceAll(/\s/g, '')));
};

const parseLine = (value: string | undefined): number | undefined => {
	if (value === undefined || value.trim() === '') {
		return undefined;
	}

	const line = Number(value.trim());
	return Number.isInteger(line) && line !== 0 ? line : undefined;
};

// Converts a 1-based (or negative, counted from the end) grid line into a 0-based line index.
const resolveLine = (line: number, explicitTrackCount: number): number =>
	line > 0 ? line - 1 : Math.max(0, explicitTrackCount + 1 + line);

export const parseGridPlacement = (
	value: number | string | undefined,
	explicitTrackCount: number,
): LineRange | undefined => {
	if (value === undefined) {
		return undefined;
	}

	const [startValue, endValue] = String(value).split('/');
	const startLine = parseLine(startValue);

	if (startLine === undefined) {
		return undefined;
	}

	const start = resolveLine(startLine, explicitTrackCount);
	const endLine = parseLine(endValue);

	if (endLine === undefined) {
		return {start, span: 1};
	}

	const end = resolveLine(endLine, explicitTrackCount);

	if (end === start) {
		return {start, span: 1};
	}

	return {start: Math.min(start, end), span: Math.abs(end - start)};
};

type ItemPlacementInput = {column?: LineRange; row?: LineRange};
type ItemPlacement = {column: LineRange; row: LineRange};

export const placeGridItems = (
	inputs: ItemPlacementInput[],
	explicitColumnCount: number,
): {placements: ItemPlacement[]; columnCount: number; rowCount: number} => {
	const occupied = new Set<string>();
	const placements: Array<ItemPlacement | undefined> = inputs.map(
		() => undefined,
	);

	let columnCount = Math.max(explicitColumnCount, 1);
	for (const {column} of inputs) {
		if (column) {
			columnCount = Math.max(columnCount, column.start + column.span);
		}
	}

	const isFree = (row: LineRange, column: LineRange): boolean => {
		for (let r = row.start; r < row.start + row.span; r++) {
			for (let c = column.start; c < column.start + column.span; c++) {
				if (occupied.has(`${r},${c}`)) {
					return false;
				}
			}
		}

		return true;
	};

	const place = (index: number, row: LineRange, column: LineRange): void => {
		for (let r = row.start; r < row.start + row.span; r++) {
			for (let c = column.start; c < column.start + column.span; c++) {
				occupied.add(`${r},${c}`);
			}
		}

		placements[index] = {row, column};
	};

	for (const [index, {row, column}] of inputs.entries()) {
		if (row && column) {
			place(index, row, column);
		}
	}

	const rowCursors = new Map<number, number>();

	for (const [index, {row, column}] of inputs.entries()) {
		if (!row || column) {
			continue;
		}

		let start = rowCursors.get(row.start) ?? 0;
		while (!isFree(row, {start, span: 1})) {
			start++;
		}

		columnCount = Math.max(columnCount, start + 1);
		rowCursors.set(row.start, start + 1);
		place(index, row, {start, span: 1});
	}

	let cursorRow = 0;
	let cursorColumn = 0;

	for (const [index, {row, column}] of inputs.entries()) {
		if (row) {
			continue;
		}

		if (column) {
			if (column.start < cursorColumn) {
				cursorRow++;
			}

			cursorColumn = column.start;

			while (!isFree({start: cursorRow, span: 1}, column)) {
				cursorRow++;
			}

			place(index, {start: cursorRow, span: 1}, column);
			continue;
		}

		while (
			!isFree({start: cursorRow, span: 1}, {start: cursorColumn, span: 1})
		) {
			cursorColumn++;

			if (cursorColumn >= columnCount) {
				cursorColumn = 0;
				cursorRow++;
			}
		}

		place(index, {start: cursorRow, span: 1}, {start: cursorColumn, span: 1});
		cursorColumn++;

		if (cursorColumn >= columnCount) {
			cursorColumn = 0;
			cursorRow++;
		}
	}

	let rowCount = 0;
	for (const placement of placements) {
		rowCount = Math.max(rowCount, placement!.row.start + placement!.row.span);
	}

	return {
		placements: placements as ItemPlacement[],
		columnCount,
		rowCount,
	};
};

const flexFactor = (track: Track): number => {
	if (track.type === 'fr') {
		return track.value;
	}

	if (track.type === 'minmax' && track.max.type === 'fr') {
		return track.max.value;
	}

	return 0;
};

const totalSize = (sizes: number[], gap: number): number =>
	sizes.reduce((sum, size) => sum + size, 0) +
	gap * Math.max(0, sizes.length - 1);

/**
Resolve track sizes. When `available` is `undefined`, tracks are sized to their max-content size and flexible tracks behave like `auto`.
*/
export const sizeTracks = (
	tracks: Track[],
	items: TrackItem[],
	available: number | undefined,
	gap: number,
): number[] => {
	const isIndefinite = available === undefined;
	const isContentSized = (track: Track): boolean =>
		track.type === 'auto' || (isIndefinite && flexFactor(track) > 0);

	const sizes = tracks.map(track => {
		if (track.type === 'fixed') {
			return track.value;
		}

		return track.type === 'minmax' ? track.min : 0;
	});

	const sortedItems = [...items].sort((a, b) => a.span - b.span);

	for (const item of sortedItems) {
		const spanned: number[] = [];
		for (let index = item.start; index < item.start + item.span; index++) {
			spanned.push(index);
		}

		const contentTracks = spanned.filter(index =>
			isContentSized(tracks[index]!),
		);

		if (contentTracks.length === 0) {
			continue;
		}

		const current = totalSize(
			spanned.map(index => sizes[index]!),
			gap,
		);
		const extra = item.size - current;

		if (extra <= 0) {
			continue;
		}

		for (const index of contentTracks) {
			sizes[index]! += extra / contentTracks.length;
		}
	}

	const limits = tracks.map((track, index) =>
		track.type === 'minmax' && track.max.type === 'fixed'
			? Math.max(track.max.value, track.min)
			: sizes[index]!,
	);

	if (isIndefinite) {
		// Flexible tracks in max-content sizing share a single fraction size large enough to fit every flexible track's content.
		let fractionSize = 0;
		for (const [index, track] of tracks.entries()) {
			const factor = flexFactor(track);
			if (factor > 0) {
				fractionSize = Math.max(fractionSize, sizes[index]! / factor);
			}
		}

		return tracks.map((track, index) =>
			Math.max(limits[index]!, fractionSize * flexFactor(track)),
		);
	}

	let freeSpace = available - totalSize(sizes, gap);

	while (freeSpace > 0) {
		const growable = sizes
			.map((_size, index) => index)
			.filter(index => sizes[index]! < limits[index]!);

		if (growable.length === 0) {
			break;
		}

		const share = freeSpace / growable.length;
		for (const index of growable) {
			const growth = Math.min(share, limits[index]! - sizes[index]!);
			sizes[index]! += growth;
			freeSpace -= growth;
		}
	}

	if (freeSpace <= 0) {
		return sizes;
	}

	const totalFlex = tracks.reduce((sum, track) => sum + flexFactor(track), 0);

	if (totalFlex > 0) {
		const flexSpace = freeSpace / Math.max(totalFlex, 1);
		for (const [index, track] of tracks.entries()) {
			sizes[index]! += flexSpace * flexFactor(track);
		}

		return sizes;
	}

	const autoTracks = tracks
		.map((track, index) => (track.type === 'auto' ? index : -1))
		.filter(index => index !== -1);

	for (const index of autoTracks) {
		sizes[index]! += freeSpace / autoTracks.length;
	}

	return sizes;
};

const trackOffsets = (sizes: number[], gap: number): TrackOffset[] => {
	let position = 0;

	return sizes.map(size => {
		const offset = {
			start: Math.round(position),
			end: Math.round(position + size),
		};
		position += size + gap;
		return offset;
	});
};

const spanOffset = (offsets: TrackOffset[], range: LineRange): TrackOffset => ({
	start: offsets[range.start]!.start,
	end: offsets[range.start + range.span - 1]!.end,
});

const isGridContainer = (node: DOMElement): boolean =>
	node.nodeName === 'ink-box' && node.style.display === 'grid';

const restoreGridItem = (node: DOMElement): void => {
	const {position, top, right, bottom, left, width, height} = node.style;
	applyStyles(node.yogaNode!, {
		position,
		top,
		right,
		bottom,
		left,
		width,
		height,
	});
	node.internal_gridItem = false;
};

const restoreGridContainer = (node: DOMElement): void => {
	const {flexDirection, width, minHeight} = node.style;

	if (flexDirection === undefined) {
		node.yogaNode!.setFlexDirection(Yoga.FLEX_DIRECTION_COLUMN);
	}

	applyStyles(node.yogaNode!, {flexDirection, width, minHeight});
	node.internal_gridContainer = false;
	node.internal_gridIntrinsicSize = undefined;
};

const collectGridContainers = (
	node: DOMElement,
	containers: DOMElement[],
): void => {
	for (const child of node.childNodes) {
		if (child.nodeName === '#text' || !child.yogaNode) {
			continue;
		}

		if (child.internal_gridItem && !isGridContainer(node)) {
			restoreGridItem(child);
		}

		if (child.internal_gridContainer && !isGridContainer(child)) {
			restoreGridContainer(child);
		}

		if (child.yogaNode.getDisplay() === Yoga.DISPLAY_NONE) {
			continue;
		}

		if (isGridContainer(child)) {
			containers.push(child);
		}

		collectGridContainers(child, containers);
	}
};

const hasGridAncestor = (node: DOMElement): boolean => {
	let parent = node.parentNode;

	while (parent) {
		if (isGridContainer(parent)) {
			return true;
		}

		parent = parent.parentNode;
	}

	return false;
};

const isStretchedHorizontally = (node: DOMElement): boolean => {
	const parentYogaNode = node.parentNode?.yogaNode;
	const yogaNode = node.yogaNode!;

	if (
		!parentYogaNode ||
		yogaNode.getPositionType() === Yoga.POSITION_TYPE_ABSOLUTE
	) {
		return false;
	}

	const direction = parentYogaNode.getFlexDirection();
	if (
		direction === Yoga.FLEX_DIRECTION_ROW ||
		direction === Yoga.FLEX_DIRECTION_ROW_REVERSE
	) {
		return false;
	}

	const alignSelf = yogaNode.getAlignSelf();
	const align =
		alignSelf === Yoga.ALIGN_AUTO ? parentYogaNode.getAlignItems() : alignSelf;

	return align === Yoga.ALIGN_STRETCH;
};

const horizontalMargin = (yogaNode: YogaNode): number =>
	yogaNode.getComputedMargin(Yoga.EDGE_LEFT) +
	yogaNode.getComputedMargin(Yoga.EDGE_RIGHT);

const verticalMargin = (yogaNode: YogaNode): number =>
	yogaNode.getComputedMargin(Yoga.EDGE_TOP) +
	yogaNode.getComputedMargin(Yoga.EDGE_BOTTOM);

const padTracks = (tracks: Track[], count: number): Track[] => [
	...tracks,
	...Array.from({length: Math.max(0, count - tracks.length)}, () => ({
		type: 'auto' as const,
	})),
];

const layoutGridContainer = (
	grid: DOMElement,
	computeLayout: () => void,
): void => {
	const yogaNode = grid.yogaNode!;
	const {style} = grid;
	const isGridItem = Boolean(grid.internal_gridItem);

	grid.internal_gridContainer = true;
	yogaNode.setFlexDirection(Yoga.FLEX_DIRECTION_ROW);

	if (!isGridItem && style.width === undefined) {
		yogaNode.setWidthAuto();
	}

	if (style.height === undefined) {
		applyStyles(yogaNode, {minHeight: style.minHeight});
	}

	const items: DOMElement[] = [];

	for (const child of grid.childNodes) {
		if (child.nodeName === '#text' || !child.yogaNode) {
			continue;
		}

		const childYogaNode = child.yogaNode;

		if (
			childYogaNode.getDisplay() === Yoga.DISPLAY_NONE ||
			child.style.position === 'absolute'
		) {
			if (child.internal_gridItem) {
				restoreGridItem(child);
			}

			continue;
		}

		child.internal_gridItem = true;
		childYogaNode.setPositionType(Yoga.POSITION_TYPE_ABSOLUTE);
		childYogaNode.setPosition(Yoga.EDGE_LEFT, 0);
		childYogaNode.setPosition(Yoga.EDGE_TOP, 0);
		childYogaNode.setPosition(Yoga.EDGE_RIGHT, Number.NaN);
		childYogaNode.setPosition(Yoga.EDGE_BOTTOM, Number.NaN);

		const intrinsicSize = child.internal_gridContainer
			? child.internal_gridIntrinsicSize
			: undefined;

		if (child.style.width === undefined) {
			if (intrinsicSize) {
				childYogaNode.setWidth(intrinsicSize.width);
			} else {
				childYogaNode.setWidthAuto();
			}
		}

		if (child.style.height === undefined) {
			if (intrinsicSize) {
				childYogaNode.setHeight(intrinsicSize.height);
			} else {
				childYogaNode.setHeightAuto();
			}
		}

		items.push(child);
	}

	computeLayout();

	const columnTracks = parseTrackList(style.gridTemplateColumns);
	const rowTracks = parseTrackList(style.gridTemplateRows);

	const {placements, columnCount, rowCount} = placeGridItems(
		items.map(item => ({
			column: parseGridPlacement(item.style.gridColumn, columnTracks.length),
			row: parseGridPlacement(item.style.gridRow, rowTracks.length),
		})),
		columnTracks.length,
	);

	const columns = padTracks(columnTracks, columnCount);
	const rows = padTracks(rowTracks, rowCount);
	const columnGap = style.columnGap ?? style.gap ?? 0;
	const rowGap = style.rowGap ?? style.gap ?? 0;

	const horizontalInset =
		yogaNode.getComputedPadding(Yoga.EDGE_LEFT) +
		yogaNode.getComputedPadding(Yoga.EDGE_RIGHT) +
		yogaNode.getComputedBorder(Yoga.EDGE_LEFT) +
		yogaNode.getComputedBorder(Yoga.EDGE_RIGHT);

	const verticalInset =
		yogaNode.getComputedPadding(Yoga.EDGE_TOP) +
		yogaNode.getComputedPadding(Yoga.EDGE_BOTTOM) +
		yogaNode.getComputedBorder(Yoga.EDGE_TOP) +
		yogaNode.getComputedBorder(Yoga.EDGE_BOTTOM);

	const columnItems = items.map((item, index) => ({
		...placements[index]!.column,
		size: item.yogaNode!.getComputedWidth() + horizontalMargin(item.yogaNode!),
	}));

	const maxContentWidth =
		totalSize(
			sizeTracks(columns, columnItems, undefined, columnGap),
			columnGap,
		) + horizontalInset;

	if (
		!isGridItem &&
		style.width === undefined &&
		!isStretchedHorizontally(grid)
	) {
		yogaNode.setWidth(maxContentWidth);
		computeLayout();
	}

	const columnOffsets = trackOffsets(
		sizeTracks(
			columns,
			columnItems,
			Math.max(0, yogaNode.getComputedWidth() - horizontalInset),
			columnGap,
		),
		columnGap,
	);

	yogaNode.setFlexDirection(Yoga.FLEX_DIRECTION_COLUMN);

	for (const [index, item] of items.entries()) {
		if (item.style.width === undefined) {
			const {start, end} = spanOffset(columnOffsets, placements[index]!.column);
			item.yogaNode!.setWidth(
				Math.max(0, end - start - horizontalMargin(item.yogaNode!)),
			);
		}
	}

	computeLayout();

	const isHeightDefinite = style.height !== undefined || isGridItem;

	const rowItems = items.map((item, index) => ({
		...placements[index]!.row,
		size: item.yogaNode!.getComputedHeight() + verticalMargin(item.yogaNode!),
	}));

	const rowSizes = sizeTracks(
		rows,
		rowItems,
		isHeightDefinite
			? Math.max(0, yogaNode.getComputedHeight() - verticalInset)
			: undefined,
		rowGap,
	);
	const rowOffsets = trackOffsets(rowSizes, rowGap);
	const contentHeight = rowOffsets.at(-1)?.end ?? 0;

	if (!isHeightDefinite) {
		const minHeight =
			typeof style.minHeight === 'string'
				? yogaNode.getComputedHeight()
				: (style.minHeight ?? 0);

		yogaNode.setMinHeight(Math.max(contentHeight + verticalInset, minHeight));
	}

	const paddingLeft = yogaNode.getComputedPadding(Yoga.EDGE_LEFT);
	const paddingTop = yogaNode.getComputedPadding(Yoga.EDGE_TOP);

	for (const [index, item] of items.entries()) {
		const itemYogaNode = item.yogaNode!;
		const column = spanOffset(columnOffsets, placements[index]!.column);
		const row = spanOffset(rowOffsets, placements[index]!.row);

		itemYogaNode.setPosition(Yoga.EDGE_LEFT, paddingLeft + column.start);
		itemYogaNode.setPosition(Yoga.EDGE_TOP, paddingTop + row.start);

		if (item.style.height === undefined) {
			itemYogaNode.setHeight(
				Math.max(0, row.end - row.start - verticalMargin(itemYogaNode)),
			);
		}
	}

	grid.internal_gridIntrinsicSize = {
		width: maxContentWidth,
		height: contentHeight + verticalInset,
	};

	computeLayout();
};

/**
Calculate the layout of the node tree, including grid containers, which Yoga doesn't support natively. Grid items are laid out as absolutely positioned children with sizes resolved from the grid tracks.
*/
export const calculateLayout = (rootNode: DOMElement): void => {
	const yogaNode = rootNode.yogaNode!;
	const computeLayout = () => {
		yogaNode.calculateLayout(undefined, undefined, Yoga.DIRECTION_LTR);
	};

	const containers: DOMElement[] = [];
	collectGridContainers(rootNode, containers);

	if (containers.length === 0) {
		computeLayout();
		return;
	}

	// Nested grids depend on each other's sizes, so a second pass lets outer grids use the inner grids' resolved sizes.
	const passes = containers.some(container => hasGridAncestor(container))
		? 2
		: 1;

	for (let pass = 0; pass < passes; pass++) {
		for (const container of containers) {
			layoutGridContainer(container, computeLayout);
		}
	}
};
