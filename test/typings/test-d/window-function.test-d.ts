import { expectError, expectType } from 'tsd'
import { type Kysely, sql } from '..'
import type { Database } from '../shared'

async function testRankingFunctionsWithDefaultGenerics(db: Kysely<Database>) {
  const result = await db
    .selectFrom('person')
    .select((eb) => [
      eb.fn.rowNumber().over().as('row_number'),
      eb.fn.rank().over().as('rank'),
      eb.fn.denseRank().over().as('dense_rank'),
      eb.fn.percentRank().over().as('percent_rank'),
      eb.fn.cumeDist().over().as('cume_dist'),
      eb.fn.ntile(4).over().as('ntile'),
    ])
    .executeTakeFirstOrThrow()

  expectType<string | number | bigint>(result.row_number)
  expectType<string | number | bigint>(result.rank)
  expectType<string | number | bigint>(result.dense_rank)
  expectType<number>(result.percent_rank)
  expectType<number>(result.cume_dist)
  expectType<string | number | bigint>(result.ntile)
}

async function testRankingFunctionsWithCustomGenerics(db: Kysely<Database>) {
  const result = await db
    .selectFrom('person')
    .select((eb) => [
      eb.fn.rowNumber<number>().over().as('row_number'),
      eb.fn.rank<bigint>().over().as('rank'),
      eb.fn.denseRank<string>().over().as('dense_rank'),
      eb.fn.percentRank<string>().over().as('percent_rank'),
      eb.fn.cumeDist<string>().over().as('cume_dist'),
      eb.fn.ntile<number>(BigInt(4)).over().as('ntile'),
    ])
    .executeTakeFirstOrThrow()

  expectType<number>(result.row_number)
  expectType<bigint>(result.rank)
  expectType<string>(result.dense_rank)
  expectType<string>(result.percent_rank)
  expectType<string>(result.cume_dist)
  expectType<number>(result.ntile)

  expectError(db.selectFrom('person').select((eb) => eb.fn.rowNumber<Date>()))
  expectError(
    db.selectFrom('person').select((eb) => eb.fn.percentRank<bigint>()),
  )
}

async function testValueFunctionsWithDefaultGenerics(db: Kysely<Database>) {
  const result = await db
    .selectFrom('person')
    .select((eb) => [
      eb.fn.firstValue('first_name').over().as('first_value'),
      eb.fn.lastValue('last_name').over().as('last_value'),
      eb.fn.firstValue(eb.ref('age')).over().as('first_age'),
      eb.fn.nthValue('age', 2).over().as('nth_value'),
      eb.fn.lag('first_name').over().as('lag'),
      eb.fn.lag('age', 2, 0).over().as('lag_with_default'),
      eb.fn.lead('person.first_name', BigInt(1)).over().as('lead'),
    ])
    .executeTakeFirstOrThrow()

  expectType<string>(result.first_value)
  expectType<string | null>(result.last_value)
  expectType<number>(result.first_age)
  expectType<number | null>(result.nth_value)
  expectType<string | null>(result.lag)
  expectType<number | null>(result.lag_with_default)
  expectType<string | null>(result.lead)
}

async function testValueFunctionsWithCustomGenerics(db: Kysely<Database>) {
  const result = await db
    .selectFrom('person')
    .select((eb) => [
      eb.fn.firstValue<number>('first_name').over().as('first_value'),
      eb.fn.lastValue<string>('last_name').over().as('last_value'),
      eb.fn.nthValue<number>('age', 2).over().as('nth_value'),
      eb.fn.lag<string>('first_name').over().as('lag'),
      eb.fn.lead<number>('age', 1, 0).over().as('lead'),
    ])
    .executeTakeFirstOrThrow()

  expectType<number>(result.first_value)
  expectType<string>(result.last_value)
  expectType<number>(result.nth_value)
  expectType<string>(result.lag)
  expectType<number>(result.lead)
}

async function testValueFunctionArguments(db: Kysely<Database>) {
  db.selectFrom('person').select((eb) => [
    eb.fn.lag('age', undefined, 0).as('lag'),
    eb.fn.nthValue('age', BigInt(1)).as('nth_value'),
  ])

  expectError(db.selectFrom('person').select((eb) => eb.fn.ntile('age')))
  expectError(
    db.selectFrom('person').select((eb) => eb.fn.ntile(eb.ref('age'))),
  )
  expectError(db.selectFrom('person').select((eb) => eb.fn.nthValue('age')))
  expectError(
    db.selectFrom('person').select((eb) => eb.fn.nthValue('age', 'age')),
  )
  expectError(db.selectFrom('person').select((eb) => eb.fn.lag('age', 'age')))
  expectError(
    db.selectFrom('person').select((eb) => eb.fn.lead('age', 1, eb.ref('age'))),
  )
  expectError(
    db.selectFrom('person').select((eb) => eb.fn.firstValue('not_a_column')),
  )
}

async function testNullTreatment(db: Kysely<Database>) {
  const result = await db
    .selectFrom('person')
    .select((eb) => [
      eb.fn.firstValue('first_name').ignoreNulls().over().as('first_value'),
      eb.fn.lag<number>('age').respectNulls().over().as('lag'),
    ])
    .executeTakeFirstOrThrow()

  expectType<string>(result.first_value)
  expectType<number>(result.lag)
}

async function testFrames(db: Kysely<Database>) {
  db.selectFrom('person').select((eb) => [
    eb.fn
      .sum('age')
      .over((ob) => ob.orderBy('age').rows((f) => f.unboundedPreceding()))
      .as('a'),
    eb.fn
      .sum('age')
      .over((ob) => ob.range((f) => f.preceding(BigInt(1)).excludeTies()))
      .as('b'),
    eb.fn
      .sum('age')
      .over((ob) =>
        ob.groups((f) =>
          f.betweenFollowing(sql.lit(1)).andUnboundedFollowing().excludeGroup(),
        ),
      )
      .as('c'),
  ])

  // An incomplete frame.
  expectError(
    db
      .selectFrom('person')
      .select((eb) =>
        eb.fn.sum('age').over((ob) => ob.rows((f) => f.betweenPreceding(1))),
      ),
  )

  // No frame bound at all.
  expectError(
    db
      .selectFrom('person')
      .select((eb) => eb.fn.sum('age').over((ob) => ob.rows((f) => f))),
  )

  // Exclusions must follow a complete frame.
  expectError(
    db
      .selectFrom('person')
      .select((eb) =>
        eb.fn
          .sum('age')
          .over((ob) =>
            ob.rows((f) => f.betweenCurrentRow().excludeTies().andCurrentRow()),
          ),
      ),
  )

  // Offsets are numbers, bigints or expressions.
  expectError(
    db
      .selectFrom('person')
      .select((eb) =>
        eb.fn.sum('age').over((ob) => ob.rows((f) => f.preceding('1'))),
      ),
  )
}

async function testGroupingElements(db: Kysely<Database>) {
  const result = await db
    .selectFrom('person')
    .select((eb) => [
      'gender',
      'marital_status',
      eb.fn.grouping('gender').as('gender_grouping'),
      eb.fn.grouping<bigint>('marital_status').as('marital_status_grouping'),
      eb.fn.countAll().as('person_count'),
    ])
    .groupBy('first_name')
    .groupByCube('gender', 'person.marital_status')
    .groupByRollup('age', (eb) => eb.ref('last_name'))
    .groupByGroupingSets(['gender', 'age'], 'marital_status', [])
    .executeTakeFirstOrThrow()

  expectType<number>(result.gender_grouping)
  expectType<bigint>(result.marital_status_grouping)

  expectError(db.selectFrom('person').groupByCube('not_a_column'))
  expectError(db.selectFrom('person').groupByRollup('pet.name'))
  expectError(db.selectFrom('person').groupByGroupingSets(['not_a_column']))
  expectError(
    db.selectFrom('person').select((eb) => eb.fn.grouping('not_a_column')),
  )
}
