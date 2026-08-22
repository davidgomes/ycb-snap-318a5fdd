import postgres from 'postgres';
import { beforeEach, describe, expect, it } from 'vitest';
import { integer, pgTable, text } from '~/pg-core';
import { PgDialect } from '~/pg-core/dialect.ts';
import { drizzle } from '~/postgres-js';
import {
	cumeDist,
	currentRow,
	denseRank,
	firstValue,
	following,
	lag,
	lead,
	ntile,
	nthValue,
	percentRank,
	preceding,
	rank,
	range,
	rowNumber,
	rows,
	unboundedFollowing,
	unboundedPreceding,
	validateWindowName,
	windowAvg,
	windowCount,
	windowMax,
	windowMin,
	windowSum,
} from '~/sql/functions/window.ts';
import { asc, desc } from '~/sql/expressions/select.ts';

const users = pgTable('users', {
	id: integer().primaryKey(),
	name: text().notNull(),
	dept: text().notNull(),
	salary: integer().notNull(),
});

const dialect = new PgDialect();
const db = drizzle(postgres(''));

describe('window function helpers', () => {
	beforeEach(() => {
		dialect.casing.clearCache();
	});

	it('compiles ranking helpers to snake_case SQL names', () => {
		expect(dialect.sqlToQuery(rowNumber().over()).sql).toBe('row_number() over ()');
		expect(dialect.sqlToQuery(rank().over()).sql).toBe('rank() over ()');
		expect(dialect.sqlToQuery(denseRank().over()).sql).toBe('dense_rank() over ()');
		expect(dialect.sqlToQuery(percentRank().over()).sql).toBe('percent_rank() over ()');
		expect(dialect.sqlToQuery(cumeDist().over()).sql).toBe('cume_dist() over ()');
		expect(dialect.sqlToQuery(ntile(4).over()).sql).toBe('ntile(4) over ()');
	});

	it('compiles aggregate window helpers', () => {
		expect(dialect.sqlToQuery(windowCount().over()).sql).toBe('count(*) over ()');
		expect(dialect.sqlToQuery(windowCount(users.id).over()).sql).toBe('count("users"."id") over ()');
		expect(dialect.sqlToQuery(windowSum(users.salary).over()).sql).toBe('sum("users"."salary") over ()');
		expect(dialect.sqlToQuery(windowAvg(users.salary).over()).sql).toBe('avg("users"."salary") over ()');
		expect(dialect.sqlToQuery(windowMin(users.salary).over()).sql).toBe('min("users"."salary") over ()');
		expect(dialect.sqlToQuery(windowMax(users.salary).over()).sql).toBe('max("users"."salary") over ()');
	});

	it('accepts optional trailing arguments for lag and lead', () => {
		const oneArg = dialect.sqlToQuery(lag(users.salary).over());
		expect(oneArg.sql).toBe('lag("users"."salary") over ()');
		expect(oneArg.params).toEqual([]);

		const twoArgs = dialect.sqlToQuery(lag(users.salary, 2).over());
		expect(twoArgs.sql).toBe('lag("users"."salary", 2) over ()');
		expect(twoArgs.params).toEqual([]);

		const threeArgs = dialect.sqlToQuery(lag(users.salary, 2, 0).over());
		expect(threeArgs.sql).toBe('lag("users"."salary", 2, $1) over ()');
		expect(threeArgs.params).toEqual([0]);

		const leadWithDefault = dialect.sqlToQuery(lead(users.salary, 1, 'none').over());
		expect(leadWithDefault.sql).toBe('lead("users"."salary", 1, $1) over ()');
		expect(leadWithDefault.params).toEqual(['none']);
	});

	it('inlines numeric positional arguments including zero', () => {
		const zeroOffset = dialect.sqlToQuery(lag(users.salary, 0).over());
		expect(zeroOffset.sql).toBe('lag("users"."salary", 0) over ()');
		expect(zeroOffset.params).toEqual([]);

		const nth = dialect.sqlToQuery(nthValue(users.salary, 1).over());
		expect(nth.sql).toBe('nth_value("users"."salary", 1) over ()');
		expect(nth.params).toEqual([]);
	});

	it('appends empty over () for empty specifications', () => {
		expect(dialect.sqlToQuery(rowNumber().over()).sql).toBe('row_number() over ()');
		expect(dialect.sqlToQuery(rowNumber().over({})).sql).toBe('row_number() over ()');
	});

	it('builds inline over specifications', () => {
		const query = dialect.sqlToQuery(
			rowNumber().over({
				partitionBy: [users.dept],
				orderBy: [desc(users.salary)],
				frame: rows({ from: unboundedPreceding, to: currentRow }),
			}),
		);

		expect(query.sql).toBe(
			'row_number() over (partition by "users"."dept" order by "users"."salary" desc rows between unbounded preceding and current row)',
		);
	});

	it('references named windows without parentheses', () => {
		const query = dialect.sqlToQuery(rowNumber().over('employee_rank'));
		expect(query.sql).toBe('row_number() over "employee_rank"');
	});

	it('compiles value access helpers', () => {
		expect(dialect.sqlToQuery(firstValue(users.name).over()).sql).toBe('first_value("users"."name") over ()');
		expect(dialect.sqlToQuery(lead(users.name).over()).sql).toBe('lead("users"."name") over ()');
	});

	it('builds range frames', () => {
		const query = dialect.sqlToQuery(
			windowSum(users.salary).over({
				orderBy: [asc(users.salary)],
				frame: range({ from: preceding(1), to: following(1) }),
			}),
		);

		expect(query.sql).toBe(
			'sum("users"."salary") over (order by "users"."salary" asc range between 1 preceding and 1 following)',
		);
	});
});

describe('window validation', () => {
	it('rejects non-positive ntile and nthValue arguments', () => {
		expect(() => ntile(0)).toThrow('ntile() requires a positive integer, got 0');
		expect(() => nthValue(users.salary, -2)).toThrow('nthValue() requires a positive integer, got -2');
	});

	it('rejects invalid window names', () => {
		expect(() => validateWindowName('')).toThrow('non-empty');
		expect(() => validateWindowName('   ')).toThrow('whitespace');
	});

	it('rejects invalid frame boundaries for preceding and following', () => {
		expect(() => preceding(-1)).toThrow('preceding()');
		expect(() => following(1.5)).toThrow('following()');
	});

	it('rejects frames where from is ordered after to', () => {
		expect(() => rows({ from: currentRow, to: unboundedPreceding })).toThrow('"from"');
		expect(() => range({ from: following(2), to: preceding(1) })).toThrow('"from"');
	});
});

describe('select builder window definitions', () => {
	beforeEach(() => {
		dialect.casing.clearCache();
	});

	it('adds a window clause before order by', () => {
		const query = db
			.select({ rn: rowNumber().over('w') })
			.from(users)
			.window('w', {
				partitionBy: [users.dept],
				orderBy: [desc(users.salary)],
			})
			.orderBy(asc(users.name));

		expect(query.toSQL().sql).toBe(
			'select row_number() over "w" from "users" window "w" as (partition by "users"."dept" order by "users"."salary" desc) order by "users"."name" asc',
		);
	});

	it('rejects invalid window names on the builder', () => {
		expect(() =>
			db.select().from(users).window('', { orderBy: [asc(users.name)] })
		).toThrow('non-empty');

		expect(() =>
			db.select().from(users).window('   ', { orderBy: [asc(users.name)] })
		).toThrow('whitespace');
	});
});

describe('window exports', () => {
	it('exports helpers from the top-level package', async () => {
		const root = await import('~/index.ts');
		expect(root.rowNumber).toBeTypeOf('function');
		expect(root.unboundedPreceding).toBeDefined();
		expect(root.rows).toBeTypeOf('function');
		expect(root.range).toBeTypeOf('function');
		expect(root.windowCount).toBeTypeOf('function');
	});
});
