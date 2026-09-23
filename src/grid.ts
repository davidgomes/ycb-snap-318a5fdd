import Yoga, {type Node as YogaNode} from 'yoga-layout';
import applyStyles, {type Styles} from './styles.js';
import {type DOMElement, type DOMNode} from './dom.js';

type FixedMax = {readonly fr: number};

type Track =
	| {readonly kind: 'fixed'; readonly size: number}
	| {readonly kind: 'fr'; readonly fr: number}
	| {readonly kind: 'auto'}
	| {
			readonly kind: 'minmax';
			readonly min: number;
			readonly max: number | FixedMax;
	  };

type Placement = {
	readonly start: number;
	readonly end: number;
};

type GridItem = {
	readonly node: DOMElement;
	readonly column: Placement;
	readonly row: Placement;
};

const trackPattern =
	/minmax\(\s*(\d+(?:\.\d+)?)\s*,\s*(\d+(?:\.\d+)?)(fr)?\s*\)|(\d+(?:\.\d+)?)fr|auto|(\d+(?:\.\d+)?)/g;

const managedNodes = new WeakSet<YogaNode>();

const parseTracks = (template: string | undefined): Track[] => {
	if (!template) {
		return [];
	}

	const tracks: Track[] = [];

	for (const match of template.matchAll(trackPattern)) {
		if (match[1] !== undefined) {
			const min = Number(match[1]);
			const maxValue = Number(match[2]);
			tracks.push({
				kind: 'minmax',
				min,
				max: match[3] ? {fr: maxValue} : maxValue,
			});
			continue;
		}

		if (match[4] !== undefined) {
			tracks.push({kind: 'fr', fr: Number(match[4])});
			continue;
		}

		if (match[0] === 'auto') {
			tracks.push({kind: 'auto'});
			continue;
		}

		if (match[5] !== undefined) {
			tracks.push({kind: 'fixed', size: Number(match[5])});
		}
	}

	return tracks;
};

const parsePlacement = (
	value: number | string | undefined,
): Placement | undefined => {
	if (value === undefined) {
		return undefined;
	}

	if (typeof value === 'number') {
		if (!Number.isFinite(value) || value < 1) {
			return undefined;
		}

		return {start: Math.floor(value), end: Math.floor(value) + 1};
	}

	const parts = value.split('/').map(part => part.trim());
	const start = Number(parts[0]);

	if (!Number.isFinite(start) || start < 1) {
		return undefined;
	}

	if (parts.length === 1) {
		return {start, end: start + 1};
	}

	const end = Number(parts[1]);

	if (!Number.isFinite(end) || end <= start) {
		return {start, end: start + 1};
	}

	return {start, end};
};

const spanOf = (placement: Placement): number =>
	placement.end - placement.start;

const minimumSize = (track: Track, content: number): number => {
	switch (track.kind) {
		case 'fixed': {
			return track.size;
		}

		case 'fr': {
			return 0;
		}

		case 'auto': {
			return content;
		}

		case 'minmax': {
			return track.min;
		}
	}
};

const frWeight = (track: Track): number => {
	if (track.kind === 'fr') {
		return track.fr;
	}

	if (track.kind === 'minmax' && typeof track.max !== 'number') {
		return track.max.fr;
	}

	return 0;
};

const fixedGrowth = (track: Track, minimum: number): number => {
	if (track.kind === 'minmax' && typeof track.max === 'number') {
		return Math.max(0, track.max - minimum);
	}

	return 0;
};

const distribute = (total: number, weights: number[]): number[] => {
	const weightSum = weights.reduce((sum, weight) => sum + weight, 0);

	if (weightSum <= 0 || total <= 0) {
		return weights.map(() => 0);
	}

	return weights.map(weight => (total * weight) / weightSum);
};

const roundToSum = (sizes: number[], target: number): number[] => {
	const floors = sizes.map(size => Math.floor(size));
	let remainder =
		Math.round(target) - floors.reduce((sum, size) => sum + size, 0);
	const order = sizes
		.map((size, index) => ({index, fraction: size - Math.floor(size)}))
		.sort((a, b) => b.fraction - a.fraction);

	let cursor = 0;

	while (remainder > 0 && order.length > 0) {
		floors[order[cursor % order.length]!.index]! += 1;
		remainder -= 1;
		cursor += 1;
	}

	while (remainder < 0 && order.length > 0) {
		const {index} = order[cursor % order.length]!;

		if (floors[index]! > 0) {
			floors[index] -= 1;
			remainder += 1;
		}

		cursor += 1;

		if (cursor > order.length * 4) {
			break;
		}
	}

	return floors;
};

const resolveTrackSizes = (
	tracks: Track[],
	available: number | undefined,
	gap: number,
	contentSizes: number[],
): number[] => {
	if (available === undefined) {
		return tracks.map((track, index) => {
			const content = contentSizes[index] ?? 0;

			if (track.kind === 'fixed') {
				return track.size;
			}

			if (track.kind === 'minmax' && typeof track.max === 'number') {
				return Math.min(track.max, Math.max(track.min, content));
			}

			if (track.kind === 'minmax') {
				return Math.max(track.min, content);
			}

			return content;
		});
	}

	const gapSize = gap * Math.max(0, tracks.length - 1);
	const freeSpace = Math.max(0, available - gapSize);
	const minimums = tracks.map((track, index) =>
		minimumSize(track, contentSizes[index] ?? 0),
	);
	const sizes = [...minimums];
	let remaining = freeSpace - minimums.reduce((sum, size) => sum + size, 0);

	if (remaining > 0) {
		const growth = tracks.map((track, index) =>
			fixedGrowth(track, sizes[index] ?? 0),
		);
		const growthSum = growth.reduce((sum, size) => sum + size, 0);

		if (growthSum > 0) {
			const given = Math.min(remaining, growthSum);
			const shares = distribute(given, growth);

			for (const [index, share] of shares.entries()) {
				sizes[index] = (sizes[index] ?? 0) + (share ?? 0);
			}

			remaining -= given;
		}
	}

	if (remaining > 0) {
		const weights = tracks.map(track => frWeight(track));
		const shares = distribute(remaining, weights);

		for (const [index, share] of shares.entries()) {
			sizes[index] = (sizes[index] ?? 0) + (share ?? 0);
		}
	}

	if (remaining >= 0 && weightsHaveFr(tracks)) {
		return roundToSum(sizes, freeSpace);
	}

	return sizes.map(size => Math.round(size));
};

const weightsHaveFr = (tracks: Track[]): boolean =>
	tracks.some(track => frWeight(track) > 0);

const lengthValue = (value: {unit: number; value: number} | number): number => {
	if (typeof value === 'number') {
		return value;
	}

	// Yoga.Unit.Point. Compared as a number so the enum types can differ.
	return value.unit === 1 ? value.value : 0;
};

const horizontalMargin = (yogaNode: YogaNode): number =>
	lengthValue(yogaNode.getMargin(Yoga.EDGE_LEFT)) +
	lengthValue(yogaNode.getMargin(Yoga.EDGE_RIGHT));

const verticalMargin = (yogaNode: YogaNode): number =>
	lengthValue(yogaNode.getMargin(Yoga.EDGE_TOP)) +
	lengthValue(yogaNode.getMargin(Yoga.EDGE_BOTTOM));

const contentBox = (
	yogaNode: YogaNode,
	borderBoxWidth: number,
	borderBoxHeight: number,
) => {
	const paddingX =
		yogaNode.getComputedPadding(Yoga.EDGE_LEFT) +
		yogaNode.getComputedPadding(Yoga.EDGE_RIGHT);
	const paddingY =
		yogaNode.getComputedPadding(Yoga.EDGE_TOP) +
		yogaNode.getComputedPadding(Yoga.EDGE_BOTTOM);
	const borderX =
		yogaNode.getComputedBorder(Yoga.EDGE_LEFT) +
		yogaNode.getComputedBorder(Yoga.EDGE_RIGHT);
	const borderY =
		yogaNode.getComputedBorder(Yoga.EDGE_TOP) +
		yogaNode.getComputedBorder(Yoga.EDGE_BOTTOM);

	return {
		paddingLeft: yogaNode.getComputedPadding(Yoga.EDGE_LEFT),
		paddingTop: yogaNode.getComputedPadding(Yoga.EDGE_TOP),
		width: Math.max(0, borderBoxWidth - paddingX - borderX),
		height: Math.max(0, borderBoxHeight - paddingY - borderY),
		chromeX: paddingX + borderX,
		chromeY: paddingY + borderY,
	};
};

const isGridItem = (
	node: DOMNode,
): node is DOMElement & {yogaNode: YogaNode} => {
	if (node.nodeName === '#text' || node.nodeName === 'ink-virtual-text') {
		return false;
	}

	if (!node.yogaNode) {
		return false;
	}

	return node.yogaNode.getDisplay() !== Yoga.DISPLAY_NONE;
};

const gapsFor = (style: Styles): {columnGap: number; rowGap: number} => ({
	columnGap: style.columnGap ?? style.gap ?? 0,
	rowGap: style.rowGap ?? style.gap ?? 0,
});

const measureIntrinsicWidth = (yogaNode: YogaNode): number => {
	yogaNode.setWidth('auto');
	yogaNode.setHeight('auto');
	yogaNode.setPositionType(Yoga.POSITION_TYPE_RELATIVE);
	yogaNode.calculateLayout(undefined, undefined, Yoga.DIRECTION_LTR);
	return yogaNode.getComputedWidth();
};

const measureHeight = (yogaNode: YogaNode, width: number): number => {
	yogaNode.setWidth(width);
	yogaNode.setHeight('auto');
	yogaNode.calculateLayout(width, undefined, Yoga.DIRECTION_LTR);
	return yogaNode.getComputedHeight();
};

const occupy = (
	taken: Set<string>,
	column: Placement,
	row: Placement,
): void => {
	for (let y = row.start; y < row.end; y += 1) {
		for (let x = column.start; x < column.end; x += 1) {
			taken.add(`${x},${y}`);
		}
	}
};

type Fit = {
	taken: Set<string>;
	column: number;
	row: number;
	columnSpan: number;
	rowSpan: number;
	columnCount: number;
};

const fits = ({
	taken,
	column,
	row,
	columnSpan,
	rowSpan,
	columnCount,
}: Fit): boolean => {
	if (column < 1 || column + columnSpan - 1 > columnCount) {
		return false;
	}

	for (let y = row; y < row + rowSpan; y += 1) {
		for (let x = column; x < column + columnSpan; x += 1) {
			if (taken.has(`${x},${y}`)) {
				return false;
			}
		}
	}

	return true;
};

const placeItems = (
	children: DOMElement[],
	columns: Track[],
	rows: Track[],
): {items: GridItem[]; columns: Track[]; rows: Track[]} => {
	const columnTracks =
		columns.length > 0 ? [...columns] : [{kind: 'auto' as const}];
	const rowTracks = [...rows];
	const taken = new Set<string>();
	const items: GridItem[] = [];
	let cursorColumn = 1;
	let cursorRow = 1;

	const ensureColumns = (count: number) => {
		while (columnTracks.length < count) {
			columnTracks.push({kind: 'auto'});
		}
	};

	const ensureRows = (count: number) => {
		while (rowTracks.length < count) {
			rowTracks.push({kind: 'auto'});
		}
	};

	for (const child of children) {
		const columnPlacement = parsePlacement(child.style.gridColumn);
		const rowPlacement = parsePlacement(child.style.gridRow);
		const columnSpan = columnPlacement ? spanOf(columnPlacement) : 1;
		const rowSpan = rowPlacement ? spanOf(rowPlacement) : 1;
		let column = columnPlacement;
		let row = rowPlacement;

		if (column && row) {
			ensureColumns(column.end - 1);
			ensureRows(row.end - 1);
		} else if (column && !row) {
			ensureColumns(column.end - 1);
			let rowStart = 1;

			while (
				!fits({
					taken,
					column: column.start,
					row: rowStart,
					columnSpan,
					rowSpan,
					columnCount: columnTracks.length,
				})
			) {
				rowStart += 1;
			}

			row = {start: rowStart, end: rowStart + rowSpan};
			ensureRows(row.end - 1);
		} else if (!column && row) {
			ensureRows(row.end - 1);
			let columnStart = 1;
			ensureColumns(columnSpan);

			while (
				!fits({
					taken,
					column: columnStart,
					row: row.start,
					columnSpan,
					rowSpan,
					columnCount: Math.max(
						columnTracks.length,
						columnStart + columnSpan - 1,
					),
				})
			) {
				columnStart += 1;
				ensureColumns(columnStart + columnSpan - 1);
			}

			column = {start: columnStart, end: columnStart + columnSpan};
		} else {
			ensureColumns(Math.max(columnTracks.length, columnSpan));

			while (
				!fits({
					taken,
					column: cursorColumn,
					row: cursorRow,
					columnSpan,
					rowSpan,
					columnCount: columnTracks.length,
				})
			) {
				cursorColumn += 1;

				if (cursorColumn + columnSpan - 1 > columnTracks.length) {
					cursorColumn = 1;
					cursorRow += 1;
				}
			}

			column = {start: cursorColumn, end: cursorColumn + columnSpan};
			row = {start: cursorRow, end: cursorRow + rowSpan};
			ensureRows(row.end - 1);
			cursorColumn += columnSpan;

			if (cursorColumn > columnTracks.length) {
				cursorColumn = 1;
				cursorRow += 1;
			}
		}

		occupy(taken, column, row);
		items.push({node: child, column, row});
	}

	if (rowTracks.length === 0) {
		rowTracks.push({kind: 'auto'});
	}

	return {items, columns: columnTracks, rows: rowTracks};
};

const areaSize = (
	sizes: number[],
	placement: Placement,
	gap: number,
): number => {
	let size = 0;

	for (let index = placement.start - 1; index < placement.end - 1; index += 1) {
		size += sizes[index] ?? 0;
	}

	const span = spanOf(placement);
	return size + gap * Math.max(0, span - 1);
};

const offsetFor = (sizes: number[], start: number, gap: number): number => {
	let offset = 0;

	for (let index = 0; index < start - 1; index += 1) {
		offset += (sizes[index] ?? 0) + gap;
	}

	return offset;
};

const contentSizesFor = (
	tracks: Track[],
	items: GridItem[],
	axis: 'column' | 'row',
	itemSize: (item: GridItem) => number,
): number[] => {
	const sizes = tracks.map(() => 0);

	for (const item of items) {
		const placement = axis === 'column' ? item.column : item.row;

		if (spanOf(placement) !== 1) {
			continue;
		}

		const index = placement.start - 1;
		sizes[index] = Math.max(sizes[index] ?? 0, itemSize(item));
	}

	return sizes;
};

const growSpanToFit = (
	sizes: number[],
	tracks: Track[],
	placement: Placement,
	gapAndNeeded: {gap: number; needed: number},
): void => {
	const {gap, needed} = gapAndNeeded;
	const current = areaSize(sizes, placement, gap);

	if (needed <= current) {
		return;
	}

	const deficit = needed - current;

	for (
		let index = placement.end - 2;
		index >= placement.start - 1;
		index -= 1
	) {
		if (tracks[index]?.kind === 'auto' || tracks[index]?.kind === 'fr') {
			sizes[index] = (sizes[index] ?? 0) + deficit;
			return;
		}
	}

	const last = placement.end - 2;
	sizes[last] = (sizes[last] ?? 0) + deficit;
};

const positionTypeFor = (style: Styles) => {
	if (style.position === 'absolute') {
		return Yoga.POSITION_TYPE_ABSOLUTE;
	}

	if (style.position === 'static') {
		return Yoga.POSITION_TYPE_STATIC;
	}

	return Yoga.POSITION_TYPE_RELATIVE;
};

const releaseManagedChild = (child: DOMElement) => {
	if (!child.yogaNode || !managedNodes.has(child.yogaNode)) {
		return;
	}

	child.yogaNode.setWidth(undefined);
	child.yogaNode.setHeight(undefined);
	child.yogaNode.setPositionType(positionTypeFor(child.style));
	applyStyles(child.yogaNode, child.style);
	managedNodes.delete(child.yogaNode);
};

const neutralizeGridItems = (node: DOMElement): void => {
	for (const child of node.childNodes) {
		if (child.nodeName === '#text' || !child.yogaNode) {
			continue;
		}

		if (
			node.style.display === 'grid' &&
			child.yogaNode.getDisplay() !== Yoga.DISPLAY_NONE
		) {
			child.yogaNode.setPositionType(Yoga.POSITION_TYPE_ABSOLUTE);
			child.yogaNode.setWidth(0);
			child.yogaNode.setHeight(0);
			managedNodes.add(child.yogaNode);
		} else {
			releaseManagedChild(child);
		}

		neutralizeGridItems(child);
	}
};

const layoutGridNode = (node: DOMElement): void => {
	for (const child of node.childNodes) {
		if (child.nodeName === '#text') {
			continue;
		}

		if (node.style.display !== 'grid') {
			layoutGridNode(child);
		}
	}

	if (node.style.display !== 'grid' || !node.yogaNode) {
		return;
	}

	const {yogaNode} = node;
	const widthValue = yogaNode.getWidth();
	const heightValue = yogaNode.getHeight();
	const widthUnit = Number(widthValue.unit);
	const heightUnit = Number(heightValue.unit);
	const assignedWidth = widthUnit === 1 ? widthValue.value : undefined;
	const assignedHeight = heightUnit === 1 ? heightValue.value : undefined;
	const box = contentBox(
		yogaNode,
		assignedWidth ?? yogaNode.getComputedWidth(),
		assignedHeight ?? yogaNode.getComputedHeight(),
	);
	const widthIndefinite =
		node.style.width === undefined &&
		assignedWidth === undefined &&
		box.width <= 0;
	const heightIndefinite =
		node.style.height === undefined &&
		assignedHeight === undefined &&
		box.height <= 0;
	const {columnGap, rowGap} = gapsFor(node.style);
	const children = node.childNodes.filter(child => isGridItem(child));
	const placed = placeItems(
		children,
		parseTracks(node.style.gridTemplateColumns),
		parseTracks(node.style.gridTemplateRows),
	);

	const intrinsicWidths = new Map<DOMElement, number>();

	for (const item of placed.items) {
		intrinsicWidths.set(item.node, measureIntrinsicWidth(item.node.yogaNode));
	}

	const columnContent = contentSizesFor(
		placed.columns,
		placed.items,
		'column',
		item => Math.max(0, intrinsicWidths.get(item.node) ?? 0),
	);
	const columnSizes = resolveTrackSizes(
		placed.columns,
		widthIndefinite ? undefined : box.width,
		columnGap,
		columnContent,
	);

	const measuredHeights = new Map<DOMElement, number>();

	for (const item of placed.items) {
		const areaWidth = areaSize(columnSizes, item.column, columnGap);
		const marginX = horizontalMargin(item.node.yogaNode);
		const innerWidth = Math.max(0, areaWidth - marginX);
		measuredHeights.set(
			item.node,
			measureHeight(item.node.yogaNode, innerWidth),
		);
	}

	const rowContent = contentSizesFor(
		placed.rows,
		placed.items,
		'row',
		item => measuredHeights.get(item.node) ?? 0,
	);
	const rowSizes = resolveTrackSizes(
		placed.rows,
		heightIndefinite ? undefined : box.height,
		rowGap,
		rowContent,
	);

	for (const item of placed.items) {
		if (spanOf(item.row) === 1) {
			continue;
		}

		growSpanToFit(rowSizes, placed.rows, item.row, {
			gap: rowGap,
			needed: measuredHeights.get(item.node) ?? 0,
		});
	}

	const contentWidth =
		columnSizes.reduce((sum, size) => sum + size, 0) +
		columnGap * Math.max(0, columnSizes.length - 1);
	const contentHeight =
		rowSizes.reduce((sum, size) => sum + size, 0) +
		rowGap * Math.max(0, rowSizes.length - 1);

	if (widthIndefinite) {
		yogaNode.setWidth(contentWidth + box.chromeX);
	}

	if (heightIndefinite) {
		yogaNode.setHeight(contentHeight + box.chromeY);
	}

	for (const item of placed.items) {
		const areaWidth = areaSize(columnSizes, item.column, columnGap);
		const areaHeight = areaSize(rowSizes, item.row, rowGap);
		const marginX = horizontalMargin(item.node.yogaNode);
		const marginY = verticalMargin(item.node.yogaNode);
		const childWidth =
			typeof item.node.style.width === 'number'
				? item.node.style.width
				: Math.max(0, areaWidth - marginX);
		const childHeight =
			typeof item.node.style.height === 'number'
				? item.node.style.height
				: Math.max(0, areaHeight - marginY);

		item.node.yogaNode.setPositionType(Yoga.POSITION_TYPE_ABSOLUTE);
		item.node.yogaNode.setWidth(childWidth);
		item.node.yogaNode.setHeight(childHeight);
		item.node.yogaNode.setPosition(
			Yoga.EDGE_LEFT,
			box.paddingLeft + offsetFor(columnSizes, item.column.start, columnGap),
		);
		item.node.yogaNode.setPosition(
			Yoga.EDGE_TOP,
			box.paddingTop + offsetFor(rowSizes, item.row.start, rowGap),
		);
		managedNodes.add(item.node.yogaNode);
	}

	for (const child of node.childNodes) {
		if (child.nodeName === '#text') {
			continue;
		}

		layoutGridNode(child);
	}
};

const calculateDomLayout = (root: DOMElement): void => {
	const {yogaNode} = root;

	if (!yogaNode) {
		return;
	}

	neutralizeGridItems(root);
	yogaNode.calculateLayout(undefined, undefined, Yoga.DIRECTION_LTR);
	layoutGridNode(root);
	yogaNode.calculateLayout(undefined, undefined, Yoga.DIRECTION_LTR);
};

export default calculateDomLayout;
