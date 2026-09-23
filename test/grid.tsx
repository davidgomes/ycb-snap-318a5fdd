import React from 'react';
import test from 'ava';
import {Box, Text} from '../src/index.js';
import {
	renderToString,
	renderToStringAsync,
} from './helpers/render-to-string.js';

test('grid columns and automatic rows', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="1fr 1fr" width={6}>
			<Text>AB</Text>
			<Text>CD</Text>
			<Text>EF</Text>
		</Box>,
	);

	t.is(output, 'AB CD\nEF');
});

test('grid gap', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="1 1" gap={1} width={3}>
			<Text>A</Text>
			<Text>B</Text>
			<Text>C</Text>
		</Box>,
	);

	t.is(output, 'A B\n\nC');
});

test('grid explicit placement', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="1 1 1" width={3}>
			<Box gridColumn="1 / 3">
				<Text>AB</Text>
			</Box>
			<Box gridColumn={3} gridRow={1}>
				<Text>C</Text>
			</Box>
		</Box>,
	);

	t.is(output, 'ABC');
});

test('grid minmax fr distributes remaining space', t => {
	const output = renderToString(
		<Box
			display="grid"
			gridTemplateColumns="minmax(2, 1fr) minmax(2, 3fr)"
			width={8}
		>
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'A  B');
});

test('grid auto columns', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="auto auto" width={10}>
			<Text>AA</Text>
			<Text>BBB</Text>
		</Box>,
	);

	t.is(output, 'AABBB');
});

test('grid rows omitted create a new row', async t => {
	const output = await renderToStringAsync(
		<Box display="grid" gridTemplateColumns="2 2" columnGap={1} width={5}>
			<Text>A</Text>
			<Text>B</Text>
			<Text>C</Text>
		</Box>,
	);

	t.is(output, 'A  B\nC');
});
