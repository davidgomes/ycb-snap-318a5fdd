import { type AnyColumn, Column, type GetColumnData } from '~/column.ts';
import { is } from '~/entity.ts';
import { bindIfParam } from '~/sql/expressions/conditions.ts';
import { type SQL, sql, type SQLWrapper } from '../sql.ts';

export class FrameBoundary implements SQLWrapper {
	constructor(
		readonly rank: number,
		private readonly boundarySql: SQL,
	) {}

	getSQL(): SQL {
		return this.boundarySql;
	}

	shouldOmitSQLParens(): boolean {
		return true;
	}
}

export const unboundedPreceding = new FrameBoundary(
	Number.NEGATIVE_INFINITY,
	sql.raw('unbounded preceding'),
);

export const currentRow = new FrameBoundary(0, sql.raw('current row'));

export const unboundedFollowing = new FrameBoundary(
	Number.POSITIVE_INFINITY,
	sql.raw('unbounded following'),
);

export function preceding(offset: number): FrameBoundary {
	validateFrameOffset('preceding', offset);
	return new FrameBoundary(-offset, sql.raw(`${offset} preceding`));
}

export function following(offset: number): FrameBoundary {
	validateFrameOffset('following', offset);
	return new FrameBoundary(offset, sql.raw(`${offset} following`));
}

export interface WindowSpec {
	partitionBy?: SQLWrapper[];
	orderBy?: SQLWrapper[];
	frame?: SQLWrapper;
}

export interface WindowDefinition {
	name: string;
	spec: WindowSpec;
}

export class WindowFunctionBuilder<T = unknown> {
	constructor(private readonly fnSql: SQL<T>) {}

	over(): SQL<T>;
	over(name: string): SQL<T>;
	over(spec: WindowSpec): SQL<T>;
	over(spec?: WindowSpec | string): SQL<T> {
		if (spec === undefined) {
			return this.withOver(sql` over ()`);
		}

		if (typeof spec === 'string') {
			return this.withOver(sql` over ${sql.identifier(spec)}`);
		}

		if (isEmptyWindowSpec(spec)) {
			return this.withOver(sql` over ()`);
		}

		return this.withOver(sql` over (${buildWindowSpecContent(spec)})`);
	}

	private withOver(overClause: SQL): SQL<T> {
		const result = sql`${this.fnSql}${overClause}`;
		result.decoder = this.fnSql.decoder;
		return result as SQL<T>;
	}
}

function isEmptyWindowSpec(spec: WindowSpec): boolean {
	return !spec.partitionBy?.length && !spec.orderBy?.length && !spec.frame;
}

export function buildWindowSpecContent(spec: WindowSpec): SQL {
	const parts: SQL[] = [];

	if (spec.partitionBy?.length) {
		parts.push(sql`partition by ${sql.join(spec.partitionBy, sql`, `)}`);
	}

	if (spec.orderBy?.length) {
		parts.push(sql`order by ${sql.join(spec.orderBy, sql`, `)}`);
	}

	if (spec.frame) {
		parts.push(spec.frame.getSQL());
	}

	return sql.join(parts, sql` `);
}

export function buildWindowClauseSql(windows: WindowDefinition[] | undefined): SQL | undefined {
	if (!windows?.length) {
		return undefined;
	}

	return sql` window ${
		sql.join(
			windows.map(({ name, spec }) => {
				validateWindowName(name);
				return sql`${sql.identifier(name)} as (${buildWindowSpecContent(spec)})`;
			}),
			sql`, `,
		)
	}`;
}

export function validateWindowName(name: string): void {
	if (name.length === 0) {
		throw new Error('Window name must be non-empty');
	}

	if (name.trim().length === 0) {
		throw new Error('Window name must not contain only whitespace');
	}
}

function validateFrameOffset(name: 'preceding' | 'following', value: number): void {
	if (!Number.isInteger(value) || value < 0) {
		throw new Error(`${name}() requires a non-negative integer, got ${value}`);
	}
}

function validatePositiveInteger(value: number, fnName: string): void {
	if (!Number.isInteger(value) || value <= 0) {
		throw new Error(`${fnName}() requires a positive integer, got ${value}`);
	}
}

function validateFrameBoundsOrder(from: FrameBoundary, to: FrameBoundary): void {
	if (from.rank > to.rank) {
		throw new Error('Invalid frame specification: "from" boundary must not be after "to" boundary');
	}
}

export function rows(spec: { from: FrameBoundary; to: FrameBoundary }): SQL {
	validateFrameBoundsOrder(spec.from, spec.to);
	return sql`rows between ${spec.from} and ${spec.to}`;
}

export function range(spec: { from: FrameBoundary; to: FrameBoundary }): SQL {
	validateFrameBoundsOrder(spec.from, spec.to);
	return sql`range between ${spec.from} and ${spec.to}`;
}

type ColumnData<T extends SQLWrapper> = T extends AnyColumn ? T['_']['data'] : unknown;

function expressionDecoder<T extends SQLWrapper>(expression: T) {
	return is(expression, Column) ? expression : String;
}

export function rowNumber(): WindowFunctionBuilder<number> {
	return new WindowFunctionBuilder(sql`row_number()`.mapWith(Number));
}

export function rank(): WindowFunctionBuilder<number> {
	return new WindowFunctionBuilder(sql`rank()`.mapWith(Number));
}

export function denseRank(): WindowFunctionBuilder<number> {
	return new WindowFunctionBuilder(sql`dense_rank()`.mapWith(Number));
}

export function ntile(buckets: number): WindowFunctionBuilder<number> {
	validatePositiveInteger(buckets, 'ntile');
	return new WindowFunctionBuilder(sql`ntile(${sql.raw(String(buckets))})`.mapWith(Number));
}

export function percentRank(): WindowFunctionBuilder<number> {
	return new WindowFunctionBuilder(sql`percent_rank()`.mapWith(Number));
}

export function cumeDist(): WindowFunctionBuilder<number> {
	return new WindowFunctionBuilder(sql`cume_dist()`.mapWith(Number));
}

export function lag<T extends SQLWrapper>(
	expression: T,
): WindowFunctionBuilder<ColumnData<T> | null>;
export function lag<T extends SQLWrapper>(
	expression: T,
	offset: number,
): WindowFunctionBuilder<ColumnData<T> | null>;
export function lag<T extends SQLWrapper, TDefault>(
	expression: T,
	offset: number,
	defaultValue: TDefault,
): WindowFunctionBuilder<ColumnData<T> | TDefault>;
export function lag<T extends SQLWrapper>(
	expression: T,
	offset?: number,
	defaultValue?: unknown,
): WindowFunctionBuilder<any> {
	return new WindowFunctionBuilder(
		buildLagLead('lag', expression, offset, defaultValue),
	);
}

export function lead<T extends SQLWrapper>(
	expression: T,
): WindowFunctionBuilder<ColumnData<T> | null>;
export function lead<T extends SQLWrapper>(
	expression: T,
	offset: number,
): WindowFunctionBuilder<ColumnData<T> | null>;
export function lead<T extends SQLWrapper, TDefault>(
	expression: T,
	offset: number,
	defaultValue: TDefault,
): WindowFunctionBuilder<ColumnData<T> | TDefault>;
export function lead<T extends SQLWrapper>(
	expression: T,
	offset?: number,
	defaultValue?: unknown,
): WindowFunctionBuilder<any> {
	return new WindowFunctionBuilder(
		buildLagLead('lead', expression, offset, defaultValue),
	);
}

function buildLagLead(
	name: 'lag' | 'lead',
	expression: SQLWrapper,
	offset?: number,
	defaultValue?: unknown,
): SQL<any> {
	let fnSql = sql`${sql.raw(name)}(${expression}`;
	const decoder = expressionDecoder(expression);

	if (offset !== undefined) {
		fnSql = sql`${fnSql}, ${sql.raw(String(offset))}`;
	}

	if (defaultValue !== undefined) {
		if (offset === undefined) {
			fnSql = sql`${fnSql}, ${sql.raw('1')}`;
		}
		fnSql = sql`${fnSql}, ${bindIfParam(defaultValue, expression)}`;
	}

	fnSql = sql`${fnSql})`;

	return fnSql.mapWith(decoder);
}

export function firstValue<T extends SQLWrapper>(
	expression: T,
): WindowFunctionBuilder<ColumnData<T> | null> {
	return new WindowFunctionBuilder(
		sql`first_value(${expression})`.mapWith(expressionDecoder(expression)) as SQL<any>,
	);
}

export function lastValue<T extends SQLWrapper>(
	expression: T,
): WindowFunctionBuilder<ColumnData<T> | null> {
	return new WindowFunctionBuilder(
		sql`last_value(${expression})`.mapWith(expressionDecoder(expression)) as SQL<any>,
	);
}

export function nthValue<T extends SQLWrapper>(
	expression: T,
	n: number,
): WindowFunctionBuilder<ColumnData<T> | null> {
	validatePositiveInteger(n, 'nthValue');
	return new WindowFunctionBuilder(
		sql`nth_value(${expression}, ${sql.raw(String(n))})`.mapWith(expressionDecoder(expression)) as SQL<any>,
	);
}

export function windowSum<T extends SQLWrapper>(
	expression: T,
): WindowFunctionBuilder<ColumnData<T> extends number ? string | null : string | null> {
	return new WindowFunctionBuilder(sql`sum(${expression})`.mapWith(String));
}

export function windowAvg<T extends SQLWrapper>(
	expression: T,
): WindowFunctionBuilder<string | null> {
	return new WindowFunctionBuilder(sql`avg(${expression})`.mapWith(String));
}

export function windowMin<T extends SQLWrapper>(
	expression: T,
): WindowFunctionBuilder<(T extends AnyColumn ? GetColumnData<T> : string) | null> {
	return new WindowFunctionBuilder(
		sql`min(${expression})`.mapWith(expressionDecoder(expression)) as SQL<any>,
	);
}

export function windowMax<T extends SQLWrapper>(
	expression: T,
): WindowFunctionBuilder<(T extends AnyColumn ? GetColumnData<T> : string) | null> {
	return new WindowFunctionBuilder(
		sql`max(${expression})`.mapWith(expressionDecoder(expression)) as SQL<any>,
	);
}

export function windowCount(expression?: SQLWrapper): WindowFunctionBuilder<number> {
	const fnSql = expression
		? sql`count(${expression})`.mapWith(Number)
		: sql`count(*)`.mapWith(Number);
	return new WindowFunctionBuilder(fnSql);
}
