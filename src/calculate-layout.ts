import Yoga, {type Node as YogaNode} from 'yoga-layout';
import {type DOMElement, type DOMNode} from './dom.js';
import {
	expandTracks,
	parseGridPlacement,
	parseGridTrackList,
	placeGridItems,
	resolveTrackSizes,
	spanSize,
	trackOffsets,
	type SpanConstraint,
} from './grid.js';
import {type Styles} from './styles.js';

type GridConstraints = {
	indefiniteWidth?: boolean;
	indefiniteHeight?: boolean;
};

type BoxEdges = {
	left: number;
	right: number;
	top: number;
	bottom: number;
};

const managedNodes = new WeakSet<DOMElement>();

const positionEdges = [
	['top', Yoga.EDGE_TOP],
	['right', Yoga.EDGE_RIGHT],
	['bottom', Yoga.EDGE_BOTTOM],
	['left', Yoga.EDGE_LEFT],
] as const;

const finite = (value: number): number => (Number.isFinite(value) ? value : 0);

const isElement = (node: DOMNode): node is DOMElement =>
	node.nodeName !== '#text' && Boolean(node.yogaNode);

const elementChildren = (node: DOMElement): DOMElement[] => {
	const children: DOMElement[] = [];
	for (const child of node.childNodes) {
		if (isElement(child)) {
			children.push(child);
		}
	}

	return children;
};

const isGridItem = (node: DOMElement): boolean => {
	if (!node.yogaNode) {
		return false;
	}

	if (node.style.position === 'absolute' || node.style.display === 'none') {
		return false;
	}

	return node.yogaNode.getDisplay() !== Yoga.DISPLAY_NONE;
};

const gridItems = (node: DOMElement): DOMElement[] =>
	elementChildren(node).filter(child => isGridItem(child));

const readGaps = (style: Styles): {columnGap: number; rowGap: number} => {
	const gap = style.gap ?? 0;
	return {
		columnGap: style.columnGap ?? gap,
		rowGap: style.rowGap ?? gap,
	};
};

const numericOffset = (value: number | string | undefined): number =>
	typeof value === 'number' && Number.isFinite(value) ? value : 0;

const applyAxisSize = (
	yogaNode: YogaNode,
	axis: 'width' | 'height',
	value: number | string | undefined,
) => {
	const setSize =
		axis === 'width'
			? yogaNode.setWidth.bind(yogaNode)
			: yogaNode.setHeight.bind(yogaNode);
	const setPercent =
		axis === 'width'
			? yogaNode.setWidthPercent.bind(yogaNode)
			: yogaNode.setHeightPercent.bind(yogaNode);
	const setAuto =
		axis === 'width'
			? yogaNode.setWidthAuto.bind(yogaNode)
			: yogaNode.setHeightAuto.bind(yogaNode);

	if (typeof value === 'number') {
		setSize(value);
		return;
	}

	if (typeof value === 'string') {
		setPercent(Number.parseInt(value, 10));
		return;
	}

	setAuto();
};

const restoreGeometry = (node: DOMElement) => {
	const {yogaNode} = node;
	if (!yogaNode) {
		return;
	}

	const {style} = node;
	if (style.position === 'absolute') {
		yogaNode.setPositionType(Yoga.POSITION_TYPE_ABSOLUTE);
	} else if (style.position === 'static') {
		yogaNode.setPositionType(Yoga.POSITION_TYPE_STATIC);
	} else {
		yogaNode.setPositionType(Yoga.POSITION_TYPE_RELATIVE);
	}

	for (const [property, edge] of positionEdges) {
		if (!(property in style) || style[property] === undefined) {
			yogaNode.setPosition(edge, undefined);
			continue;
		}

		const value = style[property];
		if (typeof value === 'string') {
			yogaNode.setPositionPercent(edge, Number.parseFloat(value));
			continue;
		}

		yogaNode.setPosition(edge, value);
	}

	applyAxisSize(yogaNode, 'width', style.width);
	applyAxisSize(yogaNode, 'height', style.height);
};

const restoreManaged = (node: DOMElement) => {
	if (managedNodes.has(node)) {
		restoreGeometry(node);
		managedNodes.delete(node);
	}

	for (const child of elementChildren(node)) {
		restoreManaged(child);
	}
};

const containsGrid = (node: DOMElement): boolean => {
	if (node.style.display === 'grid') {
		return true;
	}

	return elementChildren(node).some(child => containsGrid(child));
};

const detachGridItems = (node: DOMElement) => {
	if (node.style.display === 'grid') {
		for (const child of gridItems(node)) {
			child.yogaNode?.setPositionType(Yoga.POSITION_TYPE_ABSOLUTE);
			managedNodes.add(child);
		}
	}

	for (const child of elementChildren(node)) {
		detachGridItems(child);
	}
};

const edgesOf = (yogaNode: YogaNode): BoxEdges => ({
	left: finite(yogaNode.getComputedMargin(Yoga.EDGE_LEFT)),
	right: finite(yogaNode.getComputedMargin(Yoga.EDGE_RIGHT)),
	top: finite(yogaNode.getComputedMargin(Yoga.EDGE_TOP)),
	bottom: finite(yogaNode.getComputedMargin(Yoga.EDGE_BOTTOM)),
});

const resolveBorderBox = (
	value: number | string | undefined,
	area: number,
	marginStart: number,
	marginEnd: number,
): number => {
	if (typeof value === 'number') {
		return Math.max(0, Math.round(value));
	}

	if (typeof value === 'string') {
		const percent = Number.parseFloat(value);
		if (Number.isFinite(percent)) {
			return Math.max(0, Math.round((percent / 100) * area));
		}
	}

	return Math.max(0, Math.round(area - marginStart - marginEnd));
};

const layoutNestedGrids = (node: DOMElement, constraints: GridConstraints) => {
	for (const child of elementChildren(node)) {
		if (child.style.display === 'grid') {
			layoutGrid(child, constraints);
			continue;
		}

		layoutNestedGrids(child, constraints);
	}
};

const measureIntrinsic = (
	node: DOMElement,
): {width: number; height: number; margin: BoxEdges} => {
	const yogaNode = node.yogaNode!;
	if (node.style.display === 'grid') {
		layoutGrid(node, {indefiniteWidth: true, indefiniteHeight: true});
	} else {
		layoutNestedGrids(node, {indefiniteWidth: true, indefiniteHeight: true});
		yogaNode.calculateLayout(undefined, undefined, Yoga.DIRECTION_LTR);
	}

	return {
		width: finite(yogaNode.getComputedWidth()),
		height: finite(yogaNode.getComputedHeight()),
		margin: edgesOf(yogaNode),
	};
};

const measureHeight = (
	node: DOMElement,
	borderWidth: number,
): {height: number; margin: BoxEdges} => {
	const yogaNode = node.yogaNode!;
	yogaNode.setWidth(borderWidth);
	if (node.style.height === undefined) {
		yogaNode.setHeightAuto();
	}

	if (node.style.display === 'grid') {
		layoutGrid(node, {indefiniteHeight: true});
	} else {
		layoutNestedGrids(node, {indefiniteHeight: true});
		yogaNode.calculateLayout(borderWidth, undefined, Yoga.DIRECTION_LTR);
	}

	return {
		height: finite(yogaNode.getComputedHeight()),
		margin: edgesOf(yogaNode),
	};
};

const layoutGrid = (node: DOMElement, constraints: GridConstraints = {}) => {
	const {yogaNode} = node;
	if (!yogaNode || node.style.display !== 'grid') {
		return;
	}

	if (constraints.indefiniteWidth && node.style.width === undefined) {
		yogaNode.setWidthAuto();
	}

	if (constraints.indefiniteHeight && node.style.height === undefined) {
		yogaNode.setHeightAuto();
	}

	const paddingLeft = finite(yogaNode.getComputedPadding(Yoga.EDGE_LEFT));
	const paddingRight = finite(yogaNode.getComputedPadding(Yoga.EDGE_RIGHT));
	const paddingTop = finite(yogaNode.getComputedPadding(Yoga.EDGE_TOP));
	const paddingBottom = finite(yogaNode.getComputedPadding(Yoga.EDGE_BOTTOM));
	const borderLeft = finite(yogaNode.getComputedBorder(Yoga.EDGE_LEFT));
	const borderRight = finite(yogaNode.getComputedBorder(Yoga.EDGE_RIGHT));
	const borderTop = finite(yogaNode.getComputedBorder(Yoga.EDGE_TOP));
	const borderBottom = finite(yogaNode.getComputedBorder(Yoga.EDGE_BOTTOM));
	const horizontalChrome =
		paddingLeft + paddingRight + borderLeft + borderRight;
	const verticalChrome = paddingTop + paddingBottom + borderTop + borderBottom;

	const widthValue = yogaNode.getWidth();
	const heightValue = yogaNode.getHeight();
	let borderBoxWidth = finite(yogaNode.getComputedWidth());
	let borderBoxHeight = finite(yogaNode.getComputedHeight());
	if (
		!constraints.indefiniteWidth &&
		widthValue.unit === Yoga.UNIT_POINT &&
		Number.isFinite(widthValue.value)
	) {
		borderBoxWidth = widthValue.value;
	}

	if (
		!constraints.indefiniteHeight &&
		heightValue.unit === Yoga.UNIT_POINT &&
		Number.isFinite(heightValue.value)
	) {
		borderBoxHeight = heightValue.value;
	}

	let contentWidth = Math.max(0, Math.round(borderBoxWidth - horizontalChrome));
	let contentHeight = Math.max(0, Math.round(borderBoxHeight - verticalChrome));
	const widthDefinite =
		!constraints.indefiniteWidth &&
		(node.style.width !== undefined || contentWidth > 0);
	const heightDefinite =
		!constraints.indefiniteHeight &&
		(node.style.height !== undefined || contentHeight > 0);

	const items = gridItems(node);
	const {columnGap, rowGap} = readGaps(node.style);
	const explicitColumns = parseGridTrackList(node.style.gridTemplateColumns);
	const explicitRows = parseGridTrackList(node.style.gridTemplateRows);
	const {placements, columnCount, rowCount} = placeGridItems(
		items.map(item => ({
			column: parseGridPlacement(item.style.gridColumn),
			row: parseGridPlacement(item.style.gridRow),
		})),
		explicitColumns.length,
		explicitRows.length,
	);

	const intrinsic = items.map(item => measureIntrinsic(item));
	const columns = expandTracks(explicitColumns, columnCount);
	const columnContributions = Array.from({length: columns.length}, () => 0);
	const columnSpans: SpanConstraint[] = [];
	for (const [index, placement] of placements.entries()) {
		const marginBoxWidth =
			intrinsic[index]!.width +
			intrinsic[index]!.margin.left +
			intrinsic[index]!.margin.right;
		const start = placement.columnStart - 1;
		const end = placement.columnEnd - 1;
		if (end - start === 1) {
			columnContributions[start] = Math.max(
				columnContributions[start] ?? 0,
				marginBoxWidth,
			);
		} else {
			columnSpans.push({start, end, size: marginBoxWidth});
		}
	}

	const columnSizes = resolveTrackSizes(columns, {
		available: widthDefinite ? contentWidth : undefined,
		gap: columnGap,
		contributions: columnContributions,
		spans: columnSpans,
	});
	if (!widthDefinite) {
		contentWidth =
			columnSizes.reduce((sum, size) => sum + size, 0) +
			columnGap * Math.max(0, columnSizes.length - 1);
		yogaNode.setWidth(contentWidth + horizontalChrome);
		managedNodes.add(node);
	}

	const rowContributions = Array.from(
		{length: Math.max(rowCount, explicitRows.length)},
		() => 0,
	);
	const rowSpans: SpanConstraint[] = [];
	const measuredMargins: BoxEdges[] = [];
	for (const [index, item] of items.entries()) {
		const placement = placements[index]!;
		const areaWidth = spanSize(
			columnSizes,
			columnGap,
			placement.columnStart - 1,
			placement.columnEnd - 1,
		);
		const {margin} = intrinsic[index]!;
		const borderWidth = resolveBorderBox(
			item.style.width,
			areaWidth,
			margin.left,
			margin.right,
		);
		const measured = measureHeight(item, borderWidth);
		measuredMargins[index] = measured.margin;
		const marginBoxHeight =
			measured.height + measured.margin.top + measured.margin.bottom;
		const start = placement.rowStart - 1;
		const end = placement.rowEnd - 1;
		if (end - start === 1) {
			rowContributions[start] = Math.max(
				rowContributions[start] ?? 0,
				marginBoxHeight,
			);
		} else {
			rowSpans.push({start, end, size: marginBoxHeight});
		}
	}

	const rows = expandTracks(
		explicitRows,
		Math.max(rowCount, rowContributions.length),
	);
	while (rowContributions.length < rows.length) {
		rowContributions.push(0);
	}

	const rowSizes = resolveTrackSizes(rows, {
		available: heightDefinite ? contentHeight : undefined,
		gap: rowGap,
		contributions: rowContributions,
		spans: rowSpans,
	});
	if (!heightDefinite) {
		contentHeight =
			rowSizes.reduce((sum, size) => sum + size, 0) +
			rowGap * Math.max(0, rowSizes.length - 1);
		yogaNode.setHeight(contentHeight + verticalChrome);
		managedNodes.add(node);
	}

	const columnOffset = trackOffsets(columnSizes, columnGap);
	const rowOffset = trackOffsets(rowSizes, rowGap);
	for (const [index, item] of items.entries()) {
		const itemYoga = item.yogaNode;
		const placement = placements[index];
		if (!itemYoga || !placement) {
			continue;
		}

		const margin = measuredMargins[index] ?? intrinsic[index]!.margin;
		const areaWidth = spanSize(
			columnSizes,
			columnGap,
			placement.columnStart - 1,
			placement.columnEnd - 1,
		);
		const areaHeight = spanSize(
			rowSizes,
			rowGap,
			placement.rowStart - 1,
			placement.rowEnd - 1,
		);
		itemYoga.setWidth(
			resolveBorderBox(item.style.width, areaWidth, margin.left, margin.right),
		);
		itemYoga.setHeight(
			resolveBorderBox(
				item.style.height,
				areaHeight,
				margin.top,
				margin.bottom,
			),
		);
		itemYoga.setPositionType(Yoga.POSITION_TYPE_ABSOLUTE);
		itemYoga.setPosition(
			Yoga.EDGE_LEFT,
			paddingLeft +
				(columnOffset[placement.columnStart - 1] ?? 0) +
				numericOffset(item.style.left),
		);
		itemYoga.setPosition(
			Yoga.EDGE_TOP,
			paddingTop +
				(rowOffset[placement.rowStart - 1] ?? 0) +
				numericOffset(item.style.top),
		);
		itemYoga.setPosition(Yoga.EDGE_RIGHT, undefined);
		itemYoga.setPosition(Yoga.EDGE_BOTTOM, undefined);
		managedNodes.add(item);
	}

	if (yogaNode.getWidth().unit === Yoga.UNIT_POINT) {
		yogaNode.calculateLayout(undefined, undefined, Yoga.DIRECTION_LTR);
	}
};

const layoutElementGrids = (node: DOMElement) => {
	if (node.style.display === 'grid') {
		layoutGrid(node);
	}

	for (const child of elementChildren(node)) {
		layoutElementGrids(child);
	}
};

const calculateLayout = (rootNode: DOMElement): void => {
	restoreManaged(rootNode);

	if (!rootNode.yogaNode || !containsGrid(rootNode)) {
		rootNode.yogaNode?.calculateLayout(
			undefined,
			undefined,
			Yoga.DIRECTION_LTR,
		);
		return;
	}

	detachGridItems(rootNode);
	rootNode.yogaNode.calculateLayout(undefined, undefined, Yoga.DIRECTION_LTR);
	layoutElementGrids(rootNode);
	rootNode.yogaNode.calculateLayout(undefined, undefined, Yoga.DIRECTION_LTR);
};

export default calculateLayout;
