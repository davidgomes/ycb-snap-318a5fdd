import { expectError, expectType } from 'tsd'
import { type Kysely, sql } from '..'
import type { Database } from '../shared'

async function testWindowFunctionsWithDefaultGenerics(db: Kysely<Database>) {
  const result = await db
    .selectFrom('person')
    .select((eb) => [
      eb.fn.rowNumber().over().as('row_number'),
      eb.fn.rank().over().as('rank'),
      eb.fn.denseRank().over().as('dense_rank'),
      eb.fn.percentRank().over().as('percent_rank'),
      eb.fn.cumeDist().over().as('cume_dist'),
      eb.fn.ntile(4).over().as('ntile'),
      eb.fn.firstValue('first_name').over().as('first_value'),
      eb.fn.lastValue('last_name').over().as('last_value'),
      eb.fn.nthValue('age', 2).over().as('nth_value'),
      eb.fn.lag('first_name').over().as('lag'),
      eb.fn.lead('age', 1n, 0).over().as('lead'),
      eb.fn.grouping('gender').as('grouping'),
    ])
    .executeTakeFirstOrThrow()

  expectType<string | number | bigint>(result.row_number)
  expectType<string | number | bigint>(result.rank)
  expectType<string | number | bigint>(result.dense_rank)
  expectType<number>(result.percent_rank)
  expectType<number>(result.cume_dist)
  expectType<string | number | bigint>(result.ntile)
  expectType<string>(result.first_value)
  expectType<string | null>(result.last_value)
  expectType<number | null>(result.nth_value)
  expectType<string | null>(result.lag)
  expectType<number | null>(result.lead)
  expectType<number>(result.grouping)
}

async function testWindowFunctionsWithCustomGenerics(db: Kysely<Database>) {
  const result = await db
    .selectFrom('person')
    .select((eb) => [
      eb.fn.rowNumber<number>().over().as('row_number'),
      eb.fn.rank<bigint>().over().as('rank'),
      eb.fn.denseRank<string>().over().as('dense_rank'),
      eb.fn.percentRank<string>().over().as('percent_rank'),
      eb.fn.cumeDist<string>().over().as('cume_dist'),
      eb.fn.ntile<number>(4).over().as('ntile'),
      eb.fn.firstValue<number>('first_name').over().as('first_value'),
      eb.fn.lastValue<number>('last_name').over().as('last_value'),
      eb.fn.nthValue<string>('age', 2).over().as('nth_value'),
      eb.fn.lag<number>('first_name').over().as('lag'),
      eb.fn.lead<string>('age', 1).over().as('lead'),
      eb.fn.grouping<bigint>('gender').as('grouping'),
    ])
    .executeTakeFirstOrThrow()

  expectType<number>(result.row_number)
  expectType<bigint>(result.rank)
  expectType<string>(result.dense_rank)
  expectType<string>(result.percent_rank)
  expectType<string>(result.cume_dist)
  expectType<number>(result.ntile)
  expectType<number>(result.first_value)
  expectType<number>(result.last_value)
  expectType<string>(result.nth_value)
  expectType<number>(result.lag)
  expectType<string>(result.lead)
  expectType<bigint>(result.grouping)
}

async function testWindowFunctionArguments(db: Kysely<Database>) {
  // Bucket counts, positions, offsets and default values are values, not references.
  expectError(
    db.selectFrom('person').select((eb) => eb.fn.ntile('age').as('n')),
  )
  expectError(
    db
      .selectFrom('person')
      .select((eb) => eb.fn.nthValue('age', 'age').as('n')),
  )
  expectError(
    db.selectFrom('person').select((eb) => eb.fn.lag('age', 'age').as('n')),
  )
  expectError(
    db.selectFrom('person').select((eb) => eb.fn.lead('age', 1, 'age').as('n')),
  )
  expectError(
    db.selectFrom('person').select((eb) => eb.fn.firstValue('nope').as('n')),
  )
}

async function testNullTreatment(db: Kysely<Database>) {
  const result = await db
    .selectFrom('person')
    .select((eb) => [
      eb.fn
        .lag('first_name')
        .ignoreNulls()
        .over((ob) => ob.orderBy('id'))
        .as('lag'),
      eb.fn.firstValue('age').respectNulls().over().as('first_value'),
    ])
    .executeTakeFirstOrThrow()

  expectType<string | null>(result.lag)
  expectType<number>(result.first_value)
}

async function testFrames(db: Kysely<Database>) {
  db.selectFrom('person').select((eb) =>
    eb.fn
      .sum<number>('age')
      .over((ob) =>
        ob
          .orderBy('id')
          .rows((fb) => fb.betweenPreceding(sql.lit(1)).andFollowing(2n))
          .range((fb) => fb.unboundedPreceding().excludeTies())
          .groups((fb) =>
            fb.betweenCurrentRow().andUnboundedFollowing().excludeGroup(),
          ),
      )
      .as('s'),
  )

  // An incomplete `between` frame is not accepted.
  expectError(
    db.selectFrom('person').select((eb) =>
      eb.fn
        .sum<number>('age')
        .over((ob) => ob.rows((fb) => fb.betweenPreceding(1)))
        .as('s'),
    ),
  )

  // Offsets are values or expressions, not references.
  expectError(
    db.selectFrom('person').select((eb) =>
      eb.fn
        .sum<number>('age')
        .over((ob) => ob.rows((fb) => fb.preceding('age')))
        .as('s'),
    ),
  )

  // `and*` methods are only available after `between*`.
  expectError(
    db.selectFrom('person').select((eb) =>
      eb.fn
        .sum<number>('age')
        .over((ob) => ob.rows((fb) => fb.currentRow().andCurrentRow()))
        .as('s'),
    ),
  )
}

async function testGroupingElements(db: Kysely<Database>) {
  db.selectFrom('person')
    .select(['gender', 'first_name'])
    .groupBy('gender')
    .groupByCube('first_name', 'last_name')
    .groupByRollup('age', 'person.marital_status')
    .groupByGroupingSets(['first_name', 'age'], ['gender'], [])

  expectError(db.selectFrom('person').groupByCube('nope'))
  expectError(db.selectFrom('person').groupByRollup('nope'))
  expectError(db.selectFrom('person').groupByGroupingSets(['nope']))
}
