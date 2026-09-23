import test from 'ava';
import {
	parseGridPlacement,
	parseGridTrackList,
	placeGridItems,
	resolveTrackSizes,
} from '../src/grid.js';

test('parse track sizes', t => {
	t.deepEqual(parseGridTrackList('10 auto 2fr minmax(4, 8) minmax(6, 1fr)'), [
		{type: 'fixed', size: 10},
		{type: 'auto'},
		{type: 'fr', fr: 2},
		{type: 'minmax', min: 4, max: 8},
		{type: 'minmax-fr', min: 6, fr: 1},
	]);
});

test('parse grid line placement', t => {
	t.deepEqual(parseGridPlacement(2), {start: 2, end: 3});
	t.deepEqual(parseGridPlacement('2'), {start: 2, end: 3});
	t.deepEqual(parseGridPlacement('1 / 3'), {start: 1, end: 3});
	t.deepEqual(parseGridPlacement('1/3'), {start: 1, end: 3});
});

test('distribute remaining space across fr maximums', t => {
	const tracks = parseGridTrackList('minmax(6, 1fr) minmax(6, 2fr)');
	t.deepEqual(
		resolveTrackSizes(tracks, {available: 30, contributions: [0, 0]}),
		[12, 18],
	);
});

test('fixed track keeps its size and fr takes the rest', t => {
	const tracks = parseGridTrackList('10 1fr');
	t.deepEqual(
		resolveTrackSizes(tracks, {available: 30, contributions: [0, 0]}),
		[10, 20],
	);
});

test('auto track uses content before fr distribution', t => {
	const tracks = parseGridTrackList('auto 1fr');
	t.deepEqual(
		resolveTrackSizes(tracks, {available: 20, contributions: [5, 0]}),
		[5, 15],
	);
});

test('minmax fixed maximum grows before fr tracks', t => {
	const tracks = parseGridTrackList('minmax(2, 8) 1fr');
	t.deepEqual(
		resolveTrackSizes(tracks, {available: 20, contributions: [0, 0]}),
		[8, 12],
	);
});

test('gaps are removed before fr shares are assigned', t => {
	const tracks = parseGridTrackList('1fr 1fr');
	t.deepEqual(
		resolveTrackSizes(tracks, {available: 11, gap: 1, contributions: [0, 0]}),
		[5, 5],
	);
});

test('fixed tracks do not absorb extra space', t => {
	const tracks = parseGridTrackList('4 4');
	t.deepEqual(
		resolveTrackSizes(tracks, {available: 20, contributions: [0, 0]}),
		[4, 4],
	);
});

test('omitted rows place items automatically', t => {
	const {placements, columnCount, rowCount} = placeGridItems(
		[{}, {}, {}],
		2,
		0,
	);

	t.is(columnCount, 2);
	t.is(rowCount, 2);
	t.deepEqual(placements, [
		{columnStart: 1, columnEnd: 2, rowStart: 1, rowEnd: 2},
		{columnStart: 2, columnEnd: 3, rowStart: 1, rowEnd: 2},
		{columnStart: 1, columnEnd: 2, rowStart: 2, rowEnd: 3},
	]);
});

test('explicit placement and spans reserve cells', t => {
	const {placements, columnCount, rowCount} = placeGridItems(
		[
			{column: {start: 1, end: 3}},
			{},
			{column: {start: 3, end: 4}, row: {start: 1, end: 2}},
		],
		3,
		0,
	);

	t.is(columnCount, 3);
	t.is(rowCount, 2);
	t.deepEqual(placements[0], {
		columnStart: 1,
		columnEnd: 3,
		rowStart: 1,
		rowEnd: 2,
	});
	t.deepEqual(placements[1], {
		columnStart: 1,
		columnEnd: 2,
		rowStart: 2,
		rowEnd: 3,
	});
	t.deepEqual(placements[2], {
		columnStart: 3,
		columnEnd: 4,
		rowStart: 1,
		rowEnd: 2,
	});
});

test('a spanning item grows auto tracks', t => {
	const tracks = parseGridTrackList('auto auto');
	t.deepEqual(
		resolveTrackSizes(tracks, {
			contributions: [1, 1],
			spans: [{start: 0, end: 2, size: 7}],
		}),
		[4, 3],
	);
});

test('without column tracks, items stack in one column', t => {
	const {placements, columnCount, rowCount} = placeGridItems([{}, {}], 0, 0);
	t.is(columnCount, 1);
	t.is(rowCount, 2);
	t.deepEqual(placements, [
		{columnStart: 1, columnEnd: 2, rowStart: 1, rowEnd: 2},
		{columnStart: 1, columnEnd: 2, rowStart: 2, rowEnd: 3},
	]);
});
