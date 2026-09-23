import React from 'react';
import test from 'ava';
import {render, Box, Text} from '../src/index.js';
import {
	parseGridLine,
	parseTrackList,
	placeGridItems,
	resolveTrackSizes,
} from '../src/grid-layout.js';
import {
	renderToString,
	renderToStringAsync,
} from './helpers/render-to-string.js';
import createStdout from './helpers/create-stdout.js';

test('parse track list', t => {
	t.deepEqual(parseTrackList('10 auto 1fr minmax( 5 , 2fr ) minmax(2, 8)'), [
		{type: 'fixed', value: 10},
		{type: 'auto'},
		{type: 'fr', value: 1},
		{type: 'minmax', min: 5, max: {type: 'fr', value: 2}},
		{type: 'minmax', min: 2, max: {type: 'fixed', value: 8}},
	]);
});

test('ignore invalid track list', t => {
	t.deepEqual(parseTrackList('10 repeat(2, 1fr)'), []);
	t.deepEqual(parseTrackList('minmax(1fr, 10)'), []);
	t.deepEqual(parseTrackList(''), []);
	t.deepEqual(parseTrackList(undefined), []);
});

test('parse grid line', t => {
	t.deepEqual(parseGridLine(2), {start: 1, span: 1});
	t.deepEqual(parseGridLine('2'), {start: 1, span: 1});
	t.deepEqual(parseGridLine('1 / 3'), {start: 0, span: 2});
	t.deepEqual(parseGridLine('3 / 1'), {start: 0, span: 2});
	t.is(parseGridLine(undefined), undefined);
	t.is(parseGridLine(0), undefined);
	t.is(parseGridLine('a / 2'), undefined);
});

test('auto-place items around explicitly placed items', t => {
	const {areas, columnCount, rowCount} = placeGridItems(
		[
			{row: undefined, column: undefined},
			{row: {start: 0, span: 1}, column: {start: 1, span: 1}},
			{row: undefined, column: undefined},
			{row: undefined, column: undefined},
		],
		2,
		0,
	);

	t.is(columnCount, 2);
	t.is(rowCount, 2);
	t.deepEqual(
		areas.map(area => [area.rowStart, area.columnStart]),
		[
			[0, 0],
			[0, 1],
			[1, 0],
			[1, 1],
		],
	);
});

test('distribute remaining space among fr maximums after minimums', t => {
	t.deepEqual(
		resolveTrackSizes(parseTrackList('minmax(10, 1fr) 1fr'), [], 20, 0),
		[15, 5],
	);

	t.deepEqual(
		resolveTrackSizes(
			parseTrackList('minmax(2, 2fr) minmax(2, 1fr)'),
			[],
			10,
			0,
		),
		[6, 4],
	);
});

test('grow bounded minmax tracks up to their maximum', t => {
	t.deepEqual(
		resolveTrackSizes(parseTrackList('minmax(2, 5) 1fr'), [], 20, 0),
		[5, 15],
	);

	t.deepEqual(
		resolveTrackSizes(parseTrackList('minmax(2, 5) 4'), [], 7, 0),
		[3, 4],
	);
});

test('fixed columns', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="3 4">
			<Text>A</Text>
			<Text>B</Text>
			<Text>C</Text>
		</Box>,
	);

	t.is(output, 'A  B\nC');
});

test('fractional columns', t => {
	const output = renderToString(
		<Box display="grid" width={10} gridTemplateColumns="1fr 1fr">
			<Box borderStyle="single">
				<Text>A</Text>
			</Box>
			<Box borderStyle="single">
				<Text>BB</Text>
			</Box>
		</Box>,
	);

	t.is(output, ['┌───┐┌───┐', '│A  ││BB │', '└───┘└───┘'].join('\n'));
});

test('proportional fractional columns', t => {
	const output = renderToString(
		<Box display="grid" width={9} gridTemplateColumns="1fr 2fr">
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'A  B');
});

test('auto columns size to content', t => {
	const output = renderToString(
		<Box display="grid" width={20} gridTemplateColumns="auto 1fr 2">
			<Text>Name</Text>
			<Text>x</Text>
			<Text>!</Text>
			<Text>Longer</Text>
			<Text>y</Text>
			<Text>?</Text>
		</Box>,
	);

	t.is(output, 'Name  x           !\nLongery           ?');
});

test('minmax with fr maximum', t => {
	const output = renderToString(
		<Box display="grid" width={20} gridTemplateColumns="minmax(10, 1fr) 1fr">
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'A              B');
});

test('minmax with fixed maximum', t => {
	const output = renderToString(
		<Box display="grid" width={20} gridTemplateColumns="minmax(2, 5) 1fr">
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'A    B');
});

test('rows are created automatically and sized to content', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="3 3">
			<Text>A</Text>
			<Text>{'B\nB'}</Text>
			<Text>C</Text>
		</Box>,
	);

	t.is(output, 'A  B\n   B\nC');
});

test('explicit rows', t => {
	const output = renderToString(
		<Box flexDirection="column">
			<Box display="grid" height={6} gridTemplateRows="1 1fr 2">
				<Text>A</Text>
				<Text>B</Text>
				<Text>C</Text>
			</Box>
			<Text>D</Text>
		</Box>,
	);

	t.is(output, 'A\nB\n\n\nC\n\nD');
});

test('explicit rows without container height', t => {
	const output = renderToString(
		<Box flexDirection="column">
			<Box display="grid" gridTemplateRows="2 1">
				<Text>A</Text>
				<Text>B</Text>
			</Box>
			<Text>C</Text>
		</Box>,
	);

	t.is(output, 'A\n\nB\nC');
});

test('gap applies to grid tracks', t => {
	const output = renderToString(
		<Box display="grid" gap={1} gridTemplateColumns="2 2">
			<Text>A</Text>
			<Text>B</Text>
			<Text>C</Text>
			<Text>D</Text>
		</Box>,
	);

	t.is(output, 'A  B\n\nC  D');
});

test('columnGap and rowGap apply to grid tracks', t => {
	const output = renderToString(
		<Box
			display="grid"
			width={9}
			columnGap={3}
			rowGap={2}
			gridTemplateColumns="1fr 1fr"
		>
			<Text>A</Text>
			<Text>B</Text>
			<Text>C</Text>
			<Text>D</Text>
		</Box>,
	);

	t.is(output, 'A     B\n\n\nC     D');
});

test('explicit placement with single index', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="2 2 2">
			<Box gridColumn={3} gridRow={2}>
				<Text>X</Text>
			</Box>
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'A B\n    X');
});

test('explicit placement with start / end', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="2 2 2" gridTemplateRows="1 1">
			<Box
				gridColumn="1 / 3"
				borderStyle="single"
				borderTop={false}
				borderBottom={false}
			>
				<Text>AB</Text>
			</Box>
			<Box
				gridColumn={3}
				gridRow="1 / 3"
				borderStyle="single"
				borderTop={false}
				borderBottom={false}
			>
				<Text />
			</Box>
			<Text>C</Text>
		</Box>,
	);

	t.is(output, '│AB│││\nC   ││');
});

test('implicit columns are created for placements beyond the template', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="2">
			<Text>A</Text>
			<Box gridColumn={3} gridRow={1}>
				<Text>C</Text>
			</Box>
			<Box gridColumn={2} gridRow={1}>
				<Text>BB</Text>
			</Box>
		</Box>,
	);

	t.is(output, 'A BBC');
});

test('padding and border offset grid items', t => {
	const output = renderToString(
		<Box>
			<Box
				display="grid"
				borderStyle="single"
				paddingX={1}
				gridTemplateColumns="2 2"
			>
				<Text>A</Text>
				<Text>B</Text>
			</Box>
		</Box>,
	);

	t.is(output, ['┌──────┐', '│ A B  │', '└──────┘'].join('\n'));
});

test('content-sized grid container in a row', t => {
	const output = renderToString(
		<Box>
			<Box display="grid" columnGap={1} gridTemplateColumns="auto auto">
				<Text>A</Text>
				<Text>BB</Text>
			</Box>
			<Text>|</Text>
		</Box>,
	);

	t.is(output, 'A BB|');
});

test('text wraps inside grid tracks', t => {
	const output = renderToString(
		<Box display="grid" width={10} gridTemplateColumns="4 1fr">
			<Text>aa bb</Text>
			<Text>X</Text>
		</Box>,
	);

	t.is(output, 'aa  X\nbb');
});

test('items stretch to the row height', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="3 3">
			<Box borderStyle="single">
				<Text>A</Text>
			</Box>
			<Text>{'B\nB\nB\nB'}</Text>
		</Box>,
	);

	t.is(output, ['┌─┐B', '│A│B', '│ │B', '└─┘B'].join('\n'));
});

test('nested grid', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="auto 2">
			<Box display="grid" gridTemplateColumns="2 2">
				<Text>1</Text>
				<Text>2</Text>
				<Text>3</Text>
			</Box>
			<Text>R</Text>
		</Box>,
	);

	t.is(output, '1 2 R\n3');
});

test('absolutely positioned and hidden children are not grid items', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="2 2">
			<Box display="none">
				<Text>hidden</Text>
			</Box>
			<Text>A</Text>
			<Box position="absolute" marginLeft={5}>
				<Text>Z</Text>
			</Box>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'A B  Z');
});

test('switch between grid and flex layout', t => {
	function Test({isGrid}: {readonly isGrid: boolean}) {
		return (
			<Box
				display={isGrid ? 'grid' : 'flex'}
				flexDirection="column"
				gridTemplateColumns="3 3"
			>
				<Text>A</Text>
				<Text>B</Text>
			</Box>
		);
	}

	const stdout = createStdout();
	const {rerender, unmount} = render(<Test isGrid={false} />, {
		stdout,
		debug: true,
	});
	t.is((stdout.write as any).lastCall.args[0], 'A\nB');

	rerender(<Test isGrid />);
	t.is((stdout.write as any).lastCall.args[0], 'A  B');

	rerender(<Test isGrid={false} />);
	t.is((stdout.write as any).lastCall.args[0], 'A\nB');
	unmount();
});

test('relayout grid when children change', t => {
	function Test({items}: {readonly items: string[]}) {
		return (
			<Box flexDirection="column">
				<Box display="grid" gridTemplateColumns="auto auto" columnGap={1}>
					{items.map(item => (
						<Text key={item}>{item}</Text>
					))}
				</Box>
				<Text>end</Text>
			</Box>
		);
	}

	const stdout = createStdout();
	const {rerender, unmount} = render(<Test items={['a', 'b', 'c']} />, {
		stdout,
		debug: true,
	});
	t.is((stdout.write as any).lastCall.args[0], 'a b\nc\nend');

	rerender(<Test items={['a', 'bbb']} />);
	t.is((stdout.write as any).lastCall.args[0], 'a bbb\nend');
	unmount();
});

test('fixed columns - concurrent', async t => {
	const output = await renderToStringAsync(
		<Box display="grid" gridTemplateColumns="3 4">
			<Text>A</Text>
			<Text>B</Text>
			<Text>C</Text>
		</Box>,
	);

	t.is(output, 'A  B\nC');
});
