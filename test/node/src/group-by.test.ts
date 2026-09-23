import { sql } from '../../../'

import {
  clearDatabase,
  destroyTest,
  initTest,
  TestContext,
  testSql,
  expect,
  insertDefaultDataSet,
  NOT_SUPPORTED,
  DIALECTS,
} from './test-setup.js'

for (const dialect of DIALECTS) {
  describe(`${dialect}: group by`, () => {
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

    it('group by one column', async () => {
      const query = ctx.db
        .selectFrom('person')
        .select(['gender', sql`max(first_name)`.as('max_first_name')])
        .groupBy('gender')
        .orderBy('gender')

      testSql(query, dialect, {
        postgres: {
          sql: 'select "gender", max(first_name) as "max_first_name" from "person" group by "gender" order by "gender"',
          parameters: [],
        },
        mysql: {
          sql: 'select `gender`, max(first_name) as `max_first_name` from `person` group by `gender` order by `gender`',
          parameters: [],
        },
        mssql: {
          sql: 'select "gender", max(first_name) as "max_first_name" from "person" group by "gender" order by "gender"',
          parameters: [],
        },
        sqlite: {
          sql: 'select "gender", max(first_name) as "max_first_name" from "person" group by "gender" order by "gender"',
          parameters: [],
        },
      })

      const persons = await query.execute()

      expect(persons).to.have.length(2)
      expect(persons).to.containSubset([
        {
          max_first_name: 'Jennifer',
          gender: 'female',
        },
        {
          max_first_name: 'Sylvester',
          gender: 'male',
        },
      ])
    })

    if (dialect === 'postgres' || dialect === 'mysql' || dialect === 'sqlite') {
      it('group by selection', async () => {
        const query = ctx.db
          .selectFrom('person')
          .select(['gender as g', sql`max(first_name)`.as('max_first_name')])
          .groupBy('g')
          .orderBy('g')

        testSql(query, dialect, {
          postgres: {
            sql: 'select "gender" as "g", max(first_name) as "max_first_name" from "person" group by "g" order by "g"',
            parameters: [],
          },
          mysql: {
            sql: 'select `gender` as `g`, max(first_name) as `max_first_name` from `person` group by `g` order by `g`',
            parameters: [],
          },
          mssql: NOT_SUPPORTED,
          sqlite: {
            sql: 'select "gender" as "g", max(first_name) as "max_first_name" from "person" group by "g" order by "g"',
            parameters: [],
          },
        })

        const persons = await query.execute()

        expect(persons).to.have.length(2)
        expect(persons).to.containSubset([
          {
            max_first_name: 'Jennifer',
            g: 'female',
          },
          {
            max_first_name: 'Sylvester',
            g: 'male',
          },
        ])
      })
    }

    it('group by two columns', async () => {
      const query = ctx.db
        .selectFrom('person')
        .select(['gender', sql`max(first_name)`.as('max_first_name')])
        .groupBy(['gender', 'id'])
        .orderBy('gender')

      testSql(query, dialect, {
        postgres: {
          sql: 'select "gender", max(first_name) as "max_first_name" from "person" group by "gender", "id" order by "gender"',
          parameters: [],
        },
        mysql: {
          sql: 'select `gender`, max(first_name) as `max_first_name` from `person` group by `gender`, `id` order by `gender`',
          parameters: [],
        },
        mssql: {
          sql: 'select "gender", max(first_name) as "max_first_name" from "person" group by "gender", "id" order by "gender"',
          parameters: [],
        },
        sqlite: {
          sql: 'select "gender", max(first_name) as "max_first_name" from "person" group by "gender", "id" order by "gender"',
          parameters: [],
        },
      })

      await query.execute()
    })

    it('group by a reference', async () => {
      const query = ctx.db
        .selectFrom('person')
        .select(['gender', sql`max(first_name)`.as('max_first_name')])
        .groupBy('person.gender')
        .orderBy('gender', 'asc')

      testSql(query, dialect, {
        postgres: {
          sql: 'select "gender", max(first_name) as "max_first_name" from "person" group by "person"."gender" order by "gender" asc',
          parameters: [],
        },
        mysql: {
          sql: 'select `gender`, max(first_name) as `max_first_name` from `person` group by `person`.`gender` order by `gender` asc',
          parameters: [],
        },
        mssql: {
          sql: 'select "gender", max(first_name) as "max_first_name" from "person" group by "person"."gender" order by "gender" asc',
          parameters: [],
        },
        sqlite: {
          sql: 'select "gender", max(first_name) as "max_first_name" from "person" group by "person"."gender" order by "gender" asc',
          parameters: [],
        },
      })

      await query.execute()
    })

    it('group by a raw expression', async () => {
      const query = ctx.db
        .selectFrom('person')
        .select(['gender', sql`max(first_name)`.as('max_first_name')])
        .groupBy(sql`person.gender`)
        .orderBy('gender', 'asc')

      testSql(query, dialect, {
        postgres: {
          sql: 'select "gender", max(first_name) as "max_first_name" from "person" group by person.gender order by "gender" asc',
          parameters: [],
        },
        mysql: {
          sql: 'select `gender`, max(first_name) as `max_first_name` from `person` group by person.gender order by `gender` asc',
          parameters: [],
        },
        mssql: {
          sql: 'select "gender", max(first_name) as "max_first_name" from "person" group by person.gender order by "gender" asc',
          parameters: [],
        },
        sqlite: {
          sql: 'select "gender", max(first_name) as "max_first_name" from "person" group by person.gender order by "gender" asc',
          parameters: [],
        },
      })

      await query.execute()
    })

    if (dialect === 'postgres' || dialect === 'mysql' || dialect === 'sqlite') {
      it('group by a sub query', async () => {
        const query = ctx.db
          .selectFrom('person')
          .select(sql`max(first_name)`.as('max_first_name'))
          .groupBy((qb) =>
            qb
              .selectFrom('pet')
              .whereRef('person.id', '=', 'pet.owner_id')
              .select('pet.name'),
          )
          .orderBy('max_first_name')

        testSql(query, dialect, {
          postgres: {
            sql: [
              'select max(first_name) as "max_first_name"',
              'from "person"',
              'group by (select "pet"."name" from "pet" where "person"."id" = "pet"."owner_id")',
              'order by "max_first_name"',
            ],
            parameters: [],
          },
          mysql: {
            sql: [
              'select max(first_name) as `max_first_name`',
              'from `person`',
              'group by (select `pet`.`name` from `pet` where `person`.`id` = `pet`.`owner_id`)',
              'order by `max_first_name`',
            ],
            parameters: [],
          },
          mssql: {
            sql: [
              'select max(first_name) as "max_first_name"',
              'from "person"',
              'group by (select "pet"."name" from "pet" where "person"."id" = "pet"."owner_id")',
              'order by "max_first_name"',
            ],
            parameters: [],
          },
          sqlite: {
            sql: [
              'select max(first_name) as "max_first_name"',
              'from "person"',
              'group by (select "pet"."name" from "pet" where "person"."id" = "pet"."owner_id")',
              'order by "max_first_name"',
            ],
            parameters: [],
          },
        })

        await query.execute()
      })
    }

    it('conditional group by', async () => {
      const filterByPetCount = true
      const { count } = ctx.db.fn

      // Insert another pet for Arnold.
      await ctx.db
        .insertInto('pet')
        .values({
          name: 'Doggo 2',
          species: 'dog',
          owner_id: ctx.db
            .selectFrom('person')
            .select('id')
            .where('first_name', '=', 'Arnold'),
        })
        .execute()

      const result = await ctx.db
        .selectFrom('person')
        .select('person.first_name')
        .$if(filterByPetCount, (qb) =>
          qb
            .innerJoin('pet', 'pet.owner_id', 'person.id')
            .having(count('pet.id'), '>', 1)
            .groupBy('person.first_name'),
        )
        .execute()

      expect(result).to.eql([{ first_name: 'Arnold' }])
    })

    if (dialect === 'postgres' || dialect === 'mssql') {
      it('group by column and rollup with grouping function', async () => {
        const query = ctx.db
          .selectFrom('person')
          .select((eb) => [
            'gender',
            'first_name',
            eb.fn.grouping('first_name').as('first_name_grouping'),
            eb.fn.countAll<number>().as('count'),
          ])
          .groupBy('gender')
          .groupByRollup('first_name')

        testSql(query, dialect, {
          postgres: {
            sql: 'select "gender", "first_name", grouping("first_name") as "first_name_grouping", count(*) as "count" from "person" group by "gender", rollup("first_name")',
            parameters: [],
          },
          mysql: NOT_SUPPORTED,
          mssql: {
            sql: 'select "gender", "first_name", grouping("first_name") as "first_name_grouping", count(*) as "count" from "person" group by "gender", rollup("first_name")',
            parameters: [],
          },
          sqlite: NOT_SUPPORTED,
        })

        const result = await query.execute()

        expect(
          result.map((row) => ({ ...row, count: Number(row.count) })),
        ).to.have.deep.members([
          {
            gender: 'female',
            first_name: 'Jennifer',
            first_name_grouping: 0,
            count: 1,
          },
          {
            gender: 'female',
            first_name: null,
            first_name_grouping: 1,
            count: 1,
          },
          {
            gender: 'male',
            first_name: 'Arnold',
            first_name_grouping: 0,
            count: 1,
          },
          {
            gender: 'male',
            first_name: 'Sylvester',
            first_name_grouping: 0,
            count: 1,
          },
          {
            gender: 'male',
            first_name: null,
            first_name_grouping: 1,
            count: 2,
          },
        ])
      })

      it('group by cube', async () => {
        const query = ctx.db
          .selectFrom('person')
          .select((eb) => [
            'gender',
            'first_name',
            eb.fn.countAll<number>().as('count'),
          ])
          .groupByCube('gender', 'first_name')

        testSql(query, dialect, {
          postgres: {
            sql: 'select "gender", "first_name", count(*) as "count" from "person" group by cube("gender", "first_name")',
            parameters: [],
          },
          mysql: NOT_SUPPORTED,
          mssql: {
            sql: 'select "gender", "first_name", count(*) as "count" from "person" group by cube("gender", "first_name")',
            parameters: [],
          },
          sqlite: NOT_SUPPORTED,
        })

        const result = await query.execute()

        // (gender, first_name): 3, (gender): 2, (first_name): 3, (): 1
        expect(result).to.have.length(9)
        expect(
          result.map((row) => ({ ...row, count: Number(row.count) })),
        ).to.deep.include({ gender: null, first_name: null, count: 3 })
      })

      it('group by grouping sets', async () => {
        const query = ctx.db
          .selectFrom('person')
          .select((eb) => [
            'gender',
            'first_name',
            'last_name',
            eb.fn.countAll<number>().as('count'),
          ])
          .groupByGroupingSets(['gender'], ['first_name', 'last_name'], [])

        testSql(query, dialect, {
          postgres: {
            sql: 'select "gender", "first_name", "last_name", count(*) as "count" from "person" group by grouping sets(("gender"), ("first_name", "last_name"), ())',
            parameters: [],
          },
          mysql: NOT_SUPPORTED,
          mssql: {
            sql: 'select "gender", "first_name", "last_name", count(*) as "count" from "person" group by grouping sets(("gender"), ("first_name", "last_name"), ())',
            parameters: [],
          },
          sqlite: NOT_SUPPORTED,
        })

        const result = await query.execute()

        // (gender): 2, (first_name, last_name): 3, (): 1
        expect(result).to.have.length(6)
        expect(
          result.map((row) => ({ ...row, count: Number(row.count) })),
        ).to.deep.include({
          gender: 'male',
          first_name: null,
          last_name: null,
          count: 2,
        })
      })
    }

    it('should compose grouping elements with group by items', () => {
      const query = ctx.db
        .selectFrom('person')
        .select('gender')
        .groupByCube('first_name', 'last_name')
        .groupBy('gender')
        .groupByRollup('marital_status', 'children')
        .groupByGroupingSets(['id'], ['first_name', 'id'])

      testSql(query, dialect, {
        postgres: {
          sql: 'select "gender" from "person" group by cube("first_name", "last_name"), "gender", rollup("marital_status", "children"), grouping sets(("id"), ("first_name", "id"))',
          parameters: [],
        },
        mysql: {
          sql: 'select `gender` from `person` group by cube(`first_name`, `last_name`), `gender`, rollup(`marital_status`, `children`), grouping sets((`id`), (`first_name`, `id`))',
          parameters: [],
        },
        mssql: {
          sql: 'select "gender" from "person" group by cube("first_name", "last_name"), "gender", rollup("marital_status", "children"), grouping sets(("id"), ("first_name", "id"))',
          parameters: [],
        },
        sqlite: {
          sql: 'select "gender" from "person" group by cube("first_name", "last_name"), "gender", rollup("marital_status", "children"), grouping sets(("id"), ("first_name", "id"))',
          parameters: [],
        },
      })
    })
  })
}
