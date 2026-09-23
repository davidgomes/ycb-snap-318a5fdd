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

function createDb(plugins: SimplifyFramePlugin[] = []) {
  return new Kysely<any>({
    dialect: {
      createAdapter: () => new PostgresAdapter(),
      createDriver: () => new DummyDriver(),
      createQueryCompiler: () => new PostgresQueryCompiler(),
      createIntrospector: (db) => new PostgresIntrospector(db),
    },
    plugins,
  })
}

describe('grouped aggregation and window frames', () => {
  it('compiles cube, rollup, grouping sets and grouping()', () => {
    const compiled = createDb()
      .selectFrom('person')
      .select((eb) => eb.fn.grouping<number>('gender').as('gender_grouped'))
      .groupBy('gender')
      .groupByCube('first_name', 'last_name')
      .groupByRollup('age')
      .groupByGroupingSets(['first_name', 'last_name'], ['first_name'], [])
      .compile()

    expect(compiled.sql).to.equal(
      'select grouping("gender") as "gender_grouped" from "person" group by "gender", cube("first_name", "last_name"), rollup("age"), grouping sets(("first_name", "last_name"), ("first_name"), ())',
    )
    expect(compiled.parameters).to.eql([])
  })

  it('compiles window functions, null treatment and frame extents', () => {
    const compiled = createDb()
      .selectFrom('person')
      .select((eb) => [
        eb.fn
          .lag<string>('first_name', 1, 0)
          .ignoreNulls()
          .over((ob) => ob.orderBy('id'))
          .as('prev'),
        eb.fn
          .firstValue<string>('first_name')
          .respectNulls()
          .over((ob) => ob.orderBy('id').range((fb) => fb.preceding(sql`1`)))
          .as('fv'),
        eb.fn
          .sum<number>('age')
          .over((ob) =>
            ob
              .orderBy('id')
              .rows((fb) =>
                fb.betweenUnboundedPreceding().andCurrentRow().excludeTies(),
              ),
          )
          .as('running'),
        eb.fn
          .ntile<number>(4)
          .over((ob) =>
            ob
              .orderBy('id')
              .groups((fb) => fb.betweenPreceding(1).andFollowing(2)),
          )
          .as('bucket'),
      ])
      .compile()

    expect(compiled.sql).to.equal(
      'select lag("first_name", $1, $2) ignore nulls over(order by "id") as "prev", first_value("first_name") respect nulls over(order by "id" range 1 preceding) as "fv", sum("age") over(order by "id" rows between unbounded preceding and current row exclude ties) as "running", ntile($3) over(order by "id" groups between $4 preceding and $5 following) as "bucket" from "person"',
    )
    expect(compiled.parameters).to.eql([1, 0, 4, 1, 2])
  })

  it('strips only implicit range defaults', () => {
    const db = createDb([new SimplifyFramePlugin()])

    const orderedBetween = db
      .selectFrom('person')
      .select((eb) =>
        eb.fn
          .sum<number>('age')
          .over((ob) =>
            ob
              .orderBy('id')
              .range((fb) => fb.betweenUnboundedPreceding().andCurrentRow()),
          )
          .as('s'),
      )
      .compile()

    const orderedShorthand = db
      .selectFrom('person')
      .select((eb) =>
        eb.fn
          .sum<number>('age')
          .over((ob) => ob.orderBy('id').range((fb) => fb.unboundedPreceding()))
          .as('s'),
      )
      .compile()

    const unordered = db
      .selectFrom('person')
      .select((eb) =>
        eb.fn
          .sum<number>('age')
          .over((ob) =>
            ob.range((fb) =>
              fb.betweenUnboundedPreceding().andUnboundedFollowing(),
            ),
          )
          .as('s'),
      )
      .compile()

    const keptRows = db
      .selectFrom('person')
      .select((eb) =>
        eb.fn
          .sum<number>('age')
          .over((ob) =>
            ob
              .orderBy('id')
              .rows((fb) => fb.betweenUnboundedPreceding().andCurrentRow()),
          )
          .as('s'),
      )
      .compile()

    const keptExclusion = db
      .selectFrom('person')
      .select((eb) =>
        eb.fn
          .sum<number>('age')
          .over((ob) =>
            ob
              .orderBy('id')
              .range((fb) =>
                fb
                  .betweenUnboundedPreceding()
                  .andCurrentRow()
                  .excludeNoOthers(),
              ),
          )
          .as('s'),
      )
      .compile()

    const keptExpressionOffset = db
      .selectFrom('person')
      .select((eb) =>
        eb.fn
          .sum<number>('age')
          .over((ob) => ob.orderBy('id').range((fb) => fb.preceding(sql`1`)))
          .as('s'),
      )
      .compile()

    const keptUnorderedShorthand = db
      .selectFrom('person')
      .select((eb) =>
        eb.fn
          .sum<number>('age')
          .over((ob) => ob.range((fb) => fb.unboundedPreceding()))
          .as('s'),
      )
      .compile()

    expect(orderedBetween.sql).to.equal(
      'select sum("age") over(order by "id") as "s" from "person"',
    )
    expect(orderedShorthand.sql).to.equal(
      'select sum("age") over(order by "id") as "s" from "person"',
    )
    expect(unordered.sql).to.equal(
      'select sum("age") over() as "s" from "person"',
    )
    expect(keptRows.sql).to.equal(
      'select sum("age") over(order by "id" rows between unbounded preceding and current row) as "s" from "person"',
    )
    expect(keptExclusion.sql).to.equal(
      'select sum("age") over(order by "id" range between unbounded preceding and current row exclude no others) as "s" from "person"',
    )
    expect(keptExpressionOffset.sql).to.equal(
      'select sum("age") over(order by "id" range 1 preceding) as "s" from "person"',
    )
    expect(keptUnorderedShorthand.sql).to.equal(
      'select sum("age") over(range unbounded preceding) as "s" from "person"',
    )
  })
})
