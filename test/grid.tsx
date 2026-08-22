import React from 'react';
import test from 'ava';
import {Box, Text} from '../src/index.js';
import {
	renderToString,
	renderToStringAsync,
} from './helpers/render-to-string.js';

test('display grid - two equal columns', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="1fr 1fr" width={4}>
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'A B');
});

test('display grid - implicit rows', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="1fr 1fr" width={4}>
			<Text>A</Text>
			<Text>B</Text>
			<Text>C</Text>
		</Box>,
	);

	t.is(output, 'A B\nC');
});

test('display grid - fixed tracks', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="2 2">
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'A B');
});

test('display grid - auto columns size to content', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="auto auto">
			<Text>Hello</Text>
			<Text>X</Text>
		</Box>,
	);

	t.is(output, 'HelloX');
});

test('display grid - gap applies to tracks', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="1fr 1fr" gap={1} width={5}>
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'A  B');
});

test('display grid - column and row gap', t => {
	const output = renderToString(
		<Box
			display="grid"
			gridTemplateColumns="1 1"
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

test('display grid - explicit placement', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="1fr 1fr" width={4}>
			<Box gridColumn={2}>
				<Text>B</Text>
			</Box>
			<Text>A</Text>
		</Box>,
	);

	t.is(output, 'A B');
});

test('display grid - start / end span', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="2 2 2" width={6}>
			<Box gridColumn="1 / 3">
				<Text>AB</Text>
			</Box>
			<Text>C</Text>
		</Box>,
	);

	t.is(output, 'AB  C');
});

test('display grid - explicit rows', t => {
	const output = renderToString(
		<Box
			display="grid"
			gridTemplateColumns="1fr"
			gridTemplateRows="1 1"
			height={2}
			width={1}
		>
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'A\nB');
});

test('display grid - minmax fr distributes remaining space', t => {
	const output = renderToString(
		<Box
			display="grid"
			gridTemplateColumns="minmax(1, 1fr) minmax(1, 2fr)"
			width={9}
		>
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'A  B');
});

test('display grid - minmax with fixed max', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="minmax(2, 3) auto">
			<Text>ABC</Text>
			<Text>Z</Text>
		</Box>,
	);

	t.is(output, 'ABCZ');
});

test('display grid - padding offsets items', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="1fr" padding={1} width={5}>
			<Text>A</Text>
		</Box>,
	);

	t.is(output, '\n A\n');
});

test('display grid - concurrent', async t => {
	const output = await renderToStringAsync(
		<Box display="grid" gridTemplateColumns="1fr 1fr" width={4}>
			<Text>A</Text>
			<Text>B</Text>
			<Text>C</Text>
		</Box>,
	);

	t.is(output, 'A B\nC');
});
