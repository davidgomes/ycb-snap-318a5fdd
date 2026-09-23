import React from 'react';
import test from 'ava';
import {Box, Text} from '../src/index.js';
import {renderToString} from './helpers/render-to-string.js';

test('fixed columns with automatic rows', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="3 3">
			<Text>A</Text>
			<Text>B</Text>
			<Text>C</Text>
		</Box>,
	);

	t.is(output, 'A  B\nC');
});

test('fr columns with gap', t => {
	const output = renderToString(
		<Box display="grid" width={11} gridTemplateColumns="1fr 2fr" columnGap={2}>
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'A    B');
});

test('auto and minmax columns', t => {
	const output = renderToString(
		<Box display="grid" width={12} gridTemplateColumns="auto minmax(2, 1fr) 2">
			<Text>AAA</Text>
			<Text>B</Text>
			<Text>C</Text>
		</Box>,
	);

	t.is(output, 'AAAB      C');
});

test('minmax fr distributes space after minimums', t => {
	const output = renderToString(
		<Box
			display="grid"
			width={10}
			gridTemplateColumns="minmax(4, 1fr) minmax(2, 1fr)"
		>
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'A     B');
});

test('explicit placement and row gap', t => {
	const output = renderToString(
		<Box
			display="grid"
			gridTemplateColumns="2 2"
			gridTemplateRows="1 1"
			rowGap={1}
		>
			<Box gridColumn={2} gridRow={2}>
				<Text>X</Text>
			</Box>
			<Box gridColumn="1 / 3">
				<Text>YYY</Text>
			</Box>
		</Box>,
	);

	t.is(output, 'YYY\n\n  X');
});
