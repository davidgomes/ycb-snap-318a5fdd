import assert from 'assert';
import { lexer, fork } from 'css-tree';

function assertRoundtrip(property, value) {
    const expanded = lexer.expandShorthand(property, value);
    assert.ok(expanded, `expand(${property}, ${JSON.stringify(value)})`);
    const compressed = lexer.compressShorthand(property, expanded);
    assert.ok(compressed, `compress(${property}) after expand`);
    const again = lexer.expandShorthand(property, compressed);
    assert.deepStrictEqual(again, expanded);
}

describe('lexer shorthand', () => {
    describe('expandShorthand()', () => {
        it('returns null for a longhand or unknown property', () => {
            assert.strictEqual(lexer.expandShorthand('color', 'red'), null);
            assert.strictEqual(lexer.expandShorthand('unknown-prop', '1px'), null);
        });

        it('returns null for a value that does not match', () => {
            assert.strictEqual(lexer.expandShorthand('margin', 'red'), null);
            assert.strictEqual(lexer.expandShorthand('flex-flow', '1px'), null);
        });

        it('maps CSS-wide keywords to every longhand', () => {
            for (const keyword of ['inherit', 'initial', 'unset', 'revert', 'revert-layer']) {
                assert.deepStrictEqual(lexer.expandShorthand('margin', keyword), {
                    'margin-top': keyword,
                    'margin-right': keyword,
                    'margin-bottom': keyword,
                    'margin-left': keyword
                });
            }
        });

        it('expands box-model shorthands clockwise', () => {
            assert.deepStrictEqual(lexer.expandShorthand('margin', '10px'), {
                'margin-top': '10px',
                'margin-right': '10px',
                'margin-bottom': '10px',
                'margin-left': '10px'
            });
            assert.deepStrictEqual(lexer.expandShorthand('padding', '1px 2px'), {
                'padding-top': '1px',
                'padding-right': '2px',
                'padding-bottom': '1px',
                'padding-left': '2px'
            });
            assert.deepStrictEqual(lexer.expandShorthand('inset', '1px 2px 3px'), {
                top: '1px',
                right: '2px',
                bottom: '3px',
                left: '2px'
            });
            assert.deepStrictEqual(lexer.expandShorthand('border-radius', '1px 2px 3px 4px'), {
                'border-top-left-radius': '1px',
                'border-top-right-radius': '2px',
                'border-bottom-right-radius': '3px',
                'border-bottom-left-radius': '4px'
            });
        });

        it('fills omitted box-model components with initial values', () => {
            assert.deepStrictEqual(lexer.expandShorthand('border-top', 'solid'), {
                'border-top-width': 'medium',
                'border-top-style': 'solid',
                'border-top-color': 'currentcolor'
            });
        });

        it('expands component shorthands in any order', () => {
            assert.deepStrictEqual(lexer.expandShorthand('outline', 'dashed 2px blue'), {
                'outline-width': '2px',
                'outline-style': 'dashed',
                'outline-color': 'blue'
            });
            assert.deepStrictEqual(lexer.expandShorthand('flex-flow', 'wrap column'), {
                'flex-direction': 'column',
                'flex-wrap': 'wrap'
            });
            assert.deepStrictEqual(lexer.expandShorthand('list-style', 'inside square'), {
                'list-style-type': 'square',
                'list-style-position': 'inside',
                'list-style-image': 'none'
            });
            assert.deepStrictEqual(lexer.expandShorthand('text-decoration', 'underline wavy red'), {
                'text-decoration-line': 'underline',
                'text-decoration-style': 'wavy',
                'text-decoration-color': 'red',
                'text-decoration-thickness': 'auto'
            });
        });

        it('expands two-value shorthands', () => {
            assert.deepStrictEqual(lexer.expandShorthand('overflow', 'hidden'), {
                'overflow-x': 'hidden',
                'overflow-y': 'hidden'
            });
            assert.deepStrictEqual(lexer.expandShorthand('overflow', 'hidden scroll'), {
                'overflow-x': 'hidden',
                'overflow-y': 'scroll'
            });
            assert.deepStrictEqual(lexer.expandShorthand('gap', '10px'), {
                'row-gap': '10px',
                'column-gap': '10px'
            });
            assert.deepStrictEqual(lexer.expandShorthand('gap', '10px 20px'), {
                'row-gap': '10px',
                'column-gap': '20px'
            });
        });

        it('expands border one level to width/style/color', () => {
            assert.deepStrictEqual(lexer.expandShorthand('border', '1px solid red'), {
                'border-width': '1px',
                'border-style': 'solid',
                'border-color': 'red'
            });
        });

        it('expands flex including omitted components', () => {
            assert.deepStrictEqual(lexer.expandShorthand('flex', 'none'), {
                'flex-grow': '0',
                'flex-shrink': '0',
                'flex-basis': 'auto'
            });
            assert.deepStrictEqual(lexer.expandShorthand('flex', '2 3 10px'), {
                'flex-grow': '2',
                'flex-shrink': '3',
                'flex-basis': '10px'
            });
            assert.deepStrictEqual(lexer.expandShorthand('flex', '1'), {
                'flex-grow': '1',
                'flex-shrink': '1',
                'flex-basis': '0%'
            });
        });

        it('expands font', () => {
            assert.deepStrictEqual(lexer.expandShorthand('font', 'italic bold 16px/1.5 Arial, sans-serif'), {
                'font-style': 'italic',
                'font-variant': 'normal',
                'font-weight': 'bold',
                'font-stretch': 'normal',
                'font-size': '16px',
                'line-height': '1.5',
                'font-family': 'Arial, sans-serif'
            });
            assert.deepStrictEqual(lexer.expandShorthand('font', 'caption'), {
                'font-style': 'caption',
                'font-variant': 'caption',
                'font-weight': 'caption',
                'font-stretch': 'caption',
                'font-size': 'caption',
                'line-height': 'caption',
                'font-family': 'caption'
            });
        });

        it('expands background including layers', () => {
            assert.deepStrictEqual(lexer.expandShorthand('background', 'red'), {
                'background-image': 'none',
                'background-position': '0% 0%',
                'background-size': 'auto',
                'background-repeat': 'repeat',
                'background-origin': 'padding-box',
                'background-clip': 'border-box',
                'background-attachment': 'scroll',
                'background-color': 'red'
            });

            const layered = lexer.expandShorthand('background', 'url(a.png), url(b.png) red');
            assert.strictEqual(layered['background-image'], 'url(a.png), url(b.png)');
            assert.strictEqual(layered['background-color'], 'red');
            assert.ok(layered['background-position'].includes(','));
        });

        it('supports custom syntax from fork()', () => {
            const custom = fork({
                properties: {
                    'foo-a': '<number>',
                    'foo-b': '<ident>',
                    foo: ' <\'foo-a\'> || <\'foo-b\'>'
                }
            });

            assert.deepStrictEqual(custom.lexer.expandShorthand('foo', 'bar 1'), {
                'foo-a': '1',
                'foo-b': 'bar'
            });
            assert.strictEqual(custom.lexer.expandShorthand('foo', '1px'), null);
        });
    });

    describe('compressShorthand()', () => {
        it('returns null for non-shorthands or incomplete sets', () => {
            assert.strictEqual(lexer.compressShorthand('color', { color: 'red' }), null);
            assert.strictEqual(lexer.compressShorthand('margin', { 'margin-top': '1px' }), null);
        });

        it('compresses box-model values to the fewest sides', () => {
            assert.strictEqual(lexer.compressShorthand('margin', {
                'margin-top': '1px',
                'margin-right': '1px',
                'margin-bottom': '1px',
                'margin-left': '1px'
            }), '1px');
            assert.strictEqual(lexer.compressShorthand('margin', {
                'margin-top': '1px',
                'margin-right': '2px',
                'margin-bottom': '1px',
                'margin-left': '2px'
            }), '1px 2px');
            assert.strictEqual(lexer.compressShorthand('margin', {
                'margin-top': '1px',
                'margin-right': '2px',
                'margin-bottom': '3px',
                'margin-left': '2px'
            }), '1px 2px 3px');
            assert.strictEqual(lexer.compressShorthand('margin', {
                'margin-top': '1px',
                'margin-right': '2px',
                'margin-bottom': '3px',
                'margin-left': '4px'
            }), '1px 2px 3px 4px');
        });

        it('compresses two-value shorthands', () => {
            assert.strictEqual(lexer.compressShorthand('overflow', {
                'overflow-x': 'hidden',
                'overflow-y': 'hidden'
            }), 'hidden');
            assert.strictEqual(lexer.compressShorthand('gap', {
                'row-gap': '10px',
                'column-gap': '20px'
            }), '10px 20px');
        });

        it('joins font-size/line-height and background-position/size with /', () => {
            const font = lexer.compressShorthand('font', {
                'font-style': 'italic',
                'font-variant': 'normal',
                'font-weight': 'bold',
                'font-stretch': 'normal',
                'font-size': '16px',
                'line-height': '1.5',
                'font-family': 'Arial'
            });
            assert.ok(font.includes('16px/1.5'));

            const bg = lexer.compressShorthand('background', lexer.expandShorthand('background', 'red'));
            assert.ok(bg.includes('/'));
        });

        it('returns a shared CSS-wide keyword and null when they differ', () => {
            assert.strictEqual(lexer.compressShorthand('margin', {
                'margin-top': 'inherit',
                'margin-right': 'inherit',
                'margin-bottom': 'inherit',
                'margin-left': 'inherit'
            }), 'inherit');
            assert.strictEqual(lexer.compressShorthand('margin', {
                'margin-top': 'inherit',
                'margin-right': 'initial',
                'margin-bottom': 'inherit',
                'margin-left': 'inherit'
            }), null);
        });
    });

    describe('expand then compress', () => {
        const samples = [
            ['margin', '10px'],
            ['margin', '10px 20px'],
            ['margin', '10px 20px 30px'],
            ['margin', '10px 20px 30px 40px'],
            ['padding', '1% 2%'],
            ['inset', '0'],
            ['border-radius', '1px 2px / 3px'],
            ['border', '1px solid red'],
            ['border-top', 'thick dotted blue'],
            ['outline', '2px dashed'],
            ['overflow', 'auto'],
            ['overflow', 'hidden scroll'],
            ['flex-flow', 'column wrap'],
            ['gap', '1rem 2rem'],
            ['text-decoration', 'underline overline solid'],
            ['list-style', 'none'],
            ['flex', '1 1 auto'],
            ['font', 'italic small-caps bold 12px/1.2 serif'],
            ['background', 'url(a.png) no-repeat center / cover']
        ];

        for (const [property, value] of samples) {
            it(`${property}: ${value}`, () => {
                assertRoundtrip(property, value);
            });
        }
    });
});
