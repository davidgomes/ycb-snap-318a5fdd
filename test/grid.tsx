import React from 'react';
import test from 'ava';
import {Box, Text} from '../src/index.js';
import {
	renderToString,
	renderToStringAsync,
} from './helpers/render-to-string.js';

test('display grid places fixed columns', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="4 4">
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'A   B');
});

test('display grid creates rows when gridTemplateRows is omitted', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="1 1">
			<Text>A</Text>
			<Text>B</Text>
			<Text>C</Text>
		</Box>,
	);

	t.is(output, 'AB\nC');
});

test('fractional columns share width', t => {
	const output = renderToString(
		<Box display="grid" width={10} gridTemplateColumns="1fr 1fr">
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'A    B');
});

test('minmax fr maximums share leftover space proportionally', t => {
	const output = renderToString(
		<Box
			display="grid"
			width={20}
			gridTemplateColumns="minmax(2, 1fr) minmax(2, 3fr)"
		>
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'A     B');
});

test('minmax clamps content between a fixed minimum and maximum', t => {
	const output = renderToString(
		<Box display="grid" width={20} gridTemplateColumns="minmax(5, 8) 1">
			<Text>AB</Text>
			<Text>C</Text>
		</Box>,
	);

	t.is(output, 'AB   C');
});

test('auto tracks size to content', t => {
	const output = renderToString(
		<Box display="grid" width={10} gridTemplateColumns="auto 4">
			<Text>AA</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'AAB');
});

test('gap, columnGap, and rowGap apply between tracks', t => {
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

test('gap shorthand sets both track gaps', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="1 1" gap={1}>
			<Text>A</Text>
			<Text>B</Text>
			<Text>C</Text>
		</Box>,
	);

	t.is(output, 'A B\n\nC');
});

test('gridColumn and gridRow place children on explicit lines', t => {
	const output = renderToString(
		<Box display="grid" width={6} gridTemplateColumns="2 2 2">
			<Box gridColumn={3}>
				<Text>C</Text>
			</Box>
			<Box gridColumn={1}>
				<Text>A</Text>
			</Box>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'A B C');
});

test('gridColumn start / end spans columns', t => {
	const output = renderToString(
		<Box display="grid" width={3} gridTemplateColumns="1 1 1">
			<Box gridColumn="1 / 3">
				<Text>A</Text>
			</Box>
			<Text>B</Text>
			<Text>C</Text>
		</Box>,
	);

	t.is(output, 'A B\nC');
});

test('gridRow start / end spans rows and blocks those cells', t => {
	const output = renderToString(
		<Box display="grid" width={4} gridTemplateColumns="2 2">
			<Box gridRow="1 / 3">
				<Text>A</Text>
			</Box>
			<Text>B</Text>
			<Text>C</Text>
		</Box>,
	);

	t.is(output, 'A B\n  C');
});

test('fixed row heights and implicit auto rows', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="2 2" gridTemplateRows="2">
			<Text>A</Text>
			<Text>B</Text>
			<Text>C</Text>
		</Box>,
	);

	t.is(output, 'A B\n\nC');
});

test('text wraps inside a fixed column', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="5">
			<Text>Hello!</Text>
		</Box>,
	);

	t.is(output, 'Hello\n!');
});

test('fractional rows share a fixed height', t => {
	const output = renderToString(
		<Box
			display="grid"
			width={4}
			height={4}
			gridTemplateColumns="4"
			gridTemplateRows="minmax(1, 1fr) minmax(1, 1fr)"
		>
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'A\n\nB\n');
});

test('padding offsets grid tracks', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="1 1" paddingLeft={2}>
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, '  AB');
});

test('display none children do not occupy cells', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="1 1">
			<Box display="none">
				<Text>A</Text>
			</Box>
			<Text>B</Text>
			<Text>C</Text>
		</Box>,
	);

	t.is(output, 'BC');
});

test('nested grids honor the parent cell width', t => {
	const output = renderToString(
		<Box display="grid" width={20} gridTemplateColumns="10 10">
			<Box display="grid" gridTemplateColumns="1fr 1fr">
				<Text>A</Text>
				<Text>B</Text>
			</Box>
			<Text>C</Text>
		</Box>,
	);

	t.is(output, 'A    B    C');
});

test('display grid - concurrent', async t => {
	const output = await renderToStringAsync(
		<Box display="grid" width={10} gridTemplateColumns="1fr 1fr">
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'A    B');
});
