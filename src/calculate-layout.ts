import Yoga, {type Edge, type Node as YogaNode} from 'yoga-layout';
import {type DOMElement, type DOMNode} from './dom.js';
import {
	ensureTrackCount,
	parseGridPlacement,
	parseTrackList,
	placeGridItems,
	resolveTrackSizes,
	spanSize,
	trackOffsets,
	tracksContentSize,
	type GridPlacement,
	type TrackSize,
} from './grid.js';

type SizeOverride = {
	width: boolean;
	height: boolean;
	position: boolean;
};

type Edges = {
	top: number;
	right: number;
	bottom: number;
	left: number;
};

const overrides = new WeakMap<DOMElement, SizeOverride>();

const isElement = (node: DOMNode): node is DOMElement =>
	node.nodeName !== '#text';

const overrideFor = (node: DOMElement): SizeOverride => {
	const existing = overrides.get(node);
	if (existing) {
		return existing;
	}

	const created: SizeOverride = {
		width: false,
		height: false,
		position: false,
	};
	overrides.set(node, created);
	return created;
};

const finite = (value: number): number => (Number.isFinite(value) ? value : 0);

const edgesOf = (read: (edge: Edge) => number): Edges => ({
	top: finite(read(Yoga.EDGE_TOP)),
	right: finite(read(Yoga.EDGE_RIGHT)),
	bottom: finite(read(Yoga.EDGE_BOTTOM)),
	left: finite(read(Yoga.EDGE_LEFT)),
});

const paddingOf = (node: YogaNode): Edges =>
	edgesOf(edge => node.getComputedPadding(edge));

const borderOf = (node: YogaNode): Edges =>
	edgesOf(edge => node.getComputedBorder(edge));

const marginOf = (node: YogaNode): Edges =>
	edgesOf(edge => node.getComputedMargin(edge));

const horizontalChrome = (padding: Edges, border: Edges): number =>
	padding.left + padding.right + border.left + border.right;

const verticalChrome = (padding: Edges, border: Edges): number =>
	padding.top + padding.bottom + border.top + border.bottom;

const restorePosition = (node: DOMElement) => {
	const yoga = node.yogaNode;
	if (!yoga) {
		return;
	}

	const {position, top, right, bottom, left} = node.style;

	if (position === 'absolute') {
		yoga.setPositionType(Yoga.POSITION_TYPE_ABSOLUTE);
	} else if (position === 'static') {
		yoga.setPositionType(Yoga.POSITION_TYPE_STATIC);
	} else {
		yoga.setPositionType(Yoga.POSITION_TYPE_RELATIVE);
	}

	const restoreEdge = (edge: Edge, value: number | string | undefined) => {
		if (typeof value === 'number') {
			yoga.setPosition(edge, value);
			return;
		}

		if (typeof value === 'string') {
			yoga.setPositionPercent(edge, Number.parseFloat(value));
			return;
		}

		yoga.setPosition(edge, undefined);
	};

	restoreEdge(Yoga.EDGE_TOP, top);
	restoreEdge(Yoga.EDGE_RIGHT, right);
	restoreEdge(Yoga.EDGE_BOTTOM, bottom);
	restoreEdge(Yoga.EDGE_LEFT, left);
};

const clearSizeOverride = (node: DOMElement) => {
	const flags = overrides.get(node);
	const yoga = node.yogaNode;
	if (!flags || !yoga) {
		return;
	}

	if (flags.width && node.style.width === undefined) {
		yoga.setWidthAuto();
	}

	if (flags.height && node.style.height === undefined) {
		yoga.setHeightAuto();
	}

	flags.width = false;
	flags.height = false;
};

const prepareGrid = (node: DOMElement) => {
	clearSizeOverride(node);

	for (const child of node.childNodes) {
		if (!isElement(child)) {
			continue;
		}

		const flags = overrides.get(child);
		const inGrid =
			node.style.display === 'grid' &&
			child.style.display !== 'none' &&
			Boolean(child.yogaNode);

		if (inGrid) {
			child.yogaNode?.setPositionType(Yoga.POSITION_TYPE_ABSOLUTE);
			overrideFor(child).position = true;
		} else if (flags?.position) {
			restorePosition(child);
			flags.position = false;
		}

		prepareGrid(child);
	}
};

const applyAxisSize = (
	node: DOMElement,
	axis: 'width' | 'height',
	size: number | undefined,
) => {
	const yoga = node.yogaNode;
	if (!yoga) {
		return;
	}

	const styleValue = node.style[axis];
	if (typeof styleValue === 'number' || typeof styleValue === 'string') {
		return;
	}

	if (size === undefined) {
		if (axis === 'width') {
			yoga.setWidthAuto();
		} else {
			yoga.setHeightAuto();
		}

		return;
	}

	if (axis === 'width') {
		yoga.setWidth(size);
	} else {
		yoga.setHeight(size);
	}

	overrideFor(node)[axis] = true;
};

const gapFor = (node: DOMElement, axis: 'columnGap' | 'rowGap'): number => {
	const specific = node.style[axis];
	if (typeof specific === 'number') {
		return specific;
	}

	return node.style.gap ?? 0;
};

const definiteBorderSize = (
	node: DOMElement,
	axis: 'width' | 'height',
): number | undefined => {
	const yoga = node.yogaNode;
	if (!yoga) {
		return undefined;
	}

	const styleValue = node.style[axis];
	if (typeof styleValue === 'number') {
		return styleValue;
	}

	const computed =
		axis === 'width' ? yoga.getComputedWidth() : yoga.getComputedHeight();
	if (typeof styleValue === 'string') {
		return computed > 0 ? computed : undefined;
	}

	const padding = paddingOf(yoga);
	const border = borderOf(yoga);
	const chrome =
		axis === 'width'
			? horizontalChrome(padding, border)
			: verticalChrome(padding, border);

	if (computed - chrome > 0.5) {
		return computed;
	}

	return undefined;
};

const layoutNestedGrids = (node: DOMElement): boolean => {
	let changed = false;

	for (const child of node.childNodes) {
		if (
			!isElement(child) ||
			child.style.display === 'none' ||
			!child.yogaNode
		) {
			continue;
		}

		if (child.style.display === 'grid') {
			layoutGrid(child);
			changed = true;
			continue;
		}

		if (layoutNestedGrids(child)) {
			changed = true;
		}
	}

	return changed;
};

const sizeElement = (
	node: DOMElement,
	width: number | undefined,
	height: number | undefined,
): {width: number; height: number} => {
	if (!node.yogaNode) {
		return {width: 0, height: 0};
	}

	if (node.style.display === 'grid') {
		return layoutGrid(node, {width, height});
	}

	applyAxisSize(node, 'width', width);
	applyAxisSize(node, 'height', height);
	node.yogaNode.calculateLayout(undefined, undefined, Yoga.DIRECTION_LTR);

	if (layoutNestedGrids(node)) {
		node.yogaNode.calculateLayout(undefined, undefined, Yoga.DIRECTION_LTR);
	}

	return {
		width: node.yogaNode.getComputedWidth(),
		height: node.yogaNode.getComputedHeight(),
	};
};

const contentBox = (
	borderBox: number | undefined,
	chrome: number,
): number | undefined => {
	if (borderBox === undefined) {
		return undefined;
	}

	return Math.max(0, borderBox - chrome);
};

const placeChildren = (
	children: DOMElement[],
	columnTemplate: readonly TrackSize[],
	rowTemplate: string | undefined,
) => {
	const explicitRows = parseTrackList(rowTemplate);
	const inputs = children.map(child => ({
		column: parseGridPlacement(child.style.gridColumn),
		row: parseGridPlacement(child.style.gridRow),
	}));
	const placed = placeGridItems(
		inputs,
		columnTemplate.length,
		explicitRows.length,
	);

	return {
		placements: placed.placements,
		columns: ensureTrackCount(columnTemplate, placed.columnCount),
		rows: ensureTrackCount(
			rowTemplate === undefined ? [] : explicitRows,
			placed.rowCount,
		),
	};
};

const layoutGrid = (
	node: DOMElement,
	constraint?: {width?: number; height?: number},
): {width: number; height: number} => {
	const yoga = node.yogaNode;
	if (!yoga) {
		return {width: 0, height: 0};
	}

	const padding = paddingOf(yoga);
	const border = borderOf(yoga);
	// A provided constraint, even with undefined axes, means the caller is
	// measuring this grid. Only an omitted constraint falls back to the size
	// Yoga already resolved (stretch, percentage, or an explicit size).
	const borderBoxWidth = constraint
		? constraint.width
		: definiteBorderSize(node, 'width');
	const borderBoxHeight = constraint
		? constraint.height
		: definiteBorderSize(node, 'height');
	const innerWidth = contentBox(
		borderBoxWidth,
		horizontalChrome(padding, border),
	);
	const innerHeight = contentBox(
		borderBoxHeight,
		verticalChrome(padding, border),
	);

	const children = node.childNodes.filter(
		(child): child is DOMElement =>
			isElement(child) &&
			child.style.display !== 'none' &&
			Boolean(child.yogaNode),
	);

	const columnGap = gapFor(node, 'columnGap');
	const rowGap = gapFor(node, 'rowGap');
	const {placements, columns, rows} = placeChildren(
		children,
		parseTrackList(node.style.gridTemplateColumns),
		node.style.gridTemplateRows,
	);

	const intrinsicWidths = children.map(child => {
		return sizeElement(child, undefined, undefined).width;
	});

	const columnWidths = resolveTrackSizes(
		columns,
		placements.map((placement, index) => ({
			start: placement.columnStart,
			end: placement.columnEnd,
			content: (intrinsicWidths[index] ?? 0) + itemMargin(children[index], 'x'),
		})),
		columnGap,
		innerWidth,
	);

	const contentHeights = children.map((child, index) => {
		const placement = placements[index];
		const areaWidth = placement
			? spanSize(
					columnWidths,
					columnGap,
					placement.columnStart,
					placement.columnEnd,
				)
			: 0;
		const margin = itemMargin(child, 'x');
		const measured = sizeElement(
			child,
			Math.max(0, areaWidth - margin),
			undefined,
		);
		return measured.height + itemMargin(child, 'y');
	});

	const rowHeights = resolveTrackSizes(
		rows,
		placements.map((placement, index) => ({
			start: placement.rowStart,
			end: placement.rowEnd,
			content: contentHeights[index] ?? 0,
		})),
		rowGap,
		innerHeight,
	);

	const columnPositions = trackOffsets(columnWidths, columnGap);
	const rowPositions = trackOffsets(rowHeights, rowGap);

	for (const [index, child] of children.entries()) {
		const placement = placements[index];
		if (!placement || !child.yogaNode) {
			continue;
		}

		placeItem({
			child,
			placement,
			columnWidths,
			rowHeights,
			columnPositions,
			rowPositions,
			columnGap,
			rowGap,
			padding,
		});
	}

	const contentWidth = tracksContentSize(columnWidths, columnGap);
	const contentHeight = tracksContentSize(rowHeights, rowGap);
	const resolvedWidth =
		borderBoxWidth ?? contentWidth + horizontalChrome(padding, border);
	const resolvedHeight =
		borderBoxHeight ?? contentHeight + verticalChrome(padding, border);

	applyAxisSize(node, 'width', resolvedWidth);
	applyAxisSize(node, 'height', resolvedHeight);

	return {
		width: resolvedWidth,
		height: resolvedHeight,
	};
};

const itemMargin = (node: DOMElement | undefined, axis: 'x' | 'y'): number => {
	if (!node?.yogaNode) {
		return 0;
	}

	const margin = marginOf(node.yogaNode);
	return axis === 'x' ? margin.left + margin.right : margin.top + margin.bottom;
};

const placeItem = ({
	child,
	placement,
	columnWidths,
	rowHeights,
	columnPositions,
	rowPositions,
	columnGap,
	rowGap,
	padding,
}: {
	child: DOMElement;
	placement: GridPlacement;
	columnWidths: readonly number[];
	rowHeights: readonly number[];
	columnPositions: readonly number[];
	rowPositions: readonly number[];
	columnGap: number;
	rowGap: number;
	padding: Edges;
}) => {
	const yoga = child.yogaNode;
	if (!yoga) {
		return;
	}

	const margin = marginOf(yoga);
	const areaWidth = spanSize(
		columnWidths,
		columnGap,
		placement.columnStart,
		placement.columnEnd,
	);
	const areaHeight = spanSize(
		rowHeights,
		rowGap,
		placement.rowStart,
		placement.rowEnd,
	);
	const borderWidth = Math.max(0, areaWidth - margin.left - margin.right);
	const borderHeight = Math.max(0, areaHeight - margin.top - margin.bottom);

	if (child.style.display === 'grid') {
		layoutGrid(child, {width: borderWidth, height: borderHeight});
	} else {
		applyAxisSize(
			child,
			'width',
			child.style.width === undefined ? borderWidth : undefined,
		);
		applyAxisSize(
			child,
			'height',
			child.style.height === undefined ? borderHeight : undefined,
		);
		yoga.calculateLayout(undefined, undefined, Yoga.DIRECTION_LTR);
		if (layoutNestedGrids(child)) {
			yoga.calculateLayout(undefined, undefined, Yoga.DIRECTION_LTR);
		}
	}

	yoga.setPositionType(Yoga.POSITION_TYPE_ABSOLUTE);
	yoga.setPosition(
		Yoga.EDGE_LEFT,
		padding.left + (columnPositions[placement.columnStart] ?? 0),
	);
	yoga.setPosition(
		Yoga.EDGE_TOP,
		padding.top + (rowPositions[placement.rowStart] ?? 0),
	);
	yoga.setPositionAuto(Yoga.EDGE_RIGHT);
	yoga.setPositionAuto(Yoga.EDGE_BOTTOM);
	overrideFor(child).position = true;
};

const layoutOutermostGrids = (node: DOMElement) => {
	if (node.style.display === 'grid') {
		layoutGrid(node);
		return;
	}

	for (const child of node.childNodes) {
		if (!isElement(child) || child.style.display === 'none') {
			continue;
		}

		layoutOutermostGrids(child);
	}
};

const calculateLayout = (root: DOMElement): void => {
	const yoga = root.yogaNode;
	if (!yoga) {
		return;
	}

	prepareGrid(root);
	yoga.calculateLayout(undefined, undefined, Yoga.DIRECTION_LTR);
	layoutOutermostGrids(root);
	yoga.calculateLayout(undefined, undefined, Yoga.DIRECTION_LTR);
};

export default calculateLayout;
