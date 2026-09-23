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
      it('group by cube', async () => {
        const query = ctx.db
          .selectFrom('person')
          .select((eb) => [
            'gender',
            'marital_status',
            eb.fn.countAll<number | string>().as('person_count'),
          ])
          .groupByCube('gender', 'marital_status')

        testSql(query, dialect, {
          postgres: {
            sql: [
              'select "gender", "marital_status", count(*) as "person_count"',
              'from "person"',
              'group by cube("gender", "marital_status")',
            ],
            parameters: [],
          },
          mysql: NOT_SUPPORTED,
          mssql: {
            sql: [
              'select "gender", "marital_status", count(*) as "person_count"',
              'from "person"',
              'group by cube("gender", "marital_status")',
            ],
            parameters: [],
          },
          sqlite: NOT_SUPPORTED,
        })

        const result = await query.execute()

        expect(normalizeCounts(result)).to.have.deep.members([
          { gender: 'female', marital_status: 'divorced', person_count: 1 },
          { gender: 'male', marital_status: 'divorced', person_count: 1 },
          { gender: 'male', marital_status: 'married', person_count: 1 },
          { gender: 'female', marital_status: null, person_count: 1 },
          { gender: 'male', marital_status: null, person_count: 2 },
          { gender: null, marital_status: 'divorced', person_count: 2 },
          { gender: null, marital_status: 'married', person_count: 1 },
          { gender: null, marital_status: null, person_count: 3 },
        ])
      })

      it('group by rollup', async () => {
        const query = ctx.db
          .selectFrom('person')
          .select((eb) => [
            'gender',
            'marital_status',
            eb.fn.countAll<number | string>().as('person_count'),
          ])
          .groupByRollup('gender', 'person.marital_status')

        testSql(query, dialect, {
          postgres: {
            sql: [
              'select "gender", "marital_status", count(*) as "person_count"',
              'from "person"',
              'group by rollup("gender", "person"."marital_status")',
            ],
            parameters: [],
          },
          mysql: NOT_SUPPORTED,
          mssql: {
            sql: [
              'select "gender", "marital_status", count(*) as "person_count"',
              'from "person"',
              'group by rollup("gender", "person"."marital_status")',
            ],
            parameters: [],
          },
          sqlite: NOT_SUPPORTED,
        })

        const result = await query.execute()

        expect(normalizeCounts(result)).to.have.deep.members([
          { gender: 'female', marital_status: 'divorced', person_count: 1 },
          { gender: 'male', marital_status: 'divorced', person_count: 1 },
          { gender: 'male', marital_status: 'married', person_count: 1 },
          { gender: 'female', marital_status: null, person_count: 1 },
          { gender: 'male', marital_status: null, person_count: 2 },
          { gender: null, marital_status: null, person_count: 3 },
        ])
      })

      it('group by grouping sets', async () => {
        const query = ctx.db
          .selectFrom('person')
          .select((eb) => [
            'gender',
            'marital_status',
            eb.fn.countAll<number | string>().as('person_count'),
          ])
          .groupByGroupingSets(['gender', 'marital_status'], 'gender', [])

        testSql(query, dialect, {
          postgres: {
            sql: [
              'select "gender", "marital_status", count(*) as "person_count"',
              'from "person"',
              'group by grouping sets(("gender", "marital_status"), ("gender"), ())',
            ],
            parameters: [],
          },
          mysql: NOT_SUPPORTED,
          mssql: {
            sql: [
              'select "gender", "marital_status", count(*) as "person_count"',
              'from "person"',
              'group by grouping sets(("gender", "marital_status"), ("gender"), ())',
            ],
            parameters: [],
          },
          sqlite: NOT_SUPPORTED,
        })

        const result = await query.execute()

        expect(normalizeCounts(result)).to.have.deep.members([
          { gender: 'female', marital_status: 'divorced', person_count: 1 },
          { gender: 'male', marital_status: 'divorced', person_count: 1 },
          { gender: 'male', marital_status: 'married', person_count: 1 },
          { gender: 'female', marital_status: null, person_count: 1 },
          { gender: 'male', marital_status: null, person_count: 2 },
          { gender: null, marital_status: null, person_count: 3 },
        ])
      })

      it('group by grouping elements combined with regular group by items', async () => {
        const query = ctx.db
          .selectFrom('person')
          .select((eb) => [
            'gender',
            'marital_status',
            eb.fn.countAll<number | string>().as('person_count'),
          ])
          .groupBy('gender')
          .groupByRollup('marital_status')
          .groupByCube(sql`upper(${sql.ref('last_name')})`)
          .groupByGroupingSets(['first_name'], [])
          .groupBy('children')

        testSql(query, dialect, {
          postgres: {
            sql: [
              'select "gender", "marital_status", count(*) as "person_count"',
              'from "person"',
              'group by "gender", rollup("marital_status"), cube(upper("last_name")),',
              'grouping sets(("first_name"), ()), "children"',
            ],
            parameters: [],
          },
          mysql: NOT_SUPPORTED,
          mssql: {
            sql: [
              'select "gender", "marital_status", count(*) as "person_count"',
              'from "person"',
              'group by "gender", rollup("marital_status"), cube(upper("last_name")),',
              'grouping sets(("first_name"), ()), "children"',
            ],
            parameters: [],
          },
          sqlite: NOT_SUPPORTED,
        })

        await query.execute()
      })

      it('group by rollup with grouping() to detect super-aggregate rows', async () => {
        const query = ctx.db
          .selectFrom('person')
          .select((eb) => [
            'gender',
            eb.fn.grouping<number>('gender').as('is_total'),
            eb.fn.countAll<number | string>().as('person_count'),
          ])
          .groupByRollup('gender')

        testSql(query, dialect, {
          postgres: {
            sql: [
              'select "gender", grouping("gender") as "is_total", count(*) as "person_count"',
              'from "person"',
              'group by rollup("gender")',
            ],
            parameters: [],
          },
          mysql: NOT_SUPPORTED,
          mssql: {
            sql: [
              'select "gender", grouping("gender") as "is_total", count(*) as "person_count"',
              'from "person"',
              'group by rollup("gender")',
            ],
            parameters: [],
          },
          sqlite: NOT_SUPPORTED,
        })

        const result = await query.execute()

        expect(normalizeCounts(result)).to.have.deep.members([
          { gender: 'female', is_total: 0, person_count: 1 },
          { gender: 'male', is_total: 0, person_count: 2 },
          { gender: null, is_total: 1, person_count: 3 },
        ])
      })
    }

    it('clearGroupBy removes grouping elements', async () => {
      const query = ctx.db
        .selectFrom('person')
        .select('gender')
        .groupByCube('gender')
        .groupByRollup('gender')
        .groupByGroupingSets(['gender'])
        .clearGroupBy()
        .groupBy('gender')

      testSql(query, dialect, {
        postgres: {
          sql: 'select "gender" from "person" group by "gender"',
          parameters: [],
        },
        mysql: {
          sql: 'select `gender` from `person` group by `gender`',
          parameters: [],
        },
        mssql: {
          sql: 'select "gender" from "person" group by "gender"',
          parameters: [],
        },
        sqlite: {
          sql: 'select "gender" from "person" group by "gender"',
          parameters: [],
        },
      })

      await query.execute()
    })
  })
}

function normalizeCounts<T extends { person_count: number | string }>(
  rows: T[],
): (Omit<T, 'person_count'> & { person_count: number })[] {
  return rows.map((row) => ({
    ...row,
    person_count: Number(row.person_count),
  }))
}
