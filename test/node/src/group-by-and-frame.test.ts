import {
  DummyDriver,
  Kysely,
  PostgresAdapter,
  PostgresIntrospector,
  PostgresQueryCompiler,
  SimplifyFramePlugin,
  sql,
} from '../../../'

import { expect } from 'chai'

interface Database {
  person: {
    id: number
    first_name: string
    last_name: string
    gender: string
    age: number
  }
}

describe('group by modifiers and window frames', () => {
  const db = new Kysely<Database>({
    dialect: {
      createAdapter: () => new PostgresAdapter(),
      createDriver: () => new DummyDriver(),
      createIntrospector: (kysely) => new PostgresIntrospector(kysely),
      createQueryCompiler: () => new PostgresQueryCompiler(),
    },
  })

  it('compiles cube, rollup and grouping sets together with groupBy', () => {
    const compiled = db
      .selectFrom('person')
      .select((eb) => [
        'gender',
        'first_name',
        eb.fn.grouping<number>('gender').as('gender_grouping'),
      ])
      .groupBy('id')
      .groupByCube('gender', 'first_name')
      .groupByRollup(['last_name', 'age'])
      .groupByGroupingSets(['gender', 'first_name'], ['gender'], [])
      .compile()

    expect(compiled.sql).to.equal(
      'select "gender", "first_name", grouping("gender") as "gender_grouping" from "person" group by "id", cube("gender", "first_name"), rollup("last_name", "age"), grouping sets(("gender", "first_name"), ("gender"), ())',
    )
    expect(compiled.parameters).to.eql([])
  })

  it('compiles window functions, null treatment and frame extents', () => {
    const compiled = db
      .selectFrom('person')
      .select((eb) => [
        eb.fn
          .rowNumber<number>()
          .over((ob) => ob.orderBy('id'))
          .as('row_number'),
        eb.fn
          .rank<number>()
          .over((ob) => ob.partitionBy('gender').orderBy('age'))
          .as('rank'),
        eb.fn.denseRank<number>().over().as('dense_rank'),
        eb.fn.percentRank<number>().over().as('percent_rank'),
        eb.fn.cumeDist<number>().over().as('cume_dist'),
        eb.fn
          .ntile<number>(4)
          .over((ob) => ob.orderBy('age'))
          .as('ntile'),
        eb.fn
          .firstValue<string>('first_name')
          .ignoreNulls()
          .over((ob) =>
            ob
              .orderBy('id')
              .rows((fb) => fb.betweenPreceding(1).andFollowing(1)),
          )
          .as('first_name'),
        eb.fn
          .lastValue('last_name')
          .respectNulls()
          .over((ob) =>
            ob
              .orderBy('id')
              .range((fb) => fb.unboundedPreceding().excludeTies()),
          )
          .as('last_name'),
        eb.fn
          .nthValue('age', 2n)
          .over((ob) =>
            ob.groups((fb) =>
              fb.betweenCurrentRow().andUnboundedFollowing().excludeGroup(),
            ),
          )
          .as('nth_age'),
        eb.fn
          .lag('age', 1, 0)
          .over((ob) => ob.orderBy('id'))
          .as('prev_age'),
        eb.fn
          .lead('age', 1)
          .over((ob) => ob.orderBy('id').rows((fb) => fb.preceding(sql.lit(1))))
          .as('next_age'),
      ])
      .compile()

    expect(compiled.sql).to.equal(
      [
        'select row_number() over(order by "id") as "row_number"',
        'rank() over(partition by "gender" order by "age") as "rank"',
        'dense_rank() over() as "dense_rank"',
        'percent_rank() over() as "percent_rank"',
        'cume_dist() over() as "cume_dist"',
        'ntile($1) over(order by "age") as "ntile"',
        'first_value("first_name") ignore nulls over(order by "id" rows between $2 preceding and $3 following) as "first_name"',
        'last_value("last_name") respect nulls over(order by "id" range unbounded preceding exclude ties) as "last_name"',
        'nth_value("age", $4) over(groups between current row and unbounded following exclude group) as "nth_age"',
        'lag("age", $5, $6) over(order by "id") as "prev_age"',
        'lead("age", $7) over(order by "id" rows 1 preceding) as "next_age"',
      ].join(', ') + ' from "person"',
    )
    expect(compiled.parameters).to.eql([4, 1, 1, 2n, 1, 0, 1])
  })

  it('strips redundant range extents and keeps non-default ones', () => {
    const plugin = new SimplifyFramePlugin()

    const stripped = db
      .withPlugin(plugin)
      .selectFrom('person')
      .select((eb) => [
        eb.fn
          .sum<number>('age')
          .over((ob) =>
            ob
              .orderBy('id')
              .range((fb) => fb.betweenUnboundedPreceding().andCurrentRow()),
          )
          .as('ordered_default'),
        eb.fn
          .sum<number>('age')
          .over((ob) => ob.orderBy('id').range((fb) => fb.unboundedPreceding()))
          .as('ordered_shorthand'),
        eb.fn
          .sum<number>('age')
          .over((ob) =>
            ob.range((fb) =>
              fb.betweenUnboundedPreceding().andUnboundedFollowing(),
            ),
          )
          .as('unordered_default'),
      ])
      .compile()

    expect(stripped.sql).to.equal(
      'select sum("age") over(order by "id") as "ordered_default", sum("age") over(order by "id") as "ordered_shorthand", sum("age") over() as "unordered_default" from "person"',
    )

    const kept = db
      .withPlugin(plugin)
      .selectFrom('person')
      .select((eb) => [
        eb.fn
          .sum<number>('age')
          .over((ob) => ob.orderBy('id').rows((fb) => fb.unboundedPreceding()))
          .as('rows_mode'),
        eb.fn
          .sum<number>('age')
          .over((ob) =>
            ob
              .orderBy('id')
              .groups((fb) => fb.betweenUnboundedPreceding().andCurrentRow()),
          )
          .as('groups_mode'),
        eb.fn
          .sum<number>('age')
          .over((ob) =>
            ob
              .orderBy('id')
              .range((fb) =>
                fb
                  .betweenUnboundedPreceding()
                  .andCurrentRow()
                  .excludeCurrentRow(),
              ),
          )
          .as('with_exclusion'),
        eb.fn
          .sum<number>('age')
          .over((ob) =>
            ob.range((fb) => fb.betweenUnboundedPreceding().andCurrentRow()),
          )
          .as('current_row_end_without_order_by'),
        eb.fn
          .sum<number>('age')
          .over((ob) =>
            ob
              .orderBy('id')
              .range((fb) =>
                fb.betweenUnboundedPreceding().andUnboundedFollowing(),
              ),
          )
          .as('unbounded_following_with_order_by'),
        eb.fn
          .sum<number>('age')
          .over((ob) =>
            ob
              .orderBy('id')
              .range((fb) => fb.betweenPreceding(sql.lit(1)).andCurrentRow()),
          )
          .as('expression_offset'),
        eb.fn
          .sum<number>('age')
          .over((ob) =>
            ob
              .orderBy('id')
              .range((fb) => fb.betweenPreceding(2).andCurrentRow()),
          )
          .as('value_offset'),
        eb.fn
          .sum<number>('age')
          .over((ob) =>
            ob
              .orderBy('id')
              .range((fb) => fb.unboundedPreceding().excludeNoOthers()),
          )
          .as('default_bounds_with_exclusion'),
      ])
      .compile()

    expect(kept.sql).to.equal(
      [
        'select sum("age") over(order by "id" rows unbounded preceding) as "rows_mode"',
        'sum("age") over(order by "id" groups between unbounded preceding and current row) as "groups_mode"',
        'sum("age") over(order by "id" range between unbounded preceding and current row exclude current row) as "with_exclusion"',
        'sum("age") over(range between unbounded preceding and current row) as "current_row_end_without_order_by"',
        'sum("age") over(order by "id" range between unbounded preceding and unbounded following) as "unbounded_following_with_order_by"',
        'sum("age") over(order by "id" range between 1 preceding and current row) as "expression_offset"',
        'sum("age") over(order by "id" range between $1 preceding and current row) as "value_offset"',
        'sum("age") over(order by "id" range unbounded preceding exclude no others) as "default_bounds_with_exclusion"',
      ].join(', ') + ' from "person"',
    )
    expect(kept.parameters).to.eql([2])
  })
})
