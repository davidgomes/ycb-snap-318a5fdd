import React from 'react';
import test from 'ava';
import {Box, Text} from '../src/index.js';
import {parseTrackList, resolveTrackSizes} from '../src/grid.js';
import {
	renderToString,
	renderToStringAsync,
} from './helpers/render-to-string.js';

test('display grid lays out fixed columns', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="4 4" width={8}>
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'A   B');
});

test('display grid wraps onto automatic rows', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="4 4" width={8}>
			<Text>A</Text>
			<Text>B</Text>
			<Text>C</Text>
		</Box>,
	);

	t.is(output, 'A   B\nC');
});

test('fractional columns share free space', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="1fr 1fr" width={10}>
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'A    B');
});

test('auto columns use content size and fr takes the rest', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="auto 1fr 1" width={8}>
			<Text>AA</Text>
			<Text>B</Text>
			<Text>C</Text>
		</Box>,
	);

	// Auto is 2, the fixed track is 1, and the fr track receives the remaining 5.
	t.is(output, 'AAB    C');
});

test('minmax fr maximums share remaining space after minimums', t => {
	const widths = resolveTrackSizes(
		parseTrackList('minmax(4, 1fr) minmax(2, 3fr) 5'),
		[],
		0,
		20,
	);

	// Minimums 4 + 2 + 5 = 11. Remaining 9 is split 1:3 across the fr maximums.
	t.deepEqual(widths, [6, 9, 5]);
});

test('minmax fr renders in proportion to the flex maximum', t => {
	const output = renderToString(
		<Box
			display="grid"
			gridTemplateColumns="minmax(2, 1fr) minmax(2, 3fr)"
			width={12}
		>
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	// Minimums are 2 and 2. Remaining 8 is split 1:3, so tracks are 4 and 8.
	t.is(output, 'A   B');
});

test('auto rows are created when gridTemplateRows is omitted', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="2" width={2}>
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'A\nB');
});

test('explicit gridColumn and gridRow place children', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="2 2 2" width={6}>
			<Box gridColumn={2}>
				<Text>B</Text>
			</Box>
			<Box gridColumn={1}>
				<Text>A</Text>
			</Box>
			<Box gridColumn="3">
				<Text>C</Text>
			</Box>
		</Box>,
	);

	t.is(output, 'A B C');
});

test('gridColumn start / end spans tracks', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="2 2 2" width={6}>
			<Box gridColumn="1 / 3">
				<Text>AB</Text>
			</Box>
			<Box gridColumn="3 / 4">
				<Text>C</Text>
			</Box>
		</Box>,
	);

	t.is(output, 'AB  C');
});

test('gridRow places an item on a later automatic row', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="1" width={1}>
			<Box gridRow={2}>
				<Text>B</Text>
			</Box>
			<Box gridRow={1}>
				<Text>A</Text>
			</Box>
		</Box>,
	);

	t.is(output, 'A\nB');
});

test('gap applies to both grid axes', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="1 1" width={3} gap={1}>
			<Text>A</Text>
			<Text>B</Text>
			<Text>C</Text>
		</Box>,
	);

	t.is(output, 'A B\n\nC');
});

test('columnGap and rowGap apply independently', t => {
	const output = renderToString(
		<Box
			display="grid"
			gridTemplateColumns="1 1"
			width={4}
			columnGap={2}
			rowGap={1}
		>
			<Text>A</Text>
			<Text>B</Text>
			<Text>C</Text>
		</Box>,
	);

	t.is(output, 'A  B\n\nC');
});

test('grid items wrap text inside a fixed column', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="5" width={5}>
			<Text>HelloWorld</Text>
		</Box>,
	);

	t.is(output, 'Hello\nWorld');
});

test('padding insets grid tracks', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="2 2" width={8} padding={1}>
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, '\n A B\n');
});

test('fractional rows share a definite height', t => {
	const output = renderToString(
		<Box
			display="grid"
			gridTemplateColumns="1"
			gridTemplateRows="1fr 1fr"
			width={1}
			height={4}
		>
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'A\n\nB\n');
});

test('nested grids size tracks inside the parent cell', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="6 6" width={12}>
			<Box display="grid" gridTemplateColumns="1fr 1fr">
				<Text>A</Text>
				<Text>B</Text>
			</Box>
			<Text>C</Text>
		</Box>,
	);

	t.is(output, 'A  B  C');
});

test('display grid does not hide the element', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="auto">
			<Text>Hi</Text>
		</Box>,
	);

	t.is(output, 'Hi');
});

test('display grid - concurrent', async t => {
	const output = await renderToStringAsync(
		<Box display="grid" gridTemplateColumns="4 4" width={8}>
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'A   B');
});
