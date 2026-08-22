import { walk } from '../definition-syntax/index.js';
import * as names from '../utils/names.js';

const BORDER_RADIUS_LONGHANDS = [
    'border-top-left-radius',
    'border-top-right-radius',
    'border-bottom-right-radius',
    'border-bottom-left-radius'
];

const SYSTEM_FONTS = new Set([
    'caption',
    'icon',
    'menu',
    'message-box',
    'small-caption',
    'status-bar'
]);

const INITIAL = {
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
    'border-top-style': 'none',
    'border-top-color': 'currentcolor',
    'border-right-width': 'medium',
    'border-right-style': 'none',
    'border-right-color': 'currentcolor',
    'border-bottom-width': 'medium',
    'border-bottom-style': 'none',
    'border-bottom-color': 'currentcolor',
    'border-left-width': 'medium',
    'border-left-style': 'none',
    'border-left-color': 'currentcolor',
    'border-top-left-radius': '0',
    'border-top-right-radius': '0',
    'border-bottom-right-radius': '0',
    'border-bottom-left-radius': '0',
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
    'list-style-image': 'none',
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
    'font-family': 'initial'
};

const SPECIAL = {
    margin: {
        kind: 'box',
        longhands: ['margin-top', 'margin-right', 'margin-bottom', 'margin-left']
    },
    padding: {
        kind: 'box',
        longhands: ['padding-top', 'padding-right', 'padding-bottom', 'padding-left']
    },
    inset: {
        kind: 'box',
        longhands: ['top', 'right', 'bottom', 'left']
    },
    'border-radius': {
        kind: 'radius',
        longhands: BORDER_RADIUS_LONGHANDS
    },
    'border-width': {
        kind: 'box',
        longhands: ['border-top-width', 'border-right-width', 'border-bottom-width', 'border-left-width']
    },
    'border-style': {
        kind: 'box',
        longhands: ['border-top-style', 'border-right-style', 'border-bottom-style', 'border-left-style']
    },
    'border-color': {
        kind: 'box',
        longhands: ['border-top-color', 'border-right-color', 'border-bottom-color', 'border-left-color']
    },
    border: {
        kind: 'components',
        longhands: ['border-width', 'border-style', 'border-color'],
        typeMap: {
            'line-width': 'border-width',
            'line-style': 'border-style',
            color: 'border-color'
        }
    },
    'border-top': {
        kind: 'components',
        longhands: ['border-top-width', 'border-top-style', 'border-top-color'],
        typeMap: {
            'line-width': 'border-top-width',
            'line-style': 'border-top-style',
            color: 'border-top-color'
        }
    },
    'border-right': {
        kind: 'components',
        longhands: ['border-right-width', 'border-right-style', 'border-right-color'],
        typeMap: {
            'line-width': 'border-right-width',
            'line-style': 'border-right-style',
            color: 'border-right-color'
        }
    },
    'border-bottom': {
        kind: 'components',
        longhands: ['border-bottom-width', 'border-bottom-style', 'border-bottom-color'],
        typeMap: {
            'line-width': 'border-bottom-width',
            'line-style': 'border-bottom-style',
            color: 'border-bottom-color'
        }
    },
    'border-left': {
        kind: 'components',
        longhands: ['border-left-width', 'border-left-style', 'border-left-color'],
        typeMap: {
            'line-width': 'border-left-width',
            'line-style': 'border-left-style',
            color: 'border-left-color'
        }
    },
    outline: {
        kind: 'components',
        longhands: ['outline-width', 'outline-style', 'outline-color']
    },
    overflow: {
        kind: 'pair',
        longhands: ['overflow-x', 'overflow-y']
    },
    gap: {
        kind: 'pair',
        longhands: ['row-gap', 'column-gap']
    },
    'flex-flow': {
        kind: 'components',
        longhands: ['flex-direction', 'flex-wrap']
    },
    flex: {
        kind: 'flex',
        longhands: ['flex-grow', 'flex-shrink', 'flex-basis']
    },
    'list-style': {
        kind: 'components',
        longhands: ['list-style-type', 'list-style-position', 'list-style-image']
    },
    'text-decoration': {
        kind: 'components',
        longhands: [
            'text-decoration-line',
            'text-decoration-style',
            'text-decoration-color',
            'text-decoration-thickness'
        ]
    },
    background: {
        kind: 'background',
        longhands: [
            'background-image',
            'background-position',
            'background-size',
            'background-repeat',
            'background-origin',
            'background-clip',
            'background-attachment',
            'background-color'
        ]
    },
    font: {
        kind: 'font',
        longhands: [
            'font-style',
            'font-variant',
            'font-weight',
            'font-stretch',
            'font-size',
            'line-height',
            'font-family'
        ]
    }
};

function hasProperty(lexer, name) {
    return lexer.getProperty(name) !== null;
}

function unique(list) {
    const seen = new Set();
    const result = [];

    for (const item of list) {
        if (!seen.has(item)) {
            seen.add(item);
            result.push(item);
        }
    }

    return result;
}

function collectPropertyRefs(syntax) {
    const refs = [];

    if (!syntax) {
        return refs;
    }

    walk(syntax, node => {
        if (node.type === 'Property') {
            refs.push(node.name);
        }
    });

    return unique(refs);
}

function unwrapSingle(node) {
    while (node && node.type === 'Group' && node.terms.length === 1 && !node.explicit) {
        node = node.terms[0];
    }

    return node;
}

function findMultipliers(syntax, predicate, found = []) {
    if (!syntax) {
        return found;
    }

    if (syntax.type === 'Multiplier' && predicate(syntax)) {
        found.push(syntax);
    }

    if (syntax.type === 'Group') {
        for (const term of syntax.terms) {
            findMultipliers(term, predicate, found);
        }
    } else if (syntax.type === 'Multiplier' || syntax.type === 'Boolean') {
        findMultipliers(syntax.term, predicate, found);
    }

    return found;
}

function isBoxPattern(syntax) {
    const multipliers = findMultipliers(syntax, m => (
        !m.comma &&
        m.min === 1 &&
        m.max === 4
    ));

    if (multipliers.length !== 1) {
        return false;
    }

    return findMultipliers(syntax, m => (
        !m.comma &&
        m.min === 1 &&
        m.max === 4 &&
        m !== multipliers[0]
    )).length === 0 && !isRadiusPattern(syntax);
}

function isRadiusPattern(syntax) {
    const node = unwrapSingle(syntax);

    if (!node || node.type !== 'Group' || node.combinator !== ' ') {
        return false;
    }

    const first = unwrapSingle(node.terms[0]);
    const second = node.terms[1];

    if (!first || first.type !== 'Multiplier' || first.min !== 1 || first.max !== 4 || first.comma) {
        return false;
    }

    if (!second) {
        return false;
    }

    // [ / <...>{1,4} ]?
    let optional = unwrapSingle(second);
    if (optional.type === 'Multiplier' && optional.min === 0 && optional.max === 1) {
        optional = unwrapSingle(optional.term);
    }

    if (!optional || optional.type !== 'Group') {
        return false;
    }

    return optional.terms.some(term => term.type === 'Token' && term.value === '/');
}

function isPairPattern(syntax, refs) {
    const multipliers = findMultipliers(syntax, m => (
        !m.comma &&
        m.min === 1 &&
        m.max === 2
    ));

    if (multipliers.length === 1) {
        return true;
    }

    if (refs.length === 2) {
        const node = unwrapSingle(syntax);

        // Sequential "A B?" copies a single value to both longhands.
        // A `||` group is a component shorthand instead.
        if (node && node.type === 'Group' && node.combinator === ' ') {
            return true;
        }
    }

    return false;
}

function inferBoxLonghands(lexer, shorthandName, refs) {
    const sides = ['top', 'right', 'bottom', 'left'];
    const prefixed = sides.map(side => `${shorthandName}-${side}`);

    if (prefixed.every(name => hasProperty(lexer, name))) {
        return prefixed;
    }

    if (refs[0] && refs[0].endsWith('-top')) {
        const prefix = refs[0].slice(0, -3);
        const fromRef = sides.map(side => prefix + side);

        if (fromRef.every(name => hasProperty(lexer, name))) {
            return fromRef;
        }
    }

    if (shorthandName === 'inset' || refs[0] === 'top') {
        if (sides.every(name => hasProperty(lexer, name))) {
            return sides;
        }
    }

    const inserted = sides.map(side => {
        const parts = shorthandName.split('-');
        const last = parts.pop();
        return [...parts, side, last].join('-');
    });

    if (inserted.every(name => hasProperty(lexer, name))) {
        return inserted;
    }

    const corners = ['top-left', 'top-right', 'bottom-right', 'bottom-left'];
    const cornerNames = corners.map(corner => `${shorthandName}-${corner}`);

    if (cornerNames.every(name => hasProperty(lexer, name))) {
        return cornerNames;
    }

    if (shorthandName === 'border-radius' || shorthandName.endsWith('border-radius')) {
        if (BORDER_RADIUS_LONGHANDS.every(name => hasProperty(lexer, name))) {
            return BORDER_RADIUS_LONGHANDS;
        }
    }

    return null;
}

function inferPairLonghands(lexer, shorthandName, refs) {
    if (refs.length === 2) {
        return refs;
    }

    const candidates = [
        [`${shorthandName}-x`, `${shorthandName}-y`],
        [`${shorthandName}-start`, `${shorthandName}-end`],
        [`${shorthandName}-row`, `${shorthandName}-column`],
        [`row-${shorthandName}`, `column-${shorthandName}`],
        [`${shorthandName}-block`, `${shorthandName}-inline`]
    ];

    if (refs[0]) {
        if (refs[0].endsWith('-x')) {
            candidates.unshift([refs[0], refs[0].slice(0, -1) + 'y']);
        }
        if (refs[0].endsWith('-start')) {
            candidates.unshift([refs[0], refs[0].slice(0, -5) + 'end']);
        }
        if (refs[0].startsWith('row-')) {
            candidates.unshift([refs[0], 'column-' + refs[0].slice(4)]);
        }
    }

    for (const pair of candidates) {
        if (pair.every(name => hasProperty(lexer, name))) {
            return pair;
        }
    }

    return null;
}

function inferBorderLike(lexer, shorthandName, syntax) {
    const node = unwrapSingle(syntax);

    if (!node || node.type !== 'Group' || node.combinator !== '||') {
        return null;
    }

    const types = node.terms.filter(term => term.type === 'Type').map(term => term.name);
    const suffixes = ['width', 'style', 'color'];
    const longhands = suffixes.map(suffix => `${shorthandName}-${suffix}`);

    if (types.length === 3 && longhands.every(name => hasProperty(lexer, name))) {
        const typeMap = Object.create(null);

        types.forEach((type, index) => {
            typeMap[type] = longhands[index];
        });

        return {
            kind: 'components',
            longhands,
            typeMap
        };
    }

    return null;
}

function inferShorthand(lexer, shorthandName, syntax) {
    if (!syntax) {
        return null;
    }

    const refs = collectPropertyRefs(syntax);

    if (isRadiusPattern(syntax)) {
        const longhands = inferBoxLonghands(lexer, shorthandName, refs);

        if (longhands) {
            return { kind: 'radius', longhands };
        }
    }

    if (isBoxPattern(syntax)) {
        const longhands = inferBoxLonghands(lexer, shorthandName, refs);

        if (longhands) {
            return { kind: 'box', longhands };
        }
    }

    if (isPairPattern(syntax, refs)) {
        const longhands = inferPairLonghands(lexer, shorthandName, refs);

        if (longhands && longhands.length === 2) {
            return { kind: 'pair', longhands };
        }
    }

    if (refs.includes('flex-grow') && refs.includes('flex-basis')) {
        return {
            kind: 'flex',
            longhands: unique(['flex-grow', 'flex-shrink', 'flex-basis'].concat(refs))
        };
    }

    if (refs.includes('font-size') && refs.includes('font-family')) {
        return { kind: 'font', longhands: SPECIAL.font.longhands };
    }

    const borderLike = inferBorderLike(lexer, shorthandName, syntax);

    if (borderLike) {
        return borderLike;
    }

    if (refs.length >= 2) {
        return { kind: 'components', longhands: refs };
    }

    return null;
}

function resolveShorthandName(propertyName) {
    const descriptor = names.property(propertyName);

    if (descriptor.custom) {
        return null;
    }

    return descriptor.basename || descriptor.name;
}

export function getShorthandInfo(lexer, propertyName) {
    if (typeof propertyName !== 'string' || !propertyName) {
        return null;
    }

    const descriptor = lexer.getProperty(propertyName);

    if (!descriptor) {
        return null;
    }

    const resolvedName = resolveShorthandName(propertyName);

    if (resolvedName === null) {
        return null;
    }

    if (SPECIAL[resolvedName]) {
        return {
            name: resolvedName,
            ...SPECIAL[resolvedName]
        };
    }

    const inferred = inferShorthand(lexer, resolvedName, descriptor.syntax);

    if (!inferred) {
        return null;
    }

    return {
        name: resolvedName,
        ...inferred
    };
}

function getInitialValue(name) {
    return Object.prototype.hasOwnProperty.call(INITIAL, name)
        ? INITIAL[name]
        : 'initial';
}

function isCssWideKeyword(lexer, value) {
    if (typeof value !== 'string') {
        return false;
    }

    const normalized = value.trim().toLowerCase();

    if (!normalized) {
        return false;
    }

    const keywords = lexer.cssWideKeywords || [];

    for (let i = 0; i < keywords.length; i++) {
        if (keywords[i].toLowerCase() === normalized) {
            return true;
        }
    }

    return false;
}

function valueToString(lexer, value) {
    if (typeof value === 'string') {
        return value.trim();
    }

    if (value && typeof value === 'object' && lexer.syntax && typeof lexer.syntax.generate === 'function') {
        try {
            return lexer.syntax.generate(value);
        } catch (e) {}
    }

    return null;
}

function joinCssTokens(tokens) {
    let result = '';

    for (let i = 0; i < tokens.length; i++) {
        const token = tokens[i];

        if (!token || /^[ \t\n\r\f]+$/.test(token)) {
            continue;
        }

        if (result) {
            const noSpace = token === ',' || token === ')' || token === '/' ||
                result.endsWith('(') || result.endsWith(',') || result.endsWith('/');

            if (!noSpace) {
                result += ' ';
            }
        }

        result += token;
    }

    return result;
}

function serializeMatch(matchNode) {
    if (!matchNode) {
        return '';
    }

    const tokens = [];

    function walkMatch(node) {
        if (node.token != null) {
            tokens.push(node.token);
        }

        if (Array.isArray(node.match)) {
            for (let i = 0; i < node.match.length; i++) {
                walkMatch(node.match[i]);
            }
        }
    }

    walkMatch(matchNode);

    return joinCssTokens(tokens);
}

function collectDirectComponents(matchNode) {
    const components = [];
    const children = matchNode && Array.isArray(matchNode.match) ? matchNode.match : [];

    for (let i = 0; i < children.length; i++) {
        const child = children[i];
        const syntax = child.syntax;

        if (syntax && syntax.type === 'Token' && child.token === '/') {
            components.push('/');
            continue;
        }

        if (syntax && (syntax.type === 'Comma' || syntax.type === 'Multiplier' && child.token === ',')) {
            continue;
        }

        const value = serializeMatch(child);

        if (value) {
            components.push(value);
        }
    }

    return components;
}

function collectByProperty(matchNode, rootName) {
    const result = {};

    function walkMatch(node, insideOtherProperty) {
        const syntax = node.syntax;

        if (
            syntax &&
            syntax.type === 'Property' &&
            syntax.name !== rootName &&
            !insideOtherProperty
        ) {
            const value = serializeMatch(node);

            if (value) {
                if (!result[syntax.name]) {
                    result[syntax.name] = [];
                }

                result[syntax.name].push(value);
            }

            return;
        }

        if (Array.isArray(node.match)) {
            const nextInside = insideOtherProperty || (
                syntax &&
                syntax.type === 'Property' &&
                syntax.name !== rootName
            );

            for (let i = 0; i < node.match.length; i++) {
                walkMatch(node.match[i], nextInside);
            }
        }
    }

    if (matchNode) {
        walkMatch(matchNode, false);
    }

    return result;
}

function collectByType(matchNode, typeNames) {
    const result = {};

    function walkMatch(node, insideTarget) {
        const syntax = node.syntax;

        if (
            !insideTarget &&
            syntax &&
            syntax.type === 'Type' &&
            Object.prototype.hasOwnProperty.call(typeNames, syntax.name)
        ) {
            const value = serializeMatch(node);

            if (value) {
                if (!result[syntax.name]) {
                    result[syntax.name] = [];
                }

                result[syntax.name].push(value);
            }

            return;
        }

        if (Array.isArray(node.match)) {
            for (let i = 0; i < node.match.length; i++) {
                walkMatch(node.match[i], insideTarget);
            }
        }
    }

    if (matchNode) {
        walkMatch(matchNode, false);
    }

    return result;
}

function findTypeNodes(matchNode, typeName, found = []) {
    if (!matchNode) {
        return found;
    }

    if (matchNode.syntax && matchNode.syntax.type === 'Type' && matchNode.syntax.name === typeName) {
        found.push(matchNode);
        return found;
    }

    if (Array.isArray(matchNode.match)) {
        for (let i = 0; i < matchNode.match.length; i++) {
            findTypeNodes(matchNode.match[i], typeName, found);
        }
    }

    return found;
}

function hasKeyword(matchNode, keyword) {
    if (!matchNode) {
        return false;
    }

    if (
        matchNode.syntax &&
        matchNode.syntax.type === 'Keyword' &&
        matchNode.syntax.name === keyword
    ) {
        return true;
    }

    if (Array.isArray(matchNode.match)) {
        for (let i = 0; i < matchNode.match.length; i++) {
            if (hasKeyword(matchNode.match[i], keyword)) {
                return true;
            }
        }
    }

    return false;
}

function normalizeValue(value) {
    return String(value).trim().replace(/\s+/g, ' ').toLowerCase();
}

function valuesEqual(a, b) {
    return normalizeValue(a) === normalizeValue(b);
}

function distributeBox(values) {
    const a = values[0];
    const b = values[1];
    const c = values[2];
    const d = values[3];

    switch (values.length) {
        case 1:
            return [a, a, a, a];
        case 2:
            return [a, b, a, b];
        case 3:
            return [a, b, c, b];
        case 4:
            return [a, b, c, d];
        default:
            return null;
    }
}

function compressBox(values) {
    const top = values[0];
    const right = values[1];
    const bottom = values[2];
    const left = values[3];

    if (valuesEqual(top, bottom) && valuesEqual(right, left)) {
        if (valuesEqual(top, right)) {
            return top;
        }

        return `${top} ${right}`;
    }

    if (valuesEqual(right, left)) {
        return `${top} ${right} ${bottom}`;
    }

    return `${top} ${right} ${bottom} ${left}`;
}

function splitTopLevel(value, separator) {
    const parts = [];
    let current = '';
    let depth = 0;
    let quote = null;

    for (let i = 0; i < value.length; i++) {
        const ch = value[i];

        if (quote) {
            current += ch;

            if (ch === '\\' && i + 1 < value.length) {
                current += value[++i];
            } else if (ch === quote) {
                quote = null;
            }

            continue;
        }

        if (ch === '"' || ch === '\'') {
            quote = ch;
            current += ch;
            continue;
        }

        if (ch === '(') {
            depth++;
        } else if (ch === ')') {
            depth = Math.max(0, depth - 1);
        } else if (depth === 0) {
            if (separator === ',' && ch === ',') {
                parts.push(current.trim());
                current = '';
                continue;
            }

            if (separator === ' ' && /[ \t\n\r\f]/.test(ch)) {
                if (current.trim()) {
                    parts.push(current.trim());
                    current = '';
                }

                continue;
            }
        }

        current += ch;
    }

    if (current.trim()) {
        parts.push(current.trim());
    }

    return parts;
}

function splitCommaSeparated(value) {
    if (value == null) {
        return [];
    }

    return splitTopLevel(String(value), ',');
}

function splitSpaceSeparated(value) {
    if (value == null) {
        return [];
    }

    return splitTopLevel(String(value), ' ');
}

function mapLonghands(longhands, values) {
    const result = {};

    for (let i = 0; i < longhands.length; i++) {
        result[longhands[i]] = values[i];
    }

    return result;
}

function fillOmitted(longhands, assigned) {
    const result = {};

    for (let i = 0; i < longhands.length; i++) {
        const name = longhands[i];
        result[name] = Object.prototype.hasOwnProperty.call(assigned, name)
            ? assigned[name]
            : getInitialValue(name);
    }

    return result;
}

function joinList(values) {
    return values.join(', ');
}

function expandBox(info, matchNode) {
    const components = collectDirectComponents(matchNode).filter(value => value !== '/');
    const distributed = distributeBox(components);

    if (!distributed) {
        return null;
    }

    return mapLonghands(info.longhands, distributed);
}

function expandPair(info, matchNode) {
    const components = collectDirectComponents(matchNode).filter(value => value !== '/');

    if (components.length === 1) {
        return mapLonghands(info.longhands, [components[0], components[0]]);
    }

    if (components.length >= 2) {
        return mapLonghands(info.longhands, [components[0], components[1]]);
    }

    return fillOmitted(info.longhands, {});
}

function expandRadius(info, matchNode) {
    const components = collectDirectComponents(matchNode);
    const slash = components.indexOf('/');
    let horizontals;
    let verticals;

    if (slash === -1) {
        horizontals = components;
        verticals = null;
    } else {
        horizontals = components.slice(0, slash);
        verticals = components.slice(slash + 1);
    }

    const h = distributeBox(horizontals);

    if (!h) {
        return null;
    }

    const v = verticals && verticals.length
        ? distributeBox(verticals)
        : h;

    if (!v) {
        return null;
    }

    const corners = h.map((horizontal, index) => (
        valuesEqual(horizontal, v[index])
            ? horizontal
            : `${horizontal} ${v[index]}`
    ));

    return mapLonghands(info.longhands, corners);
}

function expandComponents(info, matchNode) {
    const assigned = {};
    const byProperty = collectByProperty(matchNode, info.name);

    for (let i = 0; i < info.longhands.length; i++) {
        const name = info.longhands[i];

        if (Object.prototype.hasOwnProperty.call(byProperty, name) && byProperty[name].length) {
            assigned[name] = byProperty[name].join(' ');
        }
    }

    if (info.typeMap) {
        const byType = collectByType(matchNode, info.typeMap);
        const typeNames = Object.keys(info.typeMap);

        for (let i = 0; i < typeNames.length; i++) {
            const typeName = typeNames[i];
            const longhand = info.typeMap[typeName];

            if (!Object.prototype.hasOwnProperty.call(assigned, longhand) &&
                Object.prototype.hasOwnProperty.call(byType, typeName) &&
                byType[typeName].length) {
                assigned[longhand] = byType[typeName].join(' ');
            }
        }
    }

    return fillOmitted(info.longhands, assigned);
}

function expandFlex(info, matchNode) {
    const byProperty = collectByProperty(matchNode, info.name);
    const assigned = {};

    if (hasKeyword(matchNode, 'none') &&
        !Object.prototype.hasOwnProperty.call(byProperty, 'flex-grow') &&
        !Object.prototype.hasOwnProperty.call(byProperty, 'flex-basis')) {
        assigned['flex-grow'] = '0';
        assigned['flex-shrink'] = '0';
        assigned['flex-basis'] = 'auto';
        return mapLonghands(info.longhands, [
            assigned['flex-grow'],
            assigned['flex-shrink'],
            assigned['flex-basis']
        ]);
    }

    if (byProperty['flex-grow']) {
        assigned['flex-grow'] = byProperty['flex-grow'][0];
    }

    if (byProperty['flex-shrink']) {
        assigned['flex-shrink'] = byProperty['flex-shrink'][0];
    }

    if (byProperty['flex-basis']) {
        assigned['flex-basis'] = byProperty['flex-basis'][0];
    }

    if (!assigned['flex-grow']) {
        assigned['flex-grow'] = '1';
    }

    if (!assigned['flex-shrink']) {
        assigned['flex-shrink'] = '1';
    }

    if (!assigned['flex-basis']) {
        assigned['flex-basis'] = '0';
    }

    return mapLonghands(info.longhands, [
        assigned['flex-grow'],
        assigned['flex-shrink'],
        assigned['flex-basis']
    ]);
}

function expandFont(info, matchNode) {
    const assigned = {};
    const system = findTypeNodes(matchNode, 'system-family-name');
    const nonStandard = findTypeNodes(matchNode, '-non-standard-font');

    if (system.length || nonStandard.length) {
        const family = serializeMatch(system[0] || nonStandard[0]);

        return fillOmitted(info.longhands, {
            'font-family': family
        });
    }

    const byProperty = collectByProperty(matchNode, info.name);
    const byType = collectByType(matchNode, {
        'font-variant-css2': true,
        'font-width-css3': true
    });

    if (byProperty['font-style']) {
        assigned['font-style'] = byProperty['font-style'][0];
    }

    if (byType['font-variant-css2']) {
        assigned['font-variant'] = byType['font-variant-css2'][0];
    }

    if (byProperty['font-weight']) {
        assigned['font-weight'] = byProperty['font-weight'][0];
    }

    if (byType['font-width-css3']) {
        assigned['font-stretch'] = byType['font-width-css3'][0];
    }

    if (byProperty['font-size']) {
        assigned['font-size'] = byProperty['font-size'][0];
    }

    if (byProperty['line-height']) {
        assigned['line-height'] = byProperty['line-height'][0];
    }

    if (byProperty['font-family']) {
        assigned['font-family'] = joinList(byProperty['font-family']);
    }

    return fillOmitted(info.longhands, assigned);
}

function expandBackgroundLayer(layerNode) {
    const byType = collectByType(layerNode, {
        'bg-image': true,
        'bg-position': true,
        'bg-size': true,
        'repeat-style': true,
        attachment: true,
        'visual-box': true
    });
    const byProperty = collectByProperty(layerNode, 'background');
    const boxes = byType['visual-box'] || [];
    let origin;
    let clip;

    if (boxes.length >= 2) {
        origin = boxes[0];
        clip = boxes[1];
    } else if (boxes.length === 1) {
        origin = boxes[0];
        clip = boxes[0];
    }

    return {
        'background-image': byType['bg-image'] ? byType['bg-image'][0] : getInitialValue('background-image'),
        'background-position': byType['bg-position'] ? byType['bg-position'][0] : getInitialValue('background-position'),
        'background-size': byType['bg-size'] ? byType['bg-size'][0] : getInitialValue('background-size'),
        'background-repeat': byType['repeat-style'] ? byType['repeat-style'][0] : getInitialValue('background-repeat'),
        'background-origin': origin || getInitialValue('background-origin'),
        'background-clip': clip || getInitialValue('background-clip'),
        'background-attachment': byType.attachment ? byType.attachment[0] : getInitialValue('background-attachment'),
        'background-color': byProperty['background-color']
            ? byProperty['background-color'][0]
            : null
    };
}

function expandBackground(info, matchNode) {
    const layers = [
        ...findTypeNodes(matchNode, 'bg-layer'),
        ...findTypeNodes(matchNode, 'final-bg-layer')
    ];
    const sourceLayers = layers.length ? layers : [matchNode];
    const expandedLayers = sourceLayers.map(expandBackgroundLayer);
    const result = {};
    const listProps = info.longhands.filter(name => name !== 'background-color');

    for (const name of listProps) {
        result[name] = joinList(expandedLayers.map(layer => layer[name]));
    }

    const lastColor = expandedLayers[expandedLayers.length - 1]['background-color'];
    result['background-color'] = lastColor || getInitialValue('background-color');

    return result;
}

function expandMatched(info, matchNode) {
    switch (info.kind) {
        case 'box':
            return expandBox(info, matchNode);
        case 'pair':
            return expandPair(info, matchNode);
        case 'radius':
            return expandRadius(info, matchNode);
        case 'components':
            return expandComponents(info, matchNode);
        case 'flex':
            return expandFlex(info, matchNode);
        case 'font':
            return expandFont(info, matchNode);
        case 'background':
            return expandBackground(info, matchNode);
        default:
            return null;
    }
}

function keywordFromLonghands(lexer, info, longhands) {
    let keyword = null;

    for (let i = 0; i < info.longhands.length; i++) {
        const name = info.longhands[i];

        if (!Object.prototype.hasOwnProperty.call(longhands, name) || longhands[name] == null) {
            return { incomplete: true };
        }

        const value = String(longhands[name]).trim();

        if (!isCssWideKeyword(lexer, value)) {
            return { mixed: true };
        }

        if (keyword === null) {
            keyword = value.trim();
        } else if (keyword.toLowerCase() !== value.toLowerCase()) {
            return { conflict: true };
        }
    }

    return { keyword };
}

function readLonghand(longhands, name) {
    if (!Object.prototype.hasOwnProperty.call(longhands, name) || longhands[name] == null) {
        return null;
    }

    return String(longhands[name]).trim();
}

function compressRadius(info, longhands) {
    const corners = [];

    for (let i = 0; i < info.longhands.length; i++) {
        const value = readLonghand(longhands, info.longhands[i]);

        if (value === null) {
            return null;
        }

        corners.push(value);
    }

    const pairs = corners.map(value => {
        const parts = splitSpaceSeparated(value);

        if (parts.length >= 2) {
            return [parts[0], parts[1]];
        }

        return [parts[0], parts[0]];
    });

    const horizontals = pairs.map(pair => pair[0]);
    const verticals = pairs.map(pair => pair[1]);
    const h = compressBox(horizontals);
    const allEqual = pairs.every(pair => valuesEqual(pair[0], pair[1]));

    if (allEqual) {
        return h;
    }

    return `${h}/${compressBox(verticals)}`;
}

function compressBackground(info, longhands) {
    const lists = {};
    let layerCount = 1;

    for (let i = 0; i < info.longhands.length; i++) {
        const name = info.longhands[i];
        const value = readLonghand(longhands, name);

        if (value === null) {
            return null;
        }

        if (name === 'background-color') {
            lists[name] = [value];
        } else {
            const parts = splitCommaSeparated(value);
            lists[name] = parts.length ? parts : [value];
            layerCount = Math.max(layerCount, lists[name].length);
        }
    }

    const layers = [];

    for (let i = 0; i < layerCount; i++) {
        const image = lists['background-image'][i] || lists['background-image'][i % lists['background-image'].length];
        const position = lists['background-position'][i] || lists['background-position'][i % lists['background-position'].length];
        const size = lists['background-size'][i] || lists['background-size'][i % lists['background-size'].length];
        const repeat = lists['background-repeat'][i] || lists['background-repeat'][i % lists['background-repeat'].length];
        const origin = lists['background-origin'][i] || lists['background-origin'][i % lists['background-origin'].length];
        const clip = lists['background-clip'][i] || lists['background-clip'][i % lists['background-clip'].length];
        const attachment = lists['background-attachment'][i] || lists['background-attachment'][i % lists['background-attachment'].length];
        const parts = [
            image,
            `${position}/${size}`,
            repeat,
            origin,
            clip,
            attachment
        ];

        if (i === layerCount - 1) {
            parts.push(lists['background-color'][0]);
        }

        layers.push(parts.join(' '));
    }

    return layers.join(', ');
}

function compressFont(info, longhands) {
    const values = [];

    for (let i = 0; i < info.longhands.length; i++) {
        const value = readLonghand(longhands, info.longhands[i]);

        if (value === null) {
            return null;
        }

        values.push(value);
    }

    const [
        fontStyle,
        fontVariant,
        fontWeight,
        fontStretch,
        fontSize,
        lineHeight,
        fontFamily
    ] = values;

    if (
        SYSTEM_FONTS.has(fontFamily.toLowerCase()) &&
        valuesEqual(fontStyle, getInitialValue('font-style')) &&
        valuesEqual(fontVariant, getInitialValue('font-variant')) &&
        valuesEqual(fontWeight, getInitialValue('font-weight')) &&
        valuesEqual(fontStretch, getInitialValue('font-stretch')) &&
        valuesEqual(fontSize, getInitialValue('font-size')) &&
        valuesEqual(lineHeight, getInitialValue('line-height'))
    ) {
        return fontFamily;
    }

    return [
        fontStyle,
        fontVariant,
        fontWeight,
        fontStretch,
        `${fontSize}/${lineHeight}`,
        fontFamily
    ].join(' ');
}

function compressGeneric(info, longhands) {
    const values = [];

    for (let i = 0; i < info.longhands.length; i++) {
        const value = readLonghand(longhands, info.longhands[i]);

        if (value === null) {
            return null;
        }

        values.push(value);
    }

    if (info.kind === 'box') {
        return compressBox(values);
    }

    if (info.kind === 'pair') {
        return valuesEqual(values[0], values[1]) ? values[0] : values.join(' ');
    }

    return values.join(' ');
}

export function expandShorthand(lexer, propertyName, value) {
    if (value == null) {
        return null;
    }

    const info = getShorthandInfo(lexer, propertyName);

    if (!info) {
        return null;
    }

    const match = lexer.matchProperty(propertyName, value);

    if (!match || !match.matched) {
        return null;
    }

    const serialized = valueToString(lexer, value);

    if (serialized && isCssWideKeyword(lexer, serialized)) {
        return mapLonghands(info.longhands, info.longhands.map(() => serialized.trim()));
    }

    return expandMatched(info, match.matched);
}

export function compressShorthand(lexer, propertyName, longhands) {
    if (!longhands || typeof longhands !== 'object') {
        return null;
    }

    const info = getShorthandInfo(lexer, propertyName);

    if (!info) {
        return null;
    }

    const wide = keywordFromLonghands(lexer, info, longhands);

    if (wide.incomplete || wide.conflict) {
        return null;
    }

    if (wide.keyword) {
        return wide.keyword;
    }

    switch (info.kind) {
        case 'radius':
            return compressRadius(info, longhands);
        case 'background':
            return compressBackground(info, longhands);
        case 'font':
            return compressFont(info, longhands);
        default:
            return compressGeneric(info, longhands);
    }
}
