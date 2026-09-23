import {
  clearDatabase,
  destroyTest,
  initTest,
  TestContext,
  testSql,
  expect,
  insertDefaultDataSet,
  DIALECTS,
} from './test-setup.js'

for (const dialect of DIALECTS) {
  describe(`${dialect}: grouping sets, cube and rollup`, () => {
    let ctx: TestContext

    before(async function () {
      ctx = await initTest(this, dialect)
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

    it('group by rollup with grouping()', async () => {
      const query = ctx.db
        .selectFrom('person')
        .select((eb) => [
          'gender',
          eb.fn.grouping<number>('gender').as('is_total'),
          eb.fn.countAll<number>().as('count'),
        ])
        .groupByRollup('gender')
        .orderBy('is_total')
        .orderBy('gender')

      testSql(query, dialect, {
        postgres: {
          sql: 'select "gender", grouping("gender") as "is_total", count(*) as "count" from "person" group by rollup("gender") order by "is_total", "gender"',
          parameters: [],
        },
        mysql: {
          sql: 'select `gender`, grouping(`gender`) as `is_total`, count(*) as `count` from `person` group by rollup(`gender`) order by `is_total`, `gender`',
          parameters: [],
        },
        mssql: {
          sql: 'select "gender", grouping("gender") as "is_total", count(*) as "count" from "person" group by rollup("gender") order by "is_total", "gender"',
          parameters: [],
        },
        sqlite: {
          sql: 'select "gender", grouping("gender") as "is_total", count(*) as "count" from "person" group by rollup("gender") order by "is_total", "gender"',
          parameters: [],
        },
      })

      if (dialect === 'postgres' || dialect === 'mssql') {
        const result = await query.execute()
        expect(
          result.map((it) => ({
            gender: it.gender,
            is_total: Number(it.is_total),
            count: Number(it.count),
          })),
        ).to.eql([
          { gender: 'female', is_total: 0, count: 1 },
          { gender: 'male', is_total: 0, count: 2 },
          { gender: null, is_total: 1, count: 3 },
        ])
      }
    })

    it('group by cube with multiple columns', () => {
      const query = ctx.db
        .selectFrom('person')
        .select(['gender', 'marital_status'])
        .groupByCube('gender', 'marital_status')

      testSql(query, dialect, {
        postgres: {
          sql: 'select "gender", "marital_status" from "person" group by cube("gender", "marital_status")',
          parameters: [],
        },
        mysql: {
          sql: 'select `gender`, `marital_status` from `person` group by cube(`gender`, `marital_status`)',
          parameters: [],
        },
        mssql: {
          sql: 'select "gender", "marital_status" from "person" group by cube("gender", "marital_status")',
          parameters: [],
        },
        sqlite: {
          sql: 'select "gender", "marital_status" from "person" group by cube("gender", "marital_status")',
          parameters: [],
        },
      })
    })

    it('group by grouping sets wraps each set in parentheses', async () => {
      const query = ctx.db
        .selectFrom('person')
        .select((eb) => [
          'gender',
          'marital_status',
          eb.fn.countAll<number>().as('count'),
        ])
        .groupByGroupingSets(
          ['gender', 'marital_status'],
          ['gender'],
          'marital_status',
          [],
        )

      testSql(query, dialect, {
        postgres: {
          sql: 'select "gender", "marital_status", count(*) as "count" from "person" group by grouping sets(("gender", "marital_status"), ("gender"), ("marital_status"), ())',
          parameters: [],
        },
        mysql: {
          sql: 'select `gender`, `marital_status`, count(*) as `count` from `person` group by grouping sets((`gender`, `marital_status`), (`gender`), (`marital_status`), ())',
          parameters: [],
        },
        mssql: {
          sql: 'select "gender", "marital_status", count(*) as "count" from "person" group by grouping sets(("gender", "marital_status"), ("gender"), ("marital_status"), ())',
          parameters: [],
        },
        sqlite: {
          sql: 'select "gender", "marital_status", count(*) as "count" from "person" group by grouping sets(("gender", "marital_status"), ("gender"), ("marital_status"), ())',
          parameters: [],
        },
      })

      if (dialect === 'postgres' || dialect === 'mssql') {
        const result = await query.execute()
        expect(result).to.have.length(3 + 2 + 2 + 1)
      }
    })

    it('grouping elements compose with groupBy', () => {
      const query = ctx.db
        .selectFrom('person')
        .select(['gender', 'marital_status', 'last_name'])
        .groupBy('last_name')
        .groupByRollup('gender', 'marital_status')
        .groupByCube('first_name')
        .groupBy(['middle_name'])
        .groupByGroupingSets(['id'])

      testSql(query, dialect, {
        postgres: {
          sql: 'select "gender", "marital_status", "last_name" from "person" group by "last_name", rollup("gender", "marital_status"), cube("first_name"), "middle_name", grouping sets(("id"))',
          parameters: [],
        },
        mysql: {
          sql: 'select `gender`, `marital_status`, `last_name` from `person` group by `last_name`, rollup(`gender`, `marital_status`), cube(`first_name`), `middle_name`, grouping sets((`id`))',
          parameters: [],
        },
        mssql: {
          sql: 'select "gender", "marital_status", "last_name" from "person" group by "last_name", rollup("gender", "marital_status"), cube("first_name"), "middle_name", grouping sets(("id"))',
          parameters: [],
        },
        sqlite: {
          sql: 'select "gender", "marital_status", "last_name" from "person" group by "last_name", rollup("gender", "marital_status"), cube("first_name"), "middle_name", grouping sets(("id"))',
          parameters: [],
        },
      })
    })
  })
}
