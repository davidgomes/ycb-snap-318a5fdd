import React, {useRef} from 'react';
import test from 'ava';
import delay from 'delay';
import stripAnsi from 'strip-ansi';
import {
	Box,
	Text,
	render,
	renderToString as renderToStringStandalone,
	useBoxMetrics,
	type DOMElement,
} from '../src/index.js';
import {
	renderToString,
	renderToStringAsync,
} from './helpers/render-to-string.js';
import createStdout from './helpers/create-stdout.js';

test('fixed columns', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="3 3">
			<Text>A</Text>
			<Text>B</Text>
			<Text>C</Text>
			<Text>D</Text>
		</Box>,
	);

	t.is(output, 'A  B\nC  D');
});

test('fractional columns share the available space', t => {
	const output = renderToString(
		<Box display="grid" width={10} gridTemplateColumns="1fr 1fr">
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'A    B');
});

test('fractional columns are proportional to their factors', t => {
	const output = renderToString(
		<Box display="grid" width={9} gridTemplateColumns="1fr 2fr">
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'A  B');
});

test('fractional columns take the space left by fixed columns', t => {
	const output = renderToString(
		<Box display="grid" width={10} gridTemplateColumns="2 1fr 3">
			<Text>A</Text>
			<Text>B</Text>
			<Text>C</Text>
		</Box>,
	);

	t.is(output, 'A B    C');
});

test('auto columns fit their content', t => {
	const output = renderToString(
		<Box display="grid" width={10} gridTemplateColumns="auto 1fr">
			<Text>AAA</Text>
			<Text>B</Text>
			<Text>C</Text>
			<Text>D</Text>
		</Box>,
	);

	t.is(output, 'AAAB\nC  D');
});

test('grid without a fixed width fits its content', t => {
	const output = renderToString(
		<Box>
			<Box display="grid" gridTemplateColumns="auto auto" columnGap={1}>
				<Text>A</Text>
				<Text>BB</Text>
				<Text>CCC</Text>
				<Text>D</Text>
			</Box>
			<Text>|</Text>
		</Box>,
	);

	t.is(output, 'A   BB|\nCCC D');
});

test('fractional columns fit their content when grid has no fixed width', t => {
	const output = renderToString(
		<Box>
			<Box display="grid" gridTemplateColumns="1fr 1fr">
				<Text>A</Text>
				<Text>BBB</Text>
			</Box>
			<Text>|</Text>
		</Box>,
	);

	t.is(output, 'A  BBB|');
});

test('minmax with fixed maximum grows up to the maximum', t => {
	const output = renderToString(
		<Box display="grid" width={20} gridTemplateColumns="minmax(2, 4) 1fr">
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'A   B');
});

test('minmax with fixed maximum shares limited space', t => {
	const output = renderToString(
		<Box
			display="grid"
			width={6}
			gridTemplateColumns="minmax(2, 6) minmax(2, 6)"
		>
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'A  B');
});

test('minmax with fractional maximum distributes space remaining after minimums', t => {
	const output = renderToString(
		<Box
			display="grid"
			width={20}
			gridTemplateColumns="minmax(10, 1fr) minmax(2, 1fr)"
		>
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	// Minimums take 12, and the remaining 8 is split evenly
	t.is(output, 'A' + ' '.repeat(13) + 'B');
});

test('minmax with fractional maximum keeps minimum when there is no space left', t => {
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

test('automatic rows fit wrapped content', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="6 1">
			<Text>hello world</Text>
			<Text>X</Text>
			<Text>Y</Text>
		</Box>,
	);

	t.is(output, 'hello X\nworld\nY');
});

test('fixed rows', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="1" gridTemplateRows="2 1">
			<Text>A</Text>
			<Text>B</Text>
			<Text>C</Text>
			<Text>D</Text>
		</Box>,
	);

	t.is(output, 'A\n\nB\nC\nD');
});

test('fractional rows share the available height', t => {
	const output = renderToString(
		<Box flexDirection="column">
			<Box
				display="grid"
				height={7}
				gridTemplateColumns="1"
				gridTemplateRows="1 1fr 2fr"
			>
				<Text>A</Text>
				<Text>B</Text>
				<Text>C</Text>
			</Box>
			<Text>D</Text>
		</Box>,
	);

	t.is(output, 'A\nB\n\nC\n\n\n\nD');
});

test('gap', t => {
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

test('column gap', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="1 1" columnGap={2}>
			<Text>A</Text>
			<Text>B</Text>
			<Text>C</Text>
			<Text>D</Text>
		</Box>,
	);

	t.is(output, 'A  B\nC  D');
});

test('row gap', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="1 1" rowGap={1}>
			<Text>A</Text>
			<Text>B</Text>
			<Text>C</Text>
			<Text>D</Text>
		</Box>,
	);

	t.is(output, 'AB\n\nCD');
});

test('gap is subtracted before distributing fractional space', t => {
	const output = renderToString(
		<Box display="grid" width={11} gridTemplateColumns="1fr 1fr" gap={1}>
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'A     B');
});

test('explicit placement', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="2 2 2">
			<Text>A</Text>
			<Box gridColumn={3} gridRow={1}>
				<Text>B</Text>
			</Box>
			<Box gridColumn="1 / 3">
				<Text>CCCC</Text>
			</Box>
			<Text>D</Text>
		</Box>,
	);

	t.is(output, 'A   B\nCCCCD');
});

test('automatically placed items skip occupied cells', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="1 1">
			<Text>A</Text>
			<Box gridColumn={1} gridRow={1}>
				<Text>X</Text>
			</Box>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'XA\nB');
});

test('item spanning rows', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="2 1">
			<Box gridRow="1 / 3">
				<Text>A</Text>
			</Box>
			<Text>B</Text>
			<Text>C</Text>
			<Text>D</Text>
		</Box>,
	);

	t.is(output, 'A B\n  C\nD');
});

test('reversed placement lines are swapped', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="2 2 2">
			<Box gridColumn="3 / 1">
				<Text>AAAA</Text>
			</Box>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'AAAAB');
});

test('placement outside of template creates columns', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="2">
			<Text>A</Text>
			<Box gridColumn={3}>
				<Text>C</Text>
			</Box>
		</Box>,
	);

	t.is(output, 'A C');
});

test('single column when columns are not defined', t => {
	const output = renderToString(
		<Box display="grid">
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'A\nB');
});

test('items stretch to fill their cell', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="5 5">
			<Box borderStyle="single">
				<Text>A</Text>
			</Box>
			<Box borderStyle="single">
				<Text>B{'\n'}C</Text>
			</Box>
		</Box>,
	);

	t.is(
		output,
		['┌───┐┌───┐', '│A  ││B  │', '│   ││C  │', '└───┘└───┘'].join('\n'),
	);
});

test('items with their own size are not stretched', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="6 1">
			<Box borderStyle="single" width={3}>
				<Text>A</Text>
			</Box>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, ['┌─┐   B', '│A│', '└─┘'].join('\n'));
});

test('item margins are applied within the cell', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="auto auto">
			<Box marginLeft={2} marginTop={1}>
				<Text>A</Text>
			</Box>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, '   B\n  A');
});

test('grid container padding and border', t => {
	const output = renderToString(
		<Box>
			<Box
				display="grid"
				gridTemplateColumns="2 2"
				borderStyle="single"
				padding={1}
			>
				<Text>A</Text>
				<Text>B</Text>
				<Text>C</Text>
				<Text>D</Text>
			</Box>
		</Box>,
	);

	t.is(
		output,
		[
			'┌──────┐',
			'│      │',
			'│ A B  │',
			'│ C D  │',
			'│      │',
			'└──────┘',
		].join('\n'),
	);
});

test('hidden items are not placed', t => {
	const output = renderToString(
		<Box display="grid" gridTemplateColumns="1 1">
			<Text>A</Text>
			<Box display="none">
				<Text>X</Text>
			</Box>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'AB');
});

test('nested grid', t => {
	const output = renderToString(
		<Box display="grid" width={12} gridTemplateColumns="1fr 1fr">
			<Box display="grid" gridTemplateColumns="auto auto" columnGap={1}>
				<Text>A</Text>
				<Text>B</Text>
				<Text>CC</Text>
				<Text>D</Text>
			</Box>
			<Text>E</Text>
		</Box>,
	);

	t.is(output, 'A  B  E\nCC D');
});

test('grid inside flex layout', t => {
	const output = renderToString(
		<Box flexDirection="column">
			<Text>Top</Text>
			<Box display="grid" gridTemplateColumns="3 3">
				<Text>A</Text>
				<Text>B</Text>
			</Box>
			<Text>Bottom</Text>
		</Box>,
	);

	t.is(output, 'Top\nA  B\nBottom');
});

test('grid inside standalone renderToString', t => {
	const output = renderToStringStandalone(
		<Box display="grid" gridTemplateColumns="1fr 1fr">
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
		{columns: 10},
	);

	t.is(output, 'A    B');
});

test('update when item content changes', t => {
	const stdout = createStdout();

	function Test({text}: {readonly text: string}) {
		return (
			<Box>
				<Box display="grid" gridTemplateColumns="auto auto">
					<Text>{text}</Text>
					<Text>B</Text>
				</Box>
				<Text>|</Text>
			</Box>
		);
	}

	const {rerender} = render(<Test text="A" />, {stdout, debug: true});
	t.is(stdout.get(), 'AB|');

	rerender(<Test text="AAA" />);
	t.is(stdout.get(), 'AAAB|');
});

test('update when items are added and removed', t => {
	const stdout = createStdout();

	function Test({items}: {readonly items: string[]}) {
		return (
			<Box display="grid" gridTemplateColumns="2 2">
				{items.map(item => (
					<Text key={item}>{item}</Text>
				))}
			</Box>
		);
	}

	const {rerender} = render(<Test items={['A', 'B']} />, {
		stdout,
		debug: true,
	});
	t.is(stdout.get(), 'A B');

	rerender(<Test items={['X', 'A', 'B']} />);
	t.is(stdout.get(), 'X A\nB');

	rerender(<Test items={['X', 'B']} />);
	t.is(stdout.get(), 'X B');
});

test('update when template changes', t => {
	const stdout = createStdout();

	function Test({columns}: {readonly columns: string}) {
		return (
			<Box display="grid" gridTemplateColumns={columns}>
				<Text>A</Text>
				<Text>B</Text>
			</Box>
		);
	}

	const {rerender} = render(<Test columns="2 2" />, {stdout, debug: true});
	t.is(stdout.get(), 'A B');

	rerender(<Test columns="4" />);
	t.is(stdout.get(), 'A\nB');
});

test('update when placement changes', t => {
	const stdout = createStdout();

	function Test({column}: {readonly column: number}) {
		return (
			<Box display="grid" gridTemplateColumns="2 2">
				<Box gridColumn={column} gridRow={1}>
					<Text>A</Text>
				</Box>
			</Box>
		);
	}

	const {rerender} = render(<Test column={1} />, {stdout, debug: true});
	t.is(stdout.get(), 'A');

	rerender(<Test column={2} />);
	t.is(stdout.get(), '  A');
});

test('switch between flex and grid display', t => {
	const stdout = createStdout();

	function Test({display}: {readonly display: 'flex' | 'grid'}) {
		return (
			<Box display={display} gridTemplateColumns="2">
				<Text>A</Text>
				<Text>B</Text>
			</Box>
		);
	}

	const {rerender, unmount} = render(<Test display="flex" />, {
		stdout,
		debug: true,
	});
	t.is(stdout.get(), 'AB');

	rerender(<Test display="grid" />);
	t.is(stdout.get(), 'A\nB');

	rerender(<Test display="flex" />);
	t.is(stdout.get(), 'AB');

	unmount();
});

test('show item that was initially hidden', t => {
	const stdout = createStdout();

	function Test({isHidden}: {readonly isHidden: boolean}) {
		return (
			<Box>
				<Box display="grid" gridTemplateColumns="auto">
					<Text>A</Text>
					<Box display={isHidden ? 'none' : 'flex'}>
						<Text>BBB</Text>
					</Box>
				</Box>
				<Text>|</Text>
			</Box>
		);
	}

	const {rerender} = render(<Test isHidden />, {stdout, debug: true});
	t.is(stdout.get(), 'A|');

	rerender(<Test isHidden={false} />);
	t.is(stdout.get(), 'A  |\nBBB');

	rerender(<Test isHidden />);
	t.is(stdout.get(), 'A|');
});

test('remove grid container', t => {
	const stdout = createStdout();

	function Test({isVisible}: {readonly isVisible: boolean}) {
		return (
			<Box flexDirection="column">
				{isVisible ? (
					<Box display="grid" gridTemplateColumns="2 2">
						<Box display="grid">
							<Text>A</Text>
						</Box>
						<Text>B</Text>
					</Box>
				) : undefined}
				<Text>C</Text>
			</Box>
		);
	}

	const {rerender, unmount} = render(<Test isVisible />, {
		stdout,
		debug: true,
	});
	t.is(stdout.get(), 'A B\nC');

	rerender(<Test isVisible={false} />);
	t.is(stdout.get(), 'C');

	t.notThrows(() => {
		unmount();
	});
});

test('box metrics of grid item are relative to grid container', async t => {
	const stdout = createStdout(100);

	function Item() {
		const ref = useRef<DOMElement>(null);
		const {left, top, width, height} = useBoxMetrics(ref);

		return (
			<Box ref={ref}>
				<Text>
					{left},{top} {width}x{height}
				</Text>
			</Box>
		);
	}

	const {waitUntilRenderFlush} = render(
		<Box display="grid" gridTemplateColumns="10 12" gap={1} padding={1}>
			<Text>first</Text>
			<Text>second</Text>
			<Text>third</Text>
			<Item />
		</Box>,
		{stdout, debug: true},
	);

	await waitUntilRenderFlush();
	await delay(50);

	t.true(stripAnsi(stdout.get()).includes('12,3 12x1'));
});

// Concurrent mode tests
test('fractional columns - concurrent', async t => {
	const output = await renderToStringAsync(
		<Box display="grid" width={10} gridTemplateColumns="1fr 1fr">
			<Text>A</Text>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'A    B');
});

test('explicit placement - concurrent', async t => {
	const output = await renderToStringAsync(
		<Box display="grid" gridTemplateColumns="1 1">
			<Text>A</Text>
			<Box gridColumn={1} gridRow={1}>
				<Text>X</Text>
			</Box>
			<Text>B</Text>
		</Box>,
	);

	t.is(output, 'XA\nB');
});
