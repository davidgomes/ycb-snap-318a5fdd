export type GridTrack =
	| {type: 'fixed'; size: number}
	| {type: 'fr'; fr: number}
	| {type: 'auto'}
	| {type: 'minmax'; min: number; max: number}
	| {type: 'minmax-fr'; min: number; fr: number};

export type GridLine = {
	start: number;
	end: number;
};

export type GridPlacementInput = {
	column?: GridLine;
	row?: GridLine;
};

export type ResolvedPlacement = {
	columnStart: number;
	columnEnd: number;
	rowStart: number;
	rowEnd: number;
};

export type SpanConstraint = {
	start: number;
	end: number;
	size: number;
};

const fixedPattern = /^(\d+(?:\.\d+)?)$/;
const frPattern = /^(\d+(?:\.\d+)?)fr$/;
const minmaxPattern =
	/^minmax\(\s*(\d+(?:\.\d+)?)\s*,\s*(\d+(?:\.\d+)?)(fr)?\s*\)$/;
const placementPattern = /^(\d+)\s*\/\s*(\d+)$/;

const splitTrackList = (template: string): string[] => {
	const tokens: string[] = [];
	let current = '';
	let depth = 0;

	for (const character of template.trim()) {
		if (character === '(') {
			depth++;
		} else if (character === ')') {
			depth = Math.max(0, depth - 1);
		}

		if (depth === 0 && /\s/.test(character)) {
			if (current.length > 0) {
				tokens.push(current);
				current = '';
			}

			continue;
		}

		current += character;
	}

	if (current.length > 0) {
		tokens.push(current);
	}

	return tokens;
};

const parseTrack = (token: string): GridTrack => {
	if (token === 'auto') {
		return {type: 'auto'};
	}

	const frMatch = frPattern.exec(token);
	if (frMatch) {
		return {type: 'fr', fr: Number(frMatch[1])};
	}

	const fixedMatch = fixedPattern.exec(token);
	if (fixedMatch) {
		return {type: 'fixed', size: Number(fixedMatch[1])};
	}

	const minmaxMatch = minmaxPattern.exec(token);
	if (minmaxMatch) {
		const min = Number(minmaxMatch[1]);
		const max = Number(minmaxMatch[2]);
		if (minmaxMatch[3]) {
			return {type: 'minmax-fr', min, fr: max};
		}

		return {type: 'minmax', min, max: Math.max(min, max)};
	}

	throw new Error(`Invalid grid track size: ${token}`);
};

export const parseGridTrackList = (
	template: string | undefined,
): GridTrack[] => {
	if (!template || template.trim() === '') {
		return [];
	}

	return splitTrackList(template).map(token => parseTrack(token));
};

export const parseGridPlacement = (
	value: number | string | undefined,
): GridLine | undefined => {
	if (value === undefined) {
		return undefined;
	}

	if (typeof value === 'number') {
		if (!Number.isInteger(value) || value < 1) {
			throw new Error(`Invalid grid placement: ${value}`);
		}

		return {start: value, end: value + 1};
	}

	const trimmed = value.trim();
	if (/^\d+$/.test(trimmed)) {
		return parseGridPlacement(Number(trimmed));
	}

	const match = placementPattern.exec(trimmed);
	if (!match) {
		throw new Error(`Invalid grid placement: ${value}`);
	}

	const start = Number(match[1]);
	const end = Number(match[2]);
	if (start < 1 || end <= start) {
		throw new Error(`Invalid grid placement: ${value}`);
	}

	return {start, end};
};

const spanLength = (
	sizes: readonly number[],
	gap: number,
	start: number,
	end: number,
) => {
	let total = 0;
	for (let index = start; index < end; index++) {
		total += sizes[index] ?? 0;
		if (index > start) {
			total += gap;
		}
	}

	return total;
};

const growTracksForSpan = (
	sizes: number[],
	tracks: readonly GridTrack[],
	span: SpanConstraint,
	{gap, allowFlexible}: {gap: number; allowFlexible: boolean},
) => {
	const {start, end, size} = span;
	if (end <= start) {
		return;
	}

	let extra = size - spanLength(sizes, gap, start, end);
	if (extra <= 0) {
		return;
	}

	const growable: number[] = [];
	for (let index = start; index < end; index++) {
		const track = tracks[index];
		if (!track) {
			continue;
		}

		if (
			track.type === 'auto' ||
			(allowFlexible && (track.type === 'fr' || track.type === 'minmax-fr'))
		) {
			growable.push(index);
			continue;
		}

		if (track.type === 'minmax' && (sizes[index] ?? 0) < track.max) {
			growable.push(index);
		}
	}

	let guard = 0;
	while (extra > 0.001 && growable.length > 0 && guard < 8) {
		guard++;
		const share = extra / growable.length;
		let consumed = 0;

		for (let index = growable.length - 1; index >= 0; index--) {
			const trackIndex = growable[index]!;
			const track = tracks[trackIndex]!;
			let addition = share;
			if (track.type === 'minmax') {
				const room = Math.max(0, track.max - (sizes[trackIndex] ?? 0));
				addition = Math.min(addition, room);
				if (room - addition <= 0.001) {
					growable.splice(index, 1);
				}
			}

			sizes[trackIndex] = (sizes[trackIndex] ?? 0) + addition;
			consumed += addition;
		}

		if (consumed <= 0.001) {
			break;
		}

		extra -= consumed;
	}
};

const integerize = (sizes: readonly number[], targetSum?: number): number[] => {
	if (sizes.length === 0) {
		return [];
	}

	const floored = sizes.map(size => Math.floor(Math.max(0, size)));
	const rawSum = sizes.reduce((sum, size) => sum + Math.max(0, size), 0);
	const target = targetSum ?? Math.round(rawSum);
	let delta = target - floored.reduce((sum, size) => sum + size, 0);
	const order = sizes.map((size, index) => ({
		index,
		fraction: Math.max(0, size) - Math.floor(Math.max(0, size)),
	}));

	if (delta > 0) {
		order.sort(
			(left, right) =>
				right.fraction - left.fraction || left.index - right.index,
		);
		for (let step = 0; delta > 0; step++) {
			floored[order[step % order.length]!.index]! += 1;
			delta--;
		}
	} else if (delta < 0) {
		order.sort(
			(left, right) =>
				left.fraction - right.fraction || right.index - left.index,
		);
		let step = 0;
		while (delta < 0 && step < sizes.length * 4) {
			const {index} = order[step % order.length]!;
			if ((floored[index] ?? 0) > 0) {
				floored[index] = (floored[index] ?? 0) - 1;
				delta++;
			}

			step++;
		}
	}

	return floored;
};

const baseSize = (track: GridTrack, contribution: number): number => {
	switch (track.type) {
		case 'fixed': {
			return track.size;
		}

		case 'auto': {
			return contribution;
		}

		case 'minmax':
		case 'minmax-fr': {
			return track.min;
		}

		case 'fr': {
			return 0;
		}
	}
};

const indefiniteSize = (track: GridTrack, contribution: number): number => {
	switch (track.type) {
		case 'fixed': {
			return track.size;
		}

		case 'auto':
		case 'fr': {
			return contribution;
		}

		case 'minmax': {
			return Math.min(track.max, Math.max(track.min, contribution));
		}

		case 'minmax-fr': {
			return Math.max(track.min, contribution);
		}
	}
};

export const resolveTrackSizes = (
	tracks: readonly GridTrack[],
	{
		available,
		gap = 0,
		contributions = [],
		spans = [],
	}: {
		available?: number;
		gap?: number;
		contributions?: readonly number[];
		spans?: readonly SpanConstraint[];
	} = {},
): number[] => {
	if (tracks.length === 0) {
		return [];
	}

	const normalizedGap = Math.max(0, gap);
	if (available === undefined) {
		const sizes = tracks.map((track, index) =>
			indefiniteSize(track, Math.max(0, contributions[index] ?? 0)),
		);
		for (const span of spans) {
			growTracksForSpan(sizes, tracks, span, {
				gap: normalizedGap,
				allowFlexible: true,
			});
		}

		return integerize(sizes);
	}

	const sizes = tracks.map((track, index) =>
		baseSize(track, Math.max(0, contributions[index] ?? 0)),
	);

	for (const span of spans) {
		growTracksForSpan(sizes, tracks, span, {
			gap: normalizedGap,
			allowFlexible: false,
		});
	}

	const gapSpace = normalizedGap * Math.max(0, tracks.length - 1);
	const budget = available - gapSpace;
	let free = budget - sizes.reduce((sum, size) => sum + size, 0);

	if (free > 0) {
		const room = tracks.map((track, index) =>
			track.type === 'minmax'
				? Math.max(0, track.max - (sizes[index] ?? 0))
				: 0,
		);
		const roomSum = room.reduce((sum, size) => sum + size, 0);
		if (roomSum > 0) {
			const used = Math.min(free, roomSum);
			for (const [index, capacity] of room.entries()) {
				if (capacity <= 0) {
					continue;
				}

				sizes[index] = (sizes[index] ?? 0) + used * (capacity / roomSum);
			}

			free -= used;
		}
	}

	const factors = tracks.map(track => {
		if (track.type === 'fr') {
			return track.fr;
		}

		if (track.type === 'minmax-fr') {
			return track.fr;
		}

		return 0;
	});
	const factorSum = factors.reduce((sum, factor) => sum + factor, 0);
	const distributedFlexibleSpace = factorSum > 0 && free > 0 && budget >= 0;
	if (distributedFlexibleSpace) {
		for (const [index, factor] of factors.entries()) {
			if (factor <= 0) {
				continue;
			}

			sizes[index] = (sizes[index] ?? 0) + free * (factor / factorSum);
		}
	}

	return integerize(
		sizes,
		distributedFlexibleSpace ? Math.max(0, Math.round(budget)) : undefined,
	);
};

export const trackOffsets = (
	sizes: readonly number[],
	gap: number,
): number[] => {
	const offsets: number[] = [];
	let cursor = 0;
	for (const size of sizes) {
		offsets.push(cursor);
		cursor += size + gap;
	}

	return offsets;
};

export const spanSize = (
	sizes: readonly number[],
	gap: number,
	start: number,
	end: number,
): number => spanLength(sizes, gap, start, end);

const cellKey = (row: number, column: number) => `${row}:${column}`;

export const placeGridItems = (
	items: readonly GridPlacementInput[],
	explicitColumnCount: number,
	explicitRowCount: number,
): {
	placements: ResolvedPlacement[];
	columnCount: number;
	rowCount: number;
} => {
	let flowColumnCount = Math.max(0, explicitColumnCount);
	if (flowColumnCount === 0) {
		let maxSpan = 1;
		for (const item of items) {
			if (item.column) {
				maxSpan = Math.max(maxSpan, item.column.end - item.column.start);
			}
		}

		flowColumnCount = maxSpan;
	}

	let columnCount = Math.max(0, explicitColumnCount);
	let rowCount = Math.max(0, explicitRowCount);
	const occupied = new Set<string>();
	const placements: ResolvedPlacement[] = Array.from({length: items.length});

	const fits = (
		row: number,
		column: number,
		rowSpan: number,
		columnSpan: number,
	) => {
		for (let rowIndex = row; rowIndex < row + rowSpan; rowIndex++) {
			for (
				let columnIndex = column;
				columnIndex < column + columnSpan;
				columnIndex++
			) {
				if (occupied.has(cellKey(rowIndex, columnIndex))) {
					return false;
				}
			}
		}

		return true;
	};

	const place = (
		index: number,
		position: {
			row: number;
			column: number;
			rowSpan: number;
			columnSpan: number;
		},
	) => {
		const {row, column, rowSpan, columnSpan} = position;
		placements[index] = {
			columnStart: column,
			columnEnd: column + columnSpan,
			rowStart: row,
			rowEnd: row + rowSpan,
		};
		for (let rowIndex = row; rowIndex < row + rowSpan; rowIndex++) {
			for (
				let columnIndex = column;
				columnIndex < column + columnSpan;
				columnIndex++
			) {
				occupied.add(cellKey(rowIndex, columnIndex));
			}
		}

		columnCount = Math.max(columnCount, column + columnSpan - 1);
		rowCount = Math.max(rowCount, row + rowSpan - 1);
	};

	const both: number[] = [];
	const rowDefinite: number[] = [];
	const rest: number[] = [];
	for (const [index, item] of items.entries()) {
		if (item.column && item.row) {
			both.push(index);
		} else if (item.row) {
			rowDefinite.push(index);
		} else {
			rest.push(index);
		}
	}

	for (const index of both) {
		const item = items[index]!;
		place(index, {
			row: item.row!.start,
			column: item.column!.start,
			rowSpan: item.row!.end - item.row!.start,
			columnSpan: item.column!.end - item.column!.start,
		});
	}

	for (const index of rowDefinite) {
		const item = items[index]!;
		const row = item.row!.start;
		const rowSpan = item.row!.end - item.row!.start;
		const columnSpan = 1;
		let column = 1;
		let guard = 0;
		while (!fits(row, column, rowSpan, columnSpan)) {
			column++;
			guard++;
			if (guard > 10_000) {
				throw new Error('Failed to place grid item');
			}
		}

		place(index, {row, column, rowSpan, columnSpan});
	}

	let cursorRow = 1;
	let cursorColumn = 1;
	for (const index of rest) {
		const item = items[index]!;
		if (item.column) {
			const column = item.column.start;
			const columnSpan = item.column.end - item.column.start;
			const rowSpan = 1;
			let row = 1;
			let guard = 0;
			while (!fits(row, column, rowSpan, columnSpan)) {
				row++;
				guard++;
				if (guard > 10_000) {
					throw new Error('Failed to place grid item');
				}
			}

			place(index, {row, column, rowSpan, columnSpan});
			continue;
		}

		let column = cursorColumn;
		let row = cursorRow;
		if (column > flowColumnCount) {
			column = 1;
			row++;
		}

		let guard = 0;
		while (!fits(row, column, 1, 1) || column > flowColumnCount) {
			column++;
			if (column > flowColumnCount) {
				column = 1;
				row++;
			}

			guard++;
			if (guard > 10_000) {
				throw new Error('Failed to place grid item');
			}
		}

		place(index, {row, column, rowSpan: 1, columnSpan: 1});
		cursorColumn = column + 1;
		cursorRow = row;
		if (cursorColumn > flowColumnCount) {
			cursorColumn = 1;
			cursorRow = row + 1;
		}
	}

	return {
		placements,
		columnCount,
		rowCount,
	};
};

export const expandTracks = (
	explicitTracks: readonly GridTrack[],
	count: number,
): GridTrack[] => {
	const tracks = [...explicitTracks];
	while (tracks.length < count) {
		tracks.push({type: 'auto'});
	}

	return tracks.slice(0, Math.max(count, 0));
};
