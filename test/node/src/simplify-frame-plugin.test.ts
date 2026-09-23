import { type OverBuilderCallback, SimplifyFramePlugin, sql } from '../../../'

import {
  clearDatabase,
  Database,
  destroyTest,
  DIALECTS,
  expect,
  initTest,
  insertDefaultDataSet,
  PerDialect,
  TestContext,
  testSql,
} from './test-setup.js'

interface Fixture {
  description: string
  over: OverBuilderCallback<Database, 'person'>
  expectedOver: string
  parameters?: unknown[]
}

const STRIPPED: Fixture[] = [
  {
    description:
      'range between unbounded preceding and current row with order by',
    over: (ob) =>
      ob
        .orderBy('first_name')
        .range((f) => f.betweenUnboundedPreceding().andCurrentRow()),
    expectedOver: 'over(order by "first_name")',
  },
  {
    description: 'range unbounded preceding with order by',
    over: (ob) => ob.orderBy('first_name').range((f) => f.unboundedPreceding()),
    expectedOver: 'over(order by "first_name")',
  },
  {
    description:
      'range between unbounded preceding and current row with partition by and order by',
    over: (ob) =>
      ob
        .partitionBy('gender')
        .orderBy('first_name')
        .range((f) => f.betweenUnboundedPreceding().andCurrentRow()),
    expectedOver: 'over(partition by "gender" order by "first_name")',
  },
  {
    description:
      'range between unbounded preceding and unbounded following without order by',
    over: (ob) =>
      ob.range((f) => f.betweenUnboundedPreceding().andUnboundedFollowing()),
    expectedOver: 'over()',
  },
  {
    description:
      'range between unbounded preceding and unbounded following with partition by',
    over: (ob) =>
      ob
        .partitionBy('gender')
        .range((f) => f.betweenUnboundedPreceding().andUnboundedFollowing()),
    expectedOver: 'over(partition by "gender")',
  },
]

const PRESERVED: Fixture[] = [
  {
    description: 'rows mode',
    over: (ob) =>
      ob
        .orderBy('first_name')
        .rows((f) => f.betweenUnboundedPreceding().andCurrentRow()),
    expectedOver:
      'over(order by "first_name" rows between unbounded preceding and current row)',
  },
  {
    description: 'groups mode',
    over: (ob) =>
      ob
        .orderBy('first_name')
        .groups((f) => f.betweenUnboundedPreceding().andCurrentRow()),
    expectedOver:
      'over(order by "first_name" groups between unbounded preceding and current row)',
  },
  {
    description: 'an exclusion',
    over: (ob) =>
      ob
        .orderBy('first_name')
        .range((f) =>
          f.betweenUnboundedPreceding().andCurrentRow().excludeNoOthers(),
        ),
    expectedOver:
      'over(order by "first_name" range between unbounded preceding and current row exclude no others)',
  },
  {
    description: 'the no order by default when there is an order by',
    over: (ob) =>
      ob
        .orderBy('first_name')
        .range((f) => f.betweenUnboundedPreceding().andUnboundedFollowing()),
    expectedOver:
      'over(order by "first_name" range between unbounded preceding and unbounded following)',
  },
  {
    description: 'the order by default when there is no order by',
    over: (ob) =>
      ob.range((f) => f.betweenUnboundedPreceding().andCurrentRow()),
    expectedOver: 'over(range between unbounded preceding and current row)',
  },
  {
    description: 'the single bound order by default when there is no order by',
    over: (ob) => ob.partitionBy('gender').range((f) => f.unboundedPreceding()),
    expectedOver: 'over(partition by "gender" range unbounded preceding)',
  },
  {
    description: 'a non-default start bound',
    over: (ob) =>
      ob
        .orderBy('first_name')
        .range((f) => f.betweenCurrentRow().andCurrentRow()),
    expectedOver:
      'over(order by "first_name" range between current row and current row)',
  },
  {
    description: 'a numeric offset',
    over: (ob) =>
      ob
        .orderBy('children')
        .range((f) => f.betweenPreceding(1).andCurrentRow()),
    expectedOver:
      'over(order by "children" range between $1 preceding and current row)',
    parameters: [1],
  },
  {
    description: 'an expression-based offset',
    over: (ob) =>
      ob
        .orderBy('children')
        .range((f) => f.betweenUnboundedPreceding().andFollowing(sql.lit(0))),
    expectedOver:
      'over(order by "children" range between unbounded preceding and 0 following)',
  },
]

for (const dialect of DIALECTS) {
  describe(`${dialect}: simplify frame plugin`, () => {
    let ctx: TestContext

    before(async function () {
      ctx = await initTest(this, dialect, {
        plugins: [new SimplifyFramePlugin()],
      })
    })

    beforeEach(async () => {
      await insertDefaultDataSet(ctx)
    })

    afterEach(async () => {
      await clearDatabase(ctx)
    })

    after(async () => {
      await destroyTest(ctx)
    })

    for (const fixture of STRIPPED) {
      it(`should strip ${fixture.description}`, () => {
        const query = ctx.db
          .selectFrom('person')
          .select((eb) => eb.fn.countAll().over(fixture.over).as('c'))

        testSql(
          query,
          dialect,
          perDialect(
            [`select count(*) ${fixture.expectedOver} as "c" from "person"`],
            [],
          ),
        )
      })
    }

    for (const fixture of PRESERVED) {
      it(`should preserve a frame with ${fixture.description}`, () => {
        const query = ctx.db
          .selectFrom('person')
          .select((eb) => eb.fn.countAll().over(fixture.over).as('c'))

        testSql(
          query,
          dialect,
          perDialect(
            [`select count(*) ${fixture.expectedOver} as "c" from "person"`],
            fixture.parameters ?? [],
          ),
        )
      })
    }

    it('should strip frames in nested queries and leave other windows alone', () => {
      const query = ctx.db
        .selectFrom((qb) =>
          qb
            .selectFrom('person')
            .select((eb) => [
              'first_name',
              eb.fn
                .countAll()
                .over((ob) =>
                  ob
                    .orderBy('first_name')
                    .range((f) =>
                      f.betweenUnboundedPreceding().andCurrentRow(),
                    ),
                )
                .as('running_count'),
            ])
            .as('p'),
        )
        .select((eb) => [
          'p.first_name',
          eb.fn
            .max('p.running_count')
            .over((ob) => ob.orderBy('p.first_name'))
            .as('max_running_count'),
        ])

      testSql(
        query,
        dialect,
        perDialect(
          [
            'select "p"."first_name", max("p"."running_count") over(order by "p"."first_name") as "max_running_count"',
            'from (select "first_name", count(*) over(order by "first_name") as "running_count" from "person") as "p"',
          ],
          [],
        ),
      )
    })

    it('should not change query results', async () => {
      const withFrame = ctx.db
        .selectFrom('person')
        .select((eb) => [
          'first_name',
          eb.fn
            .count<number | string>('id')
            .over((ob) =>
              ob
                .orderBy('gender')
                .range((f) => f.betweenUnboundedPreceding().andCurrentRow()),
            )
            .as('running_count'),
        ])
        .orderBy('first_name')

      testSql(
        withFrame,
        dialect,
        perDialect(
          [
            'select "first_name", count("id") over(order by "gender") as "running_count"',
            'from "person"',
            'order by "first_name"',
          ],
          [],
        ),
      )

      const withoutPlugin = ctx.db.withoutPlugins()

      const expected = await withoutPlugin
        .selectFrom('person')
        .select((eb) => [
          'first_name',
          eb.fn
            .count<number | string>('id')
            .over((ob) =>
              ob
                .orderBy('gender')
                .range((f) => f.betweenUnboundedPreceding().andCurrentRow()),
            )
            .as('running_count'),
        ])
        .orderBy('first_name')
        .execute()

      const result = await withFrame.execute()

      expect(result).to.eql(expected)
      expect(result.map((row) => Number(row.running_count))).to.eql([3, 1, 3])
    })
  })
}

/**
 * Builds the expected SQL for all dialects from PostgreSQL flavored SQL.
 */
function perDialect(
  postgresSql: string[],
  parameters: unknown[],
): PerDialect<{ sql: string[]; parameters: unknown[] }> {
  const convert = (
    identifierWrapper: string,
    placeholder: (index: string) => string,
  ) =>
    postgresSql.map((line) =>
      line
        .replace(/"/g, identifierWrapper)
        .replace(/\$(\d+)/g, (_, index) => placeholder(index)),
    )

  return {
    postgres: { sql: postgresSql, parameters },
    mysql: { sql: convert('`', () => '?'), parameters },
    mssql: { sql: convert('"', (index) => `@${index}`), parameters },
    sqlite: { sql: convert('"', () => '?'), parameters },
  }
}
