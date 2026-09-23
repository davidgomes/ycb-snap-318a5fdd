import {
  DummyDriver,
  Kysely,
  type KyselyPlugin,
  MssqlAdapter,
  MssqlIntrospector,
  MssqlQueryCompiler,
  MysqlAdapter,
  MysqlIntrospector,
  MysqlQueryCompiler,
  PostgresAdapter,
  PostgresIntrospector,
  PostgresQueryCompiler,
  SqliteAdapter,
  SqliteIntrospector,
  SqliteQueryCompiler,
} from '../../../'
import {
  type BuiltInDialect,
  type Database,
  type PerDialect,
} from './test-setup.js'

export function createDummyDb(
  dialect: BuiltInDialect,
  plugins: KyselyPlugin[] = [],
): Kysely<Database> {
  const compilers = {
    postgres: {
      createAdapter: () => new PostgresAdapter(),
      createIntrospector: (db: Kysely<any>) => new PostgresIntrospector(db),
      createQueryCompiler: () => new PostgresQueryCompiler(),
    },
    mysql: {
      createAdapter: () => new MysqlAdapter(),
      createIntrospector: (db: Kysely<any>) => new MysqlIntrospector(db),
      createQueryCompiler: () => new MysqlQueryCompiler(),
    },
    mssql: {
      createAdapter: () => new MssqlAdapter(),
      createIntrospector: (db: Kysely<any>) => new MssqlIntrospector(db),
      createQueryCompiler: () => new MssqlQueryCompiler(),
    },
    sqlite: {
      createAdapter: () => new SqliteAdapter(),
      createIntrospector: (db: Kysely<any>) => new SqliteIntrospector(db),
      createQueryCompiler: () => new SqliteQueryCompiler(),
    },
  }

  return new Kysely<Database>({
    dialect: {
      createDriver: () => new DummyDriver(),
      ...compilers[dialect],
    },
    plugins,
  })
}

/**
 * Build per-dialect expectations from PostgreSQL SQL.
 * Placeholders in `postgresSql` must be `$1`, `$2`, ...
 */
export function sqlExpectation(
  postgresSql: string,
  parameters: readonly unknown[] = [],
): PerDialect<{ sql: string; parameters: any[] }> {
  const params = [...parameters]

  return {
    postgres: { sql: postgresSql, parameters: params },
    mysql: {
      sql: postgresSql.replaceAll('"', '`').replace(/\$\d+/g, '?'),
      parameters: params,
    },
    sqlite: {
      sql: postgresSql.replace(/\$\d+/g, '?'),
      parameters: params,
    },
    mssql: {
      sql: postgresSql.replace(/\$(\d+)/g, (_, index: string) => `@${index}`),
      parameters: params,
    },
  }
}
