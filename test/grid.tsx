import React from 'react';
import test from 'ava';
import {Box, Text, renderToString} from '../src/index.js';

test('lays out fixed and fractional columns with automatic rows', t => {
	const output = renderToString(
		<Box display="grid" width={10} gridTemplateColumns="4 1fr" gap={1}>
			<Box>
				<Text>A</Text>
			</Box>
			<Box>
				<Text>B</Text>
			</Box>
			<Box>
				<Text>C</Text>
			</Box>
		</Box>,
	);

	t.is(output, 'A    B\n\nC');
});

test('sizes auto and minmax tracks', t => {
	const autoOutput = renderToString(
		<Box display="grid" width={8} gridTemplateColumns="auto 1fr">
			<Box>
				<Text>AA</Text>
			</Box>
			<Box>
				<Text>B</Text>
			</Box>
		</Box>,
	);
	const minmaxOutput = renderToString(
		<Box display="grid" width={10} gridTemplateColumns="minmax(4, 1fr) 1fr">
			<Box>
				<Text>A</Text>
			</Box>
			<Box>
				<Text>B</Text>
			</Box>
		</Box>,
	);

	t.is(autoOutput, 'AA B');
	t.is(minmaxOutput, 'A      B');
});

test('places children by grid row and column', t => {
	const output = renderToString(
		<Box display="grid" width={8} gridTemplateColumns="3 3">
			<Box gridColumn={2}>
				<Text>B</Text>
			</Box>
			<Box gridColumn="1 / 2" gridRow={2}>
				<Text>A</Text>
			</Box>
		</Box>,
	);

	t.is(output, '   B\nA');
});
