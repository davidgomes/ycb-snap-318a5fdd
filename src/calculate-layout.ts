import Yoga from 'yoga-layout';
import {type DOMElement} from './dom.js';
import {finalizeGridLayout, prepareGridLayout} from './grid-layout.js';

const calculateLayout = (rootNode: DOMElement, width?: number): void => {
	if (!rootNode.yogaNode) {
		return;
	}

	if (width !== undefined) {
		rootNode.yogaNode.setWidth(width);
	}

	prepareGridLayout(rootNode);
	rootNode.yogaNode.calculateLayout(undefined, undefined, Yoga.DIRECTION_LTR);
	finalizeGridLayout(rootNode);
};

export default calculateLayout;
