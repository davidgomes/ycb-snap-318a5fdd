import {
  DummyDriver,
  Kysely,
  type KyselyPlugin,
  PostgresAdapter,
  PostgresIntrospector,
  PostgresQueryCompiler,
  SimplifyFramePlugin,
  sql,
} from '../../../'
import { Database, NOT_SUPPORTED, testSql } from './test-setup.js'

function createDb(plugins: KyselyPlugin[] = []) {
  return new Kysely<Database>({
    dialect: {
      createAdapter: () => new PostgresAdapter(),
      createDriver: () => new DummyDriver(),
      createIntrospector: (db) => new PostgresIntrospector(db),
      createQueryCompiler: () => new PostgresQueryCompiler(),
    },
    plugins,
  })
}

describe('grouped aggregation', () => {
  const db = createDb()

  it('should compile groupByCube as a flat cube list', () => {
    const query = db
      .selectFrom('person')
      .select(['gender', 'last_name'])
      .groupByCube('gender', 'last_name')

    testSql(query, 'postgres', {
      postgres: {
        sql: 'select "gender", "last_name" from "person" group by cube("gender", "last_name")',
        parameters: [],
      },
      mysql: NOT_SUPPORTED,
      mssql: NOT_SUPPORTED,
      sqlite: NOT_SUPPORTED,
    })
  })

  it('should compile groupByRollup as a flat rollup list', () => {
    const query = db
      .selectFrom('person')
      .select(['gender', 'last_name'])
      .groupByRollup('gender', 'last_name')

    testSql(query, 'postgres', {
      postgres: {
        sql: 'select "gender", "last_name" from "person" group by rollup("gender", "last_name")',
        parameters: [],
      },
      mysql: NOT_SUPPORTED,
      mssql: NOT_SUPPORTED,
      sqlite: NOT_SUPPORTED,
    })
  })

  it('should wrap each grouping sets entry in its own parentheses', () => {
    const query = db
      .selectFrom('person')
      .select(['gender', 'last_name'])
      .groupByGroupingSets(['gender', 'last_name'], 'gender', [])

    testSql(query, 'postgres', {
      postgres: {
        sql: 'select "gender", "last_name" from "person" group by grouping sets(("gender", "last_name"), ("gender"), ())',
        parameters: [],
      },
      mysql: NOT_SUPPORTED,
      mssql: NOT_SUPPORTED,
      sqlite: NOT_SUPPORTED,
    })
  })

  it('should compose grouped aggregation with existing groupBy calls', () => {
    const query = db
      .selectFrom('person')
      .select(['gender', 'last_name', 'first_name'])
      .groupBy('first_name')
      .groupByCube('gender')
      .groupByRollup('last_name')

    testSql(query, 'postgres', {
      postgres: {
        sql: 'select "gender", "last_name", "first_name" from "person" group by "first_name", cube("gender"), rollup("last_name")',
        parameters: [],
      },
      mysql: NOT_SUPPORTED,
      mssql: NOT_SUPPORTED,
      sqlite: NOT_SUPPORTED,
    })
  })

  it('should compile grouping(column)', () => {
    const query = db
      .selectFrom('person')
      .select((eb) => [
        'gender',
        eb.fn.grouping('gender').as('gender_grouping'),
      ])
      .groupByCube('gender')

    testSql(query, 'postgres', {
      postgres: {
        sql: 'select "gender", grouping("gender") as "gender_grouping" from "person" group by cube("gender")',
        parameters: [],
      },
      mysql: NOT_SUPPORTED,
      mssql: NOT_SUPPORTED,
      sqlite: NOT_SUPPORTED,
    })
  })
})

describe('over-clause extent support', () => {
  const db = createDb()

  it('should compile single-bound rows/range/groups extents', () => {
    const query = db.selectFrom('person').select((eb) => [
      eb.fn
        .sum<number>('id')
        .over((ob) =>
          ob.orderBy('first_name').rows((rb) => rb.unboundedPreceding()),
        )
        .as('rows_sum'),
      eb.fn
        .sum<number>('id')
        .over((ob) => ob.orderBy('first_name').range((rb) => rb.currentRow()))
        .as('range_sum'),
      eb.fn
        .sum<number>('id')
        .over((ob) =>
          ob.orderBy('first_name').groups((gb) => gb.unboundedFollowing()),
        )
        .as('groups_sum'),
    ])

    testSql(query, 'postgres', {
      postgres: {
        sql: [
          'select sum("id") over(order by "first_name" rows unbounded preceding) as "rows_sum",',
          'sum("id") over(order by "first_name" range current row) as "range_sum",',
          'sum("id") over(order by "first_name" groups unbounded following) as "groups_sum"',
          'from "person"',
        ],
        parameters: [],
      },
      mysql: NOT_SUPPORTED,
      mssql: NOT_SUPPORTED,
      sqlite: NOT_SUPPORTED,
    })
  })

  it('should parameterize numeric offsets and inline expression offsets', () => {
    const query = db.selectFrom('person').select((eb) => [
      eb.fn
        .sum<number>('id')
        .over((ob) =>
          ob
            .orderBy('id')
            .rows((rb) => rb.betweenPreceding(1).andFollowing(2n)),
        )
        .as('param_sum'),
      eb.fn
        .sum<number>('id')
        .over((ob) => ob.orderBy('id').range((rb) => rb.preceding(sql.lit(3))))
        .as('lit_sum'),
    ])

    testSql(query, 'postgres', {
      postgres: {
        sql: [
          'select sum("id") over(order by "id" rows between $1 preceding and $2 following) as "param_sum",',
          'sum("id") over(order by "id" range 3 preceding) as "lit_sum"',
          'from "person"',
        ],
        parameters: [1, 2n],
      },
      mysql: NOT_SUPPORTED,
      mssql: NOT_SUPPORTED,
      sqlite: NOT_SUPPORTED,
    })
  })

  it('should compile between starters, and-completers, and exclusions', () => {
    const query = db.selectFrom('person').select((eb) => [
      eb.fn
        .avg<number>('id')
        .over((ob) =>
          ob
            .orderBy('first_name')
            .rows((rb) =>
              rb.betweenUnboundedPreceding().andCurrentRow().excludeTies(),
            ),
        )
        .as('avg_id'),
      eb.fn
        .avg<number>('id')
        .over((ob) =>
          ob
            .orderBy('first_name')
            .range((rb) =>
              rb.betweenCurrentRow().andUnboundedFollowing().excludeGroup(),
            ),
        )
        .as('avg_id_2'),
      eb.fn
        .avg<number>('id')
        .over((ob) =>
          ob
            .orderBy('first_name')
            .groups((gb) =>
              gb.betweenFollowing(1).andFollowing(2).excludeCurrentRow(),
            ),
        )
        .as('avg_id_3'),
      eb.fn
        .avg<number>('id')
        .over((ob) =>
          ob
            .orderBy('first_name')
            .rows((rb) =>
              rb.betweenPreceding(1).andPreceding(0).excludeNoOthers(),
            ),
        )
        .as('avg_id_4'),
    ])

    testSql(query, 'postgres', {
      postgres: {
        sql: [
          'select avg("id") over(order by "first_name" rows between unbounded preceding and current row exclude ties) as "avg_id",',
          'avg("id") over(order by "first_name" range between current row and unbounded following exclude group) as "avg_id_2",',
          'avg("id") over(order by "first_name" groups between $1 following and $2 following exclude current row) as "avg_id_3",',
          'avg("id") over(order by "first_name" rows between $3 preceding and $4 preceding exclude no others) as "avg_id_4"',
          'from "person"',
        ],
        parameters: [1, 2, 1, 0],
      },
      mysql: NOT_SUPPORTED,
      mssql: NOT_SUPPORTED,
      sqlite: NOT_SUPPORTED,
    })
  })

  it('should compile ranking and value accessors with ignore/respect nulls', () => {
    const query = db.selectFrom('person').select((eb) => [
      eb.fn
        .rowNumber()
        .over((ob) => ob.orderBy('id'))
        .as('row_number'),
      eb.fn
        .rank()
        .over((ob) => ob.orderBy('id'))
        .as('rank'),
      eb.fn
        .denseRank()
        .over((ob) => ob.orderBy('id'))
        .as('dense_rank'),
      eb.fn
        .percentRank()
        .over((ob) => ob.orderBy('id'))
        .as('percent_rank'),
      eb.fn
        .cumeDist()
        .over((ob) => ob.orderBy('id'))
        .as('cume_dist'),
      eb.fn
        .ntile(4)
        .over((ob) => ob.orderBy('id'))
        .as('ntile'),
      eb.fn.firstValue('first_name').ignoreNulls().over().as('first_value'),
      eb.fn.lastValue('first_name').respectNulls().over().as('last_value'),
      eb.fn.nthValue('first_name', 2).over().as('nth_value'),
      eb.fn
        .lag('id', 1, 0)
        .over((ob) => ob.orderBy('id'))
        .as('lag'),
      eb.fn
        .lead('id', 1n)
        .over((ob) => ob.orderBy('id'))
        .as('lead'),
    ])

    testSql(query, 'postgres', {
      postgres: {
        sql: [
          'select row_number() over(order by "id") as "row_number",',
          'rank() over(order by "id") as "rank",',
          'dense_rank() over(order by "id") as "dense_rank",',
          'percent_rank() over(order by "id") as "percent_rank",',
          'cume_dist() over(order by "id") as "cume_dist",',
          'ntile($1) over(order by "id") as "ntile",',
          'first_value("first_name") ignore nulls over() as "first_value",',
          'last_value("first_name") respect nulls over() as "last_value",',
          'nth_value("first_name", $2) over() as "nth_value",',
          'lag("id", $3, $4) over(order by "id") as "lag",',
          'lead("id", $5) over(order by "id") as "lead"',
          'from "person"',
        ],
        parameters: [4, 2, 1, 0, 1n],
      },
      mysql: NOT_SUPPORTED,
      mssql: NOT_SUPPORTED,
      sqlite: NOT_SUPPORTED,
    })
  })
})

describe('SimplifyFramePlugin', () => {
  const db = createDb([new SimplifyFramePlugin()])

  it('should strip the default ordered range extent', () => {
    const query = db.selectFrom('person').select((eb) =>
      eb.fn
        .sum<number>('id')
        .over((ob) =>
          ob
            .orderBy('first_name')
            .range((rb) => rb.betweenUnboundedPreceding().andCurrentRow()),
        )
        .as('running_sum'),
    )

    testSql(query, 'postgres', {
      postgres: {
        sql: 'select sum("id") over(order by "first_name") as "running_sum" from "person"',
        parameters: [],
      },
      mysql: NOT_SUPPORTED,
      mssql: NOT_SUPPORTED,
      sqlite: NOT_SUPPORTED,
    })
  })

  it('should strip the default unordered range extent', () => {
    const query = db.selectFrom('person').select((eb) =>
      eb.fn
        .sum<number>('id')
        .over((ob) =>
          ob.range((rb) =>
            rb.betweenUnboundedPreceding().andUnboundedFollowing(),
          ),
        )
        .as('total_sum'),
    )

    testSql(query, 'postgres', {
      postgres: {
        sql: 'select sum("id") over() as "total_sum" from "person"',
        parameters: [],
      },
      mysql: NOT_SUPPORTED,
      mssql: NOT_SUPPORTED,
      sqlite: NOT_SUPPORTED,
    })
  })

  it('should strip a single-bound unbounded preceding extent when order by is present', () => {
    const query = db.selectFrom('person').select((eb) =>
      eb.fn
        .sum<number>('id')
        .over((ob) =>
          ob.orderBy('first_name').range((rb) => rb.unboundedPreceding()),
        )
        .as('running_sum'),
    )

    testSql(query, 'postgres', {
      postgres: {
        sql: 'select sum("id") over(order by "first_name") as "running_sum" from "person"',
        parameters: [],
      },
      mysql: NOT_SUPPORTED,
      mssql: NOT_SUPPORTED,
      sqlite: NOT_SUPPORTED,
    })
  })

  it('should preserve rows, groups, exclusions, and non-default bounds', () => {
    const query = db.selectFrom('person').select((eb) => [
      eb.fn
        .sum<number>('id')
        .over((ob) =>
          ob
            .orderBy('id')
            .rows((rb) => rb.betweenUnboundedPreceding().andCurrentRow()),
        )
        .as('rows_sum'),
      eb.fn
        .sum<number>('id')
        .over((ob) =>
          ob
            .orderBy('id')
            .groups((gb) => gb.betweenUnboundedPreceding().andCurrentRow()),
        )
        .as('groups_sum'),
      eb.fn
        .sum<number>('id')
        .over((ob) =>
          ob
            .orderBy('id')
            .range((rb) =>
              rb.betweenUnboundedPreceding().andCurrentRow().excludeTies(),
            ),
        )
        .as('exclude_sum'),
      eb.fn
        .sum<number>('id')
        .over((ob) =>
          ob
            .orderBy('id')
            .range((rb) => rb.betweenPreceding(1).andCurrentRow()),
        )
        .as('offset_sum'),
      eb.fn
        .sum<number>('id')
        .over((ob) =>
          ob.range((rb) => rb.betweenUnboundedPreceding().andCurrentRow()),
        )
        .as('unordered_current_sum'),
    ])

    testSql(query, 'postgres', {
      postgres: {
        sql: [
          'select sum("id") over(order by "id" rows between unbounded preceding and current row) as "rows_sum",',
          'sum("id") over(order by "id" groups between unbounded preceding and current row) as "groups_sum",',
          'sum("id") over(order by "id" range between unbounded preceding and current row exclude ties) as "exclude_sum",',
          'sum("id") over(order by "id" range between $1 preceding and current row) as "offset_sum",',
          'sum("id") over(range between unbounded preceding and current row) as "unordered_current_sum"',
          'from "person"',
        ],
        parameters: [1],
      },
      mysql: NOT_SUPPORTED,
      mssql: NOT_SUPPORTED,
      sqlite: NOT_SUPPORTED,
    })
  })
})
