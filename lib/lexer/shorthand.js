const WIDE = new Set(['inherit', 'initial', 'unset', 'revert', 'revert-layer']);

const BOX = {
    margin: [['margin-top', 'margin-right', 'margin-bottom', 'margin-left'], '0'],
    padding: [['padding-top', 'padding-right', 'padding-bottom', 'padding-left'], '0'],
    inset: [['top', 'right', 'bottom', 'left'], 'auto'],
    'border-radius': [
        ['border-top-left-radius', 'border-top-right-radius', 'border-bottom-right-radius', 'border-bottom-left-radius'],
        '0'
    ]
};

const DEFINITIONS = {
    border: [['border-width', 'border-style', 'border-color'], ['medium', 'none', 'currentcolor']],
    'border-top': [['border-top-width', 'border-top-style', 'border-top-color'], ['medium', 'none', 'currentcolor']],
    'border-right': [['border-right-width', 'border-right-style', 'border-right-color'], ['medium', 'none', 'currentcolor']],
    'border-bottom': [['border-bottom-width', 'border-bottom-style', 'border-bottom-color'], ['medium', 'none', 'currentcolor']],
    'border-left': [['border-left-width', 'border-left-style', 'border-left-color'], ['medium', 'none', 'currentcolor']],
    outline: [['outline-width', 'outline-style', 'outline-color'], ['medium', 'none', 'currentcolor']],
    overflow: [['overflow-x', 'overflow-y'], ['visible', 'visible']],
    gap: [['row-gap', 'column-gap'], ['normal', 'normal']],
    flex: [['flex-grow', 'flex-shrink', 'flex-basis'], ['1', '1', '0%']],
    'flex-flow': [['flex-direction', 'flex-wrap'], ['row', 'nowrap']],
    'text-decoration': [
        ['text-decoration-line', 'text-decoration-style', 'text-decoration-color', 'text-decoration-thickness'],
        ['none', 'solid', 'currentcolor', 'auto']
    ],
    'list-style': [['list-style-type', 'list-style-position', 'list-style-image'], ['disc', 'outside', 'none']],
    background: [
        ['background-image', 'background-position', 'background-size', 'background-repeat',
            'background-origin', 'background-clip', 'background-attachment', 'background-color'],
        ['none', '0% 0%', 'auto', 'repeat', 'padding-box', 'border-box', 'scroll', 'transparent']
    ],
    font: [
        ['font-style', 'font-variant', 'font-weight', 'font-stretch', 'font-size', 'line-height', 'font-family'],
        ['normal', 'normal', 'normal', 'normal', 'medium', 'normal', 'serif']
    ]
};

function words(value) {
    return String(value).trim().split(/\s+/).filter(Boolean);
}

function splitLayers(value) {
    const result = [];
    let start = 0;
    let depth = 0;
    for (let i = 0; i < value.length; i++) {
        if (value[i] === '(') depth++;
        else if (value[i] === ')') depth--;
        else if (value[i] === ',' && depth === 0) {
            result.push(value.slice(start, i).trim());
            start = i + 1;
        }
    }
    result.push(value.slice(start).trim());
    return result;
}

function expandBox(names, initial, value) {
    const values = words(value);
    if (values.length < 1 || values.length > 4) return null;
    const four = values.length === 1
        ? [values[0], values[0], values[0], values[0]]
        : values.length === 2
            ? [values[0], values[1], values[0], values[1]]
            : values.length === 3
                ? [values[0], values[1], values[2], values[1]]
                : values;
    return Object.fromEntries(names.map((name, i) => [name, four[i] || initial]));
}

function compressBox(names, longhands) {
    const values = names.map(name => longhands[name]);
    if (values.some(value => typeof value !== 'string')) return null;
    if (values.every(value => value === values[0])) return values[0];
    if (values[0] === values[2] && values[1] === values[3]) {
        return values[0] === values[1] ? values[0] : `${values[0]} ${values[1]}`;
    }
    if (values[1] === values[3]) return `${values[0]} ${values[1]} ${values[2]}`;
    return values.join(' ');
}

function expandGeneric(names, initials, value) {
    const layerValues = splitLayers(String(value));
    const result = Object.fromEntries(names.map((name, i) => [name, initials[i]]));
    for (const layer of layerValues) {
        const parts = words(layer);
        if (!parts.length) return null;
        for (let i = 0; i < Math.min(parts.length, names.length); i++) {
            result[names[i]] = result[names[i]] === initials[i]
                ? parts[i]
                : `${result[names[i]]}, ${parts[i]}`;
        }
    }
    return result;
}

export function expandShorthand(lexer, propertyName, value) {
    const property = String(propertyName).toLowerCase();
    if (WIDE.has(String(value).trim().toLowerCase())) {
        const definition = BOX[property] || DEFINITIONS[property];
        return definition ? Object.fromEntries(definition[0].map(name => [name, String(value).trim()])) : null;
    }
    const box = BOX[property];
    if (box) return expandBox(box[0], box[1], value);
    const definition = DEFINITIONS[property];
    if (!definition) return null;
    if (property === 'background') return expandGeneric(definition[0], definition[1], value);
    const parts = words(value);
    if (!parts.length || parts.length > definition[0].length) return null;
    const result = Object.fromEntries(definition[0].map((name, i) => [name, definition[1][i]]));
    parts.forEach((part, i) => { result[definition[0][i]] = part; });
    return result;
}

export function compressShorthand(propertyName, longhands) {
    const property = String(propertyName).toLowerCase();
    const definition = BOX[property] || DEFINITIONS[property];
    if (!definition || !longhands || definition[0].some(name => typeof longhands[name] !== 'string')) return null;
    const names = definition[0];
    const values = names.map(name => longhands[name]);
    if (values.every(value => WIDE.has(value.toLowerCase()))) {
        return values.every(value => value.toLowerCase() === values[0].toLowerCase()) ? values[0] : null;
    }
    if (values.some(value => WIDE.has(value.toLowerCase()))) return null;
    if (BOX[property]) return compressBox(names, longhands);
    if (property === 'background') {
        return names.map(name => longhands[name]).filter(Boolean).join(' / ');
    }
    return names.map(name => longhands[name]).join(' ');
}
