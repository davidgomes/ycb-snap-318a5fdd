import React from 'react';
import test from 'ava';
import {Box, Text, render} from '../src/index.js';
import {renderToString} from './helpers/render-to-string.js';
import createStdout from './helpers/create-stdout.js';

test('fixed columns', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="4 3">
			<Text>A</Text>
			<Text>B</Text>
			<Text>C</Text>
			<Text>D</Text>
		</Box>,
	);

	t.is(output, 'A   B\nC   D');
});

test('fractional columns', t => {
	const output = renderToString(
		<Box display="grid" width={12} gridTemplateColumns="1fr 2fr">
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'A   B');
});

test('fractional columns share space left by fixed columns', t => {
	const output = renderToString(
		<Box display="grid" width={10} gridTemplateColumns="2 1fr 1fr">
			<Text>A</Text>
			<Text>B</Text>
			<Text>C</Text>
		</Box>,
	);

	t.is(output, 'A B   C');
});

test('auto column sizes to content', t => {
	const output = renderToString(
		<Box display="grid" width={12} gridTemplateColumns="auto 1fr">
			<Text>Name:</Text>
			<Text>Hello world</Text>
		</Box>,
	);

	t.is(output, 'Name:Hello\n     world');
});

test('auto columns stretch when there are no fractional columns', t => {
	const output = renderToString(
		<Box display="grid" width={10} gridTemplateColumns="2 auto">
			<Text>A</Text>
			<Box borderStyle="single">
				<Text>B</Text>
			</Box>
		</Box>,
	);

	t.is(output, ['A ┌──────┐', '  │B     │', '  └──────┘'].join('\n'));
});

test('minmax with fixed maximum', t => {
	const output = renderToString(
		<Box display="grid" width={20} gridTemplateColumns="minmax(2, 4) 1">
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'A   B');
});

test('minmax with fractional maximum distributes space remaining after minimums', t => {
	const output = renderToString(
		<Box
			display="grid"
			width={16}
			gridTemplateColumns="minmax(4, 1fr) minmax(6, 2fr)"
		>
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	// Remaining space is 16 - 4 - 6 = 6, split 2 / 4.
	t.is(output, 'A     B');
});

test('minmax minimum is kept when there is no remaining space', t => {
	const output = renderToString(
		<Box
			display="grid"
			width={4}
			gridTemplateColumns="minmax(3, 1fr) minmax(3, 1fr)"
		>
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'A  B');
});

test('rows are created automatically', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="2 2">
			<Text>A</Text>
			<Text>B</Text>
			<Text>C</Text>
			<Text>D</Text>
			<Text>E</Text>
		</Box>,
	);

	t.is(output, 'A B\nC D\nE');
});

test('auto rows size to the tallest item', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="2 2">
			<Text>A</Text>
			<Box flexDirection="column">
				<Text>B</Text>
				<Text>B</Text>
			</Box>
			<Text>C</Text>
		</Box>,
	);

	t.is(output, 'A B\n  B\nC');
});

test('fixed rows', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="2" gridTemplateRows="2 1">
			<Text>A</Text>
			<Text>B</Text>
			<Text>C</Text>
		</Box>,
	);

	t.is(output, 'A\n\nB\nC');
});

test('fractional rows in a container with fixed height', t => {
	const output = renderToString(
		<Box
			display="grid"
			height={6}
			gridTemplateColumns="2"
			gridTemplateRows="1fr 2fr"
		>
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'A\n\nB\n\n\n');
});

test('column and row gap', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="1 1" columnGap={2} rowGap={1}>
			<Text>A</Text>
			<Text>B</Text>
			<Text>C</Text>
			<Text>D</Text>
		</Box>,
	);

	t.is(output, 'A  B\n\nC  D');
});

test('gap applies to both columns and rows', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="1 1" gap={1}>
			<Text>A</Text>
			<Text>B</Text>
			<Text>C</Text>
			<Text>D</Text>
		</Box>,
	);

	t.is(output, 'A B\n\nC D');
});

test('gap is subtracted before distributing fractional space', t => {
	const output = renderToString(
		<Box display="grid" width={9} gridTemplateColumns="1fr 1fr" gap={1}>
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'A    B');
});

test('explicit placement with single index', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="2 2 2">
			<Box gridColumn={3}>
				<Text>A</Text>
			</Box>
			<Box gridColumn={1} gridRow={2}>
				<Text>B</Text>
			</Box>
		</Box>,
	);

	t.is(output, '    A\nB');
});

test('explicit placement with spans', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="3 3 3">
			<Box gridColumn="1 / 3" borderStyle="single">
				<Text>A</Text>
			</Box>
			<Text>B</Text>
			<Box gridColumn={3} gridRow="2 / 4" borderStyle="single">
				<Text>C</Text>
			</Box>
			<Text>D</Text>
		</Box>,
	);

	t.is(
		output,
		['┌────┐B', '│A   │', '└────┘', 'D     ┌─┐', '      │C│', '      └─┘'].join(
			'\n',
		),
	);
});

test('explicit row placement with spans', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="3 1">
			<Box gridRow="1 / 3" borderStyle="single">
				<Text>A</Text>
			</Box>
			<Text>B</Text>
			<Text>C</Text>
		</Box>,
	);

	// The spanning item's extra height is shared evenly by both rows.
	t.is(output, '┌─┐B\n│A│\n└─┘C');
});

test('auto-placed items skip occupied cells', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="2 2">
			<Box gridColumn={1} gridRow={1}>
				<Text>A</Text>
			</Box>
			<Text>B</Text>
			<Text>C</Text>
		</Box>,
	);

	t.is(output, 'A B\nC');
});

test('grid sizes to content in a row container', t => {
	const output = renderToString(
		<Box>
			<Box display="grid" gridTemplateColumns="auto auto" columnGap={1}>
				<Text>X</Text>
				<Text>YY</Text>
				<Text>ZZZ</Text>
			</Box>
			<Text>|</Text>
		</Box>,
	);

	t.is(output, 'X   YY|\nZZZ');
});

test('grid with padding and border', t => {
	const output = renderToString(
		<Box
			display="grid"
			borderStyle="single"
			paddingX={1}
			width={8}
			gridTemplateColumns="1fr 1fr"
		>
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, ['┌──────┐', '│ A B  │', '└──────┘'].join('\n'));
});

test('grid item margins', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="3 3">
			<Box marginLeft={1}>
				<Text>A</Text>
			</Box>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, ' A B');
});

test('nested grids', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="4 1">
			<Box display="grid" gridTemplateColumns="1fr 1fr">
				<Text>a</Text>
				<Text>b</Text>
			</Box>
			<Text>Z</Text>
		</Box>,
	);

	t.is(output, 'a b Z');
});

test('text wraps inside fractional columns', t => {
	const output = renderToString(
		<Box display="grid" width={14} gridTemplateColumns="1fr 1fr">
			<Text>Hello world</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'Hello  B\nworld');
});

test('ignores hidden children', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="2 2">
			<Box display="none">
				<Text>X</Text>
			</Box>
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'A B');
});

test('switches between grid and flex on rerender', t => {
	const stdout = createStdout();

	function Test({display}: {readonly display: 'flex' | 'grid'}) {
		return (
			<Box display={display} gridTemplateColumns="3 3">
				<Text>A</Text>
				<Text>B</Text>
			</Box>
		);
	}

	const {rerender} = render(<Test display="grid" />, {
		stdout,
		debug: true,
	});

	t.is(stdout.write.lastCall.args[0], 'A  B');

	rerender(<Test display="flex" />);
	t.is(stdout.write.lastCall.args[0], 'AB');

	rerender(<Test display="grid" />);
	t.is(stdout.write.lastCall.args[0], 'A  B');
});

test('updates when template changes on rerender', t => {
	const stdout = createStdout();

	function Test({columns}: {readonly columns: string}) {
		return (
			<Box display="grid" gridTemplateColumns={columns}>
				<Text>A</Text>
				<Text>B</Text>
				<Text>C</Text>
			</Box>
		);
	}

	const {rerender} = render(<Test columns="2 2 2" />, {
		stdout,
		debug: true,
	});

	t.is(stdout.write.lastCall.args[0], 'A B C');

	rerender(<Test columns="3 3" />);
	t.is(stdout.write.lastCall.args[0], 'A  B\nC');
});
