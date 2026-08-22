import React from 'react';
import test from 'ava';
import {Box, Text} from '../src/index.js';
import {
	renderToString,
	renderToStringAsync,
} from './helpers/render-to-string.js';

test('grid auto placement', t => {
	const output = renderToString(
		<Box display="grid" width={10} gridTemplateColumns="4 1fr">
			<Text>A</Text>
			<Text>B</Text>
			<Text>C</Text>
		</Box>,
	);

	t.is(output, 'A   B\nC');
});

test('grid gap', t => {
	const output = renderToString(
		<Box display="grid" width={5} gridTemplateColumns="2 2" gap={1}>
			<Text>A</Text>
			<Text>B</Text>
			<Text>C</Text>
		</Box>,
	);

	t.is(output, 'A  B\n\nC');
});

test('grid column and row gap', t => {
	const output = renderToString(
		<Box
			display="grid"
			width={7}
			gridTemplateColumns="2 2"
			columnGap={1}
			rowGap={1}
		>
			<Text>A</Text>
			<Text>B</Text>
			<Text>C</Text>
		</Box>,
	);

	t.is(output, 'A  B\n\nC');
});

test('grid explicit placement', t => {
	const output = renderToString(
		<Box display="grid" width={6} gridTemplateColumns="2 2 2">
			<Text gridColumn={3}>C</Text>
			<Text gridColumn={1}>A</Text>
			<Text gridColumn={2}>B</Text>
		</Box>,
	);

	t.is(output, 'A B C');
});

test('grid span placement', t => {
	const output = renderToString(
		<Box display="grid" width={6} gridTemplateColumns="2 2 2">
			<Text gridColumn="1 / 3">AB</Text>
			<Text gridColumn={3}>C</Text>
		</Box>,
	);

	t.is(output, 'AB  C');
});

test('grid row placement', t => {
	const output = renderToString(
		<Box display="grid" width={4} gridTemplateColumns="2 2">
			<Text gridRow={2}>B</Text>
			<Text gridRow={1}>A</Text>
		</Box>,
	);

	t.is(output, 'A\nB');
});

test('grid minmax with fr maximum', t => {
	const output = renderToString(
		<Box display="grid" width={10} gridTemplateColumns="minmax(2, 1fr) 4">
			<Text>AA</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'AA    B');
});

test('grid auto rows', t => {
	const output = renderToString(
		<Box display="grid" width={4} gridTemplateColumns="2 2">
			<Text>A</Text>
			<Text>B</Text>
			<Text>C</Text>
			<Text>D</Text>
			<Text>E</Text>
		</Box>,
	);

	t.is(output, 'A B\nC D\nE');
});

test('grid - concurrent', async t => {
	const output = await renderToStringAsync(
		<Box display="grid" width={10} gridTemplateColumns="4 1fr">
			<Text>A</Text>
			<Text>B</Text>
			<Text>C</Text>
		</Box>,
	);

	t.is(output, 'A   B\nC');
});
