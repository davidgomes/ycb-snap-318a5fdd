import { sql } from '../../../'

import {
  clearDatabase,
  destroyTest,
  initTest,
  TestContext,
  testSql,
  DIALECTS,
} from './test-setup.js'

for (const dialect of DIALECTS) {
  describe(`${dialect}: grouped aggregation`, () => {
    let ctx: TestContext

    before(async function () {
      ctx = await initTest(this, dialect)
    })

    beforeEach(async () => {
      await clearDatabase(ctx)
    })

    after(async () => {
      await destroyTest(ctx)
    })

    if (dialect === 'postgres' || dialect === 'mssql') {
      it('should compile group by cube', async () => {
        const query = ctx.db
          .selectFrom('person')
          .select([
            'gender',
            'first_name',
            sql<number>`count(*)`.as('count'),
          ])
          .groupByCube('gender', 'first_name')

        testSql(query, dialect, {
          postgres: {
            sql: 'select "gender", "first_name", count(*) as "count" from "person" group by cube("gender", "first_name")',
            parameters: [],
          },
          mssql: {
            sql: 'select "gender", "first_name", count(*) as "count" from "person" group by cube("gender", "first_name")',
            parameters: [],
          },
        })
      })

      it('should compile group by rollup', async () => {
        const query = ctx.db
          .selectFrom('person')
          .select([
            'gender',
            sql<number>`count(*)`.as('count'),
          ])
          .groupByRollup('gender')

        testSql(query, dialect, {
          postgres: {
            sql: 'select "gender", count(*) as "count" from "person" group by rollup("gender")',
            parameters: [],
          },
          mssql: {
            sql: 'select "gender", count(*) as "count" from "person" group by rollup("gender")',
            parameters: [],
          },
        })
      })

      it('should compile group by grouping sets', async () => {
        const query = ctx.db
          .selectFrom('person')
          .select([
            'gender',
            'first_name',
            sql<number>`count(*)`.as('count'),
          ])
          .groupByGroupingSets(['gender', 'first_name'], ['gender'])

        testSql(query, dialect, {
          postgres: {
            sql: 'select "gender", "first_name", count(*) as "count" from "person" group by grouping sets (("gender", "first_name"), ("gender"))',
            parameters: [],
          },
          mssql: {
            sql: 'select "gender", "first_name", count(*) as "count" from "person" group by grouping sets (("gender", "first_name"), ("gender"))',
            parameters: [],
          },
        })
      })

      it('should compose grouped aggregation with group by', async () => {
        const query = ctx.db
          .selectFrom('person')
          .select([
            'gender',
            'first_name',
            sql<number>`count(*)`.as('count'),
          ])
          .groupBy('first_name')
          .groupByCube('gender')

        testSql(query, dialect, {
          postgres: {
            sql: 'select "gender", "first_name", count(*) as "count" from "person" group by "first_name", cube("gender")',
            parameters: [],
          },
          mssql: {
            sql: 'select "gender", "first_name", count(*) as "count" from "person" group by "first_name", cube("gender")',
            parameters: [],
          },
        })
      })

      it('should compile grouping function', async () => {
        const query = ctx.db
          .selectFrom('person')
          .select((eb) => [
            'gender',
            eb.fn.grouping('gender').as('grouping_gender'),
          ])
          .groupByCube('gender')

        testSql(query, dialect, {
          postgres: {
            sql: 'select "gender", grouping("gender") as "grouping_gender" from "person" group by cube("gender")',
            parameters: [],
          },
          mssql: {
            sql: 'select "gender", grouping("gender") as "grouping_gender" from "person" group by cube("gender")',
            parameters: [],
          },
        })
      })
    }
  })
}
