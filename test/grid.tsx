import React from 'react';
import test from 'ava';
import {Box, Text, render} from '../src/index.js';
import {renderToString} from './helpers/render-to-string.js';
import createStdout from './helpers/create-stdout.js';

test('equal fr columns', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="1fr 1fr" width={10}>
			<Text>A</Text>
			<Text>BBB</Text>
		</Box>,
	);

	t.is(output, 'A    BBB');
});

test('fixed column and fr column', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="4 1fr" width={10}>
			<Text>AB</Text>
			<Text>Z</Text>
		</Box>,
	);

	t.is(output, 'AB  Z');
});

test('auto column sizes to content', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="auto 1fr" width={12}>
			<Text>Hello</Text>
			<Text>X</Text>
		</Box>,
	);

	t.is(output, 'HelloX');
});

test('omitted row template creates rows as needed', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="auto auto">
			<Text>A</Text>
			<Text>B</Text>
			<Text>C</Text>
		</Box>,
	);

	t.is(output, 'AB\nC');
});

test('minmax fr maximums share remaining space', t => {
	const output = renderToString(
		<Box
			display="grid"
			gridTemplateColumns="minmax(6, 1fr) minmax(6, 2fr)"
			width={30}
		>
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, `${'A'.padEnd(12, ' ')}B`);
});

test('explicit rows and columns', t => {
	const output = renderToString(
		<Box
			display="grid"
			gridTemplateColumns="2 2"
			gridTemplateRows="1 1"
			width={4}
		>
			<Text>A</Text>
			<Text>B</Text>
			<Text>C</Text>
			<Text>D</Text>
		</Box>,
	);

	t.is(output, 'A B\nC D');
});

test('gridColumn and gridRow place children', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="1fr 1fr 1fr" width={9}>
			<Box gridColumn={3}>
				<Text>C</Text>
			</Box>
			<Box gridColumn="1 / 3" gridRow={2}>
				<Text>AB</Text>
			</Box>
		</Box>,
	);

	t.is(output, '      C\nAB');
});

test('gap properties apply between tracks', t => {
	const output = renderToString(
		<Box
			display="grid"
			gridTemplateColumns="auto auto"
			columnGap={2}
			rowGap={1}
		>
			<Text>A</Text>
			<Text>B</Text>
			<Text>C</Text>
			<Text>D</Text>
		</Box>,
	);

	t.is(output, 'A  B\n\nC  D');
});

test('gap shorthand applies to both axes', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="auto auto" gap={1}>
			<Text>A</Text>
			<Text>B</Text>
			<Text>C</Text>
			<Text>D</Text>
		</Box>,
	);

	t.is(output, 'A B\n\nC D');
});

test('columnGap overrides gap on columns only', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="auto auto" gap={1} columnGap={3}>
			<Text>A</Text>
			<Text>B</Text>
			<Text>C</Text>
			<Text>D</Text>
		</Box>,
	);

	t.is(output, 'A   B\n\nC   D');
});

test('text wraps inside an fr column', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="1fr 1fr" width={10}>
			<Text>ABCDEFGHIJ</Text>
			<Text>Z</Text>
		</Box>,
	);

	t.is(output, 'ABCDEZ\nFGHIJ');
});

test('nested grid uses the cell width', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="1fr" width={10}>
			<Box display="grid" gridTemplateColumns="1fr 1fr">
				<Text>A</Text>
				<Text>B</Text>
			</Box>
		</Box>,
	);

	t.is(output, 'A    B');
});

test('display none inside a grid does not occupy a cell', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="auto auto">
			<Box display="none">
				<Text>NO</Text>
			</Box>
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'AB');
});

test('fr rows share a definite height', t => {
	const output = renderToString(
		<Box
			display="grid"
			gridTemplateColumns="auto"
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

test('grid layout updates when children change', t => {
	function Test({text}: {readonly text: string}) {
		return (
			<Box display="grid" gridTemplateColumns="auto auto">
				<Text>{text}</Text>
				<Text>Z</Text>
			</Box>
		);
	}

	const stdout = createStdout();
	const {rerender} = render(<Test text="A" />, {stdout, debug: true});
	t.is((stdout.write as any).lastCall.args[0], 'AZ');

	rerender(<Test text="HELLO" />);
	t.is((stdout.write as any).lastCall.args[0], 'HELLOZ');
});

test('leaving grid restores flex layout', t => {
	function Test({grid}: {readonly grid?: boolean}) {
		return (
			<Box
				display={grid ? 'grid' : 'flex'}
				gridTemplateColumns="1fr 1fr"
				width={10}
			>
				<Text>A</Text>
				<Text>B</Text>
			</Box>
		);
	}

	const stdout = createStdout();
	const {rerender} = render(<Test grid />, {stdout, debug: true});
	t.is((stdout.write as any).lastCall.args[0], 'A    B');

	rerender(<Test />);
	t.is((stdout.write as any).lastCall.args[0], 'AB');
});

test('padding offsets grid tracks', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="1fr 1fr" width={8} padding={1}>
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, '\n A  B\n');
});
