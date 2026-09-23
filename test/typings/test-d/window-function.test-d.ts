import { expectError, expectType } from 'tsd'
import { type Kysely, SimplifyFramePlugin, sql } from '..'
import type { Database } from '../shared'

async function testWindowFunctionOutputTypes(db: Kysely<Database>) {
  const result = await db
    .selectFrom('person')
    .select((eb) => [
      eb.fn
        .rowNumber<number>()
        .over((ob) => ob.orderBy('id'))
        .as('row_number'),
      eb.fn.rank<bigint>().over().as('rank'),
      eb.fn.denseRank<string>().over().as('dense_rank'),
      eb.fn.percentRank<number>().over().as('percent_rank'),
      eb.fn.cumeDist<number>().over().as('cume_dist'),
      eb.fn.ntile<number>(4).over().as('ntile'),
      eb.fn.ntile<number>(4n).over().as('ntile_bigint'),
      eb.fn.firstValue<string>('first_name').as('first_value'),
      eb.fn.lastValue<number | null>('age').ignoreNulls().as('last_value'),
      eb.fn.nthValue<Date>('modified_at', 2).respectNulls().as('nth_value'),
      eb.fn.lag<number>('age', 1, 0).as('lag'),
      eb.fn.lead<bigint | null>('age', 1n).as('lead'),
      eb.fn.grouping<number>('gender').as('grouping'),
    ])
    .groupByCube('gender', 'first_name')
    .executeTakeFirstOrThrow()

  expectType<number>(result.row_number)
  expectType<bigint>(result.rank)
  expectType<string>(result.dense_rank)
  expectType<number>(result.percent_rank)
  expectType<number>(result.cume_dist)
  expectType<number>(result.ntile)
  expectType<number>(result.ntile_bigint)
  expectType<string>(result.first_value)
  expectType<number | null>(result.last_value)
  expectType<Date>(result.nth_value)
  expectType<number>(result.lag)
  expectType<bigint | null>(result.lead)
  expectType<number>(result.grouping)
}

function testWindowArgumentsRejectReferences(db: Kysely<Database>) {
  expectError(
    db.selectFrom('person').select((eb) => eb.fn.ntile('age').as('n')),
  )
  expectError(
    db.selectFrom('person').select((eb) => eb.fn.ntile(sql`4`).as('n')),
  )
  expectError(
    db.selectFrom('person').select((eb) => eb.fn.lag('age', 'id').as('lag')),
  )
  expectError(
    db
      .selectFrom('person')
      .select((eb) => eb.fn.lag('age', eb.ref('id')).as('lag')),
  )
  expectError(
    db
      .selectFrom('person')
      .select((eb) => eb.fn.lead('age', 1, eb.ref('age')).as('lead')),
  )
  expectError(
    db
      .selectFrom('person')
      .select((eb) => eb.fn.nthValue('first_name', sql`2`).as('nth')),
  )
  expectError(
    db.selectFrom('person').select((eb) =>
      eb.fn
        .sum<number>('age')
        .over((ob) => ob.rows((fb) => fb.preceding('age')))
        .as('sum'),
    ),
  )
  expectError(
    db.selectFrom('person').select((eb) =>
      eb.fn
        .sum<number>('age')
        .over((ob) => ob.rows((fb) => fb.betweenUnboundedPreceding()))
        .as('sum'),
    ),
  )

  db.selectFrom('person').select((eb) =>
    eb.fn
      .sum<number>('age')
      .over((ob) => ob.orderBy('id').rows((fb) => fb.preceding(sql`1`)))
      .as('sum'),
  )

  db.selectFrom('person').select((eb) =>
    eb.fn
      .sum<number>('age')
      .over((ob) =>
        ob.range((fb) => fb.betweenPreceding(1n).andFollowing(2).excludeTies()),
      )
      .as('sum'),
  )
}

function testGroupByExtensions(db: Kysely<Database>) {
  db.selectFrom('person')
    .select(['gender', 'first_name'])
    .groupBy('gender')
    .groupByCube('first_name', 'last_name')
    .groupByRollup('gender')
    .groupByGroupingSets(['first_name', 'last_name'], 'gender', [])

  db.withPlugin(new SimplifyFramePlugin())
    .selectFrom('person')
    .select((eb) => eb.fn.sum<number>('age').as('total'))

  expectError(db.selectFrom('person').select('id').groupByCube('not_a_column'))
}
