# Plugin system

Plugins are classes that implement [KyselyPlugin](https://kysely-org.github.io/kysely-apidoc/interfaces/KyselyPlugin.html). Plugins are then added to the `Kysely` instance as follows:

```ts
const db = new Kysely<Database>({
  dialect: new PostgresDialect({
    database: 'kysely_test',
    host: 'localhost',
  }),
  plugins: [new CamelCasePlugin()],
})
```

## Built-in plugins

### Camel case plugin

A plugin that converts snake_case identifiers in the database into camelCase in the JavaScript side. [Learn more](https://kysely-org.github.io/kysely-apidoc/classes/CamelCasePlugin.html).

### Deduplicate joins plugin

A plugin that removes duplicate joins from queries. You can read more about it in the [examples](/docs/recipes/deduplicate-joins) section or check the [API docs](https://kysely-org.github.io/kysely-apidoc/classes/DeduplicateJoinsPlugin.html).

### Simplify frame plugin

A plugin that removes window-frame extents that only repeat the SQL-standard default. With `order by`, that default is `range between unbounded preceding and current row`. Without `order by`, it is `range between unbounded preceding and unbounded following`. Frames that use `rows` or `groups`, an `exclude` clause, a non-default bound, or an expression offset are kept.

### Handle `in ()` and `not in ()` plugin

A plugin that allows handling `in ()` and `not in ()` with a chosen strategy. [Learn more](https://kysely-org.github.io/kysely-apidoc/classes/HandleEmptyInListsPlugin.html).
