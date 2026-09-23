export type TrackSize =
	| {
			readonly type: 'fixed';
			readonly size: number;
	  }
	| {
			readonly type: 'fr';
			readonly fr: number;
	  }
	| {
			readonly type: 'auto';
	  }
	| {
			readonly type: 'minmax-fixed';
			readonly min: number;
			readonly max: number;
	  }
	| {
			readonly type: 'minmax-fr';
			readonly min: number;
			readonly fr: number;
	  };

export type GridLineRange = {
	readonly start: number;
	readonly end: number;
};

export type GridPlacement = {
	/** 0-based inclusive track index. */
	readonly columnStart: number;
	/** 0-based exclusive track index. */
	readonly columnEnd: number;
	/** 0-based inclusive track index. */
	readonly rowStart: number;
	/** 0-based exclusive track index. */
	readonly rowEnd: number;
};

type MutableTrack = {
	base: number;
	flex: number;
	max: number;
	contentGrow: boolean;
};

const numberPattern = String.raw`(?:\d+\.?\d*|\.\d+)`;
const fixedPattern = new RegExp(`^${numberPattern}$`);
const frPattern = new RegExp(`^(${numberPattern})fr$`);
const minmaxPattern = new RegExp(
	`^minmax\\(\\s*(${numberPattern})\\s*,\\s*(${numberPattern})(fr)?\\s*\\)$`,
);

const roundTrack = (value: number): number => {
	if (!Number.isFinite(value)) {
		return 0;
	}

	return Math.max(0, Math.round(value));
};

const snapContent = (value: number): number => {
	if (!Number.isFinite(value) || value <= 0) {
		return 0;
	}

	const nearest = Math.round(value);
	if (Math.abs(value - nearest) < 0.01) {
		return Math.max(0, nearest);
	}

	return Math.ceil(value);
};

export const parseTrackList = (template: string | undefined): TrackSize[] => {
	if (!template?.trim()) {
		return [];
	}

	const tokens: string[] = [];
	let current = '';
	let depth = 0;

	for (const character of template) {
		if (character === '(') {
			depth++;
		} else if (character === ')') {
			depth = Math.max(0, depth - 1);
		}

		if (/\s/.test(character) && depth === 0) {
			if (current.trim()) {
				tokens.push(current.trim());
			}

			current = '';
			continue;
		}

		current += character;
	}

	if (current.trim()) {
		tokens.push(current.trim());
	}

	const tracks: TrackSize[] = [];

	for (const token of tokens) {
		if (token === 'auto') {
			tracks.push({type: 'auto'});
			continue;
		}

		const minmax = minmaxPattern.exec(token);
		if (minmax) {
			const min = roundTrack(Number(minmax[1]));
			const max = roundTrack(Number(minmax[2]));

			if (minmax[3] === 'fr') {
				tracks.push({
					type: 'minmax-fr',
					min,
					fr: Number(minmax[2]),
				});
			} else {
				tracks.push({
					type: 'minmax-fixed',
					min: Math.min(min, max),
					max: Math.max(min, max),
				});
			}

			continue;
		}

		const fractional = frPattern.exec(token);
		if (fractional) {
			tracks.push({
				type: 'fr',
				fr: Number(fractional[1]),
			});
			continue;
		}

		if (fixedPattern.test(token)) {
			tracks.push({
				type: 'fixed',
				size: roundTrack(Number(token)),
			});
		}
	}

	return tracks;
};

/**
Parse a 1-based grid line.
A bare index covers that single cell. `"start / end"` uses an exclusive end line.
*/
export const parseGridPlacement = (
	value: number | string | undefined,
): GridLineRange | undefined => {
	if (typeof value === 'number') {
		if (!Number.isFinite(value)) {
			return undefined;
		}

		const start = Math.max(1, Math.trunc(value));
		return {start, end: start + 1};
	}

	if (typeof value !== 'string') {
		return undefined;
	}

	const parts = value
		.split('/')
		.map(part => part.trim())
		.filter(part => part.length > 0);

	if (parts.length === 0) {
		return undefined;
	}

	const start = Math.trunc(Number(parts[0]));
	if (!Number.isFinite(start)) {
		return undefined;
	}

	const normalizedStart = Math.max(1, start);
	if (parts.length === 1) {
		return {start: normalizedStart, end: normalizedStart + 1};
	}

	const end = Math.trunc(Number(parts[1]));
	if (!Number.isFinite(end)) {
		return {start: normalizedStart, end: normalizedStart + 1};
	}

	return {
		start: normalizedStart,
		end: Math.max(normalizedStart + 1, end),
	};
};

const cellKey = (column: number, row: number): string => `${column},${row}`;

type CellSpan = {
	occupied: Set<string>;
	column: number;
	row: number;
	columnSpan: number;
	rowSpan: number;
};

const spanFits = ({
	occupied,
	column,
	row,
	columnSpan,
	rowSpan,
}: CellSpan): boolean => {
	for (let rowIndex = row; rowIndex < row + rowSpan; rowIndex++) {
		for (
			let columnIndex = column;
			columnIndex < column + columnSpan;
			columnIndex++
		) {
			if (occupied.has(cellKey(columnIndex, rowIndex))) {
				return false;
			}
		}
	}

	return true;
};

const occupySpan = ({occupied, column, row, columnSpan, rowSpan}: CellSpan) => {
	for (let rowIndex = row; rowIndex < row + rowSpan; rowIndex++) {
		for (
			let columnIndex = column;
			columnIndex < column + columnSpan;
			columnIndex++
		) {
			occupied.add(cellKey(columnIndex, rowIndex));
		}
	}
};

export type PlacementInput = {
	readonly column?: GridLineRange;
	readonly row?: GridLineRange;
};

/**
Place items in row-major order.
Explicit `start / end` ranges reserve cells before auto-placed items.
When no explicit columns exist, items stack in a single column.
*/
export const placeGridItems = (
	items: readonly PlacementInput[],
	explicitColumnCount: number,
	explicitRowCount: number,
): {
	placements: GridPlacement[];
	columnCount: number;
	rowCount: number;
} => {
	const flowColumnCount = Math.max(explicitColumnCount, 1);
	const occupied = new Set<string>();
	const placements: Array<GridPlacement | undefined> = Array.from(
		{length: items.length},
		() => undefined,
	);

	let columnCount = explicitColumnCount;
	let rowCount = explicitRowCount;

	const commit = ({
		index,
		columnStart,
		columnEnd,
		rowStart,
		rowEnd,
	}: {
		index: number;
		columnStart: number;
		columnEnd: number;
		rowStart: number;
		rowEnd: number;
	}) => {
		placements[index] = {
			columnStart: columnStart - 1,
			columnEnd: columnEnd - 1,
			rowStart: rowStart - 1,
			rowEnd: rowEnd - 1,
		};
		occupySpan({
			occupied,
			column: columnStart,
			row: rowStart,
			columnSpan: columnEnd - columnStart,
			rowSpan: rowEnd - rowStart,
		});
		columnCount = Math.max(columnCount, columnEnd - 1);
		rowCount = Math.max(rowCount, rowEnd - 1);
	};

	for (const [index, item] of items.entries()) {
		if (!item.column || !item.row) {
			continue;
		}

		commit({
			index,
			columnStart: item.column.start,
			columnEnd: item.column.end,
			rowStart: item.row.start,
			rowEnd: item.row.end,
		});
	}

	let cursorColumn = 1;
	let cursorRow = 1;

	const advanceCursor = (column: number, columnSpan: number, row: number) => {
		let nextColumn = column + columnSpan;
		let nextRow = row;
		if (nextColumn > flowColumnCount) {
			nextColumn = 1;
			nextRow += 1;
		}

		if (
			nextRow > cursorRow ||
			(nextRow === cursorRow && nextColumn > cursorColumn)
		) {
			cursorColumn = nextColumn;
			cursorRow = nextRow;
		}
	};

	for (const [index, item] of items.entries()) {
		if (placements[index]) {
			continue;
		}

		const columnSpan = item.column ? item.column.end - item.column.start : 1;
		const rowSpan = item.row ? item.row.end - item.row.start : 1;

		if (item.column && !item.row) {
			let row = 1;
			while (
				!spanFits({
					occupied,
					column: item.column.start,
					row,
					columnSpan,
					rowSpan,
				})
			) {
				row++;
				if (row > items.length + explicitRowCount + 1) {
					break;
				}
			}

			commit({
				index,
				columnStart: item.column.start,
				columnEnd: item.column.end,
				rowStart: row,
				rowEnd: row + rowSpan,
			});
			advanceCursor(item.column.start, columnSpan, row);
			continue;
		}

		if (item.row && !item.column) {
			let column = 1;
			while (
				!spanFits({
					occupied,
					column,
					row: item.row.start,
					columnSpan,
					rowSpan,
				})
			) {
				column++;
				if (column > items.length + explicitColumnCount + 1) {
					break;
				}
			}

			commit({
				index,
				columnStart: column,
				columnEnd: column + columnSpan,
				rowStart: item.row.start,
				rowEnd: item.row.end,
			});
			advanceCursor(column, columnSpan, item.row.start);
			continue;
		}

		let column = cursorColumn;
		let row = cursorRow;
		let guard = 0;
		while (
			!spanFits({
				occupied,
				column,
				row,
				columnSpan,
				rowSpan,
			})
		) {
			column++;
			if (column + columnSpan - 1 > flowColumnCount) {
				column = 1;
				row++;
			}

			guard++;
			if (guard > items.length * flowColumnCount + items.length + 2) {
				break;
			}
		}

		commit({
			index,
			columnStart: column,
			columnEnd: column + columnSpan,
			rowStart: row,
			rowEnd: row + rowSpan,
		});
		advanceCursor(column, columnSpan, row);
	}

	return {
		placements: placements.map(
			placement =>
				placement ?? {
					columnStart: 0,
					columnEnd: 1,
					rowStart: 0,
					rowEnd: 1,
				},
		),
		columnCount,
		rowCount,
	};
};

const createMutableTrack = (
	track: TrackSize,
	indefinite: boolean,
): MutableTrack => {
	switch (track.type) {
		case 'fixed': {
			return {
				base: track.size,
				flex: 0,
				max: track.size,
				contentGrow: false,
			};
		}

		case 'auto': {
			return {
				base: 0,
				flex: 0,
				max: Number.POSITIVE_INFINITY,
				contentGrow: true,
			};
		}

		case 'fr': {
			return {
				base: 0,
				flex: track.fr,
				max: Number.POSITIVE_INFINITY,
				contentGrow: indefinite,
			};
		}

		case 'minmax-fixed': {
			return {
				base: track.min,
				flex: 0,
				max: track.max,
				contentGrow: true,
			};
		}

		case 'minmax-fr': {
			return {
				base: track.min,
				flex: track.fr,
				max: Number.POSITIVE_INFINITY,
				contentGrow: indefinite,
			};
		}
	}
};

const distributeByWeight = (
	total: number,
	weights: readonly number[],
): number[] => {
	const shares = Array.from({length: weights.length}, () => 0);
	const weightSum = weights.reduce((sum, weight) => sum + weight, 0);
	if (total <= 0 || weightSum <= 0) {
		return shares;
	}

	const exact = weights.map(weight => (total * weight) / weightSum);
	let used = 0;
	for (const [index, value] of exact.entries()) {
		shares[index] = Math.floor(value);
		used += shares[index] ?? 0;
	}

	let left = total - used;
	const order = exact
		.map((value, index) => ({
			index,
			fraction: value - Math.floor(value),
		}))
		.sort((a, b) => b.fraction - a.fraction || a.index - b.index);

	for (const {index} of order) {
		if (left <= 0) {
			break;
		}

		shares[index] = (shares[index] ?? 0) + 1;
		left--;
	}

	if (left > 0 && shares.length > 0) {
		shares[0] = (shares[0] ?? 0) + left;
	}

	return shares;
};

const growTracksFromContent = (
	tracks: MutableTrack[],
	{
		start,
		end,
		gap,
		content,
	}: {
		start: number;
		end: number;
		gap: number;
		content: number;
	},
) => {
	const span = end - start;
	if (span <= 0 || content <= 0) {
		return;
	}

	const gapSpace = gap * Math.max(0, span - 1);
	let current = 0;
	for (let index = start; index < end; index++) {
		current += tracks[index]?.base ?? 0;
	}

	let deficit = content - gapSpace - current;
	if (deficit <= 0) {
		return;
	}

	let guard = 0;
	while (deficit > 0 && guard < tracks.length + 2) {
		guard++;
		const growable: number[] = [];
		for (let index = start; index < end; index++) {
			const track = tracks[index];
			if (track && track.contentGrow && track.base < track.max) {
				growable.push(index);
			}
		}

		if (growable.length === 0) {
			return;
		}

		const shares = distributeByWeight(
			deficit,
			growable.map(() => 1),
		);
		let consumed = 0;
		for (const [shareIndex, trackIndex] of growable.entries()) {
			const track = tracks[trackIndex];
			if (!track) {
				continue;
			}

			const room = track.max - track.base;
			const add = Math.min(room, shares[shareIndex] ?? 0);
			track.base += add;
			consumed += add;
		}

		if (consumed === 0) {
			return;
		}

		deficit -= consumed;
	}
};

export type TrackContribution = {
	readonly start: number;
	readonly end: number;
	readonly content: number;
};

/**
Resolve track sizes.

Minimums are satisfied first: fixed tracks, `auto` content, and `minmax` minimums.
Leftover space in a definite container is then split across `fr` tracks and `minmax` tracks whose maximum is `fr`, in proportion to those flex factors.
*/
export const resolveTrackSizes = (
	template: readonly TrackSize[],
	contributions: readonly TrackContribution[],
	gap: number,
	available: number | undefined,
): number[] => {
	const indefinite = available === undefined;
	const tracks = template.map(track => createMutableTrack(track, indefinite));
	const normalizedGap = roundTrack(gap);

	const ordered = [...contributions].sort((a, b) => {
		const spanA = a.end - a.start;
		const spanB = b.end - b.start;
		return spanA - spanB;
	});

	for (const contribution of ordered) {
		growTracksFromContent(tracks, {
			start: contribution.start,
			end: contribution.end,
			gap: normalizedGap,
			content: snapContent(contribution.content),
		});
	}

	if (available !== undefined && tracks.length > 0) {
		const availableSize = roundTrack(available);
		const gapSpace = normalizedGap * Math.max(0, tracks.length - 1);
		const baseSum = tracks.reduce((sum, track) => sum + track.base, 0);
		const remaining = availableSize - baseSum - gapSpace;
		const flexible = tracks
			.map((track, index) => ({index, flex: track.flex}))
			.filter(track => track.flex > 0);

		if (remaining > 0 && flexible.length > 0) {
			const shares = distributeByWeight(
				remaining,
				flexible.map(track => track.flex),
			);
			for (const [shareIndex, track] of flexible.entries()) {
				const target = tracks[track.index];
				if (target) {
					target.base += shares[shareIndex] ?? 0;
				}
			}
		}
	}

	return tracks.map(track => track.base);
};

export const trackOffsets = (
	sizes: readonly number[],
	gap: number,
): number[] => {
	const offsets: number[] = [];
	let position = 0;
	const normalizedGap = roundTrack(gap);

	for (const [index, size] of sizes.entries()) {
		offsets.push(position);
		position += size;
		if (index < sizes.length - 1) {
			position += normalizedGap;
		}
	}

	return offsets;
};

export const spanSize = (
	sizes: readonly number[],
	gap: number,
	start: number,
	end: number,
): number => {
	if (end <= start) {
		return 0;
	}

	let size = 0;
	for (let index = start; index < end; index++) {
		size += sizes[index] ?? 0;
	}

	size += roundTrack(gap) * (end - start - 1);
	return size;
};

export const tracksContentSize = (
	sizes: readonly number[],
	gap: number,
): number => {
	if (sizes.length === 0) {
		return 0;
	}

	return (
		sizes.reduce((sum, size) => sum + size, 0) +
		roundTrack(gap) * (sizes.length - 1)
	);
};

export const ensureTrackCount = (
	tracks: readonly TrackSize[],
	count: number,
): TrackSize[] => {
	const next = [...tracks];
	while (next.length < count) {
		next.push({type: 'auto'});
	}

	return next;
};
