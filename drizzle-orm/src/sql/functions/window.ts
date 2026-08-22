import { type AnyColumn, Column } from '~/column.ts';
import { entityKind, is } from '~/entity.ts';
import { type SQL, sql, type SQLWrapper } from '../sql.ts';

export interface WindowSpec {
	partitionBy?: SQLWrapper | SQLWrapper[];
	orderBy?: SQLWrapper | SQLWrapper[];
	frame?: SQLWrapper;
}

export interface NamedWindow {
	name: string;
	spec: WindowSpec;
}

export interface WindowFrameBound {
	readonly sql: SQL;
	readonly rank: number;
}

function frameBound(text: string, rank: number): WindowFrameBound {
	return { sql: sql.raw(text), rank };
}

export const unboundedPreceding: WindowFrameBound = frameBound('unbounded preceding', Number.NEGATIVE_INFINITY);
export const currentRow: WindowFrameBound = frameBound('current row', 0);
export const unboundedFollowing: WindowFrameBound = frameBound('unbounded following', Number.POSITIVE_INFINITY);

function assertNonNegativeInteger(helperName: string, value: number): void {
	if (typeof value !== 'number' || !Number.isInteger(value) || value < 0) {
		throw new Error(`${helperName}() requires a non-negative integer, received ${value}`);
	}
}

function assertPositiveInteger(fnName: string, value: number): void {
	if (typeof value !== 'number' || !Number.isInteger(value) || value <= 0) {
		throw new Error(`${fnName}() requires a positive integer, received ${value}`);
	}
}

export function assertWindowName(name: string): void {
	if (name === '') {
		throw new Error('Window name must be a non-empty string');
	}
	if (name.trim() === '') {
		throw new Error('Window name must not contain only whitespace');
	}
}

export function preceding(n: number): WindowFrameBound {
	assertNonNegativeInteger('preceding', n);
	return frameBound(`${n} preceding`, -n);
}

export function following(n: number): WindowFrameBound {
	assertNonNegativeInteger('following', n);
	return frameBound(`${n} following`, n);
}

export class WindowFrame implements SQLWrapper {
	static readonly [entityKind]: string = 'WindowFrame';

	constructor(
		readonly unit: 'rows' | 'range',
		readonly from: WindowFrameBound,
		readonly to: WindowFrameBound,
	) {
		if (from.rank > to.rank) {
			throw new Error('Invalid window frame: from boundary is ordered after the to boundary');
		}
	}

	getSQL(): SQL {
		return sql`${sql.raw(this.unit)} between ${this.from.sql} and ${this.to.sql}`;
	}
}

export function rows(spec: { from: WindowFrameBound; to: WindowFrameBound }): WindowFrame {
	return new WindowFrame('rows', spec.from, spec.to);
}

export function range(spec: { from: WindowFrameBound; to: WindowFrameBound }): WindowFrame {
	return new WindowFrame('range', spec.from, spec.to);
}

function asList(value: SQLWrapper | SQLWrapper[] | undefined): SQLWrapper[] {
	if (value === undefined) {
		return [];
	}
	return Array.isArray(value) ? value : [value];
}

export function buildWindowSpecSql(spec?: WindowSpec): SQL {
	if (!spec) {
		return sql.empty();
	}

	const parts: SQL[] = [];
	const partitionBy = asList(spec.partitionBy);
	if (partitionBy.length > 0) {
		parts.push(sql`partition by ${sql.join(partitionBy, sql`, `)}`);
	}
	const orderBy = asList(spec.orderBy);
	if (orderBy.length > 0) {
		parts.push(sql`order by ${sql.join(orderBy, sql`, `)}`);
	}
	if (spec.frame) {
		parts.push(spec.frame.getSQL());
	}
	return sql.join(parts, sql` `);
}

export function buildNamedWindowsSql(windows?: NamedWindow[]): SQL | undefined {
	if (!windows || windows.length === 0) {
		return undefined;
	}
	const definitions = windows.map((window) =>
		sql`${sql.identifier(window.name)} as (${buildWindowSpecSql(window.spec)})`
	);
	return sql` window ${sql.join(definitions, sql`, `)}`;
}

type InferExprType<T extends SQLWrapper> = T extends AnyColumn ? T['_']['data']
	: T extends SQL<infer U> ? U
	: unknown;

export class WindowFunctionBuilder<T> {
	static readonly [entityKind]: string = 'WindowFunctionBuilder';

	constructor(
		private readonly fnSql: SQL,
		private readonly mapper?: ((value: unknown) => T) | SQLWrapper,
	) {}

	over(spec?: WindowSpec | string): SQL<T> {
		let result: SQL;
		if (typeof spec === 'string') {
			assertWindowName(spec);
			result = sql`${this.fnSql} over ${sql.identifier(spec)}`;
		} else {
			result = sql`${this.fnSql} over (${buildWindowSpecSql(spec)})`;
		}
		if (this.mapper) {
			return result.mapWith(this.mapper as any) as SQL<T>;
		}
		return result as SQL<T>;
	}
}

function ranking(fnSql: SQL): WindowFunctionBuilder<number> {
	return new WindowFunctionBuilder<number>(fnSql, Number);
}

export function rowNumber(): WindowFunctionBuilder<number> {
	return ranking(sql`row_number()`);
}

export function rank(): WindowFunctionBuilder<number> {
	return ranking(sql`rank()`);
}

export function denseRank(): WindowFunctionBuilder<number> {
	return ranking(sql`dense_rank()`);
}

export function ntile(buckets: number): WindowFunctionBuilder<number> {
	assertPositiveInteger('ntile', buckets);
	return ranking(sql`ntile(${sql.raw(String(buckets))})`);
}

export function percentRank(): WindowFunctionBuilder<number> {
	return ranking(sql`percent_rank()`);
}

export function cumeDist(): WindowFunctionBuilder<number> {
	return ranking(sql`cume_dist()`);
}

export function lag<T extends SQLWrapper>(value: T): WindowFunctionBuilder<InferExprType<T> | null>;
export function lag<T extends SQLWrapper>(value: T, offset: number): WindowFunctionBuilder<InferExprType<T> | null>;
export function lag<T extends SQLWrapper>(
	value: T,
	offset: number,
	defaultValue: SQLWrapper,
): WindowFunctionBuilder<NonNullable<InferExprType<T>>>;
export function lag(
	value: SQLWrapper,
	offset?: number,
	defaultValue?: SQLWrapper,
): WindowFunctionBuilder<unknown> {
	return new WindowFunctionBuilder(buildOffsetFn('lag', value, offset, defaultValue));
}

export function lead<T extends SQLWrapper>(value: T): WindowFunctionBuilder<InferExprType<T> | null>;
export function lead<T extends SQLWrapper>(value: T, offset: number): WindowFunctionBuilder<InferExprType<T> | null>;
export function lead<T extends SQLWrapper>(
	value: T,
	offset: number,
	defaultValue: SQLWrapper,
): WindowFunctionBuilder<NonNullable<InferExprType<T>>>;
export function lead(
	value: SQLWrapper,
	offset?: number,
	defaultValue?: SQLWrapper,
): WindowFunctionBuilder<unknown> {
	return new WindowFunctionBuilder(buildOffsetFn('lead', value, offset, defaultValue));
}

function buildOffsetFn(
	name: 'lag' | 'lead',
	value: SQLWrapper,
	offset?: number,
	defaultValue?: SQLWrapper,
): SQL {
	if (offset !== undefined && defaultValue !== undefined) {
		return sql`${sql.raw(name)}(${value}, ${sql.raw(String(offset))}, ${defaultValue})`;
	}
	if (offset !== undefined) {
		return sql`${sql.raw(name)}(${value}, ${sql.raw(String(offset))})`;
	}
	if (defaultValue !== undefined) {
		return sql`${sql.raw(name)}(${value}, ${sql.raw('1')}, ${defaultValue})`;
	}
	return sql`${sql.raw(name)}(${value})`;
}

export function firstValue<T extends SQLWrapper>(value: T): WindowFunctionBuilder<InferExprType<T> | null> {
	return new WindowFunctionBuilder(sql`first_value(${value})`);
}

export function lastValue<T extends SQLWrapper>(value: T): WindowFunctionBuilder<InferExprType<T> | null> {
	return new WindowFunctionBuilder(sql`last_value(${value})`);
}

export function nthValue<T extends SQLWrapper>(
	value: T,
	n: number,
): WindowFunctionBuilder<InferExprType<T> | null> {
	assertPositiveInteger('nthValue', n);
	return new WindowFunctionBuilder(sql`nth_value(${value}, ${sql.raw(String(n))})`);
}

export function windowSum(expression: SQLWrapper): WindowFunctionBuilder<string | null> {
	return new WindowFunctionBuilder(sql`sum(${expression})`, String);
}

export function windowAvg(expression: SQLWrapper): WindowFunctionBuilder<string | null> {
	return new WindowFunctionBuilder(sql`avg(${expression})`, String);
}

export function windowMin<T extends SQLWrapper>(
	expression: T,
): WindowFunctionBuilder<(T extends AnyColumn ? T['_']['data'] : string) | null> {
	return new WindowFunctionBuilder(
		sql`min(${expression})`,
		is(expression, Column) ? expression : String,
	) as WindowFunctionBuilder<(T extends AnyColumn ? T['_']['data'] : string) | null>;
}

export function windowMax<T extends SQLWrapper>(
	expression: T,
): WindowFunctionBuilder<(T extends AnyColumn ? T['_']['data'] : string) | null> {
	return new WindowFunctionBuilder(
		sql`max(${expression})`,
		is(expression, Column) ? expression : String,
	) as WindowFunctionBuilder<(T extends AnyColumn ? T['_']['data'] : string) | null>;
}

export function windowCount(expression?: SQLWrapper): WindowFunctionBuilder<number> {
	return new WindowFunctionBuilder(sql`count(${expression || sql.raw('*')})`, Number);
}
