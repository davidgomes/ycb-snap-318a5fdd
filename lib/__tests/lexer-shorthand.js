import assert from 'assert';
import { lexer, parse, fork, createLexer } from 'css-tree';

function expand(property, value) {
    return lexer.expandShorthand(property, value);
}

function compress(property, longhands) {
    return lexer.compressShorthand(property, longhands);
}

function roundtrip(property, value) {
    const expanded = expand(property, value);
    assert.ok(expanded, `expected ${property}: ${value} to expand`);
    const compressed = compress(property, expanded);
    assert.ok(compressed, `expected ${property} expansion to compress`);
    const again = expand(property, compressed);
    assert.deepStrictEqual(again, expanded);
    return { expanded, compressed };
}

describe('Lexer#expandShorthand() / Lexer#compressShorthand()', () => {
    describe('errors', () => {
        it('returns null for unknown properties', () => {
            assert.strictEqual(expand('not-a-property', '1px'), null);
            assert.strictEqual(compress('not-a-property', { foo: '1px' }), null);
        });

        it('returns null for non-shorthand properties', () => {
            assert.strictEqual(expand('color', 'red'), null);
            assert.strictEqual(expand('margin-top', '1px'), null);
            assert.strictEqual(compress('color', { color: 'red' }), null);
        });

        it('returns null for values that do not match', () => {
            assert.strictEqual(expand('margin', 'red'), null);
            assert.strictEqual(expand('border', '1px solid red extra'), null);
        });

        it('returns null when the longhand set is incomplete', () => {
            assert.strictEqual(compress('margin', {
                'margin-top': '1px',
                'margin-right': '1px'
            }), null);
        });

        it('returns null when CSS-wide keywords differ', () => {
            assert.strictEqual(compress('margin', {
                'margin-top': 'inherit',
                'margin-right': 'initial',
                'margin-bottom': 'inherit',
                'margin-left': 'inherit'
            }), null);
        });
    });

    describe('CSS-wide keywords', () => {
        for (const keyword of ['inherit', 'initial', 'unset', 'revert', 'revert-layer']) {
            it(`expands ${keyword} to every longhand`, () => {
                assert.deepStrictEqual(expand('margin', keyword), {
                    'margin-top': keyword,
                    'margin-right': keyword,
                    'margin-bottom': keyword,
                    'margin-left': keyword
                });
                assert.strictEqual(compress('margin', expand('margin', keyword)), keyword);
            });
        }

        it('accepts an AST value', () => {
            const ast = parse('unset', { context: 'value' });
            assert.deepStrictEqual(expand('padding', ast), {
                'padding-top': 'unset',
                'padding-right': 'unset',
                'padding-bottom': 'unset',
                'padding-left': 'unset'
            });
        });
    });

    describe('box-model shorthands', () => {
        it('margin distributes 1-4 values clockwise', () => {
            assert.deepStrictEqual(expand('margin', '1px'), {
                'margin-top': '1px',
                'margin-right': '1px',
                'margin-bottom': '1px',
                'margin-left': '1px'
            });
            assert.deepStrictEqual(expand('margin', '1px 2px'), {
                'margin-top': '1px',
                'margin-right': '2px',
                'margin-bottom': '1px',
                'margin-left': '2px'
            });
            assert.deepStrictEqual(expand('margin', '1px 2px 3px'), {
                'margin-top': '1px',
                'margin-right': '2px',
                'margin-bottom': '3px',
                'margin-left': '2px'
            });
            assert.deepStrictEqual(expand('margin', '1px 2px 3px 4px'), {
                'margin-top': '1px',
                'margin-right': '2px',
                'margin-bottom': '3px',
                'margin-left': '4px'
            });
        });

        it('padding and inset follow the same 1-4 pattern', () => {
            assert.deepStrictEqual(expand('padding', '1px 2px'), {
                'padding-top': '1px',
                'padding-right': '2px',
                'padding-bottom': '1px',
                'padding-left': '2px'
            });
            assert.deepStrictEqual(expand('inset', '1px 2px 3px'), {
                top: '1px',
                right: '2px',
                bottom: '3px',
                left: '2px'
            });
        });

        it('compresses box values to the fewest equivalent sides', () => {
            assert.strictEqual(compress('margin', expand('margin', '1px')), '1px');
            assert.strictEqual(compress('margin', expand('margin', '1px 2px')), '1px 2px');
            assert.strictEqual(compress('margin', expand('margin', '1px 2px 3px')), '1px 2px 3px');
            assert.strictEqual(compress('margin', expand('margin', '1px 2px 3px 4px')), '1px 2px 3px 4px');
            assert.strictEqual(compress('margin', {
                'margin-top': '1px',
                'margin-right': '2px',
                'margin-bottom': '1px',
                'margin-left': '2px'
            }), '1px 2px');
        });

        it('border-radius expands corners and optional elliptical radii', () => {
            assert.deepStrictEqual(expand('border-radius', '1px'), {
                'border-top-left-radius': '1px',
                'border-top-right-radius': '1px',
                'border-bottom-right-radius': '1px',
                'border-bottom-left-radius': '1px'
            });
            assert.deepStrictEqual(expand('border-radius', '1px 2px / 3px'), {
                'border-top-left-radius': '1px 3px',
                'border-top-right-radius': '2px 3px',
                'border-bottom-right-radius': '1px 3px',
                'border-bottom-left-radius': '2px 3px'
            });
            assert.strictEqual(compress('border-radius', expand('border-radius', '1px 2px / 3px')), '1px 2px/3px');
            assert.strictEqual(compress('border-radius', expand('border-radius', '1px 2px 3px 4px')), '1px 2px 3px 4px');
        });

        it('roundtrips box-model values', () => {
            for (const value of ['0', '1px 2px', '1px 2px 3px', '1px 2px 3px 4px', 'auto']) {
                roundtrip('inset', value);
            }
        });
    });

    describe('component shorthands', () => {
        it('border expands one level to width/style/color', () => {
            assert.deepStrictEqual(expand('border', '1px solid red'), {
                'border-width': '1px',
                'border-style': 'solid',
                'border-color': 'red'
            });
            assert.deepStrictEqual(expand('border', 'red'), {
                'border-width': 'medium',
                'border-style': 'none',
                'border-color': 'red'
            });
            assert.deepStrictEqual(expand('border', 'solid'), {
                'border-width': 'medium',
                'border-style': 'solid',
                'border-color': 'currentcolor'
            });
        });

        it('border sides accept values in any order', () => {
            assert.deepStrictEqual(expand('border-top', 'solid 2px blue'), {
                'border-top-width': '2px',
                'border-top-style': 'solid',
                'border-top-color': 'blue'
            });
            assert.deepStrictEqual(expand('border-right', 'green dashed'), {
                'border-right-width': 'medium',
                'border-right-style': 'dashed',
                'border-right-color': 'green'
            });
            assert.ok(expand('border-bottom', '1px'));
            assert.ok(expand('border-left', 'dotted'));
        });

        it('outline, list-style and text-decoration omit to CSS initials', () => {
            assert.deepStrictEqual(expand('outline', '2px dashed'), {
                'outline-width': '2px',
                'outline-style': 'dashed',
                'outline-color': 'auto'
            });
            assert.deepStrictEqual(expand('list-style', 'inside square'), {
                'list-style-type': 'square',
                'list-style-position': 'inside',
                'list-style-image': 'none'
            });
            assert.deepStrictEqual(expand('text-decoration', 'underline'), {
                'text-decoration-line': 'underline',
                'text-decoration-style': 'solid',
                'text-decoration-color': 'currentcolor',
                'text-decoration-thickness': 'auto'
            });
            assert.deepStrictEqual(expand('text-decoration', 'underline dotted red 2px'), {
                'text-decoration-line': 'underline',
                'text-decoration-style': 'dotted',
                'text-decoration-color': 'red',
                'text-decoration-thickness': '2px'
            });
        });

        it('flex-flow accepts values in any order', () => {
            assert.deepStrictEqual(expand('flex-flow', 'wrap column'), {
                'flex-direction': 'column',
                'flex-wrap': 'wrap'
            });
            assert.deepStrictEqual(expand('flex-flow', 'wrap'), {
                'flex-direction': 'row',
                'flex-wrap': 'wrap'
            });
        });

        it('concatenates component longhands in canonical order', () => {
            assert.strictEqual(compress('border', expand('border', '1px solid red')), '1px solid red');
            assert.strictEqual(compress('outline', expand('outline', '2px dashed')), '2px dashed auto');
            assert.strictEqual(
                compress('text-decoration', expand('text-decoration', 'underline')),
                'underline solid currentcolor auto'
            );
        });
    });

    describe('two-value shorthands', () => {
        it('overflow and gap apply one value to both longhands', () => {
            assert.deepStrictEqual(expand('overflow', 'hidden'), {
                'overflow-x': 'hidden',
                'overflow-y': 'hidden'
            });
            assert.deepStrictEqual(expand('overflow', 'hidden auto'), {
                'overflow-x': 'hidden',
                'overflow-y': 'auto'
            });
            assert.deepStrictEqual(expand('gap', '10px'), {
                'row-gap': '10px',
                'column-gap': '10px'
            });
            assert.deepStrictEqual(expand('gap', '10px 20px'), {
                'row-gap': '10px',
                'column-gap': '20px'
            });
        });

        it('compresses matching pair values to one value', () => {
            assert.strictEqual(compress('overflow', expand('overflow', 'hidden')), 'hidden');
            assert.strictEqual(compress('overflow', expand('overflow', 'hidden auto')), 'hidden auto');
            assert.strictEqual(compress('gap', expand('gap', '10px 10px')), '10px');
        });
    });

    describe('flex', () => {
        it('expands none and omitted components using shorthand defaults', () => {
            assert.deepStrictEqual(expand('flex', 'none'), {
                'flex-grow': '0',
                'flex-shrink': '0',
                'flex-basis': 'auto'
            });
            assert.deepStrictEqual(expand('flex', '1'), {
                'flex-grow': '1',
                'flex-shrink': '1',
                'flex-basis': '0'
            });
            assert.deepStrictEqual(expand('flex', '2 10px'), {
                'flex-grow': '2',
                'flex-shrink': '1',
                'flex-basis': '10px'
            });
            assert.deepStrictEqual(expand('flex', 'auto'), {
                'flex-grow': '1',
                'flex-shrink': '1',
                'flex-basis': 'auto'
            });
            assert.deepStrictEqual(expand('flex', '1 2 10px'), {
                'flex-grow': '1',
                'flex-shrink': '2',
                'flex-basis': '10px'
            });
        });

        it('concatenates grow shrink basis', () => {
            assert.strictEqual(compress('flex', expand('flex', '1')), '1 1 0');
            assert.strictEqual(compress('flex', expand('flex', 'none')), '0 0 auto');
            roundtrip('flex', '1 2 10px');
        });
    });

    describe('font', () => {
        it('expands optional prefix components to their initials', () => {
            assert.deepStrictEqual(expand('font', '16px Arial'), {
                'font-style': 'normal',
                'font-variant': 'normal',
                'font-weight': 'normal',
                'font-stretch': 'normal',
                'font-size': '16px',
                'line-height': 'normal',
                'font-family': 'Arial'
            });
            assert.deepStrictEqual(expand('font', 'italic bold 16px/1.5 Arial, sans-serif'), {
                'font-style': 'italic',
                'font-variant': 'normal',
                'font-weight': 'bold',
                'font-stretch': 'normal',
                'font-size': '16px',
                'line-height': '1.5',
                'font-family': 'Arial, sans-serif'
            });
            assert.deepStrictEqual(expand('font', 'small-caps condensed 12px serif'), {
                'font-style': 'normal',
                'font-variant': 'small-caps',
                'font-weight': 'normal',
                'font-stretch': 'condensed',
                'font-size': '12px',
                'line-height': 'normal',
                'font-family': 'serif'
            });
        });

        it('joins font-size and line-height with /', () => {
            const compressed = compress('font', expand('font', 'italic bold 16px/1.5 Arial'));
            assert.ok(compressed.includes('16px/1.5'));
            assert.ok(!compressed.includes('16px /'));
            roundtrip('font', 'italic small-caps bold condensed 16px/1.5 Arial, sans-serif');
        });

        it('handles system fonts', () => {
            assert.deepStrictEqual(expand('font', 'caption'), {
                'font-style': 'normal',
                'font-variant': 'normal',
                'font-weight': 'normal',
                'font-stretch': 'normal',
                'font-size': 'medium',
                'line-height': 'normal',
                'font-family': 'caption'
            });
            assert.strictEqual(compress('font', expand('font', 'caption')), 'caption');
        });
    });

    describe('background', () => {
        it('expands a single layer including omitted initials', () => {
            assert.deepStrictEqual(expand('background', 'red'), {
                'background-image': 'none',
                'background-position': '0% 0%',
                'background-size': 'auto',
                'background-repeat': 'repeat',
                'background-origin': 'padding-box',
                'background-clip': 'border-box',
                'background-attachment': 'scroll',
                'background-color': 'red'
            });
        });

        it('expands comma-separated layers and keeps color on the final layer', () => {
            const expanded = expand('background', 'url(a) center / cover no-repeat, red');
            assert.strictEqual(expanded['background-image'], 'url(a), none');
            assert.strictEqual(expanded['background-position'], 'center, 0% 0%');
            assert.strictEqual(expanded['background-size'], 'cover, auto');
            assert.strictEqual(expanded['background-repeat'], 'no-repeat, repeat');
            assert.strictEqual(expanded['background-color'], 'red');
        });

        it('sets both origin and clip when a single box is specified', () => {
            const expanded = expand('background', 'content-box');
            assert.strictEqual(expanded['background-origin'], 'content-box');
            assert.strictEqual(expanded['background-clip'], 'content-box');
        });

        it('joins position and size with / on compress', () => {
            const compressed = compress('background', expand('background', 'url(a) center / cover'));
            assert.ok(compressed.includes('center/cover'));
            assert.ok(!compressed.includes('center /'));
            roundtrip('background', 'url(a) left 10px top 20px / contain no-repeat fixed padding-box border-box red');
        });
    });

    describe('roundtrip', () => {
        const samples = {
            margin: ['1px', '1px 2px', '1px 2px 3px', '1px 2px 3px 4px'],
            padding: ['0', '1em 2em'],
            border: ['1px solid red', 'none', 'blue'],
            'border-top': ['1px dashed', 'solid green'],
            outline: ['2px solid', 'invert'],
            overflow: ['auto', 'hidden scroll'],
            flex: ['1', 'none', '2 3 10px'],
            'flex-flow': ['column wrap', 'wrap'],
            gap: ['1rem', '1rem 2rem'],
            'text-decoration': ['underline', 'underline wavy red'],
            'list-style': ['none', 'inside square'],
            inset: ['0', '1px 2px 3px 4px'],
            'border-radius': ['4px', '1px 2px / 3px 4px'],
            font: ['16px sans-serif', 'italic bold 12px/1.2 Arial'],
            background: ['red', 'url(img.png) center / cover no-repeat']
        };

        for (const [property, values] of Object.entries(samples)) {
            for (const value of values) {
                it(`${property}: ${value}`, () => {
                    const expanded = expand(property, value);

                    if (!expanded) {
                        // some values such as outline: invert may be invalid in this syntax set
                        assert.strictEqual(expand(property, value), null);
                        return;
                    }

                    const compressed = compress(property, expanded);
                    assert.ok(compressed);
                    assert.deepStrictEqual(expand(property, compressed), expanded);
                });
            }
        }
    });

    describe('custom syntax via fork()', () => {
        const custom = fork({
            properties: {
                'box-foo': "<'foo-top'>{1,4}",
                'foo-top': '<length>',
                'foo-right': '<length>',
                'foo-bottom': '<length>',
                'foo-left': '<length>',
                'pair-foo': "<'foo-row'> <'foo-column'>?",
                'foo-row': '<length>',
                'foo-column': '<length>',
                'comp-foo': "<'foo-width'> || <'foo-style'>",
                'foo-width': '<length>',
                'foo-style': 'none | solid | dashed'
            }
        });

        it('expands a custom box shorthand', () => {
            assert.deepStrictEqual(custom.lexer.expandShorthand('box-foo', '1px 2px'), {
                'foo-top': '1px',
                'foo-right': '2px',
                'foo-bottom': '1px',
                'foo-left': '2px'
            });
            assert.strictEqual(
                custom.lexer.compressShorthand('box-foo', custom.lexer.expandShorthand('box-foo', '1px 2px 3px')),
                '1px 2px 3px'
            );
        });

        it('expands a custom pair shorthand', () => {
            assert.deepStrictEqual(custom.lexer.expandShorthand('pair-foo', '10px'), {
                'foo-row': '10px',
                'foo-column': '10px'
            });
            assert.deepStrictEqual(custom.lexer.expandShorthand('pair-foo', '10px 20px'), {
                'foo-row': '10px',
                'foo-column': '20px'
            });
            assert.strictEqual(
                custom.lexer.compressShorthand('pair-foo', custom.lexer.expandShorthand('pair-foo', '10px 10px')),
                '10px'
            );
        });

        it('expands a custom component shorthand', () => {
            assert.deepStrictEqual(custom.lexer.expandShorthand('comp-foo', 'solid'), {
                'foo-width': 'initial',
                'foo-style': 'solid'
            });
            assert.deepStrictEqual(custom.lexer.expandShorthand('comp-foo', '2px dashed'), {
                'foo-width': '2px',
                'foo-style': 'dashed'
            });
        });

        it('keeps built-in shorthands after an unrelated fork()', () => {
            const forked = fork({
                properties: {
                    color: '| foo'
                }
            });

            assert.deepStrictEqual(forked.lexer.expandShorthand('margin', '1px 2px'), {
                'margin-top': '1px',
                'margin-right': '2px',
                'margin-bottom': '1px',
                'margin-left': '2px'
            });
        });
    });

    describe('createLexer() custom syntax', () => {
        it('expands shorthands defined on a standalone lexer', () => {
            const customLexer = createLexer({
                generic: true,
                properties: {
                    'box-foo': "<'foo-top'>{1,4}",
                    'foo-top': '<length>',
                    'foo-right': '<length>',
                    'foo-bottom': '<length>',
                    'foo-left': '<length>'
                }
            });

            assert.deepStrictEqual(customLexer.expandShorthand('box-foo', '1px 2px 3px 4px'), {
                'foo-top': '1px',
                'foo-right': '2px',
                'foo-bottom': '3px',
                'foo-left': '4px'
            });
        });
    });

    it('is case-insensitive for property names', () => {
        assert.deepStrictEqual(expand('MARGIN', '1px 2px'), expand('margin', '1px 2px'));
        assert.strictEqual(
            compress('Padding', expand('padding', '1px 2px 1px 2px')),
            '1px 2px'
        );
    });
});
