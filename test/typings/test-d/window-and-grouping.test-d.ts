import { expectError, expectType } from 'tsd'
import { type Kysely, sql } from '..'
import type { Database } from '../shared'

async function testGroupedAggregationAndWindows(db: Kysely<Database>) {
  const result = await db
    .selectFrom('person')
    .select((eb) => [
      eb.fn.rowNumber<number>().over().as('rn'),
      eb.fn.rank<number>().over().as('rank'),
      eb.fn.denseRank<bigint>().over().as('dense'),
      eb.fn.percentRank<number>().over().as('pct'),
      eb.fn.cumeDist<number>().over().as('cume'),
      eb.fn.ntile<number>(4).over().as('tile'),
      eb.fn.firstValue<string>('first_name').respectNulls().over().as('first'),
      eb.fn.lastValue<string | null>('last_name').over().as('last'),
      eb.fn.nthValue<string>('first_name', 2).ignoreNulls().over().as('nth'),
      eb.fn.lag<string | null>('first_name', 1, 0).over().as('prev'),
      eb.fn.lead<string>('first_name', 1n).over().as('next'),
      eb.fn.grouping<number>('gender').as('g'),
      eb.fn
        .sum<number>('age')
        .over((ob) =>
          ob
            .orderBy('id')
            .rows((fb) => fb.betweenPreceding(sql`1`).andCurrentRow())
            .range((fb) => fb.unboundedFollowing())
            .groups((fb) =>
              fb.betweenCurrentRow().andFollowing(2).excludeTies(),
            ),
        )
        .as('total'),
    ])
    .groupBy('gender')
    .groupByCube('first_name', 'last_name')
    .groupByRollup('gender')
    .groupByGroupingSets(['gender'], ['first_name', 'last_name'])
    .executeTakeFirstOrThrow()

  expectType<number>(result.rn)
  expectType<number>(result.rank)
  expectType<bigint>(result.dense)
  expectType<number>(result.pct)
  expectType<number>(result.cume)
  expectType<number>(result.tile)
  expectType<string>(result.first)
  expectType<string | null>(result.last)
  expectType<string>(result.nth)
  expectType<string | null>(result.prev)
  expectType<string>(result.next)
  expectType<number>(result.g)
  expectType<number>(result.total)
}

function testRejectedOffsets(db: Kysely<Database>) {
  expectError(db.fn.ntile('id'))
  expectError(db.fn.nthValue('first_name', db.dynamic.ref('id')))

  db.selectFrom('person').select((eb) => {
    expectError(eb.fn.lag('first_name', eb.ref('id')))
    expectError(eb.fn.lead('first_name', 1, eb.ref('id')))
    expectError(
      eb.fn
        .sum<number>('age')
        .over((ob) => ob.rows((fb) => fb.betweenUnboundedPreceding())),
    )

    return eb.fn.rowNumber<number>().as('rn')
  })
}
