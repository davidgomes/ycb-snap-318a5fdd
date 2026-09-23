.. _incremental_delivery:

Incremental delivery (@defer and @stream)
=========================================

The :code:`@defer` and :code:`@stream` directives allow a server to send the
critical data of a query first, and to send the deferred fragments or the
remaining items of a list later, in subsequent payloads.

.. warning::
    Incremental delivery is not yet part of the GraphQL specification and
    requires a backend supporting it. gql supports the response format
    of the :code:`deferSpec=20220824` version of the proposal.

Use the :meth:`execute_incremental <gql.client.AsyncClientSession.execute_incremental>`
async generator of an async session to receive a result for each payload:

.. code-block:: python

    query = gql("""
        query {
          hero {
            id
            ... @defer(label: "name") { name }
            friends @stream(initialCount: 1) { name }
          }
        }
    """)

    async with Client(transport=transport) as session:

        async for result in session.execute_incremental(query):
            print(result.data, result.errors, result.has_next)

Each yielded :class:`IncrementalExecutionResult <gql.incremental.IncrementalExecutionResult>`
contains:

- :code:`data`: the data of the initial payload in which all the deferred
  fragments and streamed items received so far have been merged
- :code:`errors`: the errors of this payload, including the errors of its
  incremental items. GraphQL errors are not raised.
- :code:`extensions`: the extensions of this payload
- :code:`has_next`: :code:`True` if more payloads are expected
- :code:`incremental`: the raw incremental items of this payload

If the server does not support incremental delivery and returns a single
response, a single result is yielded with :code:`has_next` set to :code:`False`.

Incremental delivery is supported by the following transports:

- :ref:`AIOHTTPTransport <aiohttp_transport>`, using the multipart HTTP protocol
- :ref:`WebsocketsTransport <websockets_transport>` and
  :ref:`AIOHTTPWebsocketsTransport <aiohttp_websockets_transport>`,
  with both the :code:`graphql-transport-ws` and apollo :code:`graphql-ws` protocols

If a schema is provided to the client, the request is validated with the
:code:`@defer` and :code:`@stream` directives added to the schema if they are
not already defined.

Using the :doc:`DSL module <dsl_module>`, you can add these directives with the
:meth:`defer <gql.dsl.DSLFragmentSpread.defer>` and
:meth:`stream <gql.dsl.DSLField.stream>` methods.
