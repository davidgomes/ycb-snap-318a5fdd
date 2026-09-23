.. _incremental_delivery:

Incremental delivery (@defer and @stream)
=========================================

With the :code:`@defer` and :code:`@stream` directives, a server can send the
critical data first and send the deferred fragments or the streamed list items
later, in subsequent payloads.

To receive those payloads, use the :code:`execute_incremental` async generator
of an async session. It yields an :class:`IncrementalResult <gql.incremental.IncrementalResult>`
for each payload received, with the following attributes:

- :code:`data`: the data accumulated from all the payloads received so far
- :code:`errors`: the errors received in this payload
- :code:`extensions`: the extensions received in this payload
- :code:`has_next`: :code:`True` if more payloads will follow

.. code-block:: python

    query = gql("""
        query {
          hero {
            id
            ... @defer {
              name
            }
            friends @stream(initialCount: 1) {
              name
            }
          }
        }
    """)

    async with Client(transport=transport) as session:
        async for result in session.execute_incremental(query):
            print(result.data, result.has_next)

Incremental delivery is supported by the
:ref:`AIOHTTPTransport <aiohttp_transport>` (using a
:code:`multipart/mixed;boundary=graphql;deferSpec=20220824` response) and by the
websockets transports. With other transports, or if the server returns a
standard response, a single result is yielded.

Errors are not raised by :code:`execute_incremental`, they are available in the
:code:`errors` attribute of the results.

.. note::
    If you provide a schema to the client, it has to define the :code:`@defer`
    and :code:`@stream` directives for the query to be validated.

Using the DSL
-------------

.. code-block:: python

    ds = DSLSchema(schema)

    name_fragment = DSLFragment("NameFragment").on(ds.Character).select(
        ds.Character.name
    )

    query = dsl_gql(
        DSLQuery(
            ds.Query.hero.select(
                ds.Character.id,
                name_fragment.defer(label="name"),
                ds.Character.friends.stream(initial_count=1).select(
                    ds.Character.name
                ),
            )
        ),
        name_fragment,
    )

:code:`defer` is available on :class:`DSLFragment <gql.dsl.DSLFragment>` (for all
the usages of the fragment) and on :class:`DSLFragmentSpread <gql.dsl.DSLFragmentSpread>`
(for a single usage). :code:`stream` is available on list fields.
