import { expect, test } from 'vitest';
import { integer, pgTable, QueryBuilder } from '~/pg-core';
import { PgDialect } from '~/pg-core/dialect.ts';
import {
	cumeDist,
	currentRow,
	denseRank,
	firstValue,
	following,
	lag,
	lastValue,
	lead,
	nthValue,
	ntile,
	percentRank,
	preceding,
	range,
	rank,
	rowNumber,
	rows,
	unboundedFollowing,
	unboundedPreceding,
	windowAvg,
	windowCount,
	windowMax,
	windowMin,
	windowSum,
} from '~/sql/functions/window.ts';

const dialect = new PgDialect();
const users = pgTable('users', {
	id: integer('id').notNull(),
	score: integer('score'),
});

function compile(sqlLike: { getSQL(): { toQuery: typeof dialect.sqlToQuery } } | ReturnType<typeof rowNumber>['over']) {
	const sql = 'getSQL' in sqlLike ? sqlLike.getSQL() : sqlLike;
	return dialect.sqlToQuery(sql as any);
}

test('ranking helpers use snake_case names', () => {
	expect(compile(rowNumber().over()).sql).toBe('row_number() over ()');
	expect(compile(rank().over()).sql).toBe('rank() over ()');
	expect(compile(denseRank().over()).sql).toBe('dense_rank() over ()');
	expect(compile(percentRank().over()).sql).toBe('percent_rank() over ()');
	expect(compile(cumeDist().over()).sql).toBe('cume_dist() over ()');
	expect(compile(ntile(4).over()).sql).toBe('ntile(4) over ()');
	expect(compile(ntile(4).over()).params).toEqual([]);
});

test('offset and value helpers compile optional trailing arguments inline', () => {
	expect(compile(lag(users.score).over()).sql).toContain('lag("users"."score")');
	expect(compile(lag(users.score, 0).over()).sql).toContain('lag("users"."score", 0)');
	expect(compile(lag(users.score, 0).over()).params).toEqual([]);
	expect(compile(lead(users.score, 1, users.id).over()).sql).toContain('lead("users"."score", 1, "users"."id")');
	expect(compile(firstValue(users.score).over()).sql).toContain('first_value("users"."score")');
	expect(compile(lastValue(users.score).over()).sql).toContain('last_value("users"."score")');
	expect(compile(nthValue(users.score, 2).over()).sql).toContain('nth_value("users"."score", 2)');
	expect(compile(nthValue(users.score, 2).over()).params).toEqual([]);
});

test('window aggregates and empty over spec', () => {
	expect(compile(windowSum(users.score).over()).sql).toBe('sum("users"."score") over ()');
	expect(compile(windowAvg(users.score).over()).sql).toBe('avg("users"."score") over ()');
	expect(compile(windowMin(users.score).over()).sql).toBe('min("users"."score") over ()');
	expect(compile(windowMax(users.score).over()).sql).toBe('max("users"."score") over ()');
	expect(compile(windowCount().over()).sql).toBe('count(*) over ()');
	expect(compile(windowCount(users.id).over()).sql).toBe('count("users"."id") over ()');
});

test('named window reference has no parentheses', () => {
	expect(compile(rowNumber().over('w')).sql).toBe('row_number() over "w"');
});

test('named window definitions compile before order by', () => {
	const qb = new QueryBuilder();
	const query = qb
		.select({
			id: users.id,
			n: rowNumber().over('w'),
		})
		.from(users)
		.window('w', { partitionBy: users.id, orderBy: users.score })
		.orderBy(users.id)
		.toSQL();

	expect(query.sql).toContain('window "w" as (partition by "users"."id" order by "users"."score")');
	expect(query.sql.indexOf('window ')).toBeLessThan(query.sql.indexOf('order by'));
});

test('ntile and nthValue reject non-positive integers', () => {
	expect(() => ntile(0)).toThrow(/ntile.*0/);
	expect(() => ntile(-1)).toThrow(/ntile/);
	expect(() => nthValue(users.score, 0)).toThrow(/nthValue.*0/);
});

test('window names reject empty and whitespace-only values', () => {
	const qb = new QueryBuilder();
	expect(() => qb.select().from(users).window('', {})).toThrow(/non-empty/);
	expect(() => qb.select().from(users).window('   ', {})).toThrow(/whitespace/);
});

test('frame constructors validate boundaries', () => {
	expect(() => rows({ from: currentRow, to: unboundedPreceding })).toThrow(/from/);
	expect(() => range({ from: following(1), to: preceding(1) })).toThrow(/from/);
	expect(() => preceding(-1)).toThrow(/preceding/);
	expect(() => following(1.5)).toThrow(/following/);
	expect(
		compile(windowSum(users.score).over({
			frame: rows({ from: unboundedPreceding, to: currentRow }),
		})).sql,
	).toBe('sum("users"."score") over (rows between unbounded preceding and current row)');
	expect(
		compile(windowSum(users.score).over({
			frame: range({ from: currentRow, to: unboundedFollowing }),
		})).sql,
	).toBe('sum("users"."score") over (range between current row and unbounded following)');
});
