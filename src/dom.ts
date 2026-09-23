import Yoga, {type Node as YogaNode} from 'yoga-layout';
import measureText from './measure-text.js';
import {type Styles} from './styles.js';
import wrapText from './wrap-text.js';
import squashTextNodes from './squash-text-nodes.js';
import {type OutputTransformer} from './render-node-to-output.js';
import {createGridMeasureFunc, isGridContainer} from './grid.js';

type InkNode = {
	parentNode: DOMElement | undefined;
	yogaNode?: YogaNode;
	internal_static?: boolean;
	style: Styles;

	// Position of a grid item's cell within its grid container. Grid items are separate Yoga roots, so Yoga doesn't include it in their computed position.
	internal_gridOffset?: {x: number; y: number};
};

type LayoutListener = () => void;

export type TextName = '#text';
export type ElementNames =
	| 'ink-root'
	| 'ink-box'
	| 'ink-text'
	| 'ink-virtual-text';

export type NodeNames = ElementNames | TextName;

// eslint-disable-next-line @typescript-eslint/naming-convention
export type DOMElement = {
	nodeName: ElementNames;
	attributes: Record<string, DOMNodeAttribute>;
	childNodes: DOMNode[];
	internal_transform?: OutputTransformer;

	internal_accessibility?: {
		role?:
			| 'button'
			| 'checkbox'
			| 'combobox'
			| 'list'
			| 'listbox'
			| 'listitem'
			| 'menu'
			| 'menuitem'
			| 'option'
			| 'progressbar'
			| 'radio'
			| 'radiogroup'
			| 'tab'
			| 'tablist'
			| 'table'
			| 'textbox'
			| 'timer'
			| 'toolbar';
		state?: {
			busy?: boolean;
			checked?: boolean;
			disabled?: boolean;
			expanded?: boolean;
			multiline?: boolean;
			multiselectable?: boolean;
			readonly?: boolean;
			required?: boolean;
			selected?: boolean;
		};
	};

	// Internal properties
	isStaticDirty?: boolean;
	staticNode?: DOMElement;
	onComputeLayout?: () => void;
	onRender?: () => void;
	onImmediateRender?: () => void;
	internal_layoutListeners?: Set<LayoutListener>;
} & InkNode;

export type TextNode = {
	nodeName: TextName;
	nodeValue: string;
} & InkNode;

// eslint-disable-next-line @typescript-eslint/naming-convention
export type DOMNode<T = {nodeName: NodeNames}> = T extends {
	nodeName: infer U;
}
	? U extends '#text'
		? TextNode
		: DOMElement
	: never;

// eslint-disable-next-line @typescript-eslint/naming-convention
export type DOMNodeAttribute = boolean | string | number;

export const createNode = (nodeName: ElementNames): DOMElement => {
	const node: DOMElement = {
		nodeName,
		style: {},
		attributes: {},
		childNodes: [],
		parentNode: undefined,
		yogaNode: nodeName === 'ink-virtual-text' ? undefined : Yoga.Node.create(),
		// eslint-disable-next-line @typescript-eslint/naming-convention
		internal_accessibility: {},
	};

	if (nodeName === 'ink-text') {
		node.yogaNode?.setMeasureFunc(measureTextNode.bind(null, node));
	}

	return node;
};

// Grid items aren't Yoga children of their grid container, so changes to them have to be forwarded to it manually
const addGridItem = (node: DOMElement, itemNode: DOMNode): void => {
	itemNode.yogaNode?.setDirtiedFunc(() => {
		node.yogaNode?.markDirty();
	});

	node.yogaNode?.markDirty();
};

const removeGridItem = (node: DOMElement, itemNode: DOMNode): void => {
	itemNode.yogaNode?.unsetDirtiedFunc();
	itemNode.internal_gridOffset = undefined;
	node.yogaNode?.markDirty();
};

export const appendChildNode = (
	node: DOMElement,
	childNode: DOMElement,
): void => {
	if (childNode.parentNode) {
		removeChildNode(childNode.parentNode, childNode);
	}

	childNode.parentNode = node;
	node.childNodes.push(childNode);

	if (childNode.yogaNode && isGridContainer(node)) {
		addGridItem(node, childNode);
	} else if (childNode.yogaNode) {
		node.yogaNode?.insertChild(
			childNode.yogaNode,
			node.yogaNode.getChildCount(),
		);
	}

	if (node.nodeName === 'ink-text' || node.nodeName === 'ink-virtual-text') {
		markNodeAsDirty(node);
	}
};

export const insertBeforeNode = (
	node: DOMElement,
	newChildNode: DOMNode,
	beforeChildNode: DOMNode,
): void => {
	if (newChildNode.parentNode) {
		removeChildNode(newChildNode.parentNode, newChildNode);
	}

	newChildNode.parentNode = node;

	const index = node.childNodes.indexOf(beforeChildNode);
	if (newChildNode.yogaNode && isGridContainer(node)) {
		node.childNodes.splice(
			index >= 0 ? index : node.childNodes.length,
			0,
			newChildNode,
		);
		addGridItem(node, newChildNode);
	} else if (index >= 0) {
		node.childNodes.splice(index, 0, newChildNode);
		if (newChildNode.yogaNode) {
			node.yogaNode?.insertChild(newChildNode.yogaNode, index);
		}
	} else {
		node.childNodes.push(newChildNode);

		if (newChildNode.yogaNode) {
			node.yogaNode?.insertChild(
				newChildNode.yogaNode,
				node.yogaNode.getChildCount(),
			);
		}
	}

	if (node.nodeName === 'ink-text' || node.nodeName === 'ink-virtual-text') {
		markNodeAsDirty(node);
	}
};

export const removeChildNode = (
	node: DOMElement,
	removeNode: DOMNode,
): void => {
	if (removeNode.yogaNode && isGridContainer(node)) {
		removeGridItem(node, removeNode);
	} else if (removeNode.yogaNode) {
		removeNode.parentNode?.yogaNode?.removeChild(removeNode.yogaNode);
	}

	removeNode.parentNode = undefined;

	const index = node.childNodes.indexOf(removeNode);
	if (index >= 0) {
		node.childNodes.splice(index, 1);
	}

	if (node.nodeName === 'ink-text' || node.nodeName === 'ink-virtual-text') {
		markNodeAsDirty(node);
	}
};

export const setAttribute = (
	node: DOMElement,
	key: string,
	value: DOMNodeAttribute,
): void => {
	if (key === 'internal_accessibility') {
		node.internal_accessibility = value as DOMElement['internal_accessibility'];
		return;
	}

	node.attributes[key] = value;
};

const gridContainerStyles = [
	'gridTemplateColumns',
	'gridTemplateRows',
	'gap',
	'columnGap',
	'rowGap',
] as const;

const gridItemStyles = ['gridColumn', 'gridRow'] as const;

const updateGridStyles = (node: DOMElement, previousStyle: Styles): void => {
	const {yogaNode, parentNode} = node;

	if (!yogaNode) {
		return;
	}

	const wasGridContainer =
		node.nodeName === 'ink-box' && previousStyle.display === 'grid';
	const isGrid = isGridContainer(node);

	if (isGrid && !wasGridContainer) {
		// Yoga doesn't allow children on nodes with a measure function
		for (const childNode of node.childNodes) {
			if (childNode.yogaNode) {
				yogaNode.removeChild(childNode.yogaNode);
			}
		}

		yogaNode.setMeasureFunc(createGridMeasureFunc(node));

		for (const childNode of node.childNodes) {
			if (childNode.yogaNode) {
				addGridItem(node, childNode);
			}
		}

		yogaNode.markDirty();
	} else if (wasGridContainer && !isGrid) {
		// Yoga only allows marking nodes with a measure function as dirty
		yogaNode.markDirty();
		yogaNode.unsetMeasureFunc();

		for (const childNode of node.childNodes) {
			if (childNode.yogaNode) {
				childNode.yogaNode.unsetDirtiedFunc();
				childNode.internal_gridOffset = undefined;
				yogaNode.insertChild(childNode.yogaNode, yogaNode.getChildCount());
			}
		}
	} else if (
		isGrid &&
		gridContainerStyles.some(key => previousStyle[key] !== node.style[key])
	) {
		yogaNode.markDirty();
	}

	if (
		parentNode &&
		isGridContainer(parentNode) &&
		gridItemStyles.some(key => previousStyle[key] !== node.style[key])
	) {
		parentNode.yogaNode?.markDirty();
	}
};

export const setStyle = (node: DOMNode, style?: Styles): void => {
	const previousStyle = node.style;

	// Rendering code assumes style is always an object.
	node.style = style ?? {};

	if (node.nodeName !== '#text') {
		updateGridStyles(node, previousStyle);
	}
};

export const createTextNode = (text: string): TextNode => {
	const node: TextNode = {
		nodeName: '#text',
		nodeValue: text,
		yogaNode: undefined,
		parentNode: undefined,
		style: {},
	};

	setTextNodeValue(node, text);

	return node;
};

const measureTextNode = function (
	node: DOMNode,
	width: number,
): {width: number; height: number} {
	const text =
		node.nodeName === '#text' ? node.nodeValue : squashTextNodes(node);

	const dimensions = measureText(text);

	// Text fits into container, no need to wrap
	if (dimensions.width <= width) {
		return dimensions;
	}

	// This is happening when <Box> is shrinking child nodes and Yoga asks
	// if we can fit this text node in a <1px space, so we just tell Yoga "no"
	if (dimensions.width >= 1 && width > 0 && width < 1) {
		return dimensions;
	}

	const textWrap = node.style?.textWrap ?? 'wrap';
	const wrappedText = wrapText(text, width, textWrap);

	return measureText(wrappedText);
};

const findClosestYogaNode = (node?: DOMNode): YogaNode | undefined => {
	if (!node?.parentNode) {
		return undefined;
	}

	return node.yogaNode ?? findClosestYogaNode(node.parentNode);
};

const markNodeAsDirty = (node?: DOMNode): void => {
	// Mark closest Yoga node as dirty to measure text dimensions again
	const yogaNode = findClosestYogaNode(node);
	yogaNode?.markDirty();
};

export const setTextNodeValue = (node: TextNode, text: string): void => {
	if (typeof text !== 'string') {
		text = String(text);
	}

	node.nodeValue = text;
	markNodeAsDirty(node);
};

export const addLayoutListener = (
	rootNode: DOMElement,
	listener: LayoutListener,
): (() => void) => {
	if (rootNode.nodeName !== 'ink-root') {
		return () => {};
	}

	rootNode.internal_layoutListeners ??= new Set();
	rootNode.internal_layoutListeners.add(listener);

	return () => {
		rootNode.internal_layoutListeners?.delete(listener);
	};
};

export const emitLayoutListeners = (rootNode: DOMElement): void => {
	if (rootNode.nodeName !== 'ink-root' || !rootNode.internal_layoutListeners) {
		return;
	}

	for (const listener of rootNode.internal_layoutListeners) {
		listener();
	}
};
