import Yoga, {type Node as YogaNode} from 'yoga-layout';
import {type DOMElement} from './dom.js';
import {type Styles} from './styles.js';

type Track =
	| {type: 'fixed'; size: number}
	| {type: 'fr'; fr: number}
	| {type: 'auto'}
	| {type: 'minmax'; min: number; maxFixed?: number; maxFr?: number};

type AxisPlacement = {
	start: number;
	end: number;
};

type PlacedItem = {
	node: DOMElement;
	columnStart: number;
	columnEnd: number;
	rowStart: number;
	rowEnd: number;
};

type GridBox = {
	width: number;
	height: number;
};

const managedContainers = new Set<DOMElement>();
const managedItems = new Set<DOMElement>();
const layingOut = new Set<DOMElement>();

const finite = (value: number): number => (Number.isFinite(value) ? value : 0);

const sum = (values: readonly number[]): number => {
	let total = 0;
	for (const value of values) {
		total += value;
	}

	return total;
};

const elementChildren = (node: DOMElement): DOMElement[] => {
	const children: DOMElement[] = [];
	for (const child of node.childNodes) {
		if (child.nodeName === '#text' || !child.yogaNode) {
			continue;
		}

		children.push(child);
	}

	return children;
};

const isHidden = (node: DOMElement): boolean =>
	node.yogaNode?.getDisplay() === Yoga.DISPLAY_NONE ||
	node.style.display === 'none';

const horizontalChrome = (yoga: YogaNode): number =>
	finite(yoga.getComputedPadding(Yoga.EDGE_LEFT)) +
	finite(yoga.getComputedPadding(Yoga.EDGE_RIGHT)) +
	finite(yoga.getComputedBorder(Yoga.EDGE_LEFT)) +
	finite(yoga.getComputedBorder(Yoga.EDGE_RIGHT));

const verticalChrome = (yoga: YogaNode): number =>
	finite(yoga.getComputedPadding(Yoga.EDGE_TOP)) +
	finite(yoga.getComputedPadding(Yoga.EDGE_BOTTOM)) +
	finite(yoga.getComputedBorder(Yoga.EDGE_TOP)) +
	finite(yoga.getComputedBorder(Yoga.EDGE_BOTTOM));

const horizontalMargins = (yoga: YogaNode): {left: number; right: number} => ({
	left: finite(yoga.getComputedMargin(Yoga.EDGE_LEFT)),
	right: finite(yoga.getComputedMargin(Yoga.EDGE_RIGHT)),
});

const verticalMargins = (yoga: YogaNode): {top: number; bottom: number} => ({
	top: finite(yoga.getComputedMargin(Yoga.EDGE_TOP)),
	bottom: finite(yoga.getComputedMargin(Yoga.EDGE_BOTTOM)),
});

const applyWidthFromStyle = (node: DOMElement): void => {
	const yoga = node.yogaNode;
	if (!yoga) {
		return;
	}

	const {width} = node.style;
	if (typeof width === 'number') {
		yoga.setWidth(width);
		return;
	}

	if (typeof width === 'string') {
		yoga.setWidthPercent(Number.parseFloat(width));
		return;
	}

	yoga.setWidthAuto();
};

const applyHeightFromStyle = (node: DOMElement): void => {
	const yoga = node.yogaNode;
	if (!yoga) {
		return;
	}

	const {height} = node.style;
	if (typeof height === 'number') {
		yoga.setHeight(height);
		return;
	}

	if (typeof height === 'string') {
		yoga.setHeightPercent(Number.parseFloat(height));
		return;
	}

	yoga.setHeightAuto();
};

const restoreFromStyle = (node: DOMElement): void => {
	const yoga = node.yogaNode;
	if (!yoga) {
		return;
	}

	const {position} = node.style;
	if (position === 'absolute') {
		yoga.setPositionType(Yoga.POSITION_TYPE_ABSOLUTE);
	} else if (position === 'static') {
		yoga.setPositionType(Yoga.POSITION_TYPE_STATIC);
	} else {
		yoga.setPositionType(Yoga.POSITION_TYPE_RELATIVE);
	}

	const edges = [
		['top', Yoga.EDGE_TOP],
		['right', Yoga.EDGE_RIGHT],
		['bottom', Yoga.EDGE_BOTTOM],
		['left', Yoga.EDGE_LEFT],
	] as const;

	for (const [property, edge] of edges) {
		const value = node.style[property];
		if (typeof value === 'number') {
			yoga.setPosition(edge, value);
		} else if (typeof value === 'string') {
			yoga.setPositionPercent(edge, Number.parseFloat(value));
		} else {
			yoga.setPosition(edge, undefined);
		}
	}

	applyWidthFromStyle(node);
	applyHeightFromStyle(node);
};

const styleLength = (
	value: number | string | undefined,
	basis: number,
): number | undefined => {
	if (typeof value === 'number' && Number.isFinite(value)) {
		return value;
	}

	if (typeof value === 'string' && value.endsWith('%')) {
		const percent = Number.parseFloat(value);
		if (Number.isFinite(percent)) {
			return (percent / 100) * basis;
		}
	}

	return undefined;
};

const tokenizeTracks = (input: string): string[] => {
	const tokens: string[] = [];
	let current = '';
	let depth = 0;

	for (const char of input) {
		if (char === '(') {
			depth++;
			current += char;
			continue;
		}

		if (char === ')') {
			depth = Math.max(0, depth - 1);
			current += char;
			continue;
		}

		if (depth === 0 && /\s/.test(char)) {
			if (current.length > 0) {
				tokens.push(current);
				current = '';
			}

			continue;
		}

		current += char;
	}

	if (current.length > 0) {
		tokens.push(current);
	}

	return tokens;
};

const parseTrack = (token: string): Track => {
	if (token === 'auto') {
		return {type: 'auto'};
	}

	if (/^\d+(?:\.\d+)?$/.test(token)) {
		return {type: 'fixed', size: Number(token)};
	}

	const fr = /^(\d+(?:\.\d+)?)fr$/.exec(token);
	if (fr?.[1]) {
		return {type: 'fr', fr: Number(fr[1])};
	}

	const compact = token.replaceAll(/\s+/g, '');
	const minmax = /^minmax\((\d+(?:\.\d+)?),(\d+(?:\.\d+)?)(fr)?\)$/.exec(
		compact,
	);
	if (minmax?.[1] && minmax[2]) {
		const min = Number(minmax[1]);
		const max = Number(minmax[2]);
		if (minmax[3]) {
			return {type: 'minmax', min, maxFr: max};
		}

		return {type: 'minmax', min, maxFixed: Math.max(min, max)};
	}

	throw new Error(
		`Invalid grid track size "${token}". Expected a number, fr unit, auto, or minmax(min, max).`,
	);
};

const parseTrackList = (input: string | undefined): Track[] | undefined => {
	if (input === undefined) {
		return undefined;
	}

	const tokens = tokenizeTracks(input);
	if (tokens.length === 0) {
		return undefined;
	}

	return tokens.map(token => parseTrack(token));
};

const parsePlacement = (
	value: Styles['gridColumn'],
): AxisPlacement | undefined => {
	if (value === undefined) {
		return undefined;
	}

	if (typeof value === 'number') {
		if (!Number.isFinite(value)) {
			return undefined;
		}

		const line = Math.max(1, Math.floor(value));
		return {start: line - 1, end: line};
	}

	const trimmed = value.trim();
	if (trimmed.length === 0) {
		return undefined;
	}

	if (trimmed.includes('/')) {
		const [rawStart, rawEnd] = trimmed.split('/');
		const startLine = Number.parseInt(rawStart?.trim() ?? '', 10);
		const endLine = Number.parseInt(rawEnd?.trim() ?? '', 10);
		if (!Number.isFinite(startLine) || !Number.isFinite(endLine)) {
			throw new TypeError(
				`Invalid grid placement "${value}". Expected "start / end".`,
			);
		}

		const start = Math.max(1, startLine);
		const end = Math.max(start + 1, endLine);
		return {start: start - 1, end: end - 1};
	}

	const line = Number.parseInt(trimmed, 10);
	if (!Number.isFinite(line)) {
		throw new TypeError(
			`Invalid grid placement "${value}". Expected a line index or "start / end".`,
		);
	}

	const startLine = Math.max(1, line);
	return {start: startLine - 1, end: startLine};
};

const distribute = (total: number, weights: readonly number[]): number[] => {
	const shares = Array.from({length: weights.length}, () => 0);
	const amount = Math.round(total);
	if (amount <= 0 || weights.length === 0) {
		return shares;
	}

	const weightSum = sum(weights);
	if (weightSum <= 0) {
		return shares;
	}

	const raw = weights.map(weight => (amount * weight) / weightSum);
	const floors = raw.map(value => Math.floor(value));
	let assigned = sum(floors);
	const ranked = raw
		.map((value, index) => ({
			index,
			fraction: value - Math.floor(value),
		}))
		.sort((a, b) => b.fraction - a.fraction || a.index - b.index);

	for (const entry of ranked) {
		if (assigned >= amount) {
			break;
		}

		floors[entry.index] = (floors[entry.index] ?? 0) + 1;
		assigned++;
	}

	return floors;
};

const shrinkSizes = (
	sizes: number[],
	floors: readonly number[],
	overflow: number,
): void => {
	if (overflow <= 0) {
		return;
	}

	const room = sizes.map((size, index) =>
		Math.max(0, size - (floors[index] ?? 0)),
	);
	const shares = distribute(Math.min(overflow, sum(room)), room);
	for (const [index, size] of sizes.entries()) {
		sizes[index] = Math.max(floors[index] ?? 0, size - (shares[index] ?? 0));
	}
};

const resolveTrackSizes = (
	tracks: readonly Track[],
	content: readonly number[],
	gap: number,
	available: number | undefined,
): number[] => {
	const bases = tracks.map((track, index) => {
		const contentSize = Math.max(0, Math.round(content[index] ?? 0));
		if (track.type === 'fixed') {
			return track.size;
		}

		if (track.type === 'fr') {
			return available === undefined ? contentSize : 0;
		}

		if (track.type === 'auto') {
			return contentSize;
		}

		if (track.maxFr !== undefined) {
			return available === undefined
				? Math.max(track.min, contentSize)
				: track.min;
		}

		const max = Math.max(track.min, track.maxFixed ?? track.min);
		return Math.min(max, Math.max(track.min, contentSize));
	});

	const floors = tracks.map(track => {
		if (track.type === 'fixed') {
			return track.size;
		}

		if (track.type === 'minmax') {
			return track.min;
		}

		return 0;
	});

	const flex = tracks.map(track => {
		if (track.type === 'fr') {
			return track.fr;
		}

		if (track.type === 'minmax' && track.maxFr !== undefined) {
			return track.maxFr;
		}

		return 0;
	});

	if (available === undefined) {
		return bases.map(size => Math.max(0, Math.round(size)));
	}

	const sizes = bases.map(size => Math.max(0, Math.round(size)));
	const target = Math.max(0, Math.round(available));
	const gapTotal = Math.round(gap) * Math.max(0, tracks.length - 1);
	const used = sum(sizes) + gapTotal;
	if (used > target) {
		shrinkSizes(sizes, floors, used - target);
		return sizes;
	}

	const shares = distribute(target - used, flex);
	for (const [index, share] of shares.entries()) {
		sizes[index] = (sizes[index] ?? 0) + share;
	}

	return sizes;
};

const spanContent = (
	tracks: readonly Track[],
	items: readonly PlacedItem[],
	contributions: ReadonlyMap<DOMElement, number>,
	options: {gap: number; axis: 'column' | 'row'},
): number[] => {
	const {gap, axis} = options;
	const content = Array.from({length: tracks.length}, () => 0);
	const startOf = (item: PlacedItem) =>
		axis === 'column' ? item.columnStart : item.rowStart;
	const endOf = (item: PlacedItem) =>
		axis === 'column' ? item.columnEnd : item.rowEnd;

	for (const item of items) {
		if (endOf(item) - startOf(item) !== 1) {
			continue;
		}

		const index = startOf(item);
		content[index] = Math.max(
			content[index] ?? 0,
			contributions.get(item.node) ?? 0,
		);
	}

	for (const item of items) {
		const start = startOf(item);
		const end = endOf(item);
		const span = end - start;
		if (span <= 1) {
			continue;
		}

		let current = gap * (span - 1);
		for (let index = start; index < end; index++) {
			current += content[index] ?? 0;
		}

		const extra = (contributions.get(item.node) ?? 0) - current;
		if (extra <= 0) {
			continue;
		}

		const indexes: number[] = [];
		for (let index = start; index < end; index++) {
			const track = tracks[index];
			if (!track || track.type === 'fixed') {
				continue;
			}

			if (
				track.type === 'minmax' &&
				track.maxFixed !== undefined &&
				(content[index] ?? 0) >= track.maxFixed
			) {
				continue;
			}

			indexes.push(index);
		}

		if (indexes.length === 0) {
			continue;
		}

		const shares = distribute(
			extra,
			indexes.map(() => 1),
		);
		for (const [offset, index] of indexes.entries()) {
			content[index] = (content[index] ?? 0) + (shares[offset] ?? 0);
		}
	}

	return content;
};

const materializeTracks = (
	template: Track[] | undefined,
	count: number,
): Track[] => {
	const tracks: Track[] = [];
	for (let index = 0; index < count; index++) {
		tracks.push(template?.[index] ?? {type: 'auto'});
	}

	return tracks;
};

const placeItems = (
	children: readonly DOMElement[],
	columnTemplateCount: number,
	rowTemplateCount: number,
): {items: PlacedItem[]; columnCount: number; rowCount: number} => {
	let columnCount = columnTemplateCount;
	let rowCount = rowTemplateCount;
	const occupancy = new Set<string>();
	const items: PlacedItem[] = [];

	const fits = (
		columnStart: number,
		columnEnd: number,
		rowStart: number,
		rowEnd: number,
	): boolean => {
		for (let column = columnStart; column < columnEnd; column++) {
			for (let row = rowStart; row < rowEnd; row++) {
				if (occupancy.has(`${column}:${row}`)) {
					return false;
				}
			}
		}

		return true;
	};

	const place = ({
		node,
		columnStart,
		columnEnd,
		rowStart,
		rowEnd,
	}: PlacedItem) => {
		for (let column = columnStart; column < columnEnd; column++) {
			for (let row = rowStart; row < rowEnd; row++) {
				occupancy.add(`${column}:${row}`);
			}
		}

		columnCount = Math.max(columnCount, columnEnd);
		rowCount = Math.max(rowCount, rowEnd);
		items.push({
			node,
			columnStart,
			columnEnd,
			rowStart,
			rowEnd,
		});
	};

	const findColumn = (
		rowStart: number,
		spanColumns: number,
		spanRows: number,
	): number => {
		if (spanColumns > columnCount) {
			columnCount = spanColumns;
		}

		for (let column = 0; column < 10_000; column++) {
			if (column + spanColumns > columnCount) {
				columnCount = column + spanColumns;
			}

			if (fits(column, column + spanColumns, rowStart, rowStart + spanRows)) {
				return column;
			}
		}

		return 0;
	};

	const findRow = (
		columnStart: number,
		spanColumns: number,
		spanRows: number,
	): number => {
		columnCount = Math.max(columnCount, columnStart + spanColumns);
		for (let row = 0; row < 100_000; row++) {
			if (fits(columnStart, columnStart + spanColumns, row, row + spanRows)) {
				return row;
			}
		}

		return 0;
	};

	const rowLocked: Array<{node: DOMElement; row: AxisPlacement}> = [];
	const columnLocked: Array<{node: DOMElement; column: AxisPlacement}> = [];
	const automatic: DOMElement[] = [];

	for (const child of children) {
		const column = parsePlacement(child.style.gridColumn);
		const row = parsePlacement(child.style.gridRow);
		if (column && row) {
			place({
				node: child,
				columnStart: column.start,
				columnEnd: column.end,
				rowStart: row.start,
				rowEnd: row.end,
			});
		} else if (row) {
			rowLocked.push({node: child, row});
		} else if (column) {
			columnLocked.push({node: child, column});
		} else {
			automatic.push(child);
		}
	}

	if (columnCount === 0) {
		columnCount = 1;
	}

	for (const item of rowLocked) {
		const spanRows = item.row.end - item.row.start;
		const columnStart = findColumn(item.row.start, 1, spanRows);
		place({
			node: item.node,
			columnStart,
			columnEnd: columnStart + 1,
			rowStart: item.row.start,
			rowEnd: item.row.end,
		});
	}

	for (const item of columnLocked) {
		const spanColumns = item.column.end - item.column.start;
		const rowStart = findRow(item.column.start, spanColumns, 1);
		place({
			node: item.node,
			columnStart: item.column.start,
			columnEnd: item.column.end,
			rowStart,
			rowEnd: rowStart + 1,
		});
	}

	let cursorColumn = 0;
	let cursorRow = 0;
	for (const node of automatic) {
		if (columnCount === 0) {
			columnCount = 1;
		}

		let column = cursorColumn;
		let row = cursorRow;
		for (let step = 0; step < 100_000; step++) {
			if (column + 1 <= columnCount && fits(column, column + 1, row, row + 1)) {
				break;
			}

			column++;
			if (column + 1 > columnCount) {
				column = 0;
				row++;
			}
		}

		place({
			node,
			columnStart: column,
			columnEnd: column + 1,
			rowStart: row,
			rowEnd: row + 1,
		});
		cursorColumn = column + 1;
		cursorRow = row;
		if (cursorColumn >= columnCount) {
			cursorColumn = 0;
			cursorRow = row + 1;
		}
	}

	return {items, columnCount, rowCount};
};

const trackOffset = (
	sizes: readonly number[],
	index: number,
	gap: number,
): number => {
	let offset = 0;
	for (let cursor = 0; cursor < index; cursor++) {
		offset += (sizes[cursor] ?? 0) + gap;
	}

	return offset;
};

const spanSize = (
	sizes: readonly number[],
	start: number,
	end: number,
	gap: number,
): number => {
	let size = 0;
	for (let index = start; index < end; index++) {
		size += sizes[index] ?? 0;
	}

	const span = end - start;
	if (span > 1) {
		size += gap * (span - 1);
	}

	return size;
};

const gapsFor = (style: Styles): {columnGap: number; rowGap: number} => ({
	columnGap: style.columnGap ?? style.gap ?? 0,
	rowGap: style.rowGap ?? style.gap ?? 0,
});

const borderBoxFromComputed = (
	node: DOMElement,
	axis: 'width' | 'height',
): number | undefined => {
	const yoga = node.yogaNode;
	if (!yoga) {
		return undefined;
	}

	if (axis === 'width' && node.style.width === undefined) {
		const content = yoga.getComputedWidth() - horizontalChrome(yoga);
		if (content <= 0) {
			return undefined;
		}
	}

	if (axis === 'height' && node.style.height === undefined) {
		return undefined;
	}

	return axis === 'width' ? yoga.getComputedWidth() : yoga.getComputedHeight();
};

function layoutDescendantGrids(node: DOMElement): void {
	for (const child of elementChildren(node)) {
		if (isHidden(child)) {
			continue;
		}

		if (child.style.display === 'grid') {
			applyGridLayout(child, borderBoxFromComputed(child, 'width'));
		} else {
			layoutDescendantGrids(child);
		}
	}
}

function measureIntrinsicWidth(node: DOMElement): number {
	const yoga = node.yogaNode;
	if (!yoga) {
		return 0;
	}

	applyWidthFromStyle(node);
	applyHeightFromStyle(node);

	if (node.style.display === 'grid') {
		return applyGridLayout(node).width;
	}

	yoga.calculateLayout(undefined, undefined, Yoga.DIRECTION_LTR);
	layoutDescendantGrids(node);
	yoga.calculateLayout(undefined, undefined, Yoga.DIRECTION_LTR);
	return finite(yoga.getComputedWidth());
}

function measureContentHeight(
	node: DOMElement,
	borderBoxWidth: number,
): number {
	const yoga = node.yogaNode;
	if (!yoga) {
		return 0;
	}

	yoga.setWidth(borderBoxWidth);
	if (node.style.display === 'grid') {
		return applyGridLayout(node, borderBoxWidth).height;
	}

	applyHeightFromStyle(node);
	yoga.calculateLayout(undefined, undefined, Yoga.DIRECTION_LTR);
	layoutDescendantGrids(node);
	yoga.calculateLayout(undefined, undefined, Yoga.DIRECTION_LTR);
	return finite(yoga.getComputedHeight());
}

function applyGridLayout(
	node: DOMElement,
	forcedWidth?: number,
	forcedHeight?: number,
): GridBox {
	const yoga = node.yogaNode;
	if (!yoga || layingOut.has(node)) {
		return {
			width: finite(node.yogaNode?.getComputedWidth() ?? 0),
			height: finite(node.yogaNode?.getComputedHeight() ?? 0),
		};
	}

	layingOut.add(node);
	try {
		const {columnGap, rowGap} = gapsFor(node.style);
		const columnTemplate = parseTrackList(node.style.gridTemplateColumns);
		const rowTemplate = parseTrackList(node.style.gridTemplateRows);
		const children = elementChildren(node).filter(child => !isHidden(child));
		const placement = placeItems(
			children,
			columnTemplate?.length ?? 0,
			rowTemplate?.length ?? 0,
		);
		const columns = materializeTracks(columnTemplate, placement.columnCount);
		const rows = materializeTracks(rowTemplate, placement.rowCount);

		const widthContributions = new Map<DOMElement, number>();
		for (const item of placement.items) {
			const width = measureIntrinsicWidth(item.node);
			const margins = horizontalMargins(item.node.yogaNode!);
			widthContributions.set(item.node, width + margins.left + margins.right);
		}

		const definiteWidth = forcedWidth ?? borderBoxFromComputed(node, 'width');
		const availableWidth =
			definiteWidth === undefined
				? undefined
				: Math.max(0, definiteWidth - horizontalChrome(yoga));
		const columnSizes = resolveTrackSizes(
			columns,
			spanContent(columns, placement.items, widthContributions, {
				gap: columnGap,
				axis: 'column',
			}),
			columnGap,
			availableWidth,
		);

		const heightContributions = new Map<DOMElement, number>();
		for (const item of placement.items) {
			const childYoga = item.node.yogaNode!;
			const margins = horizontalMargins(childYoga);
			const areaWidth = spanSize(
				columnSizes,
				item.columnStart,
				item.columnEnd,
				columnGap,
			);
			const explicitWidth = styleLength(item.node.style.width, areaWidth);
			const borderBoxWidth =
				explicitWidth === undefined
					? Math.max(0, areaWidth - margins.left - margins.right)
					: Math.max(0, explicitWidth);
			const height = measureContentHeight(item.node, borderBoxWidth);
			const vertical = verticalMargins(childYoga);
			heightContributions.set(
				item.node,
				height + vertical.top + vertical.bottom,
			);
		}

		const definiteHeight =
			forcedHeight ?? borderBoxFromComputed(node, 'height');
		const availableHeight =
			definiteHeight === undefined
				? undefined
				: Math.max(0, definiteHeight - verticalChrome(yoga));
		const rowSizes = resolveTrackSizes(
			rows,
			spanContent(rows, placement.items, heightContributions, {
				gap: rowGap,
				axis: 'row',
			}),
			rowGap,
			availableHeight,
		);

		const paddingLeft = finite(yoga.getComputedPadding(Yoga.EDGE_LEFT));
		const paddingTop = finite(yoga.getComputedPadding(Yoga.EDGE_TOP));

		for (const item of placement.items) {
			const childYoga = item.node.yogaNode!;
			const horizontal = horizontalMargins(childYoga);
			const vertical = verticalMargins(childYoga);
			const areaWidth = spanSize(
				columnSizes,
				item.columnStart,
				item.columnEnd,
				columnGap,
			);
			const areaHeight = spanSize(rowSizes, item.rowStart, item.rowEnd, rowGap);
			const explicitWidth = styleLength(item.node.style.width, areaWidth);
			const explicitHeight = styleLength(item.node.style.height, areaHeight);
			const borderBoxWidth =
				explicitWidth === undefined
					? Math.max(0, areaWidth - horizontal.left - horizontal.right)
					: Math.max(0, explicitWidth);
			const borderBoxHeight =
				explicitHeight === undefined
					? Math.max(0, areaHeight - vertical.top - vertical.bottom)
					: Math.max(0, explicitHeight);

			childYoga.setPositionType(Yoga.POSITION_TYPE_ABSOLUTE);
			childYoga.setPosition(
				Yoga.EDGE_LEFT,
				paddingLeft + trackOffset(columnSizes, item.columnStart, columnGap),
			);
			childYoga.setPosition(
				Yoga.EDGE_TOP,
				paddingTop + trackOffset(rowSizes, item.rowStart, rowGap),
			);
			childYoga.setPosition(Yoga.EDGE_RIGHT, undefined);
			childYoga.setPosition(Yoga.EDGE_BOTTOM, undefined);
			childYoga.setWidth(borderBoxWidth);
			childYoga.setHeight(borderBoxHeight);
			managedItems.add(item.node);

			if (item.node.style.display === 'grid') {
				applyGridLayout(item.node, borderBoxWidth, borderBoxHeight);
			}
		}

		const contentWidth =
			sum(columnSizes) + columnGap * Math.max(0, columnSizes.length - 1);
		const contentHeight =
			sum(rowSizes) + rowGap * Math.max(0, rowSizes.length - 1);
		let borderBoxWidth: number;
		let borderBoxHeight: number;
		let wroteSize = false;

		if (forcedWidth !== undefined) {
			borderBoxWidth = forcedWidth;
			yoga.setWidth(forcedWidth);
			wroteSize = node.style.width === undefined;
		} else if (availableWidth === undefined) {
			borderBoxWidth = contentWidth + horizontalChrome(yoga);
			yoga.setWidth(borderBoxWidth);
			wroteSize = true;
		} else {
			borderBoxWidth = finite(yoga.getComputedWidth());
		}

		if (forcedHeight !== undefined) {
			borderBoxHeight = forcedHeight;
			yoga.setHeight(forcedHeight);
			wroteSize = true;
		} else if (node.style.height === undefined) {
			borderBoxHeight = contentHeight + verticalChrome(yoga);
			yoga.setHeight(borderBoxHeight);
			wroteSize = true;
		} else {
			borderBoxHeight = finite(yoga.getComputedHeight());
		}

		if (wroteSize) {
			managedContainers.add(node);
		}

		return {width: borderBoxWidth, height: borderBoxHeight};
	} finally {
		layingOut.delete(node);
	}
}

const pinGridItems = (node: DOMElement): boolean => {
	if (isHidden(node)) {
		return false;
	}

	let found = false;
	if (node.style.display === 'grid') {
		found = true;
		for (const child of elementChildren(node)) {
			if (!child.yogaNode || isHidden(child)) {
				continue;
			}

			child.yogaNode.setPositionType(Yoga.POSITION_TYPE_ABSOLUTE);
			managedItems.add(child);
		}
	}

	for (const child of elementChildren(node)) {
		if (pinGridItems(child)) {
			found = true;
		}
	}

	return found;
};

const unlockManagedContainers = (): void => {
	for (const node of managedContainers) {
		applyWidthFromStyle(node);
		applyHeightFromStyle(node);
	}

	managedContainers.clear();
};

const restoreStaleGridItems = (): void => {
	for (const node of managedItems) {
		if (node.parentNode?.style.display === 'grid' && !isHidden(node)) {
			continue;
		}

		restoreFromStyle(node);
		managedItems.delete(node);
	}
};

const layoutGrids = (node: DOMElement): void => {
	if (isHidden(node)) {
		return;
	}

	if (node.style.display === 'grid' && node.yogaNode) {
		applyGridLayout(node);
		return;
	}

	for (const child of elementChildren(node)) {
		layoutGrids(child);
	}
};

export const calculateLayout = (rootNode: DOMElement, width?: number): void => {
	const yoga = rootNode.yogaNode;
	if (!yoga) {
		return;
	}

	if (width !== undefined) {
		yoga.setWidth(width);
	}

	unlockManagedContainers();
	const foundGrid = pinGridItems(rootNode);
	restoreStaleGridItems();

	yoga.calculateLayout(undefined, undefined, Yoga.DIRECTION_LTR);

	if (!foundGrid) {
		return;
	}

	layoutGrids(rootNode);
	yoga.calculateLayout(undefined, undefined, Yoga.DIRECTION_LTR);
};
