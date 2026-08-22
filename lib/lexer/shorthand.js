import * as names from '../utils/names.js';
import { walk } from '../definition-syntax/index.js';

const BOX_SIDES = ['top', 'right', 'bottom', 'left'];
const BOX_CORNERS = ['top-left', 'top-right', 'bottom-right', 'bottom-left'];

const INITIALS = {
    'margin-top': '0',
    'margin-right': '0',
    'margin-bottom': '0',
    'margin-left': '0',
    'padding-top': '0',
    'padding-right': '0',
    'padding-bottom': '0',
    'padding-left': '0',
    top: 'auto',
    right: 'auto',
    bottom: 'auto',
    left: 'auto',
    'border-width': 'medium',
    'border-style': 'none',
    'border-color': 'currentcolor',
    'border-top-width': 'medium',
    'border-right-width': 'medium',
    'border-bottom-width': 'medium',
    'border-left-width': 'medium',
    'border-top-style': 'none',
    'border-right-style': 'none',
    'border-bottom-style': 'none',
    'border-left-style': 'none',
    'border-top-color': 'currentcolor',
    'border-right-color': 'currentcolor',
    'border-bottom-color': 'currentcolor',
    'border-left-color': 'currentcolor',
    'border-top-left-radius': '0',
    'border-top-right-radius': '0',
    'border-bottom-right-radius': '0',
    'border-bottom-left-radius': '0',
    'background-image': 'none',
    'background-position': '0% 0%',
    'background-size': 'auto',
    'background-repeat': 'repeat',
    'background-origin': 'padding-box',
    'background-clip': 'border-box',
    'background-attachment': 'scroll',
    'background-color': 'transparent',
    'font-style': 'normal',
    'font-variant': 'normal',
    'font-weight': 'normal',
    'font-stretch': 'normal',
    'font-size': 'medium',
    'line-height': 'normal',
    'font-family': 'initial',
    'outline-width': 'medium',
    'outline-style': 'none',
    'outline-color': 'auto',
    'overflow-x': 'visible',
    'overflow-y': 'visible',
    'flex-grow': '0',
    'flex-shrink': '1',
    'flex-basis': 'auto',
    'flex-direction': 'row',
    'flex-wrap': 'nowrap',
    'row-gap': 'normal',
    'column-gap': 'normal',
    'text-decoration-line': 'none',
    'text-decoration-style': 'solid',
    'text-decoration-color': 'currentcolor',
    'text-decoration-thickness': 'auto',
    'list-style-type': 'disc',
    'list-style-position': 'outside',
    'list-style-image': 'none'
};

const BACKGROUND_LONGHANDS = [
    'background-image',
    'background-position',
    'background-size',
    'background-repeat',
    'background-origin',
    'background-clip',
    'background-attachment',
    'background-color'
];

const FONT_LONGHANDS = [
    'font-style',
    'font-variant',
    'font-weight',
    'font-stretch',
    'font-size',
    'line-height',
    'font-family'
];

const TYPE_ALIAS = {
    'font-variant-css2': 'font-variant',
    'font-width-css3': 'font-stretch',
    'bg-image': 'background-image',
    'bg-position': 'background-position',
    'bg-size': 'background-size',
    'repeat-style': 'background-repeat',
    attachment: 'background-attachment'
};

const BORDER_SIDE_TYPE_MAP = {
    'line-width': 'width',
    'line-style': 'style',
    color: 'color'
};

function unique(list) {
    const seen = Object.create(null);
    const result = [];

    for (const item of list) {
        if (!seen[item]) {
            seen[item] = true;
            result.push(item);
        }
    }

    return result;
}

function getInitial(name) {
    return Object.prototype.hasOwnProperty.call(INITIALS, name) ? INITIALS[name] : 'initial';
}

function normalizeKeyword(value) {
    if (typeof value === 'string') {
        return value.trim().toLowerCase();
    }

    if (value && value.type === 'Value' && value.children && value.children.size === 1) {
        const node = value.children.first;

        if (node.type === 'Identifier') {
            return String(node.name).toLowerCase();
        }
    }

    if (value && value.type === 'Identifier') {
        return String(value.name).toLowerCase();
    }

    return null;
}

function isCssWideKeyword(lexer, value) {
    const keyword = normalizeKeyword(value);

    if (!keyword) {
        return false;
    }

    return lexer.cssWideKeywords.some(item => item.toLowerCase() === keyword);
}

function serializeMatch(matchNode) {
    const tokens = [];

    function walkMatch(node) {
        if (!node) {
            return;
        }

        if (typeof node.token === 'string') {
            tokens.push(node.token);
        }

        if (Array.isArray(node.match)) {
            node.match.forEach(walkMatch);
        }
    }

    walkMatch(matchNode);

    let result = '';

    for (const token of tokens) {
        if (!result) {
            result = token;
            continue;
        }

        if (
            token === ',' ||
            token === '/' ||
            token === ')' ||
            token === ']' ||
            token === '}' ||
            result.endsWith('(') ||
            result.endsWith('[') ||
            result.endsWith('{') ||
            result.endsWith('/')
        ) {
            result += token;
        } else if (result.endsWith(',')) {
            result += ' ' + token;
        } else {
            result += ' ' + token;
        }
    }

    return result;
}

function collectDirectPropertyMatches(matchNode, result, rootName) {
    if (!matchNode) {
        return result;
    }

    if (matchNode.syntax && matchNode.syntax.type === 'Property') {
        if (!rootName || matchNode.syntax.name !== rootName) {
            result.push(matchNode);
            return result;
        }
    }

    if (Array.isArray(matchNode.match)) {
        for (const child of matchNode.match) {
            collectDirectPropertyMatches(child, result, rootName);
        }
    }

    return result;
}

function collectTypeMatches(matchNode, namesSet, result) {
    if (!matchNode || !matchNode.syntax) {
        if (matchNode && Array.isArray(matchNode.match)) {
            matchNode.match.forEach(child => collectTypeMatches(child, namesSet, result));
        }

        return result;
    }

    if (matchNode.syntax.type === 'Type' && namesSet.has(matchNode.syntax.name)) {
        result.push(matchNode);
        return result;
    }

    if (Array.isArray(matchNode.match)) {
        matchNode.match.forEach(child => collectTypeMatches(child, namesSet, result));
    }

    return result;
}

function collectKeywordTokens(matchNode, result) {
    if (!matchNode) {
        return result;
    }

    if (matchNode.syntax && matchNode.syntax.type === 'Keyword' && typeof matchNode.token === 'string') {
        result.push(matchNode.token);
        return result;
    }

    if (Array.isArray(matchNode.match)) {
        matchNode.match.forEach(child => collectKeywordTokens(child, result));
    }

    return result;
}

function assignBox(target, longhands, values) {
    const [a, b, c, d] = longhands;

    switch (values.length) {
        case 1:
            target[a] = target[b] = target[c] = target[d] = values[0];
            break;
        case 2:
            target[a] = target[c] = values[0];
            target[b] = target[d] = values[1];
            break;
        case 3:
            target[a] = values[0];
            target[b] = target[d] = values[1];
            target[c] = values[2];
            break;
        default:
            target[a] = values[0];
            target[b] = values[1];
            target[c] = values[2];
            target[d] = values[3];
    }
}

function compressBox(values) {
    const [t, r, b, l] = values;

    if (t === r && r === b && b === l) {
        return t;
    }

    if (t === b && r === l) {
        return t + ' ' + r;
    }

    if (r === l) {
        return t + ' ' + r + ' ' + b;
    }

    return t + ' ' + r + ' ' + b + ' ' + l;
}

function splitTopLevel(value) {
    const result = [];
    let current = '';
    let depth = 0;

    for (let i = 0; i < value.length; i++) {
        const ch = value[i];

        if (ch === '(' || ch === '[' || ch === '{') {
            depth++;
            current += ch;
        } else if (ch === ')' || ch === ']' || ch === '}') {
            depth = Math.max(0, depth - 1);
            current += ch;
        } else if (ch === ' ' && depth === 0) {
            if (current) {
                result.push(current);
                current = '';
            }
        } else {
            current += ch;
        }
    }

    if (current) {
        result.push(current);
    }

    return result;
}

function splitCommaList(value) {
    const result = [];
    let current = '';
    let depth = 0;

    for (let i = 0; i < value.length; i++) {
        const ch = value[i];

        if (ch === '(' || ch === '[' || ch === '{') {
            depth++;
            current += ch;
        } else if (ch === ')' || ch === ']' || ch === '}') {
            depth = Math.max(0, depth - 1);
            current += ch;
        } else if (ch === ',' && depth === 0) {
            result.push(current.trim());
            current = '';
        } else {
            current += ch;
        }
    }

    if (current.trim() || result.length) {
        result.push(current.trim());
    }

    return result;
}

function sidesFromTop(name) {
    if (name.endsWith('-top')) {
        const prefix = name.slice(0, -4);

        return BOX_SIDES.map(side => prefix + '-' + side);
    }

    if (name === 'top') {
        return BOX_SIDES.slice();
    }

    return null;
}

function cornersFromTopLeft(name) {
    const suffix = '-top-left-radius';

    if (name.endsWith(suffix)) {
        const prefix = name.slice(0, -suffix.length) || 'border';

        return BOX_CORNERS.map(corner => prefix + '-' + corner + '-radius');
    }

    return null;
}

function pairFromFirst(name) {
    if (name.endsWith('-x')) {
        return [name, name.slice(0, -2) + '-y'];
    }

    if (name.startsWith('row-')) {
        return [name, 'column-' + name.slice(4)];
    }

    return null;
}

function analyzeSyntax(syntax) {
    const props = [];
    let boxMultiplier = false;
    let pairMultiplier = false;
    let commaMultiplier = false;

    walk(syntax, (node) => {
        if (node.type === 'Property') {
            props.push(node.name);
        }

        if (node.type === 'Multiplier') {
            if (node.min === 1 && node.max === 4) {
                boxMultiplier = true;
            }

            if (node.min === 1 && node.max === 2) {
                pairMultiplier = true;
            }

            if (node.comma) {
                commaMultiplier = true;
            }
        }
    });

    return {
        props: unique(props),
        boxMultiplier,
        pairMultiplier,
        commaMultiplier
    };
}

function boxLonghandsForName(propertyName) {
    if (propertyName === 'inset') {
        return ['top', 'right', 'bottom', 'left'];
    }

    if (propertyName === 'border-radius') {
        return [
            'border-top-left-radius',
            'border-top-right-radius',
            'border-bottom-right-radius',
            'border-bottom-left-radius'
        ];
    }

    const prefixSides = BOX_SIDES.map(side => propertyName + '-' + side);

    if (
        propertyName === 'margin' ||
        propertyName === 'padding' ||
        propertyName === 'border-width' ||
        propertyName === 'border-style' ||
        propertyName === 'border-color'
    ) {
        return prefixSides;
    }

    return prefixSides;
}

export function getShorthandInfo(lexer, propertyName) {
    const descriptor = lexer.getProperty(propertyName);

    if (!descriptor || !descriptor.syntax) {
        return null;
    }

    const basename = names.property(descriptor.name).basename;
    const analysis = analyzeSyntax(descriptor.syntax);

    if (basename === 'background') {
        return { kind: 'background', longhands: BACKGROUND_LONGHANDS.slice() };
    }

    if (basename === 'font') {
        return { kind: 'font', longhands: FONT_LONGHANDS.slice() };
    }

    if (basename === 'flex') {
        return { kind: 'flex', longhands: ['flex-grow', 'flex-shrink', 'flex-basis'] };
    }

    if (basename === 'overflow') {
        return { kind: 'pair', longhands: ['overflow-x', 'overflow-y'] };
    }

    if (basename === 'border-radius') {
        return { kind: 'radius', longhands: boxLonghandsForName('border-radius') };
    }

    if (
        basename === 'border' ||
        basename === 'border-top' ||
        basename === 'border-right' ||
        basename === 'border-bottom' ||
        basename === 'border-left'
    ) {
        const prefix = basename === 'border' ? 'border' : basename;

        return {
            kind: 'border-components',
            longhands: [prefix + '-width', prefix + '-style', prefix + '-color']
        };
    }

    if (basename === 'margin' || basename === 'padding' || basename === 'inset') {
        return { kind: 'box', longhands: boxLonghandsForName(basename) };
    }

    if (analysis.props.length === 1 && analysis.boxMultiplier) {
        const fromTop = sidesFromTop(analysis.props[0]);
        const fromCorner = cornersFromTopLeft(analysis.props[0]);

        if (fromCorner) {
            return { kind: 'radius', longhands: fromCorner };
        }

        if (fromTop) {
            return { kind: 'box', longhands: fromTop };
        }

        return { kind: 'box', longhands: boxLonghandsForName(basename) };
    }

    if (analysis.props.length === 2) {
        const syntax = descriptor.syntax;
        const optionalSecond = (
            syntax.type === 'Group' &&
            syntax.combinator === ' ' &&
            syntax.terms &&
            syntax.terms[1] &&
            syntax.terms[1].type === 'Multiplier' &&
            syntax.terms[1].min === 0
        );

        if (analysis.pairMultiplier || optionalSecond) {
            return { kind: 'pair', longhands: analysis.props };
        }

        return { kind: 'components', longhands: analysis.props };
    }

    if (analysis.props.length >= 2) {
        return { kind: 'components', longhands: analysis.props };
    }

    if (analysis.props.length === 0 && analysis.boxMultiplier) {
        const longhands = boxLonghandsForName(basename);

        if (basename.endsWith('-radius')) {
            return { kind: 'radius', longhands: boxLonghandsForName('border-radius') };
        }

        return { kind: 'box', longhands };
    }

    if (analysis.props.length === 0 && analysis.pairMultiplier) {
        const xy = [basename + '-x', basename + '-y'];

        if (xy.every(name => lexer.getProperty(name))) {
            return { kind: 'pair', longhands: xy };
        }
    }

    if (analysis.props.length === 1 && !analysis.boxMultiplier) {
        const pair = pairFromFirst(analysis.props[0]);

        if (pair) {
            return { kind: 'pair', longhands: pair };
        }
    }

    return null;
}

function fillInitials(longhands) {
    const result = {};

    for (const name of longhands) {
        result[name] = getInitial(name);
    }

    return result;
}

function keywordMap(longhands, keyword) {
    const result = {};

    for (const name of longhands) {
        result[name] = keyword;
    }

    return result;
}

function expandBackground(match) {
    const layers = [];

    function visit(node) {
        if (!node || !node.syntax) {
            if (node && Array.isArray(node.match)) {
                node.match.forEach(visit);
            }

            return;
        }

        if (node.syntax.type === 'Type' && (node.syntax.name === 'bg-layer' || node.syntax.name === 'final-bg-layer')) {
            layers.push(node);
            return;
        }

        if (Array.isArray(node.match)) {
            node.match.forEach(visit);
        }
    }

    visit(match);

    if (layers.length === 0) {
        layers.push(match);
    }

    const collected = {
        'background-image': [],
        'background-position': [],
        'background-size': [],
        'background-repeat': [],
        'background-origin': [],
        'background-clip': [],
        'background-attachment': [],
        'background-color': []
    };

    for (let i = 0; i < layers.length; i++) {
        const layer = layers[i];
        const layerValues = Object.create(null);
        const boxes = [];

        function walkLayer(node) {
            if (!node || !node.syntax) {
                if (node && Array.isArray(node.match)) {
                    node.match.forEach(walkLayer);
                }

                return;
            }

            if (node.syntax.type === 'Type') {
                const alias = TYPE_ALIAS[node.syntax.name];

                if (alias) {
                    layerValues[alias] = serializeMatch(node);
                    return;
                }

                if (node.syntax.name === 'visual-box' || node.syntax.name === 'bg-clip' || node.syntax.name === 'box') {
                    boxes.push(serializeMatch(node));
                    return;
                }
            }

            if (node.syntax.type === 'Property' && node.syntax.name === 'background-color') {
                layerValues['background-color'] = serializeMatch(node);
                return;
            }

            if (Array.isArray(node.match)) {
                node.match.forEach(walkLayer);
            }
        }

        walkLayer(layer);

        if (boxes.length === 1) {
            layerValues['background-origin'] = boxes[0];
            layerValues['background-clip'] = boxes[0];
        } else if (boxes.length >= 2) {
            layerValues['background-origin'] = boxes[0];
            layerValues['background-clip'] = boxes[1];
        }

        for (const name of BACKGROUND_LONGHANDS) {
            if (name === 'background-color') {
                continue;
            }

            collected[name].push(layerValues[name] || getInitial(name));
        }

        if (i === layers.length - 1) {
            collected['background-color'].push(layerValues['background-color'] || getInitial('background-color'));
        }
    }

    const result = {};

    for (const name of BACKGROUND_LONGHANDS) {
        result[name] = collected[name].join(', ');
    }

    return result;
}

function expandRadius(match, longhands) {
    const lengths = [];
    let slashSeen = false;
    const before = [];
    const after = [];

    function walkMatch(node) {
        if (!node) {
            return;
        }

        if (node.syntax && node.syntax.type === 'Token' && node.token === '/') {
            slashSeen = true;
            return;
        }

        if (node.syntax && node.syntax.type === 'Type' && node.syntax.name && node.syntax.name.startsWith('length-percentage')) {
            (slashSeen ? after : before).push(serializeMatch(node));
            return;
        }

        if (typeof node.token === 'string' && !node.match && node.token !== '/') {
            lengths.push(node.token);
        }

        if (Array.isArray(node.match)) {
            node.match.forEach(walkMatch);
        }
    }

    walkMatch(match);

    const horiz = before.length ? before : lengths;
    const vert = after;
    const result = {};
    const h = {};
    const v = {};

    assignBox(h, longhands, horiz.length ? horiz : ['0']);

    if (vert.length) {
        assignBox(v, longhands, vert);
    }

    for (const name of longhands) {
        result[name] = vert.length && h[name] !== v[name]
            ? h[name] + ' ' + v[name]
            : h[name];
    }

    return result;
}

function expandFlex(match, longhands) {
    const result = fillInitials(longhands);
    const keywords = collectKeywordTokens(match, []);

    if (keywords.length === 1 && keywords[0].toLowerCase() === 'none') {
        result['flex-grow'] = '0';
        result['flex-shrink'] = '0';
        result['flex-basis'] = 'auto';
        return result;
    }

    if (keywords.length === 1 && keywords[0].toLowerCase() === 'auto') {
        result['flex-grow'] = '1';
        result['flex-shrink'] = '1';
        result['flex-basis'] = 'auto';
        return result;
    }

    const props = collectDirectPropertyMatches(match, [], match.syntax && match.syntax.name);
    const seen = Object.create(null);

    for (const node of props) {
        const name = node.syntax.name;

        if (name === 'flex-grow' || name === 'flex-shrink' || name === 'flex-basis') {
            seen[name] = serializeMatch(node);
        }
    }

    if (seen['flex-grow'] || seen['flex-shrink'] || seen['flex-basis']) {
        result['flex-grow'] = seen['flex-grow'] || '1';
        result['flex-shrink'] = seen['flex-shrink'] || '1';
        result['flex-basis'] = seen['flex-basis'] || (seen['flex-grow'] || seen['flex-shrink'] ? '0%' : 'auto');
    }

    return result;
}

function expandFont(match, longhands) {
    const system = [];

    collectTypeMatches(match, new Set(['system-family-name', '-non-standard-font']), system);

    if (system.length && !collectDirectPropertyMatches(match, [], match.syntax && match.syntax.name).some(node => node.syntax.name === 'font-size')) {
        return keywordMap(longhands, serializeMatch(system[0]));
    }

    const result = fillInitials(longhands);
    const props = collectDirectPropertyMatches(match, [], match.syntax && match.syntax.name);

    for (const node of props) {
        const name = node.syntax.name;

        if (name === 'font-family') {
            result[name] = result[name] === getInitial(name)
                ? serializeMatch(node)
                : result[name] + ', ' + serializeMatch(node);
        } else if (longhands.includes(name)) {
            result[name] = serializeMatch(node);
        }
    }

    const aliases = collectTypeMatches(match, new Set(Object.keys(TYPE_ALIAS)), []);

    for (const node of aliases) {
        const name = TYPE_ALIAS[node.syntax.name];

        if (name && FONT_LONGHANDS.includes(name)) {
            result[name] = serializeMatch(node);
        }
    }

    return result;
}

function expandBorderComponents(match, longhands) {
    const result = fillInitials(longhands);
    const types = collectTypeMatches(match, new Set(Object.keys(BORDER_SIDE_TYPE_MAP)), []);

    for (const node of types) {
        const suffix = BORDER_SIDE_TYPE_MAP[node.syntax.name];
        const prefix = longhands[0].slice(0, -'-width'.length);
        const name = prefix + '-' + suffix;

        if (Object.prototype.hasOwnProperty.call(result, name)) {
            result[name] = serializeMatch(node);
        }
    }

    const props = collectDirectPropertyMatches(match, [], match.syntax && match.syntax.name);

    for (const node of props) {
        if (Object.prototype.hasOwnProperty.call(result, node.syntax.name)) {
            result[node.syntax.name] = serializeMatch(node);
        }
    }

    return result;
}

function expandGeneric(match, info) {
    const result = fillInitials(info.longhands);

    if (info.kind === 'box') {
        const props = collectDirectPropertyMatches(match, [], match.syntax && match.syntax.name);
        const values = props.length
            ? props.map(serializeMatch)
            : collectTypeMatches(match, new Set(['length-percentage', 'length', 'percentage', 'line-width']), []).map(serializeMatch);

        if (values.length) {
            assignBox(result, info.longhands, values);
        }

        return result;
    }

    if (info.kind === 'pair') {
        const props = collectDirectPropertyMatches(match, [], match.syntax && match.syntax.name);

        if (props.length) {
            const first = serializeMatch(props[0]);
            const second = props[1] ? serializeMatch(props[1]) : first;

            result[info.longhands[0]] = first;
            result[info.longhands[1]] = second;
            return result;
        }

        const keywords = collectKeywordTokens(match, []);

        if (keywords.length) {
            result[info.longhands[0]] = keywords[0];
            result[info.longhands[1]] = keywords[1] || keywords[0];
        }

        return result;
    }

    const props = collectDirectPropertyMatches(match, [], match.syntax && match.syntax.name);

    for (const node of props) {
        const name = node.syntax.name;

        if (!Object.prototype.hasOwnProperty.call(result, name)) {
            continue;
        }

        result[name] = result[name] === getInitial(name)
            ? serializeMatch(node)
            : result[name] + ', ' + serializeMatch(node);
    }

    const aliases = collectTypeMatches(match, new Set(Object.keys(TYPE_ALIAS)), []);

    for (const node of aliases) {
        const name = TYPE_ALIAS[node.syntax.name];

        if (name && Object.prototype.hasOwnProperty.call(result, name)) {
            result[name] = serializeMatch(node);
        }
    }

    return result;
}

function lookupLonghand(map, name) {
    if (Object.prototype.hasOwnProperty.call(map, name)) {
        return map[name];
    }

    for (const key of Object.keys(map)) {
        if (names.property(key).basename === name || names.property(key).name === name) {
            return map[key];
        }
    }

    return undefined;
}

function allPresent(info, longhands) {
    return info.longhands.every(name => lookupLonghand(longhands, name) != null);
}

function compressBackground(info, longhands) {
    const lists = info.longhands.map(name => splitCommaList(String(lookupLonghand(longhands, name))));
    const layerCount = Math.max(...lists.map(list => list.length));
    const layers = [];

    for (let i = 0; i < layerCount; i++) {
        const image = lists[0][i] || getInitial('background-image');
        const position = lists[1][i] || getInitial('background-position');
        const size = lists[2][i] || getInitial('background-size');
        const repeat = lists[3][i] || getInitial('background-repeat');
        const origin = lists[4][i] || getInitial('background-origin');
        const clip = lists[5][i] || getInitial('background-clip');
        const attachment = lists[6][i] || getInitial('background-attachment');
        const color = i === layerCount - 1 ? (lists[7][0] || getInitial('background-color')) : null;
        const parts = [image, position + '/' + size, repeat, origin, clip, attachment];

        if (color) {
            parts.push(color);
        }

        layers.push(parts.join(' '));
    }

    return layers.join(', ');
}

function compressFont(info, longhands) {
    const values = info.longhands.map(name => String(lookupLonghand(longhands, name)));

    return (
        values[0] + ' ' +
        values[1] + ' ' +
        values[2] + ' ' +
        values[3] + ' ' +
        values[4] + '/' +
        values[5] + ' ' +
        values[6]
    );
}

function compressRadius(info, longhands) {
    const horiz = [];
    const vert = [];

    for (const name of info.longhands) {
        const parts = splitTopLevel(String(lookupLonghand(longhands, name)));

        horiz.push(parts[0]);
        vert.push(parts[1] || parts[0]);
    }

    const h = compressBox(horiz);
    const v = compressBox(vert);

    return h === v ? h : h + '/' + v;
}

export function expandShorthand(lexer, propertyName, value) {
    const info = getShorthandInfo(lexer, propertyName);

    if (!info) {
        return null;
    }

    if (isCssWideKeyword(lexer, value)) {
        return keywordMap(info.longhands, typeof value === 'string' ? value.trim() : normalizeKeyword(value));
    }

    const match = lexer.matchProperty(propertyName, value);

    if (!match.matched) {
        return null;
    }

    switch (info.kind) {
        case 'background':
            return expandBackground(match.matched);
        case 'font':
            return expandFont(match.matched, info.longhands);
        case 'flex':
            return expandFlex(match.matched, info.longhands);
        case 'radius':
            return expandRadius(match.matched, info.longhands);
        case 'border-components':
            return expandBorderComponents(match.matched, info.longhands);
        default:
            return expandGeneric(match.matched, info);
    }
}

export function compressShorthand(lexer, propertyName, longhands) {
    const info = getShorthandInfo(lexer, propertyName);

    if (!info || !longhands || typeof longhands !== 'object') {
        return null;
    }

    if (!allPresent(info, longhands)) {
        return null;
    }

    const values = info.longhands.map(name => String(lookupLonghand(longhands, name)));
    const wideFlags = values.map(value => isCssWideKeyword(lexer, value));

    if (wideFlags.some(Boolean)) {
        if (wideFlags.every(Boolean) && values.every(value => value.trim().toLowerCase() === values[0].trim().toLowerCase())) {
            return values[0].trim();
        }

        return null;
    }

    if (info.kind === 'box' || info.kind === 'pair') {
        if (info.kind === 'pair') {
            return values[0] === values[1] ? values[0] : values[0] + ' ' + values[1];
        }

        return compressBox(values);
    }

    if (info.kind === 'radius') {
        return compressRadius(info, longhands);
    }

    if (info.kind === 'background') {
        return compressBackground(info, longhands);
    }

    if (info.kind === 'font') {
        return compressFont(info, longhands);
    }

    if (info.kind === 'flex') {
        return values.join(' ');
    }

    return values.join(' ');
}
