import { sql } from '../../../'
import { createDummyDb, sqlExpectation } from './dummy-db.js'
import { DIALECTS, testSql } from './test-setup.js'

for (const dialect of DIALECTS) {
  describe(`${dialect}: grouped aggregation`, () => {
    const db = createDummyDb(dialect)

    it('should compile cube, rollup and grouping sets as flat or parenthesized lists', () => {
      const query = db
        .selectFrom('person')
        .select(['gender', 'first_name'])
        .groupByCube('gender', 'first_name')
        .groupByRollup('last_name')
        .groupByGroupingSets(['first_name', 'last_name'], 'gender', [])

      testSql(
        query,
        dialect,
        sqlExpectation(
          'select "gender", "first_name" from "person" group by cube("gender", "first_name"), rollup("last_name"), grouping sets(("first_name", "last_name"), ("gender"), ())',
          [],
        ),
      )
    })

    it('should compose with groupBy in either order', () => {
      const query = db
        .selectFrom('person')
        .select('gender')
        .groupBy('id')
        .groupByCube('gender')
        .groupBy('first_name')
        .groupByRollup('last_name', 'gender')
        .groupByGroupingSets(['gender'], 'first_name')
        .groupBy('last_name')

      testSql(
        query,
        dialect,
        sqlExpectation(
          'select "gender" from "person" group by "id", cube("gender"), "first_name", rollup("last_name", "gender"), grouping sets(("gender"), ("first_name")), "last_name"',
          [],
        ),
      )
    })

    it('should accept expressions inside cube and grouping sets', () => {
      const query = db
        .selectFrom('person')
        .select('gender')
        .groupByCube('person.gender', sql`lower(first_name)`)
        .groupByGroupingSets([sql`lower(last_name)`, 'first_name'])

      testSql(
        query,
        dialect,
        sqlExpectation(
          'select "gender" from "person" group by cube("person"."gender", lower(first_name)), grouping sets((lower(last_name), "first_name"))',
          [],
        ),
      )
    })

    it('should compile grouping() for super-aggregate detection', () => {
      const query = db
        .selectFrom('person')
        .select((eb) => [
          'gender',
          eb.fn.grouping<number>('gender').as('gender_grouping'),
          eb.fn.grouping<number>('person.first_name').as('name_grouping'),
        ])
        .groupByRollup('gender', 'first_name')
        .having((eb) => eb.fn.grouping('gender'), '=', 0)

      testSql(
        query,
        dialect,
        sqlExpectation(
          'select "gender", grouping("gender") as "gender_grouping", grouping("person"."first_name") as "name_grouping" from "person" group by rollup("gender", "first_name") having grouping("gender") = $1',
          [0],
        ),
      )
    })
  })
}
