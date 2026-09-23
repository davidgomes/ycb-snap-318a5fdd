.. testsetup:: *

   from pwn import *
   from pwnlib.tubes.mux import *

:mod:`pwnlib.tubes.mux` --- Multiplexing channels over a tube
=============================================================

.. automodule:: pwnlib.tubes.mux

  .. autoclass:: pwnlib.tubes.mux.TubeMultiplexer
     :members:

  .. autoclass:: pwnlib.tubes.mux.MuxChannel
     :members: channel_id, stats, close
     :show-inheritance:
